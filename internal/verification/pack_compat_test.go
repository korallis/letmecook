package verification_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/korallis/letmecook/internal/artifacts"
	v "github.com/korallis/letmecook/internal/verification"
	p "github.com/korallis/letmecook/schemas/execution"
)

type packedSource struct {
	raw   []byte
	files map[string]string
}

func (s packedSource) CandidateManifest(context.Context, v.Candidate) ([]byte, error) {
	return s.raw, nil
}
func (s packedSource) OpenCandidateBlob(_ context.Context, _ v.Candidate, d string) (io.ReadCloser, error) {
	return os.Open(s.files[d])
}
func TestRecreateCustodyManifestV1Compatibility(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=f@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=f@example.invalid"}
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(string(b), err)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deleted.txt"), []byte("delete me\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	if err := os.Remove(filepath.Join(repo, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("candidate\n"), 0600); err != nil {
		t.Fatal(err)
	}
	id := p.Identity{Generation: v.ID(), TaskID: v.ID(), AttemptID: v.ID(), Epoch: 1}
	pack, err := artifacts.Pack(ctx, artifacts.Request{Root: repo, Base: base, RecoveryDir: t.TempDir(), Identity: id, Outcome: "succeeded"})
	if err != nil {
		t.Fatal(err)
	}
	source := packedSource{raw: pack.Manifest, files: map[string]string{}}
	for _, s := range pack.Sources {
		b, err := os.ReadFile(s.File)
		if err != nil {
			t.Fatal(err)
		}
		source.files[v.Digest(b)] = s.File
	}
	c := v.Candidate{SelectionID: v.ID(), Identity: id, Manifest: p.Manifest{ManifestID: v.ID(), SHA256: v.Digest(pack.Manifest), Bytes: int64(len(pack.Manifest))}, BaseCommit: base}
	root, err := v.Recreate(ctx, source, c, repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	b, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil || string(b) != "candidate\n" {
		t.Fatal(string(b), err)
	}
	t.Run("upstream-null-deletions-refused", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(repo, "deleted.txt"), []byte("delete me\n"), 0600); err != nil {
			t.Fatal(err)
		}
		withoutDeletion, err := artifacts.Pack(ctx, artifacts.Request{Root: repo, Base: base, RecoveryDir: t.TempDir(), Identity: id, Outcome: "succeeded"})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(withoutDeletion.Manifest, []byte(`"deleted":null`)) {
			t.Fatal("upstream changed: review explicit-list compatibility")
		}
		ref := c
		ref.Manifest.ManifestID = v.ID()
		ref.Manifest.SHA256 = v.Digest(withoutDeletion.Manifest)
		ref.Manifest.Bytes = int64(len(withoutDeletion.Manifest))
		_, err = v.Recreate(ctx, packedSource{raw: withoutDeletion.Manifest}, ref, repo, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "shape mismatch") {
			t.Fatal("null categories must not silently pass", err)
		}
	})
}
