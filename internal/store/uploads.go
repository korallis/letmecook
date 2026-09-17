package store

// Upload store half (docs/decisions/0002 §4, routes /x/v1/attempts/{id}/uploads,
// /x/v1/uploads/{id}/blobs/{sha256} and /x/v1/uploads/{id}/commit). Blobs stage
// under <artifacts-dir>/upload/<upload_id>/<sha256>: each PUT is hashed while
// written to a temp file, fsynced, renamed to its digest name and the directory
// fsynced before the upload_blobs row is marked staged. CommitUpload hands the
// staged files to CustodyResult, which is the only custody path and whose reply
// is deterministic in the receipt, so a lost commit reply is replayed
// byte-identically. Custody never terminalizes an attempt; FinalizeAttempt does.
// Bulk bytes never move under the store lock: admission, staging and the row
// update are three steps, and only the row commits are serialized.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
)

const (
	// MaxBlobBytes bounds one PUT; MaxUploadBytes bounds every upload of one attempt.
	MaxBlobBytes   int64 = 64 << 20
	MaxUploadBytes int64 = 256 << 20
	// uploadRetention is the custody retention CommitUpload requests. Deletion
	// stays an explicit operator policy (CollectArtifacts), never automatic.
	uploadRetention  = 30 * 24 * time.Hour
	maxMissingListed = 32
)

// UploadBegin is the POST /x/v1/attempts/{id}/uploads body. Manifest is the raw
// canonical manifest; its JSON form is the wire's manifest_base64 string.
type UploadBegin struct {
	Version   string    `json:"version"`
	MessageID string    `json:"message_id"`
	Result    p.Message `json:"result"`
	Manifest  []byte    `json:"manifest_base64"`
}

// MissingBlob names one manifest blob the daemon does not hold yet.
type MissingBlob struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// UploadSession is the reply to UploadBegin and mirrors the upload_sessions row.
// Missing is the exact inventory still to PUT; BytesAllowed is the remaining
// per-attempt allowance (256 MiB aggregate).
type UploadSession struct {
	UploadID     string        `json:"upload_id"`
	Missing      []MissingBlob `json:"missing,omitempty"`
	BytesAllowed int64         `json:"bytes_allowed"`
}

// UploadedBlob is the reply to one blob PUT. Duplicate reports 200 instead of 201.
type UploadedBlob struct {
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
	Duplicate bool   `json:"duplicate"`
}

// CommitReply is the POST /x/v1/uploads/{id}/commit reply: the custody
// acknowledgement and receipt, byte-identical on replay.
type CommitReply struct {
	Ack         p.Message `json:"ack"`
	Receipt     p.Receipt `json:"receipt"`
	Quarantined bool      `json:"quarantined"`
}

type uploadRow struct {
	ID, AttemptID, RunnerID, ManifestID, ManifestSHA256, State string
	ManifestBytes, BytesTotal                                  int64
	Result                                                     p.Message
}

func (s *Store) uploadDir(id string) string { return filepath.Join(s.artifacts, "upload", id) }

func loadUpload(ctx context.Context, tx *sql.Tx, id string) (uploadRow, error) {
	var v uploadRow
	var result string
	err := tx.QueryRowContext(ctx, "SELECT id,attempt_id,runner_id,manifest_id,manifest_sha256,manifest_bytes,result,state,bytes_total FROM upload_sessions WHERE id=?", id).Scan(&v.ID, &v.AttemptID, &v.RunnerID, &v.ManifestID, &v.ManifestSHA256, &v.ManifestBytes, &result, &v.State, &v.BytesTotal)
	if err != nil {
		return v, err
	}
	if v.Result, err = p.Decode([]byte(result)); err != nil {
		return v, g.Deny("corrupt_record", "upload")
	}
	return v, nil
}

func attemptUploadBytes(ctx context.Context, tx *sql.Tx, attemptID string) (int64, error) {
	var used int64
	err := tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(bytes_total),0) FROM upload_sessions WHERE attempt_id=?", attemptID).Scan(&used)
	return used, err
}

func uploadView(ctx context.Context, tx *sql.Tx, row uploadRow) (UploadSession, error) {
	v := UploadSession{UploadID: row.ID, Missing: []MissingBlob{}}
	rows, err := tx.QueryContext(ctx, "SELECT digest,bytes FROM upload_blobs WHERE upload_id=? AND state='missing' ORDER BY digest", row.ID)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var b MissingBlob
		if err := rows.Scan(&b.SHA256, &b.Bytes); err != nil {
			rows.Close()
			return v, err
		}
		v.Missing = append(v.Missing, b)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return v, err
	}
	used, err := attemptUploadBytes(ctx, tx, row.AttemptID)
	if err != nil {
		return v, err
	}
	v.BytesAllowed = max(MaxUploadBytes-used, 0)
	return v, nil
}

// uploadOwner loads an upload session the runner owns.
func (s *Store) uploadOwner(ctx context.Context, tx *sql.Tx, fingerprint, session, uploadID string) (uploadRow, error) {
	if !p.ValidID(uploadID) {
		return uploadRow{}, g.Deny("malformed", "upload_id")
	}
	who, _, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return uploadRow{}, err
	}
	row, err := loadUpload(ctx, tx, uploadID)
	if err != nil {
		return uploadRow{}, err
	}
	if row.RunnerID != who.ID {
		return uploadRow{}, g.Deny("runner_disabled", "upload")
	}
	return row, nil
}

// BeginUpload binds result.identity, the manifest hash and the blob inventory
// before any bytes arrive; the attempt must be result_pending and owned by the
// runner (a stale-generation identity is admitted so its bytes can be retained
// and quarantined at commit, never made current). Durable point: the manifest
// file under the upload directory plus the upload_sessions and upload_blobs
// rows committed before the reply; the same message_id replays the same
// session, a second session for the same manifest reuses the open one, and a
// different manifest for the attempt is identity_conflict.
func (s *Store) BeginUpload(ctx context.Context, fingerprint, session, attemptID string, begin UploadBegin) (UploadSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fixture || s.artifacts == "" {
		return UploadSession{}, fmt.Errorf("artifact custody unavailable")
	}
	if begin.Version != execwire.Version || !p.ValidID(begin.MessageID) || !p.ValidID(attemptID) {
		return UploadSession{}, p.Malformed
	}
	result := begin.Result
	if result.Kind != "result" || result.Version != p.FencedVersion || result.Manifest == nil || result.Identity.AttemptID != attemptID {
		return UploadSession{}, p.Malformed
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return UploadSession{}, err
	}
	defer tx.Rollback()
	who, _, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return UploadSession{}, err
	}
	d, err := runnerDispatch(ctx, tx, who, attemptID)
	if err != nil {
		return UploadSession{}, err
	}
	identity := d.Assignment.Identity
	if r := p.CheckCurrent(result, identity); r != p.OK {
		return UploadSession{}, r
	}
	_, blobs, err := canonicalManifest(begin.Manifest, result)
	if err != nil {
		return UploadSession{}, p.Malformed
	}
	sum := sha256.Sum256(begin.Manifest)
	want := *result.Manifest
	if want.SHA256 != hex.EncodeToString(sum[:]) || want.Bytes != int64(len(begin.Manifest)) {
		return UploadSession{}, p.IdentityConflict
	}
	inventory := map[string]int64{}
	digests := []string{}
	var total int64
	for _, b := range blobs {
		if bytes, seen := inventory[b.SHA256]; seen {
			if bytes != b.Bytes {
				return UploadSession{}, p.Malformed
			}
			continue
		}
		if b.Bytes > MaxBlobBytes {
			return UploadSession{}, p.Oversized
		}
		inventory[b.SHA256] = b.Bytes
		digests = append(digests, b.SHA256)
		total += b.Bytes
	}
	if total > MaxUploadBytes {
		return UploadSession{}, p.Oversized
	}
	stale := identity.Generation != s.meta.Generation
	if !stale {
		state, _, err := attemptState(ctx, tx, attemptID)
		if err != nil {
			return UploadSession{}, err
		}
		if state != p.ResultPending {
			return UploadSession{}, p.ReconciliationRequired
		}
	}
	var oldManifest, oldSHA string
	var oldBytes int64
	err = tx.QueryRowContext(ctx, "SELECT manifest_id,manifest_sha256,manifest_bytes FROM artifact_results WHERE generation=? AND task_id=? AND attempt_id=? AND epoch=?", identity.Generation, identity.TaskID, identity.AttemptID, identity.Epoch).Scan(&oldManifest, &oldSHA, &oldBytes)
	if err == nil && (oldManifest != want.ManifestID || oldSHA != want.SHA256 || oldBytes != want.Bytes) {
		return UploadSession{}, p.IdentityConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return UploadSession{}, err
	}
	id := dispatchID(begin.MessageID, "upload")
	old, err := loadUpload(ctx, tx, id)
	if err == nil {
		if old.AttemptID != attemptID || old.RunnerID != who.ID || old.ManifestID != want.ManifestID || old.ManifestSHA256 != want.SHA256 || !reflect.DeepEqual(old.Result, result) {
			return UploadSession{}, p.IdentityConflict
		}
		return uploadView(ctx, tx, old)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return UploadSession{}, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,manifest_sha256,state FROM upload_sessions WHERE attempt_id=? ORDER BY created_ms", attemptID)
	if err != nil {
		return UploadSession{}, err
	}
	reuse := ""
	for rows.Next() {
		var otherID, otherSHA, otherState string
		if err := rows.Scan(&otherID, &otherSHA, &otherState); err != nil {
			rows.Close()
			return UploadSession{}, err
		}
		if otherState == "abandoned" {
			continue
		}
		if otherSHA != want.SHA256 {
			rows.Close()
			return UploadSession{}, p.IdentityConflict
		}
		if otherState == "open" && reuse == "" {
			reuse = otherID
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return UploadSession{}, err
	}
	if reuse != "" {
		row, err := loadUpload(ctx, tx, reuse)
		if err != nil {
			return UploadSession{}, err
		}
		return uploadView(ctx, tx, row)
	}
	dir := s.uploadDir(id)
	if err := os.RemoveAll(dir); err != nil {
		return UploadSession{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return UploadSession{}, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "manifest.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return UploadSession{}, err
	}
	_, err = f.Write(begin.Manifest)
	if err = errors.Join(err, f.Sync(), f.Close()); err != nil {
		return UploadSession{}, err
	}
	if err := errors.Join(syncDir(dir), syncDir(filepath.Dir(dir)), syncDir(s.artifacts)); err != nil {
		return UploadSession{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO upload_sessions VALUES(?,?,?,?,?,?,?,'open',0,?)", id, attemptID, who.ID, want.ManifestID, want.SHA256, want.Bytes, controlJSON(result), time.Now().UnixMilli()); err != nil {
		return UploadSession{}, err
	}
	for _, digest := range digests {
		if _, err := tx.ExecContext(ctx, "INSERT INTO upload_blobs VALUES(?,?,?,'missing')", id, digest, inventory[digest]); err != nil {
			return UploadSession{}, err
		}
	}
	row := uploadRow{ID: id, AttemptID: attemptID}
	v, err := uploadView(ctx, tx, row)
	if err != nil {
		return UploadSession{}, err
	}
	if err := s.step("before_upload_commit"); err != nil {
		return UploadSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return UploadSession{}, err
	}
	if err := s.step("after_upload_commit"); err != nil {
		return UploadSession{}, err
	}
	return v, nil
}

// blobAdmit checks one PUT against the open session before any byte is read.
func (s *Store) blobAdmit(ctx context.Context, fingerprint, session, uploadID, digest string, length int64) (uploadRow, int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return uploadRow{}, 0, false, err
	}
	defer tx.Rollback()
	row, err := s.uploadOwner(ctx, tx, fingerprint, session, uploadID)
	if err != nil {
		return uploadRow{}, 0, false, err
	}
	if row.State != "open" {
		return uploadRow{}, 0, false, p.ReconciliationRequired
	}
	var want int64
	var state string
	err = tx.QueryRowContext(ctx, "SELECT bytes,state FROM upload_blobs WHERE upload_id=? AND digest=?", uploadID, digest).Scan(&want, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return uploadRow{}, 0, false, p.IdentityConflict
	}
	if err != nil {
		return uploadRow{}, 0, false, err
	}
	if length >= 0 && length != want {
		return uploadRow{}, 0, false, g.Deny("digest_mismatch", "length")
	}
	if state != "missing" {
		if verifyBlob(filepath.Join(s.uploadDir(uploadID), digest), ArtifactBlob{SHA256: digest, Bytes: want}) == nil {
			return row, want, true, nil
		}
	}
	used, err := attemptUploadBytes(ctx, tx, row.AttemptID)
	if err != nil {
		return uploadRow{}, 0, false, err
	}
	if used+want > MaxUploadBytes {
		return uploadRow{}, 0, false, p.Oversized
	}
	return row, want, false, nil
}

// stageBlob streams exactly want bytes into a temp file, hashing as it writes,
// fsyncs, and only then renames the file to its digest name and fsyncs the
// directory. A short, long or mismatching body removes the temp file.
func (s *Store) stageBlob(uploadID, digest string, body io.Reader, want int64) error {
	dir := s.uploadDir(uploadID)
	final := filepath.Join(dir, digest)
	tmp := final + ".part-" + newID()
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.CopyN(io.MultiWriter(f, h), body, want)
	var probe [1]byte
	_, probeErr := io.ReadFull(body, probe[:])
	err = errors.Join(f.Sync(), f.Close())
	mismatch := copyErr != nil || n != want || !errors.Is(probeErr, io.EOF) || hex.EncodeToString(h.Sum(nil)) != digest
	if err != nil || mismatch {
		removeErr := os.Remove(tmp)
		if err != nil {
			return errors.Join(err, removeErr)
		}
		return g.Deny("digest_mismatch", "bytes")
	}
	if err := s.step("after_blob_fsync"); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	if err := os.Rename(tmp, final); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return syncDir(dir)
}

// blobStaged marks a durably staged blob and charges its bytes to the attempt.
func (s *Store) blobStaged(ctx context.Context, fingerprint, session, uploadID, digest string, want int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	row, err := s.uploadOwner(ctx, tx, fingerprint, session, uploadID)
	if err != nil {
		return false, err
	}
	if row.State != "open" {
		return false, p.ReconciliationRequired
	}
	var state string
	if err := tx.QueryRowContext(ctx, "SELECT state FROM upload_blobs WHERE upload_id=? AND digest=?", uploadID, digest).Scan(&state); err != nil {
		return false, err
	}
	if state != "missing" {
		return true, nil
	}
	used, err := attemptUploadBytes(ctx, tx, row.AttemptID)
	if err != nil {
		return false, err
	}
	if used+want > MaxUploadBytes {
		return false, errors.Join(p.Oversized, os.Remove(filepath.Join(s.uploadDir(uploadID), digest)))
	}
	if _, err := tx.ExecContext(ctx, "UPDATE upload_blobs SET state='staged' WHERE upload_id=? AND digest=?", uploadID, digest); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE upload_sessions SET bytes_total=bytes_total+? WHERE id=?", want, uploadID); err != nil {
		return false, err
	}
	if err := s.step("before_blob_commit"); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, s.step("after_blob_commit")
}

// RecordUploadedBlob stores one blob of exactly length bytes (0..64 MiB) from
// body; a negative length means the inventory's promised size. Durable point:
// the temp file is hashed while written, fsynced, renamed to its digest name and
// the directory fsynced, then the upload_blobs row is marked staged and
// committed. A digest mismatch removes the temp file (422 digest_mismatch); a
// duplicate returns Duplicate without reading; exceeding 256 MiB per attempt is
// refused (413) before any byte is stored.
func (s *Store) RecordUploadedBlob(ctx context.Context, fingerprint, session, uploadID, sha256 string, body io.Reader, length int64) (UploadedBlob, error) {
	if !validHex(sha256) || length > MaxBlobBytes {
		return UploadedBlob{}, p.Malformed
	}
	if s.fixture || s.artifacts == "" {
		return UploadedBlob{}, fmt.Errorf("artifact custody unavailable")
	}
	_, want, duplicate, err := s.blobAdmit(ctx, fingerprint, session, uploadID, sha256, length)
	if err != nil {
		return UploadedBlob{}, err
	}
	if duplicate {
		return UploadedBlob{SHA256: sha256, Bytes: want, Duplicate: true}, nil
	}
	if err := s.stageBlob(uploadID, sha256, body, want); err != nil {
		return UploadedBlob{}, err
	}
	duplicate, err = s.blobStaged(ctx, fingerprint, session, uploadID, sha256, want)
	if err != nil {
		return UploadedBlob{}, err
	}
	return UploadedBlob{SHA256: sha256, Bytes: want, Duplicate: duplicate}, nil
}

// commitAdmit reads what a commit needs: the session, its retained custody (if
// any), the missing inventory and the staged files to verify.
func (s *Store) commitAdmit(ctx context.Context, fingerprint, session, uploadID string) (uploadRow, *CommitReply, []string, []ArtifactBlob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return uploadRow{}, nil, nil, nil, err
	}
	defer tx.Rollback()
	row, err := s.uploadOwner(ctx, tx, fingerprint, session, uploadID)
	if err != nil {
		return uploadRow{}, nil, nil, nil, err
	}
	var receipt string
	var quarantined bool
	err = tx.QueryRowContext(ctx, "SELECT receipt_id,quarantined FROM artifact_results WHERE manifest_id=?", row.ManifestID).Scan(&receipt, &quarantined)
	if err == nil {
		custody := custodyReply(row.Result, receipt, quarantined)
		return row, &CommitReply{Ack: custody.Ack, Receipt: custody.Receipt, Quarantined: custody.Quarantined}, nil, nil, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uploadRow{}, nil, nil, nil, err
	}
	if row.State != "open" {
		return uploadRow{}, nil, nil, nil, p.ReconciliationRequired
	}
	rows, err := tx.QueryContext(ctx, "SELECT digest,bytes,state FROM upload_blobs WHERE upload_id=? ORDER BY digest", uploadID)
	if err != nil {
		return uploadRow{}, nil, nil, nil, err
	}
	var missing []string
	var staged []ArtifactBlob
	for rows.Next() {
		var b ArtifactBlob
		var state string
		if err := rows.Scan(&b.SHA256, &b.Bytes, &state); err != nil {
			rows.Close()
			return uploadRow{}, nil, nil, nil, err
		}
		if state == "missing" {
			missing = append(missing, b.SHA256)
		} else {
			staged = append(staged, b)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return uploadRow{}, nil, nil, nil, err
	}
	return row, nil, missing, staged, nil
}

func incomplete(missing []string) error {
	listed := missing
	if len(listed) > maxMissingListed {
		listed = listed[:maxMissingListed]
	}
	return g.Deny("upload_incomplete", strings.Join(listed, ","))
}

// commitMark closes the session after custody committed; broken names blobs
// whose staged bytes no longer verify and are sent back to missing instead.
func (s *Store) commitMark(ctx context.Context, fingerprint, session, uploadID string, broken []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := s.uploadOwner(ctx, tx, fingerprint, session, uploadID)
	if err != nil {
		return err
	}
	if row.State != "open" {
		return nil
	}
	if len(broken) > 0 {
		for _, digest := range broken {
			if _, err := tx.ExecContext(ctx, "UPDATE upload_blobs SET state='missing' WHERE upload_id=? AND digest=?", uploadID, digest); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, "UPDATE upload_blobs SET state='committed' WHERE upload_id=?", uploadID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE upload_sessions SET state='committed' WHERE id=?", uploadID); err != nil {
		return err
	}
	if err := s.step("before_commit_upload_commit"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.step("after_commit_upload_commit")
}

// CommitUpload verifies the inventory is complete (else upload_incomplete listing
// the missing digests), re-verifies every staged file, builds
// CustodyRequest{Sources: staged} and calls CustodyResult. Durable point: custody
// promotion and the metadata commit, then the session's committed mark; a lost
// reply returns the byte-identical acknowledgement (result_ack message_id is
// dispatchID(receipt_id, "ack")) whether or not the session mark landed.
func (s *Store) CommitUpload(ctx context.Context, fingerprint, session, uploadID, messageID string) (CommitReply, error) {
	if !p.ValidID(messageID) {
		return CommitReply{}, p.Malformed
	}
	if s.fixture || s.artifacts == "" {
		return CommitReply{}, fmt.Errorf("artifact custody unavailable")
	}
	row, replay, missing, staged, err := s.commitAdmit(ctx, fingerprint, session, uploadID)
	if err != nil {
		return CommitReply{}, err
	}
	if replay != nil {
		if err := s.commitMark(ctx, fingerprint, session, uploadID, nil); err != nil {
			return CommitReply{}, err
		}
		return *replay, nil
	}
	if len(missing) > 0 {
		return CommitReply{}, incomplete(missing)
	}
	dir := s.uploadDir(uploadID)
	var broken []string
	for _, b := range staged {
		if err := verifyBlob(filepath.Join(dir, b.SHA256), b); err != nil {
			broken = append(broken, b.SHA256)
		}
	}
	if len(broken) > 0 {
		if err := s.commitMark(ctx, fingerprint, session, uploadID, broken); err != nil {
			return CommitReply{}, err
		}
		return CommitReply{}, incomplete(broken)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return CommitReply{}, err
	}
	sum := sha256.Sum256(manifest)
	if hex.EncodeToString(sum[:]) != row.ManifestSHA256 || int64(len(manifest)) != row.ManifestBytes {
		return CommitReply{}, g.Deny("corrupt_record", "upload_manifest")
	}
	_, blobs, err := canonicalManifest(manifest, row.Result)
	if err != nil {
		return CommitReply{}, g.Deny("corrupt_record", "upload_manifest")
	}
	sources := make([]ArtifactSource, 0, len(blobs))
	for _, b := range blobs {
		sources = append(sources, ArtifactSource{Path: b.Path, File: filepath.Join(dir, b.SHA256)})
	}
	custody, err := s.CustodyResult(ctx, CustodyRequest{Result: row.Result, Manifest: manifest, Sources: sources, RetainUntilMS: time.Now().Add(uploadRetention).UnixMilli()})
	if err != nil {
		return CommitReply{}, err
	}
	if err := s.step("after_custody"); err != nil {
		return CommitReply{}, err
	}
	if err := s.commitMark(ctx, fingerprint, session, uploadID, nil); err != nil {
		return CommitReply{}, err
	}
	// Custody holds verified copies and the receipt replays from metadata, so
	// the staging directory is disposable; its removal is best effort.
	_ = os.RemoveAll(dir)
	return CommitReply{Ack: custody.Ack, Receipt: custody.Receipt, Quarantined: custody.Quarantined}, nil
}
