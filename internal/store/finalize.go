package store

// Finalization store half (docs/decisions/0002 §4, POST /x/v1/attempts/{id}/finalize).
// Finalization is evidence-gated: a runner success flag is never enough, and
// nothing reruns after a crash between custody and finalize. The receipt, the
// exit observation, the sink watermark, the boundary accounting, the stop
// latch, the grant and the boots are all checked inside the one transaction
// that moves result_pending to succeeded|failed, records the terminal event,
// releases the reservation and (on success) moves the task's result head.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
)

// ExitRecord is the observed process exit for the attempt's job process group.
type ExitRecord struct {
	Code           int   `json:"code"`
	PGID           int   `json:"pgid"`
	ObservedUnixNS int64 `json:"observed_unix_ns"`
}

// Completion is the finalize body: the custody receipt, the stream watermark the
// runner claims, the exit observation and the boundary's terminal state.
type Completion struct {
	Version   string          `json:"version"`
	MessageID string          `json:"message_id"`
	ReceiptID string          `json:"receipt_id"`
	Stream    StreamWatermark `json:"stream"`
	Exit      ExitRecord      `json:"exit"`
	Boundary  BoundaryState   `json:"boundary"`
}

// FinalizeReply reports the terminal outcome (succeeded|failed) and that the
// reservation was released in the same transaction.
type FinalizeReply struct {
	Outcome  string `json:"outcome"`
	Released bool   `json:"released"`
}

// exitObserved reports whether a retained exit observation matches the claimed exit.
func exitObserved(ctx context.Context, tx *sql.Tx, attemptID string, exit ExitRecord) (bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT body FROM runtime_observations WHERE attempt_id=? AND kind='exit'", attemptID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		var v runtimeRecord
		if err := rows.Scan(&body); err != nil {
			return false, err
		}
		if json.Unmarshal([]byte(body), &v) != nil || v.Evidence == nil {
			continue
		}
		e := v.Evidence
		if e.Kind == "exit" && e.PGIDEmpty && e.Code == exit.Code && e.PGID == exit.PGID && e.ObservedUnixNS == exit.ObservedUnixNS {
			return true, nil
		}
	}
	return false, rows.Err()
}

// FinalizeAttempt verifies a non-quarantined current-generation receipt, a sink
// watermark at least the claimed one with an equal digest, an exit observation in
// runtime_observations, boundary.Quiescent, no stop latch, a live grant and
// current boots; incomplete evidence is reconciliation_required and a latched
// stop is stop_latched. Durable point: one committed transaction holding the CAS
// result_pending -> succeeded|failed from the manifest outcome, the event
// dispatchID(receipt_id, "terminal"), the dispatch_releases row (actor: runner
// principal), tasks.state awaiting_review|reconciling and, for succeeded, the
// artifact_result_heads upsert. Replay for the same receipt returns the same reply.
func (s *Store) FinalizeAttempt(ctx context.Context, fingerprint, session, attemptID string, completion Completion) (FinalizeReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if completion.Version != execwire.Version || !p.ValidID(completion.MessageID) || !p.ValidID(completion.ReceiptID) || completion.Stream.Through < 0 || completion.Stream.Through > p.MaxInteger || !validHex(completion.Stream.Digest) {
		return FinalizeReply{}, p.Malformed
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return FinalizeReply{}, err
	}
	defer tx.Rollback()
	who, sess, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return FinalizeReply{}, err
	}
	d, err := runnerDispatch(ctx, tx, who, attemptID)
	if err != nil {
		return FinalizeReply{}, err
	}
	identity := d.Assignment.Identity
	if identity.Generation != s.meta.Generation {
		return FinalizeReply{}, p.StaleGeneration
	}
	if sess.Paused {
		return FinalizeReply{}, g.Deny("paused", "finalize")
	}
	terminalID := dispatchID(completion.ReceiptID, "terminal")
	state, revision, err := attemptState(ctx, tx, attemptID)
	if err != nil {
		return FinalizeReply{}, err
	}
	if state == p.Succeeded || state == p.Failed {
		var finalized bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM events WHERE message_id=? AND attempt_id=?)", terminalID, attemptID).Scan(&finalized); err != nil {
			return FinalizeReply{}, err
		}
		if finalized {
			return FinalizeReply{Outcome: string(state), Released: true}, nil
		}
		return FinalizeReply{}, p.InvalidTransition
	}
	if state != p.ResultPending || d.Released {
		return FinalizeReply{}, p.ReconciliationRequired
	}
	var generation, taskID, receiptAttempt, manifestID string
	var epoch int64
	var quarantined bool
	err = tx.QueryRowContext(ctx, "SELECT generation,task_id,attempt_id,epoch,manifest_id,quarantined FROM artifact_results WHERE receipt_id=?", completion.ReceiptID).Scan(&generation, &taskID, &receiptAttempt, &epoch, &manifestID, &quarantined)
	if errors.Is(err, sql.ErrNoRows) {
		return FinalizeReply{}, p.ReconciliationRequired
	}
	if err != nil {
		return FinalizeReply{}, err
	}
	if (p.Identity{Generation: generation, TaskID: taskID, AttemptID: receiptAttempt, Epoch: epoch}) != identity {
		return FinalizeReply{}, p.IdentityConflict
	}
	if quarantined {
		return FinalizeReply{}, p.ReconciliationRequired
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT body FROM artifact_manifests WHERE manifest_id=? AND state='committed'", manifestID).Scan(&raw); err != nil {
		return FinalizeReply{}, err
	}
	manifest, _, err := canonicalManifest(raw, p.Message{Identity: identity})
	if err != nil {
		return FinalizeReply{}, g.Deny("corrupt_record", "manifest")
	}
	if sess.RunnerBoot != d.Facts.RunnerBoot {
		return FinalizeReply{}, p.BootMismatch
	}
	now := time.Now().UnixMilli()
	if err := expireGrants(ctx, tx, now); err != nil {
		return FinalizeReply{}, err
	}
	if err := dispatchAllowed(ctx, tx, d.Request, now); err != nil {
		var refusal *g.Refusal
		if !errors.As(err, &refusal) {
			return FinalizeReply{}, err
		}
		// Elapsed grant expiry stays sticky, including under a refused finalization.
		if commitErr := tx.Commit(); commitErr != nil {
			return FinalizeReply{}, commitErr
		}
		return FinalizeReply{}, err
	}
	observed, err := exitObserved(ctx, tx, attemptID, completion.Exit)
	if err != nil {
		return FinalizeReply{}, err
	}
	if !observed || !completion.Boundary.settled() {
		return FinalizeReply{}, p.ReconciliationRequired
	}
	watermark, err := s.Streams().Watermark(attemptID)
	if err != nil {
		return FinalizeReply{}, err
	}
	if watermark.Through < completion.Stream.Through {
		return FinalizeReply{}, p.ReconciliationRequired
	}
	digest, err := s.Streams().Digest(attemptID, completion.Stream.Through)
	if err != nil || digest != completion.Stream.Digest {
		return FinalizeReply{}, p.ReconciliationRequired
	}
	to := p.Failed
	if manifest.Outcome == "succeeded" {
		to = p.Succeeded
	}
	m := p.Message{Version: p.FencedVersion, Kind: "transition", MessageID: terminalID, Identity: identity, ExpectedRevision: &revision, From: p.ResultPending, To: to}
	if r := p.CheckTransition(m, identity, state, revision); r != p.OK {
		return FinalizeReply{}, r
	}
	result, err := tx.ExecContext(ctx, "UPDATE attempts SET state=?,revision=revision+1 WHERE id=? AND revision=?", to, attemptID, revision)
	if err != nil {
		return FinalizeReply{}, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return FinalizeReply{}, p.RevisionConflict
	}
	if err := record(ctx, tx, m, revision+1); err != nil {
		return FinalizeReply{}, err
	}
	taskState := p.TaskReconciling
	if to == p.Succeeded {
		taskState = p.TaskAwaitingReview
	}
	if _, err := tx.ExecContext(ctx, "UPDATE tasks SET state=? WHERE id=?", taskState, identity.TaskID); err != nil {
		return FinalizeReply{}, err
	}
	body, err := runtimeBody(runtimeRecord{MessageID: completion.MessageID, Completion: &completion})
	if err != nil {
		return FinalizeReply{}, err
	}
	sum := sha256.Sum256(body)
	proof := Reconciliation{DispatchID: d.ID, Identity: identity, ExpectedRevision: revision, To: to, ConfirmedProcess: "terminated", RemoteWork: "quiescent", LaunchFenced: true, ArtifactsPreserved: true, EvidenceDigest: hex.EncodeToString(sum[:])}
	release, err := json.Marshal(proof)
	if err != nil {
		return FinalizeReply{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO dispatch_releases VALUES(?,?,?)", d.ID, string(release), who.ID); err != nil {
		return FinalizeReply{}, err
	}
	if to == p.Succeeded {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifact_result_heads VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(task_id) DO UPDATE SET generation=excluded.generation,attempt_id=excluded.attempt_id,epoch=excluded.epoch,manifest_id=excluded.manifest_id,receipt_id=excluded.receipt_id,finalized_ms=excluded.finalized_ms`, identity.TaskID, identity.Generation, attemptID, identity.Epoch, manifestID, completion.ReceiptID, now); err != nil {
			return FinalizeReply{}, err
		}
	}
	if _, err := recordRuntime(ctx, tx, attemptID, "completion", sess.RunnerBoot, s.meta.DaemonBoot, body, now); err != nil {
		return FinalizeReply{}, err
	}
	if err := s.step("before_finalize_commit"); err != nil {
		return FinalizeReply{}, err
	}
	if err := tx.Commit(); err != nil {
		return FinalizeReply{}, err
	}
	if err := s.step("after_finalize_commit"); err != nil {
		return FinalizeReply{}, err
	}
	return FinalizeReply{Outcome: string(to), Released: true}, nil
}
