package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/korallis/letmecook/internal/closedjson"
	"github.com/korallis/letmecook/internal/isolation"
)

type ResourceLimits struct {
	OpenFiles  uint64 `json:"open_files"`
	FileBytes  uint64 `json:"file_bytes"`
	CPUSeconds uint64 `json:"cpu_seconds"`
}

// LaunchSpec is supervisor-private. No gateway credential is permitted in Env.
// DeadlineMS is remaining monotonic duration at send, not a wall-clock timestamp.
type LaunchSpec struct {
	Argv           []string       `json:"argv"`
	Env            []string       `json:"env"`
	Cwd            string         `json:"cwd"`
	SandboxProfile string         `json:"sandbox_profile"`
	DeadlineMS     int64          `json:"deadline_ms"`
	GraceMS        int64          `json:"grace_ms"`
	KillMS         int64          `json:"kill_ms"`
	Rlimits        ResourceLimits `json:"rlimits"`
	ReceiptPath    string         `json:"receipt_path"`
}
type GuardianStarted struct {
	PID              int    `json:"pid"`
	PGID             int    `json:"pgid"`
	StartUnixNS      int64  `json:"start_unix_ns"`
	StartToken       string `json:"start_token"`
	BoundedResources bool   `json:"bounded_resources"`
}
type GuardianReport struct {
	GuardianStarted
	ObservedUnixNS   int64  `json:"observed_unix_ns"`
	StopUnixNS       int64  `json:"stop_unix_ns"`
	StopToObservedNS int64  `json:"stop_to_observed_ns"`
	Escalated        bool   `json:"escalated"`
	PGIDEmpty        bool   `json:"pgid_empty"`
	Escaped          bool   `json:"escaped"`
	ExitCode         int    `json:"exit_code"`
	Cause            string `json:"cause"`
}
type Renewal struct {
	DeadlineMS int64 `json:"deadline_ms"`
}

// DurableFile atomically replaces private supervisor state and syncs its directory.
func DurableFile(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".pending-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func processToken(pid int) string {
	b, e := exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if e != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

type processRow struct{ pid, ppid, pgid int }

func processRows() []processRow {
	b, e := exec.Command("/bin/ps", "-axo", "pid=,ppid=,pgid=").Output()
	if e != nil {
		return nil
	}
	var rows []processRow
	for _, line := range strings.Split(string(b), "\n") {
		var r processRow
		if _, e := fmt.Sscan(line, &r.pid, &r.ppid, &r.pgid); e == nil {
			rows = append(rows, r)
		}
	}
	return rows
}
func groupGone(group int) bool { return errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) }
func applyLimits(l ResourceLimits) error {
	for _, v := range []struct {
		key   int
		value uint64
	}{{syscall.RLIMIT_NOFILE, l.OpenFiles}, {syscall.RLIMIT_FSIZE, l.FileBytes}, {syscall.RLIMIT_CPU, l.CPUSeconds}} {
		if v.value == 0 {
			return errors.New("resource limit missing")
		}
		var old syscall.Rlimit
		if err := syscall.Getrlimit(v.key, &old); err != nil {
			return err
		}
		n := min(v.value, old.Max)
		if err := syscall.Setrlimit(v.key, &syscall.Rlimit{Cur: n, Max: n}); err != nil {
			return err
		}
	}
	return nil
}

// Guardian runs in a separate session outside the sandbox. EOF, malformed renewal,
// deadline or cancellation stops the group; EPERM never counts as disappearance.
func Guardian(ctx context.Context, input io.Reader, control, stdout, stderr io.Writer) error {
	scan := bufio.NewScanner(input)
	scan.Buffer(make([]byte, 4096), 256<<10)
	if !scan.Scan() {
		return errors.New("guardian launch missing")
	}
	var spec LaunchSpec
	if closedjson.Decode(scan.Bytes(), &spec, 256<<10, nil) != nil || len(spec.Argv) == 0 || !filepath.IsAbs(spec.Argv[0]) || !filepath.IsAbs(spec.Cwd) || !filepath.IsAbs(spec.ReceiptPath) || spec.DeadlineMS <= 0 || spec.DeadlineMS > 30000 || spec.GraceMS <= 0 || spec.GraceMS >= spec.KillMS || spec.KillMS > 5000 {
		return errors.New("invalid guardian launch")
	}
	if err := applyLimits(spec.Rlimits); err != nil {
		return err
	}
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Cwd
	cmd.Env = spec.Env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	startedAt := time.Now()
	cutoff := startedAt.Add(time.Duration(spec.DeadlineMS) * time.Millisecond)
	wallCutoff := startedAt.UnixMilli() + spec.DeadlineMS
	if err := cmd.Start(); err != nil {
		return err
	}
	started := GuardianStarted{PID: cmd.Process.Pid, PGID: cmd.Process.Pid, StartUnixNS: startedAt.UnixNano(), StartToken: processToken(cmd.Process.Pid), BoundedResources: true}
	enc := json.NewEncoder(control)
	if err := enc.Encode(started); err != nil {
		syscall.Kill(-started.PGID, syscall.SIGKILL)
		cmd.Wait()
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	updates := make(chan Renewal, 1)
	go func() {
		defer close(updates)
		for scan.Scan() {
			var r Renewal
			if closedjson.Decode(scan.Bytes(), &r, 1024, nil) != nil || r.DeadlineMS < 0 || r.DeadlineMS > 30000 {
				return
			}
			select {
			case updates <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	report := GuardianReport{GuardianStarted: started, ExitCode: -1}
	known := map[int]bool{started.PID: true}
	escaped := map[int]bool{}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var stopping time.Time
	reaped := false
	observe := func() {
		rows := processRows()
		for changed := true; changed; {
			changed = false
			for _, r := range rows {
				if known[r.ppid] && !known[r.pid] {
					known[r.pid] = true
					changed = true
				}
			}
		}
		for _, r := range rows {
			if known[r.pid] && r.pgid != started.PGID {
				escaped[r.pid] = true
				report.Escaped = true
			}
		}
	}
	beginStop := func(cause string) {
		if stopping.IsZero() {
			stopping = time.Now()
			report.StopUnixNS = stopping.UnixNano()
			report.Cause = cause
			_ = syscall.Kill(-started.PGID, syscall.SIGTERM)
		}
	}
	for {
		select {
		case err := <-wait:
			reaped = true
			wait = nil
			if err == nil {
				report.ExitCode = 0
			} else if e, ok := err.(*exec.ExitError); ok {
				report.ExitCode = e.ExitCode()
			}
			observe()
			if stopping.IsZero() {
				beginStop("exit")
			}
		case r, ok := <-updates:
			if !ok {
				updates = nil
				beginStop("supervisor_eof")
			} else if r.DeadlineMS == 0 {
				beginStop("cancel")
			} else if stopping.IsZero() {
				cutoff = time.Now().Add(time.Duration(r.DeadlineMS) * time.Millisecond)
				wallCutoff = time.Now().UnixMilli() + r.DeadlineMS
			}
		case <-ctx.Done():
			beginStop("guardian_cancelled")
		case <-ticker.C:
			observe()
		}
		now := time.Now()
		if stopping.IsZero() && (!now.Before(cutoff) || now.UnixMilli() >= wallCutoff) {
			beginStop("lease_expired")
		}
		if !stopping.IsZero() {
			if !report.Escalated && now.Sub(stopping) >= time.Duration(spec.GraceMS)*time.Millisecond {
				_ = syscall.Kill(-started.PGID, syscall.SIGKILL)
				report.Escalated = true
				for pid := range escaped {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
			report.PGIDEmpty = groupGone(started.PGID)
			if reaped && report.PGIDEmpty || now.Sub(stopping) >= time.Duration(spec.KillMS)*time.Millisecond {
				observed := time.Now()
				report.ObservedUnixNS = observed.UnixNano()
				report.StopToObservedNS = observed.Sub(stopping).Nanoseconds()
				if report.Escaped {
					for pid := range escaped {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
				if err := DurableFile(spec.ReceiptPath, report); err != nil {
					return err
				}
				return enc.Encode(report)
			}
		}
	}
}

type guardianLauncher struct {
	inner                    isolation.Launcher
	executable, receipt      string
	deadline                 func() int64
	limits                   ResourceLimits
	mu                       sync.Mutex
	pipe                     *os.File
	control                  *os.File
	childInput, childControl *os.File
	started                  GuardianStarted
	report                   GuardianReport
	done                     chan struct{}
	once                     sync.Once
	spec                     LaunchSpec
	specSent                 bool
}

func (l *guardianLauncher) Wrap(cmd *exec.Cmd) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.inner.Wrap(cmd); err != nil {
		return err
	}
	if l.pipe != nil {
		return errors.New("one launch per guardian")
	}
	spec := LaunchSpec{Argv: append([]string(nil), cmd.Args...), Env: append([]string(nil), cmd.Env...), Cwd: cmd.Dir, DeadlineMS: l.deadline(), GraceMS: 2000, KillMS: 5000, Rlimits: l.limits, ReceiptPath: l.receipt}
	if spec.DeadlineMS <= 0 {
		return ErrStopped
	}
	if len(spec.Argv) > 2 && spec.Argv[1] == "-f" {
		spec.SandboxProfile = spec.Argv[2]
	}
	read, write, err := os.Pipe()
	if err != nil {
		return err
	}
	cr, cw, err := os.Pipe()
	if err != nil {
		read.Close()
		write.Close()
		return err
	}
	cmd.Path = l.executable
	cmd.Args = []string{l.executable, "guardian"}
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/var/empty"}
	cmd.Stdin = read
	cmd.ExtraFiles = append(cmd.ExtraFiles, cw)
	if len(cmd.ExtraFiles) != 1 {
		read.Close()
		write.Close()
		cr.Close()
		cw.Close()
		return errors.New("guardian requires control descriptor 3")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	l.pipe = write
	l.control = cr
	l.childInput = read
	l.childControl = cw
	l.done = make(chan struct{})
	l.spec = spec
	return nil
}
func (l *guardianLauncher) startedHandle(ctx context.Context) (GuardianStarted, error) {
	l.mu.Lock()
	l.childInput.Close()
	l.childControl.Close()
	// Start has installed a reader. Serialize the entire initial frame with renewals;
	// writing before Start could block forever on a spec larger than the pipe buffer.
	err := json.NewEncoder(l.pipe).Encode(l.spec)
	l.specSent = err == nil
	l.mu.Unlock()
	if err != nil {
		close(l.done)
		return GuardianStarted{}, err
	}
	type result struct {
		v   GuardianStarted
		err error
	}
	ch := make(chan result, 1)
	go func() {
		dec := json.NewDecoder(l.control)
		var s GuardianStarted
		err := dec.Decode(&s)
		ch <- result{s, err}
		if err == nil {
			var report GuardianReport
			err = dec.Decode(&report)
			l.mu.Lock()
			if err == nil {
				l.report = report
			}
			l.mu.Unlock()
		}
		close(l.done)
	}()
	select {
	case <-ctx.Done():
		l.CloseInput()
		return GuardianStarted{}, ctx.Err()
	case r := <-ch:
		l.started = r.v
		return r.v, r.err
	}
}
func (l *guardianLauncher) Renew(ms int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pipe == nil || !l.specSent {
		return ErrStopped
	}
	return json.NewEncoder(l.pipe).Encode(Renewal{ms})
}
func (l *guardianLauncher) CloseInput() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pipe != nil {
		l.pipe.Close()
		l.pipe = nil
	}
}
func (l *guardianLauncher) Observation() isolation.Observation { return l.inner.Observation() }
func (l *guardianLauncher) Cleanup() error {
	l.CloseInput()
	if l.control != nil {
		l.control.Close()
	}
	return l.inner.Cleanup()
}
