package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/runner"
)

// TestGuardianJobCannotInheritControlDescriptor asks whether the untrusted job
// inherits the guardian's private control descriptor (fd 3). If it does, the job
// can write a fabricated GuardianReport that the supervisor decodes as guardian
// evidence.
func TestGuardianJobCannotInheritControlDescriptor(t *testing.T) {
	dir := t.TempDir()
	receipt := filepath.Join(dir, "guardian.json")
	// A forged report that satisfies runner.GuardianEvidence: empty pgid, no
	// escape, plausible timestamps, and a pid above 1.
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
	script := `printf '%s\n' "$FORGED" >&3 2>/dev/null && echo wrote-fd3 || echo fd3-unavailable; sleep 30`
	spec := runner.LaunchSpec{
		Argv:        []string{"/bin/sh", "-c", script},
		Env:         []string{"PATH=/usr/bin:/bin", "FORGED=" + string(body)},
		Cwd:         dir,
		DeadlineMS:  20000,
		GraceMS:     2000,
		KillMS:      5000,
		Rlimits:     runner.ResourceLimits{OpenFiles: 1024, FileBytes: 64 << 20, CPUSeconds: 30},
		ReceiptPath: receipt,
	}
	in, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer write.Close()
	control, childControl, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	jobOut, jobOutWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer jobOut.Close()
	defer jobOutWrite.Close()

	cmd := exec.Command(testBinary, "guardian")
	cmd.Stdin = in
	cmd.Stdout = jobOutWrite
	cmd.Stderr = jobOutWrite
	cmd.ExtraFiles = []*os.File{childControl}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	childControl.Close()
	jobOutWrite.Close()
	t.Cleanup(func() { write.Close(); cmd.Wait() })

	if err := json.NewEncoder(write).Encode(spec); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(control)
	var started runner.GuardianStarted
	if err := dec.Decode(&started); err != nil {
		t.Fatal("started frame:", err)
	}
	t.Logf("guardian reported job pid=%d pgid=%d", started.PID, started.PGID)

	// Read what the job printed about fd 3.
	go func() {
		buf := make([]byte, 4096)
		n, _ := jobOut.Read(buf)
		if n > 0 {
			t.Logf("job stdout: %q", string(buf[:n]))
		}
	}()

	// The second frame on the control pipe is supposed to be the guardian's own
	// report, produced only after it observed the group disappear. Anything that
	// arrives while the job is still alive came from the job.
	type result struct {
		report runner.GuardianReport
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		var rep runner.GuardianReport
		err := dec.Decode(&rep)
		ch <- result{rep, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatal("second frame decode:", r.err)
		}
		alive := syscall.Kill(-started.PGID, 0)
		t.Logf("second control frame while kill(-pgid,0)=%v: %+v", alive, r.report)
		if r.report.Cause == "forged-by-job" {
			if !errors.Is(alive, syscall.ESRCH) {
				t.Fatalf("JOB FORGED GUARDIAN EVIDENCE on fd 3 while its process group is still alive (kill(-%d,0)=%v): PGIDEmpty=%v Escaped=%v Cause=%q", started.PGID, alive, r.report.PGIDEmpty, r.report.Escaped, r.report.Cause)
			}
			t.Fatalf("job forged guardian evidence on fd 3: %+v", r.report)
		}
		t.Log("second frame came from the guardian, not the job; fd 3 appears private")
	case <-time.After(4 * time.Second):
		t.Log("no second control frame within 4s: fd 3 not writable by the job")
	}
}
