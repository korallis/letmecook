package store

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
	"reflect"
	"slices"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	r "github.com/korallis/letmecook/internal/review"
	v "github.com/korallis/letmecook/internal/verification"
	p "github.com/korallis/letmecook/schemas/execution"
)

const verificationSchema = `
CREATE TABLE verification_candidates (
 sequence INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, task_id TEXT NOT NULL,
 manifest_id TEXT NOT NULL REFERENCES artifact_manifests(manifest_id), previous_id TEXT NOT NULL, body BLOB NOT NULL, sha256 TEXT NOT NULL
) STRICT;
CREATE TABLE verification_reports (
 sequence INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, selection_id TEXT NOT NULL REFERENCES verification_candidates(id),
 body BLOB NOT NULL CHECK(length(body)<=8388608), sha256 TEXT NOT NULL
) STRICT;
CREATE TABLE verification_evidence (
 id TEXT PRIMARY KEY, report_id TEXT NOT NULL REFERENCES verification_reports(id), ordinal INTEGER NOT NULL,
 UNIQUE(report_id,ordinal)
) STRICT;
CREATE TABLE review_decisions (
 sequence INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, selection_id TEXT NOT NULL REFERENCES verification_candidates(id),
 report_id TEXT NOT NULL REFERENCES verification_reports(id), grant_id TEXT NOT NULL REFERENCES execution_grants(id),
 body BLOB NOT NULL CHECK(length(body)<=1048576), sha256 TEXT NOT NULL
) STRICT;
CREATE TABLE review_invocations (
 sequence INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, context_id TEXT NOT NULL UNIQUE,
 report_id TEXT NOT NULL REFERENCES verification_reports(id), body BLOB NOT NULL CHECK(length(body)<=9437184), sha256 TEXT NOT NULL
) STRICT;
CREATE TRIGGER verification_candidates_no_update BEFORE UPDATE ON verification_candidates BEGIN SELECT RAISE(ABORT,'candidate selection immutable'); END;
CREATE TRIGGER verification_candidates_no_delete BEFORE DELETE ON verification_candidates BEGIN SELECT RAISE(ABORT,'candidate selection retained'); END;
CREATE TRIGGER verification_reports_no_update BEFORE UPDATE ON verification_reports BEGIN SELECT RAISE(ABORT,'verification immutable'); END;
CREATE TRIGGER verification_reports_no_delete BEFORE DELETE ON verification_reports BEGIN SELECT RAISE(ABORT,'verification retained'); END;
CREATE TRIGGER verification_evidence_no_update BEFORE UPDATE ON verification_evidence BEGIN SELECT RAISE(ABORT,'evidence immutable'); END;
CREATE TRIGGER verification_evidence_no_delete BEFORE DELETE ON verification_evidence BEGIN SELECT RAISE(ABORT,'evidence retained'); END;
CREATE TRIGGER review_decisions_no_update BEFORE UPDATE ON review_decisions BEGIN SELECT RAISE(ABORT,'decision immutable'); END;
CREATE TRIGGER review_decisions_no_delete BEFORE DELETE ON review_decisions BEGIN SELECT RAISE(ABORT,'decision retained'); END;
CREATE TRIGGER review_invocations_no_update BEFORE UPDATE ON review_invocations BEGIN SELECT RAISE(ABORT,'review invocation immutable'); END;
CREATE TRIGGER review_invocations_no_delete BEFORE DELETE ON review_invocations BEGIN SELECT RAISE(ABORT,'review invocation retained'); END;
PRAGMA user_version=9;
`

func decodeVerification(body []byte, digest string, out any) error {
	if v.Digest(body) != digest {
		return fmt.Errorf("verification metadata corruption")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	canonical, err := json.Marshal(out)
	if err != nil || !bytes.Equal(body, canonical) {
		return fmt.Errorf("noncanonical verification metadata")
	}
	return nil
}
func readSelection(ctx context.Context, tx *sql.Tx, query string, arg string) (v.Candidate, error) {
	var body []byte
	var digest string
	var c v.Candidate
	if err := tx.QueryRowContext(ctx, query, arg).Scan(&body, &digest); err != nil {
		return c, err
	}
	if err := decodeVerification(body, digest, &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}
func currentCandidate(ctx context.Context, tx *sql.Tx, task string) (v.Candidate, error) {
	return readSelection(ctx, tx, "SELECT body,sha256 FROM verification_candidates WHERE task_id=? ORDER BY sequence DESC LIMIT 1", task)
}
func retainedCandidate(ctx context.Context, tx *sql.Tx, c v.Candidate) error {
	got, err := readSelection(ctx, tx, "SELECT body,sha256 FROM verification_candidates WHERE id=?", c.SelectionID)
	if err != nil {
		return err
	}
	if got != c {
		return fmt.Errorf("candidate selection conflict")
	}
	return nil
}
func custodyManifest(ctx context.Context, tx *sql.Tx, ref p.Manifest, generation string) (CandidateManifest, []byte, error) {
	var raw []byte
	var digest, state, gen string
	var size int64
	var q int
	err := tx.QueryRowContext(ctx, `SELECT m.body,m.sha256,m.bytes,m.state,a.generation,a.quarantined FROM artifact_manifests m JOIN artifact_results a ON a.manifest_id=m.manifest_id WHERE m.manifest_id=?`, ref.ManifestID).Scan(&raw, &digest, &size, &state, &gen, &q)
	if err != nil {
		return CandidateManifest{}, nil, err
	}
	if digest != ref.SHA256 || size != ref.Bytes || v.Digest(raw) != digest || int64(len(raw)) != size || state != "committed" || q != 0 || gen != generation {
		return CandidateManifest{}, nil, fmt.Errorf("stale, quarantined or corrupt custody manifest")
	}
	var m CandidateManifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return m, nil, err
	}
	if _, _, err = canonicalManifest(raw, p.Message{Identity: m.Identity}); err != nil {
		return m, nil, err
	}
	if m.Identity.Generation != generation {
		return m, nil, fmt.Errorf("stale candidate generation")
	}
	return m, raw, nil
}

// SelectVerificationCandidate is a trusted caller CAS, not authority. Selection
// history is independent of #18's immutable receipt/current flags. New custody
// references must already exist; this method neither creates nor rewrites them.
func (s *Store) SelectVerificationCandidate(ctx context.Context, expectedSelection string, ref p.Manifest) (v.Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return v.Candidate{}, err
	}
	defer tx.Rollback()
	m, _, err := custodyManifest(ctx, tx, ref, s.meta.Generation)
	if err != nil {
		return v.Candidate{}, err
	}
	old, err := currentCandidate(ctx, tx, m.Identity.TaskID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return v.Candidate{}, err
	}
	// An exact lost-ack retry returns the retained selection, not a new generation.
	if old.Manifest == ref {
		var previous string
		if err = tx.QueryRowContext(ctx, "SELECT previous_id FROM verification_candidates WHERE id=?", old.SelectionID).Scan(&previous); err != nil {
			return v.Candidate{}, err
		}
		if old.SelectionID == expectedSelection || previous == expectedSelection {
			return old, nil
		}
	}
	if old.SelectionID != expectedSelection {
		return v.Candidate{}, fmt.Errorf("candidate selection conflict")
	}
	c := v.Candidate{SelectionID: v.ID(), Identity: m.Identity, Manifest: ref, BaseCommit: m.Base.Revision}
	if err = c.Validate(); err != nil {
		return v.Candidate{}, err
	}
	body, _ := json.Marshal(c)
	if _, err = tx.ExecContext(ctx, "INSERT INTO verification_candidates(id,task_id,manifest_id,previous_id,body,sha256) VALUES(?,?,?,?,?,?)", c.SelectionID, c.Identity.TaskID, ref.ManifestID, expectedSelection, body, v.Digest(body)); err != nil {
		return v.Candidate{}, err
	}
	if err = tx.Commit(); err != nil {
		return v.Candidate{}, err
	}
	return c, nil
}
func (s *Store) CandidateManifest(ctx context.Context, c v.Candidate) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = retainedCandidate(ctx, tx, c); err != nil {
		return nil, err
	}
	m, raw, err := custodyManifest(ctx, tx, c.Manifest, s.meta.Generation)
	if err != nil {
		return nil, err
	}
	if m.Identity != c.Identity || m.Base.Revision != c.BaseCommit {
		return nil, fmt.Errorf("candidate manifest conflict")
	}
	return raw, nil
}
func (s *Store) OpenCandidateBlob(ctx context.Context, c v.Candidate, digest string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !v.IsDigest(digest) {
		return nil, fmt.Errorf("invalid blob digest")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = retainedCandidate(ctx, tx, c); err != nil {
		return nil, err
	}
	var found int
	if err = tx.QueryRowContext(ctx, "SELECT 1 FROM artifact_manifest_blobs WHERE manifest_id=? AND digest=? LIMIT 1", c.Manifest.ManifestID, digest).Scan(&found); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.artifacts)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := filepath.Join("blobs", digest[:2], digest)
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("nonregular candidate blob")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("nonregular opened blob")
	}
	return f, nil // Recreate hashes the actual bytes it copies, not this row.
}

// Acceptance/readiness cannot rely on a database row after the corresponding
// blob has been lost or corrupted. Recheck actual retained bytes at that boundary.
func (s *Store) verifyCandidateContent(ctx context.Context, tx *sql.Tx, c v.Candidate) error {
	m, _, err := custodyManifest(ctx, tx, c.Manifest, s.meta.Generation)
	if err != nil {
		return err
	}
	if m.Identity != c.Identity || m.Base.Revision != c.BaseCommit || m.Base.SHA256 != v.Digest([]byte(c.BaseCommit)) {
		return fmt.Errorf("candidate base/identity mismatch")
	}
	root, err := os.OpenRoot(s.artifacts)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, group := range [][]ArtifactBlob{m.Tracked, m.Untracked, m.Binary, m.Recovery} {
		for _, b := range group {
			if err = ctx.Err(); err != nil {
				return err
			}
			name := filepath.Join("blobs", b.SHA256[:2], b.SHA256)
			info, err := root.Lstat(name)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() != b.Bytes {
				return fmt.Errorf("candidate blob missing or nonregular")
			}
			f, err := root.Open(name)
			if err != nil {
				return err
			}
			h := sha256.New()
			n, copyErr := io.Copy(h, io.LimitReader(f, b.Bytes+1))
			if err = errors.Join(copyErr, f.Close()); err != nil {
				return err
			}
			if n != b.Bytes || hex.EncodeToString(h.Sum(nil)) != b.SHA256 {
				return fmt.Errorf("candidate content digest mismatch")
			}
		}
	}
	return nil
}

// SaveVerification is a trusted in-process verifier boundary, not a worker API.
// It atomically stores the report and all evidence IDs; exact retries are stable.
func (s *Store) SaveVerification(ctx context.Context, report v.Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = retainedCandidate(ctx, tx, report.Candidate); err != nil {
		return err
	}
	body, err := json.Marshal(report)
	if err != nil {
		return err
	}
	duplicate, err := identicalRecord(ctx, tx, "SELECT body FROM verification_reports WHERE id=?", report.ID, body)
	if err != nil || duplicate {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO verification_reports(id,selection_id,body,sha256) VALUES(?,?,?,?)", report.ID, report.Candidate.SelectionID, body, v.Digest(body)); err != nil {
		return err
	}
	for n, e := range report.Evidence {
		if _, err = tx.ExecContext(ctx, "INSERT INTO verification_evidence VALUES(?,?,?)", e.ID, report.ID, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func identicalRecord(ctx context.Context, tx *sql.Tx, query, id string, body []byte) (bool, error) {
	var old []byte
	err := tx.QueryRowContext(ctx, query, id).Scan(&old)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !bytes.Equal(body, old) {
		return false, fmt.Errorf("immutable record identity conflict")
	}
	return true, nil
}
func loadVerification(ctx context.Context, tx *sql.Tx, id string) (v.Report, error) {
	var body []byte
	var digest string
	var report v.Report
	if err := tx.QueryRowContext(ctx, "SELECT body,sha256 FROM verification_reports WHERE id=?", id).Scan(&body, &digest); err != nil {
		return report, err
	}
	if err := decodeVerification(body, digest, &report); err != nil {
		return report, err
	}
	if report.ID != id {
		return report, fmt.Errorf("verification identity conflict")
	}
	if err := report.Validate(); err != nil {
		return report, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM verification_evidence WHERE report_id=? ORDER BY ordinal", id)
	if err != nil {
		return report, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return report, err
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return report, err
	}
	if !slices.Equal(ids, r.EvidenceIDs(report)) {
		return report, fmt.Errorf("incomplete verification evidence")
	}
	return report, nil
}
func (s *Store) Verification(ctx context.Context, id string) (v.Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return v.Report{}, err
	}
	defer tx.Rollback()
	return loadVerification(ctx, tx, id)
}

// CurrentVerification derives status from committed evidence and the current
// selection. History reads alone are not a verified-status assertion.
func (s *Store) CurrentVerification(ctx context.Context, task string) (v.Report, v.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return v.Report{}, v.Status{}, err
	}
	defer tx.Rollback()
	c, err := currentCandidate(ctx, tx, task)
	if err != nil {
		return v.Report{}, v.Status{}, err
	}
	report, err := latestVerification(ctx, tx, c.SelectionID)
	if errors.Is(err, sql.ErrNoRows) {
		return v.Report{}, v.Status{Reasons: []string{"current candidate has no verification"}}, nil
	}
	if err != nil {
		return v.Report{}, v.Status{}, err
	}
	status := v.Evaluate(report, c)
	if c.Identity.Generation != s.meta.Generation {
		status.Verified = false
		status.Reasons = append(status.Reasons, "candidate generation stale")
	}
	if status.Verified {
		if err = s.verifyCandidateContent(ctx, tx, c); err != nil {
			return report, v.Status{}, err
		}
	}
	return report, status, nil
}
func latestVerification(ctx context.Context, tx *sql.Tx, selection string) (v.Report, error) {
	var id string
	if err := tx.QueryRowContext(ctx, "SELECT id FROM verification_reports WHERE selection_id=? ORDER BY sequence DESC LIMIT 1", selection).Scan(&id); err != nil {
		return v.Report{}, err
	}
	return loadVerification(ctx, tx, id)
}

// RecordLocalDecision records a trusted operator's provisional local intent only.
// A referenced execution grant is provenance, never local-acceptance authority.
func (s *Store) RecordLocalDecision(ctx context.Context, d r.Decision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	report, err := loadVerification(ctx, tx, d.VerificationID)
	if err != nil {
		return err
	}
	if err = r.ValidateDecision(d, report); err != nil {
		return err
	}
	body, err := json.Marshal(d)
	if err != nil || len(body) > 1<<20 {
		return fmt.Errorf("decision bounds")
	}
	duplicate, err := identicalRecord(ctx, tx, "SELECT body FROM review_decisions WHERE id=?", d.ID, body)
	if err != nil || duplicate {
		return err
	} // retry is a receipt, not renewed acceptance
	current, err := currentCandidate(ctx, tx, d.Candidate.Identity.TaskID)
	if err != nil {
		return err
	}
	if current != d.Candidate {
		return fmt.Errorf("candidate replaced; decision refused")
	}
	latest, err := latestVerification(ctx, tx, current.SelectionID)
	if err != nil {
		return err
	}
	if latest.ID != report.ID {
		return fmt.Errorf("verification superseded")
	}
	grant, err := loadGrant(ctx, tx, d.Grant.ID)
	if err != nil {
		return err
	}
	if grant.TaskID != current.Identity.TaskID || grant.Revision != d.Grant.Revision || grant.Envelope.BaseCommit != current.BaseCommit {
		return fmt.Errorf("decision grant identity mismatch")
	}
	criteria := make([]string, 0, len(d.Coverage))
	for _, c := range d.Coverage {
		criteria = append(criteria, c.CriterionID)
	}
	slices.Sort(criteria)
	if !slices.Equal(criteria, grant.Envelope.CriterionIDs) {
		return fmt.Errorf("decision criterion coverage incomplete")
	}
	if d.Action == "accept" {
		if err = s.verifyCandidateContent(ctx, tx, current); err != nil {
			return err
		}
		head, err := headGrant(ctx, tx, grant.TaskID)
		if err != nil {
			return err
		}
		if head.ID != grant.ID {
			return fmt.Errorf("grant superseded")
		}
		if err = liveGrant(ctx, tx, grant, time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO review_decisions(id,selection_id,report_id,grant_id,body,sha256) VALUES(?,?,?,?,?,?)", d.ID, current.SelectionID, report.ID, grant.ID, body, v.Digest(body)); err != nil {
		return err
	}
	return tx.Commit()
}
func decisionHistory(ctx context.Context, tx *sql.Tx, task string) ([]r.Decision, error) {
	rows, err := tx.QueryContext(ctx, `SELECT d.body,d.sha256 FROM review_decisions d JOIN verification_candidates c ON c.id=d.selection_id WHERE c.task_id=? ORDER BY d.sequence`, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []r.Decision{}
	for rows.Next() {
		var body []byte
		var digest string
		var d r.Decision
		if err = rows.Scan(&body, &digest); err != nil {
			return nil, err
		}
		if err = decodeVerification(body, digest, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) LocalDecisionHistory(ctx context.Context, task string) ([]r.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return decisionHistory(ctx, tx, task)
}
func (s *Store) CurrentLocalReview(ctx context.Context, task string) (r.Current, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return r.Current{}, err
	}
	defer tx.Rollback()
	c, err := currentCandidate(ctx, tx, task)
	if err != nil {
		return r.Current{}, err
	}
	if c.Identity.Generation != s.meta.Generation {
		return r.Current{Reasons: []string{"candidate generation stale"}}, nil
	}
	history, err := decisionHistory(ctx, tx, task)
	if err != nil {
		return r.Current{}, err
	}
	report, err := latestVerification(ctx, tx, c.SelectionID)
	if errors.Is(err, sql.ErrNoRows) {
		return r.Current{Reasons: []string{"current candidate has no verification"}}, nil
	}
	if err != nil {
		return r.Current{}, err
	}
	out := r.Derive(c, report, history)
	if out.Accepted {
		if err = s.verifyCandidateContent(ctx, tx, c); err != nil {
			return r.Current{}, err
		}
		grant, err := headGrant(ctx, tx, task)
		if err != nil {
			return r.Current{}, err
		}
		liveErr := liveGrant(ctx, tx, grant, time.Now().UnixMilli())
		var refusal *g.Refusal
		if liveErr != nil && !errors.As(liveErr, &refusal) {
			return r.Current{}, liveErr
		}
		if grant.ID != out.Decision.Grant.ID || liveErr != nil {
			out.Accepted = false
			out.Reasons = append(out.Reasons, "grant no longer current/live")
		}
	}
	return out, nil
}

func (s *Store) RecordReviewInvocation(ctx context.Context, i r.Invocation) error {
	if err := i.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	report, err := loadVerification(ctx, tx, i.Verification.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(report, i.Verification) {
		return fmt.Errorf("review must contain actual retained verification")
	}
	body, err := json.Marshal(i)
	if err != nil || len(body) > 9<<20 {
		return fmt.Errorf("review packet bounds")
	}
	duplicate, err := identicalRecord(ctx, tx, "SELECT body FROM review_invocations WHERE id=?", i.ID, body)
	if err != nil || duplicate {
		return err
	}
	current, err := currentCandidate(ctx, tx, i.Candidate.Identity.TaskID)
	if err != nil {
		return err
	}
	if current != i.Candidate {
		return fmt.Errorf("review candidate replaced")
	}
	latest, err := latestVerification(ctx, tx, current.SelectionID)
	if err != nil {
		return err
	}
	if latest.ID != report.ID {
		return fmt.Errorf("review verification superseded")
	}
	grant, err := loadGrant(ctx, tx, i.Grant.ID)
	if err != nil {
		return err
	}
	if grant.TaskID != current.Identity.TaskID || grant.Revision != i.Grant.Revision || grant.Envelope.BaseCommit != current.BaseCommit {
		return fmt.Errorf("review grant identity mismatch")
	}
	criteria := make([]string, 0, len(i.Criteria))
	for _, criterion := range i.Criteria {
		criteria = append(criteria, criterion.ID)
	}
	slices.Sort(criteria)
	if !slices.Equal(criteria, grant.Envelope.CriterionIDs) {
		return fmt.Errorf("review criteria do not match retained grant")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO review_invocations(id,context_id,report_id,body,sha256) VALUES(?,?,?,?,?)", i.ID, i.ContextID, report.ID, body, v.Digest(body)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ReviewInvocation(ctx context.Context, id string) (r.Invocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return r.Invocation{}, err
	}
	defer tx.Rollback()
	var body []byte
	var digest string
	var i r.Invocation
	if err = tx.QueryRowContext(ctx, "SELECT body,sha256 FROM review_invocations WHERE id=?", id).Scan(&body, &digest); err != nil {
		return i, err
	}
	if err = decodeVerification(body, digest, &i); err != nil {
		return i, err
	}
	if i.ID != id {
		return i, fmt.Errorf("review identity mismatch")
	}
	return i, i.Validate()
}
