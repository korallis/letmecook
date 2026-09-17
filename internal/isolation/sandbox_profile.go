package isolation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const DevelopmentProfileID = "macos-sandbox-exec-dev"
const DevelopmentLimitation = "isolation profile macos-sandbox-exec-dev is a development profile: unqualified for unattended execution"

// SandboxConfig contains paths, never secret contents. Only Darwin installs a factory.
type SandboxConfig struct {
	SandboxExec                    string
	AllowReadRoots                 []string
	BoundaryPort                   int
	CredentialPath, RunnerStateDir string
	SecretPaths                    []string
	RepositoryRoot                 string
}

var developmentFactory func(SandboxConfig) Profile

func DevelopmentProfile(c SandboxConfig) (Profile, error) {
	if developmentFactory == nil {
		return nil, ErrExecutionUnqualified
	}
	return developmentFactory(c), nil
}
func quoteSB(s string) (string, error) {
	if !filepath.IsAbs(s) || strings.ContainsAny(s, "\x00\n\r") {
		return "", errors.New("invalid sandbox path")
	}
	return strconv.Quote(s), nil
}
func canonical(path string) (string, error) {
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}
func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func profileText(c SandboxConfig, w Workspace) (string, error) {
	if c.BoundaryPort < 0 || c.BoundaryPort > 65535 {
		return "", errors.New("invalid boundary port")
	}
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny network*)\n(deny signal)\n(allow signal (target same-sandbox))\n")
	if c.BoundaryPort > 0 {
		fmt.Fprintf(&b, "(allow network-outbound (remote ip \"localhost:%d\"))\n", c.BoundaryPort)
	}
	b.WriteString("(deny file-write*)\n")
	if c.RepositoryRoot != "" {
		q, e := quoteSB(c.RepositoryRoot)
		if e != nil {
			return "", e
		}
		fmt.Fprintf(&b, "(deny file-write* (subpath %s))\n", q)
	}
	for _, path := range []string{w.Root, w.PrivateHome, w.TempDir, w.RuntimeDir, "/dev/null", "/dev/tty", "/dev/ttys"} {
		q, e := quoteSB(path)
		if e != nil {
			return "", e
		}
		kind := "subpath"
		if strings.HasPrefix(path, "/dev/") {
			kind = "literal"
		}
		fmt.Fprintf(&b, "(allow file-write* (%s %s))\n", kind, q)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	denies := []string{filepath.Join(home, ".config"), filepath.Join(home, ".claude"), filepath.Join(home, ".codex"), filepath.Join(home, ".ssh"), filepath.Join(home, ".local/share/opencode"), filepath.Join(home, ".local/state/opencode")}
	for _, path := range append([]string{c.CredentialPath, c.RunnerStateDir}, c.SecretPaths...) {
		if path != "" {
			denies = append(denies, path)
			if p, e := canonical(path); e == nil && p != path {
				denies = append(denies, p)
			}
		}
	}
	for _, path := range denies {
		q, e := quoteSB(path)
		if e != nil {
			return "", e
		}
		fmt.Fprintf(&b, "(deny file-read-data file-write* (subpath %s))\n", q)
	}
	return b.String(), nil
}
func developmentControls() []string {
	v := []string{"controlled-egress", "external-supervisor", "secret-separation", "tree-termination", "workspace-only"}
	if os.Geteuid() != 0 {
		v = append(v, "non-root")
	}
	sort.Strings(v)
	return v
}
func observation(profile, runtimeDigest string) Observation {
	return Observation{ProfileDigest: profile, RuntimeDigest: runtimeDigest, OS: runtime.GOOS, Arch: runtime.GOARCH, Controls: developmentControls(), Limitations: []string{DevelopmentLimitation, "allow-default compatibility profile; mach-lookup remains open, including securityd/keychain IPC; sampled escaped sessions cannot be proven terminated"}}
}
