package runstream

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	p "github.com/korallis/letmecook/schemas/execution"
)

func uuid(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func identityFor(t *testing.T) p.Identity {
	t.Helper()
	return p.Identity{Generation: uuid(t), TaskID: uuid(t), AttemptID: uuid(t), Epoch: 3}
}

func pathFor(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name)
}

func chunk(n int) (Native, Normalized) {
	text := fmt.Sprintf("line %d\n", n)
	return Native{Version: "codex-jsonl-v1", Kind: "item.completed", Data: []byte(`{"text":"` + text + `"}`)},
		Normalized{Stream: "stdout", Text: text}
}

func appendN(t *testing.T, s *Spool, from, to int) []Record {
	t.Helper()
	var out []Record
	for i := from; i <= to; i++ {
		r, err := s.Append(chunk(i))
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		out = append(out, r)
	}
	return out
}

// A reconnect resends only unacknowledged records and the receiver keeps one
// logical record per sequence, including for records it already durably held.
func TestReconnectResumesWithoutDuplicates(t *testing.T) {
	id := identityFor(t)
	spoolPath, sinkPath := pathFor(t, "spool"), pathFor(t, "sink")
	spool, err := CreateSpool(spoolPath, id, MinLimit)
	if err != nil {
		t.Fatal(err)
	}
	sink, err := CreateSink(sinkPath, id, MinLimit)
	if err != nil {
		t.Fatal(err)
	}
	sent := appendN(t, spool, 1, 5)
	for _, r := range sent[:3] {
		ack, err := sink.Receive(r)
		if err != nil {
			t.Fatal(err)
		}
		if ack.Through != r.Sequence || ack.Duplicate || ack.Bytes != r.Offset+int64(len(r.Native.Data)) {
			t.Fatalf("unexpected ack %+v", ack)
		}
		if err = spool.Acknowledge(ack.Through); err != nil {
			t.Fatal(err)
		}
	}
	// Connection loss: both sides reopen from disk with no in-memory carryover.
	spool.Close()
	sink.Close()
	spool, err = OpenSpool(spoolPath, id)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	sink, err = OpenSink(sinkPath, id)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if spool.Acknowledged() != 3 || sink.Expected() != 4 {
		t.Fatalf("watermarks lost: spool %d sink %d", spool.Acknowledged(), sink.Expected())
	}
	pending := spool.Pending()
	if len(pending) != 2 || pending[0].Sequence != 4 {
		t.Fatalf("unexpected resend set %d", len(pending))
	}
	for _, r := range pending {
		ack, err := sink.Receive(r)
		if err != nil {
			t.Fatal(err)
		}
		if err = spool.Acknowledge(ack.Through); err != nil {
			t.Fatal(err)
		}
	}
	got := sink.Records()
	if len(got) != 5 {
		t.Fatalf("expected 5 logical records, got %d", len(got))
	}
	for i, r := range got {
		if r.Sequence != int64(i+1) {
			t.Fatalf("out of order at %d: %d", i, r.Sequence)
		}
		// Native bytes survive beside the normalized projection.
		if !bytes.Equal(r.Native.Data, sent[i].Native.Data) || r.Native.Version != "codex-jsonl-v1" {
			t.Fatal("native event not preserved")
		}
		if r.Normalized.Text != sent[i].Normalized.Text {
			t.Fatal("normalized event not preserved")
		}
		if r.Offset != sent[i].Offset {
			t.Fatal("byte offset not preserved")
		}
	}
	if spool.Acknowledged() != 5 {
		t.Fatal("acknowledged watermark did not advance")
	}
}

// Duplicates replay the retained watermark, gaps are reported as gaps, and a
// changed payload for a retained sequence conflicts instead of overwriting.
func TestDuplicateOutOfOrderAndConflict(t *testing.T) {
	id := identityFor(t)
	spool, err := CreateSpool(pathFor(t, "spool"), id, MinLimit)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	sink, err := CreateSink(pathFor(t, "sink"), id, MinLimit)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	sent := appendN(t, spool, 1, 3)
	if _, err = sink.Receive(sent[0]); err != nil {
		t.Fatal(err)
	}
	ack, err := sink.Receive(sent[2])
	if !errors.Is(err, ErrGap) {
		t.Fatalf("out-of-order accepted: %v", err)
	}
	if ack.Expected != 2 || ack.Through != 1 {
		t.Fatalf("gap not exposed: %+v", ack)
	}
	ack, err = sink.Receive(sent[0])
	if err != nil || !ack.Duplicate || ack.Through != 1 {
		t.Fatalf("duplicate not replayed: %+v %v", ack, err)
	}
	if len(sink.Records()) != 1 {
		t.Fatal("duplicate created a second logical record")
	}
	forged := sent[0]
	forged.Normalized.Text = "tampered"
	forged.Digest = digest(forged)
	if _, err = sink.Receive(forged); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed payload accepted: %v", err)
	}
	stale := sent[1]
	stale.Identity.Generation = uuid(t)
	stale.Digest = digest(stale)
	if _, err = sink.Receive(stale); !errors.Is(err, ErrIdentity) {
		t.Fatalf("foreign generation accepted: %v", err)
	}
	broken := sent[1]
	if _, err = sink.Receive(broken); err != nil {
		t.Fatal(err)
	}
	broken.Sequence = 9
	if _, err = sink.Receive(broken); !errors.Is(err, ErrConflict) {
		t.Fatalf("digest mismatch accepted: %v", err)
	}
	if _, err = OpenSpool(pathFor(t, "missing"), id); err == nil {
		t.Fatal("missing spool opened")
	}
}

// Nothing may report remote durability that the receiver never acknowledged.
func TestUnacknowledgedWorkIsNeverDurableRemotely(t *testing.T) {
	id := identityFor(t)
	path := pathFor(t, "spool")
	spool, err := CreateSpool(path, id, MinLimit)
	if err != nil {
		t.Fatal(err)
	}
	appendN(t, spool, 1, 4)
	if err = spool.Acknowledge(5); !errors.Is(err, ErrConflict) {
		t.Fatalf("watermark beyond appended work accepted: %v", err)
	}
	if err = spool.Acknowledge(2); err != nil {
		t.Fatal(err)
	}
	// Monotonic and idempotent: a stale or repeated ack never rewinds custody.
	if err = spool.Acknowledge(1); err != nil || spool.Acknowledged() != 2 {
		t.Fatalf("stale ack changed watermark: %v", err)
	}
	if err = spool.Acknowledge(2); err != nil || spool.Acknowledged() != 2 {
		t.Fatalf("repeated ack not idempotent: %v", err)
	}
	stats := spool.Stats()
	if stats.Appended != 4 || stats.Acknowledged != 2 || stats.NativeBytes == 0 {
		t.Fatalf("stats conflate local and remote durability: %+v", stats)
	}
	if len(spool.Pending()) != 2 {
		t.Fatal("resend set does not cover unacknowledged work")
	}
	// Losing directory ownership poisons the handle; no positive return follows.
	if err = os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = spool.Append(chunk(5)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsafe storage acknowledged: %v", err)
	}
	if err = spool.Acknowledge(4); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("poisoned handle reused: %v", err)
	}
	os.Chmod(path, 0o700)
	spool.Close()
	reopened, err := OpenSpool(path, id)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Acknowledged() != 2 || len(reopened.Pending()) != 2 {
		t.Fatal("recoverable work lost after poisoning")
	}
	// The exclusive owner is released before identity and role refusals are
	// exercised, so neither result can be explained by the directory lock.
	reopened.Close()
	if _, err = OpenSpool(path, identityFor(t)); !errors.Is(err, ErrIdentity) {
		t.Fatalf("foreign identity adopted existing spool: %v", err)
	}
	if _, err = OpenSink(path, id); err == nil {
		t.Fatal("spool opened as sink")
	}
}

// A full spool refuses new output, keeps unacknowledged work recoverable and
// still records acknowledgement progress.
func TestSpoolExhaustionPreservesRecoverableWork(t *testing.T) {
	id := identityFor(t)
	path := pathFor(t, "spool")
	limit := 4 * MinLimit
	spool, err := CreateSpool(path, id, limit)
	if err != nil {
		t.Fatal(err)
	}
	big := Native{Version: "codex-jsonl-v1", Kind: "item.delta", Data: bytes.Repeat([]byte("x"), MaxNative/2)}
	normalized := Normalized{Stream: "stderr", Text: "delta"}
	accepted := 0
	for {
		if _, err = spool.Append(big, normalized); err != nil {
			break
		}
		accepted++
		if accepted > 200 {
			t.Fatal("limit never enforced")
		}
	}
	if !errors.Is(err, ErrSpoolFull) || accepted == 0 {
		t.Fatalf("expected bounded spool, got %v after %d", err, accepted)
	}
	stats := spool.Stats()
	if stats.UsedBytes > stats.LimitBytes || stats.LimitBytes != limit {
		t.Fatalf("limit exceeded: %+v", stats)
	}
	if err = spool.Acknowledge(int64(accepted)); err != nil {
		t.Fatalf("acknowledgement blocked on full spool: %v", err)
	}
	if _, err = spool.Append(big, normalized); !errors.Is(err, ErrSpoolFull) {
		t.Fatal("full spool silently resumed after acknowledgement")
	}
	spool.Close()
	reopened, err := OpenSpool(path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Acknowledged() != int64(accepted) || len(reopened.Records()) != accepted {
		t.Fatal("full spool lost retained work")
	}
	if _, err = CreateSpool(pathFor(t, "small"), id, MinLimit-1); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded or undersized limit accepted")
	}
	oversized := Native{Version: "codex-jsonl-v1", Kind: "item.delta", Data: bytes.Repeat([]byte("x"), MaxNative+1)}
	if _, err = reopened.Append(oversized, normalized); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized chunk accepted")
	}
}

func TestSinkCrashHelper(t *testing.T) {
	path := os.Getenv("GAFFER_RUNSTREAM_CRASH_PATH")
	identity := os.Getenv("GAFFER_RUNSTREAM_CRASH_IDENTITY")
	if path == "" || identity == "" {
		return
	}
	id := p.Identity{Generation: identity, TaskID: identity, AttemptID: identity, Epoch: 1}
	sink, err := CreateSink(path, id, MinLimit)
	if err != nil {
		os.Exit(3)
	}
	spool, err := CreateSpool(path+"-sender", id, MinLimit)
	if err != nil {
		os.Exit(4)
	}
	for i := 1; i <= 2; i++ {
		r, err := spool.Append(chunk(i))
		if err != nil {
			os.Exit(5)
		}
		if _, err = sink.Receive(r); err != nil {
			os.Exit(6)
		}
	}
	// SIGKILL after the second acknowledged write and before any close.
	self, _ := os.FindProcess(os.Getpid())
	if self.Kill() != nil {
		os.Exit(99)
	}
	for {
		time.Sleep(time.Second)
	}
}

// A killed receiver reopens at exactly its durable watermark, and the sender's
// replay of the chunk whose ack was lost is deduplicated rather than doubled.
func TestReceiverCrashRecoversDurableWatermark(t *testing.T) {
	path := pathFor(t, "sink")
	identity := uuid(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestSinkCrashHelper$")
	cmd.Env = append(os.Environ(),
		"GAFFER_RUNSTREAM_CRASH_PATH="+path,
		"GAFFER_RUNSTREAM_CRASH_IDENTITY="+identity)
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != -1 {
		t.Fatalf("helper did not crash: %v", err)
	}
	id := p.Identity{Generation: identity, TaskID: identity, AttemptID: identity, Epoch: 1}
	sink, err := OpenSink(path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if sink.Acknowledged() != 2 || sink.Expected() != 3 {
		t.Fatalf("durable watermark lost: %d", sink.Acknowledged())
	}
	spool, err := OpenSpool(path+"-sender", id)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	// The sender never learned of the second acknowledgement and resends both.
	pending := spool.Pending()
	if len(pending) != 2 {
		t.Fatalf("unexpected resend set %d", len(pending))
	}
	for _, r := range pending {
		ack, err := sink.Receive(r)
		if err != nil || !ack.Duplicate {
			t.Fatalf("replay after crash not deduplicated: %+v %v", ack, err)
		}
	}
	if len(sink.Records()) != 2 {
		t.Fatal("crash replay duplicated logical records")
	}
}
