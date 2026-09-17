// Adopted from the cross-model review of the execution channel (S1, #115):
// each case reproduced a defect on the reviewed head and must keep passing.

package store

import (
	"errors"
	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewCustodyManifestCollision(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	_, first := x.upload(t, "succeeded", map[string]string{"a.txt": "old bytes"})
	x.finalize(t, x.completion(t, first.Receipt.ReceiptID, 0))
	x.redispatch(t)
	x.run(t)
	begin, _ := x.candidate(t, "succeeded", map[string]string{"b.txt": "NEW NEVER UPLOADED BYTES"})
	begin.Result.Manifest.ManifestID = first.Receipt.Manifest.ManifestID
	u, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, x.d.Assignment.Identity.AttemptID, begin)
	if err != nil {
		t.Fatal(err)
	}
	got, err := x.s.CommitUpload(ctx, x.runner, x.session.SessionID, u.UploadID, newID())
	if err == nil {
		t.Fatalf("acked unuploaded result: missing=%d same_receipt=%v protocol_check=%s receipt_identity_current_attempt=%v", len(u.Missing), got.Receipt.ReceiptID == first.Receipt.ReceiptID, p.CheckAck(begin.Result, got.Ack, begin.Result.Identity, got.Receipt), got.Receipt.Identity == begin.Result.Identity)
	}
}

func TestReviewFinalizeExitWatermarkMismatch(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t, "first", "second")
	_, r := x.upload(t, "succeeded", map[string]string{"a.txt": "ok"})
	completion := x.completion(t, r.Receipt.ReceiptID, 0)
	got, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, x.d.Assignment.Identity.AttemptID, completion)
	if err == nil {
		t.Fatalf("finalized despite exit.stream_through=2 and completion.stream.through=0: %+v", got)
	}
}

func TestReviewTerminationBoundaryIdentity(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	evidence := terminatedFor(&x, stop.ID, "quiescent")
	first, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, BoundaryState{Reservations: 2, TerminalReceipts: 1, InFlight: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, quiescent())
	if !errors.Is(err, p.IdentityConflict) {
		t.Fatalf("same message_id changed boundary: first=%+v second=%+v err=%v", first, second, err)
	}
}

func TestReviewUsageIdentity(t *testing.T) {
	x := executionFixtureFor(t, nil)
	u := UsageReport{Version: execwire.Version, MessageID: newID(), Identity: x.d.Assignment.Identity, Receipts: []UsageReceipt{{RequestID: "r", Terminal: true, PromptTokens: 100}}}
	if err := x.s.RecordUsage(ctx, x.runner, x.session.SessionID, u); err != nil {
		t.Fatal(err)
	}
	u.Receipts[0].PromptTokens = 1
	err := x.s.RecordUsage(ctx, x.runner, x.session.SessionID, u)
	got, _ := x.s.AttemptUsage(ctx, x.d.Assignment.Identity.AttemptID)
	if !errors.Is(err, p.IdentityConflict) {
		t.Fatalf("same message_id overwrote terminal usage: tokens=%d err=%v", got[0].PromptTokens, err)
	}
}

func TestReviewLeaseReplayAfterCutoff(t *testing.T) {
	x := executionFixtureFor(t, nil)
	r := x.leaseRequest()
	first, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, r)
	if err != nil {
		t.Fatal(err)
	}
	x.s.controlNow = func() time.Time { return time.UnixMilli(*r.SentMS + 14000) }
	second, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, r)
	if err != nil || second.DeadlineNS != first.DeadlineNS {
		t.Fatalf("retained nonce replay rejected before full lease deadline: %v", err)
	}
}

func TestReviewFinalizeChangedReplay(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	_, r := x.upload(t, "succeeded", map[string]string{"a.txt": "ok"})
	completion := x.completion(t, r.Receipt.ReceiptID, 0)
	x.finalize(t, completion)
	completion.Exit.Code = 999
	completion.Boundary = BoundaryState{InFlight: 99}
	got, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, x.d.Assignment.Identity.AttemptID, completion)
	if !errors.Is(err, p.IdentityConflict) {
		t.Fatalf("changed completion under same message_id acknowledged: %+v err=%v", got, err)
	}
}

func TestReviewStaleGenerationStopping(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.lease(t)
	sqlExec(t, x.s, "UPDATE metadata SET generation='"+newID()+"' WHERE singleton=1")
	reopen(t, &x)
	x.session = sessionFor(t, x.dispatchFixture)
	got, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, p.Stopping), RuntimeEvidence{Kind: "stop", Nonce: lease.Request.Nonce})
	if !errors.Is(err, p.StaleGeneration) {
		t.Fatalf("old-generation stopping mutates attempt: to=%s err=%v", got.To, err)
	}
}

func TestReviewUploadSymlinkEscape(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	begin, blobs := x.candidate(t, "succeeded", map[string]string{"a.txt": "outside"})
	u, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, x.d.Assignment.Identity.AttemptID, begin)
	if err != nil {
		t.Fatal(err)
	}
	dir := x.s.uploadDir(u.UploadID)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	digest := u.Missing[0].SHA256
	_, err = x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, u.UploadID, digest, strings.NewReader(string(blobs[digest])), -1)
	if _, statErr := os.Stat(filepath.Join(outside, digest)); err == nil && statErr == nil {
		t.Fatal("upload wrote and acknowledged a blob outside staging through a symlink")
	}
}
