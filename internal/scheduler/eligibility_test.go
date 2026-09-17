package scheduler

import (
	"errors"
	"strings"
	"testing"

	g "github.com/korallis/letmecook/internal/authority"
	repo "github.com/korallis/letmecook/internal/repositories"
)

const testRunner = "00000000-0000-4000-8000-000000000001"

// admissionFixture is a self-consistent request/decision/facts triple with a
// development isolation profile: Supported false, native, qualification
// "development", the measured development control subset only.
func admissionFixture(t *testing.T) (g.Request, Decision, Eligibility) {
	t.Helper()
	h := strings.Repeat("a", 64)
	rev := g.Revision{Number: 1, SHA256: h}
	route := g.Route{RouteRef: "worker-dev", ProfileRef: "gateway-local", RouteRevision: 1, Policy: rev, RouterBuild: h, GraphDigest: h, Evidence: rev, Harness: "fake", Protocol: "responses", SettingsDigest: h, Isolation: "macos-sandbox-exec-dev", LimitsProfile: "gateway-local-bounds-v1", LimitsAuthority: "operator", Targets: []g.Target{{Provider: "gateway-local", Model: "gpt-6-astra", Billing: "gateway-managed"}}}
	env := g.Envelope{Repository: "greeting", BaseCommit: strings.Repeat("b", 40), Brief: rev, Plan: rev, RouteDecision: rev, CriterionIDs: []string{"c1"}, TaskKinds: []string{"code"}, Paths: []string{"greeting.txt"}, Operations: []string{"read", "verify", "write"}, Systems: []string{}, Runners: []string{testRunner}, Routes: []g.Route{route}, Selection: "pinned", Budgets: g.Budgets{Requests: 5, Attempts: 1, Subattempts: 2, Retries: 0, Concurrency: 1, RequestBytes: 1024, ResponseBytes: 4096, TotalMS: 60000, AttemptMS: 30000, FirstOutputMS: 5000, IdleMS: 5000}, NotBeforeMS: 1, ExpiresMS: 1000000}
	facts := Eligibility{ID: "dev-pair", Revision: 1, Repository: repo.Selection{Repository: "greeting", Revision: 1, ProfileDigest: h, Remote: "https://example.invalid/greeting.git", BaseCommit: env.BaseCommit, RunnerRoot: repo.RunnerRoot{RunnerID: testRunner, Root: "/fixture/owned"}}, Enabled: true, RunnerBoot: "00000000-0000-4000-8000-000000000002", LocalPolicy: rev, LocalEnvelope: env, Config: rev, Route: route, Paths: []PathEvidence{{Target: route.Targets[0], Compatible: true, Capabilities: []string{}, ContextTokens: 8192, LocalBounds: true}}, Capabilities: []string{}, Isolation: IsolationProfile{ID: "macos-sandbox-exec-dev", Revision: rev, RuntimeDigest: h, ObservedDigest: h, Kind: "native", Supported: false, Controls: DevelopmentIsolationControls(), Qualification: "development"}, Capacity: Resources{CPU: 1000, MemoryBytes: 4096, DiskBytes: 8192, Processes: 8}, Concurrency: 1, RouterAuthenticated: true, Availability: "available", ValidUntilMS: 1000000}
	fh, err := Digest(facts)
	if err != nil {
		t.Fatal(err)
	}
	decision := Decision{ID: "00000000-0000-4000-8000-000000000003", Revision: 1, Assessment: Assessment{TaskID: "00000000-0000-4000-8000-000000000004", Brief: rev, Plan: rev, ContextDigest: h, Role: "worker", WorkClass: "code", Ambiguity: "low", Consequence: "low", Required: []string{}, Preferences: []string{}, EvidenceRefs: []string{"operator"}, Unknowns: []string{}, ContextTokens: 4096, AssessorVersion: "operator-v1", SelectorVersion: "deterministic-v1"}, EligibilityID: facts.ID, Eligibility: g.Revision{Number: 1, SHA256: fh}, Selected: route, RankedAlternatives: []string{}, Authorization: "operator", Reason: "pinned", Resources: Resources{CPU: 500, MemoryBytes: 2048, DiskBytes: 4096, Processes: 4}}
	dh, err := Digest(decision)
	if err != nil {
		t.Fatal(err)
	}
	env.RouteDecision = g.Revision{Number: 1, SHA256: dh}
	request := g.Request{GrantID: "00000000-0000-4000-8000-000000000005", TaskID: decision.Assessment.TaskID, GrantRevision: 1, Action: "execute", Envelope: env}
	return request, decision, facts
}

func code(t *testing.T, err error, want string) {
	t.Helper()
	var r *g.Refusal
	if !errors.As(err, &r) || r.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

func TestAdmissionPolicyGatesDevelopmentProfiles(t *testing.T) {
	request, decision, facts := admissionFixture(t)
	allow := AdmissionPolicy{DevelopmentProfiles: []string{"macos-sandbox-exec-dev"}}
	// Default and empty policies refuse; CheckDispatch is the empty policy.
	code(t, CheckDispatch(request, decision, facts, 100000), "development_isolation_refused")
	code(t, CheckDispatchWithPolicy(request, decision, facts, 100000, AdmissionPolicy{}), "development_isolation_refused")
	code(t, CheckDispatchWithPolicy(request, decision, facts, 100000, AdmissionPolicy{DevelopmentProfiles: []string{"other-dev"}}), "development_isolation_refused")
	if err := CheckDispatchWithPolicy(request, decision, facts, 100000, allow); err != nil {
		t.Fatal("allowed development profile refused", err)
	}
	// The admitted shape is exact: native, unsupported, development-qualified,
	// digests agreeing, route bound, measured subset present.
	rebind := func(mutate func(*Eligibility)) (g.Request, Decision, Eligibility) {
		t.Helper()
		r, d, f := admissionFixture(t)
		mutate(&f)
		fh, err := Digest(f)
		if err != nil {
			t.Fatal(err)
		}
		d.Eligibility = g.Revision{Number: 1, SHA256: fh}
		dh, err := Digest(d)
		if err != nil {
			t.Fatal(err)
		}
		r.Envelope.RouteDecision = g.Revision{Number: 1, SHA256: dh}
		return r, d, f
	}
	for name, tc := range map[string]struct {
		mutate func(*Eligibility)
		want   string
	}{
		"dedicated-vm-kind":  {func(f *Eligibility) { f.Isolation.Kind = "dedicated-vm" }, "isolation_unsupported"},
		"docker":             {func(f *Eligibility) { f.Isolation.DockerRequired = true }, "isolation_unsupported"},
		"missing-control":    {func(f *Eligibility) { f.Isolation.Controls = []string{"controlled-egress", "workspace-only"} }, "isolation_unsupported"},
		"digest-drift":       {func(f *Eligibility) { f.Isolation.ObservedDigest = strings.Repeat("c", 64) }, "isolation_drift"},
		"route-mismatch":     {func(f *Eligibility) { f.Isolation.ID = "macos-sandbox-exec-dev-2" }, "isolation_drift"},
		"supported-claim":    {func(f *Eligibility) { f.Isolation.Supported = true }, "malformed"},
		"unqualified-native": {func(f *Eligibility) { f.Isolation.Qualification = "" }, "malformed"},
	} {
		r, d, f := rebind(tc.mutate)
		policy := allow
		if name == "route-mismatch" {
			policy = AdmissionPolicy{DevelopmentProfiles: []string{"macos-sandbox-exec-dev-2"}}
		}
		code(t, CheckDispatchWithPolicy(r, d, f, 100000, policy), tc.want)
	}
	// Historical non-development records still need the full control set.
	r, d, f := rebind(func(f *Eligibility) {
		f.Isolation.ID = "historical-qualified-profile"
		f.Route.Isolation = f.Isolation.ID
		f.LocalEnvelope.Routes[0].Isolation = f.Isolation.ID
		f.Isolation.Qualification = ""
		f.Isolation.Supported = true
		f.Isolation.Controls = RequiredIsolationControls()
	})
	d.Selected = f.Route
	dhHistorical, _ := Digest(d)
	r.Envelope.Routes = []g.Route{f.Route}
	r.Envelope.RouteDecision = g.Revision{Number: 1, SHA256: dhHistorical}
	if err := CheckDispatch(r, d, f, 100000); err != nil {
		t.Fatal("supported profile refused", err)
	}
	// A development profile cannot claim a hard provider bound.
	r, d, f = admissionFixture(t)
	d.Assessment.HardCostBound = true
	dh, _ := Digest(d)
	r.Envelope.RouteDecision = g.Revision{Number: 1, SHA256: dh}
	code(t, CheckDispatchWithPolicy(r, d, f, 100000, allow), "hard_bound_unavailable")
}

func TestEligibilityQualificationValidation(t *testing.T) {
	_, _, facts := admissionFixture(t)
	if err := facts.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Eligibility){
		"unknown-qualification": func(f *Eligibility) { f.Isolation.Qualification = "production" },
		"test-only-supported":   func(f *Eligibility) { f.Isolation.Qualification = "test-only"; f.Isolation.Supported = true },
		"unqualified-supported": func(f *Eligibility) { f.Isolation.Qualification = "unqualified"; f.Isolation.Supported = true },
		"qualified-without-id":  func(f *Eligibility) { f.Isolation = IsolationProfile{Qualification: "development"} },
	} {
		bad := facts
		mutate(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatal(name, "accepted")
		} else {
			code(t, err, "malformed")
		}
	}
}

// An explicitly unqualified profile is never admitted by either entry point, with
// or without a policy, a Supported claim or a complete control set.
func TestUnqualifiedProfileNeverAdmitted(t *testing.T) {
	allow := AdmissionPolicy{DevelopmentProfiles: []string{"macos-sandbox-exec-dev"}}
	rebind := func(mutate func(*Eligibility)) (g.Request, Decision, Eligibility) {
		t.Helper()
		r, d, f := admissionFixture(t)
		mutate(&f)
		fh, err := Digest(f)
		if err != nil {
			t.Fatal(err)
		}
		d.Eligibility = g.Revision{Number: 1, SHA256: fh}
		dh, err := Digest(d)
		if err != nil {
			t.Fatal(err)
		}
		r.Envelope.RouteDecision = g.Revision{Number: 1, SHA256: dh}
		return r, d, f
	}
	for name, tc := range map[string]struct {
		mutate func(*Eligibility)
		want   string
	}{
		// The known development ID cannot be relabelled as unqualified.
		"unqualified-native-full-controls": {func(f *Eligibility) {
			f.Isolation.Qualification = "unqualified"
			f.Isolation.Controls = RequiredIsolationControls()
		}, "malformed"},
		"unqualified-dedicated-vm": {func(f *Eligibility) {
			f.Isolation.Qualification = "unqualified"
			f.Isolation.Kind = "dedicated-vm"
			f.Isolation.Controls = RequiredIsolationControls()
		}, "malformed"},
		// Fails Validate: a Supported claim cannot accompany an explicit qualification.
		"unqualified-supported-full-controls": {func(f *Eligibility) {
			f.Isolation.Qualification = "unqualified"
			f.Isolation.Supported = true
			f.Isolation.Controls = RequiredIsolationControls()
		}, "malformed"},
	} {
		r, d, f := rebind(tc.mutate)
		code(t, CheckDispatch(r, d, f, 100000), tc.want)
		code(t, CheckDispatchWithPolicy(r, d, f, 100000, AdmissionPolicy{}), tc.want)
		code(t, CheckDispatchWithPolicy(r, d, f, 100000, allow), tc.want)
		_ = name
	}
}

func TestDevelopmentIdentityRequiresHonestLabels(t *testing.T) {
	for _, qualification := range []string{"", "unqualified", "development", "test-only"} {
		for _, supported := range []bool{false, true} {
			_, _, facts := admissionFixture(t)
			facts.Isolation.Qualification, facts.Isolation.Supported = qualification, supported
			err := facts.Validate()
			if (err == nil) != (qualification == "development" && !supported) {
				t.Fatalf("qualification=%q supported=%t: %v", qualification, supported, err)
			}
		}
	}
}
