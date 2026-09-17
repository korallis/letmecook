package store

import (
	"strings"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
)

func TestRunnerRestartLocalTerminationWaitsForBarrier(t *testing.T) {
	for _, cause := range c.LocalStopCauses {
		t.Run(cause, func(t *testing.T) {
			x := executionFixtureFor(t, nil)
			lease := x.lease(t)
			x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
			x.propose(t, p.Running, launchedEvidence())
			attempt := x.d.Assignment.Identity.AttemptID
			evidence := terminatedFor(&x, execwire.LocalStopID(attempt, cause), "quiescent")
			// SIGKILL left no stopping proposal or daemon target. The protected
			// guardian evidence is imported by a different runner incarnation.
			hello := helloFor(x.dispatchFixture)
			hello.RunnerBoot = newID()
			var err error
			x.session, err = x.s.RunnerSession(ctx, x.runner, hello)
			if err != nil {
				t.Fatal(err)
			}
			if reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, quiescent()); err != nil || reply.Outcome != "retained" || reply.Released {
				t.Fatal(reply, err)
			}
			basis := ReleaseBasis{Kind: BasisRetainedTermination, EvidenceSHA256: retainedSHA(t, x.s, attempt, evidence.Terminated.MessageID)}
			if _, err := x.s.ReleaseAttempt(ctx, attempt, basis); err == nil || !strings.Contains(err.Error(), "lease_barrier") {
				t.Fatal("old runner evidence bypassed barrier or failed stop recognition", err)
			}
			rcReleases(t, x.s, 0)
			rcAdvanceControl(x.s, 40*time.Second)
			out, err := x.s.ReleaseAttempt(ctx, attempt, basis)
			if err != nil || out.Proof.To != p.Cancelled || out.Cause != cause || len(out.Cleared) != 1 {
				t.Fatal(out, err)
			}
			var body string
			if err := x.s.db.QueryRow("SELECT body FROM control_stops WHERE id=?", evidence.Terminated.StopID).Scan(&body); err != nil {
				t.Fatal(err)
			}
			var stop c.Receipt
			if err := decodeControl(body, &stop); err != nil || stop.Actor != "runner-local" {
				t.Fatal(stop, err)
			}
			rcReleases(t, x.s, 1)
		})
	}
}

func TestRunningLocalTerminationRequiresSettledBoundary(t *testing.T) {
	for _, settled := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote-unknown", true: "quiescent"}[settled], func(t *testing.T) {
			x := executionFixtureFor(t, nil)
			lease := x.lease(t)
			x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
			x.propose(t, p.Running, launchedEvidence())
			attempt := x.d.Assignment.Identity.AttemptID
			evidence := terminatedFor(&x, execwire.LocalStopID(attempt, "runner_shutdown"), "quiescent")
			boundary := quiescent()
			if !settled {
				evidence.Terminated.RemoteWork = "unknown"
				boundary = BoundaryState{Reservations: 1, InFlight: 1}
			}
			reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, boundary)
			if err != nil || reply.Released != settled {
				t.Fatal(reply, err)
			}
			state, _ := attemptRow(t, x.s, attempt)
			if settled && state != p.Cancelled || !settled && rcTerminal(state) {
				t.Fatal(state)
			}
		})
	}
}
