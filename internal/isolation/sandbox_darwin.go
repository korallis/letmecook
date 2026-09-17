package isolation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

type MacOSSandboxExecDev struct {
	SandboxExec                    string
	AllowReadRoots                 []string
	BoundaryPort                   int
	CredentialPath, RunnerStateDir string
	SecretPaths                    []string
}

func init()                                       { developmentFactory = func(c SandboxConfig) Profile { return MacOSSandboxExecDev(c) } }
func (MacOSSandboxExecDev) ID() string            { return DevelopmentProfileID }
func (MacOSSandboxExecDev) Kind() string          { return "native" }
func (MacOSSandboxExecDev) Qualification() string { return QualificationDevelopment }
func (MacOSSandboxExecDev) Controls() []string    { return developmentControls() }
func (p MacOSSandboxExecDev) Prepare(ctx context.Context, w Workspace) (Launcher, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.SandboxExec == "" {
		p.SandboxExec = "/usr/bin/sandbox-exec"
	}
	if p.SandboxExec != "/usr/bin/sandbox-exec" {
		return nil, errors.New("sandbox binary must be /usr/bin/sandbox-exec")
	}
	for _, path := range []string{w.Root, w.PrivateHome, w.TempDir, w.RuntimeDir} {
		q, e := canonical(path)
		if e != nil || q != path {
			return nil, errors.New("sandbox roots must be canonical directories")
		}
		st, e := os.Stat(path)
		if e != nil || !st.IsDir() {
			return nil, errors.New("sandbox root missing")
		}
	}
	text, err := profileText(SandboxConfig(p), w)
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Dir(w.RuntimeDir), "isolation-*.sb")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.WriteString(text)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		os.Remove(name)
		return nil, err
	}
	digest := sha256.Sum256([]byte(text))
	runtimeDigest, err := hashFile(p.SandboxExec)
	if err != nil {
		os.Remove(name)
		return nil, err
	}
	return &sandboxLauncher{binary: p.SandboxExec, path: name, workspace: w, observed: observation(hex.EncodeToString(digest[:]), runtimeDigest)}, nil
}

type sandboxLauncher struct {
	binary, path string
	workspace    Workspace
	observed     Observation
}

func (l *sandboxLauncher) Wrap(cmd *exec.Cmd) error {
	if cmd == nil || !filepath.IsAbs(cmd.Path) || len(cmd.Args) == 0 || len(cmd.Env) == 0 {
		return errors.New("explicit command and environment required")
	}
	args := append([]string{l.binary, "-f", l.path, "--", cmd.Path}, cmd.Args[1:]...)
	cmd.Path = l.binary
	cmd.Args = args
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}
func (l *sandboxLauncher) Observation() Observation { return l.observed }
func (l *sandboxLauncher) Cleanup() error           { return os.Remove(l.path) }

// WithBoundary returns a copy pinned to one loopback endpoint; malformed endpoints
// yield the refusing profile, never a broader network allowance.
func (p MacOSSandboxExecDev) WithBoundary(addr string) Profile {
	raw := addr
	if u, e := url.Parse(addr); e == nil && u.Host != "" {
		raw = u.Host
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return Unqualified{}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return Unqualified{}
	}
	p.BoundaryPort = n
	return p
}
