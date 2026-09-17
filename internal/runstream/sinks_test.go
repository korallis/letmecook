package runstream

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	p "github.com/korallis/letmecook/schemas/execution"
)

func sinkIdentityFixture() p.Identity {
	return p.Identity{Generation: "11111111-1111-4111-8111-111111111111", TaskID: "22222222-2222-4222-8222-222222222222", AttemptID: "33333333-3333-4333-8333-333333333333", Epoch: 1}
}

// spooled builds records the way a runner would: through a real spool so the
// sequences, offsets and digests are the sender's, not hand-written.
func spooled(t *testing.T, identity p.Identity, texts ...string) []Record {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spool, err := CreateSpool(filepath.Join(base, "spool"), identity, MinLimit)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	out := []Record{}
	for _, text := range texts {
		r, err := spool.Append(Native{Version: "fixture", Kind: "text", Data: []byte(text)}, Normalized{Stream: "stdout", Text: text})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestSinksReceiveGapConflictDuplicateAndWindow(t *testing.T) {
	identity := sinkIdentityFixture()
	dir := filepath.Join(t.TempDir(), "streams")
	k := NewSinks(dir)
	defer k.Close()
	records := spooled(t, identity, "one", "two", "three", "four")
	if _, err := k.Watermark(identity.AttemptID); err != nil {
		t.Fatal("missing sink is an empty stream, not an error", err)
	}
	w, _ := k.Watermark(identity.AttemptID)
	if w.Through != 0 || w.Expected != 1 || w.Digest != StreamDigest(nil) {
		t.Fatal(w)
	}
	if _, err := os.Stat(k.Path(identity.AttemptID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("watermark read created a sink")
	}
	// A gap before the first record is refused with the expected sequence and
	// creates no durable record.
	ack, err := k.Receive(identity.AttemptID, identity, records[1:2])
	if !errors.Is(err, ErrGap) || ack.Expected != 1 || ack.Through != 0 {
		t.Fatal("gap", ack, err)
	}
	ack, err = k.Receive(identity.AttemptID, identity, records[:2])
	if err != nil || ack.Through != 2 || ack.Expected != 3 || ack.Bytes != 6 || ack.Duplicate {
		t.Fatal(ack, err)
	}
	// A retained sequence with changed bytes conflicts; the retained record wins.
	changed := records[1]
	changed.Native.Data = []byte("TWO")
	changed.Digest = digest(changed)
	ack, err = k.Receive(identity.AttemptID, identity, []Record{changed})
	if !errors.Is(err, ErrConflict) || ack.Through != 2 {
		t.Fatal("conflict", ack, err)
	}
	// Duplicates replay the retained acknowledgement.
	ack, err = k.Receive(identity.AttemptID, identity, records[:2])
	if err != nil || !ack.Duplicate || ack.Through != 2 {
		t.Fatal("duplicate", ack, err)
	}
	// A batch that overlaps then extends is accepted; Duplicate is only whole-batch.
	ack, err = k.Receive(identity.AttemptID, identity, records[1:4])
	if err != nil || ack.Duplicate || ack.Through != 4 || ack.Expected != 5 {
		t.Fatal(ack, err)
	}
	// Non-contiguous or foreign batches are refused before any append.
	other := identity
	other.AttemptID = "44444444-4444-4444-8444-444444444444"
	for name, batch := range map[string][]Record{"empty": {}, "skip": {records[0], records[2]}, "foreign": spooled(t, other, "x")} {
		if _, err := k.Receive(identity.AttemptID, identity, batch); !errors.Is(err, ErrBatch) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	if _, err := k.Receive(identity.AttemptID, other, spooled(t, other, "x")); !errors.Is(err, ErrBatch) {
		t.Fatal("attempt/identity mismatch accepted")
	}
	got, ack, err := k.Window(identity.AttemptID, 1, 2)
	if err != nil || len(got) != 2 || got[0].Sequence != 2 || got[1].Sequence != 3 || ack.Through != 4 {
		t.Fatal(got, ack, err)
	}
	got, _, err = k.Window(identity.AttemptID, 4, 10)
	if err != nil || len(got) != 0 {
		t.Fatal("window past end", got, err)
	}
	if _, _, err := k.Window(identity.AttemptID, 0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded window")
	}
	got, ack, err = k.Window(other.AttemptID, 0, 10)
	if err != nil || len(got) != 0 || ack.Expected != 1 {
		t.Fatal("missing sink window", got, ack, err)
	}
	w, err = k.Watermark(identity.AttemptID)
	if err != nil || w.Through != 4 || w.Digest != StreamDigest(records) {
		t.Fatal(w, err)
	}
	d, err := k.Digest(identity.AttemptID, 2)
	if err != nil || d != StreamDigest(records[:2]) {
		t.Fatal(d, err)
	}
	if _, err := k.Digest(identity.AttemptID, 5); !errors.Is(err, ErrGap) {
		t.Fatal("digest beyond durable watermark attested")
	}
	if err := k.Close(); err != nil {
		t.Fatal(err)
	}
	// A reopened set replays the durable sink and refuses a different identity.
	reopened := NewSinks(dir)
	defer reopened.Close()
	w, err = reopened.Watermark(identity.AttemptID)
	if err != nil || w.Through != 4 || w.Digest != StreamDigest(records) {
		t.Fatal("reopen lost watermark", w, err)
	}
	if _, err := reopened.Receive(identity.AttemptID, other, []Record{}); !errors.Is(err, ErrBatch) {
		t.Fatal(err)
	}
	foreign := spooled(t, other, "x")
	foreign[0].Identity = identity
	foreign[0].Digest = digest(foreign[0])
	// A retained sequence with foreign bytes conflicts with the durable record.
	if _, err := reopened.Receive(identity.AttemptID, identity, []Record{foreign[0]}); !errors.Is(err, ErrConflict) {
		t.Fatal("reopened sink accepted a rewritten record", err)
	}
}

func TestStreamDigestChainsRecordDigests(t *testing.T) {
	identity := sinkIdentityFixture()
	records := spooled(t, identity, "a", "b")
	if StreamDigest(nil) != StreamDigest([]Record{}) || StreamDigest(records[:1]) == StreamDigest(records) || StreamDigest(records) == StreamDigest(nil) {
		t.Fatal("chain digest is not a function of the record sequence")
	}
	swapped := []Record{records[1], records[0]}
	if StreamDigest(swapped) == StreamDigest(records) {
		t.Fatal("order-insensitive digest")
	}
}

// TestSinksOwnedProcess is the SIGKILLed receiver: it acknowledges one record
// and blocks; the parent kills it and proves the record survived the ack.
func TestSinksOwnedProcess(t *testing.T) {
	dir := os.Getenv("GAFFER_SINK_TEST_DIR")
	if dir == "" {
		return
	}
	var records []Record
	if err := json.Unmarshal([]byte(os.Getenv("GAFFER_SINK_TEST_RECORDS")), &records); err != nil {
		t.Fatal(err)
	}
	k := NewSinks(dir)
	ack, err := k.Receive(records[0].Identity.AttemptID, records[0].Identity, records)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("acked %d\n", ack.Through)
	bufio.NewReader(os.Stdin).ReadByte()
	t.Fatal("crash test escaped barrier")
}

func TestSinksSIGKILLAfterAckKeepsRecord(t *testing.T) {
	identity := sinkIdentityFixture()
	records := spooled(t, identity, "durable before ack")
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "streams")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSinksOwnedProcess$")
	cmd.Env = append(os.Environ(), "GAFFER_SINK_TEST_DIR="+dir, "GAFFER_SINK_TEST_RECORDS="+string(raw))
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "acked 1" {
		lines := []string{scanner.Text()}
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		t.Fatalf("child: %q (%v)", strings.Join(lines, "|"), scanner.Err())
	}
	// The acknowledgement has been sent; the receiver dies now.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed child exited successfully")
	}
	k := NewSinks(dir)
	defer k.Close()
	got, ack, err := k.Window(identity.AttemptID, 0, 10)
	if err != nil || len(got) != 1 || got[0].Digest != records[0].Digest || ack.Through != 1 {
		t.Fatal("acknowledged record lost after SIGKILL", got, ack, err)
	}
	w, err := k.Watermark(identity.AttemptID)
	if err != nil || w.Through != 1 || w.Digest != StreamDigest(records) {
		t.Fatal(w, err)
	}
	// The lost acknowledgement is replayed, never re-appended.
	ack, err = k.Receive(identity.AttemptID, identity, records)
	if err != nil || !ack.Duplicate || ack.Through != 1 {
		t.Fatal(ack, err)
	}
}
