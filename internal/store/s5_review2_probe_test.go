package store

// Regression probes adopted verbatim from the S5 delta review of c29707a
// (/Users/leebarry/.claude/jobs/13633b12/tmp/s5-review2/store_probe_test.go):
// each either reproduced an unsafe outcome before its fix or pins a rule the
// review confirmed, and passes once the store enforces it. Only gofmt and this
// header changed.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
)

func review2JournalHello(t *testing.T, x *executionFixture, j execwire.Journal) HelloRecord {
	t.Helper()
	h := helloFor(x.dispatchFixture)
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	h.Journals = []json.RawMessage{raw}
	return h
}

func TestS5Review2RunnerBootCannotRegress(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.lease(t)
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, stop.ID, "quiescent"), quiescent())
	if err != nil || reply.Released {
		t.Fatal(reply, err)
	}
	attempt := x.d.Assignment.Identity.AttemptID
	newBoot := helloFor(x.dispatchFixture)
	newBoot.RunnerBoot = newID()
	if _, err := x.s.RunnerSession(ctx, x.runner, newBoot); err != nil {
		t.Fatal(err)
	}
	basis := ReleaseBasis{Kind: BasisObservation, StopID: stop.ID}
	if _, err := x.s.ReleaseAttempt(ctx, attempt, basis); err == nil || !strings.Contains(err.Error(), "lease_barrier") {
		t.Fatalf("new boot did not impose barrier: %v", err)
	}
	t.Log("new runner boot: release refused with lease_barrier")
	// A different hello message reasserts the original boot. No DB writes or clock advance.
	oldAgain, err := x.s.RunnerSession(ctx, x.runner, helloFor(x.dispatchFixture))
	if err != nil {
		t.Logf("old boot rejected: %v", err)
		return
	}
	in, err := x.s.ReconciliationInputs(ctx, attempt)
	if err != nil || in.LeaseBarrierPassed {
		t.Fatal("invalid pre-barrier fixture", in.LeaseBarrierPassed, err)
	}
	t.Logf("old boot reasserted: mode=%s latest_matches_attempt=%t barrier_passed=%t", oldAgain.Mode, in.LastSession.RunnerBoot == x.facts.RunnerBoot, in.LeaseBarrierPassed)
	got, err := x.s.ReleaseAttempt(ctx, attempt, basis)
	if err == nil {
		t.Fatalf("UNSAFE: original-boot hello erased restart history; released=%s before lease barrier", got.Proof.To)
	}
}

func TestS5Review2FinalizationProofBindings(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	_, custody := x.upload(t, "succeeded", map[string]string{"result.txt": "result\n"})
	attempt := x.d.Assignment.Identity.AttemptID
	wrongBoot := &BoundaryAttestation{RunnerBoot: newID(), ReceiptID: custody.Receipt.ReceiptID, Boundary: quiescent()}
	if _, err := x.s.CompleteFinalization(ctx, attempt, wrongBoot); !errors.Is(err, p.BootMismatch) {
		t.Fatalf("foreign boot: %v", err)
	}
	wrongReceipt := &BoundaryAttestation{RunnerBoot: x.facts.RunnerBoot, ReceiptID: newID(), Boundary: quiescent()}
	if _, err := x.s.CompleteFinalization(ctx, attempt, wrongReceipt); !errors.Is(err, p.IdentityConflict) {
		t.Fatalf("foreign receipt: %v", err)
	}
	if _, err := x.s.CompleteFinalization(ctx, attempt, nil); err == nil || !strings.Contains(err.Error(), "quiescence") {
		t.Fatalf("missing proof: %v", err)
	}
	receipt := UsageReceipt{RequestID: "still-running", Protocol: "openai-chat", Model: "model-a", Source: "boundary", StartedMS: 1}
	if err := x.s.RecordUsage(ctx, x.runner, x.session.SessionID, UsageReport{Version: execwire.Version, MessageID: newID(), Identity: x.d.Assignment.Identity, Receipts: []UsageReceipt{receipt}}); err != nil {
		t.Fatal(err)
	}
	proof := &BoundaryAttestation{RunnerBoot: x.facts.RunnerBoot, ReceiptID: custody.Receipt.ReceiptID, Boundary: quiescent()}
	if _, err := x.s.CompleteFinalization(ctx, attempt, proof); err == nil || !strings.Contains(err.Error(), "boundary") {
		t.Fatalf("nonterminal usage: %v", err)
	}
	rowCount(t, x.s, "dispatch_releases", 0)
	t.Log("foreign boot, foreign receipt, missing proof, and nonterminal retained usage all refused; zero releases")
}

func TestS5Review2OpencodeCannotClaimNoBoundary(t *testing.T) {
	x := executionFixtureFor(t, nil)
	insertBrief(t, x, "opencode", "brief", nil, nil, nil, "{}", strings.Repeat("a", 64))
	lease := x.lease(t)
	intent := launchIntent(lease.Request.Nonce)
	intent.BoundaryPort = 0
	_, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, p.Starting), intent)
	if !errors.Is(err, p.Malformed) {
		t.Fatalf("opencode boundary_port=0 admitted: %v", err)
	}
	t.Log("opencode launch_intent boundary_port=0 refused: malformed")
}

func TestS5Review2CorruptJournalGatesAllStoreWriters(t *testing.T) {
	for _, which := range []string{"release", "finalize", "recover"} {
		t.Run(which, func(t *testing.T) {
			x := executionFixtureFor(t, nil)
			attempt := x.d.Assignment.Identity.AttemptID
			var basis ReleaseBasis
			switch which {
			case "release":
				lease := x.lease(t)
				x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
				x.propose(t, p.Running, launchedEvidence())
				stop := stopRequest(c.CancelAttempt, x.d)
				if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
					t.Fatal(err)
				}
				got, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, stop.ID, "quiescent"), quiescent())
				if err != nil || got.Released {
					t.Fatal(got, err)
				}
				basis = ReleaseBasis{Kind: BasisObservation, StopID: stop.ID}
			case "finalize":
				x.rcRunFake(t)
				x.upload(t, "succeeded", map[string]string{"result.txt": "ok\n"})
			case "recover":
				x.run(t)
				reopen(t, &x)
			}
			j := execwire.Journal{DispatchID: x.d.ID, Identity: x.d.Assignment.Identity, RunnerBoot: x.facts.RunnerBoot, DaemonBoot: x.s.meta.DaemonBoot, State: p.Running, Corrupt: true}
			corrupted := review2JournalHello(t, &x, j)
			if _, err := x.s.RunnerSession(ctx, x.runner, corrupted); err != nil {
				t.Fatal(err)
			}
			act := func() error {
				switch which {
				case "release":
					_, err := x.s.ReleaseAttempt(ctx, attempt, basis)
					return err
				case "finalize":
					_, err := x.s.CompleteFinalization(ctx, attempt, nil)
					return err
				default:
					_, err := x.s.RecoverResultPending(ctx, attempt, execwire.Journal{})
					return err
				}
			}
			if err := act(); err == nil || !strings.Contains(err.Error(), "journal_corrupt") {
				t.Fatalf("corruption gate: %v", err)
			}
			// A later hello omitting the journal must not clear the retained block.
			if _, err := x.s.RunnerSession(ctx, x.runner, helloFor(x.dispatchFixture)); err != nil {
				t.Fatal(err)
			}
			if err := act(); err == nil || !strings.Contains(err.Error(), "journal_corrupt") {
				t.Fatalf("omitted journal cleared corruption: %v", err)
			}
			// Another authenticated runner cannot repair this runner's journal.
			otherFP := strings.Repeat("c", 64)
			invite, err := x.s.CreateEnrollment(ctx, x.owner, newID(), otherFP)
			if err != nil {
				t.Fatal(err)
			}
			other, err := x.s.Enroll(ctx, otherFP, invite.Token)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := x.s.UpdateIdentity(ctx, x.owner, other.ID, 1, "enable", ""); err != nil {
				t.Fatal(err)
			}
			j.Corrupt = false
			if _, err := x.s.RunnerSession(ctx, otherFP, review2JournalHello(t, &x, j)); err != nil {
				t.Fatal(err)
			}
			if err := act(); err == nil || !strings.Contains(err.Error(), "journal_corrupt") {
				t.Fatalf("foreign runner cleared corruption: %v", err)
			}
			// A valid, later same-runner report repairs it.
			if _, err := x.s.RunnerSession(ctx, x.runner, review2JournalHello(t, &x, j)); err != nil {
				t.Fatal(err)
			}
			if err := act(); err != nil {
				t.Fatalf("same-runner repair refused: %v", err)
			}
			t.Log("corruption survived omitted journal and foreign-runner hello; exact later same-runner repair accepted")
		})
	}
}

func TestS5Review2RetainedTerminationAlwaysWaits(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.lease(t)
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	evidence := terminatedFor(&x, execwire.ExpiryStopID(lease.Request.Nonce), "quiescent")
	h := helloFor(x.dispatchFixture)
	h.RunnerBoot = newID()
	sess, err := x.s.RunnerSession(ctx, x.runner, h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := x.s.ReportTermination(ctx, x.runner, sess.SessionID, p.FencedVersion, evidence, quiescent())
	if err != nil || got.Outcome != "retained" {
		t.Fatal(got, err)
	}
	attempt := x.d.Assignment.Identity.AttemptID
	basis := ReleaseBasis{Kind: BasisRetainedTermination, EvidenceSHA256: retainedSHA(t, x.s, attempt, evidence.Terminated.MessageID)}
	// Even restoring the original session boot cannot turn a retained report into an observation.
	if _, err := x.s.RunnerSession(ctx, x.runner, helloFor(x.dispatchFixture)); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.ReleaseAttempt(ctx, attempt, basis); err == nil || !strings.Contains(err.Error(), "lease_barrier") {
		t.Fatalf("retained barrier: %v", err)
	}
	rcAdvanceControl(x.s, 40*time.Second)
	if got, err := x.s.ReleaseAttempt(ctx, attempt, basis); err != nil || got.Proof.To != p.Expired {
		t.Fatal(got, err)
	}
	t.Log("retained termination blocked before barrier and released expired after barrier")
}

func TestS5Review2ExitEvidenceBoundAtIngress(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.lease(t)
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	m := x.proposal(t, p.ResultPending)
	m.Identity.TaskID = newID()
	if _, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, m, exitEvidence(0)); err == nil {
		t.Fatal("foreign attempt identity accepted")
	}
	h := helloFor(x.dispatchFixture)
	h.RunnerBoot = newID()
	sess, err := x.s.RunnerSession(ctx, x.runner, h)
	if err != nil {
		t.Fatal(err)
	}
	m = x.proposal(t, p.ResultPending)
	if _, err := x.s.ProposeTransition(ctx, x.runner, sess.SessionID, p.FencedVersion, m, exitEvidence(0)); !errors.Is(err, p.BootMismatch) {
		t.Fatalf("foreign boot exit accepted: %v", err)
	}
	reopen(t, &x)
	if _, err := x.s.RecoverResultPending(ctx, x.d.Assignment.Identity.AttemptID, execwire.Journal{}); err == nil || !strings.Contains(err.Error(), "exit") {
		t.Fatalf("invalid exit enabled recovery: %v", err)
	}
	t.Log("foreign identity and foreign runner-boot exits refused at ingress; recovery still refused without valid daemon exit")
}
