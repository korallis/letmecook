// Adopted from the second cross-model review of the execution channel (S1,
// #115): each case reproduced a defect on the reviewed head and must keep passing.

package store

import (
	"errors"
	c "github.com/korallis/letmecook/internal/control"
	p "github.com/korallis/letmecook/schemas/execution"
	"os"
	"path/filepath"
	"testing"
)

func TestReview2TerminationCanSettleBoundary(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	evidence := terminatedFor(&x, stop.ID, "quiescent")
	first, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, BoundaryState{Reservations: 2, TerminalReceipts: 1, InFlight: 1})
	if err != nil || first.Released {
		t.Fatal(first, err)
	}
	evidence.Terminated.MessageID = newID()
	next, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, quiescent())
	if err != nil || !next.Released {
		t.Fatalf("new message cannot settle boundary: first=%+v second=%+v err=%v", first, next, err)
	}
}

func TestReview2BeginUploadInternalSymlinkKeepsTarget(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	begin, _ := x.candidate(t, "succeeded", map[string]string{"a.txt": "bytes"})
	id := dispatchID(begin.MessageID, "upload")
	target := filepath.Join(x.artifacts, "other-area")
	victim := filepath.Join(target, id, "retained")
	if err := os.MkdirAll(filepath.Dir(victim), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("retain me"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other-area", filepath.Join(x.artifacts, "upload")); err != nil {
		t.Fatal(err)
	}
	_, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, x.d.Assignment.Identity.AttemptID, begin)
	if err == nil {
		t.Fatal("symlink accepted")
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Fatalf("refused upload deleted through internal symlink: begin=%v stat=%v", err, statErr)
	}
}

func TestReview2UnknownTerminationWrongStopCannotRelease(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	unknown := terminatedFor(&x, stop.ID, "unknown")
	unknown.Terminated.ConfirmedProcess = "unknown"
	unknown.Measurement.ObservedAt, unknown.Measurement.AckToObservedNS = nil, nil
	reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, unknown, quiescent())
	if err != nil || reply.Released {
		t.Fatal(reply, err)
	}
	confirmed := terminatedFor(&x, newID(), "quiescent")
	reply, err = x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, confirmed, quiescent())
	if !errors.Is(err, p.ReconciliationRequired) || reply.Released {
		t.Fatalf("foreign stop released: %+v %v", reply, err)
	}
	rowCount(t, x.s, "dispatch_releases", 0)
	confirmed.Terminated.MessageID, confirmed.Terminated.StopID = newID(), stop.ID
	reply, err = x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, confirmed, quiescent())
	if err != nil || !reply.Released {
		t.Fatal(reply, err)
	}
}

func TestReview2FinalizeCannotRaceAlreadyAdmittedStream(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t, "first")
	_, custody := x.upload(t, "succeeded", map[string]string{"a.txt": "bytes"})
	attempt := x.d.Assignment.Identity.AttemptID
	identity, err := x.s.StreamIdentity(ctx, x.runner, x.session.SessionID, attempt)
	if err != nil {
		t.Fatal(err)
	}
	records := spooledRecords(t, identity, "first", "trailing")
	completion := x.completion(t, custody.Receipt.ReceiptID, 1)
	through := int64(0)
	x.s.controlHook = func(step string) error {
		if step == "before_finalize_commit" {
			ack, err := x.s.Streams().Receive(attempt, identity, records[1:])
			through = ack.Through
			return err
		}
		return nil
	}
	reply, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, attempt, completion)
	if err == nil && reply.Released && through == 2 {
		t.Fatalf("finalize released prefix 1 after admitting and durably acknowledging watermark %d: %+v", through, reply)
	}
}
