//go:build ownercommit

package store

// Exercise the commit-time owner and decision hooks, not just transport auth.
// No sleeps/race luck: each revocation happens after authentication, before commit.
import (
	"errors"
	"strings"
	"testing"

	i "github.com/korallis/letmecook/internal/identity"
	r "github.com/korallis/letmecook/internal/review"
)

func TestOwnerCommitRevocationBetweenAuthenticationAndDomainCommit(t *testing.T) {
	for _, operation := range []string{"approve", "restrict", "invalidate", "dispatch", "review"} {
		t.Run(operation, func(t *testing.T) {
			f := dispatchFixtureFor(t, nil)
			var decision r.Decision
			if operation == "review" {
				report, d := verificationFixture(t, f.s)
				mustSaveVerification(t, f.s, report)
				decision = d
			}
			if _, err := f.s.Authenticate(ctx, f.owner); err != nil {
				t.Fatal(err)
			}
			authenticated := OwnerContext(ctx, f.owner)
			if err := f.s.BootstrapOwner(ctx, strings.Repeat("c", 64), true); err != nil {
				t.Fatal(err)
			}
			// Recovery revokes all runners too. Restore only the unrelated test
			// runner principal so dispatch tests the owner gate, not placement.
			if operation == "dispatch" {
				if _, err := f.s.db.Exec("UPDATE principals SET enabled=1,revoked=0 WHERE id=?", f.facts.Repository.RunnerRoot.RunnerID); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			switch operation {
			case "approve":
				grant := grantFixture()
				_, err = f.s.ApproveExecution(authenticated, "", grant)
			case "restrict":
				grant := cloneGrant(t, f.grant)
				grant.ID = newID()
				grant.Revision++
				grant.Envelope.Budgets.Requests--
				_, err = f.s.RestrictExecution(authenticated, f.grant.ID, grant)
			case "invalidate":
				err = f.s.InvalidateExecution(authenticated, f.grant.TaskID, f.grant.ID, "owner", "revoked")
			case "dispatch":
				_, err = f.s.Dispatch(authenticated, f.request)
			case "review":
				err = f.s.RecordLocalDecision(authenticated, decision)
			}
			if !errors.Is(err, i.Denied) {
				t.Fatalf("revoked owner %s commit must fail identity_denied, got %v", operation, err)
			}
		})
	}
}
func TestOwnerCommitDecisionAtomicity(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	grant := cloneGrant(t, f.grant)
	grant.ID = newID()
	grant.Revision++
	// An unchanged envelope intentionally returns existing authority without a
	// commit. Narrow it so this tests rollback of a new grant and its decision.
	grant.Envelope.Budgets.Requests--
	decision := f.request.Decision
	decision.Assessment.TaskID = newID()
	_, err := f.s.ApproveExecution(DecisionContext(OwnerContext(ctx, f.owner), decision), f.grant.ID, grant)
	requireReason(t, err, "stale_decision")
	var count int
	if err = f.s.db.QueryRow("SELECT count(*) FROM execution_grants WHERE id=?", grant.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("grant committed without decision", count, err)
	}
}
func TestOwnerCommitPausedAdmission(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	if _, err := f.s.SetPaused(ctx, f.owner, newID(), true, "maintenance", false); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.Dispatch(OwnerContext(ctx, f.owner), f.request)
	requireReason(t, err, "paused")
	dispatchRows(t, f.s, 0)
}

func TestOwnerCommitWithoutOwnerContextPreservesTrustedCaller(t *testing.T) {
	s, _ := persistent(t)
	// Trusted, non-workflow APIs use actor strings and have no owner context.
	// They must not acquire a new authentication requirement from these hooks.
	grant := grantFixture()
	got, err := s.ApproveExecution(ctx, "", grant)
	if err != nil || got.ID != grant.ID {
		t.Fatalf("trusted approval without owner context: %+v, %v", got, err)
	}
}

func TestOwnerCommitRetainsDecisionAtomically(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	grant := cloneGrant(t, f.grant)
	grant.ID = newID()
	grant.Revision++
	grant.Envelope.Budgets.Requests--
	if _, err := f.s.ApproveExecution(DecisionContext(OwnerContext(ctx, f.owner), f.request.Decision), f.grant.ID, grant); err != nil {
		t.Fatal(err)
	}
	decision, err := f.s.ExecutionDecision(ctx, grant.ID)
	if err != nil || decision.ID != f.request.Decision.ID {
		t.Fatalf("committed decision missing: %+v, %v", decision, err)
	}
}
