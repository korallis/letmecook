package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	r "github.com/korallis/letmecook/internal/repositories"
	"github.com/korallis/letmecook/internal/runstream"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

var ctx = context.Background()

type fixture struct {
	s                               *store.Store
	root, state, artifacts, streams string
	db                              *sql.DB
	identity                        p.Identity
	request                         store.CustodyRequest
	assignment                      p.Message
	runner, dispatch                string
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func execute(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	_, err := db.Exec(q, args...)
	must(t, err)
}
func rawJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	must(t, err)
	return b
}
func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func testFixture(t *testing.T, terminal bool) fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	f := fixture{root: root, state: filepath.Join(root, "state"), artifacts: filepath.Join(root, "artifacts"), streams: filepath.Join(root, "state", "streams")}
	f.s, err = store.Open(ctx, f.state, f.artifacts)
	must(t, err)
	t.Cleanup(func() { f.s.Close() })
	f.db, err = openDatabase(filepath.Join(f.state, "state.db"), false)
	must(t, err)
	t.Cleanup(func() { f.db.Close() })
	status, err := f.s.Status(ctx)
	must(t, err)
	f.identity = p.Identity{Generation: status.Generation, TaskID: uuid(), AttemptID: uuid(), Epoch: 1}
	state := "running"
	if terminal {
		state = "succeeded"
	}
	execute(t, f.db, "INSERT INTO tasks VALUES(?,'awaiting_review')", f.identity.TaskID)
	execute(t, f.db, "INSERT INTO attempts VALUES(?,?,?,?,?,?)", f.identity.AttemptID, f.identity.TaskID, 1, state, 1, uuid())
	execute(t, f.db, "UPDATE daemon_state SET paused=1,reason='backup-test',updated_ms=?", time.Now().UnixMilli())
	body := []byte("immutable candidate\n")
	source := filepath.Join(root, "runner-recovery.txt")
	must(t, os.WriteFile(source, body, 0600))
	manifest := store.CandidateManifest{Version: "gaffer-artifact-manifest-v1", Identity: f.identity, Base: store.ArtifactBase{Revision: strings.Repeat("a", 40), SHA256: strings.Repeat("b", 64)}, Outcome: "succeeded", Tracked: []store.ArtifactBlob{{Path: "hello.txt", SHA256: sum(body), Bytes: int64(len(body))}}, Untracked: []store.ArtifactBlob{}, Binary: []store.ArtifactBlob{}, Recovery: []store.ArtifactBlob{}, Deleted: []string{}}
	raw := rawJSON(t, manifest)
	f.request = store.CustodyRequest{Result: p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "result", Identity: f.identity, Manifest: &p.Manifest{ManifestID: uuid(), SHA256: sum(raw), Bytes: int64(len(raw))}}, Manifest: raw, Sources: []store.ArtifactSource{{Path: "hello.txt", File: source}}, RetainUntilMS: time.Now().Add(time.Hour).UnixMilli()}
	must(t, os.MkdirAll(f.streams, 0700))
	sink, err := runstream.CreateSink(filepath.Join(f.streams, f.identity.AttemptID+".sink"), f.identity, runstream.MinLimit)
	must(t, err)
	spool, err := runstream.CreateSpool(filepath.Join(root, "spool"), f.identity, runstream.MinLimit)
	must(t, err)
	record, err := spool.Append(runstream.Native{Version: "test-v1", Kind: "output", Data: []byte("hello\n")}, runstream.Normalized{Stream: "stdout", Text: "hello\n"})
	must(t, err)
	ack, err := sink.Receive(record)
	must(t, err)
	if ack.Through != 1 {
		t.Fatal(ack)
	}
	evidence := rawJSON(t, store.RuntimeEvidence{Kind: "exit", StreamThrough: 1})
	execute(t, f.db, "INSERT INTO runtime_observations VALUES(?,?,?,?,?,?,?)", f.identity.AttemptID, sum(evidence), "exit", uuid(), status.DaemonBoot, string(evidence), time.Now().UnixMilli())
	must(t, sink.Close())
	must(t, spool.Close())
	return f
}
func (f fixture) custody(t *testing.T) store.CustodyReceipt {
	t.Helper()
	r, err := f.s.CustodyResult(ctx, f.request)
	must(t, err)
	return r
}
func (f fixture) backup(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(f.root, "backup")
	_, err := Create(ctx, f.s, f.artifacts, f.streams, dir)
	must(t, err)
	return dir
}
func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	must(t, err)
	return b
}

func TestBackupSnapshotIntegrityAndContent(t *testing.T) {
	f := testFixture(t, true)
	f.custody(t)
	// Uncheckpointed WAL writes are present in the consistent VACUUM INTO image.
	execute(t, f.db, "PRAGMA wal_autocheckpoint=0")
	execute(t, f.db, "INSERT INTO tasks VALUES(?,'ready')", uuid())
	out := f.backup(t)
	m, err := Verify(out)
	must(t, err)
	if m.SchemaVersion != 10 || m.Generation != f.identity.Generation || m.AttemptStates[f.identity.AttemptID] != p.Succeeded || len(m.Inventory) != 3 {
		t.Fatalf("manifest: %+v", m)
	}
	db, err := openDatabase(filepath.Join(out, "state.db"), true)
	must(t, err)
	defer db.Close()
	must(t, integrity(ctx, db))
	var count int
	must(t, db.QueryRow("SELECT count(*) FROM tasks").Scan(&count))
	if count != 2 {
		t.Fatal("WAL row omitted", count)
	}
	for _, e := range m.Inventory {
		source := filepath.Join(f.artifacts, strings.TrimPrefix(e.RelativePath, "artifacts/"))
		if e.Kind == "stream" {
			source = filepath.Join(f.state, e.RelativePath)
		}
		if !bytes.Equal(read(t, source), read(t, filepath.Join(out, e.RelativePath))) {
			t.Fatal("copy not byte-identical", e)
		}
	}
	if _, err = Create(ctx, f.s, f.artifacts, f.streams, out); err == nil || err.Error() != "target_not_empty" {
		t.Fatal("overwrote completed backup", err)
	}
}
func TestBackupRefusesUnpausedAndEveryUnresolvedState(t *testing.T) {
	for _, state := range []string{"assigned", "starting", "running", "result_pending", "stopping", "unknown"} {
		t.Run(state, func(t *testing.T) {
			f := testFixture(t, false)
			execute(t, f.db, "UPDATE attempts SET state=?", state)
			out := filepath.Join(f.root, "refused")
			_, err := Create(ctx, f.s, f.artifacts, f.streams, out)
			if err == nil || err.Error() != "active_execution" {
				t.Fatal(err)
			}
			if _, err = os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("active snapshot created", err)
			}
		})
	}
	f := testFixture(t, true)
	execute(t, f.db, "UPDATE daemon_state SET paused=0")
	_, err := Create(ctx, f.s, f.artifacts, f.streams, filepath.Join(f.root, "refused"))
	if err == nil || err.Error() != "not_paused" {
		t.Fatal(err)
	}
}
func TestArtifactPinBlocksCollectionUntilRelease(t *testing.T) {
	f := testFixture(t, true)
	hash := sum([]byte("orphan"))
	file := filepath.Join(f.artifacts, "blobs", hash[:2], hash)
	must(t, os.MkdirAll(filepath.Dir(file), 0700))
	must(t, os.WriteFile(file, []byte("orphan"), 0600))
	execute(t, f.db, "INSERT INTO artifact_blobs VALUES(?,?,?,?,?)", hash, 6, 1, 1, "committed")
	release, err := f.s.PinArtifacts()
	must(t, err)
	defer release()
	done := make(chan error, 1)
	go func() { done <- f.s.CollectArtifacts(ctx, time.Now().UnixMilli()) }()
	select {
	case err := <-done:
		t.Fatal("collection crossed backup pin", err)
	case <-time.After(30 * time.Millisecond):
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("pinned blob removed", err)
	}
	must(t, f.s.SnapshotDatabase(ctx, filepath.Join(f.root, "pinned.db")))
	release()
	release()
	select {
	case err = <-done:
		must(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("collection remained pinned")
	}
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("collection did not resume", err)
	}
}
func TestVerifyRejectsCorruptionMissingAndInconsistentInventory(t *testing.T) {
	for _, mode := range []string{"corrupt-blob", "missing-blob", "omit-blob", "corrupt-db", "wrong-schema", "duplicate-key", "unknown-field", "traversal", "symlink", "extra-file", "missing-sink-journal"} {
		t.Run(mode, func(t *testing.T) {
			f := testFixture(t, true)
			f.custody(t)
			out := f.backup(t)
			m, err := Verify(out)
			must(t, err)
			var blob Entry
			for _, e := range m.Inventory {
				if e.Kind == "blob" {
					blob = e
				}
			}
			switch mode {
			case "corrupt-blob":
				must(t, os.WriteFile(filepath.Join(out, blob.RelativePath), []byte("broken"), 0600))
			case "missing-blob":
				must(t, os.Remove(filepath.Join(out, blob.RelativePath)))
			case "omit-blob":
				for i, e := range m.Inventory {
					if e.Kind == "blob" {
						m.Inventory = append(m.Inventory[:i], m.Inventory[i+1:]...)
						break
					}
				}
				must(t, os.WriteFile(filepath.Join(out, manifestName), rawJSON(t, m), 0600))
			case "corrupt-db":
				must(t, os.WriteFile(filepath.Join(out, "state.db"), []byte("not sqlite"), 0600))
			case "wrong-schema":
				m.SchemaVersion = 11
				must(t, os.WriteFile(filepath.Join(out, manifestName), rawJSON(t, m), 0600))
			case "duplicate-key":
				b := read(t, filepath.Join(out, manifestName))
				b = append([]byte(`{"version":"gaffer-backup-v1",`), b[1:]...)
				must(t, os.WriteFile(filepath.Join(out, manifestName), b, 0600))
			case "unknown-field":
				b := read(t, filepath.Join(out, manifestName))
				b = append([]byte(`{"secrets":false,`), b[1:]...)
				must(t, os.WriteFile(filepath.Join(out, manifestName), b, 0600))
			case "traversal":
				m.Inventory[0].RelativePath = "../credential"
				must(t, os.WriteFile(filepath.Join(out, manifestName), rawJSON(t, m), 0600))
			case "symlink":
				target := filepath.Join(out, blob.RelativePath)
				must(t, os.Remove(target))
				must(t, os.Symlink(f.request.Sources[0].File, target))
			case "extra-file":
				must(t, os.WriteFile(filepath.Join(out, "state.db-wal"), []byte("unexpected sidecar"), 0600))
			case "missing-sink-journal":
				for _, e := range m.Inventory {
					if e.Kind == "stream" {
						must(t, os.Remove(filepath.Join(out, e.RelativePath)))
					}
				}
			}
			if _, err = Verify(out); err == nil {
				t.Fatal("invalid backup accepted")
			}
		})
	}
}
func TestMissingSourceNeverCompletes(t *testing.T) {
	f := testFixture(t, true)
	f.custody(t)
	var sha string
	must(t, f.db.QueryRow("SELECT digest FROM artifact_blobs").Scan(&sha))
	must(t, os.Remove(filepath.Join(f.artifacts, "blobs", sha[:2], sha)))
	out := filepath.Join(f.root, "incomplete")
	if _, err := Create(ctx, f.s, f.artifacts, f.streams, out); err == nil {
		t.Fatal("missing source acknowledged")
	}
	if _, err := Verify(out); err == nil {
		t.Fatal("incomplete backup verified")
	}
}
func TestBackupNoCredentialsOrGatewayConfiguration(t *testing.T) {
	f := testFixture(t, true)
	f.custody(t)
	secret := []byte("sk-backup-test-credential-never-copy-581632")
	for _, file := range []string{filepath.Join(f.state, "owner.key"), filepath.Join(f.state, "gateway.json"), filepath.Join(f.artifacts, "token.txt"), filepath.Join(f.streams, "credentials.json")} {
		must(t, os.WriteFile(file, secret, 0600))
	}
	execute(t, f.db, "INSERT INTO gateway_profiles VALUES(?,?,?)", strings.Repeat("e", 64), string(secret), time.Now().UnixMilli())
	out := f.backup(t)
	must(t, filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b := read(t, path)
		for _, pattern := range [][]byte{secret, []byte("-----BEGIN PRIVATE KEY-----"), []byte("Bearer sk-"), []byte("sk-backup-test-")} {
			if bytes.Contains(b, pattern) {
				t.Errorf("secret pattern in %s", path)
			}
		}
		return nil
	}))
	var original string
	must(t, f.db.QueryRow("SELECT body FROM gateway_profiles").Scan(&original))
	if original != string(secret) {
		t.Fatal("source config mutated")
	}
}
func TestRestoreNewGenerationPausedHistoryAndRefusesNonEmpty(t *testing.T) {
	f := testFixture(t, true)
	f.custody(t)
	out := f.backup(t)
	state, art := filepath.Join(f.root, "restored"), filepath.Join(f.root, "restored-artifacts")
	entry, err := Restore(ctx, out, state, art)
	must(t, err)
	if entry.OldGeneration != f.identity.Generation || entry.NewGeneration == entry.OldGeneration || !p.ValidID(entry.NewGeneration) || entry.ManifestSHA256 != sum(read(t, filepath.Join(out, manifestName))) {
		t.Fatal(entry)
	}
	restored, err := store.Open(ctx, state, art)
	must(t, err)
	defer restored.Close()
	history, err := restored.RestoreHistory(ctx)
	must(t, err)
	if len(history) != 1 || history[0] != entry {
		t.Fatal(history)
	}
	db, err := openDatabase(filepath.Join(state, "state.db"), false)
	must(t, err)
	defer db.Close()
	var paused int
	var reason string
	must(t, db.QueryRow("SELECT paused,reason FROM daemon_state").Scan(&paused, &reason))
	if paused != 1 || reason != "restored" {
		t.Fatal(paused, reason)
	}
	must(t, integrity(ctx, db))
	sink, err := runstream.OpenSink(filepath.Join(state, "streams", f.identity.AttemptID+".sink"), f.identity)
	must(t, err)
	if sink.Acknowledged() != 1 {
		t.Fatal("sink lost durable ack")
	}
	must(t, sink.Close())
	before := read(t, filepath.Join(state, "state.db"))
	if _, err = Restore(ctx, out, state, filepath.Join(f.root, "another-art")); err == nil {
		t.Fatal("nonempty state accepted")
	}
	if !bytes.Equal(before, read(t, filepath.Join(state, "state.db"))) {
		t.Fatal("refusal overwrote database")
	}
	nonempty := filepath.Join(f.root, "not-empty")
	must(t, os.Mkdir(nonempty, 0700))
	must(t, os.WriteFile(filepath.Join(nonempty, "keep"), []byte("only copy"), 0600))
	empty := filepath.Join(f.root, "empty-state")
	if _, err = Restore(ctx, out, empty, nonempty); err == nil {
		t.Fatal("nonempty artifacts accepted")
	}
	if string(read(t, filepath.Join(nonempty, "keep"))) != "only copy" {
		t.Fatal("source lost")
	}
	items, err := os.ReadDir(empty)
	must(t, err)
	if len(items) != 0 {
		t.Fatal("wrote before both targets passed preflight")
	}
}
func TestENOSPCDoesNotLoseOnlyDurableCopy(t *testing.T) {
	for _, mode := range []string{"backup", "restore"} {
		t.Run(mode, func(t *testing.T) {
			f := testFixture(t, true)
			f.custody(t)
			good := f.backup(t)
			before := read(t, filepath.Join(good, "state.db"))
			source := read(t, f.request.Sources[0].File)
			budget := int64(7)
			if mode == "restore" {
				budget = 37 + 2048
			}
			ops := fileOps{write: func(file *os.File, b []byte) (int, error) {
				if budget <= 0 {
					return 0, syscall.ENOSPC
				}
				if int64(len(b)) > budget {
					n, err := file.Write(b[:budget])
					budget = 0
					if err != nil {
						return n, err
					}
					return n, syscall.ENOSPC
				}
				n, err := file.Write(b)
				budget -= int64(n)
				return n, err
			}}
			var err error
			if mode == "backup" {
				_, err = create(ctx, f.s, f.artifacts, f.streams, filepath.Join(f.root, "full"), ops)
			} else {
				_, err = restore(ctx, good, filepath.Join(f.root, "restore-full"), filepath.Join(f.root, "restore-full-art"), ops)
			}
			if !errors.Is(err, syscall.ENOSPC) {
				t.Fatal("injected disk pressure not surfaced", err)
			}
			_, err = Verify(good)
			must(t, err)
			if !bytes.Equal(before, read(t, filepath.Join(good, "state.db"))) || !bytes.Equal(source, read(t, f.request.Sources[0].File)) {
				t.Fatal("only durable copy changed")
			}
			if mode == "backup" {
				if _, err = Verify(filepath.Join(f.root, "full")); err == nil {
					t.Fatal("ENOSPC completed backup")
				}
			}
			if mode == "restore" {
				if _, err = os.Stat(filepath.Join(f.root, "restore-full", "state.db")); !os.IsNotExist(err) {
					t.Fatal("unfenced restore published")
				}
			}
		})
	}
}
func TestInterruptedBackupKilledChild(t *testing.T) {
	if root := os.Getenv("GAFFER_BACKUP_KILL_CHILD"); root != "" {
		s, err := store.Open(ctx, filepath.Join(root, "state"), filepath.Join(root, "artifacts"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		// Block after a real partial blob write. The parent sends SIGKILL, so neither
		// defers nor a simulated error can clean up or manufacture this evidence.
		ops := fileOps{write: func(f *os.File, b []byte) (int, error) {
			n, err := f.Write(b[:1])
			if err != nil {
				return n, err
			}
			if err = f.Sync(); err != nil {
				return n, err
			}
			fmt.Println("MID_COPY_DURABLE")
			select {}
		}}
		_, err = create(ctx, s, filepath.Join(root, "artifacts"), filepath.Join(root, "state", "streams"), filepath.Join(root, "killed"), ops)
		t.Fatal("child escaped copy barrier", err)
	}
	f := testFixture(t, true)
	f.custody(t)
	must(t, f.db.Close())
	must(t, f.s.Close())
	child := exec.Command(os.Args[0], "-test.run=^TestInterruptedBackupKilledChild$")
	child.Env = append(os.Environ(), "GAFFER_BACKUP_KILL_CHILD="+f.root)
	stdout, err := child.StdoutPipe()
	must(t, err)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	must(t, child.Start())
	defer func() { _ = child.Process.Kill() }()
	marker := make(chan string, 1)
	go func() {
		var line string
		_, err := fmt.Fscanln(stdout, &line)
		if err != nil {
			marker <- err.Error()
			return
		}
		if line != "MID_COPY_DURABLE" {
			marker <- "unexpected child output: " + line
			return
		}
		marker <- "ready"
	}()
	select {
	case value := <-marker:
		if value != "ready" {
			t.Fatal(value, stderr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("child never reached copy barrier", stderr.String())
	}
	must(t, child.Process.Kill())
	err = child.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("not killed", err)
	}
	out := filepath.Join(f.root, "killed")
	if _, err = os.Stat(filepath.Join(out, manifestName)); !os.IsNotExist(err) {
		t.Fatal("interrupted backup has manifest", err)
	}
	if _, err = Verify(out); err == nil {
		t.Fatal("interrupted backup verified")
	}
	restored, err := store.Open(ctx, f.state, f.artifacts)
	must(t, err)
	defer restored.Close()
	if _, err = Create(ctx, restored, f.artifacts, f.streams, filepath.Join(f.root, "after-kill")); err != nil {
		t.Fatal("source not recoverable", err)
	}
}

// Populate retained relational outbox history, not an execution/runtime mock.
// The real store readers decode its canonical message and input digest before
// the generation checks. No admission or execution is inferred from these rows.
func (f *fixture) retainedDispatch(t *testing.T) {
	t.Helper()
	owner, runner := uuid(), uuid()
	f.runner = strings.Repeat("d", 64)
	f.dispatch = uuid()
	execute(t, f.db, "INSERT INTO principals VALUES(?,'owner',1,0,1)", owner)
	execute(t, f.db, "INSERT INTO principals VALUES(?,'runner',1,0,1)", runner)
	execute(t, f.db, "INSERT INTO credentials VALUES(?,?,0)", f.runner, runner)
	execute(t, f.db, "INSERT INTO repositories VALUES('repo','file:///not-used')")
	grantID := uuid()
	execute(t, f.db, "INSERT INTO execution_grants VALUES(?,?,1,'{}',?)", grantID, f.identity.TaskID, time.Now().Add(time.Hour).UnixMilli())
	facts := sc.Eligibility{ID: "retained", Revision: 1, RunnerBoot: uuid(), Repository: r.Selection{RunnerRoot: r.RunnerRoot{RunnerID: runner}}}
	request := store.DispatchRequest{ID: f.dispatch, Request: g.Request{TaskID: f.identity.TaskID, GrantID: grantID}}
	in := struct {
		store.DispatchRequest
		Facts sc.Eligibility `json:"facts"`
	}{request, facts}
	input := rawJSON(t, in)
	hash := sum(input)
	execute(t, f.db, "INSERT INTO dispatch_eligibility VALUES(?,1,?,?, 'repo',?,?)", facts.ID, string(rawJSON(t, facts)), owner, runner, "/not-used")
	f.assignment = p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "assign", Identity: f.identity, AssignmentID: uuid(), InputDigest: hash, Route: &p.Route{RouteRef: "test", DecisionDigest: strings.Repeat("a", 64), PolicyDigest: strings.Repeat("b", 64), LimitsProfile: "strict-provider-output-v1"}}
	execute(t, f.db, "INSERT INTO dispatches VALUES(?,?,?,?,?,?,1,?,?,?,?)", f.dispatch, f.identity.AttemptID, f.identity.TaskID, grantID, runner, facts.ID, hash, string(input), string(rawJSON(t, f.assignment)), time.Now().UnixMilli())
	execute(t, f.db, "INSERT INTO events VALUES(1,?,?,?,?,?,?)", f.assignment.MessageID, f.identity.AttemptID, f.identity.TaskID, f.identity.Epoch, 1, string(rawJSON(t, f.assignment)))
	d, err := f.s.Assignment(ctx, f.dispatch)
	must(t, err)
	if !reflect.DeepEqual(d.Assignment, f.assignment) {
		t.Fatal("fixture failed real outbox decoding")
	}
}
func TestRestoreFencesOldAssignmentLeaseAndResult(t *testing.T) {
	f := testFixture(t, true)
	f.retainedDispatch(t)
	// The result is retained at the old runner, not yet acknowledged by the source.
	out := f.backup(t)
	state, art := filepath.Join(f.root, "fenced"), filepath.Join(f.root, "fenced-artifacts")
	_, err := Restore(ctx, out, state, art)
	must(t, err)
	s, err := store.Open(ctx, state, art)
	must(t, err)
	defer s.Close()
	d, err := f.s.Assignment(ctx, f.dispatch)
	must(t, err)
	old, err := f.s.Status(ctx)
	must(t, err)
	ack := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: uuid(), Identity: f.identity, AssignmentID: f.assignment.AssignmentID, RunnerBoot: d.Facts.RunnerBoot, DaemonBoot: old.DaemonBoot}
	if err = s.AcknowledgeAssignment(ctx, f.runner, f.dispatch, ack); !errors.Is(err, p.StaleGeneration) {
		t.Fatal("old assignment not fenced", err)
	}
	sent := time.Now().UnixMilli()
	lease := p.Message{SentMS: &sent, Version: p.FencedVersion, Kind: "lease_request", MessageID: uuid(), Identity: f.identity, RunnerBoot: d.Facts.RunnerBoot, DaemonBoot: old.DaemonBoot, Nonce: uuid()}
	if err = s.FenceControlMessage(ctx, f.runner, p.FencedVersion, lease); !errors.Is(err, p.StaleGeneration) {
		t.Fatal("old lease not fenced", err)
	}
	if err = s.FenceControlMessage(ctx, f.runner, p.FencedVersion, f.request.Result); !errors.Is(err, p.StaleGeneration) {
		t.Fatal("old result not fenced", err)
	}
	receipt, err := s.CustodyResult(ctx, f.request)
	must(t, err)
	if !receipt.Quarantined {
		t.Fatal("old result treated as current")
	}
	db, err := openDatabase(filepath.Join(state, "state.db"), false)
	must(t, err)
	defer db.Close()
	var current, quarantined, heads int
	must(t, db.QueryRow("SELECT current,quarantined FROM artifact_results WHERE receipt_id=?", receipt.Receipt.ReceiptID).Scan(&current, &quarantined))
	must(t, db.QueryRow("SELECT count(*) FROM artifact_result_heads WHERE generation=?", f.identity.Generation).Scan(&heads))
	if current != 0 || quarantined != 1 || heads != 0 {
		t.Fatal("old generation promoted", current, quarantined, heads)
	}
}
func TestRestoreQuarantinesAlreadyAcknowledgedReceiptReplay(t *testing.T) {
	f := testFixture(t, true)
	prior := f.custody(t)
	out := f.backup(t)
	state, art := filepath.Join(f.root, "replay"), filepath.Join(f.root, "replay-artifacts")
	_, err := Restore(ctx, out, state, art)
	must(t, err)
	s, err := store.Open(ctx, state, art)
	must(t, err)
	defer s.Close()
	replay := f.request
	replay.Sources = nil
	receipt, err := s.CustodyResult(ctx, replay)
	must(t, err)
	if !receipt.Quarantined || receipt.Receipt.ReceiptID != prior.Receipt.ReceiptID {
		t.Fatal("historical receipt replay not quarantined", receipt)
	}
}
func TestOpenRestoredAtomicallyRecoversActiveAttempts(t *testing.T) {
	f := testFixture(t, false)
	// Create correctly refuses active work. Exercise OpenRestored's defensive
	// recovery separately using an actual SQLite consistent snapshot, as a legacy
	// or imported backup may carry unresolved history.
	target := filepath.Join(f.root, "legacy-copy")
	must(t, os.Mkdir(target, 0700))
	path := filepath.Join(target, "state.db")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	must(t, err)
	must(t, file.Close())
	execute(t, f.db, "VACUUM INTO ?", path)
	s, err := store.OpenRestored(ctx, target, filepath.Join(f.root, "legacy-artifacts"), store.RestoreRecord{BackupID: uuid(), BackupCreatedMS: time.Now().UnixMilli(), ManifestSHA256: strings.Repeat("a", 64)}, store.Options{})
	must(t, err)
	defer s.Close()
	db, err := openDatabase(path, false)
	must(t, err)
	defer db.Close()
	var state, generation, reason string
	var paused, revision, history int
	must(t, db.QueryRow("SELECT state,revision FROM attempts WHERE id=?", f.identity.AttemptID).Scan(&state, &revision))
	must(t, db.QueryRow("SELECT generation FROM metadata").Scan(&generation))
	must(t, db.QueryRow("SELECT paused,reason FROM daemon_state").Scan(&paused, &reason))
	must(t, db.QueryRow("SELECT count(*) FROM restore_history").Scan(&history))
	if state != "unknown" || revision != 2 || generation == f.identity.Generation || paused != 1 || reason != "restored" || history != 1 {
		t.Fatal(state, revision, generation, paused, reason, history)
	}
	must(t, integrity(ctx, db))
}
func TestOpenRestoredRollsBackGenerationWhenHistoryFails(t *testing.T) {
	f := testFixture(t, true)
	out := f.backup(t)
	target := filepath.Join(f.root, "rollback")
	must(t, os.Mkdir(target, 0700))
	must(t, os.WriteFile(filepath.Join(target, "state.db"), read(t, filepath.Join(out, "state.db")), 0600))
	db, err := openDatabase(filepath.Join(target, "state.db"), false)
	must(t, err)
	execute(t, db, "CREATE TRIGGER injected_full BEFORE INSERT ON restore_history BEGIN SELECT RAISE(ABORT,'disk is full'); END")
	must(t, db.Close())
	if s, err := store.OpenRestored(ctx, target, filepath.Join(f.root, "rollback-artifacts"), store.RestoreRecord{BackupID: uuid(), BackupCreatedMS: time.Now().UnixMilli(), ManifestSHA256: strings.Repeat("a", 64)}, store.Options{}); err == nil {
		s.Close()
		t.Fatal("failed restore committed")
	}
	db, err = openDatabase(filepath.Join(target, "state.db"), false)
	must(t, err)
	defer db.Close()
	var generation string
	var history int
	must(t, db.QueryRow("SELECT generation FROM metadata").Scan(&generation))
	must(t, db.QueryRow("SELECT count(*) FROM restore_history").Scan(&history))
	if generation != f.identity.Generation || history != 0 {
		t.Fatal("partial generation committed", generation, history)
	}
}
func TestServiceDestinationConfinement(t *testing.T) {
	f := testFixture(t, true)
	svc, err := NewService(f.s, f.artifacts, f.streams, filepath.Join(f.root, "backups"))
	must(t, err)
	for _, bad := range []string{"../escape", "nested/child", "/tmp/elsewhere", ".", "..", "x\\y"} {
		if _, err = svc.Create(ctx, bad); err == nil {
			t.Fatal("destination escaped", bad)
		}
	}
	must(t, os.Symlink(f.artifacts, filepath.Join(f.root, "backups", "linked")))
	if _, err = svc.Create(ctx, "linked"); err == nil {
		t.Fatal("symlink destination accepted")
	}
	if _, err = svc.Create(ctx, "safe"); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Verify("safe"); err != nil {
		t.Fatal(err)
	}
}
func TestOfflineRestoreCLI(t *testing.T) {
	f := testFixture(t, true)
	f.custody(t)
	out := f.backup(t)
	binary := filepath.Join(f.root, "gaffer")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/gaffer")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	args := []string{"restore", "--timeout", "1ms", "--backup", out, "--state-dir", filepath.Join(f.root, "cli-state"), "--artifacts-dir", filepath.Join(f.root, "cli-art"), "--json"}
	command := exec.Command(binary, args...)
	command.Env = []string{"PATH=" + os.Getenv("PATH")}
	output, err := command.CombinedOutput()
	must(t, err)
	var result struct {
		Version, Command string
		Result           struct {
			Paused  bool
			Restore store.RestoreEntry
		}
	}
	must(t, json.Unmarshal(output, &result))
	if result.Version != "gaffer-cli-v1" || result.Command != "restore" || !result.Result.Paused || result.Result.Restore.NewGeneration == f.identity.Generation {
		t.Fatal(string(output))
	}
	command = exec.Command(binary, args...)
	output, err = command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !bytes.Contains(output, []byte("restore_refused")) || !bytes.Contains(output, []byte("target_not_empty")) {
		t.Fatal(string(output), err)
	}
	for _, mode := range []string{"invalid_destination", "digest_mismatch"} {
		t.Run(mode, func(t *testing.T) {
			destination := filepath.Join(f.root, "cli-"+mode)
			if mode == "invalid_destination" {
				destination = "relative-state"
			} else {
				blob := f.request.Manifest
				must(t, os.WriteFile(filepath.Join(out, "artifacts", "manifests", f.request.Result.Manifest.ManifestID+".json"), append(bytes.Clone(blob), '\n'), 0600))
			}
			command := exec.Command(binary, "restore", "--backup", out, "--state-dir", destination, "--artifacts-dir", filepath.Join(f.root, "cli-"+mode+"-art"), "--timeout", "1ms", "--json")
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || !bytes.Contains(output, []byte(mode)) {
				t.Fatal(string(output), err)
			}
		})
	}
}

func TestEmptyBlobAndRequiredZeroByteField(t *testing.T) {
	f := testFixture(t, true)
	must(t, os.WriteFile(f.request.Sources[0].File, nil, 0600))
	var candidate store.CandidateManifest
	must(t, json.Unmarshal(f.request.Manifest, &candidate))
	candidate.Tracked[0].Bytes = 0
	candidate.Tracked[0].SHA256 = sum(nil)
	f.request.Manifest = rawJSON(t, candidate)
	f.request.Result.Manifest.SHA256 = sum(f.request.Manifest)
	f.request.Result.Manifest.Bytes = int64(len(f.request.Manifest))
	f.custody(t)
	out := f.backup(t)
	m, err := Verify(out)
	must(t, err)
	var found bool
	for _, e := range m.Inventory {
		if e.Kind == "blob" {
			found = true
			if e.Bytes != 0 || len(read(t, filepath.Join(out, e.RelativePath))) != 0 {
				t.Fatal(e)
			}
		}
	}
	if !found {
		t.Fatal("empty blob omitted")
	}
	var raw map[string]json.RawMessage
	must(t, json.Unmarshal(read(t, filepath.Join(out, manifestName)), &raw))
	var entries []map[string]json.RawMessage
	must(t, json.Unmarshal(raw["inventory"], &entries))
	for _, e := range entries {
		if string(e["kind"]) == `"blob"` {
			delete(e, "bytes")
		}
	}
	raw["inventory"] = rawJSON(t, entries)
	must(t, os.WriteFile(filepath.Join(out, manifestName), rawJSON(t, raw), 0600))
	if _, err = Verify(out); err == nil {
		t.Fatal("missing zero-byte field defaulted to zero")
	}
}
func TestVerifyChecksDatabaseForeignKeysNotOnlyHashes(t *testing.T) {
	f := testFixture(t, true)
	f.custody(t)
	out := f.backup(t)
	m, err := Verify(out)
	must(t, err)
	db, err := openDatabase(filepath.Join(out, "state.db"), false)
	must(t, err)
	execute(t, db, "PRAGMA foreign_keys=OFF")
	execute(t, db, "INSERT INTO artifact_manifest_blobs VALUES(?,?,'tracked','missing.txt',1)", f.request.Result.Manifest.ManifestID, strings.Repeat("f", 64))
	must(t, db.Close())
	data := read(t, filepath.Join(out, "state.db"))
	m.Database = Blob{sum(data), int64(len(data))}
	must(t, os.WriteFile(filepath.Join(out, manifestName), rawJSON(t, m), 0600))
	if _, err = Verify(out); err == nil || err.Error() != "database_foreign_keys" {
		t.Fatal("foreign-key corruption accepted behind valid hash", err)
	}
}
func TestPresentSinkWithMissingJournalRefused(t *testing.T) {
	f := testFixture(t, true)
	must(t, os.Remove(filepath.Join(f.streams, f.identity.AttemptID+".sink", "journal.jsonl")))
	out := filepath.Join(f.root, "missing-journal")
	if _, err := Create(ctx, f.s, f.artifacts, f.streams, out); err == nil {
		t.Fatal("missing known sink silently omitted")
	}
	if _, err := Verify(out); err == nil {
		t.Fatal("missing sink acknowledged")
	}
}
func TestCancellationReleasesPinWithoutManifest(t *testing.T) {
	f := testFixture(t, true)
	f.custody(t)
	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	ops := fileOps{write: func(file *os.File, b []byte) (int, error) { n, err := file.Write(b); cancel(); return n, err }}
	out := filepath.Join(f.root, "cancelled")
	if _, err := create(cancelled, f.s, f.artifacts, f.streams, out, ops); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Verify(out); err == nil {
		t.Fatal("cancelled backup completed")
	}
	done := make(chan error, 1)
	go func() { _, err := f.s.Status(ctx); done <- err }()
	select {
	case err := <-done:
		must(t, err)
	case <-time.After(time.Second):
		t.Fatal("cancelled backup left store pinned")
	}
}

func TestRestoreExcludesConcurrentDaemonStartup(t *testing.T) {
	f := testFixture(t, true)
	out := f.backup(t)
	state, art := filepath.Join(f.root, "owned-state"), filepath.Join(f.root, "owned-artifacts")
	checked := false
	ops := fileOps{write: func(file *os.File, b []byte) (int, error) {
		if !checked {
			checked = true
			competing, err := store.Open(ctx, state, art)
			if err == nil {
				competing.Close()
				return 0, errors.New("racing daemon acquired final target")
			}
			if _, err = os.Stat(filepath.Join(state, "state.db")); !os.IsNotExist(err) {
				return 0, errors.New("racing daemon created final database")
			}
		}
		return file.Write(b)
	}}
	_, err := restore(ctx, out, state, art, ops)
	must(t, err)
	if !checked {
		t.Fatal("ownership race not exercised")
	}
	s, err := store.Open(ctx, state, art)
	must(t, err)
	must(t, s.Close())
}
func TestRequiredStreamRootOrDirectoryLossRefusesBackup(t *testing.T) {
	for _, wholeRoot := range []bool{true, false} {
		t.Run(fmt.Sprint(wholeRoot), func(t *testing.T) {
			f := testFixture(t, true)
			lost := f.streams
			if !wholeRoot {
				lost = filepath.Join(lost, f.identity.AttemptID+".sink")
			}
			must(t, os.RemoveAll(lost))
			out := filepath.Join(f.root, "lost-output")
			_, err := Create(ctx, f.s, f.artifacts, f.streams, out)
			if err == nil || !strings.Contains(err.Error(), "required_stream_missing") {
				t.Fatal("acknowledged stream loss accepted", err)
			}
			if _, err = Verify(out); err == nil {
				t.Fatal("incomplete backup acknowledged")
			}
		})
	}
}
func TestVerifyRequiredStreamAndWatermark(t *testing.T) {
	for _, mode := range []string{"omitted", "watermark", "identity"} {
		t.Run(mode, func(t *testing.T) {
			f := testFixture(t, true)
			out := f.backup(t)
			m, err := Verify(out)
			must(t, err)
			for i, e := range m.Inventory {
				if e.Kind != "stream" {
					continue
				}
				if mode == "omitted" {
					must(t, os.Remove(filepath.Join(out, e.RelativePath)))
					m.Inventory = append(m.Inventory[:i], m.Inventory[i+1:]...)
				} else {
					identity := f.identity
					if mode == "identity" {
						identity.TaskID = uuid()
					}
					empty := filepath.Join(f.root, "empty-sink")
					sink, err := runstream.CreateSink(empty, identity, runstream.MinLimit)
					must(t, err)
					must(t, sink.Close())
					body := read(t, filepath.Join(empty, "journal.jsonl"))
					must(t, os.WriteFile(filepath.Join(out, e.RelativePath), body, 0600))
					m.Inventory[i].Bytes = int64(len(body))
					m.Inventory[i].SHA256 = sum(body)
				}
				break
			}
			must(t, os.WriteFile(filepath.Join(out, manifestName), rawJSON(t, m), 0600))
			if _, err = Verify(out); err == nil {
				t.Fatal("stream evidence accepted", mode)
			}
		})
	}
}
func TestManifestReferencesCannotLoseRelationalLinks(t *testing.T) {
	f := testFixture(t, true)
	f.custody(t)
	execute(t, f.db, "DELETE FROM artifact_manifest_blobs")
	out := filepath.Join(f.root, "lost-link")
	_, err := Create(ctx, f.s, f.artifacts, f.streams, out)
	if err == nil || err.Error() != "artifact_reference_missing" {
		t.Fatal("manifest content omitted behind missing links", err)
	}
}
func TestRepeatedRestorePreservesHistoricalStreamIdentity(t *testing.T) {
	f := testFixture(t, true)
	out := f.backup(t)
	state, art := filepath.Join(f.root, "first-state"), filepath.Join(f.root, "first-artifacts")
	first, err := Restore(ctx, out, state, art)
	must(t, err)
	s, err := store.Open(ctx, state, art)
	must(t, err)
	defer s.Close()
	next := filepath.Join(f.root, "next-backup")
	_, err = Create(ctx, s, art, filepath.Join(state, "streams"), next)
	must(t, err)
	_, err = Verify(next)
	must(t, err)
	second, err := Restore(ctx, next, filepath.Join(f.root, "second-state"), filepath.Join(f.root, "second-artifacts"))
	must(t, err)
	if second.OldGeneration != first.NewGeneration || second.NewGeneration == first.NewGeneration {
		t.Fatal(first, second)
	}
}
func TestRestoredSnapshotPreservesHistoricalEventGenerations(t *testing.T) {
	f := testFixture(t, true)
	f.retainedDispatch(t)
	before, err := f.s.Snapshot(ctx, "", 50)
	must(t, err)
	if len(before.Events) == 0 {
		t.Fatal("fixture has no real retained events")
	}
	out := f.backup(t)
	state, art := filepath.Join(f.root, "history-state"), filepath.Join(f.root, "history-artifacts")
	_, err = Restore(ctx, out, state, art)
	must(t, err)
	s, err := store.Open(ctx, state, art)
	must(t, err)
	defer s.Close()
	// The restored store has a new generation; the bounded snapshot projects
	// only current-generation events, so it serves reads without rejecting
	// the retained history, which stays in the immutable log unrewritten.
	after, err := s.Snapshot(ctx, "", 50)
	must(t, err)
	if after.Generation == before.Generation {
		t.Fatal("restore kept the old generation")
	}
	if len(after.Events) != 0 {
		t.Fatal("retired-generation events projected as current", after.Events)
	}
	must(t, s.Close())
	db, err := sql.Open("sqlite", "file:"+filepath.Join(state, "state.db")+"?mode=ro&immutable=1")
	must(t, err)
	defer db.Close()
	var retained int
	must(t, db.QueryRow("SELECT count(*) FROM events WHERE json_extract(message,'$.identity.generation')=?", before.Generation).Scan(&retained))
	if retained != len(before.Events) {
		t.Fatalf("historical events lost or rewritten: %d != %d", retained, len(before.Events))
	}
}

func TestDaemonRefusesIncompleteRestoreMarker(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	must(t, os.Mkdir(state, 0700))
	must(t, os.WriteFile(filepath.Join(state, ".restore-incomplete"), []byte("interrupted restore\n"), 0600))
	s, err := store.Open(ctx, state, filepath.Join(root, "artifacts"))
	if err == nil {
		s.Close()
		t.Fatal("daemon started on incomplete restore")
	}
	if _, err = os.Stat(filepath.Join(state, "state.db")); !os.IsNotExist(err) {
		t.Fatal("incomplete restore initialized")
	}
}
