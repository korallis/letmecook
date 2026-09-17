package store

// Regression probes adopted verbatim from the S5 review of 2a220a8
// (/Users/leebarry/.claude/jobs/13633b12/tmp/s5-review/store_probe_test.go):
// each reproduced an unsafe release before its fix and passes once the store
// refuses it. Only gofmt, this header and the CompleteFinalization signature
// (the attestation argument added by the fix) changed.

import (
	"github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
	"testing"
)

func TestS5ReviewUnpostedReservation(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	_, custody := x.upload(t, "succeeded", map[string]string{"result.txt": "result\n"})
	completion := x.completion(t, custody.Receipt.ReceiptID, 0)
	// The supervisor knows one request is still upstream. Its reservation
	// has not reached the daemon's asynchronous /usage endpoint.
	completion.Boundary = BoundaryState{Reservations: 1, InFlight: 1}
	if _, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, x.d.Assignment.Identity.AttemptID, completion); err == nil {
		t.Fatal("fixture unexpectedly finalized nonquiescent boundary")
	}
	reopen(t, &x)
	reply, err := x.s.CompleteFinalization(ctx, x.d.Assignment.Identity.AttemptID, nil)
	if err == nil && reply.Released {
		t.Fatalf("UNSAFE: no boundary closure or usage rows; CompleteFinalization released as %s", reply.Outcome)
	}
}

func TestS5ReviewOldRunnerBootStoreBarrier(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.lease(t)
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	oldEvidence := terminatedFor(&x, execwire.ExpiryStopID(lease.Request.Nonce), "quiescent")
	hello := helloFor(x.dispatchFixture)
	hello.RunnerBoot = newID()
	sess, err := x.s.RunnerSession(ctx, x.runner, hello)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := x.s.ReportTermination(ctx, x.runner, sess.SessionID, p.FencedVersion, oldEvidence, quiescent())
	if err != nil || reply.Outcome != "retained" {
		t.Fatal(reply, err)
	}
	attempt := x.d.Assignment.Identity.AttemptID
	in, err := x.s.ReconciliationInputs(ctx, attempt)
	if err != nil || in.LeaseBarrierPassed {
		t.Fatal("fixture barrier", in.LeaseBarrierPassed, err)
	}
	sha := retainedSHA(t, x.s, attempt, oldEvidence.Terminated.MessageID)
	out, err := x.s.ReleaseAttempt(ctx, attempt, ReleaseBasis{Kind: BasisRetainedTermination, EvidenceSHA256: sha})
	if err == nil {
		t.Fatalf("UNSAFE: old-runner-boot retained evidence released before barrier: %s", out.Proof.To)
	}
}

func TestS5ReviewUnknownRecoveredByBareJournal(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.lease(t)
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	reopen(t, &x)
	attempt := x.d.Assignment.Identity.AttemptID
	// No exit, result artifact, receipt, stream digest or session binding.
	journal := execwire.Journal{DispatchID: x.d.ID, Identity: x.d.Assignment.Identity, RunnerBoot: x.facts.RunnerBoot, State: p.ResultPending}
	got, err := x.s.RecoverResultPending(ctx, attempt, journal)
	if err == nil {
		t.Fatalf("UNVERIFIED: bare journal moved attempt %s -> %s", got.From, got.To)
	}
}
