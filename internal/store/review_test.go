package store

// Regressions for the cross-model review of the execution channel: each case
// failed against the reviewed head (16f3248) and passes with its fix.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
)

// P1: retained custody is matched by the full identity and manifest tuple; a
// manifest id bound to another attempt can never replay that attempt's receipt.
func TestCommitUploadReplayIsBoundToTheAttemptIdentity(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	_, first := x.upload(t, "succeeded", map[string]string{"greeting.txt": "hello, gaffer\n"})
	x.finalize(t, x.completion(t, first.Receipt.ReceiptID, 0))
	x.redispatch(t)
	x.run(t)
	begin, blobs := x.candidate(t, "succeeded", map[string]string{"greeting.txt": "hello again\n"})
	begin.Result.Manifest.ManifestID = first.Receipt.Manifest.ManifestID
	attempt := x.d.Assignment.Identity.AttemptID
	session, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, begin)
	if err != nil || len(session.Missing) != 1 {
		t.Fatal(session, err)
	}
	// Without the bytes and with them, the foreign manifest id never commits.
	for range 2 {
		reply, err := x.s.CommitUpload(ctx, x.runner, x.session.SessionID, session.UploadID, newID())
		if !errors.Is(err, p.IdentityConflict) || reply.Receipt.ReceiptID != "" {
			t.Fatal("foreign custody replayed for an unuploaded attempt", reply, err)
		}
		digest := session.Missing[0].SHA256
		if _, err := x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, digest, bytes.NewReader(blobs[digest]), -1); err != nil {
			t.Fatal(err)
		}
	}
	rowCount(t, x.s, "artifact_results", 1)
	if state, _ := attemptRow(t, x.s, attempt); state != p.ResultPending {
		t.Fatal(state)
	}
}

// P1: the finalize stream claim must equal the exit observation's watermark and
// the sink's; a shorter prefix or later records never finalize.
func TestFinalizeRequiresTheExitWatermarkExactly(t *testing.T) {
	x := executionFixtureFor(t, nil)
	attempt := x.d.Assignment.Identity.AttemptID
	x.run(t, "one", "two")
	_, custody := x.upload(t, "succeeded", map[string]string{"greeting.txt": "hello, gaffer\n"})
	for _, through := range []int64{0, 1} {
		if _, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, attempt, x.completion(t, custody.Receipt.ReceiptID, through)); !errors.Is(err, p.ReconciliationRequired) {
			t.Fatalf("prefix claim %d finalized: %v", through, err)
		}
	}
	records := spooledRecords(t, x.d.Assignment.Identity, "one", "two", "late")
	if _, err := x.s.sinks().Receive(attempt, x.d.Assignment.Identity, records[2:]); err != nil {
		t.Fatal(err)
	}
	for _, through := range []int64{2, 3} {
		if _, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, attempt, x.completion(t, custody.Receipt.ReceiptID, through)); !errors.Is(err, p.ReconciliationRequired) {
			t.Fatalf("claim %d finalized although the exit and the sink disagree: %v", through, err)
		}
	}
	if state, _ := attemptRow(t, x.s, attempt); state != p.ResultPending {
		t.Fatal(state)
	}
}

// P1: one replay rule for every runner request keyed by its message id: the
// same request returns the same outcome bytes; a changed request is refused
// and never applied.
func TestRunnerRequestsReplayUniformly(t *testing.T) {
	x := executionFixtureFor(t, nil)
	attempt := x.d.Assignment.Identity.AttemptID
	lease := x.lease(t)
	starting := x.proposal(t, p.Starting)
	first, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, starting, launchIntent(lease.Request.Nonce))
	if err != nil {
		t.Fatal(err)
	}
	again, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, starting, launchIntent(lease.Request.Nonce))
	if err != nil || !reflect.DeepEqual(again, first) {
		t.Fatal("identical replay changed the outcome", again, err)
	}
	// Same message id, changed proposal: refused, nothing applied.
	changed := x.proposal(t, p.Running)
	changed.MessageID = starting.MessageID
	if _, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, changed, launchedEvidence()); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed proposal under the same message id applied", err)
	}
	if state, revision := attemptRow(t, x.s, attempt); state != p.Starting || revision != 2 {
		t.Fatal("changed replay advanced the attempt", state, revision)
	}
	x.propose(t, p.Running, launchedEvidence())
	x.propose(t, p.ResultPending, exitEvidence(0))
	// Usage: the same message id cannot rewrite what it recorded, and a terminal
	// receipt is immutable per request id even under a new message id.
	usage := UsageReport{Version: execwire.Version, MessageID: newID(), Identity: x.d.Assignment.Identity, Receipts: []UsageReceipt{{RequestID: "r", Terminal: true, PromptTokens: 10, CompletionTokens: 20}}}
	if err := x.s.RecordUsage(ctx, x.runner, x.session.SessionID, usage); err != nil {
		t.Fatal(err)
	}
	tampered := usage
	tampered.Receipts = []UsageReceipt{{RequestID: "r", Terminal: true, PromptTokens: 1, CompletionTokens: 1}}
	if err := x.s.RecordUsage(ctx, x.runner, x.session.SessionID, tampered); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed usage under the same message id applied", err)
	}
	tampered.MessageID = newID()
	if err := x.s.RecordUsage(ctx, x.runner, x.session.SessionID, tampered); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("terminal receipt rewritten under a new message id", err)
	}
	got, err := x.s.AttemptUsage(ctx, attempt)
	if err != nil || len(got) != 1 || got[0].PromptTokens != 10 {
		t.Fatal("terminal tokens overwritten", got, err)
	}
	if err := x.s.RecordUsage(ctx, x.runner, x.session.SessionID, usage); err != nil {
		t.Fatal("identical usage replay refused", err)
	}
	// Finalize: a changed completion under the same message id is refused even
	// after the attempt is terminal; the identical one replays the same bytes,
	// and a new message id replays only the retained attestation.
	_, custody := x.upload(t, "succeeded", map[string]string{"greeting.txt": "hello, gaffer\n"})
	completion := x.completion(t, custody.Receipt.ReceiptID, 0)
	reply := x.finalize(t, completion)
	tampered2 := completion
	tampered2.Exit.Code = 7
	if _, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, attempt, tampered2); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed completion under the same message id succeeded", err)
	}
	tampered2.MessageID = newID()
	if _, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, attempt, tampered2); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed completion under a new message id succeeded", err)
	}
	replay, _ := json.Marshal(x.finalize(t, completion))
	want, _ := json.Marshal(reply)
	if !bytes.Equal(replay, want) {
		t.Fatal("finalize replay bytes differ")
	}
	// Termination: the first report under a message id decides; a changed
	// boundary under the same id is refused and never flips released.
	y := executionFixtureFor(t, nil)
	y.run(t)
	stop := stopRequest(c.CancelAttempt, y.d)
	if _, err := y.s.RequestStop(ctx, y.owner, stop); err != nil {
		t.Fatal(err)
	}
	y.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	evidence := terminatedFor(&y, stop.ID, "quiescent")
	busy := BoundaryState{Reservations: 2, TerminalReceipts: 1, InFlight: 1}
	observed, err := y.s.ReportTermination(ctx, y.runner, y.session.SessionID, p.FencedVersion, evidence, busy)
	if err != nil || observed.Released {
		t.Fatal(observed, err)
	}
	if _, err := y.s.ReportTermination(ctx, y.runner, y.session.SessionID, p.FencedVersion, evidence, quiescent()); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed boundary under the same message id accepted", err)
	}
	if state, _ := attemptRow(t, y.s, y.d.Assignment.Identity.AttemptID); state != p.Stopping {
		t.Fatal("changed replay released the attempt", state)
	}
	rowCount(t, y.s, "dispatch_releases", 0)
	if same, err := y.s.ReportTermination(ctx, y.runner, y.session.SessionID, p.FencedVersion, evidence, busy); err != nil || !reflect.DeepEqual(same, observed) {
		t.Fatal("identical termination replay changed", same, err)
	}
}

// P1: a stale-generation proposal, lease request, termination report or refusal
// never changes state, even on the stopping edge.
func TestStaleGenerationNeverChangesState(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.run(t)
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	sqlExec(t, x.s, "UPDATE metadata SET generation='"+newID()+"' WHERE singleton=1")
	reopen(t, &x)
	x.session = sessionFor(t, x.dispatchFixture)
	// Snapshots refuse to validate events of another generation, so history is
	// compared row by row.
	stateBefore, revisionBefore := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID)
	var eventsBefore, observationsBefore int
	if err := x.s.db.QueryRow("SELECT (SELECT count(*) FROM events),(SELECT count(*) FROM runtime_observations)").Scan(&eventsBefore, &observationsBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, p.Stopping), RuntimeEvidence{Kind: "stop"}); !errors.Is(err, p.StaleGeneration) {
		t.Fatal("stale-generation stop proposal", err)
	}
	if _, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, x.leaseRequest()); !errors.Is(err, p.StaleGeneration) {
		t.Fatal("stale-generation lease", err)
	}
	if _, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, lease.Request); !errors.Is(err, p.StaleGeneration) {
		t.Fatal("stale-generation lease replay", err)
	}
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, stop.ID, "quiescent"), quiescent()); !errors.Is(err, p.StaleGeneration) {
		t.Fatal("stale-generation termination", err)
	}
	refusal := p.Message{Version: p.FencedVersion, Kind: "refuse", MessageID: newID(), Identity: x.d.Assignment.Identity, InReplyTo: x.d.Assignment.MessageID, Reason: p.ReconciliationRequired}
	if err := x.s.RecordRefusal(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.d.ID, refusal); !errors.Is(err, p.StaleGeneration) {
		t.Fatal("stale-generation refusal", err)
	}
	stateAfter, revisionAfter := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID)
	var eventsAfter, observationsAfter int
	if err := x.s.db.QueryRow("SELECT (SELECT count(*) FROM events),(SELECT count(*) FROM runtime_observations)").Scan(&eventsAfter, &observationsAfter); err != nil {
		t.Fatal(err)
	}
	if stateAfter != stateBefore || revisionAfter != revisionBefore || eventsAfter != eventsBefore || observationsAfter != observationsBefore || taskRow(t, x.s, x.d.Request.TaskID) != p.TaskReconciling {
		t.Fatal("stale-generation request changed history", stateAfter, revisionAfter, eventsAfter, observationsAfter)
	}
	rowCount(t, x.s, "control_leases", 1)
	rowCount(t, x.s, "control_observations", 0)
	rowCount(t, x.s, "control_fenced", 2)
}

// P1: a session binds the dispatches it may see or acknowledge to its runner
// boot and the eligibility revision its hello cited.
func TestSessionBindsDispatchesToBootAndEligibility(t *testing.T) {
	x := executionFixtureFor(t, nil)
	if err := x.session.Binds(x.d); err != nil {
		t.Fatal(err)
	}
	if x.session.EligibilityID != x.facts.ID || x.session.EligibilityRevision != x.facts.Revision {
		t.Fatal(x.session)
	}
	loaded, err := x.s.ExecutionSession(ctx, x.runner, x.session.SessionID)
	if err != nil || loaded.EligibilityID != x.facts.ID || loaded.EligibilityRevision != x.facts.Revision || loaded.RunnerBoot != x.facts.RunnerBoot {
		t.Fatal("binding not read back from the session row", loaded, err)
	}
	restarted := helloFor(x.dispatchFixture)
	restarted.RunnerBoot = newID()
	rebooted, err := x.s.RunnerSession(ctx, x.runner, restarted)
	if err != nil {
		t.Fatal(err)
	}
	if err := rebooted.Binds(x.d); !errors.Is(err, p.BootMismatch) {
		t.Fatal("old-boot dispatch bound to a new boot", err)
	}
	other := helloFor(x.dispatchFixture)
	other.EligibilityID = "fixture-pair-b"
	foreign, err := x.s.RunnerSession(ctx, x.runner, other)
	if err != nil {
		t.Fatal(err)
	}
	requireReason(t, foreign.Binds(x.d), "reconciliation_required")
	stale := helloFor(x.dispatchFixture)
	stale.EligibilityRevision = 2
	older, err := x.s.RunnerSession(ctx, x.runner, stale)
	if err != nil {
		t.Fatal(err)
	}
	requireReason(t, older.Binds(x.d), "reconciliation_required")
}

// P2: staging directories that are symbolic links are refused at every step;
// no byte lands outside <artifacts-dir>/upload.
func TestUploadStagingRefusesSymlinks(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	begin, blobs := x.candidate(t, "succeeded", map[string]string{"greeting.txt": "hello, gaffer\n"})
	attempt := x.d.Assignment.Identity.AttemptID
	session, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, begin)
	if err != nil {
		t.Fatal(err)
	}
	digest := session.Missing[0].SHA256
	outside := t.TempDir()
	dir := x.s.uploadDir(session.UploadID)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, digest, bytes.NewReader(blobs[digest]), -1); err == nil {
		t.Fatal("blob written through a symlinked staging directory")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatal("bytes escaped staging", entries, err)
	}
	if _, err := x.s.CommitUpload(ctx, x.runner, x.session.SessionID, session.UploadID, newID()); err == nil {
		t.Fatal("committed through a symlinked staging directory")
	}
	// A staged blob replaced by a symlink is not a duplicate and is not verified.
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "victim"), blobs[digest], 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "victim"), filepath.Join(dir, digest)); err != nil {
		t.Fatal(err)
	}
	sqlExec(t, x.s, "UPDATE upload_blobs SET state='staged' WHERE upload_id='"+session.UploadID+"'")
	if _, err := x.s.CommitUpload(ctx, x.runner, x.session.SessionID, session.UploadID, newID()); err == nil {
		t.Fatal("committed a symlinked staged blob")
	}
	// The upload root itself is checked the same way: abandon the open session
	// so the retry must create a new staging directory under the root.
	sqlExec(t, x.s, "UPDATE upload_sessions SET state='abandoned' WHERE id='"+session.UploadID+"'")
	root := filepath.Join(x.artifacts, "upload")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	retry := begin
	retry.MessageID = newID()
	if _, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, retry); err == nil {
		t.Fatal("upload began under a symlinked upload root")
	}
	if _, err := os.Lstat(filepath.Join(outside, "manifest.json")); err == nil {
		t.Fatal("manifest escaped staging")
	}
}

// P2: a same-nonce lease replay is answered from the retained reply before any
// timing check and never extended; a changed request under it conflicts.
func TestLeaseReplayByNonceBeforeTiming(t *testing.T) {
	x := executionFixtureFor(t, nil)
	request := x.leaseRequest()
	lease, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, request)
	if err != nil {
		t.Fatal(err)
	}
	x.s.controlNow = func() time.Time { return time.Now().Add(14 * time.Second) }
	again, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, request)
	if err != nil || !reflect.DeepEqual(again, lease) {
		t.Fatal("late replay of the same nonce refused", again, err)
	}
	rowCount(t, x.s, "control_fenced", 0)
	rowCount(t, x.s, "control_leases", 1)
	changed := request
	changed.MessageID = newID()
	if _, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, changed); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed request under a retained nonce accepted", err)
	}
}

// Commit-before-reply for the remaining execution-channel writes: refusal,
// termination, usage and the commit-upload session mark.
func TestExecutionChannelCommitHooks(t *testing.T) {
	inject := func(x *executionFixture, step string) {
		x.s.controlHook = func(got string) error {
			if got == step {
				return errors.New("crash")
			}
			return nil
		}
	}
	t.Run("refusal", func(t *testing.T) {
		f := dispatchFixtureFor(t, nil)
		d := admitted(t, f)
		x := executionFixture{dispatchFixture: f, d: d, session: sessionFor(t, f)}
		refusal := p.Message{Version: p.FencedVersion, Kind: "refuse", MessageID: newID(), Identity: d.Assignment.Identity, InReplyTo: d.Assignment.MessageID, Reason: p.ReconciliationRequired}
		for _, step := range []string{"before_refusal_commit", "after_refusal_commit"} {
			inject(&x, step)
			if err := x.s.RecordRefusal(ctx, x.runner, x.session.SessionID, p.FencedVersion, d.ID, refusal); err == nil {
				t.Fatal("reply after injected failure")
			}
			x.s.controlHook = nil
			rowCount(t, x.s, "runtime_observations WHERE kind='refused'", map[string]int{"before_refusal_commit": 0, "after_refusal_commit": 1}[step])
		}
		if err := x.s.RecordRefusal(ctx, x.runner, x.session.SessionID, p.FencedVersion, d.ID, refusal); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("termination", func(t *testing.T) {
		x := executionFixtureFor(t, nil)
		x.run(t)
		stop := stopRequest(c.CancelAttempt, x.d)
		if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
			t.Fatal(err)
		}
		x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
		evidence := terminatedFor(&x, stop.ID, "quiescent")
		for _, step := range []string{"before_termination_commit", "after_termination_commit"} {
			inject(&x, step)
			if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, quiescent()); err == nil {
				t.Fatal("reply after injected failure")
			}
			x.s.controlHook = nil
			want := map[string]int{"before_termination_commit": 0, "after_termination_commit": 1}[step]
			rowCount(t, x.s, "control_observations", want)
			rowCount(t, x.s, "dispatch_releases", want)
			// Three run proposals and the stop proposal already hold receipts.
			rowCount(t, x.s, "runtime_observations WHERE kind='receipt'", want+4)
		}
		reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, quiescent())
		if err != nil || !reply.Released {
			t.Fatal(reply, err)
		}
	})
	t.Run("usage", func(t *testing.T) {
		x := executionFixtureFor(t, nil)
		usage := UsageReport{Version: execwire.Version, MessageID: newID(), Identity: x.d.Assignment.Identity, Receipts: []UsageReceipt{{RequestID: "r", Terminal: true, PromptTokens: 3}}}
		for _, step := range []string{"before_usage_commit", "after_usage_commit"} {
			inject(&x, step)
			if err := x.s.RecordUsage(ctx, x.runner, x.session.SessionID, usage); err == nil {
				t.Fatal("reply after injected failure")
			}
			x.s.controlHook = nil
			rowCount(t, x.s, "attempt_usage", map[string]int{"before_usage_commit": 0, "after_usage_commit": 1}[step])
		}
		if err := x.s.RecordUsage(ctx, x.runner, x.session.SessionID, usage); err != nil {
			t.Fatal(err)
		}
	})
	for _, step := range []string{"before_commit_upload_commit", "after_commit_upload_commit"} {
		t.Run("commit mark "+step, func(t *testing.T) {
			x := executionFixtureFor(t, nil)
			x.run(t)
			begin, blobs := x.candidate(t, "succeeded", map[string]string{"greeting.txt": "hello, gaffer\n"})
			attempt := x.d.Assignment.Identity.AttemptID
			session, err := x.s.BeginUpload(ctx, x.runner, x.session.SessionID, attempt, begin)
			if err != nil {
				t.Fatal(err)
			}
			digest := session.Missing[0].SHA256
			if _, err := x.s.RecordUploadedBlob(ctx, x.runner, x.session.SessionID, session.UploadID, digest, bytes.NewReader(blobs[digest]), -1); err != nil {
				t.Fatal(err)
			}
			inject(&x, step)
			if _, err := x.s.CommitUpload(ctx, x.runner, x.session.SessionID, session.UploadID, newID()); err == nil {
				t.Fatal("reply after injected failure")
			}
			x.s.controlHook = nil
			// Custody committed before the session mark either way; the mark is
			// durable only past its own commit.
			rowCount(t, x.s, "artifact_results", 1)
			rowCount(t, x.s, "upload_sessions WHERE state='committed'", map[string]int{"before_commit_upload_commit": 0, "after_commit_upload_commit": 1}[step])
			reply, err := x.s.CommitUpload(ctx, x.runner, x.session.SessionID, session.UploadID, newID())
			if err != nil || p.CheckAck(begin.Result, reply.Ack, begin.Result.Identity, reply.Receipt) != p.OK {
				t.Fatal(reply, err)
			}
			rowCount(t, x.s, "upload_sessions WHERE state='committed'", 1)
			first, _ := json.Marshal(reply)
			again, err := x.s.CommitUpload(ctx, x.runner, x.session.SessionID, session.UploadID, newID())
			second, _ := json.Marshal(again)
			if err != nil || !bytes.Equal(first, second) {
				t.Fatal("commit replay differs across the session mark", err)
			}
		})
	}
}

// An unconfirmed containment report is retained, never promoted: no control
// observation, no release, the cancel stays pending; a later confirmed report
// under a new message id still releases.
func TestUnknownTerminationIsRetainedWithoutRelease(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	unknown := terminatedFor(&x, stop.ID, "unknown")
	unknown.Terminated.ConfirmedProcess = "unknown"
	// A confirmed-shaped measurement under an unconfirmed report is malformed.
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, unknown, quiescent()); !errors.Is(err, p.Malformed) {
		t.Fatal("unknown report with an observation timestamp accepted", err)
	}
	unknown.Measurement.ObservedAt, unknown.Measurement.AckToObservedNS = nil, nil
	reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, unknown, quiescent())
	if err != nil || reply.Outcome != "observed" || reply.Released {
		t.Fatal(reply, err)
	}
	rowCount(t, x.s, "control_observations", 0)
	rowCount(t, x.s, "dispatch_releases", 0)
	rowCount(t, x.s, "runtime_observations WHERE kind='terminated'", 1)
	if state, _ := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID); state != p.Stopping {
		t.Fatal(state)
	}
	if cancels, err := x.s.PendingCancels(ctx, x.runner, x.session.SessionID); err != nil || len(cancels) != 1 {
		t.Fatal("unconfirmed report cleared the pending cancel", cancels, err)
	}
	if again, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, unknown, quiescent()); err != nil || !reflect.DeepEqual(again, reply) {
		t.Fatal("identical unknown replay changed", again, err)
	}
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, unknown, BoundaryState{InFlight: 1}); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed boundary under the same message id accepted", err)
	}
	confirmed := terminatedFor(&x, stop.ID, "quiescent")
	reply, err = x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, confirmed, quiescent())
	if err != nil || !reply.Released {
		t.Fatal("confirmed report after an unknown one did not release", reply, err)
	}
	if state, _ := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID); state != p.Cancelled {
		t.Fatal(state)
	}
	rowCount(t, x.s, "control_observations", 1)
	rowCount(t, x.s, "runtime_observations WHERE kind='terminated'", 2)
}

// A runner-local policy refusal before acceptance is recorded like any other
// refusal; reconcile classifies it refused_before_accept.
func TestLocalPolicyRefusalIsRecorded(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	d := admitted(t, f)
	sess := sessionFor(t, f)
	refusal := p.Message{Version: p.FencedVersion, Kind: "refuse", MessageID: newID(), Identity: d.Assignment.Identity, InReplyTo: d.Assignment.MessageID, Reason: p.LocalPolicyDenied}
	for range 2 {
		if err := f.s.RecordRefusal(ctx, f.runner, sess.SessionID, p.FencedVersion, d.ID, refusal); err != nil {
			t.Fatal(err)
		}
	}
	rowCount(t, f.s, "runtime_observations WHERE kind='refused'", 1)
	var body string
	if err := f.s.db.QueryRow("SELECT body FROM runtime_observations WHERE kind='refused'").Scan(&body); err != nil || !strings.Contains(body, string(p.LocalPolicyDenied)) {
		t.Fatal("refusal reason not retained", body, err)
	}
	if state, revision := attemptRow(t, f.s, d.Assignment.Identity.AttemptID); state != p.Assigned || revision != 1 {
		t.Fatal("refusal changed the attempt", state, revision)
	}
	retained, err := f.s.Assignment(ctx, d.ID)
	if err != nil || retained.Acknowledged || retained.Released {
		t.Fatal("refusal acknowledged or released", err)
	}
}

// Runner-local stops: a terminated report whose stop id derives from the
// attempt and one of the local causes is latched under actor runner-local on
// first sight, observed, and released only under the confirmed-and-quiescent
// rule; an unconfirmed containment report latches without releasing and the
// stopping proposal is admitted once that target exists.
func TestRunnerLocalStopsLatchObserveAndRelease(t *testing.T) {
	for _, cause := range execwire.LocalStopCauses {
		t.Run(cause, func(t *testing.T) {
			x := executionFixtureFor(t, nil)
			attempt := x.d.Assignment.Identity.AttemptID
			lease := x.lease(t)
			x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
			x.propose(t, p.Running, launchedEvidence())
			stopID := execwire.LocalStopID(attempt, cause)
			if cause == "containment_unconfirmed" {
				// Unconfirmed containment: the report latches the local stop and is
				// retained; nothing releases until containment is confirmed.
				unknown := terminatedFor(&x, stopID, "unknown")
				unknown.Terminated.ConfirmedProcess = "unknown"
				unknown.Measurement.ObservedAt, unknown.Measurement.AckToObservedNS = nil, nil
				reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, unknown, BoundaryState{})
				if err != nil || reply.Outcome != "observed" || reply.Released {
					t.Fatal(reply, err)
				}
				rowCount(t, x.s, "control_targets", 1)
				rowCount(t, x.s, "control_observations", 0)
				rowCount(t, x.s, "dispatch_releases", 0)
				if cancels, err := x.s.PendingCancels(ctx, x.runner, x.session.SessionID); err != nil || len(cancels) != 1 || cancels[0].StopID != stopID {
					t.Fatal("local stop not pending", cancels, err)
				}
				// The derived target admits the stopping proposal without a nonce.
				x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
			} else {
				// Before any report there is no target: a bare stop is refused, an
				// unknown cause is malformed, and a local cause latches the derived
				// cancel under actor runner-local before the edge is applied.
				if _, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, p.Stopping), RuntimeEvidence{Kind: "stop"}); !errors.Is(err, p.ReconciliationRequired) {
					t.Fatal("stopping admitted without a target or the lease nonce", err)
				}
				if _, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, p.Stopping), RuntimeEvidence{Kind: "stop", Cause: "operator"}); !errors.Is(err, p.Malformed) {
					t.Fatal("non-local cause accepted", err)
				}
				rowCount(t, x.s, "control_stops", 0)
				x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop", Cause: cause})
				rowCount(t, x.s, "control_targets", 1)
				if cancels, err := x.s.PendingCancels(ctx, x.runner, x.session.SessionID); err != nil || len(cancels) != 1 || cancels[0].StopID != stopID {
					t.Fatal("proposal did not latch the local stop", cancels, err)
				}
				_ = lease
			}
			confirmed := terminatedFor(&x, stopID, "quiescent")
			reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, confirmed, quiescent())
			if err != nil || reply.Outcome != "observed" || !reply.Released {
				t.Fatal(reply, err)
			}
			view, err := x.s.StopStatus(ctx, stopID, attempt)
			if err != nil || view.Receipt.Actor != "runner-local" || view.Receipt.Request.Cause != cause || view.Receipt.Request.Kind != c.CancelAttempt || view.Status != c.TerminationObserved {
				t.Fatal(view, err)
			}
			if state, _ := attemptRow(t, x.s, attempt); state != p.Cancelled {
				t.Fatal(state)
			}
			var actor string
			if err := x.s.db.QueryRow("SELECT actor FROM dispatch_releases WHERE dispatch_id=?", x.d.ID).Scan(&actor); err != nil || actor != x.session.RunnerID {
				t.Fatal("release actor", actor, err)
			}
			if again, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, confirmed, quiescent()); err != nil || !reflect.DeepEqual(again, reply) {
				t.Fatal("identical replay changed", again, err)
			}
			if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, confirmed, BoundaryState{InFlight: 1}); !errors.Is(err, p.IdentityConflict) {
				t.Fatal("changed boundary under the same message id accepted", err)
			}
			rowCount(t, x.s, "control_stops", 1)
		})
	}
	// A local stop id derived for another attempt, or any other unknown id, has
	// no target and latches nothing.
	y := executionFixtureFor(t, nil)
	lease := y.run(t)
	y.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop", Nonce: lease.Request.Nonce})
	for _, stopID := range []string{execwire.LocalStopID(newID(), "runner_shutdown"), execwire.LocalStopID(y.d.Assignment.Identity.AttemptID, "not_a_cause"), newID()} {
		if _, err := y.s.ReportTermination(ctx, y.runner, y.session.SessionID, p.FencedVersion, terminatedFor(&y, stopID, "quiescent"), quiescent()); !errors.Is(err, p.ReconciliationRequired) {
			t.Fatalf("foreign stop id %s: %v", stopID, err)
		}
	}
	rowCount(t, y.s, "control_stops", 0)
	rowCount(t, y.s, "dispatch_releases", 0)
	if state, _ := attemptRow(t, y.s, y.d.Assignment.Identity.AttemptID); state != p.Stopping {
		t.Fatal(state)
	}
}

// Second review: a confirmed report whose boundary was unsettled can settle
// later under a new message id (an observation revision); the first control
// observation stays immutable, weaker or different evidence conflicts.
func TestTerminationRevisionSettlesUnderNewMessageID(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	first := terminatedFor(&x, stop.ID, "unknown")
	if reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, first, BoundaryState{Reservations: 1, InFlight: 1}); err != nil || reply.Released {
		t.Fatal(reply, err)
	}
	var retained string
	if err := x.s.db.QueryRow("SELECT body FROM control_observations WHERE stop_id=?", stop.ID).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	// Different evidence under a new id conflicts; weaker remote_work conflicts.
	other := terminatedFor(&x, stop.ID, "quiescent")
	other.Terminated.EvidenceDigest = strings.Repeat("f", 64)
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, other, quiescent()); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("different evidence accepted as a revision", err)
	}
	settled := first
	settled.Terminated.MessageID = newID()
	settled.Terminated.RemoteWork = "quiescent"
	reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, settled, quiescent())
	if err != nil || !reply.Released {
		t.Fatal("stronger revision did not release", reply, err)
	}
	var after string
	if err := x.s.db.QueryRow("SELECT body FROM control_observations WHERE stop_id=?", stop.ID).Scan(&after); err != nil || after != retained {
		t.Fatal("first observation rewritten", err)
	}
	rowCount(t, x.s, "control_observations", 1)
	rowCount(t, x.s, "runtime_observations WHERE kind='terminated'", 2)
	if state, _ := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID); state != p.Cancelled {
		t.Fatal(state)
	}
	// A first observation that already attested quiescent remote work can be
	// revised only by an equal report; unknown remote work is weaker and conflicts.
	y := executionFixtureFor(t, nil)
	y.run(t)
	ystop := stopRequest(c.CancelAttempt, y.d)
	if _, err := y.s.RequestStop(ctx, y.owner, ystop); err != nil {
		t.Fatal(err)
	}
	y.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	yfirst := terminatedFor(&y, ystop.ID, "quiescent")
	if reply, err := y.s.ReportTermination(ctx, y.runner, y.session.SessionID, p.FencedVersion, yfirst, BoundaryState{Reservations: 1, InFlight: 1}); err != nil || reply.Released {
		t.Fatal(reply, err)
	}
	weaker := yfirst
	weaker.Terminated.MessageID, weaker.Terminated.RemoteWork = newID(), "unknown"
	if _, err := y.s.ReportTermination(ctx, y.runner, y.session.SessionID, p.FencedVersion, weaker, quiescent()); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("weaker revision accepted", err)
	}
	rowCount(t, y.s, "dispatch_releases", 0)
	equal := yfirst
	equal.Terminated.MessageID = newID()
	if reply, err := y.s.ReportTermination(ctx, y.runner, y.session.SessionID, p.FencedVersion, equal, quiescent()); err != nil || !reply.Released {
		t.Fatal("equal revision with a settled boundary did not release", reply, err)
	}
}

// Second review: stream appends serialize with finalization on the store lock
// and finalization re-reads the sink before commit, so an append can never
// slip between the watermark check and the release.
func TestAppendStreamSerializesWithFinalize(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t, "first")
	attempt := x.d.Assignment.Identity.AttemptID
	_, custody := x.upload(t, "succeeded", map[string]string{"a.txt": "bytes"})
	records := spooledRecords(t, x.d.Assignment.Identity, "first", "trailing")
	late := make(chan error, 1)
	x.s.controlHook = func(step string) error {
		if step != "before_finalize_commit" {
			return nil
		}
		go func() {
			_, err := x.s.AppendStream(ctx, x.runner, x.session.SessionID, attempt, records[1:])
			late <- err
		}()
		select {
		case err := <-late:
			t.Errorf("append ran while finalization held the store: %v", err)
			late <- err
		case <-time.After(300 * time.Millisecond):
		}
		return nil
	}
	reply, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, attempt, x.completion(t, custody.Receipt.ReceiptID, 1))
	if err != nil || !reply.Released {
		t.Fatal(reply, err)
	}
	select {
	case err := <-late:
		if !errors.Is(err, p.StaleAttempt) {
			t.Fatal("append after finalization was not refused", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked append never completed")
	}
	// The queued append has finished reading the hook; only now may it change.
	x.s.controlHook = nil
	watermark, err := x.s.Streams().Watermark(attempt)
	if err != nil || watermark.Through != 1 {
		t.Fatal("finalized sink moved", watermark, err)
	}
	if _, err := x.s.AppendStream(ctx, x.runner, newID(), attempt, records[1:]); err == nil {
		t.Fatal("append without a session")
	}
}
