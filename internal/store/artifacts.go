package store

// Provisional local candidate-artifact custody. This package deliberately has no
// upload endpoint or runner integration: callers provide runner-owned recovery
// files and retain them after this method returns. A receipt is custody evidence,
// never execution, verification, acceptance, or publication authority.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	p "github.com/korallis/letmecook/schemas/execution"
)

const (
	artifactManifestVersion       = "gaffer-artifact-manifest-v1"
	maxArtifactFiles              = 4096
	maxArtifactBytes        int64 = 1 << 30
)

const artifactSchema = `
CREATE TABLE IF NOT EXISTS artifact_blobs (
 digest TEXT PRIMARY KEY CHECK(length(digest)=64), bytes INTEGER NOT NULL CHECK(bytes BETWEEN 1 AND 1073741824), created_ms INTEGER NOT NULL,
 retained_until_ms INTEGER NOT NULL, state TEXT NOT NULL CHECK(state IN ('committed','quarantined'))
) STRICT;
CREATE TABLE IF NOT EXISTS artifact_manifests (
 manifest_id TEXT PRIMARY KEY, sha256 TEXT NOT NULL UNIQUE CHECK(length(sha256)=64), bytes INTEGER NOT NULL CHECK(bytes BETWEEN 1 AND 1048576),
 body BLOB NOT NULL, created_ms INTEGER NOT NULL, state TEXT NOT NULL CHECK(state IN ('committed','quarantined')), UNIQUE(manifest_id,sha256,bytes)
) STRICT;
CREATE TABLE IF NOT EXISTS artifact_manifest_blobs (
 manifest_id TEXT NOT NULL REFERENCES artifact_manifests(manifest_id), digest TEXT NOT NULL REFERENCES artifact_blobs(digest), role TEXT NOT NULL,
 path TEXT NOT NULL, bytes INTEGER NOT NULL, PRIMARY KEY(manifest_id,path), UNIQUE(manifest_id,digest,path)
) STRICT;
CREATE TABLE IF NOT EXISTS artifact_results (
 generation TEXT NOT NULL, task_id TEXT NOT NULL, attempt_id TEXT NOT NULL, epoch INTEGER NOT NULL,
 manifest_id TEXT NOT NULL REFERENCES artifact_manifests(manifest_id), manifest_sha256 TEXT NOT NULL, manifest_bytes INTEGER NOT NULL,
 receipt_id TEXT NOT NULL UNIQUE, current INTEGER NOT NULL CHECK(current IN (0,1)), quarantined INTEGER NOT NULL CHECK(quarantined IN (0,1)),
 created_ms INTEGER NOT NULL, PRIMARY KEY(generation,task_id,attempt_id,epoch), UNIQUE(manifest_id),
 FOREIGN KEY(manifest_id,manifest_sha256,manifest_bytes) REFERENCES artifact_manifests(manifest_id,sha256,bytes)
) STRICT;
CREATE UNIQUE INDEX IF NOT EXISTS artifact_results_current_attempt ON artifact_results(task_id) WHERE current=1;
CREATE TRIGGER IF NOT EXISTS artifact_blobs_immutable BEFORE UPDATE ON artifact_blobs BEGIN SELECT RAISE(ABORT,'artifact blob immutable'); END;
CREATE TRIGGER IF NOT EXISTS artifact_manifests_immutable BEFORE UPDATE ON artifact_manifests BEGIN SELECT RAISE(ABORT,'artifact manifest immutable'); END;
CREATE TRIGGER IF NOT EXISTS artifact_results_immutable BEFORE UPDATE ON artifact_results BEGIN SELECT RAISE(ABORT,'artifact result immutable'); END;
CREATE TRIGGER IF NOT EXISTS artifact_results_no_delete BEFORE DELETE ON artifact_results BEGIN SELECT RAISE(ABORT,'artifact result retained'); END;
PRAGMA user_version=7;`

type ArtifactBlob struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type ArtifactBase struct {
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
}

// CandidateManifest is canonical JSON, including every category even when empty.
// Its payload lists are a complete candidate set; categories make omissions visible.
type CandidateManifest struct {
	Version   string         `json:"version"`
	Identity  p.Identity     `json:"identity"`
	Base      ArtifactBase   `json:"base"`
	Outcome   string         `json:"outcome"`
	Tracked   []ArtifactBlob `json:"tracked"`
	Untracked []ArtifactBlob `json:"untracked"`
	Binary    []ArtifactBlob `json:"binary"`
	Recovery  []ArtifactBlob `json:"recovery"`
	Deleted   []string       `json:"deleted"`
}

// ArtifactSource is a runner recovery file. Custody copies it; it never moves or
// deletes it. The logical path must occur exactly once in the manifest.
type ArtifactSource struct {
	Path string
	File string
}
type CustodyRequest struct {
	Result        p.Message
	Manifest      []byte
	Sources       []ArtifactSource
	RetainUntilMS int64
}
type CustodyReceipt struct {
	Receipt     p.Receipt
	Ack         p.Message
	Quarantined bool
}

// artifactHook is test-only crash/failure injection at named durable boundaries.
var artifactHook func(string) error

func artifactStep(name string) error {
	if artifactHook != nil {
		return artifactHook(name)
	}
	return nil
}

func validHex(v string) bool {
	_, err := hex.DecodeString(v)
	return len(v) == 64 && err == nil && strings.ToLower(v) == v
}
func safeArtifactPath(path string) bool {
	return path != "" && len(path) <= 1024 && filepath.ToSlash(path) == path && !strings.HasPrefix(path, "/") &&
		filepath.Clean(path) == path && path != "." && !strings.HasPrefix(path, "../") && !strings.Contains(path, "//")
}
func canonicalManifest(raw []byte, result p.Message) (CandidateManifest, []ArtifactBlob, error) {
	if len(raw) < 1 || len(raw) > 1048576 {
		return CandidateManifest{}, nil, fmt.Errorf("manifest bounds")
	}
	var m CandidateManifest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil || d.Decode(&struct{}{}) != io.EOF {
		return CandidateManifest{}, nil, fmt.Errorf("manifest JSON")
	}
	canonical, err := json.Marshal(m)
	if err != nil || !bytes.Equal(canonical, raw) {
		return CandidateManifest{}, nil, fmt.Errorf("manifest must be canonical")
	}
	if m.Version != artifactManifestVersion || m.Identity != result.Identity || !validHex(m.Base.SHA256) || m.Base.Revision == "" || len(m.Base.Revision) > 256 || (m.Outcome != "succeeded" && m.Outcome != "failed") {
		return CandidateManifest{}, nil, fmt.Errorf("manifest identity/base/outcome")
	}
	if m.Tracked == nil || m.Untracked == nil || m.Binary == nil || m.Recovery == nil || m.Deleted == nil {
		return CandidateManifest{}, nil, fmt.Errorf("manifest categories must be explicit")
	}
	all := make([]ArtifactBlob, 0, len(m.Tracked)+len(m.Untracked)+len(m.Binary)+len(m.Recovery))
	seen := map[string]bool{}
	for _, group := range [][]ArtifactBlob{m.Tracked, m.Untracked, m.Binary, m.Recovery} {
		for _, b := range group {
			if !safeArtifactPath(b.Path) || !validHex(b.SHA256) || b.Bytes < 1 || b.Bytes > maxArtifactBytes || seen[b.Path] {
				return CandidateManifest{}, nil, fmt.Errorf("manifest path/blob")
			}
			seen[b.Path] = true
			all = append(all, b)
		}
	}
	for _, path := range m.Deleted {
		if !safeArtifactPath(path) || seen[path] {
			return CandidateManifest{}, nil, fmt.Errorf("manifest deletion")
		}
		seen[path] = true
	}
	if len(all) == 0 || len(all) > maxArtifactFiles {
		return CandidateManifest{}, nil, fmt.Errorf("manifest file count")
	}
	return m, all, nil
}
func artifactDigest(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
func copyVerified(source, destination string, want ArtifactBlob) error {
	st, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsupported artifact shape")
	}
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	// Git LFS pointers require separate, explicit materialization; preserving their
	// text as a candidate blob would silently omit the actual content.
	peek := make([]byte, 64)
	n, _ := io.ReadFull(f, peek)
	if strings.HasPrefix(string(peek[:n]), "version https://git-lfs.github.com/spec/v1") {
		return fmt.Errorf("LFS artifact refused")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	copied, err := io.Copy(io.MultiWriter(out, h), f)
	err = errors.Join(err, out.Sync(), out.Close())
	if err != nil {
		return err
	}
	if copied != want.Bytes || hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
		return fmt.Errorf("artifact digest mismatch")
	}
	return nil
}
func blobPath(root, digest string) string { return filepath.Join(root, "blobs", digest[:2], digest) }
func verifyBlob(path string, want ArtifactBlob) error {
	got, n, err := artifactDigest(path)
	if err != nil {
		return err
	}
	if got != want.SHA256 || n != want.Bytes {
		return fmt.Errorf("artifact corruption")
	}
	return nil
}

// CustodyResult durably copies/promotes complete verified candidate bytes before
// its one SQLite receipt transaction. Retry with the same tuple returns the same
// receipt; no caller declaration can create an acknowledged receipt by itself.
func (s *Store) CustodyResult(ctx context.Context, request CustodyRequest) (CustodyReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fixture || s.db == nil || s.artifacts == "" {
		return CustodyReceipt{}, fmt.Errorf("artifact custody unavailable")
	}
	if request.Result.Kind != "result" || request.Result.Version != p.FencedVersion || request.Result.Manifest == nil || p.CheckCurrent(request.Result, request.Result.Identity) != p.OK {
		return CustodyReceipt{}, p.Malformed
	}
	m, blobs, err := canonicalManifest(request.Manifest, request.Result)
	if err != nil {
		return CustodyReceipt{}, err
	}
	h := sha256.Sum256(request.Manifest)
	digest := hex.EncodeToString(h[:])
	want := *request.Result.Manifest
	if want.SHA256 != digest || want.Bytes != int64(len(request.Manifest)) {
		return CustodyReceipt{}, p.IdentityConflict
	}
	if request.RetainUntilMS < time.Now().UnixMilli() {
		return CustodyReceipt{}, fmt.Errorf("retention expired")
	}
	// Stable receipt lookup precedes staging, including a lost acknowledgement.
	var oldID, oldHash string
	var oldBytes int64
	var receipt string
	var quarantined int
	err = s.db.QueryRowContext(ctx, "SELECT manifest_id,manifest_sha256,manifest_bytes,receipt_id,quarantined FROM artifact_results WHERE generation=? AND task_id=? AND attempt_id=? AND epoch=?", request.Result.Identity.Generation, request.Result.Identity.TaskID, request.Result.Identity.AttemptID, request.Result.Identity.Epoch).Scan(&oldID, &oldHash, &oldBytes, &receipt, &quarantined)
	if err == nil {
		if oldID != want.ManifestID || oldHash != want.SHA256 || oldBytes != want.Bytes {
			return CustodyReceipt{}, p.IdentityConflict
		}
		return custodyReply(request.Result, receipt, quarantined != 0), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CustodyReceipt{}, err
	}
	byPath := map[string]ArtifactBlob{}
	for _, b := range blobs {
		byPath[b.Path] = b
	}
	if len(request.Sources) != len(blobs) {
		return CustodyReceipt{}, fmt.Errorf("incomplete artifact sources")
	}
	seen := map[string]bool{}
	for _, src := range request.Sources {
		_, ok := byPath[src.Path]
		if !ok || seen[src.Path] {
			return CustodyReceipt{}, fmt.Errorf("source alias")
		}
		seen[src.Path] = true
	}
	stage := filepath.Join(s.artifacts, "staging", want.ManifestID)
	if err = os.MkdirAll(stage, 0700); err != nil {
		return CustodyReceipt{}, err
	}
	if err = syncDir(filepath.Dir(stage)); err != nil {
		return CustodyReceipt{}, err
	}
	for _, src := range request.Sources {
		b := byPath[src.Path]
		staged := filepath.Join(stage, b.SHA256)
		if err = verifyBlob(staged, b); err == nil {
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(staged)
		}
		if err = copyVerified(src.File, staged, b); err != nil {
			return CustodyReceipt{}, err
		}
	}
	if err = syncDir(stage); err != nil {
		return CustodyReceipt{}, err
	}
	if err = artifactStep("after_staging_fsync"); err != nil {
		return CustodyReceipt{}, err
	}
	for _, b := range blobs {
		dst := blobPath(s.artifacts, b.SHA256)
		if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return CustodyReceipt{}, err
		}
		staged := filepath.Join(stage, b.SHA256)
		if err = verifyBlob(dst, b); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return CustodyReceipt{}, err
		}
		if err = os.Rename(staged, dst); err != nil {
			return CustodyReceipt{}, err
		}
		if err = syncDir(filepath.Dir(dst)); err != nil {
			return CustodyReceipt{}, err
		}
	}
	manifestStage := filepath.Join(stage, "manifest.json")
	if err = os.WriteFile(manifestStage, request.Manifest, 0600); err != nil {
		return CustodyReceipt{}, err
	}
	f, err := os.OpenFile(manifestStage, os.O_RDWR, 0)
	if err != nil {
		return CustodyReceipt{}, err
	}
	err = errors.Join(f.Sync(), f.Close())
	if err != nil {
		return CustodyReceipt{}, err
	}
	manifestDst := filepath.Join(s.artifacts, "manifests", want.ManifestID+".json")
	if err = os.MkdirAll(filepath.Dir(manifestDst), 0700); err != nil {
		return CustodyReceipt{}, err
	}
	if err = os.Rename(manifestStage, manifestDst); err != nil {
		return CustodyReceipt{}, err
	}
	if err = syncDir(filepath.Dir(manifestDst)); err != nil {
		return CustodyReceipt{}, err
	}
	if err = artifactStep("after_promotion_fsync"); err != nil {
		return CustodyReceipt{}, err
	}
	// Stale generations are saved as immutable quarantine evidence, never current.
	q := request.Result.Identity.Generation != s.meta.Generation
	if !q {
		var found int
		err = s.db.QueryRowContext(ctx, "SELECT 1 FROM attempts WHERE id=? AND task_id=? AND epoch=?", request.Result.Identity.AttemptID, request.Result.Identity.TaskID, request.Result.Identity.Epoch).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return CustodyReceipt{}, p.StaleAttempt
		}
		if err != nil {
			return CustodyReceipt{}, err
		}
	}
	receipt = newID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CustodyReceipt{}, err
	}
	defer tx.Rollback()
	stopped, err := controlResultFenced(ctx, tx, request.Result.Identity)
	if err != nil {
		return CustodyReceipt{}, err
	}
	if stopped {
		q = true
		if err := controlFence(ctx, tx, request.Result, "revoked_or_expired"); err != nil {
			return CustodyReceipt{}, err
		}
	}
	now := time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, "INSERT INTO artifact_manifests VALUES(?,?,?,?,?,?)", want.ManifestID, want.SHA256, want.Bytes, request.Manifest, now, map[bool]string{true: "quarantined", false: "committed"}[q]); err != nil {
		return CustodyReceipt{}, err
	}
	roles := map[string]string{}
	for _, v := range m.Tracked {
		roles[v.Path] = "tracked"
	}
	for _, v := range m.Untracked {
		roles[v.Path] = "untracked"
	}
	for _, v := range m.Binary {
		roles[v.Path] = "binary"
	}
	for _, v := range m.Recovery {
		roles[v.Path] = "recovery"
	}
	for _, b := range blobs {
		if _, err = tx.ExecContext(ctx, "INSERT INTO artifact_blobs VALUES(?,?,?,?,?) ON CONFLICT(digest) DO NOTHING", b.SHA256, b.Bytes, now, request.RetainUntilMS, map[bool]string{true: "quarantined", false: "committed"}[q]); err != nil {
			return CustodyReceipt{}, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO artifact_manifest_blobs VALUES(?,?,?,?,?)", want.ManifestID, b.SHA256, roles[b.Path], b.Path, b.Bytes); err != nil {
			return CustodyReceipt{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO artifact_results VALUES(?,?,?,?,?,?,?,?,?,?,?)", request.Result.Identity.Generation, request.Result.Identity.TaskID, request.Result.Identity.AttemptID, request.Result.Identity.Epoch, want.ManifestID, want.SHA256, want.Bytes, receipt, !q, q, now); err != nil {
		return CustodyReceipt{}, err
	}
	if err = artifactStep("before_metadata_commit"); err != nil {
		return CustodyReceipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return CustodyReceipt{}, err
	}
	if err = artifactStep("after_metadata_commit"); err != nil {
		return CustodyReceipt{}, err
	}
	return custodyReply(request.Result, receipt, q), nil
}
func custodyReply(result p.Message, receipt string, q bool) CustodyReceipt {
	r := p.Receipt{Identity: result.Identity, Manifest: *result.Manifest, ReceiptID: receipt, Artifacts: "verified_durable", Metadata: "manifest_and_result_committed"}
	ack := p.Message{Version: result.Version, MessageID: newID(), Kind: "result_ack", Identity: result.Identity, Manifest: result.Manifest, ReceiptID: receipt}
	return CustodyReceipt{Receipt: r, Ack: ack, Quarantined: q}
}

// CollectArtifacts only removes unreferenced, expired staging directories. It
// intentionally leaves quarantine/recovery evidence for explicit operator policy.
func (s *Store) CollectArtifacts(ctx context.Context, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fixture || s.db == nil || s.artifacts == "" {
		return fmt.Errorf("artifact custody unavailable")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT digest FROM artifact_blobs WHERE retained_until_ms<? AND digest NOT IN (SELECT digest FROM artifact_manifest_blobs)", now)
	if err != nil {
		return err
	}
	defer rows.Close()
	var doomed []string
	for rows.Next() {
		var d string
		if err = rows.Scan(&d); err != nil {
			return err
		}
		doomed = append(doomed, d)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	sort.Strings(doomed)
	for _, d := range doomed {
		if err = os.Remove(blobPath(s.artifacts, d)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
