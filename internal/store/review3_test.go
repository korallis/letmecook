package store

// Regressions for the third review of the execution channel (findings on
// 80871cd~2..80871cd): each case is a verified defect and its fix.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/runstream"
	p "github.com/korallis/letmecook/schemas/execution"
)

// P1-1: staging directories are pinned by an open handle before anything is
// removed or created, so replacing upload/ with a link after the check cannot
// redirect the removal or the writes.
func TestBeginUploadPinsStagingBeforeRemoving(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	begin, _ := x.candidate(t, "succeeded", map[string]string{"a.txt": "bytes"})
	id := dispatchID(begin.MessageID, "upload")
	upload := filepath.Join(x.artifacts, "upload")
	victim := filepath.Join(x.artifacts, "victim")
	retained := filepath.Join(victim, id, "retained")
	if err := os.MkdirAll(filepath.Dir(retained), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retained, []byte("retain me"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(upload, 0700); err != nil {
		t.Fatal(err)
	}
	x.s.controlHook = func(step string) error {
		if step != "after_staging_pin" {
			return nil
		}
		// The real directory is already pinned; swap the path under it.
		if err := os.Rename(upload, upload+".real"); err != nil {
			return err
		}
		return os.Symlink("victim", upload)
	}
	_, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, x.d.Assignment.Identity.AttemptID, begin)
	x.s.controlHook = nil
	if _, statErr := os.Stat(retained); statErr != nil {
		t.Fatal("removal followed the replaced path", err, statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(victim, id, "manifest.json")); statErr == nil {
		t.Fatal("manifest written through the replaced path")
	}
	if err != nil {
		t.Fatal("begin refused although the pinned directory was intact", err)
	}
	if _, statErr := os.Stat(filepath.Join(upload+".real", id, "manifest.json")); statErr != nil {
		t.Fatal("manifest not written through the pinned directory", statErr)
	}
	if err := os.Remove(upload); err != nil {
		t.Fatal(err)
	}
}

// P1-2: the store lock is not held across sink I/O; only the attempt's own
// append lock is, so other store work proceeds and same-attempt appends queue.
func TestAppendStreamReleasesStoreLockDuringIO(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.lease(t)
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	attempt := x.d.Assignment.Identity.AttemptID
	records := spooledRecords(t, x.d.Assignment.Identity, "a", "b")
	other := make(chan error, 1)
	same := make(chan error, 1)
	var fired atomic.Bool
	x.s.controlHook = func(step string) error {
		if step != "append_stream_io" || !fired.CompareAndSwap(false, true) {
			return nil
		}
		go func() { _, err := x.s.ExecutionSession(ctx, x.runner, x.session.SessionID); other <- err }()
		go func() {
			_, err := x.s.AppendStream(ctx, x.runner, x.session.SessionID, attempt, records[1:])
			same <- err
		}()
		select {
		case err := <-other:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("store lock held across stream I/O")
		}
		select {
		case <-same:
			t.Error("same-attempt append ran during another append")
		case <-time.After(300 * time.Millisecond):
		}
		return nil
	}
	ack, err := x.s.AppendStream(ctx, x.runner, x.session.SessionID, attempt, records[:1])
	if err != nil || ack.Through != 1 {
		t.Fatal(ack, err)
	}
	select {
	case err := <-same:
		if err != nil {
			t.Fatal("queued append failed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued append never ran")
	}
	x.s.controlHook = nil
	if watermark, err := x.s.Streams().Watermark(attempt); err != nil || watermark.Through != 2 {
		t.Fatal(watermark, err)
	}
}

// P2-1: the exported sink set is read-only; only the store's serialized append
// path writes.
func TestStreamsViewIsReadOnly(t *testing.T) {
	x := executionFixtureFor(t, nil)
	view := x.s.Streams()
	if _, writable := any(view).(interface {
		Receive(string, p.Identity, []runstream.Record) (runstream.Ack, error)
	}); writable {
		t.Fatal("Streams exposes a writer")
	}
	var _ runstream.SinkReader = view
	if w, err := view.Watermark(x.d.Assignment.Identity.AttemptID); err != nil || w.Through != 0 {
		t.Fatal(w, err)
	}
}

// P2-4: a runner-local cancel latch carries the attempt, actor and cause the
// reconcile lane's clearing targets. Release alone retains suppression; an
// immutable clearance then admits a fresh attempt without deleting the latch.
func TestRunnerLocalLatchIsTargetable(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.lease(t)
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop", Cause: "runner_shutdown"})
	attempt := x.d.Assignment.Identity.AttemptID
	stopID := execwire.LocalStopID(attempt, "runner_shutdown")
	reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, stopID, "quiescent"), quiescent())
	if err != nil || !reply.Released {
		t.Fatal(reply, err)
	}
	var kind, task, target string
	if err := x.s.db.QueryRow("SELECT kind,task_id,attempt_id FROM control_stops WHERE id=?", stopID).Scan(&kind, &task, &target); err != nil || kind != string(c.CancelAttempt) || task != x.d.Request.TaskID || target != attempt {
		t.Fatal("latch not targetable", kind, task, target, err)
	}
	view, err := x.s.StopStatus(ctx, stopID, attempt)
	if err != nil || view.Receipt.Actor != "runner-local" || view.Receipt.Request.Cause != "runner_shutdown" {
		t.Fatal(view, err)
	}
	x.request.ID = newID()
	_, err = x.s.Dispatch(ctx, x.request)
	requireReason(t, err, "stop_latched")
	t.Run("cleared", func(t *testing.T) {
		cleared, err := x.s.ClearLatches(ctx, x.d.Request.TaskID)
		if err != nil || len(cleared) != 1 || cleared[0].StopID != stopID {
			t.Fatal(cleared, err)
		}
		next, err := x.s.Dispatch(ctx, x.request)
		if err != nil || next.Assignment.Identity.Epoch != 2 || next.Assignment.Identity.AttemptID == attempt {
			t.Fatal("cleared runner-local latch did not admit a new attempt", next, err)
		}
		rowCount(t, x.s, "control_stops", 1)
		rowCount(t, x.s, "dispatch_releases", 1)
	})
}

// P2-5: a manifest that claims success over a non-zero exit finalizes failed
// and never becomes the head.
func TestFinalizeRequiresExitZeroForSuccess(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.lease(t)
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	exit := exitEvidence(0)
	exit.Code = 1
	x.propose(t, p.ResultPending, exit)
	_, custody := x.upload(t, "succeeded", map[string]string{"a.txt": "bytes"})
	completion := x.completion(t, custody.Receipt.ReceiptID, 0)
	completion.Exit.Code = 1
	reply := x.finalize(t, completion)
	if reply.Outcome != "failed" || !reply.Released {
		t.Fatal("non-zero exit finalized as", reply)
	}
	if state, _ := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID); state != p.Failed || taskRow(t, x.s, x.d.Request.TaskID) != p.TaskReconciling {
		t.Fatal(state)
	}
	rowCount(t, x.s, "artifact_result_heads", 0)
}

// P2-6, P2-7, P2-8: revisions must carry the same measurement, are validated
// against the strongest accepted revision, and the effective termination is
// readable even though the first control observation is immutable.
func TestTerminationRevisionsCompareFullEvidenceAgainstTheStrongest(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	attempt := x.d.Assignment.Identity.AttemptID
	first := terminatedFor(&x, stop.ID, "unknown")
	if reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, first, BoundaryState{Reservations: 1, InFlight: 1}); err != nil || reply.Released {
		t.Fatal(reply, err)
	}
	// P2-6: same terminated fields, different measurement.
	retimed := first
	retimed.Terminated.MessageID = newID()
	retimed.Measurement.RequestedAt = first.Measurement.RequestedAt.Add(time.Second)
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, retimed, quiescent()); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed measurement accepted as a revision", err)
	}
	escalated := first
	escalated.Terminated.MessageID = newID()
	escalated.Measurement.Escalated = true
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, escalated, quiescent()); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed escalation accepted as a revision", err)
	}
	settled := first
	settled.Terminated.MessageID = newID()
	settled.Terminated.RemoteWork = "quiescent"
	if reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, settled, quiescent()); err != nil || !reply.Released {
		t.Fatal(reply, err)
	}
	// P2-7: a later report weaker than the strongest accepted revision conflicts.
	weaker := first
	weaker.Terminated.MessageID = newID()
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, weaker, quiescent()); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("regression below the strongest revision accepted", err)
	}
	// P2-8: the effective view is the settled revision; the first observation
	// row remains unchanged history while both public views show settled evidence.
	effective, err := x.s.TerminationView(ctx, stop.ID, attempt)
	if err != nil || effective.Terminated.RemoteWork != "quiescent" || effective.Terminated.MessageID != settled.Terminated.MessageID {
		t.Fatal(effective, err)
	}
	view, err := x.s.StopStatus(ctx, stop.ID, attempt)
	if err != nil || view.RemoteWork != "quiescent" || view.Evidence == nil || view.Evidence.Terminated.MessageID != settled.Terminated.MessageID {
		t.Fatal("stop view did not expose strongest evidence", view, err)
	}
	var body string
	if err := x.s.db.QueryRow("SELECT body FROM control_observations WHERE stop_id=? AND attempt_id=?", stop.ID, attempt).Scan(&body); err != nil {
		t.Fatal(err)
	}
	var retained c.Evidence
	if err := decodeControl(body, &retained); err != nil || retained.Terminated.RemoteWork != "unknown" || retained.Terminated.MessageID != first.Terminated.MessageID {
		t.Fatal("first observation rewritten", retained, err)
	}
	if _, err := x.s.TerminationView(ctx, newID(), attempt); err == nil {
		t.Fatal("view of an unknown stop")
	}
}

// P2-12: after finalization a durably appended batch replays its
// acknowledgement; only new records are refused.
func TestAppendStreamReplaysDuplicatesAfterFinalize(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t, "one", "two")
	attempt := x.d.Assignment.Identity.AttemptID
	_, custody := x.upload(t, "succeeded", map[string]string{"a.txt": "bytes"})
	x.finalize(t, x.completion(t, custody.Receipt.ReceiptID, 2))
	records := spooledRecords(t, x.d.Assignment.Identity, "one", "two", "three")
	ack, err := x.s.AppendStream(ctx, x.runner, x.session.SessionID, attempt, records[:2])
	if err != nil || !ack.Duplicate || ack.Through != 2 {
		t.Fatal("lost ack not replayed after finalization", ack, err)
	}
	for _, batch := range [][]runstream.Record{records[2:], records[1:]} {
		if _, err := x.s.AppendStream(ctx, x.runner, x.session.SessionID, attempt, batch); !errors.Is(err, p.StaleAttempt) {
			t.Fatal("new record appended after finalization", err)
		}
	}
	if watermark, err := x.s.Streams().Watermark(attempt); err != nil || watermark.Through != 2 {
		t.Fatal(watermark, err)
	}
}

// P2-13: a retained upload session replays by message id after the attempt
// has left result_pending; only a new session is refused.
func TestBeginUploadReplaysAfterFinalization(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	attempt := x.d.Assignment.Identity.AttemptID
	begin, blobs := x.candidate(t, "succeeded", map[string]string{"a.txt": "bytes"})
	session, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, begin)
	if err != nil {
		t.Fatal(err)
	}
	digest := session.Missing[0].SHA256
	if _, err := x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, digest, bytes.NewReader(blobs[digest]), -1); err != nil {
		t.Fatal(err)
	}
	custody, err := x.s.CommitUpload(ctx, x.runner, x.session.SessionID, session.UploadID, newID())
	if err != nil {
		t.Fatal(err)
	}
	x.finalize(t, x.completion(t, custody.Receipt.ReceiptID, 0))
	again, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, begin)
	if err != nil || again.UploadID != session.UploadID || len(again.Missing) != 0 {
		t.Fatal("retained upload not replayed after finalization", again, err)
	}
	fresh := begin
	fresh.MessageID = newID()
	if _, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, fresh); !errors.Is(err, p.ReconciliationRequired) {
		t.Fatal("new upload admitted on a terminal attempt", err)
	}
}
