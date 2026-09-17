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

// sameCompletion compares what a completion attests, ignoring its message id.
func sameCompletion(a, b Completion) bool {
	a.MessageID, b.MessageID = "", ""
	return a == b
}

// exitObserved returns the stream watermark of the retained exit observation
// matching the claimed exit, and whether one exists.
func exitObserved(ctx context.Context, tx *sql.Tx, attemptID string, exit ExitRecord) (int64, bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT body FROM runtime_observations WHERE attempt_id=? AND kind='exit'", attemptID)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		var v runtimeRecord
		if err := rows.Scan(&body); err != nil {
			return 0, false, err
		}
		if json.Unmarshal([]byte(body), &v) != nil || v.Evidence == nil {
			continue
		}
		e := v.Evidence
		if e.Kind == "exit" && e.PGIDEmpty && e.Code == exit.Code && e.PGID == exit.PGID && e.ObservedUnixNS == exit.ObservedUnixNS {
			return e.StreamThrough, true, nil
		}
	}
	return 0, false, rows.Err()
}

// retainedCompletion returns the completion a finalized attempt was closed with.
func retainedCompletion(ctx context.Context, tx *sql.Tx, attemptID string) (Completion, bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT body FROM runtime_observations WHERE attempt_id=? AND kind='completion'", attemptID)
	if err != nil {
		return Completion{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		var v runtimeRecord
		if err := rows.Scan(&body); err != nil {
			return Completion{}, false, err
		}
		if json.Unmarshal([]byte(body), &v) == nil && v.Completion != nil {
			return *v.Completion, true, nil
		}
	}
	return Completion{}, false, rows.Err()
}

// FinalizeAttempt verifies a non-quarantined current-generation receipt, a sink
// watermark equal to the claimed one and to the retained exit observation's,
// with an equal chain digest, boundary.Quiescent, no stop latch, a live grant
// and current boots and an unpaused daemon (dispatchAllowed refuses paused inside
// the transaction); incomplete evidence is reconciliation_required and a
// latched stop is stop_latched. Durable point: one committed transaction
// holding the CAS result_pending -> succeeded|failed from the manifest outcome,
// the event dispatchID(receipt_id, "terminal"), the dispatch_releases row
// (actor: runner principal), tasks.state awaiting_review|reconciling, the
// retained completion, the request receipt and, for succeeded, the
// artifact_result_heads upsert. The same message_id replays the same reply for
// the same completion and refuses a changed one; a different message_id for the
// same receipt replays only when it attests the retained completion.
func (s *Store) FinalizeAttempt(ctx context.Context, fingerprint, session, attemptID string, completion Completion) (FinalizeReply, error) {
	if completion.Version != execwire.Version || !p.ValidID(completion.MessageID) || !p.ValidID(completion.ReceiptID) || !p.ValidID(attemptID) || completion.Stream.Through < 0 || completion.Stream.Through > p.MaxInteger || !validHex(completion.Stream.Digest) {
		return FinalizeReply{}, p.Malformed
	}
	// The attempt's append lock is held across the whole finalization (taken
	// before the store lock, as AppendStream does), so no stream append can land
	// between the watermark check and the commit.
	unlock := s.sinks().Serialize(attemptID)
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
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
	requestSHA, err := requestDigest(completion)
	if err != nil {
		return FinalizeReply{}, err
	}
	if response, found, err := retainedReceipt(ctx, tx, attemptID, ReceiptFinalize, completion.MessageID, requestSHA); err != nil {
		return FinalizeReply{}, err
	} else if found {
		var reply FinalizeReply
		if json.Unmarshal(response, &reply) != nil {
			return FinalizeReply{}, g.Deny("corrupt_record", "receipt")
		}
		return reply, nil
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
		if !finalized {
			return FinalizeReply{}, p.InvalidTransition
		}
		// A new message id for the same receipt replays only the retained attestation.
		retained, found, err := retainedCompletion(ctx, tx, attemptID)
		if err != nil {
			return FinalizeReply{}, err
		}
		if !found || !sameCompletion(retained, completion) {
			return FinalizeReply{}, p.IdentityConflict
		}
		return FinalizeReply{Outcome: string(state), Released: true}, nil
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
	exitThrough, observed, err := exitObserved(ctx, tx, attemptID, completion.Exit)
	if err != nil {
		return FinalizeReply{}, err
	}
	if !observed || !completion.Boundary.settled() || completion.Stream.Through != exitThrough {
		return FinalizeReply{}, p.ReconciliationRequired
	}
	// The claim, the exit observation and the durable sink must agree exactly:
	// nothing streamed after the exit, nothing attested beyond the sink.
	watermark, err := s.sinks().Watermark(attemptID)
	if err != nil {
		return FinalizeReply{}, err
	}
	if watermark.Through != completion.Stream.Through || watermark.Digest != completion.Stream.Digest {
		return FinalizeReply{}, p.ReconciliationRequired
	}
	// A success needs the process to have exited 0: a manifest that claims
	// succeeded over a non-zero exit finalizes failed, never becomes the head,
	// and parks the task for review of the retained completion.
	to := p.Failed
	if manifest.Outcome == "succeeded" && completion.Exit.Code == 0 {
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
	reply := FinalizeReply{Outcome: string(to), Released: true}
	if err := recordReceipt(ctx, tx, attemptID, ReceiptFinalize, completion.MessageID, requestSHA, reply, sess.RunnerBoot, s.meta.DaemonBoot, now); err != nil {
		return FinalizeReply{}, err
	}
	if err := s.step("before_finalize_commit"); err != nil {
		return FinalizeReply{}, err
	}
	// Appends serialize on the attempt lock (AppendStream), and the sink is read
	// again immediately before commit so a record that reached the sink by any
	// other path still refuses the finalization.
	if again, err := s.sinks().Watermark(attemptID); err != nil {
		return FinalizeReply{}, err
	} else if again.Through != completion.Stream.Through || again.Digest != completion.Stream.Digest {
		return FinalizeReply{}, p.ReconciliationRequired
	}
	if err := tx.Commit(); err != nil {
		return FinalizeReply{}, err
	}
	if err := s.step("after_finalize_commit"); err != nil {
		return FinalizeReply{}, err
	}
	return reply, nil
}
