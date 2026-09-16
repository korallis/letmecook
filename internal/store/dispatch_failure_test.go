package store

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	g "github.com/korallis/letmecook/internal/authority"

	i "github.com/korallis/letmecook/internal/identity"
	sc "github.com/korallis/letmecook/internal/scheduler"
	p "github.com/korallis/letmecook/schemas/execution"
)

func TestDispatchEvidenceOwnerAndConcurrentDrift(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	changed := f.facts
	changed.Revision++
	changed.Isolation.ObservedDigest = strings.Repeat("d", 64)
	if err := f.s.PublishEligibility(ctx, f.runner, 1, changed); !errors.Is(err, i.Denied) {
		t.Fatal("runner granted eligibility", err)
	}
	alias := f.facts
	alias.ID = "alias"
	requireReason(t, f.s.PublishEligibility(ctx, f.owner, 0, alias), "identity_conflict")
	var dispatched error
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() { <-start; _, dispatched = f.s.Dispatch(ctx, f.request) })
	wg.Go(func() {
		<-start
		if err := f.s.PublishEligibility(ctx, f.owner, 1, changed); err != nil {
			t.Error(err)
		}
	})
	close(start)
	wg.Wait()
	if dispatched == nil {
		_, err := f.s.Delivery(ctx, f.runner, f.request.ID)
		requireReason(t, err, "stale_decision")
		v, err := f.s.Assignment(ctx, f.request.ID)
		if err != nil || !reflect.DeepEqual(v.Facts, f.facts) || v.Released {
			t.Fatal("drift rewrote old reservation", err)
		}
	} else {
		requireReason(t, dispatched, "stale_decision")
		dispatchRows(t, f.s, 0)
	}
}

func TestDispatchAckAndReleaseWriteFailure(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	v := admitted(t, f)
	ack := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newID(), Identity: v.Assignment.Identity, AssignmentID: v.Assignment.AssignmentID, RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.s.meta.DaemonBoot}
	bad := ack
	bad.RunnerBoot = newID()
	if err := f.s.AcknowledgeAssignment(ctx, f.runner, v.ID, bad); !errors.Is(err, p.BootMismatch) {
		t.Fatal(err)
	}
	bad = ack
	bad.MessageID = v.Assignment.MessageID
	if err := f.s.AcknowledgeAssignment(ctx, f.runner, v.ID, bad); !errors.Is(err, p.IdentityConflict) {
		t.Fatal(err)
	}
	sqlExec(t, f.s, "CREATE TRIGGER fail_ack BEFORE INSERT ON dispatch_acks BEGIN SELECT RAISE(ABORT,'interrupted'); END")
	if err := f.s.AcknowledgeAssignment(ctx, f.runner, v.ID, ack); err == nil {
		t.Fatal("failed ack succeeded")
	}
	retained, err := f.s.Assignment(ctx, v.ID)
	if err != nil || retained.Acknowledged {
		t.Fatal("partial ack", err)
	}
	sqlExec(t, f.s, "DROP TRIGGER fail_ack")
	if err := f.s.AcknowledgeAssignment(ctx, f.runner, v.ID, ack); err != nil {
		t.Fatal(err)
	}
	proof := reconcile(t, f, v)
	sqlExec(t, f.s, "CREATE TRIGGER fail_release BEFORE INSERT ON dispatch_releases BEGIN SELECT RAISE(ABORT,'interrupted'); END")
	if err := f.s.ReconcileDispatch(ctx, f.owner, proof); err == nil {
		t.Fatal("failed release succeeded")
	}
	view := snapshot(t, f.s)
	if view.Tasks[0].Attempt.State != p.Stopping || view.Tasks[0].Attempt.Revision != 2 || len(view.Events) != 2 {
		t.Fatal("partial terminal transaction")
	}
	retained, err = f.s.Assignment(ctx, v.ID)
	if err != nil || retained.Released {
		t.Fatal("failed terminal released capacity", err)
	}
	sqlExec(t, f.s, "DROP TRIGGER fail_release")
	if err := f.s.ReconcileDispatch(ctx, f.owner, proof); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchAllowanceAndUnknownTuple(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	for _, tc := range []struct {
		reason string
		change func(*DispatchRequest)
	}{
		{"malformed", func(r *DispatchRequest) { r.Allowance.Concurrency = 0 }},
		{"malformed", func(r *DispatchRequest) { r.Allowance.Attempts = 2 }},
		{"widened_scope", func(r *DispatchRequest) { r.Allowance.Requests = 6 }},
		{"invalid_assessment", func(r *DispatchRequest) { r.Decision.Assessment.Required = []string{"vision", "tools"} }},
		{"invalid_assessment", func(r *DispatchRequest) { r.Decision.Assessment.AssessorVersion = "model-proposal" }},
	} {
		r := f.request
		tc.change(&r)
		_, err := f.s.Dispatch(ctx, r)
		requireReason(t, err, tc.reason)
	}
	f.request.Decision.EligibilityID = "missing"
	bindDispatch(t, &f)
	f.grant.ID, f.grant.Revision = newID(), 2
	f.grant.Envelope.RouteDecision.Number++
	f.request.Decision.Revision++
	hash, _ := sc.Digest(f.request.Decision)
	f.grant.Envelope.RouteDecision.SHA256 = hash
	f.grant = approve(t, f.s, f.request.Request.GrantID, f.grant)
	f.request.Request = requestFor(t, f.grant)
	_, err := f.s.Dispatch(ctx, f.request)
	requireReason(t, err, "no_eligible_tuple")
	dispatchRows(t, f.s, 0)
}

func TestDispatchNativeHardBoundRefusal(t *testing.T) {
	f := dispatchFixtureFor(t, func(f *dispatchFixture) {
		route := &f.grant.Envelope.Routes[0]
		route.LimitsProfile, route.LimitsAuthority = "native-subscription-local-v1", "operator-native"
		route.Targets[1].Billing = "subscription"
		f.grant.Envelope.Budgets.ProviderOutputTokens, f.grant.Envelope.Budgets.ProviderCostMicros = 0, nil
		f.facts.LocalEnvelope = cloneGrant(t, f.grant).Envelope
		f.facts.Route = *route
		f.facts.Paths[1].Target = route.Targets[1]
		f.request.Decision.Selected = *route
		f.request.Decision.Assessment.HardOutputBound = true
	})
	_, errs := concurrentDispatch(t, f.s, f.request, f.request)
	for _, err := range errs {
		requireReason(t, err, "hard_bound_unavailable")
	}
	dispatchRows(t, f.s, 0)
	// Independently approved strict policy cannot invent native provider usage.
	f = dispatchFixtureFor(t, func(f *dispatchFixture) {
		route := &f.grant.Envelope.Routes[0]
		route.LimitsProfile, route.LimitsAuthority = "native-subscription-local-v1", "operator-native"
		route.Targets[1].Billing = "subscription"
		f.grant.Envelope.Budgets.ProviderOutputTokens, f.grant.Envelope.Budgets.ProviderCostMicros = 0, nil
		f.facts.LocalEnvelope = cloneGrant(t, f.grant).Envelope
		f.facts.Route = *route
		f.facts.Paths[1].Target = route.Targets[1]
		f.request.Decision.Selected = *route
	})
	v := admitted(t, f)
	if err := f.s.ReconcileDispatch(ctx, f.owner, reconcile(t, f, v)); err != nil {
		t.Fatal(err)
	}
	f.grant.ID, f.grant.Revision = newID(), 2
	f.grant.Envelope.Budgets.ProviderOutputTokens = 128
	route := &f.grant.Envelope.Routes[0]
	route.RouteRevision++
	route.Policy = g.Revision{Number: 2, SHA256: strings.Repeat("c", 64)}
	route.LimitsProfile, route.LimitsAuthority = "strict-provider-output-v1", "operator-strict"
	f.facts.Revision++
	f.facts.Route = *route
	f.facts.LocalEnvelope = cloneGrant(t, f.grant).Envelope
	f.request.ID, f.request.Decision.ID = newID(), newID()
	f.request.Decision.Revision++
	f.request.Decision.Selected = *route
	bindDispatch(t, &f)
	if err := f.s.PublishEligibility(ctx, f.owner, 1, f.facts); err != nil {
		t.Fatal(err)
	}
	approve(t, f.s, v.Request.GrantID, f.grant)
	_, err := f.s.Dispatch(ctx, f.request)
	requireReason(t, err, "budget_unknown")
}

func TestDispatchChargesSurviveGrantRevisionAndRestart(t *testing.T) {
	f := dispatchFixtureFor(t, func(f *dispatchFixture) { f.grant.Envelope.Budgets.Requests = 1 })
	v := admitted(t, f)
	if err := f.s.ReconcileDispatch(ctx, f.owner, reconcile(t, f, v)); err != nil {
		t.Fatal(err)
	}
	next := cloneGrant(t, f.grant)
	next.ID, next.Revision = newID(), 2
	next.Envelope.Paths = []string{"a.txt"}
	next = approve(t, f.s, f.grant.ID, next)
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, f.s.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	f.request.ID = newID()
	f.request.Request = requestFor(t, next)
	_, err = r.Dispatch(ctx, f.request)
	requireReason(t, err, "budget_exhausted")
	dispatchRows(t, r, 1)
}
