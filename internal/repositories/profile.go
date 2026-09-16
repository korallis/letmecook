// Package repositories owns provisional, explicitly approved repository inputs.
// Registration and checkout confer no execution, acceptance or publication authority.
package repositories

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	a "github.com/korallis/letmecook/internal/authority"
	p "github.com/korallis/letmecook/schemas/execution"
)

const Version = "repository-provisional-v1"
const MaxBytes = 65536

var (
	Invalid       = errors.New("invalid_repository_profile")
	Denied        = errors.New("repository_denied")
	Unavailable   = errors.New("repository_unavailable")
	RemoteChanged = errors.New("repository_remote_or_ref_changed")
	ProtectedPath = errors.New("repository_protected_path")
)

// Base is pinned: even a fast-forward requires a new owner-approved revision.
type Base struct {
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
	Policy string `json:"policy"`
}
type Command struct {
	Argv      []string `json:"argv"`
	Directory string   `json:"directory"`
	TimeoutMS int64    `json:"timeout_ms"`
}
type Verification struct {
	Name     string    `json:"name"`
	Commands []Command `json:"commands"`
}

// RunnerRoot is an owner-selected candidate pairing, NOT runner-local permission
// or proof of OS isolation. Local policy may further restrict it, never widen it.
type RunnerRoot struct {
	RunnerID string `json:"runner_id"`
	Root     string `json:"root"`
}
type Profile struct {
	Version        string       `json:"version"`
	ID             string       `json:"id"`
	Revision       int64        `json:"revision"`
	Remote         string       `json:"remote"`
	Base           Base         `json:"base"`
	ProtectedPaths []string     `json:"protected_paths"`
	Verification   Verification `json:"verification"`
	ContextScope   []string     `json:"context_scope"`
	RunnerRoots    []RunnerRoot `json:"runner_roots"`
}
type Validation struct {
	ProfileDigest string `json:"profile_digest"`
	BaseCommit    string `json:"base_commit"`
}

// Selection carries the exact approved identity, not just a reusable project label.
type Selection struct {
	Repository    string     `json:"repository"`
	Revision      int64      `json:"revision"`
	ProfileDigest string     `json:"profile_digest"`
	Remote        string     `json:"remote"`
	BaseCommit    string     `json:"base_commit"`
	RunnerRoot    RunnerRoot `json:"runner_root"`
}

var commit = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
var remotePath = regexp.MustCompile(`^/[A-Za-z0-9_-][A-Za-z0-9._/-]*\.git$`)
var host = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

func printable(s string, limit int) bool {
	if s == "" || len(s) > limit {
		return false
	}
	for _, c := range s {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}
func validPath(s string) bool {
	if !printable(s, 512) || !fs.ValidPath(s) || strings.ContainsAny(s, "\\*?[]:") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}
func validRoot(s string) bool {
	return printable(s, 4096) && filepath.IsAbs(s) && filepath.Clean(s) == s && s != string(filepath.Separator)
}
func ordered[T any](values []T, key func(T) string) bool {
	if values == nil || len(values) > 128 {
		return false
	}
	for i := 1; i < len(values); i++ {
		if key(values[i-1]) >= key(values[i]) {
			return false
		}
	}
	return true
}
func rootKey(r RunnerRoot) string { return r.RunnerID + "\x00" + r.Root }

// Remote identities must already be canonical. No URL rewrites, redirects, SSH,
// embedded credentials, remote helpers, query strings or implicit local paths.
// ponytail: anonymous HTTPS and explicit local file transport only; private forge
// access needs a separately reviewed repository-scoped credential broker.
func ValidRemote(raw string) bool {
	if !printable(raw, 4096) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || u.String() != raw {
		return false
	}
	switch u.Scheme {
	case "https":
		if !host.MatchString(u.Host) || strings.Contains(u.Host, "..") || !remotePath.MatchString(u.Path) {
			return false
		}
		return validPath(strings.TrimPrefix(u.Path, "/")) && !strings.HasSuffix(u.Path, "/.git")
	case "file":
		return u.Host == "" && validRoot(u.Path)
	default:
		return false
	}
}
func ValidRef(ref string) bool {
	return strings.HasPrefix(ref, "refs/heads/") && printable(ref, 256) && !strings.Contains(ref, "./") && validRefName(ref)
}
func validRefName(ref string) bool {
	if !strings.HasPrefix(ref, "refs/") || strings.HasSuffix(ref, ".") || strings.ContainsAny(ref, " ~^:?*[\\") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") {
		return false
	}
	for _, c := range ref {
		if c < 32 || c == 127 {
			return false
		}
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}
func (v Profile) Validate() error {
	if v.Version != Version || !a.ValidActor(v.ID) || v.Revision < 1 || v.Revision > p.MaxInteger || !ValidRemote(v.Remote) || !ValidRef(v.Base.Ref) || !commit.MatchString(v.Base.Commit) || v.Base.Policy != "pinned" {
		return Invalid
	}
	for _, paths := range [][]string{v.ProtectedPaths, v.ContextScope} {
		if !ordered(paths, func(s string) string { return s }) {
			return Invalid
		}
		for _, path := range paths {
			if !validPath(path) {
				return Invalid
			}
		}
	}
	if len(v.ContextScope) == 0 || !a.ValidActor(v.Verification.Name) || len(v.Verification.Commands) == 0 || len(v.Verification.Commands) > 32 {
		return Invalid
	}
	for _, c := range v.Verification.Commands {
		if !validPath(c.Directory) || c.TimeoutMS < 1 || c.TimeoutMS > p.MaxInteger || len(c.Argv) == 0 || len(c.Argv) > 128 {
			return Invalid
		}
		for _, arg := range c.Argv {
			if !printable(arg, 4096) {
				return Invalid
			}
		}
	}
	if len(v.RunnerRoots) == 0 || !ordered(v.RunnerRoots, rootKey) {
		return Invalid
	}
	for _, r := range v.RunnerRoots {
		if !p.ValidID(r.RunnerID) || !validRoot(r.Root) {
			return Invalid
		}
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > MaxBytes {
		return Invalid
	}
	return nil
}
func (v Profile) Digest() (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(v)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
func (v Profile) Select(s Selection) error {
	digest, err := v.Digest()
	if err != nil {
		return err
	}
	if s.Repository != v.ID || s.Revision != v.Revision || s.ProfileDigest != digest || s.Remote != v.Remote || s.BaseCommit != v.Base.Commit || !slices.Contains(v.RunnerRoots, s.RunnerRoot) {
		return Denied
	}
	return nil
}

// CheckChanges checks authoritative changed paths (both sides of renames). It
// neither computes a diff nor trusts a worker's path list as verification evidence.
// Directory prefixes are literal; protection includes deletion/replacement of an
// ancestor. #20 must obtain the complete diff under its own custody.
func (v Profile) CheckChanges(paths []string) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if paths == nil || len(paths) > 10000 {
		return Invalid
	}
	for _, path := range paths {
		if !validPath(path) || path == "." {
			return Invalid
		}
		for _, protected := range v.ProtectedPaths {
			if protected == "." || path == protected || strings.HasPrefix(path, protected+"/") || strings.HasPrefix(protected, path+"/") {
				return ProtectedPath
			}
		}
	}
	return nil
}
