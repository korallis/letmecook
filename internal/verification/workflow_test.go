package verification

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/isolation"
	"github.com/korallis/letmecook/internal/scheduler"
)

func envelopeFor(base string) g.Envelope {
	h := strings.Repeat("a", 64)
	rev := g.Revision{Number: 1, SHA256: h}
	now := time.Now().UnixMilli()
	return g.Envelope{Repository: "fixture", BaseCommit: base, Brief: rev, Plan: rev, RouteDecision: rev, CriterionIDs: []string{"c1"}, TaskKinds: []string{"code"}, Paths: []string{"a.txt", "binary.bin", "deleted.txt", "nested/new.txt"}, Operations: []string{"read", "verify", "write"}, Systems: []string{}, Runners: []string{ID()}, Routes: []g.Route{{RouteRef: "worker", ProfileRef: "default", RouteRevision: 1, Policy: rev, RouterBuild: h, GraphDigest: h, Evidence: rev, Harness: "fake", Protocol: "responses", SettingsDigest: h, Isolation: "macos-sandbox-exec-dev", LimitsProfile: "strict-provider-output-v1", LimitsAuthority: "operator-strict", Targets: []g.Target{{Provider: "fixture", Model: "fixture", Billing: "subscription"}}}}, Selection: "pinned", Budgets: g.Budgets{Requests: 1, Attempts: 1, Subattempts: 1, Concurrency: 1, RequestBytes: 1024, ResponseBytes: 1024, TotalMS: 1000, AttemptMS: 1000, FirstOutputMS: 500, IdleMS: 500, ProviderOutputTokens: 1}, NotBeforeMS: now - 1, ExpiresMS: now + 60000}
}
func TestWorkflowEnvelopeViolationsPersistAndBlockVerification(t *testing.T) {
	for _, kind := range []string{"allowed", "path", "readonly", "base"} {
		t.Run(kind, func(t *testing.T) {
			src, req := fixture(t)
			envelope := envelopeFor(req.Candidate.BaseCommit)
			switch kind {
			case "path":
				envelope.Paths = []string{"a.txt"}
			case "readonly":
				envelope.Operations = []string{"read", "verify"}
			case "base":
				envelope.BaseCommit = strings.Repeat("a", 40)
			}
			req.Envelope = &envelope
			report := runTest(t, src, req, newTestOnlyProfile())
			if kind == "allowed" {
				if !Evaluate(report, req.Candidate).Verified {
					t.Fatal(report)
				}
			} else if report.RecreationFailure != "envelope_violation" || len(report.Evidence) != 0 || Evaluate(report, req.Candidate).Verified {
				t.Fatal("scope violation passed", report)
			}
		})
	}
}
func TestWorkflowEnvelopeSeesUntrackedIgnoredDeletedAndSymlink(t *testing.T) {
	for _, kind := range []string{"untracked", "ignored", "deleted", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			_, req := fixture(t)
			e := envelopeFor(req.Candidate.BaseCommit)
			e.Paths = []string{"a.txt"}
			root := req.TrustedRepo
			switch kind {
			case "untracked":
				os.WriteFile(filepath.Join(root, "untracked"), []byte("x"), 0600)
			case "ignored":
				os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte("ignored\n"), 0600)
				os.WriteFile(filepath.Join(root, "ignored"), []byte("x"), 0600)
			case "deleted":
				os.Remove(filepath.Join(root, "deleted.txt"))
			case "symlink":
				os.Symlink("a.txt", filepath.Join(root, "link"))
			}
			var refusal *g.Refusal
			if err := CheckEnvelope(root, e.BaseCommit, e); !errors.As(err, &refusal) || refusal.Code != "envelope_violation" {
				t.Fatal("missed", kind, err)
			}
		})
	}
}
func TestWorkflowDevelopmentReportLabels(t *testing.T) {
	src, req := fixture(t)
	report := runTest(t, src, req, newTestOnlyProfile())
	e := &report.Evidence[0]
	e.ProfileID = "macos-sandbox-exec-dev"
	e.Qualification = "development"
	e.ProfileDigest = strings.Repeat("a", 64)
	e.RuntimeDigest = strings.Repeat("b", 64)
	e.Environment.ExpectedConfinement = e.ProfileID
	e.Environment.ObservedConfinement = e.ProfileID
	report.Limitations = append(report.Limitations, DevelopmentLimitation)
	if err := report.Validate(); err != nil {
		t.Fatal("labelled report", err)
	}
	for _, kind := range []string{"qualification", "profile-digest", "runtime-digest", "confinement", "limitation"} {
		t.Run(kind, func(t *testing.T) {
			copy := report
			copy.Evidence = append([]Evidence(nil), report.Evidence...)
			switch kind {
			case "qualification":
				copy.Evidence[0].Qualification = ""
			case "profile-digest":
				copy.Evidence[0].ProfileDigest = ""
			case "runtime-digest":
				copy.Evidence[0].RuntimeDigest = ""
			case "confinement":
				copy.Evidence[0].Environment.ObservedConfinement = "other"
			case "limitation":
				copy.Limitations = nil
			}
			if copy.Validate() == nil {
				t.Fatal("unlabelled dev evidence accepted")
			}
		})
	}
}

// Deliberately lying profile exists only in tests; canaries must catch it before
// any trusted check is executed. This is not a selectable production executor.
type lyingDevelopment struct{}

func (lyingDevelopment) ID() string            { return "macos-sandbox-exec-dev" }
func (lyingDevelopment) Kind() string          { return "native" }
func (lyingDevelopment) Qualification() string { return "development" }
func (lyingDevelopment) Controls() []string    { return scheduler.DevelopmentIsolationControls() }
func (lyingDevelopment) Prepare(context.Context, isolation.Workspace) (isolation.Launcher, error) {
	return lyingLauncher{}, nil
}

type lyingLauncher struct{}

func (lyingLauncher) Wrap(*exec.Cmd) error { return nil }
func (lyingLauncher) Cleanup() error       { return nil }
func (lyingLauncher) Observation() isolation.Observation {
	return isolation.Observation{ProfileDigest: strings.Repeat("a", 64), RuntimeDigest: strings.Repeat("b", 64), Controls: scheduler.DevelopmentIsolationControls(), Limitations: []string{"test-only observation limitation"}}
}
func TestWorkflowCanaryDriftRetainsUnqualifiedRefusal(t *testing.T) {
	if _, err := NewDevelopmentProfile(isolation.Unqualified{}, time.Second, t.TempDir()); err == nil {
		t.Fatal("unqualified constructor accepted")
	}
	// /var/tmp is outside the launcher's /tmp and per-user temp exceptions;
	// this disposable explicit state root never writes into the real HOME.
	state, err := os.MkdirTemp("/var/tmp", "gaffer-verify-state-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(state)
	state, err = filepath.EvalSymlinks(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(t.TempDir(), "must-not-create-home"))
	profile, err := NewDevelopmentProfile(lyingDevelopment{}, time.Second, state)
	if err != nil {
		t.Fatal(err)
	}
	src, req := fixture(t)
	marker := filepath.Join(t.TempDir(), "must-not-run")
	req.Checks.Checks[0].Argv = []string{"/usr/bin/touch", marker}
	report := runTest(t, src, req, profile)
	if len(report.Evidence) != 1 || report.Evidence[0].ProfileID != "unqualified" || !report.Evidence[0].Environment.ConfinementDrift || report.Evidence[0].Refusal == nil || Evaluate(report, req.Candidate).Verified {
		t.Fatal(report)
	}
	if !slices.Contains(report.Limitations, "test-only observation limitation") {
		t.Fatal("measured profile limitations lost")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("trusted check ran after escape canary", err)
	}
	if _, err := os.Stat(os.Getenv("HOME")); !os.IsNotExist(err) {
		t.Fatal("HOME was touched", err)
	}
	entries, err := os.ReadDir(filepath.Join(state, "verification-canary"))
	if err != nil || len(entries) != 0 {
		t.Fatal("canary cleanup", entries, err)
	}
}
func TestWorkflowDevelopmentCommandTimeoutKillsGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 30 & wait")
	cmd.WaitDelay = time.Second
	start := time.Now()
	if err := runDevelopmentCommand(cmd); err == nil {
		t.Fatal("timed out group passed")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("unbounded termination")
	}
}

func TestDevelopmentCanaryRefusesUnsafeStateRoots(t *testing.T) {
	for _, state := range []string{"", "relative", "/tmp", "/private/tmp", t.TempDir()} {
		if _, err := NewDevelopmentProfile(lyingDevelopment{}, time.Second, state); err == nil {
			t.Fatal("unsafe state accepted", state)
		}
	}
	state, err := os.MkdirTemp("/var/tmp", "gaffer-canary-roots-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(state)
	state, err = filepath.EvalSymlinks(state)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(t.TempDir(), filepath.Join(state, "verification-canary")); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	w := isolation.Workspace{Root: workspace, PrivateHome: workspace, TempDir: workspace, RuntimeDir: workspace}
	if err = verificationCanary(context.Background(), lyingLauncher{}, w, state); err == nil {
		t.Fatal("symlink canary root accepted")
	}
	if err = os.Remove(filepath.Join(state, "verification-canary")); err != nil {
		t.Fatal(err)
	}
	w.Root = state
	if err = verificationCanary(context.Background(), lyingLauncher{}, w, state); err == nil {
		t.Fatal("canary inside workspace accepted")
	}
}
