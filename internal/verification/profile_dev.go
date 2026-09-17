package verification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/korallis/letmecook/internal/isolation"
	"github.com/korallis/letmecook/internal/scheduler"
)

type developmentProfile struct {
	profile  isolation.Profile
	timeout  time.Duration
	stateDir string
}

// NewDevelopmentProfile is an explicit development-only constructor. A profile
// label alone is insufficient: every invocation measures escape canaries before
// running the trusted command through the same launcher.
func NewDevelopmentProfile(profile isolation.Profile, timeout time.Duration, stateDir string) (Profile, error) {
	if profile == nil || profile.ID() != "macos-sandbox-exec-dev" || profile.Kind() != "native" || profile.Qualification() != isolation.QualificationDevelopment || timeout <= 0 || timeout > time.Hour {
		return nil, isolation.ErrExecutionUnqualified
	}
	for _, control := range scheduler.DevelopmentIsolationControls() {
		if !slices.Contains(profile.Controls(), control) {
			return nil, isolation.ErrExecutionUnqualified
		}
	}
	if !filepath.IsAbs(stateDir) {
		return nil, isolation.ErrExecutionUnqualified
	}
	canonical, err := filepath.EvalSymlinks(stateDir)
	if err != nil || canonical != filepath.Clean(stateDir) || systemTempException(canonical) {
		return nil, isolation.ErrExecutionUnqualified
	}
	info, err := os.Lstat(canonical)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, isolation.ErrExecutionUnqualified
	}
	return developmentProfile{profile: profile, timeout: timeout, stateDir: canonical}, nil
}
func (developmentProfile) id() string { return "macos-sandbox-exec-dev" }

type devCapture struct {
	h      hash.Hash
	prefix []byte
	bytes  int64
}

func newDevCapture() *devCapture { return &devCapture{h: sha256.New(), prefix: []byte{}} }
func (c *devCapture) Write(b []byte) (int, error) {
	c.h.Write(b)
	c.bytes += int64(len(b))
	c.prefix = append(c.prefix, b[:min(len(b), CaptureLimit-len(c.prefix))]...)
	return len(b), nil
}
func (c *devCapture) stream() Stream {
	return Stream{Prefix: c.prefix, Bytes: c.bytes, SHA256: hex.EncodeToString(c.h.Sum(nil))}
}
func (p developmentProfile) run(ctx context.Context, root string, check Check) (o outcome) {
	o = outcome{qualification: "development", environment: observe(p.id(), p.id(), check.Env["PATH"]), stdout: emptyStream(), stderr: emptyStream()}
	defer func() {
		if o.failure != "" && o.exit == nil && (!IsDigest(o.profileDigest) || !IsDigest(o.runtimeDigest) || o.environment.ConfinementDrift) {
			o.profileID = "unqualified"
			o.refusal = &Refusal{Code: "unqualified_profile", Reason: UnqualifiedReason, Environment: o.environment}
		}
	}()
	parent, err := os.MkdirTemp(filepath.Dir(root), "verify-runtime-")
	if err != nil {
		o.failure = "verification runtime unavailable"
		return o
	}
	defer os.RemoveAll(parent)
	w := isolation.Workspace{Root: root, PrivateHome: filepath.Join(parent, "home"), TempDir: filepath.Join(parent, "tmp"), RuntimeDir: filepath.Join(parent, "runtime")}
	for _, path := range []string{w.PrivateHome, w.TempDir, w.RuntimeDir} {
		if err = os.Mkdir(path, 0700); err != nil {
			o.failure = "verification runtime unavailable"
			return o
		}
	}
	ctx, cancel := context.WithTimeout(ctx, min(p.timeout, check.Timeout))
	defer cancel()
	launcher, err := p.profile.Prepare(ctx, w)
	if err != nil {
		o.failure = "development profile preparation refused"
		return o
	}
	defer launcher.Cleanup()
	observation := launcher.Observation()
	o.limitations = append([]string(nil), observation.Limitations...)
	o.profileDigest = observation.ProfileDigest
	o.runtimeDigest = observation.RuntimeDigest
	for _, control := range scheduler.DevelopmentIsolationControls() {
		if !slices.Contains(observation.Controls, control) {
			o.failure = "missing isolation controls"
			o.environment = observe(p.id(), "controls-missing", check.Env["PATH"])
			return o
		}
	}
	if !IsDigest(o.profileDigest) || !IsDigest(o.runtimeDigest) {
		o.failure = "missing isolation digests"
		return o
	}
	if err = verificationCanary(ctx, launcher, w, p.stateDir); err != nil {
		// Drift is recorded rather than turned into an executed passing report. The
		// labelled environment remains the expected profile; failure carries drift.
		o.environment = observe(p.id(), "canary-failed", check.Env["PATH"])
		o.failure = "confinement_drift"
		return o
	}
	dir := root
	for _, part := range strings.Split(check.CWD, "/") {
		if part == "." {
			continue
		}
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			o.failure = "check cwd unavailable or unsafe"
			return o
		}
	}
	cmd := exec.CommandContext(ctx, check.Argv[0], check.Argv[1:]...)
	cmd.Dir = dir
	cmd.Env = []string{}
	cmd.WaitDelay = time.Second
	for _, key := range envKeys(check.Env) {
		cmd.Env = append(cmd.Env, key+"="+check.Env[key])
	}
	stdout, stderr := newDevCapture(), newDevCapture()
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err = launcher.Wrap(cmd); err != nil {
		o.failure = "development launcher refused"
		return o
	}
	err = runDevelopmentCommand(cmd)
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		o.exit = &code
	}
	if err != nil {
		o.failure = "verification command failed"
	}
	o.stdout = stdout.stream()
	o.stderr = stderr.stream()
	return o
}
func pathWithin(root, path string) bool {
	return root == path || strings.HasPrefix(path, root+string(os.PathSeparator))
}
func systemTempException(path string) bool {
	for _, root := range []string{"/private/tmp", "/private/var/folders", "/tmp", "/var/folders"} {
		if pathWithin(root, path) {
			return true
		}
	}
	return false
}
func verificationCanary(ctx context.Context, launcher isolation.Launcher, w isolation.Workspace, stateDir string) error {
	// Explicit daemon state only: never infer HOME or place the canary in the
	// verification workspace or any known launcher system-temp write exception.
	canonical, err := filepath.EvalSymlinks(stateDir)
	if err != nil || canonical != stateDir || systemTempException(canonical) {
		return fmt.Errorf("unsafe canary state")
	}
	canaryRoot := filepath.Join(stateDir, "verification-canary")
	for _, allowed := range []string{w.Root, w.PrivateHome, w.TempDir, w.RuntimeDir} {
		resolved, err := filepath.EvalSymlinks(allowed)
		if err != nil || pathWithin(resolved, canaryRoot) || pathWithin(canaryRoot, resolved) {
			return fmt.Errorf("canary overlaps workspace")
		}
	}
	if err = os.Mkdir(canaryRoot, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(canaryRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("unsafe canary root")
	}
	parent, err := os.MkdirTemp(canaryRoot, "probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(parent)
	outside := filepath.Join(parent, "escape-canary")
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `if printf escape > "$1" 2>/dev/null; then exit 42; fi`, "canary", outside)
	cmd.Dir = w.Root
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.WaitDelay = time.Second
	if err := launcher.Wrap(cmd); err != nil {
		return err
	}
	if err := runDevelopmentCommand(cmd); err != nil {
		return fmt.Errorf("write canary failed")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		return fmt.Errorf("write escaped")
	}
	// A real listening loopback endpoint distinguishes denied egress from an
	// unrelated connect failure. Verification never needs an inference boundary.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd = exec.CommandContext(probeCtx, "/usr/bin/nc", "-z", "127.0.0.1", port)
	cmd.Dir = w.Root
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.WaitDelay = time.Second
	if err = launcher.Wrap(cmd); err != nil {
		return err
	}
	err = runDevelopmentCommand(cmd)
	var exit *exec.ExitError
	if err == nil || !errors.As(err, &exit) {
		return fmt.Errorf("network canary inconclusive or escaped")
	}
	return nil
}

// Verification owns its process group. A launcher may change executable/argv,
// never let a timed-out child keep writing after evidence has been recorded.
func runDevelopmentCommand(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	err := cmd.Run()
	if cmd.Process != nil {
		if alive := syscall.Kill(-cmd.Process.Pid, 0); alive == nil || errors.Is(alive, syscall.EPERM) {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return fmt.Errorf("verification process group not quiescent")
		}
	}
	return err
}
