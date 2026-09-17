// Package artifacts derives a bounded candidate snapshot from a trusted checkout.
// It never runs repository code, hooks, filters, text conversion, or Git helpers.
package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

const maxFiles = 4096
const maxBytes int64 = 1 << 30

type Request struct {
	Root, Base, RecoveryDir string
	Identity                p.Identity
	Outcome                 string
}
type Package struct {
	Manifest []byte
	Sources  []store.ArtifactSource
}

func git(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "diff.external=", "-c", "filter.lfs.process=", "--no-optional-locks"}, args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_ATTR_NOSYSTEM=1"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("safe git read: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
func safe(path string) bool {
	return path != "" && filepath.ToSlash(path) == path && !strings.HasPrefix(path, "/") && filepath.Clean(path) == path && path != "." && !strings.HasPrefix(path, "../") && !strings.Contains(path, "//")
}
func names(raw []byte) ([]string, error) {
	parts := bytes.Split(raw, []byte{0})
	var out []string
	for _, v := range parts[:len(parts)-1] {
		p := string(v)
		if !safe(p) {
			return nil, fmt.Errorf("unsafe checkout path")
		}
		out = append(out, p)
	}
	return out, nil
}
func digestFile(source, target string) (store.ArtifactBlob, error) {
	st, err := os.Lstat(source)
	if err != nil {
		return store.ArtifactBlob{}, err
	}
	if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		return store.ArtifactBlob{}, fmt.Errorf("unsupported checkout shape")
	}
	in, err := os.Open(source)
	if err != nil {
		return store.ArtifactBlob{}, err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return store.ArtifactBlob{}, err
	}
	h := sha256.New()
	n, err := out.ReadFrom(in)
	err = join(err, out.Sync(), out.Close())
	if err != nil {
		return store.ArtifactBlob{}, err
	}
	if n < 1 || n > maxBytes {
		return store.ArtifactBlob{}, fmt.Errorf("artifact size")
	} // Re-read snapshot to hash after its durable copy.
	f, err := os.Open(target)
	if err != nil {
		return store.ArtifactBlob{}, err
	}
	defer f.Close()
	if _, err = h.Write(nil); err != nil {
		return store.ArtifactBlob{}, err
	}
	h.Reset()
	if _, err = f.WriteTo(h); err != nil {
		return store.ArtifactBlob{}, err
	}
	return store.ArtifactBlob{SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: n}, nil
}
func join(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// Pack records every working-tree tracked path, every non-ignored untracked path,
// binary classification and base-relative deletions. It snapshots bytes to the
// caller's private recovery directory, preventing a later working-tree mutation
// from changing what CustodyResult receives.
func Pack(ctx context.Context, r Request) (Package, error) {
	if !filepath.IsAbs(r.Root) || !filepath.IsAbs(r.RecoveryDir) || r.Outcome != "succeeded" && r.Outcome != "failed" {
		return Package{}, fmt.Errorf("invalid package request")
	}
	rootInfo, err := os.Lstat(r.Root)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 {
		return Package{}, fmt.Errorf("checkout symlink refused")
	}
	root, err := filepath.EvalSymlinks(r.Root)
	if err != nil {
		return Package{}, err
	}
	if err = os.MkdirAll(r.RecoveryDir, 0700); err != nil {
		return Package{}, err
	}
	base, err := git(ctx, root, "rev-parse", "--verify", r.Base+"^{commit}")
	if err != nil {
		return Package{}, err
	}
	baseID := strings.TrimSpace(string(base))
	if len(baseID) != 40 && len(baseID) != 64 {
		return Package{}, fmt.Errorf("invalid base")
	}
	stage, err := git(ctx, root, "ls-files", "--stage", "-z")
	if err != nil {
		return Package{}, err
	}
	for _, line := range bytes.Split(stage, []byte{0})[:len(bytes.Split(stage, []byte{0}))-1] {
		parts := bytes.SplitN(line, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return Package{}, fmt.Errorf("invalid git index")
		}
		fields := bytes.Fields(parts[0])
		if len(fields) < 1 || string(fields[0]) == "160000" || string(fields[0]) == "120000" {
			return Package{}, fmt.Errorf("submodule or symlink refused")
		}
	}
	trackedRaw, err := git(ctx, root, "ls-files", "-z")
	if err != nil {
		return Package{}, err
	}
	untrackedRaw, err := git(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return Package{}, err
	}
	deletedRaw, err := git(ctx, root, "diff", "--name-only", "--diff-filter=D", "-z", baseID, "--")
	if err != nil {
		return Package{}, err
	}
	tracked, err := names(trackedRaw)
	if err != nil {
		return Package{}, err
	}
	untracked, err := names(untrackedRaw)
	if err != nil {
		return Package{}, err
	}
	deleted, err := names(deletedRaw)
	if err != nil {
		return Package{}, err
	}
	if len(tracked)+len(untracked) > maxFiles {
		return Package{}, fmt.Errorf("too many files")
	}
	baseHash := sha256.Sum256([]byte(baseID))
	deletedSet := map[string]bool{}
	for _, path := range deleted {
		deletedSet[path] = true
	}
	m := store.CandidateManifest{Version: "gaffer-artifact-manifest-v1", Identity: r.Identity, Base: store.ArtifactBase{Revision: baseID, SHA256: hex.EncodeToString(baseHash[:])}, Outcome: r.Outcome, Tracked: []store.ArtifactBlob{}, Untracked: []store.ArtifactBlob{}, Binary: []store.ArtifactBlob{}, Recovery: []store.ArtifactBlob{}, Deleted: deleted}
	sources := []store.ArtifactSource{}
	seen := map[string]bool{}
	for _, pair := range []struct {
		paths   []string
		tracked bool
	}{{tracked, true}, {untracked, false}} {
		for _, path := range pair.paths {
			if deletedSet[path] {
				continue
			}
			if seen[path] {
				return Package{}, fmt.Errorf("checkout alias")
			}
			seen[path] = true
			target := filepath.Join(r.RecoveryDir, hex.EncodeToString(sha256.New().Sum([]byte(path))))
			b, err := digestFile(filepath.Join(root, path), target)
			if err != nil {
				return Package{}, err
			}
			b.Path = path // Reject LFS pointers after exact byte snapshot.
			body, _ := os.ReadFile(target)
			if strings.HasPrefix(string(body), "version https://git-lfs.github.com/spec/v1") {
				return Package{}, fmt.Errorf("LFS content refused")
			}
			if bytes.IndexByte(body, 0) >= 0 {
				m.Binary = append(m.Binary, b)
			} else if pair.tracked {
				m.Tracked = append(m.Tracked, b)
			} else {
				m.Untracked = append(m.Untracked, b)
			}
			sources = append(sources, store.ArtifactSource{Path: path, File: target})
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return Package{}, err
	}
	return Package{Manifest: raw, Sources: sources}, nil
}
