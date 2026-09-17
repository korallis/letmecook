package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	i "github.com/korallis/letmecook/internal/identity"
	sc "github.com/korallis/letmecook/internal/scheduler"
	p "github.com/korallis/letmecook/schemas/execution"
	"modernc.org/sqlite"
)

// Prior-schema fixtures remove new objects before replaying old migrations.
const dropDispatchSchema = dropControlSchema + `DROP TABLE dispatch_acks; DROP TABLE dispatch_releases; DROP TABLE dispatches; DROP TABLE dispatch_eligibility; DROP TABLE dispatch_stops;`

type dispatchFixture struct {
	s                        *Store
	artifacts, owner, runner string
	grant                    g.Grant
	facts                    sc.Eligibility
	request                  DispatchRequest
}

func dispatchFixtureFor(t *testing.T, change func(*dispatchFixture)) dispatchFixture {
	t.Helper()
	s, artifacts := persistent(t)
	profile, owner, runner := repositoryFixture(t, s)
	if _, err := s.RegisterRepository(ctx, owner, 0, profile); err != nil {
		t.Fatal(err)
	}
	selection := repositorySelection(t, profile)
	if _, err := s.UpdateIdentity(ctx, owner, selection.RunnerRoot.RunnerID, 1, "enable", ""); err != nil {
		t.Fatal(err)
	}
	grant := grantFixture()
	grant.Envelope.Repository, grant.Envelope.BaseCommit = profile.ID, profile.Base.Commit
	grant.Envelope.Runners = []string{selection.RunnerRoot.RunnerID}
	grant.Envelope.Routes = grant.Envelope.Routes[:1]
	grant.Envelope.Selection = "pinned"
	rev := grant.Envelope.Brief
	facts := sc.Eligibility{ID: "fixture-pair", Revision: 1, Repository: selection, Enabled: true, RunnerBoot: newID(), LocalPolicy: rev, LocalEnvelope: cloneGrant(t, grant).Envelope, Config: rev, Route: grant.Envelope.Routes[0], Capabilities: []string{"tools", "vision"}, Isolation: sc.IsolationProfile{ID: "fixture-isolation", Revision: rev, RuntimeDigest: rev.SHA256, ObservedDigest: rev.SHA256, Kind: "dedicated-vm", Supported: true, Controls: sc.RequiredIsolationControls()}, Capacity: sc.Resources{CPU: 1000, MemoryBytes: 4096, DiskBytes: 8192, Processes: 8}, Concurrency: 1, RouterAuthenticated: true, Availability: "unknown", ValidUntilMS: grant.Envelope.ExpiresMS}
	for _, target := range facts.Route.Targets {
		facts.Paths = append(facts.Paths, sc.PathEvidence{Target: target, Compatible: true, Capabilities: []string{"tools", "vision"}, ContextTokens: 8192, LocalBounds: true, ProviderOutputBound: true, ProviderCostBound: true})
	}
	request := DispatchRequest{ID: newID(), Decision: sc.Decision{ID: newID(), Revision: 1, Assessment: sc.Assessment{TaskID: grant.TaskID, Brief: rev, Plan: rev, ContextDigest: rev.SHA256, Role: "worker", WorkClass: "code", Ambiguity: "low", Consequence: "high", Required: []string{"tools", "vision"}, Preferences: []string{}, EvidenceRefs: []string{"operator-brief"}, Unknowns: []string{"subscription-headroom"}, ContextTokens: 4096, AssessorVersion: "operator-v1", SelectorVersion: "deterministic-v1"}, EligibilityID: facts.ID, Selected: facts.Route, RankedAlternatives: []string{}, Authorization: "operator", Reason: "pinned", Resources: sc.Resources{CPU: 500, MemoryBytes: 2048, DiskBytes: 4096, Processes: 4}}}
	f := dispatchFixture{s: s, artifacts: artifacts, owner: owner, runner: runner, grant: grant, facts: facts, request: request}
	if change != nil {
		change(&f)
	}
	bindDispatch(t, &f)
	if err := s.PublishEligibility(ctx, owner, 0, f.facts); err != nil {
		t.Fatal(err)
	}
	f.grant = approve(t, s, "", f.grant)
	return f
}

func bindDispatch(t *testing.T, f *dispatchFixture) {
	t.Helper()
	hash, err := sc.Digest(f.facts)
	if err != nil {
		t.Fatal(err)
	}
	f.request.Decision.Eligibility = g.Revision{Number: f.facts.Revision, SHA256: hash}
	hash, err = sc.Digest(f.request.Decision)
	if err != nil {
		t.Fatal(err)
	}
	f.grant.Envelope.RouteDecision = g.Revision{Number: f.request.Decision.Revision, SHA256: hash}
	f.request.Request = requestFor(t, f.grant)
	// Reserve bounded per-attempt allowance, not every task request on each retry.
	f.request.Allowance = f.request.Request.Envelope.Budgets
	b := &f.request.Allowance
	b.Attempts, b.Retries, b.Concurrency, b.TotalMS = 1, 0, 1, 10000
	b.Requests, b.Subattempts, b.AttemptMS, b.ProviderOutputTokens = 1, 2, 10000, 32
	cost := int64(100)
	b.ProviderCostMicros = &cost
	if f.grant.Envelope.Budgets.ProviderCostMicros == nil {
		b.ProviderCostMicros = nil
	}
	if f.grant.Envelope.Budgets.ProviderOutputTokens == 0 {
		b.ProviderOutputTokens, b.ProviderCostMicros = 0, nil
	}
}

func dispatchRows(t *testing.T, s *Store, want int) {
	t.Helper()
	for _, table := range []string{"dispatches", "attempts"} {
		var n int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != want {
			t.Fatalf("%s: got %d want %d: %v", table, n, want, err)
		}
	}
}
func admitted(t *testing.T, f dispatchFixture) Dispatch {
	t.Helper()
	v, err := f.s.Dispatch(ctx, f.request)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func concurrentDispatch(t *testing.T, s *Store, requests ...DispatchRequest) ([]Dispatch, []error) {
	t.Helper()
	out, errs := make([]Dispatch, len(requests)), make([]error, len(requests))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for j, request := range requests {
		wg.Go(func() { <-start; out[j], errs[j] = s.Dispatch(ctx, request) })
	}
	close(start)
	wg.Wait()
	return out, errs
}

func TestDispatchAtomicConcurrencyAndLostAck(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	out, errs := concurrentDispatch(t, f.s, f.request, f.request)
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(out[0], out[1]) {
		t.Fatal("duplicate changed immutable dispatch")
	}
	dispatchRows(t, f.s, 1)
	v := out[0]
	pending, err := f.s.PendingAssignments(ctx, "", 1)
	if err != nil || !reflect.DeepEqual(pending, []string{v.ID}) {
		t.Fatal("outbox discovery", pending, err)
	}
	nextPage, err := f.s.PendingAssignments(ctx, v.ID, 1)
	if err != nil || len(nextPage) != 0 {
		t.Fatal("outbox cursor", nextPage, err)
	}
	for range 2 {
		m, err := f.s.Delivery(ctx, f.runner, f.request.ID)
		if err != nil || !reflect.DeepEqual(m, v.Assignment) {
			t.Fatal("lost ack changed assignment", err)
		}
	}
	ack := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newID(), Identity: v.Assignment.Identity, AssignmentID: v.Assignment.AssignmentID, RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.s.meta.DaemonBoot}
	for range 2 {
		if err := f.s.AcknowledgeAssignment(ctx, f.runner, f.request.ID, ack); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := f.s.Assignment(ctx, f.request.ID)
	if err != nil || !stored.Acknowledged || !reflect.DeepEqual(stored.Decision, f.request.Decision) || !reflect.DeepEqual(stored.Facts, f.facts) {
		t.Fatal("decision/ack not durable", err)
	}
	pending, err = f.s.PendingAssignments(ctx, "", 128)
	if err != nil || len(pending) != 0 {
		t.Fatal("ack left pending record", pending, err)
	}
	ack.AssignmentID = newID()
	if err := f.s.AcknowledgeAssignment(ctx, f.runner, f.request.ID, ack); !errors.Is(err, p.IdentityConflict) {
		t.Fatal(err)
	}
	changed := f.request
	changed.Request.Envelope = cloneGrant(t, f.grant).Envelope
	changed.Request.Envelope.Paths = []string{"a.txt"}
	_, err = f.s.Dispatch(ctx, changed)
	requireReason(t, err, "identity_conflict")
	for _, query := range []string{"UPDATE dispatches SET input='{}'", "DELETE FROM dispatches", "DELETE FROM dispatch_acks", "UPDATE attempts SET epoch=2"} {
		if _, err := f.s.db.Exec(query); err == nil {
			t.Fatal("mutable record", query)
		}
	}
}

func TestDispatchCompetingIntentAndGlobalConcurrency(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	other := f.request
	other.ID = newID()
	_, errs := concurrentDispatch(t, f.s, f.request, other)
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			requireReason(t, err, "current_assignment")
		}
	}
	if ok != 1 {
		t.Fatal("multiple winners", errs)
	}
	dispatchRows(t, f.s, 1)
	other = otherTaskRequest(t, f)
	_, err := f.s.Dispatch(ctx, other)
	requireReason(t, err, "concurrency_ceiling")
}

func otherTaskRequest(t *testing.T, f dispatchFixture) DispatchRequest {
	t.Helper()
	other := f.request
	other.ID = newID()
	other.Request.TaskID, other.Request.GrantID = newID(), newID()
	other.Decision.ID, other.Decision.Assessment.TaskID = newID(), other.Request.TaskID
	hash, err := sc.Digest(other.Decision)
	if err != nil {
		t.Fatal(err)
	}
	other.Request.Envelope.RouteDecision.SHA256 = hash
	grant := f.grant
	grant.ID, grant.TaskID, grant.Envelope.RouteDecision = other.Request.GrantID, other.Request.TaskID, other.Request.Envelope.RouteDecision
	approve(t, f.s, "", grant)
	return other
}

func TestDispatchRefusalsUnderConcurrency(t *testing.T) {
	cases := []struct {
		name, reason string
		change       func(*dispatchFixture)
	}{
		{"missing isolation", "isolation_missing", func(f *dispatchFixture) { f.facts.Isolation = sc.IsolationProfile{} }},
		{"unsupported isolation", "isolation_unsupported", func(f *dispatchFixture) { f.facts.Isolation.Supported = false }},
		{"docker required", "isolation_unsupported", func(f *dispatchFixture) { f.facts.Isolation.DockerRequired = true }},
		{"host shell", "isolation_unsupported", func(f *dispatchFixture) { f.facts.Isolation.Kind = "host-shell" }},
		{"worktree", "isolation_unsupported", func(f *dispatchFixture) { f.facts.Isolation.Kind = "worktree" }},
		{"profile drift", "isolation_drift", func(f *dispatchFixture) { f.facts.Isolation.ObservedDigest = strings.Repeat("d", 64) }},
		{"missing control", "isolation_unsupported", func(f *dispatchFixture) { f.facts.Isolation.Controls = []string{} }},
		{"local disabled", "local_policy_denied", func(f *dispatchFixture) { f.facts.Enabled = false }},
		{"local scope", "local_policy_denied", func(f *dispatchFixture) { f.facts.LocalEnvelope.Paths = []string{"unrelated"} }},
		{"runner capability", "runner_capability_missing", func(f *dispatchFixture) { f.facts.Capabilities = []string{"tools"} }},
		{"fallback missing", "fallback_evidence_missing", func(f *dispatchFixture) { f.facts.Paths = f.facts.Paths[:1] }},
		{"fallback vision", "fallback_capability_missing", func(f *dispatchFixture) { f.facts.Paths[1].Capabilities = []string{"tools"} }},
		{"fallback incompatible", "fallback_ineligible", func(f *dispatchFixture) { f.facts.Paths[1].Compatible = false }},
		{"fallback context", "fallback_ineligible", func(f *dispatchFixture) { f.facts.Paths[1].ContextTokens = 512 }},
		{"fallback output cap", "hard_bound_unavailable", func(f *dispatchFixture) { f.facts.Paths[1].ProviderOutputBound = false }},
		{"fallback cost cap", "hard_bound_unavailable", func(f *dispatchFixture) { f.facts.Paths[1].ProviderCostBound = false }},
		{"outage", "route_unavailable", func(f *dispatchFixture) {
			f.facts.Availability = "outage"
			f.request.Decision.RankedAlternatives = []string{"unapproved-backup"}
		}},
		{"router unauthenticated", "router_unauthenticated", func(f *dispatchFixture) { f.facts.RouterAuthenticated = false }},
		{"expired evidence", "stale_evidence", func(f *dispatchFixture) { f.facts.ValidUntilMS = time.Now().UnixMilli() - 1 }},
		{"mandatory unknown", "mandatory_unknown", func(f *dispatchFixture) { f.request.Decision.Assessment.MandatoryUnknown = true }},
		{"resource ceiling", "resource_ceiling", func(f *dispatchFixture) { f.request.Decision.Resources.CPU = 1001 }},
		{"stale assessment", "stale_decision", func(f *dispatchFixture) { f.request.Decision.Assessment.Brief.Number++ }},
		{"short validity", "duration_ceiling", func(f *dispatchFixture) { f.facts.ValidUntilMS = time.Now().UnixMilli() + 5000 }},
		{"unconfigured hard cost", "hard_bound_unavailable", func(f *dispatchFixture) {
			f.grant.Envelope.Budgets.ProviderCostMicros = nil
			f.facts.LocalEnvelope.Budgets.ProviderCostMicros = nil
			f.request.Decision.Assessment.HardCostBound = true
		}},
		{"billing mismatch", "stale_decision", func(f *dispatchFixture) {
			f.request.Decision.Selected.Targets = append([]g.Target(nil), f.request.Decision.Selected.Targets...)
			f.request.Decision.Selected.Targets[0].Billing = "metered"
		}},
		{"changed graph", "stale_decision", func(f *dispatchFixture) { f.request.Decision.Selected.GraphDigest = strings.Repeat("c", 64) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := dispatchFixtureFor(t, tc.change)
			_, errs := concurrentDispatch(t, f.s, f.request, f.request)
			for _, err := range errs {
				requireReason(t, err, tc.reason)
			}
			dispatchRows(t, f.s, 0)
			if len(snapshot(t, f.s).Events) != 0 {
				t.Fatal("refusal committed event")
			}
		})
	}
}

func TestDispatchStaleSelectionAndRevocation(t *testing.T) {
	for _, action := range []string{"route", "billing", "isolation", "policy", "stop", "revoke", "disable", "repository"} {
		t.Run(action, func(t *testing.T) {
			f := dispatchFixtureFor(t, nil)
			reason := "stale_decision"
			switch action {
			case "route", "billing", "isolation", "policy":
				changed := f.facts
				changed.Revision++
				if action == "route" {
					changed.Route.GraphDigest = strings.Repeat("d", 64)
					changed.Route.RouteRevision++
					changed.LocalEnvelope.Routes = []g.Route{changed.Route}
				}
				if action == "billing" {
					changed.Route.Targets = append([]g.Target(nil), changed.Route.Targets...)
					changed.Route.Targets[0].Billing = "metered"
					changed.Paths = append([]sc.PathEvidence(nil), changed.Paths...)
					changed.Paths[0].Target = changed.Route.Targets[0]
					changed.LocalEnvelope.Routes = []g.Route{changed.Route}
				}
				if action == "isolation" {
					changed.Isolation.ObservedDigest = strings.Repeat("d", 64)
				}
				if action == "policy" {
					changed.LocalEnvelope.Paths = []string{"other"}
				}
				if err := f.s.PublishEligibility(ctx, f.owner, 1, changed); err != nil {
					t.Fatal(err)
				}
			case "stop":
				reason = "stopped"
				if err := f.s.StopDispatch(ctx, f.owner, f.grant.TaskID); err != nil {
					t.Fatal(err)
				}
			case "revoke":
				reason = "revoked"
				if err := f.s.InvalidateExecution(ctx, f.grant.TaskID, f.grant.ID, "operator", "revoked"); err != nil {
					t.Fatal(err)
				}
			case "disable":
				reason = "runner_disabled"
				if _, err := f.s.UpdateIdentity(ctx, f.owner, f.facts.Repository.RunnerRoot.RunnerID, 2, "disable", ""); err != nil {
					t.Fatal(err)
				}
			case "repository":
				reason = "repository_policy_drift"
				profile, err := f.s.RepositoryProfile(ctx, f.owner, f.facts.Repository.Repository)
				if err != nil {
					t.Fatal(err)
				}
				profile.Revision++
				if _, err := f.s.RegisterRepository(ctx, f.owner, 1, profile); err != nil {
					t.Fatal(err)
				}
			}
			_, errs := concurrentDispatch(t, f.s, f.request, f.request)
			for _, err := range errs {
				requireReason(t, err, reason)
			}
			dispatchRows(t, f.s, 0)
		})
	}
}

func TestDispatchStopRace(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "revoke"}[revoke], func(t *testing.T) {
			f := dispatchFixtureFor(t, nil)
			var result error
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Go(func() { <-start; _, result = f.s.Dispatch(ctx, f.request) })
			wg.Go(func() {
				<-start
				var err error
				if revoke {
					err = f.s.InvalidateExecution(ctx, f.grant.TaskID, f.grant.ID, "operator", "revoked")
				} else {
					err = f.s.StopDispatch(ctx, f.owner, f.grant.TaskID)
				}
				if err != nil {
					t.Error(err)
				}
			})
			close(start)
			wg.Wait()
			reason := "stopped"
			if revoke {
				reason = "revoked"
			}
			if result != nil {
				requireReason(t, result, reason)
			} else {
				_, err := f.s.Delivery(ctx, f.runner, f.request.ID)
				deliveryReason := reason
				if !revoke {
					deliveryReason = "reconciliation_required"
				}
				requireReason(t, err, deliveryReason)
				v, err := f.s.Assignment(ctx, f.request.ID)
				if err != nil || v.Released {
					t.Fatal("stop released reservation", err)
				}
			}
			f.request.ID = newID()
			_, err := f.s.Dispatch(ctx, f.request)
			requireReason(t, err, reason)
		})
	}
}

func reconciliation(v Dispatch) Reconciliation {
	return Reconciliation{DispatchID: v.ID, Identity: v.Assignment.Identity, ExpectedRevision: 2, To: p.Cancelled, ConfirmedProcess: "terminated", RemoteWork: "quiescent", LaunchFenced: true, ArtifactsPreserved: true, EvidenceDigest: strings.Repeat("a", 64)}
}

func recoverDispatch(t *testing.T, f *dispatchFixture, v Dispatch) Reconciliation {
	t.Helper()
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, f.s.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	f.s = r
	return reconciliation(v)
}

func TestDispatchStopReconcileLifecycle(t *testing.T) {
	for _, phase := range []string{"cancelled", "expired", "unknown"} {
		t.Run(phase, func(t *testing.T) {
			f := dispatchFixtureFor(t, nil)
			v := admitted(t, f)
			proof := reconciliation(v)
			wantState := p.Stopping
			if phase == "unknown" {
				proof = recoverDispatch(t, &f, v)
				wantState = p.Unknown
			} else {
				premature := proof
				premature.ExpectedRevision = 1
				if err := f.s.ReconcileDispatch(ctx, f.owner, premature); !errors.Is(err, p.InvalidTransition) {
					t.Fatal("assigned attempt released without stop", err)
				}
			}
			if phase == "expired" {
				proof.To, proof.ConfirmedProcess = p.Expired, "not_started"
			}
			before := snapshot(t, f.s)
			for _, actor := range []string{"", strings.Repeat("f", 64), f.runner} {
				if err := f.s.StopDispatch(ctx, actor, f.grant.TaskID); !errors.Is(err, i.Denied) {
					t.Fatal("stop accepted non-owner", err)
				}
				if err := f.s.ReconcileDispatch(ctx, actor, proof); !errors.Is(err, i.Denied) {
					t.Fatal("release accepted non-owner", err)
				}
			}
			if !reflect.DeepEqual(before, snapshot(t, f.s)) {
				t.Fatal("unauthorized request changed attempt")
			}
			if err := f.s.StopDispatch(ctx, f.owner, f.grant.TaskID); err != nil {
				t.Fatal(err)
			}
			stopped := snapshot(t, f.s)
			if stopped.Tasks[0].Attempt.State != wantState || stopped.Tasks[0].Attempt.Revision != 2 || stopped.Tasks[0].State != p.TaskReconciling || len(stopped.Events) != 2 {
				t.Fatal("stop state/event missing", stopped)
			}
			if phase == "unknown" {
				if !reflect.DeepEqual(before, stopped) {
					t.Fatal("stop changed unknown attempt")
				}
			} else {
				event := stopped.Events[1]
				if event.Revision != 2 || event.Message.To != p.Stopping || p.CheckTransition(event.Message, v.Assignment.Identity, p.Assigned, 1) != p.OK {
					t.Fatal("stop event does not match transition", event)
				}
			}
			if err := f.s.StopDispatch(ctx, f.owner, f.grant.TaskID); err != nil {
				t.Fatal(err)
			}
			other := otherTaskRequest(t, f)
			for _, change := range []func(*Reconciliation){
				func(r *Reconciliation) { *r = Reconciliation{} },
				func(r *Reconciliation) { r.ConfirmedProcess = "" },
				func(r *Reconciliation) { r.ConfirmedProcess = "unknown" },
				func(r *Reconciliation) { r.RemoteWork = "" },
				func(r *Reconciliation) { r.RemoteWork = "unknown" },
				func(r *Reconciliation) { r.LaunchFenced = false },
				func(r *Reconciliation) { r.ArtifactsPreserved = false },
				func(r *Reconciliation) { r.EvidenceDigest = "" },
				func(r *Reconciliation) { r.EvidenceDigest = strings.Repeat("z", 64) },
				func(r *Reconciliation) { r.Identity.Generation = newID() },
				func(r *Reconciliation) { r.Identity.TaskID = newID() },
				func(r *Reconciliation) { r.Identity.AttemptID = newID() },
				func(r *Reconciliation) { r.Identity.Epoch++ },
				func(r *Reconciliation) { r.ExpectedRevision-- },
				func(r *Reconciliation) { r.ExpectedRevision++ },
				func(r *Reconciliation) { r.To = p.Succeeded },
			} {
				bad := proof
				change(&bad)
				if err := f.s.ReconcileDispatch(ctx, f.owner, bad); err == nil {
					t.Fatal("invalid proof released reservation", bad)
				}
			}
			if !reflect.DeepEqual(stopped, snapshot(t, f.s)) {
				t.Fatal("duplicate stop or invalid proof changed attempt/events")
			}
			retained, err := f.s.Assignment(ctx, v.ID)
			if err != nil || !reflect.DeepEqual(retained, v) {
				t.Fatal("stop changed reservation", err)
			}
			_, err = f.s.Delivery(ctx, f.runner, v.ID)
			requireReason(t, err, "reconciliation_required")
			_, err = f.s.Dispatch(ctx, other)
			requireReason(t, err, "concurrency_ceiling")
			for range 2 {
				if err := f.s.ReconcileDispatch(ctx, f.owner, proof); err != nil {
					t.Fatal(err)
				}
			}
			terminal := snapshot(t, f.s)
			if terminal.Tasks[0].Attempt.State != proof.To || terminal.Tasks[0].Attempt.Revision != 3 || len(terminal.Events) != 3 {
				t.Fatal("terminal state/event missing", terminal)
			}
			if err := f.s.StopDispatch(ctx, f.owner, f.grant.TaskID); err != nil {
				t.Fatal(err)
			}
			proof.EvidenceDigest = strings.Repeat("b", 64)
			if err := f.s.ReconcileDispatch(ctx, f.owner, proof); !errors.Is(err, p.IdentityConflict) {
				t.Fatal("conflicting terminal proof accepted", err)
			}
			if !reflect.DeepEqual(terminal, snapshot(t, f.s)) {
				t.Fatal("terminal history changed")
			}
			retained, err = f.s.Assignment(ctx, v.ID)
			if err != nil || !retained.Released || !reflect.DeepEqual(retained.Assignment, v.Assignment) {
				t.Fatal("release lost assignment", err)
			}
			pending, err := f.s.PendingAssignments(ctx, "", 128)
			if err != nil || len(pending) != 0 {
				t.Fatal("released outbox still pending", pending, err)
			}
			f.request.ID = newID()
			_, err = f.s.Dispatch(ctx, f.request)
			requireReason(t, err, "stopped")
			if _, err := f.s.Dispatch(ctx, other); err != nil {
				t.Fatal("terminal proof did not release global capacity", err)
			}
		})
	}
}

func TestDispatchConcurrentStopReconcile(t *testing.T) {
	for _, phase := range []string{"assigned", "stopping", "unknown"} {
		t.Run(phase, func(t *testing.T) {
			f := dispatchFixtureFor(t, nil)
			v := admitted(t, f)
			proof := reconciliation(v)
			if phase == "stopping" {
				if err := f.s.StopDispatch(ctx, f.owner, f.grant.TaskID); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "unknown" {
				proof = recoverDispatch(t, &f, v)
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			var result error
			for range 4 {
				wg.Go(func() {
					<-start
					if err := f.s.StopDispatch(ctx, f.owner, f.grant.TaskID); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Go(func() { <-start; result = f.s.ReconcileDispatch(ctx, f.owner, proof) })
			close(start)
			wg.Wait()
			if result != nil && (phase != "assigned" || !errors.Is(result, p.RevisionConflict)) {
				t.Fatal("unexpected concurrent reconciliation refusal", result)
			}
			if err := f.s.ReconcileDispatch(ctx, f.owner, proof); err != nil {
				t.Fatal(err)
			}
			view := snapshot(t, f.s)
			if view.Tasks[0].Attempt.State != p.Cancelled || view.Tasks[0].Attempt.Revision != 3 || len(view.Events) != 3 {
				t.Fatal("concurrent stop/reconcile changed revision twice", view)
			}
			retained, err := f.s.Assignment(ctx, v.ID)
			if err != nil || !retained.Released {
				t.Fatal("concurrent terminal proof did not release", err)
			}
			f.request.ID = newID()
			_, err = f.s.Dispatch(ctx, f.request)
			requireReason(t, err, "stopped")
		})
	}
}

func TestDispatchReleaseAndTaskCeilings(t *testing.T) {
	for _, ceiling := range []string{"attempt", "retry", "requests", "subattempts", "output", "cost", "duration", "wall", "native"} {
		t.Run(ceiling, func(t *testing.T) {
			f := dispatchFixtureFor(t, func(f *dispatchFixture) {
				switch ceiling {
				case "attempt":
					f.grant.Envelope.Budgets.Attempts = 1
				case "retry":
					f.grant.Envelope.Budgets.Retries = 0
				case "requests":
					f.grant.Envelope.Budgets.Requests = 1
				case "subattempts":
					f.grant.Envelope.Budgets.Subattempts = 2
				case "output":
					f.grant.Envelope.Budgets.ProviderOutputTokens = 32
				case "cost":
					cost := int64(100)
					f.grant.Envelope.Budgets.ProviderCostMicros = &cost
				case "duration":
					f.grant.Envelope.Budgets.TotalMS, f.grant.Envelope.Budgets.AttemptMS = 15000, 10000
				case "native":
					f.grant.Envelope.Routes[0].LimitsProfile, f.grant.Envelope.Routes[0].LimitsAuthority = "native-subscription-local-v1", "operator-native"
					f.grant.Envelope.Routes[0].Targets[1].Billing = "subscription"
					f.grant.Envelope.Budgets.ProviderOutputTokens, f.grant.Envelope.Budgets.ProviderCostMicros = 0, nil
					f.facts.Route = f.grant.Envelope.Routes[0]
					f.request.Decision.Selected = f.facts.Route
					f.facts.LocalEnvelope = cloneGrant(t, f.grant).Envelope
					f.facts.Paths[1].Target = f.facts.Route.Targets[1]
					f.grant.Envelope.Budgets.Retries = 0
				}
			})
			v := admitted(t, f)
			proof := recoverDispatch(t, &f, v)
			bad := proof
			bad.RemoteWork = "unknown"
			requireReason(t, f.s.ReconcileDispatch(ctx, f.owner, bad), "reconciliation_required")
			if err := f.s.ReconcileDispatch(ctx, f.runner, proof); !errors.Is(err, i.Denied) {
				t.Fatal("runner self-released", err)
			}
			for range 2 {
				if err := f.s.ReconcileDispatch(ctx, f.owner, proof); err != nil {
					t.Fatal(err)
				}
			}
			stored, err := f.s.Assignment(ctx, v.ID)
			if err != nil || !stored.Released {
				t.Fatal("release absent", err)
			}
			if ceiling == "wall" {
				// Persisted wall-age contract, not a source probe. Shorten total bound via
				// fresh grant to avoid time sleeps, while retaining consumed allowance.
				f.grant.ID, f.grant.Revision = newID(), 2
				f.grant.Envelope.Budgets.TotalMS, f.grant.Envelope.Budgets.AttemptMS = 10000, 10000
				f.grant = approve(t, f.s, v.Request.GrantID, f.grant)
				f.request.Request.GrantID, f.request.Request.GrantRevision = f.grant.ID, 2
				f.request.Request.Envelope.Budgets.TotalMS, f.request.Request.Envelope.Budgets.AttemptMS = 10000, 10000
			}
			f.request.ID = newID()
			_, err = f.s.Dispatch(ctx, f.request)
			reason := "attempt_ceiling"
			if ceiling == "requests" || ceiling == "subattempts" || ceiling == "output" || ceiling == "cost" || ceiling == "duration" {
				reason = "budget_exhausted"
			}
			if ceiling == "wall" {
				reason = "duration_ceiling"
			}
			requireReason(t, err, reason)
			dispatchRows(t, f.s, 1)
		})
	}
	f := dispatchFixtureFor(t, nil)
	first := admitted(t, f)
	proof := recoverDispatch(t, &f, first)
	if err := f.s.ReconcileDispatch(ctx, f.owner, proof); err != nil {
		t.Fatal(err)
	}
	f.request.ID = newID()
	second := admitted(t, f)
	if second.Assignment.Identity.Epoch != 2 || second.Assignment.Identity.AttemptID == first.Assignment.Identity.AttemptID {
		t.Fatal("replacement reused epoch/attempt")
	}
	view := snapshot(t, f.s)
	if len(view.Tasks) != 1 || view.Tasks[0].Attempt.Identity != second.Assignment.Identity {
		t.Fatal("snapshot selected stale attempt")
	}
	dispatchRows(t, f.s, 2)
}

func TestDispatchRollbackRestartAndDiskFull(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.s.Dispatch(cancelled, f.request); err == nil {
		t.Fatal("cancelled transaction")
	}
	sqlExec(t, f.s, "CREATE TRIGGER interrupt BEFORE INSERT ON dispatches BEGIN SELECT RAISE(ABORT,'interrupted'); END")
	if _, err := f.s.Dispatch(ctx, f.request); err == nil {
		t.Fatal("failed commit returned assignment")
	}
	dispatchRows(t, f.s, 0)
	sqlExec(t, f.s, "DROP TRIGGER interrupt")
	sqlExec(t, f.s, "CREATE TABLE pressure(payload BLOB) STRICT; CREATE TRIGGER full_dispatch BEFORE INSERT ON dispatches BEGIN INSERT INTO pressure VALUES(zeroblob(1048576)); END")
	var pages int
	if err := f.s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec("PRAGMA max_page_count=" + strconv.Itoa(pages)); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.Dispatch(ctx, f.request)
	var full *sqlite.Error
	if !errors.As(err, &full) || full.Code() != 13 {
		t.Fatal("expected SQLITE_FULL", err)
	}
	dispatchRows(t, f.s, 0)
	sqlExec(t, f.s, "DROP TRIGGER full_dispatch; PRAGMA max_page_count=1073741823")
	v := admitted(t, f)
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, f.s.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	pending, err := reopened.PendingAssignments(ctx, "", 128)
	if err != nil || !reflect.DeepEqual(pending, []string{v.ID}) {
		t.Fatal("restart lost outbox discovery", pending, err)
	}
	replay, err := reopened.Dispatch(ctx, f.request)
	if err != nil || !reflect.DeepEqual(replay.Assignment, v.Assignment) {
		t.Fatal("restart lost assignment", err)
	}
	_, err = reopened.Delivery(ctx, f.runner, f.request.ID)
	requireReason(t, err, "reconciliation_required")
	f.request.ID = newID()
	_, err = reopened.Dispatch(ctx, f.request)
	requireReason(t, err, "current_assignment")
}

func TestDispatchProcessCrash(t *testing.T) {
	for _, mode := range []string{"dispatch-crash", "dispatch-commit"} {
		t.Run(mode, func(t *testing.T) {
			f := dispatchFixtureFor(t, nil)
			body, _ := json.Marshal(f.request)
			t.Setenv("GAFFER_OWNED_TEST_DISPATCH", string(body))
			t.Setenv("GAFFER_OWNED_TEST_ARTIFACTS", f.artifacts)
			if err := f.s.Close(); err != nil {
				t.Fatal(err)
			}
			want := "interrupted"
			if mode == "dispatch-commit" {
				want = "committed"
			}
			child(t, f.s.dir, mode, want, true)
			r, err := Open(ctx, f.s.dir, f.artifacts)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if mode == "dispatch-crash" {
				dispatchRows(t, r, 0)
				sqlExec(t, r, "DROP TRIGGER pause_write")
			} else {
				dispatchRows(t, r, 1)
			}
			v, err := r.Dispatch(ctx, f.request)
			if err != nil {
				t.Fatal(err)
			}
			if v.Assignment.AssignmentID != dispatchID(f.request.ID, "assignment") || v.Assignment.Identity.AttemptID != dispatchID(f.request.ID, "attempt") {
				t.Fatal("crash replay allocated different identity")
			}
			dispatchRows(t, r, 1)
		})
	}
}

func TestDispatchSchemaFiveMigration(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	// Actual v5 attempts/events layout with retained protocol history.
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(f.s.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(dropDispatchSchema + `DROP TABLE events; DROP TABLE attempts; DROP TABLE tasks; DROP TABLE metadata;` + schema + `ALTER TABLE metadata ADD COLUMN artifacts_dir TEXT NOT NULL DEFAULT ''; CREATE TRIGGER events_no_update BEFORE UPDATE ON events BEGIN SELECT RAISE(ABORT,'events are append-only'); END; CREATE TRIGGER events_no_delete BEFORE DELETE ON events BEGIN SELECT RAISE(ABORT,'events are append-only'); END; PRAGMA user_version=5;`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO metadata VALUES(1,?,?,'')", f.s.meta.Generation, newID()); err != nil {
		t.Fatal(err)
	}
	m := assignment(f.s)
	if _, err := db.Exec("INSERT INTO tasks VALUES(?,'ready')", m.Identity.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO attempts VALUES(?,?,1,'assigned',1,?)", m.Identity.AttemptID, m.Identity.TaskID, m.AssignmentID); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	if _, err := db.Exec("INSERT INTO events(message_id,attempt_id,task_id,epoch,revision,message) VALUES(?,?,?,1,1,?)", m.MessageID, m.Identity.AttemptID, m.Identity.TaskID, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, f.s.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.meta.SchemaVersion != 8 {
		t.Fatal(r.meta)
	}
	view := snapshot(t, r)
	if len(view.Tasks) != 1 || view.Tasks[0].Attempt.State != p.Unknown || len(view.Events) != 2 || !reflect.DeepEqual(view.Events[0].Message, m) {
		t.Fatal("migration lost retained event or restart fencing")
	}
	if _, err := r.Authenticate(ctx, f.owner); err != nil {
		t.Fatal("lost migrated identity", err)
	}
	if _, err := r.ExecutionGrant(ctx, f.grant.ID); err != nil {
		t.Fatal("lost migrated authority", err)
	}
}

// Keep subprocess dispatch payload entirely within owned test state.
func ownedDispatch(t *testing.T, s *Store) {
	t.Helper()
	var request DispatchRequest
	if err := json.Unmarshal([]byte(os.Getenv("GAFFER_OWNED_TEST_DISPATCH")), &request); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(ctx, request); err != nil {
		t.Fatal(err)
	}
}
