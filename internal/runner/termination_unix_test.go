//go:build linux || darwin

package runner

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	p "github.com/korallis/letmecook/schemas/execution"
)

func cancelMessage(f *fixture) p.Message {
	return p.Message{Version: p.FencedVersion, MessageID: id(), Kind: "cancel", Identity: f.d.Assignment.Identity, StopID: id(), RunnerBoot: f.r.Status().RunnerBoot, DaemonBoot: f.o.Session.DaemonBoot}
}

// Only tests launch processes. Both actual child PIDs are read from the shell
// after fork and checked to belong to its process group before testing stop.
func subprocessTree(t *testing.T, ignoreTERM bool) (int, []int, <-chan error) {
	t.Helper()
	script := `sleep 60 & first=$!; sleep 60 & second=$!; printf '%s %s\n' "$first" "$second"; wait`
	if ignoreTERM {
		script = `trap '' TERM; ` + script
	}
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	group := cmd.Process.Pid
	done := make(chan error, 1)
	t.Cleanup(func() { _ = syscall.Kill(-group, syscall.SIGKILL) })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var first, second int
	if _, err := fmt.Sscan(line, &first, &second); err != nil {
		t.Fatal(err)
	}
	children := []int{first, second}
	for _, pid := range children {
		pg, err := syscall.Getpgid(pid)
		if err != nil || pg != group {
			t.Fatal("child escaped test group", pid, pg, err)
		}
	}
	go func() { done <- cmd.Wait() }()
	return group, children, done
}

func TestObservedProcessTreeTerminationWithinFiveSeconds(t *testing.T) {
	for _, escalate := range []bool{false, true} {
		t.Run(map[bool]string{false: "SIGTERM", true: "SIGKILL-escalation"}[escalate], func(t *testing.T) {
			f := setup(t)
			accept(t, f)
			// Pending lease cannot survive a connected cancel, even if a reply is late.
			req, err := f.r.RequestLease(f.o.Session)
			if err != nil {
				t.Fatal(err)
			}
			partial := filepath.Join(filepath.Dir(f.path), "partial.patch")
			if err := os.WriteFile(partial, []byte("retained partial work\n"), 0600); err != nil {
				t.Fatal(err)
			}
			group, children, done := subprocessTree(t, escalate)
			cancel := cancelMessage(f)
			started := time.Now()
			v, err := f.r.TerminateProcessGroup(f.o.Session, cancel, group, TerminationBounds{Grace: 100 * time.Millisecond, Timeout: 5 * time.Second})
			elapsed := time.Since(started)
			if err != nil {
				t.Fatal(v, err)
			}
			if v.Stage != c.TerminationObserved || v.Terminated == nil || v.Measurement.Validate(true) != nil || v.Measurement.Escalated != escalate || elapsed >= 5*time.Second {
				t.Fatal("unobserved or late termination", v, elapsed)
			}
			if v.Measurement.RequestToAckNS+*v.Measurement.AckToObservedNS >= int64(5*time.Second) {
				t.Fatal("measured stop exceeded bound")
			}
			t.Logf("request→ack %.3f ms; ack→observed %.3f ms; total call %.3f ms; escalated=%v", float64(v.Measurement.RequestToAckNS)/1e6, float64(*v.Measurement.AckToObservedNS)/1e6, float64(elapsed)/float64(time.Millisecond), v.Measurement.Escalated)
			for _, pid := range append(children, group) {
				if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
					t.Fatal("surviving tree member", pid, err)
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("parent not reaped")
			}
			if v.Terminated.EvidenceDigest != terminationDigest(v) || v.Terminated.RemoteWork != "unknown" || p.CheckSession(*v.Terminated, cancel.Identity, p.FencedVersion) != p.OK {
				t.Fatal("incorrect evidence", v)
			}
			records := f.r.log.Records()
			if len(records) != 6 {
				t.Fatal("missing requested/ack/observed records", len(records))
			}
			if !strings.Contains(string(records[3]), `"stage":"stop_requested"`) || strings.Contains(string(records[3]), `"kind":"terminated"`) || !strings.Contains(string(records[4]), `"stage":"stop_acknowledged"`) {
				t.Fatal("signal intent conflated with observation")
			}
			replay, err := f.r.TerminateProcessGroup(f.o.Session, cancel, group, TerminationBounds{Grace: 100 * time.Millisecond, Timeout: 5 * time.Second})
			if err != nil || !reflect.DeepEqual(v, replay) || len(f.r.log.Records()) != len(records) {
				t.Fatal("replay signaled reused PID", err)
			}
			if err := apply(f.r, f.o.Session, reply(req)); !errors.Is(err, ErrStopped) {
				t.Fatal("late lease resumed cancelled work", err)
			}
			if !errors.Is(f.r.Launch(), ErrExecutionDisabled) {
				t.Fatal("launch enabled")
			}
			if err := f.r.Close(); err != nil {
				t.Fatal(err)
			}
			r, err := Open(f.path, f.o)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			status := r.Status()
			if !status.StopRequired || !status.Quarantined || status.Termination == nil || !reflect.DeepEqual(*status.Termination, v) {
				t.Fatal("restart lost history", status)
			}
			if _, err := r.TerminateProcessGroup(f.o.Session, cancel, group, TerminationBounds{Grace: time.Millisecond, Timeout: time.Second}); !errors.Is(err, p.BootMismatch) {
				t.Fatal("restart reused process identity", err)
			}
			data, err := os.ReadFile(partial)
			if err != nil || string(data) != "retained partial work\n" {
				t.Fatal("partial artifact lost", err)
			}
		})
	}
}

func TestTerminationUnconfirmedAndJournalFailure(t *testing.T) {
	f := setup(t)
	accept(t, f)
	cancel := cancelMessage(f)
	own, err := syscall.Getpgid(0)
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.r.TerminateProcessGroup(f.o.Session, cancel, own, TerminationBounds{Grace: time.Millisecond, Timeout: time.Second})
	if !errors.Is(err, ErrTerminationUnconfirmed) || v.Stage != c.StopAcknowledged || v.Terminated != nil || v.Measurement.ObservedAt != nil {
		t.Fatal("unsafe group confirmed", v, err)
	}
	if !f.r.Status().StopRequired || !f.r.Status().Quarantined {
		t.Fatal("unconfirmed stop cleared quarantine")
	}
	if err := f.r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(f.path, f.o)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Status().Termination.Terminated != nil {
		t.Fatal("restart fabricated termination")
	}
	f = setup(t)
	accept(t, f)
	cancel = cancelMessage(f)
	if err := f.r.log.Close(); err != nil {
		t.Fatal(err)
	}
	if v, err := f.r.TerminateProcessGroup(f.o.Session, cancel, own, TerminationBounds{Grace: time.Millisecond, Timeout: time.Second}); err == nil || v.Terminated != nil {
		t.Fatal("unavailable journal acknowledged", v, err)
	}
}

func TestCancelSessionIdentityAndReplayConflicts(t *testing.T) {
	f := setup(t)
	accept(t, f)
	cancel := cancelMessage(f)
	own, err := syscall.Getpgid(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*p.Message){func(m *p.Message) { m.Version = p.Version }, func(m *p.Message) { m.RunnerBoot = id() }, func(m *p.Message) { m.DaemonBoot = id() }, func(m *p.Message) { m.Identity.Epoch++ }, func(m *p.Message) { m.StopID = "invalid" }} {
		bad := cancel
		change(&bad)
		if _, err := f.r.TerminateProcessGroup(f.o.Session, bad, own, TerminationBounds{Grace: time.Millisecond, Timeout: time.Second}); err == nil {
			t.Fatal("bad cancel accepted")
		}
	}
	if len(f.r.log.Records()) != 2 {
		t.Fatal("invalid cancel persisted")
	}
	if _, err := f.r.TerminateProcessGroup(f.o.Session, cancel, own, TerminationBounds{Grace: time.Millisecond, Timeout: time.Second}); !errors.Is(err, ErrTerminationUnconfirmed) {
		t.Fatal(err)
	}
	cancel.StopID = id()
	if _, err := f.r.TerminateProcessGroup(f.o.Session, cancel, own, TerminationBounds{Grace: time.Millisecond, Timeout: time.Second}); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("cancel identity reused", err)
	}
}

func TestTerminationObservationWriteFailureAndTimeout(t *testing.T) {
	for _, mode := range []string{"observation-write-failure", "deadline-exhausted"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			accept(t, f)
			group, _, done := subprocessTree(t, true)
			bounds := TerminationBounds{Grace: 10 * time.Millisecond, Timeout: 5 * time.Second}
			if mode == "observation-write-failure" {
				f.r.beforeTerminationCommit = func() {
					if err := f.r.log.Close(); err != nil {
						t.Error(err)
					}
				}
			} else {
				bounds = TerminationBounds{Grace: time.Nanosecond, Timeout: 2 * time.Nanosecond}
			}
			v, err := f.r.TerminateProcessGroup(f.o.Session, cancelMessage(f), group, bounds)
			if err == nil || v.Terminated != nil || f.r.Status().Termination.Terminated != nil {
				t.Fatal("uncommitted/unobserved termination acknowledged", v, err)
			}
			if mode == "deadline-exhausted" && !errors.Is(err, ErrTerminationUnconfirmed) {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("kill escalation not attempted")
			}
			if err := f.r.Close(); err != nil {
				t.Fatal(err)
			}
			r, err := Open(f.path, f.o)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if r.Status().Termination.Stage != c.StopAcknowledged || r.Status().Termination.Terminated != nil || !r.Status().Quarantined {
				t.Fatal("reopen invented lost observation", r.Status())
			}
		})
	}
}
