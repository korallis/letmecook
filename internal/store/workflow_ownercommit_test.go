//go:build ownercommit

package store

// Enable after integration applies the workflowOwner/workflowDecisionTx calls to
// grants.go and the daemon pause check to dispatchAllowed. No sleeps/race luck:
// each test revokes the identity after authentication and before domain commit.
import (
	"errors"
	"strings"
	"testing"

	i "github.com/korallis/letmecook/internal/identity"
)

func TestOwnerCommitRevocationBetweenAuthenticationAndDomainCommit(t *testing.T) {
	for _, operation := range []string{"approve", "restrict", "invalidate", "dispatch"} {
		t.Run(operation, func(t *testing.T) {
			f := dispatchFixtureFor(t, nil)
			if _, err := f.s.Authenticate(ctx, f.owner); err != nil {
				t.Fatal(err)
			}
			authenticated := OwnerContext(ctx, f.owner)
			if err := f.s.BootstrapOwner(ctx, strings.Repeat("c", 64), true); err != nil {
				t.Fatal(err)
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
