package verification_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/artifacts"
	"github.com/korallis/letmecook/internal/store"
	v "github.com/korallis/letmecook/internal/verification"
	p "github.com/korallis/letmecook/schemas/execution"
	_ "modernc.org/sqlite"
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

// promotedSource reads what custody promoted: the store's manifests/<id>.json and
// blobs/<xx>/<digest> layout. It is the same content OpenCandidateBlob serves once
// a candidate selection exists; this test has no dispatched attempt to select.
type promotedSource struct{ artifacts, manifest string }

func (s promotedSource) CandidateManifest(context.Context, v.Candidate) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.artifacts, "manifests", s.manifest+".json"))
}
func (s promotedSource) OpenCandidateBlob(_ context.Context, _ v.Candidate, d string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(s.artifacts, "blobs", d[:2], d))
}

func gitIn(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=f@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=f@example.invalid"}
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(string(b), err)
	}
	return strings.TrimSpace(string(b))
}
func writeFile(t *testing.T, repo, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// candidateRepo commits a.txt, keep.txt and deleted.txt, then applies the candidate:
// a.txt changed, keep.txt emptied, deleted.txt removed and new.txt added untracked.
func candidateRepo(t *testing.T) (repo, base string) {
	t.Helper()
	repo = t.TempDir()
	gitIn(t, repo, "init", "-q")
	writeFile(t, repo, "a.txt", "base\n")
	writeFile(t, repo, "keep.txt", "not empty yet\n")
	writeFile(t, repo, "deleted.txt", "delete me\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "base")
	base = gitIn(t, repo, "rev-parse", "HEAD")
	if err := os.Remove(filepath.Join(repo, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "a.txt", "candidate\n")
	writeFile(t, repo, "keep.txt", "")
	writeFile(t, repo, "new.txt", "new\n")
	return repo, base
}

func TestRecreateCustodyManifestV1Compatibility(t *testing.T) {
	ctx := context.Background()
	repo, base := candidateRepo(t)
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
	t.Run("explicit-empty-deletions-recreate", func(t *testing.T) {
		// A candidate without deletions carries "deleted":[] (never null), which
		// custody and Recreate both require as an explicit category.
		writeFile(t, repo, "deleted.txt", "delete me\n")
		withoutDeletion, err := artifacts.Pack(ctx, artifacts.Request{Root: repo, Base: base, RecoveryDir: t.TempDir(), Identity: id, Outcome: "succeeded"})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(withoutDeletion.Manifest, []byte(`"deleted":[]`)) || bytes.Contains(withoutDeletion.Manifest, []byte(`:null`)) {
			t.Fatalf("categories must be explicit: %s", withoutDeletion.Manifest)
		}
		ref := c
		ref.Manifest.ManifestID = v.ID()
		ref.Manifest.SHA256 = v.Digest(withoutDeletion.Manifest)
		ref.Manifest.Bytes = int64(len(withoutDeletion.Manifest))
		files := map[string]string{}
		for _, s := range withoutDeletion.Sources {
			body, err := os.ReadFile(s.File)
			if err != nil {
				t.Fatal(err)
			}
			files[v.Digest(body)] = s.File
		}
		root, err := v.Recreate(ctx, packedSource{raw: withoutDeletion.Manifest, files: files}, ref, repo, t.TempDir())
		if err != nil {
			t.Fatal("explicit empty deletion list refused", err)
		}
		defer os.RemoveAll(root)
		if body, err := os.ReadFile(filepath.Join(root, "deleted.txt")); err != nil || string(body) != "delete me\n" {
			t.Fatal("restored file missing from recreated candidate", err)
		}
	})
}

// The full path a supervisor result takes: Pack snapshots the checkout, custody
// verifies and promotes the bytes (including a zero-byte file and a deletion), and
// Recreate rebuilds the candidate from what custody promoted.
func TestPackCustodyRecreateRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, base := candidateRepo(t)
	id := p.Identity{Generation: v.ID(), TaskID: v.ID(), AttemptID: v.ID(), Epoch: 1}
	pack, err := artifacts.Pack(ctx, artifacts.Request{Root: repo, Base: base, RecoveryDir: t.TempDir(), Identity: id, Outcome: "succeeded"})
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Tracked   []store.ArtifactBlob `json:"tracked"`
		Untracked []store.ArtifactBlob `json:"untracked"`
		Deleted   []string             `json:"deleted"`
	}
	if err := json.Unmarshal(pack.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	empty := v.Digest(nil)
	var emptied bool
	for _, b := range m.Tracked {
		emptied = emptied || b.Path == "keep.txt" && b.Bytes == 0 && b.SHA256 == empty
	}
	if !emptied || len(m.Untracked) != 1 || m.Untracked[0].Path != "new.txt" || strings.Join(m.Deleted, ",") != "deleted.txt" {
		t.Fatalf("pack shape: %s", pack.Manifest)
	}
	stateDir, artifactsDir := filepath.Join(t.TempDir(), "state"), filepath.Join(t.TempDir(), "artifacts")
	s, err := store.Open(ctx, stateDir, artifactsDir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	manifest := p.Manifest{ManifestID: v.ID(), SHA256: v.Digest(pack.Manifest), Bytes: int64(len(pack.Manifest))}
	result := p.Message{Version: p.FencedVersion, MessageID: v.ID(), Kind: "result", Identity: id, Manifest: &manifest}
	retain := time.Now().Add(time.Hour).UnixMilli()
	// This identity's generation is not the store's, so custody retains the result
	// as quarantined evidence. Validation, promotion and the zero-byte blob row are
	// exercised exactly as for a current attempt; only the current flag differs.
	first, err := s.CustodyResult(ctx, store.CustodyRequest{Result: result, Manifest: pack.Manifest, Sources: pack.Sources, RetainUntilMS: retain})
	if err != nil || !first.Quarantined || p.CheckAck(result, first.Ack, id, first.Receipt) != p.OK {
		t.Fatal(first, err)
	}
	again, err := s.CustodyResult(ctx, store.CustodyRequest{Result: result, Manifest: pack.Manifest, RetainUntilMS: retain})
	if err != nil || again.Receipt.ReceiptID != first.Receipt.ReceiptID || again.Ack.MessageID != first.Ack.MessageID {
		t.Fatal("lost-acknowledgement replay must be byte-identical", again, err)
	}
	c := v.Candidate{SelectionID: v.ID(), Identity: id, Manifest: manifest, BaseCommit: base}
	root, err := v.Recreate(ctx, promotedSource{artifacts: artifactsDir, manifest: manifest.ManifestID}, c, repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	for name, want := range map[string]string{"a.txt": "candidate\n", "keep.txt": "", "new.txt": "new\n"} {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(body) != want {
			t.Fatalf("%s: %q %v", name, body, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "deleted.txt")); !os.IsNotExist(err) {
		t.Fatal("deleted base file recreated", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(stateDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var size, current int64
	if err := db.QueryRow("SELECT bytes FROM artifact_blobs WHERE digest=?", empty).Scan(&size); err != nil || size != 0 {
		t.Fatal("zero-byte blob row", size, err)
	}
	if err := db.QueryRow("SELECT current FROM artifact_results WHERE receipt_id=?", first.Receipt.ReceiptID).Scan(&current); err != nil || current != 0 {
		t.Fatal("custody must never name a current result", current, err)
	}
}
