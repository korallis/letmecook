package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
)

// candidate builds a canonical manifest and result message for the fixture's
// attempt from in-memory file contents, the way the runner packs a workspace.
func (x *executionFixture) candidate(t *testing.T, outcome string, files map[string]string) (UploadBegin, map[string][]byte) {
	t.Helper()
	identity := x.d.Assignment.Identity
	m := CandidateManifest{Version: artifactManifestVersion, Identity: identity, Base: ArtifactBase{Revision: x.d.Request.Envelope.BaseCommit, SHA256: sha256Hex([]byte(x.d.Request.Envelope.BaseCommit))}, Outcome: outcome, Tracked: []ArtifactBlob{}, Untracked: []ArtifactBlob{}, Binary: []ArtifactBlob{}, Recovery: []ArtifactBlob{}, Deleted: []string{}}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	blobs := map[string][]byte{}
	for _, name := range names {
		body := []byte(files[name])
		m.Tracked = append(m.Tracked, ArtifactBlob{Path: name, SHA256: sha256Hex(body), Bytes: int64(len(body))})
		blobs[sha256Hex(body)] = body
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	result := p.Message{Version: p.FencedVersion, Kind: "result", MessageID: newID(), Identity: identity, Manifest: &p.Manifest{ManifestID: newID(), SHA256: sha256Hex(raw), Bytes: int64(len(raw))}}
	return UploadBegin{Version: execwire.Version, MessageID: newID(), Result: result, Manifest: raw}, blobs
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// upload runs begin, every PUT and commit for the fixture's attempt.
func (x *executionFixture) upload(t *testing.T, outcome string, files map[string]string) (UploadSession, CommitReply) {
	t.Helper()
	begin, blobs := x.candidate(t, outcome, files)
	attempt := x.d.Assignment.Identity.AttemptID
	session, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, begin)
	if err != nil {
		t.Fatal(err)
	}
	for _, missing := range session.Missing {
		if _, err := x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, missing.SHA256, bytes.NewReader(blobs[missing.SHA256]), -1); err != nil {
			t.Fatal(err)
		}
	}
	reply, err := x.s.CommitUpload(ctx, x.runner, x.session.SessionID, session.UploadID, newID())
	if err != nil {
		t.Fatal(err)
	}
	return session, reply
}

func TestUploadLifecycleReplayIncompleteAndDigestMismatch(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	files := map[string]string{"greeting.txt": "hello, gaffer\n", "empty.txt": ""}
	begin, blobs := x.candidate(t, "succeeded", files)
	attempt := x.d.Assignment.Identity.AttemptID
	session, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, begin)
	if err != nil || len(session.Missing) != 2 || session.BytesAllowed != MaxUploadBytes || session.UploadID != dispatchID(begin.MessageID, "upload") {
		t.Fatal(session, err)
	}
	if session.Missing[0].SHA256 > session.Missing[1].SHA256 {
		t.Fatal("missing inventory unsorted")
	}
	// Replay by message id, reuse by manifest, conflict by changed manifest.
	again, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, begin)
	if err != nil || !reflect.DeepEqual(again, session) {
		t.Fatal("replay", again, err)
	}
	changed := begin
	changed.Manifest = append([]byte(nil), begin.Manifest...)
	changed.Manifest[len(changed.Manifest)-2] = ' '
	if _, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, changed); err == nil {
		t.Fatal("changed manifest under the same message id accepted")
	}
	retry := begin
	retry.MessageID = newID()
	reused, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, retry)
	if err != nil || reused.UploadID != session.UploadID {
		t.Fatal("same manifest opened a second session", reused, err)
	}
	other, _ := x.candidate(t, "failed", map[string]string{"other.txt": "x"})
	if _, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, other); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("second manifest for the attempt accepted", err)
	}
	rowCount(t, x.s, "upload_sessions", 1)
	// Wrong bytes for a promised digest: refused, temp file removed, row still missing.
	greeting := sha256Hex([]byte(files["greeting.txt"]))
	_, err = x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, greeting, strings.NewReader("hello, world\n"), -1)
	requireReason(t, err, "digest_mismatch")
	_, err = x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, greeting, strings.NewReader(files["greeting.txt"]+"x"), -1)
	requireReason(t, err, "digest_mismatch")
	_, err = x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, greeting, strings.NewReader("hello"), -1)
	requireReason(t, err, "digest_mismatch")
	entries, err := os.ReadDir(x.s.uploadDir(session.UploadID))
	if err != nil || len(entries) != 1 || entries[0].Name() != "manifest.json" {
		t.Fatal("mismatch left files", entries, err)
	}
	_, err = x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, sha256Hex([]byte("not in inventory")), strings.NewReader("not in inventory"), -1)
	if !errors.Is(err, p.IdentityConflict) {
		t.Fatal("foreign digest staged", err)
	}
	_, err = x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, greeting, strings.NewReader(files["greeting.txt"]), 3)
	requireReason(t, err, "digest_mismatch")
	// Commit with a missing blob names it and commits nothing.
	_, err = x.s.CommitUpload(ctx, x.runner, x.session.SessionID, session.UploadID, newID())
	requireReason(t, err, "upload_incomplete")
	if !strings.Contains(err.Error(), greeting) {
		t.Fatal("missing digest not listed", err)
	}
	rowCount(t, x.s, "artifact_results", 0)
	blob, err := x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, greeting, strings.NewReader(files["greeting.txt"]), -1)
	if err != nil || blob.Duplicate || blob.Bytes != int64(len(files["greeting.txt"])) {
		t.Fatal(blob, err)
	}
	blob, err = x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, greeting, strings.NewReader(files["greeting.txt"]), -1)
	if err != nil || !blob.Duplicate {
		t.Fatal("re-PUT not a duplicate", blob, err)
	}
	view, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, begin)
	if err != nil || len(view.Missing) != 1 || view.Missing[0].Bytes != 0 || view.BytesAllowed != MaxUploadBytes-int64(len(files["greeting.txt"])) {
		t.Fatal(view, err)
	}
	empty := sha256Hex(nil)
	if _, err := x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, empty, bytes.NewReader(nil), -1); err != nil {
		t.Fatal("zero-byte blob refused", err)
	}
	reply, err := x.s.CommitUpload(ctx, x.runner, x.session.SessionID, session.UploadID, newID())
	if err != nil || reply.Quarantined || p.CheckAck(begin.Result, reply.Ack, begin.Result.Identity, reply.Receipt) != p.OK || reply.Ack.MessageID != dispatchID(reply.Receipt.ReceiptID, "ack") {
		t.Fatal(reply, err)
	}
	for digest, body := range blobs {
		if err := verifyBlob(blobPath(x.artifacts, digest), ArtifactBlob{SHA256: digest, Bytes: int64(len(body))}); err != nil {
			t.Fatal("custody blob", digest, err)
		}
	}
	if _, err := os.Stat(x.s.uploadDir(session.UploadID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staging retained after commit", err)
	}
	// Lost reply: the replayed commit is byte-identical, before and after restart.
	first, _ := json.Marshal(reply)
	replay, err := x.s.CommitUpload(ctx, x.runner, x.session.SessionID, session.UploadID, newID())
	second, _ := json.Marshal(replay)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("commit replay differs", err)
	}
	var state string
	if err := x.s.db.QueryRow("SELECT state FROM upload_sessions WHERE id=?", session.UploadID).Scan(&state); err != nil || state != "committed" {
		t.Fatal(state, err)
	}
	rowCount(t, x.s, "upload_blobs WHERE state='committed'", 2)
	rowCount(t, x.s, "artifact_results", 1)
	reopen(t, &x)
	x.session = sessionFor(t, x.dispatchFixture)
	replay, err = x.s.CommitUpload(ctx, x.runner, x.session.SessionID, session.UploadID, newID())
	second, _ = json.Marshal(replay)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("commit replay after restart differs", err)
	}
	if _, err := x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, greeting, strings.NewReader(files["greeting.txt"]), -1); !errors.Is(err, p.ReconciliationRequired) {
		t.Fatal("closed session accepted bytes", err)
	}
	for _, query := range []string{"DELETE FROM upload_sessions", "DELETE FROM upload_blobs", "UPDATE upload_sessions SET state='open'", "UPDATE upload_blobs SET digest='x'"} {
		if _, err := x.s.db.Exec(query); err == nil {
			t.Fatal("mutable upload history", query)
		}
	}
}

func TestUploadRefusalsBoundsAndStaleGenerationQuarantine(t *testing.T) {
	x := executionFixtureFor(t, nil)
	begin, blobs := x.candidate(t, "succeeded", map[string]string{"a.txt": "a"})
	attempt := x.d.Assignment.Identity.AttemptID
	// Only a result_pending attempt may upload; only its runner; only its identity.
	if _, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, begin); !errors.Is(err, p.ReconciliationRequired) {
		t.Fatal("assigned attempt uploaded", err)
	}
	x.run(t)
	other, otherSession := secondRunner(t, x.dispatchFixture)
	_, err := x.s.BeginUpload(ctx, other, otherSession.SessionID, attempt, begin)
	requireReason(t, err, "runner_disabled")
	wrong := begin
	wrong.Result.Identity.Epoch = 2
	if _, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, wrong); err == nil {
		t.Fatal("foreign identity bound")
	}
	drifted := begin
	drifted.Result.Manifest = &p.Manifest{ManifestID: begin.Result.Manifest.ManifestID, SHA256: strings.Repeat("0", 64), Bytes: begin.Result.Manifest.Bytes}
	if _, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, drifted); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("manifest hash drift accepted", err)
	}
	// Inventory bounds: one blob over 64 MiB or an aggregate over 256 MiB is
	// refused before any byte moves.
	huge, _ := x.candidate(t, "succeeded", map[string]string{"big.bin": "x"})
	var m CandidateManifest
	if err := json.Unmarshal(huge.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	m.Tracked[0].Bytes = MaxBlobBytes + 1
	huge.Manifest, _ = json.Marshal(m)
	huge.Result.Manifest.SHA256, huge.Result.Manifest.Bytes = sha256Hex(huge.Manifest), int64(len(huge.Manifest))
	if _, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, huge); !errors.Is(err, p.Oversized) {
		t.Fatal("oversized blob admitted", err)
	}
	m.Tracked = []ArtifactBlob{}
	for n := range 5 {
		m.Tracked = append(m.Tracked, ArtifactBlob{Path: "part" + string(rune('a'+n)), SHA256: strings.Repeat(string(rune('0'+n)), 64), Bytes: MaxBlobBytes})
	}
	huge.Manifest, _ = json.Marshal(m)
	huge.Result.Manifest.SHA256, huge.Result.Manifest.Bytes = sha256Hex(huge.Manifest), int64(len(huge.Manifest))
	if _, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, huge); !errors.Is(err, p.Oversized) {
		t.Fatal("oversized aggregate admitted", err)
	}
	rowCount(t, x.s, "upload_sessions", 0)
	// Aggregate bytes across the attempt's sessions cap every PUT.
	session, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, begin)
	if err != nil {
		t.Fatal(err)
	}
	sqlExec(t, x.s, "INSERT INTO upload_sessions VALUES('"+newID()+"','"+attempt+"','"+x.session.RunnerID+"','"+newID()+"','"+strings.Repeat("1", 64)+"',1,'{}','abandoned',"+itoa(MaxUploadBytes)+","+itoa(time.Now().UnixMilli())+")")
	digest := session.Missing[0].SHA256
	if _, err := x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, digest, bytes.NewReader(blobs[digest]), -1); !errors.Is(err, p.Oversized) {
		t.Fatal("aggregate cap ignored", err)
	}
	if _, err := os.Stat(filepath.Join(x.s.uploadDir(session.UploadID), digest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused PUT stored bytes")
	}
	if _, err := x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, digest, bytes.NewReader(blobs[digest]), MaxBlobBytes+1); !errors.Is(err, p.Malformed) {
		t.Fatal(err)
	}
	// Commit before reply on begin and on the staged mark.
	y := executionFixtureFor(t, nil)
	y.run(t)
	ybegin, yblobs := y.candidate(t, "succeeded", map[string]string{"b.txt": "b"})
	yattempt := y.d.Assignment.Identity.AttemptID
	for _, step := range []string{"before_upload_commit", "after_upload_commit"} {
		y.s.controlHook = func(got string) error {
			if got == step {
				return errors.New("crash")
			}
			return nil
		}
		if _, err := y.s.BeginUpload(ctx, y.runner, y.session.SessionID, yattempt, ybegin); err == nil {
			t.Fatal("reply after injected failure")
		}
		y.s.controlHook = nil
		rowCount(t, y.s, "upload_sessions", map[string]int{"before_upload_commit": 0, "after_upload_commit": 1}[step])
	}
	ysession, err := y.s.BeginUpload(ctx, y.runner, y.session.SessionID, yattempt, ybegin)
	if err != nil {
		t.Fatal(err)
	}
	ydigest := ysession.Missing[0].SHA256
	for _, step := range []string{"after_blob_fsync", "before_blob_commit", "after_blob_commit"} {
		y.s.controlHook = func(got string) error {
			if got == step {
				return errors.New("crash")
			}
			return nil
		}
		if _, err := y.s.RecordUploadedBlob(ctx, y.runner, y.session.SessionID, ysession.UploadID, ydigest, bytes.NewReader(yblobs[ydigest]), -1); err == nil {
			t.Fatal("reply after injected failure")
		}
		y.s.controlHook = nil
		var state string
		if err := y.s.db.QueryRow("SELECT state FROM upload_blobs WHERE upload_id=? AND digest=?", ysession.UploadID, ydigest).Scan(&state); err != nil {
			t.Fatal(err)
		}
		_, statErr := os.Stat(filepath.Join(y.s.uploadDir(ysession.UploadID), ydigest))
		switch step {
		case "after_blob_fsync":
			if state != "missing" || statErr == nil {
				t.Fatal("unrenamed temp file promoted", state, statErr)
			}
			if _, err := y.s.CommitUpload(ctx, y.runner, y.session.SessionID, ysession.UploadID, newID()); err == nil {
				t.Fatal("committed without the blob")
			}
		case "before_blob_commit":
			if state != "missing" || statErr != nil {
				t.Fatal("staged file without row, or row without file", state, statErr)
			}
		case "after_blob_commit":
			if state != "staged" || statErr != nil {
				t.Fatal("committed mark lost", state, statErr)
			}
		}
	}
	blob, err := y.s.RecordUploadedBlob(ctx, y.runner, y.session.SessionID, ysession.UploadID, ydigest, bytes.NewReader(yblobs[ydigest]), -1)
	if err != nil || !blob.Duplicate {
		t.Fatal(blob, err)
	}
	// Lost reply between custody and the session mark: the replay is identical
	// and the session closes on replay.
	y.s.controlHook = func(got string) error {
		if got == "after_custody" {
			return errors.New("crash")
		}
		return nil
	}
	if _, err := y.s.CommitUpload(ctx, y.runner, y.session.SessionID, ysession.UploadID, newID()); err == nil {
		t.Fatal("reply after injected failure")
	}
	y.s.controlHook = nil
	rowCount(t, y.s, "artifact_results", 1)
	rowCount(t, y.s, "upload_sessions WHERE state='open'", 1)
	reply, err := y.s.CommitUpload(ctx, y.runner, y.session.SessionID, ysession.UploadID, newID())
	if err != nil || p.CheckAck(ybegin.Result, reply.Ack, ybegin.Result.Identity, reply.Receipt) != p.OK {
		t.Fatal(reply, err)
	}
	rowCount(t, y.s, "upload_sessions WHERE state='committed'", 1)
	rowCount(t, y.s, "artifact_results", 1)
	// A stale-generation result is retained as quarantine evidence, never current,
	// and can never finalize.
	z := executionFixtureFor(t, nil)
	z.run(t)
	sqlExec(t, z.s, "UPDATE metadata SET generation='"+newID()+"' WHERE singleton=1")
	reopen(t, &z)
	z.session = sessionFor(t, z.dispatchFixture)
	_, zreply := z.upload(t, "succeeded", map[string]string{"c.txt": "c"})
	if !zreply.Quarantined {
		t.Fatal("stale-generation result not quarantined")
	}
	rowCount(t, z.s, "artifact_results WHERE quarantined=1 AND current=0", 1)
	completion := Completion{Version: execwire.Version, MessageID: newID(), ReceiptID: zreply.Receipt.ReceiptID, Stream: StreamWatermark{Digest: sha256Hex(nil)}, Exit: ExitRecord{PGID: 101, ObservedUnixNS: exitEvidence(0).ObservedUnixNS}, Boundary: quiescent()}
	if _, err := z.s.FinalizeAttempt(ctx, z.runner, z.session.SessionID, z.d.Assignment.Identity.AttemptID, completion); !errors.Is(err, p.StaleGeneration) {
		t.Fatal("stale generation finalized", err)
	}
	if _, err := z.s.StreamIdentity(ctx, z.runner, z.session.SessionID, z.d.Assignment.Identity.AttemptID); !errors.Is(err, p.StaleGeneration) {
		t.Fatal("stale generation streamed", err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
