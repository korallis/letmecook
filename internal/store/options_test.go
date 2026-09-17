package store

import (
	"testing"

	sc "github.com/korallis/letmecook/internal/scheduler"
	p "github.com/korallis/letmecook/schemas/execution"
)

// developmentFacts turns the fixture's supported isolation profile into the M1
// development shape: native, unsupported, qualification "development", measured
// subset of controls. Dispatch input digests are rebound by dispatchFixtureFor.
func developmentFacts(f *dispatchFixture) {
	f.facts.Isolation.Kind = "native"
	f.facts.Isolation.Supported = false
	f.facts.Isolation.Qualification = "development"
	f.facts.Isolation.Controls = sc.DevelopmentIsolationControls()
}

func TestOpenWithOptionsThreadsAdmissionPolicy(t *testing.T) {
	f := dispatchFixtureFor(t, developmentFacts)
	// Open (zero Options) refuses the development profile at admission.
	_, err := f.s.Dispatch(ctx, f.request)
	requireReason(t, err, "development_isolation_refused")
	dispatchRows(t, f.s, 0)
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenWithOptions(ctx, f.s.dir, f.artifacts, Options{Admission: sc.AdmissionPolicy{DevelopmentProfiles: []string{"fixture-isolation"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := s.Dispatch(ctx, f.request)
	if err != nil {
		t.Fatal("allowed development profile refused", err)
	}
	if d.Facts.Isolation.Qualification != "development" || d.Facts.Isolation.Supported {
		t.Fatal("admitted facts must record the development qualification", d.Facts.Isolation)
	}
	// Delivery and acknowledgement recheck admission with the same policy.
	if _, err := s.Delivery(ctx, f.runner, d.ID); err != nil {
		t.Fatal(err)
	}
	ack := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newID(), Identity: d.Assignment.Identity, AssignmentID: d.Assignment.AssignmentID, RunnerBoot: f.facts.RunnerBoot, DaemonBoot: s.meta.DaemonBoot}
	if err := s.AcknowledgeAssignment(ctx, f.runner, d.ID, ack); err != nil {
		t.Fatal(err)
	}
	// The policy is process configuration, never persisted: a daemon restarted
	// without the flag refuses the same facts before it even reaches the
	// single-attempt reservation check.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	plain, err := Open(ctx, f.s.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	fresh := f.request
	fresh.ID = newID()
	_, err = plain.Dispatch(ctx, fresh)
	requireReason(t, err, "development_isolation_refused")
}
