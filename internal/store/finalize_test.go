package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	r "github.com/korallis/letmecook/internal/review"
	v "github.com/korallis/letmecook/internal/verification"
	p "github.com/korallis/letmecook/schemas/execution"
)

func (x *executionFixture) completion(t *testing.T, receipt string, through int64) Completion {
	t.Helper()
	digest, err := x.s.Streams().Digest(x.d.Assignment.Identity.AttemptID, through)
	if err != nil {
		t.Fatal(err)
	}
	return Completion{Version: execwire.Version, MessageID: newID(), ReceiptID: receipt, Stream: StreamWatermark{Through: through, Digest: digest}, Exit: ExitRecord{Code: 0, PGID: 101, ObservedUnixNS: exitEvidence(0).ObservedUnixNS}, Boundary: quiescent()}
}

func (x *executionFixture) finalize(t *testing.T, completion Completion) FinalizeReply {
	t.Helper()
	reply, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, x.d.Assignment.Identity.AttemptID, completion)
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

// redispatch admits the next epoch of the fixture's task after a release.
func (x *executionFixture) redispatch(t *testing.T) {
	t.Helper()
	x.request.ID = newID()
	d, err := x.s.Dispatch(ctx, x.request)
	if err != nil {
		t.Fatal(err)
	}
	if err := x.s.AcknowledgeAssignment(ctx, x.runner, d.ID, acceptFor(x.dispatchFixture, d)); err != nil {
		t.Fatal(err)
	}
	x.d = d
}

func TestFinalizeRefusesIncompleteEvidence(t *testing.T) {
	x := executionFixtureFor(t, nil)
	attempt := x.d.Assignment.Identity.AttemptID
	x.run(t, "line one", "line two")
	_, reply := x.upload(t, "succeeded", map[string]string{"greeting.txt": "hello, gaffer\n"})
	receipt := reply.Receipt.ReceiptID
	good := x.completion(t, receipt, 2)
	refusals := map[string]func(*Completion){
		"unknown receipt":     func(c *Completion) { c.ReceiptID = newID() },
		"claim beyond sink":   func(c *Completion) { c.Stream.Through = 3 },
		"wrong stream digest": func(c *Completion) { c.Stream.Digest = strings.Repeat("0", 64) },
		"short claim wrong digest": func(c *Completion) {
			c.Stream.Through = 1
		},
		"wrong exit code":      func(c *Completion) { c.Exit.Code = 1 },
		"wrong pgid":           func(c *Completion) { c.Exit.PGID = 7 },
		"wrong exit time":      func(c *Completion) { c.Exit.ObservedUnixNS++ },
		"in flight":            func(c *Completion) { c.Boundary = BoundaryState{Reservations: 2, TerminalReceipts: 1, InFlight: 1} },
		"unsettled quiescence": func(c *Completion) { c.Boundary = BoundaryState{Reservations: 2, TerminalReceipts: 1, Quiescent: true} },
	}
	for name, mutate := range refusals {
		t.Run(name, func(t *testing.T) {
			bad := good
			mutate(&bad)
			_, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, attempt, bad)
			if !errors.Is(err, p.ReconciliationRequired) {
				t.Fatalf("want reconciliation_required, got %v", err)
			}
			if state, _ := attemptRow(t, x.s, attempt); state != p.ResultPending {
				t.Fatal("refusal changed the attempt", state)
			}
			rowCount(t, x.s, "dispatch_releases", 0)
			rowCount(t, x.s, "artifact_result_heads", 0)
		})
	}
	for name, bad := range map[string]Completion{
		"version":  {Version: "x", MessageID: good.MessageID, ReceiptID: receipt, Stream: good.Stream, Exit: good.Exit, Boundary: good.Boundary},
		"digest":   {Version: execwire.Version, MessageID: good.MessageID, ReceiptID: receipt, Stream: StreamWatermark{Through: 2, Digest: "short"}, Exit: good.Exit, Boundary: good.Boundary},
		"negative": {Version: execwire.Version, MessageID: good.MessageID, ReceiptID: receipt, Stream: StreamWatermark{Through: -1, Digest: good.Stream.Digest}, Exit: good.Exit, Boundary: good.Boundary},
	} {
		if _, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, attempt, bad); !errors.Is(err, p.Malformed) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	other, otherSession := secondRunner(t, x.dispatchFixture)
	_, err := x.s.FinalizeAttempt(ctx, other, otherSession.SessionID, attempt, good)
	requireReason(t, err, "runner_disabled")
	_, err = x.s.FinalizeAttempt(ctx, x.runner, newID(), attempt, good)
	requireReason(t, err, "session_stale")
	sqlExec(t, x.s, "UPDATE daemon_state SET paused=1")
	_, err = x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, attempt, good)
	requireReason(t, err, "paused")
	sqlExec(t, x.s, "UPDATE daemon_state SET paused=0")
	// Once a stop is latched the result is late: stop_latched, never finalized.
	if _, err := x.s.RequestStop(ctx, x.owner, stopRequest(c.CancelAttempt, x.d)); err != nil {
		t.Fatal(err)
	}
	_, err = x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, attempt, good)
	requireReason(t, err, "stop_latched")
	rowCount(t, x.s, "dispatch_releases", 0)
	if state, _ := attemptRow(t, x.s, attempt); state != p.ResultPending {
		t.Fatal(state)
	}
}

func TestFinalizeSucceedsMovesHeadAndSupersedesCandidates(t *testing.T) {
	x := executionFixtureFor(t, nil)
	task := x.d.Request.TaskID
	first := x.d.Assignment.Identity
	x.run(t, "first attempt output")
	_, custody := x.upload(t, "succeeded", map[string]string{"greeting.txt": "hello, gaffer\n"})
	completion := x.completion(t, custody.Receipt.ReceiptID, 1)
	// Commit before reply on the terminal transaction.
	for _, step := range []string{"before_finalize_commit", "after_finalize_commit"} {
		x.s.controlHook = func(got string) error {
			if got == step {
				return errors.New("crash")
			}
			return nil
		}
		if _, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, first.AttemptID, completion); err == nil {
			t.Fatal("reply after injected failure")
		}
		x.s.controlHook = nil
		state, _ := attemptRow(t, x.s, first.AttemptID)
		if step == "before_finalize_commit" {
			if state != p.ResultPending {
				t.Fatal("uncommitted finalization visible", state)
			}
			rowCount(t, x.s, "dispatch_releases", 0)
			rowCount(t, x.s, "artifact_result_heads", 0)
			continue
		}
		if state != p.Succeeded {
			t.Fatal("committed finalization lost", state)
		}
	}
	reply := x.finalize(t, completion)
	if reply.Outcome != "succeeded" || !reply.Released || taskRow(t, x.s, task) != p.TaskAwaitingReview {
		t.Fatal(reply)
	}
	replay := completion
	replay.MessageID = newID()
	if again := x.finalize(t, replay); !reflect.DeepEqual(again, reply) {
		t.Fatal("replay differs", again)
	}
	if _, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, first.AttemptID, x.completion(t, newID(), 1)); !errors.Is(err, p.InvalidTransition) {
		t.Fatal("terminal attempt finalized under another receipt", err)
	}
	head, err := x.s.ExecutionState(ctx, x.runner, x.session.SessionID, x.d.ID)
	if err != nil || head.Head.AttemptID != first.AttemptID || head.Head.Epoch != 1 || head.Head.ReceiptID != custody.Receipt.ReceiptID || head.ReceiptID != custody.Receipt.ReceiptID || head.AttemptState != p.Succeeded || !head.Released {
		t.Fatal(head, err)
	}
	var actor string
	if err := x.s.db.QueryRow("SELECT actor FROM dispatch_releases WHERE dispatch_id=?", x.d.ID).Scan(&actor); err != nil || actor != x.session.RunnerID {
		t.Fatal("release actor", actor, err)
	}
	var terminal int
	if err := x.s.db.QueryRow("SELECT count(*) FROM events WHERE message_id=?", dispatchID(custody.Receipt.ReceiptID, "terminal")).Scan(&terminal); err != nil || terminal != 1 {
		t.Fatal("terminal event", terminal, err)
	}
	rowCount(t, x.s, "runtime_observations WHERE kind='completion'", 1)
	// The first result becomes the verification candidate.
	firstManifest := custody.Receipt.Manifest
	candidate, err := x.s.SelectVerificationCandidate(ctx, "", firstManifest)
	if err != nil {
		t.Fatal(err)
	}
	report, decision := reviewFixture(t, x, candidate)
	mustSaveVerification(t, x.s, report)
	// A second attempt after the release moves the head; the old candidate is
	// then superseded for selection and acceptance alike.
	x.redispatch(t)
	second := x.d.Assignment.Identity
	if second.Epoch != 2 {
		t.Fatal(second)
	}
	x.run(t)
	_, next := x.upload(t, "succeeded", map[string]string{"greeting.txt": "hello again\n"})
	reply = x.finalize(t, x.completion(t, next.Receipt.ReceiptID, 0))
	if reply.Outcome != "succeeded" {
		t.Fatal(reply)
	}
	head, err = x.s.ExecutionState(ctx, x.runner, x.session.SessionID, x.d.ID)
	if err != nil || head.Head.AttemptID != second.AttemptID || head.Head.Epoch != 2 || head.Head.ReceiptID != next.Receipt.ReceiptID {
		t.Fatal("head did not move", head, err)
	}
	if err := x.s.RecordLocalDecision(ctx, decision); err == nil {
		t.Fatal("superseded candidate accepted")
	}
	if _, err := x.s.SelectVerificationCandidate(ctx, candidate.SelectionID, firstManifest); err == nil {
		t.Fatal("superseded manifest reselected")
	}
	if _, err := x.s.SelectVerificationCandidate(ctx, candidate.SelectionID, next.Receipt.Manifest); err != nil {
		t.Fatal("head manifest refused", err)
	}
	if _, err := x.s.db.Exec("DELETE FROM artifact_result_heads"); err == nil {
		t.Fatal("head deletable")
	}
	consistent(t, x.s)
}

func TestFinalizeFailedOutcomeParksTask(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	_, custody := x.upload(t, "failed", map[string]string{"recovery.log": "it broke\n"})
	reply := x.finalize(t, x.completion(t, custody.Receipt.ReceiptID, 0))
	if reply.Outcome != "failed" || !reply.Released || taskRow(t, x.s, x.d.Request.TaskID) != p.TaskReconciling {
		t.Fatal(reply)
	}
	if state, _ := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID); state != p.Failed {
		t.Fatal(state)
	}
	rowCount(t, x.s, "artifact_result_heads", 0)
	rowCount(t, x.s, "dispatch_releases", 1)
	x.redispatch(t)
	if x.d.Assignment.Identity.Epoch != 2 {
		t.Fatal("failure did not release capacity")
	}
}

func TestFinalizeSIGKILLAroundCommit(t *testing.T) {
	for _, step := range []string{"before_finalize_commit", "after_finalize_commit"} {
		t.Run(step, func(t *testing.T) {
			x := executionFixtureFor(t, nil)
			x.run(t)
			_, custody := x.upload(t, "succeeded", map[string]string{"greeting.txt": "hello, gaffer\n"})
			if err := x.s.Close(); err != nil {
				t.Fatal(err)
			}
			executionChild(t, x, "finalize:"+step, "GAFFER_EXECUTION_TEST_RECEIPT="+custody.Receipt.ReceiptID)
			s, err := Open(ctx, x.s.dir, x.artifacts)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			state, _ := attemptRow(t, s, x.d.Assignment.Identity.AttemptID)
			if step == "before_finalize_commit" {
				// The child's result_pending proposal committed; the finalization did
				// not, so this restart quarantined the attempt again.
				if state != p.Unknown {
					t.Fatal("uncommitted finalization survived SIGKILL", state)
				}
				rowCount(t, s, "dispatch_releases", 0)
				rowCount(t, s, "artifact_result_heads", 0)
				rowCount(t, s, "events WHERE message_id='"+dispatchID(custody.Receipt.ReceiptID, "terminal")+"'", 0)
				return
			}
			if state != p.Succeeded || taskRow(t, s, x.d.Request.TaskID) != p.TaskAwaitingReview {
				t.Fatal("committed finalization lost after SIGKILL", state)
			}
			rowCount(t, s, "dispatch_releases", 1)
			rowCount(t, s, "artifact_result_heads", 1)
			consistent(t, s)
		})
	}
}

// reviewFixture builds a validated report and accept decision for a candidate
// of the execution fixture's grant, mirroring verificationFixture.
func reviewFixture(t *testing.T, x executionFixture, candidate v.Candidate) (v.Report, r.Decision) {
	t.Helper()
	checks := v.TrustedChecks{ID: "test-approved", ApprovedBy: "operator", ApprovalRef: "test-approval", Checks: []v.Check{}}
	evidence := []v.Evidence{}
	for _, name := range []string{"first", "second"} {
		check := v.Check{Name: name, Argv: []string{"/bin/true"}, Env: map[string]string{"PATH": "/no-docker-in-test-environment"}, CWD: ".", Timeout: time.Second, Required: true}
		checks.Checks = append(checks.Checks, check)
		zero := 0
		at := time.Now().UTC()
		evidence = append(evidence, v.Evidence{ID: v.ID(), CandidateDigest: candidate.Manifest.SHA256, BaseCommit: candidate.BaseCommit, ProfileID: "test-only-unconfined", CheckName: name, Argv: check.Argv, EnvKeys: []string{"PATH"}, CWD: ".", ExitCode: &zero, Stdout: v.Stream{Prefix: []byte{}, SHA256: v.Digest(nil)}, Stderr: v.Stream{Prefix: []byte{}, SHA256: v.Digest(nil)}, Started: at, Ended: at, Verifier: "synthetic-store-fixture", Environment: v.Environment{OS: "synthetic", Arch: "synthetic", DockerBinary: "absent", DockerSearchPath: check.Env["PATH"], ExpectedConfinement: "test-only-unconfined", ObservedConfinement: "test-only-unconfined"}})
	}
	report := v.Report{ID: v.ID(), Candidate: candidate, Checks: checks, Evidence: evidence, Suggestions: []v.Suggestion{}, Limitations: []string{v.ContentLimitation, "synthetic persistence fixture; no actual checks executed"}}
	decision := r.Decision{ID: v.ID(), Candidate: candidate, VerificationID: report.ID, EvidenceIDs: r.EvidenceIDs(report), Grant: r.GrantReference{ID: x.grant.ID, Revision: x.grant.Revision}, Action: "accept", Actor: "operator", Coverage: []r.Coverage{{CriterionID: "c1", Status: "partial", EvidenceIDs: []string{evidence[0].ID}, Explanation: "synthetic metadata check"}, {CriterionID: "c2", Status: "not-covered", EvidenceIDs: []string{}, Explanation: "runtime unqualified"}}, Limitations: append([]string{}, report.Limitations...), At: time.Now().UTC()}
	return report, decision
}
