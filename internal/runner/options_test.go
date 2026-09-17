package runner

import (
	"errors"
	"path/filepath"
	"testing"

	g "github.com/korallis/letmecook/internal/authority"
	sc "github.com/korallis/letmecook/internal/scheduler"
	p "github.com/korallis/letmecook/schemas/execution"
)

// rebindDevelopment switches the fixture's isolation facts to the M1 development
// shape and rebinds every digest the dispatch carries, as the daemon would.
func rebindDevelopment(t *testing.T, f *fixture) {
	t.Helper()
	f.local.Isolation.Kind = "native"
	f.local.Isolation.Supported = false
	f.local.Isolation.Qualification = "development"
	f.local.Isolation.Controls = sc.DevelopmentIsolationControls()
	fh, err := sc.Digest(f.local)
	if err != nil {
		t.Fatal(err)
	}
	f.d.Decision.Eligibility = g.Revision{Number: 1, SHA256: fh}
	dh, err := sc.Digest(f.d.Decision)
	if err != nil {
		t.Fatal(err)
	}
	f.d.Request.Envelope.RouteDecision = g.Revision{Number: 1, SHA256: dh}
	f.d.Facts = clone(f.local)
	ih, err := inputDigest(f.d)
	if err != nil {
		t.Fatal(err)
	}
	f.d.Assignment.InputDigest = ih
	f.d.Assignment.Route.DecisionDigest = dh
}

func refused(t *testing.T, err error, code string) {
	t.Helper()
	var r *g.Refusal
	if !errors.As(err, &r) || r.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

// journalPath is a fresh, symlink-free private journal directory name.
func journalPath(t *testing.T, name string) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(base, name)
}

func TestOptionsBootAndAdmission(t *testing.T) {
	f := setup(t)
	rebindDevelopment(t, f)
	// A supervisor without its own policy refuses what the daemon admitted.
	_, err := f.r.Accept(f.o.Session, f.d)
	refused(t, err, "development_isolation_refused")
	if f.r.Status().AssignmentID != "" {
		t.Fatal("refused assignment journaled as accepted")
	}
	o := f.o
	o.Admission = sc.AdmissionPolicy{DevelopmentProfiles: []string{"fixture-isolation"}}
	o.Boot = f.local.RunnerBoot // the boot this process published in its facts
	path := journalPath(t, "runner-dev")
	r, err := Create(path, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status().RunnerBoot != o.Boot {
		t.Fatal("Create ignored the supervisor boot", r.Status())
	}
	ack, err := r.Accept(o.Session, f.d)
	if err != nil || ack.RunnerBoot != o.Boot || ack.Kind != "accept" {
		t.Fatal(ack, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// Open never resumes the published boot: every reopen mints a restart boot
	// and quarantines, even with Options.Boot set.
	reopened, err := Open(path, o)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if s := reopened.Status(); s.RunnerBoot == o.Boot || !s.Quarantined || !s.StopRequired || s.Reason != "supervisor_restart" {
		t.Fatal("reopen reused the published boot or skipped quarantine", s)
	}
	bad := o
	bad.Boot = "not-a-uuid"
	if _, err := Create(journalPath(t, "runner-bad"), bad); !errors.Is(err, p.Malformed) {
		t.Fatal("invalid boot accepted", err)
	}
	other := o
	other.Admission = sc.AdmissionPolicy{DevelopmentProfiles: []string{"some-other-dev"}}
	strict, err := Create(journalPath(t, "runner-other"), other)
	if err != nil {
		t.Fatal(err)
	}
	defer strict.Close()
	_, err = strict.Accept(other.Session, f.d)
	refused(t, err, "development_isolation_refused")
}
