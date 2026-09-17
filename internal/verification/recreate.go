package verification

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	p "github.com/korallis/letmecook/schemas/execution"
)

type Source interface {
	CandidateManifest(context.Context, Candidate) ([]byte, error)
	OpenCandidateBlob(context.Context, Candidate, string) (io.ReadCloser, error)
}

// This private wire view matches custody's manifest v1 without a store import
// cycle. Canonical round-trip plus digest checking rejects format drift.
type blob struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type manifest struct {
	Version  string     `json:"version"`
	Identity p.Identity `json:"identity"`
	Base     struct {
		Revision string `json:"revision"`
		SHA256   string `json:"sha256"`
	} `json:"base"`
	Outcome   string   `json:"outcome"`
	Tracked   []blob   `json:"tracked"`
	Untracked []blob   `json:"untracked"`
	Binary    []blob   `json:"binary"`
	Recovery  []blob   `json:"recovery"`
	Deleted   []string `json:"deleted"`
}

func safePath(s string) bool {
	if s == "" || len(s) > 1024 || filepath.IsAbs(s) || filepath.ToSlash(s) != s || filepath.Clean(s) != s || s == "." || s == ".." || strings.HasPrefix(s, "../") || strings.ContainsAny(s, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}

func safeGit(ctx context.Context, repo string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "diff.external=", "-c", "filter.lfs.process=", "--no-optional-locks"}, args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_ATTR_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1"}
	var out, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("safe git read: %w: %s", err, stderr.b)
	}
	if out.overflow {
		return nil, fmt.Errorf("base tree bounds")
	}
	return out.b, nil
}

type limitedBuffer struct {
	b        []byte
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := min(len(p), (1<<20)-len(b.b))
	b.b = append(b.b, p[:n]...)
	b.overflow = b.overflow || n != len(p)
	return len(p), nil
}

// Recreate reads Git plumbing only. It creates no .git directory, runs no
// checkout/filter/hook, and materializes only the complete manifest payload.
// repo and parent must be trusted host-owned directories, not worker choices.
func Recreate(ctx context.Context, src Source, c Candidate, repo, parent string) (root string, err error) {
	if err = c.Validate(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(repo) || !filepath.IsAbs(parent) {
		return "", fmt.Errorf("absolute trusted paths required")
	}
	raw, err := src.CandidateManifest(ctx, c)
	if err != nil {
		return "", err
	}
	if int64(len(raw)) != c.Manifest.Bytes || Digest(raw) != c.Manifest.SHA256 {
		return "", fmt.Errorf("manifest digest mismatch")
	}
	var m manifest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(m)
	if err != nil || !bytes.Equal(raw, canonical) || m.Version != "gaffer-artifact-manifest-v1" || m.Identity != c.Identity || m.Base.Revision != c.BaseCommit || m.Base.SHA256 != Digest([]byte(c.BaseCommit)) || (m.Outcome != "succeeded" && m.Outcome != "failed") || m.Tracked == nil || m.Untracked == nil || m.Binary == nil || m.Recovery == nil || m.Deleted == nil {
		return "", fmt.Errorf("manifest identity/shape mismatch")
	}
	all := append(append(append(append([]blob{}, m.Tracked...), m.Untracked...), m.Binary...), m.Recovery...)
	if len(all) < 1 || len(all) > 4096 || len(m.Deleted) > 4096 {
		return "", fmt.Errorf("candidate file bounds")
	}
	paths := map[string]bool{}
	var total int64
	for _, b := range all {
		// Zero-byte candidate files are recreated as empty files, never skipped.
		if !safePath(b.Path) || paths[b.Path] || !IsDigest(b.SHA256) || b.Bytes < 0 || b.Bytes > 1<<30 {
			return "", fmt.Errorf("unsafe candidate path/blob")
		}
		paths[b.Path] = true
		total += b.Bytes
	}
	if total > 1<<30 {
		return "", fmt.Errorf("candidate byte bounds")
	}
	for name := range paths {
		for dir := filepath.Dir(name); dir != "."; dir = filepath.Dir(dir) {
			if paths[dir] {
				return "", fmt.Errorf("candidate path collision")
			}
		}
	}
	for _, name := range m.Deleted {
		if !safePath(name) || paths[name] {
			return "", fmt.Errorf("invalid deletion")
		}
		paths[name] = true
	}
	base, err := safeGit(ctx, repo, "rev-parse", "--verify", c.BaseCommit+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("base commit unavailable: %w", err)
	}
	if strings.TrimSpace(string(base)) != c.BaseCommit {
		return "", fmt.Errorf("base commit mismatched")
	}
	tree, err := safeGit(ctx, repo, "ls-tree", "-r", "-z", c.BaseCommit)
	if err != nil {
		return "", err
	}
	basePaths := map[string]bool{}
	for _, line := range bytes.Split(tree, []byte{0}) {
		if len(line) == 0 {
			continue
		}
		parts := bytes.SplitN(line, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid base tree")
		}
		fields := bytes.Fields(parts[0])
		name := string(parts[1])
		if len(fields) != 3 || (string(fields[0]) != "100644" && string(fields[0]) != "100755") || string(fields[1]) != "blob" || !safePath(name) {
			return "", fmt.Errorf("unsupported base symlink/submodule/path")
		}
		basePaths[name] = true
		if !paths[name] {
			return "", fmt.Errorf("base path omitted without deletion: %s", name)
		}
	}
	for _, name := range m.Deleted {
		if !basePaths[name] {
			return "", fmt.Errorf("deletion absent from base")
		}
	}
	root, err = os.MkdirTemp(parent, "gaffer-verify-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(root))
			root = ""
		}
	}()
	for _, b := range all {
		if err = ctx.Err(); err != nil {
			return root, err
		}
		// Recovery is retained evidence, not a candidate file. Still verify its bytes.
		recovery := false
		for _, r := range m.Recovery {
			if r.Path == b.Path {
				recovery = true
				break
			}
		}
		if recovery && basePaths[b.Path] {
			return root, fmt.Errorf("recovery cannot replace base content")
		}
		var out *os.File
		if !recovery {
			if err = os.MkdirAll(filepath.Join(root, filepath.Dir(b.Path)), 0700); err != nil {
				return root, err
			}
			out, err = os.OpenFile(filepath.Join(root, b.Path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return root, err
			}
		}
		err = copyBlob(ctx, src, c, b, out)
		if out != nil {
			err = errors.Join(err, out.Close())
		}
		if err != nil {
			return root, err
		}
	}
	return root, nil
}
func copyBlob(ctx context.Context, src Source, c Candidate, b blob, out *os.File) error {
	in, err := src.OpenCandidateBlob(ctx, c, b.SHA256)
	if err != nil {
		return err
	}
	defer in.Close()
	peek := make([]byte, min(int64(64), b.Bytes+1))
	npeek, readErr := io.ReadFull(in, peek)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return readErr
	}
	if strings.HasPrefix(string(peek[:npeek]), "version https://git-lfs.github.com/spec/v1") {
		return fmt.Errorf("LFS candidate content refused")
	}
	h := sha256.New()
	var w io.Writer = h
	if out != nil {
		w = io.MultiWriter(out, h)
	}
	n, err := io.Copy(w, io.LimitReader(io.MultiReader(bytes.NewReader(peek[:npeek]), in), b.Bytes+1))
	if err != nil {
		return err
	}
	if n != b.Bytes || hex.EncodeToString(h.Sum(nil)) != b.SHA256 {
		return fmt.Errorf("candidate content digest mismatch: %s", b.Path)
	}
	return nil
}
