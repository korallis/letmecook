package main

import (
	"context"
	"encoding/json"
	"errors"
	w "github.com/korallis/letmecook/internal/execwire"
	repo "github.com/korallis/letmecook/internal/repositories"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	h "github.com/korallis/letmecook/internal/harness"
	"github.com/korallis/letmecook/internal/runner"
)

// forgingHarness stands in for an adapter whose untrusted job writes a forged
// GuardianReport to descriptor 3. It configures the command before Wrap exactly
// as the runner README requires, and touches nothing afterwards.
type forgingHarness struct {
	h.Harness
	forged []byte
	t      *testing.T
}

func (a forgingHarness) Describe(context.Context) (h.Descriptor, error) {
	return h.Descriptor{Name: "fake", Version: "forge-v1", BinaryDigest: "0", Protocols: []string{"responses"}, StructuredOutput: true, Approval: true, Sandbox: true}, nil
}
func (a forgingHarness) Start(ctx context.Context, r h.RunRequest) (h.RunHandle, error) {
	// The job writes the forged report to fd 3, then stays alive so the process
	// group is provably NOT empty when the supervisor consumes that frame.
	cmd := exec.Command("/bin/sh", "-c", `printf '%s\n' "$FORGED" >&3 2>/dev/null && echo wrote-fd3 || echo no-fd3; sleep 30`)
	cmd.Dir = r.Workspace.Root
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + r.Workspace.PrivateHome, "FORGED=" + string(a.forged)}
	if err := r.Launcher.Wrap(cmd); err != nil {
		return h.RunHandle{}, err
	}
	if err := cmd.Start(); err != nil {
		return h.RunHandle{}, err
	}
	go cmd.Wait()
	return h.RunHandle{ID: r.Identity.AttemptID, PID: cmd.Process.Pid, PGID: cmd.Process.Pid}, nil
}

// TestSandboxJobCannotForgeGuardianEvidence drives the real
// supervisor launch path (PrepareLaunch -> Launch -> WaitGuardian ->
// GuardianEvidence) with a job that forges guardian evidence on fd 3.
func TestSandboxJobCannotForgeGuardianEvidence(t *testing.T) {
	f := newFixture(t, "hang")
	forged := runner.GuardianReport{
		GuardianStarted:  runner.GuardianStarted{PID: 4242, PGID: 4242, StartUnixNS: time.Now().UnixNano(), BoundedResources: true},
		ObservedUnixNS:   time.Now().Add(time.Second).UnixNano(),
		StopUnixNS:       time.Now().UnixNano(),
		StopToObservedNS: 1000,
		PGIDEmpty:        true,
		Escaped:          false,
		ExitCode:         0,
		Cause:            "forged-by-job",
	}
	body, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	f.s.harness = forgingHarness{Harness: f.s.harness, forged: body, t: t}

	// Minimal real launch sequence, reusing the fixture's dispatch and profile.
	d := f.d.dispatch
	dir := filepath.Join(f.s.cfg.StateDir, "attempts", d.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	r, err := runner.Create(filepath.Join(dir, "journal"), f.s.options())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ack, err := r.Accept(f.s.options().Session, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.message(f.ctx, r, d.ID, ack, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = f.s.lease(f.ctx, r, d.ID); err != nil {
		t.Fatal(err)
	}
	checkout, err := repo.Prepare(f.ctx, f.profile, f.local.Repository)
	if err != nil {
		t.Fatal(err)
	}
	exe := f.s.executable
	req := runner.LaunchRequest{Profile: f.s.profile, Harness: f.s.harness, Checkout: checkout, Executable: exe, RecoveryDir: dir, Brief: f.d.input.Brief, Settings: f.d.input.Settings}
	launch, err := r.PrepareLaunch(f.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	_ = launch
	m, err := f.s.transition(f.ctx, r, d, "assigned", "starting", 1, w.Evidence{Kind: "launch_intent", Workspace: launch.Workspace.Root, BoundaryPort: launch.BoundaryPort, Nonce: lastNonce(r)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.StartingAcknowledged(m); err != nil {
		t.Fatal(err)
	}
	if err = r.Launch(f.ctx, req); err != nil {
		t.Fatal(err)
	}
	runtime := r.Status().Runtime
	t.Logf("real job pid=%d pgid=%d guardian=%d", runtime.PID, runtime.PGID, runtime.GuardianPID)

	// The supervisor now waits for guardian evidence. A truthful guardian would
	// still be watching a live job; only the forged frame can arrive this fast.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := r.WaitGuardian(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || report.PID != 0 {
		t.Fatalf("forged control frame became evidence: %+v, %v", report, err)
	}
	if syscall.Kill(-runtime.PGID, 0) != nil {
		t.Fatal("probe job unexpectedly exited")
	}
	if r.Status().Guardian != nil {
		t.Fatal("untrusted report journalled")
	}
	if _, err := r.GuardianEvidence(f.s.localCancel(d, "containment_unconfirmed")); !errors.Is(err, runner.ErrTerminationUnconfirmed) {
		t.Fatalf("forged terminated evidence: %v", err)
	}
	r.StopGuardian()
	wait, stop := context.WithTimeout(context.Background(), 7*time.Second)
	defer stop()
	if report, err := r.WaitGuardian(wait); err != nil || !report.PGIDEmpty || report.Cause == "forged-by-job" {
		t.Fatalf("trusted receipt unavailable: %+v %v", report, err)
	}
}
