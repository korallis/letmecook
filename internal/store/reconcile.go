package store

// Reconcile store half (docs/decisions/0002 §8, issue #20). internal/reconcile
// classifies every non-terminal attempt from the single-snapshot read seam
// ReconciliationInputs and acts only through the evidence-typed writers in this
// file. Every writer re-derives its evidence inside the transaction it commits:
// a caller's classification is a request, never proof. Releases go through
// releaseDispatchTx with a Reconciliation whose confirmed_process, remote_work
// and evidence digest come from retained records (runtime_observations,
// control_observations, control_stops, control_leases, artifact_results); a
// timer, a runner's bare flag or an absent PID never releases anything. unknown
// remains a visible, blocking outcome until such evidence exists.
//
// This file depends on the persisted shapes of the execution-channel tables
// (runtime_observations bodies, artifact_results, upload manifests) and on the
// pre-S1 control/dispatch helpers, not on the internals of execution.go,
// uploads.go or finalize.go, which may change under review.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	i "github.com/korallis/letmecook/internal/identity"
	p "github.com/korallis/letmecook/schemas/execution"
)

const (
	maxReconcileReport      = 1048576
	maxReconcileRuntimeBody = 16384
	// latchClearedPrefix keys the immutable clearing record of one cancel_attempt
	// stop in reconcile_reports: id = latchClearedPrefix + stop_id. control.go's
	// admission check consults exactly this key; history is never deleted.
	latchClearedPrefix = "latch-cleared:"
	leaseClockActor    = "lease-clock"
	// Release bases ReleaseAttempt verifies. Each names the retained evidence the
	// store re-checks before it releases a reservation.
	BasisRefused             = "refused"
	BasisNotStarted          = "not_started"
	BasisObservation         = "observation"
	BasisRetainedTermination = "retained_termination"
)

// ReconcileReport mirrors one reconcile_reports row: one Startup, OnHello or
// Sweep run's classifications, keyed by the daemon boot that produced them.
type ReconcileReport struct {
	ID         string          `json:"id"`
	DaemonBoot string          `json:"daemon_boot"`
	CreatedMS  int64           `json:"created_ms"`
	Body       json.RawMessage `json:"body,omitempty"`
}

// RuntimeObservation mirrors one runtime_observations row: transition evidence
// retained with the boots it was reported under, so old-boot evidence stays
// visible history rather than current confirmation.
type RuntimeObservation struct {
	AttemptID      string          `json:"attempt_id"`
	EvidenceSHA256 string          `json:"evidence_sha256"`
	Kind           string          `json:"kind"`
	RunnerBoot     string          `json:"runner_boot"`
	DaemonBoot     string          `json:"daemon_boot"`
	Body           json.RawMessage `json:"body,omitempty"`
	RecordedMS     int64           `json:"recorded_ms"`
}

// ReconciliationInputs is everything reconcile classifies one attempt from, read
// in a single snapshot: the retained dispatch and whether it was acknowledged,
// the attempt's state and revision, the latest lease, how many leases were ever
// issued and whether the old-lease barrier has passed (true when no lease was
// ever issued: nothing could have launched), the cancel targets with the stop
// requests behind them, termination observations, runtime observations, the
// custody receipt (if any) and whether it is quarantined, the task's result
// head, the runner's last session, whether task admission is latched (honouring
// cleared cancel latches), the grant refusal code (empty when live), the paused
// flag and the store identity. Absent parts are omitted or zero values, never
// null; a present Dispatch always carries validated, non-nil envelope sets.
type ReconciliationInputs struct {
	Dispatch            Dispatch             `json:"dispatch,omitzero"`
	State               p.AttemptState       `json:"state"`
	Revision            int64                `json:"revision"`
	Acknowledged        bool                 `json:"acknowledged"`
	LastLease           c.Lease              `json:"last_lease,omitzero"`
	LeaseCount          int                  `json:"lease_count"`
	LeaseBarrierPassed  bool                 `json:"lease_barrier_passed"`
	StopTargets         []c.Target           `json:"stop_targets,omitempty"`
	StopRequests        []c.Receipt          `json:"stop_requests,omitempty"`
	Observations        []c.Evidence         `json:"observations,omitempty"`
	RuntimeObservations []RuntimeObservation `json:"runtime_observations,omitempty"`
	Receipt             p.Receipt            `json:"receipt,omitzero"`
	Quarantined         bool                 `json:"quarantined"`
	Head                ResultHead           `json:"head,omitzero"`
	LastSession         SessionRecord        `json:"last_session,omitzero"`
	Latched             bool                 `json:"latched"`
	GrantRefusal        string               `json:"grant_refusal,omitempty"`
	Paused              bool                 `json:"paused"`
	Generation          string               `json:"generation"`
	DaemonBoot          string               `json:"daemon_boot"`
}

// ReleaseBasis names the retained evidence a release rests on. Kind selects the
// verification: refused (an unacknowledged runner refusal), not_started (no
// lease was ever issued and delivery is closed by a stop, a dead grant, a daemon
// restart or a restarted runner), observation (a control_observations row for
// StopID) or retained_termination (a runtime_observations terminated row keyed by
// EvidenceSHA256). Cause is the report cause recorded with the release; Actor is
// the preferred release actor (a stop requester's fingerprint); when empty or not
// a current principal the dispatch's runner principal is recorded, and the owner
// only when that runner is revoked.
type ReleaseBasis struct {
	Kind           string `json:"kind"`
	StopID         string `json:"stop_id,omitempty"`
	EvidenceSHA256 string `json:"evidence_sha256,omitempty"`
	Cause          string `json:"cause,omitempty"`
	Actor          string `json:"actor,omitempty"`
}

// ReleaseOutcome is the committed release: its proof, the principal recorded as
// actor, the cancel latches cleared in the same transaction and whether the
// dispatch had already been released (replay).
type ReleaseOutcome struct {
	Proof    Reconciliation   `json:"proof"`
	Actor    string           `json:"actor"`
	Cause    string           `json:"cause,omitempty"`
	Cleared  []LatchClearance `json:"cleared,omitempty"`
	Replayed bool             `json:"replayed"`
}

// LatchClearance is the immutable record that a cancel_attempt stop no longer
// suppresses admission for its task: its attempt is terminal and its reservation
// released. The stop itself, its targets and observations are never deleted.
type LatchClearance struct {
	StopID     string         `json:"stop_id"`
	TaskID     string         `json:"task_id"`
	AttemptID  string         `json:"attempt_id"`
	Kind       c.Kind         `json:"kind"`
	Cause      string         `json:"cause"`
	Actor      string         `json:"actor"`
	Terminal   p.AttemptState `json:"terminal"`
	DaemonBoot string         `json:"daemon_boot"`
	ClearedMS  int64          `json:"cleared_ms"`
}

// RetryInputs is what reconcile.PlanRetry decides from: every attempt of the
// task, the last dispatch and its terminal cause, the head grant and its refusal
// code (empty when live), whether admission is latched or the daemon paused, how
// many dispatches the task has consumed and the ceilings they count against.
type RetryInputs struct {
	TaskID       string           `json:"task_id"`
	Attempts     []AttemptSummary `json:"attempts,omitempty"`
	Last         Dispatch         `json:"last,omitzero"`
	LastState    p.AttemptState   `json:"last_state,omitempty"`
	Cause        string           `json:"cause,omitempty"`
	Grant        g.Grant          `json:"grant,omitzero"`
	GrantRefusal string           `json:"grant_refusal,omitempty"`
	Latched      bool             `json:"latched"`
	Paused       bool             `json:"paused"`
	Dispatched   int64            `json:"dispatched"`
	FirstMS      int64            `json:"first_ms,omitempty"`
	Ceiling      g.Budgets        `json:"ceiling,omitzero"`
}

// retainedRuntime is the persisted runtime_observations body: the message the
// evidence arrived with plus exactly one evidence payload. It is decoded here
// from the stored JSON contract, not from execution.go's private type.
type retainedRuntime struct {
	MessageID   string             `json:"message_id"`
	Message     *p.Message         `json:"message,omitempty"`
	Evidence    *RuntimeEvidence   `json:"evidence,omitempty"`
	Termination *c.Evidence        `json:"termination,omitempty"`
	Boundary    *BoundaryState     `json:"boundary,omitempty"`
	Completion  *Completion        `json:"completion,omitempty"`
	Reconcile   *reconcileEvidence `json:"reconcile,omitempty"`
}

// reconcileEvidence is the daemon-ledger snapshot retained (kind "reconcile")
// when a release rests on the absence of a launch capability rather than on a
// supervisor report. Its digest is the release's evidence_digest.
type reconcileEvidence struct {
	Basis        string         `json:"basis"`
	DispatchID   string         `json:"dispatch_id"`
	Identity     p.Identity     `json:"identity"`
	State        p.AttemptState `json:"state"`
	Acknowledged bool           `json:"acknowledged"`
	Leases       int            `json:"leases"`
	StopID       string         `json:"stop_id,omitempty"`
	Cause        string         `json:"cause"`
	GrantRefusal string         `json:"grant_refusal,omitempty"`
	RunnerBoot   string         `json:"runner_boot,omitempty"`
	SessionBoot  string         `json:"session_boot,omitempty"`
	DaemonBoot   string         `json:"daemon_boot"`
	RecordedMS   int64          `json:"recorded_ms"`
}

// Boot reports the store's generation and this process's daemon boot.
func (s *Store) Boot() (generation, daemonBoot string) {
	return s.meta.Generation, s.meta.DaemonBoot
}

// SetControlClock replaces the wall clock behind control stamps, which the
// replacement barrier is measured on. Tests and diagnostics use it to advance
// the barrier without waiting; nil restores the real clock. Production never
// calls it: the barrier must elapse on the daemon's own clock.
func (s *Store) SetControlClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now == nil {
		now = time.Now
	}
	s.controlNow = now
}

// reconcileStep is the instance-local crash seam for this file's commits.
func (s *Store) reconcileStep(name string) error {
	if s.controlHook != nil {
		return s.controlHook(name)
	}
	return nil
}

func attemptDispatch(ctx context.Context, tx *sql.Tx, attemptID string) (Dispatch, error) {
	if !p.ValidID(attemptID) {
		return Dispatch{}, g.Deny("malformed", "attempt_id")
	}
	var id string
	if err := tx.QueryRowContext(ctx, "SELECT id FROM dispatches WHERE attempt_id=?", attemptID).Scan(&id); err != nil {
		return Dispatch{}, err
	}
	return loadDispatch(ctx, tx, id)
}

func attemptCursor(ctx context.Context, tx *sql.Tx, attemptID string) (p.AttemptState, int64, error) {
	var state p.AttemptState
	var revision int64
	err := tx.QueryRowContext(ctx, "SELECT state,revision FROM attempts WHERE id=?", attemptID).Scan(&state, &revision)
	return state, revision, err
}

func isTerminal(state p.AttemptState) bool {
	return state == p.Succeeded || state == p.Failed || state == p.Cancelled || state == p.Expired
}

func settledBoundary(b BoundaryState) bool {
	return b.Quiescent && b.InFlight == 0 && b.Reservations == b.TerminalReceipts && b.Reservations >= 0
}

func runtimeRows(ctx context.Context, tx *sql.Tx, attemptID string) ([]RuntimeObservation, error) {
	rows, err := tx.QueryContext(ctx, "SELECT attempt_id,evidence_sha256,kind,runner_boot,daemon_boot,body,recorded_ms FROM runtime_observations WHERE attempt_id=? ORDER BY recorded_ms,rowid", attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuntimeObservation
	for rows.Next() {
		var v RuntimeObservation
		var body string
		if err := rows.Scan(&v.AttemptID, &v.EvidenceSHA256, &v.Kind, &v.RunnerBoot, &v.DaemonBoot, &body, &v.RecordedMS); err != nil {
			return nil, err
		}
		if !json.Valid([]byte(body)) {
			return nil, g.Deny("corrupt_record", "runtime_observation")
		}
		v.Body = json.RawMessage(body)
		out = append(out, v)
	}
	return out, rows.Err()
}

func decodeRuntime(body []byte) (retainedRuntime, bool) {
	var v retainedRuntime
	if json.Unmarshal(body, &v) != nil {
		return retainedRuntime{}, false
	}
	return v, true
}

// retainRuntime keeps one evidence body keyed by its digest; identical bodies
// are no-ops. It is the same durable contract recordRuntime writes.
func retainRuntime(ctx context.Context, tx *sql.Tx, attemptID, kind, runnerBoot, daemonBoot string, body []byte, now int64) (string, error) {
	if len(body) > maxReconcileRuntimeBody {
		return "", p.Oversized
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	_, err := tx.ExecContext(ctx, "INSERT INTO runtime_observations VALUES(?,?,?,?,?,?,?) ON CONFLICT(attempt_id,evidence_sha256) DO NOTHING", attemptID, digest, kind, runnerBoot, daemonBoot, string(body), now)
	return digest, err
}

func pausedTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var paused bool
	err := tx.QueryRowContext(ctx, "SELECT paused FROM daemon_state WHERE singleton=1").Scan(&paused)
	return paused, err
}

func headRow(ctx context.Context, tx *sql.Tx, taskID string) (ResultHead, error) {
	v := ResultHead{TaskID: taskID}
	err := tx.QueryRowContext(ctx, "SELECT generation,attempt_id,epoch,manifest_id,receipt_id,finalized_ms FROM artifact_result_heads WHERE task_id=?", taskID).Scan(&v.Generation, &v.AttemptID, &v.Epoch, &v.ManifestID, &v.ReceiptID, &v.FinalizedMS)
	if errors.Is(err, sql.ErrNoRows) {
		return ResultHead{}, nil
	}
	return v, err
}

// attemptStopCauses lists the causes of the cancel_attempt stops targeting one
// attempt, in latch order.
func attemptStopCauses(ctx context.Context, tx *sql.Tx, attemptID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT body FROM control_stops WHERE kind='cancel_attempt' AND attempt_id=? ORDER BY rowid", attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var body string
		var receipt c.Receipt
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := decodeControl(body, &receipt); err != nil {
			return nil, err
		}
		out = append(out, receipt.Request.Cause)
	}
	return out, rows.Err()
}

func leaseNonces(ctx context.Context, tx *sql.Tx, attemptID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT nonce FROM control_leases WHERE attempt_id=? ORDER BY rowid", attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var nonce string
		if err := rows.Scan(&nonce); err != nil {
			return nil, err
		}
		out = append(out, nonce)
	}
	return out, rows.Err()
}

// taskLatchedTx is admission suppression as reconcile sees it: the sticky task
// stop, global stops, task pauses, authority supersession and cancel_attempt
// latches that have not been cleared. It mirrors control.go's controlSuppressed
// plus the clearing record this file writes.
func taskLatchedTx(ctx context.Context, tx *sql.Tx, taskID, grantID string) (bool, error) {
	var latched bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dispatch_stops WHERE task_id=?) OR EXISTS(SELECT 1 FROM control_stops s WHERE kind='global_stop' OR
 (kind='pause_task' AND task_id=?) OR
 (kind='cancel_attempt' AND task_id=? AND NOT EXISTS(SELECT 1 FROM reconcile_reports r WHERE r.id=?||s.id)) OR
 (kind='authority_supersession' AND (grant_id=? OR (task_id=? AND EXISTS(SELECT 1 FROM control_targets t WHERE t.stop_id=s.id)))))`, taskID, taskID, taskID, latchClearedPrefix, grantID, taskID).Scan(&latched)
	return latched, err
}

// grantRefusal is the live-authority check as a code: empty when the grant is
// live and current for the task, else the deny code (expired, superseded,
// revoked, unknown_grant, ...). It writes nothing.
func grantRefusal(ctx context.Context, tx *sql.Tx, request g.Request, now int64) (string, error) {
	err := checkExecution(ctx, tx, request, now)
	if err == nil {
		return "", nil
	}
	var refusal *g.Refusal
	if errors.As(err, &refusal) {
		return refusal.Code, nil
	}
	return "", err
}

func lastSessionFor(ctx context.Context, tx *sql.Tx, runnerID string) (SessionRecord, error) {
	v := SessionRecord{DriftMS: SessionDriftMS, TerminationMS: SessionTerminationMS, LeaseValidityMS: LeaseValidityMS, RenewEveryMS: LeaseRenewEveryMS}
	err := tx.QueryRowContext(ctx, "SELECT id,runner_id,runner_boot,daemon_boot,generation,mode FROM runner_sessions WHERE runner_id=? ORDER BY created_ms DESC,rowid DESC LIMIT 1", runnerID).Scan(&v.SessionID, &v.RunnerID, &v.RunnerBoot, &v.DaemonBoot, &v.Generation, &v.Mode)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, nil
	}
	return v, err
}

// ReconciliationInputs reads the classification inputs for one attempt in one
// read transaction. Durable point: none (read); reading never releases.
func (s *Store) ReconciliationInputs(ctx context.Context, attemptID string) (ReconciliationInputs, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return ReconciliationInputs{}, err
	}
	defer tx.Rollback()
	v := ReconciliationInputs{Generation: s.meta.Generation, DaemonBoot: s.meta.DaemonBoot}
	if v.State, v.Revision, err = attemptCursor(ctx, tx, attemptID); err != nil {
		return ReconciliationInputs{}, err
	}
	d, err := attemptDispatch(ctx, tx, attemptID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ReconciliationInputs{}, err
	}
	if v.Paused, err = pausedTx(ctx, tx); err != nil {
		return ReconciliationInputs{}, err
	}
	if v.RuntimeObservations, err = runtimeRows(ctx, tx, attemptID); err != nil {
		return ReconciliationInputs{}, err
	}
	if d.ID == "" {
		// A synthetic attempt without a dispatch (fixture assign) has no
		// reservation to release and no lease to wait for; the caller reports it
		// as unrecoverable.
		v.LeaseBarrierPassed = true
		return v, nil
	}
	v.Dispatch, v.Acknowledged = d, d.Acknowledged
	identity := d.Assignment.Identity
	lease, err := lastControlLease(ctx, tx, attemptID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ReconciliationInputs{}, err
	}
	if err == nil {
		v.LastLease = lease
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM control_leases WHERE attempt_id=?", attemptID).Scan(&v.LeaseCount); err != nil {
		return ReconciliationInputs{}, err
	}
	expired, err := controlExpiry(ctx, tx, attemptID, s.controlStamp())
	if err != nil {
		return ReconciliationInputs{}, err
	}
	v.LeaseBarrierPassed = v.LeaseCount == 0 || expired
	rows, err := tx.QueryContext(ctx, "SELECT body FROM control_targets WHERE attempt_id=? ORDER BY rowid", attemptID)
	if err != nil {
		return ReconciliationInputs{}, err
	}
	for rows.Next() {
		var body string
		var target c.Target
		if err := rows.Scan(&body); err != nil {
			rows.Close()
			return ReconciliationInputs{}, err
		}
		if err := decodeControl(body, &target); err != nil {
			rows.Close()
			return ReconciliationInputs{}, err
		}
		v.StopTargets = append(v.StopTargets, target)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return ReconciliationInputs{}, err
	}
	for _, target := range v.StopTargets {
		stop, err := loadStop(ctx, tx, target.Cancel.StopID)
		if err != nil {
			return ReconciliationInputs{}, err
		}
		v.StopRequests = append(v.StopRequests, stop)
	}
	rows, err = tx.QueryContext(ctx, "SELECT body FROM control_observations WHERE attempt_id=? ORDER BY rowid", attemptID)
	if err != nil {
		return ReconciliationInputs{}, err
	}
	for rows.Next() {
		var body string
		var evidence c.Evidence
		if err := rows.Scan(&body); err != nil {
			rows.Close()
			return ReconciliationInputs{}, err
		}
		if err := decodeControl(body, &evidence); err != nil {
			rows.Close()
			return ReconciliationInputs{}, err
		}
		v.Observations = append(v.Observations, evidence)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return ReconciliationInputs{}, err
	}
	var manifest p.Manifest
	err = tx.QueryRowContext(ctx, "SELECT manifest_id,manifest_sha256,manifest_bytes,receipt_id,quarantined FROM artifact_results WHERE generation=? AND task_id=? AND attempt_id=? AND epoch=?", identity.Generation, identity.TaskID, identity.AttemptID, identity.Epoch).Scan(&manifest.ManifestID, &manifest.SHA256, &manifest.Bytes, &v.Receipt.ReceiptID, &v.Quarantined)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ReconciliationInputs{}, err
	}
	if err == nil {
		v.Receipt.Identity, v.Receipt.Manifest, v.Receipt.Artifacts, v.Receipt.Metadata = identity, manifest, "verified_durable", "manifest_and_result_committed"
	}
	if v.Head, err = headRow(ctx, tx, identity.TaskID); err != nil {
		return ReconciliationInputs{}, err
	}
	if v.LastSession, err = lastSessionFor(ctx, tx, d.Facts.Repository.RunnerRoot.RunnerID); err != nil {
		return ReconciliationInputs{}, err
	}
	if v.Latched, err = taskLatchedTx(ctx, tx, identity.TaskID, d.Request.GrantID); err != nil {
		return ReconciliationInputs{}, err
	}
	if v.GrantRefusal, err = grantRefusal(ctx, tx, d.Request, s.controlNow().UnixMilli()); err != nil {
		return ReconciliationInputs{}, err
	}
	return v, nil
}

// NonTerminalAttempts lists every attempt outside succeeded|failed|cancelled|
// expired with its dispatch binding, using the attempts_active index.
// Durable point: none (read).
func (s *Store) NonTerminalAttempts(ctx context.Context) ([]AttemptSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.task_id,a.epoch,a.state,a.revision,COALESCE(d.id,''),
 EXISTS(SELECT 1 FROM dispatch_acks k WHERE k.dispatch_id=d.id),EXISTS(SELECT 1 FROM dispatch_releases r WHERE r.dispatch_id=d.id)
 FROM attempts a LEFT JOIN dispatches d ON d.attempt_id=a.id
 WHERE a.state NOT IN ('succeeded','failed','cancelled','expired') ORDER BY a.task_id,a.epoch`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AttemptSummary{}
	for rows.Next() {
		v := AttemptSummary{Identity: p.Identity{Generation: s.meta.Generation}}
		if err := rows.Scan(&v.Identity.AttemptID, &v.Identity.TaskID, &v.Identity.Epoch, &v.State, &v.Revision, &v.DispatchID, &v.Acknowledged, &v.Released); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// fenceTx moves a non-terminal attempt outside stopping|unknown to stopping so a
// release edge exists, recording the event and parking the task. key makes the
// event's message id stable per (key, revision).
func fenceTx(ctx context.Context, tx *sql.Tx, identity p.Identity, state p.AttemptState, revision int64, key string) (p.Message, int64, error) {
	m := p.Message{Version: p.FencedVersion, Kind: "transition", MessageID: dispatchID(key+":"+strconv.FormatInt(revision, 10), "fence"), Identity: identity, ExpectedRevision: &revision, From: state, To: p.Stopping}
	if r := p.CheckTransition(m, identity, state, revision); r != p.OK {
		return p.Message{}, revision, r
	}
	result, err := tx.ExecContext(ctx, "UPDATE attempts SET state='stopping',revision=revision+1 WHERE id=? AND revision=?", identity.AttemptID, revision)
	if err != nil {
		return p.Message{}, revision, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return p.Message{}, revision, p.RevisionConflict
	}
	if err := record(ctx, tx, m, revision+1); err != nil {
		return p.Message{}, revision, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE tasks SET state='reconciling' WHERE id=?", identity.TaskID); err != nil {
		return p.Message{}, revision, err
	}
	return m, revision + 1, nil
}

func currentEvent(ctx context.Context, tx *sql.Tx, attemptID string, revision int64) (p.Message, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT message FROM events WHERE attempt_id=? AND revision=?", attemptID, revision).Scan(&raw); err != nil {
		return p.Message{}, err
	}
	return p.Decode([]byte(raw))
}

// FenceAttempt moves an assigned|starting|running|result_pending|unknown attempt to
// stopping and latches its cancel with cause: lease_expired uses the lease-clock
// stop id dispatchID(last nonce, "expired") that issueLeaseTx also uses, so a
// lapse latched by a late renewal and a lapse latched by reconcile are one stop;
// operator uses dispatchID(attempt, "reconcile-fence"). Durable point: the latch,
// the attempts CAS and its event committed together; the transition that made
// the attempt stopping is returned (the retained one on replay). It never
// releases the reservation.
func (s *Store) FenceAttempt(ctx context.Context, attemptID, cause string) (p.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cause != "lease_expired" && cause != "operator" {
		return p.Message{}, g.Deny("malformed", "cause")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return p.Message{}, err
	}
	defer tx.Rollback()
	d, err := attemptDispatch(ctx, tx, attemptID)
	if err != nil {
		return p.Message{}, err
	}
	identity := d.Assignment.Identity
	state, revision, err := attemptCursor(ctx, tx, attemptID)
	if err != nil {
		return p.Message{}, err
	}
	if isTerminal(state) || d.Released {
		return p.Message{}, p.InvalidTransition
	}
	stopID, actor := dispatchID(attemptID, "reconcile-fence"), "reconcile"
	if cause == "lease_expired" {
		lease, err := lastControlLease(ctx, tx, attemptID)
		if errors.Is(err, sql.ErrNoRows) {
			return p.Message{}, g.Deny("reconciliation_required", "lease")
		}
		if err != nil {
			return p.Message{}, err
		}
		stopID, actor = dispatchID(lease.Request.Nonce, "expired"), leaseClockActor
	}
	if _, err := latchStop(ctx, tx, actor, c.Request{ID: stopID, Kind: c.CancelAttempt, TaskID: identity.TaskID, AttemptID: attemptID, Cause: cause}, s.controlStamp()); err != nil {
		return p.Message{}, err
	}
	m := p.Message{}
	if state == p.Stopping {
		if m, err = currentEvent(ctx, tx, attemptID, revision); err != nil {
			return p.Message{}, err
		}
	} else if m, _, err = fenceTx(ctx, tx, identity, state, revision, stopID); err != nil {
		return p.Message{}, err
	}
	if err := s.reconcileStep("before_fence_commit"); err != nil {
		return p.Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return p.Message{}, err
	}
	return m, s.reconcileStep("after_fence_commit")
}

// releaseActor resolves the principal recorded on a release: the preferred
// fingerprint when it is a current principal, else the dispatch's runner
// principal, else (runner revoked) the owner. releaseDispatchTx records the
// principal id behind the fingerprint.
func releaseActor(ctx context.Context, tx *sql.Tx, d Dispatch, preferred string) (string, error) {
	if preferred != "" {
		if _, err := principal(ctx, tx, preferred); err == nil {
			return preferred, nil
		}
	}
	var fingerprint string
	err := tx.QueryRowContext(ctx, "SELECT c.fingerprint FROM credentials c JOIN principals p ON p.id=c.principal_id WHERE c.principal_id=? AND c.revoked=0 AND p.revoked=0 ORDER BY c.fingerprint LIMIT 1", d.Facts.Repository.RunnerRoot.RunnerID).Scan(&fingerprint)
	if err == nil {
		return fingerprint, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	err = tx.QueryRowContext(ctx, "SELECT c.fingerprint FROM credentials c JOIN principals p ON p.id=c.principal_id WHERE p.role='owner' AND c.revoked=0 AND p.revoked=0 ORDER BY c.fingerprint LIMIT 1").Scan(&fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", i.Denied
	}
	return fingerprint, err
}

// terminationCheck verifies one terminated report against the dispatch and the
// retained control records: exact session, a latched target or the attempt's own
// lease-expiry stop id, a confirmed process outcome, quiescent remote work, a
// settled boundary when one was retained and, for evidence from another daemon
// boot, an elapsed replacement barrier. It returns the terminal state the stop
// cause selects and the stop's cause (the report cause: operator cancels never
// become auto-retryable because a daemon happened to restart). A supervisor's
// autonomous lease-expiry stop that the daemon never latched is not latched
// here: the retained report and the release proof are its durable record, and
// no cancel latch is left to suppress the task's retry.
func (s *Store) terminationCheck(ctx context.Context, tx *sql.Tx, d Dispatch, m p.Message, boundary *BoundaryState, measurement c.Measurement) (to p.AttemptState, cause string, err error) {
	identity := d.Assignment.Identity
	if m.Kind != "terminated" || !p.ValidID(m.StopID) || measurement.Validate(true) != nil {
		return "", "", p.Malformed
	}
	if r := p.CheckSession(m, identity, p.FencedVersion); r != p.OK {
		return "", "", r
	}
	if m.RunnerBoot != d.Facts.RunnerBoot {
		return "", "", p.BootMismatch
	}
	if m.ConfirmedProcess != "terminated" && m.ConfirmedProcess != "not_started" {
		return "", "", g.Deny("reconciliation_required", "confirmed_process")
	}
	if m.RemoteWork != "quiescent" || (boundary != nil && !settledBoundary(*boundary)) {
		return "", "", g.Deny("reconciliation_required", "remote_work")
	}
	var latched bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM control_targets WHERE stop_id=? AND attempt_id=?)", m.StopID, identity.AttemptID).Scan(&latched); err != nil {
		return "", "", err
	}
	nonces, err := leaseNonces(ctx, tx, identity.AttemptID)
	if err != nil {
		return "", "", err
	}
	expiry := false
	for _, nonce := range nonces {
		if execwire.ExpiryStopID(nonce) == m.StopID {
			expiry = true
		}
	}
	to, cause = p.Cancelled, "operator"
	if latched {
		stop, err := loadStop(ctx, tx, m.StopID)
		if err != nil {
			return "", "", err
		}
		if stop.Request.Kind == c.CancelAttempt && stop.Request.AttemptID != identity.AttemptID {
			return "", "", p.IdentityConflict
		}
		cause = stop.Request.Cause
	} else if expiry {
		cause = "lease_expired"
	} else {
		return "", "", g.Deny("reconciliation_required", "stop_id")
	}
	if cause == "lease_expired" {
		to = p.Expired
	}
	if m.DaemonBoot != s.meta.DaemonBoot {
		expired, err := controlExpiry(ctx, tx, identity.AttemptID, s.controlStamp())
		if err != nil {
			return "", "", err
		}
		if len(nonces) > 0 && !expired {
			return "", "", g.Deny("reconciliation_required", "lease_barrier")
		}
	}
	return to, cause, nil
}

// clearLatchesTx records clearances for cancel_attempt stops whose attempt is
// terminal and whose dispatch is released, scoped to one attempt (attemptID) or
// one task (taskID) or everything. Task pauses, global stops and authority
// supersession are never cleared here.
func clearLatchesTx(ctx context.Context, tx *sql.Tx, taskID, attemptID, daemonBoot string, now int64) ([]LatchClearance, error) {
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.task_id,s.attempt_id,s.body,a.state FROM control_stops s JOIN attempts a ON a.id=s.attempt_id JOIN dispatches d ON d.attempt_id=a.id
 WHERE s.kind='cancel_attempt' AND (?='' OR s.task_id=?) AND (?='' OR s.attempt_id=?)
 AND a.state IN ('succeeded','failed','cancelled','expired') AND EXISTS(SELECT 1 FROM dispatch_releases r WHERE r.dispatch_id=d.id)
 AND NOT EXISTS(SELECT 1 FROM reconcile_reports r WHERE r.id=?||s.id) ORDER BY s.rowid`, taskID, taskID, attemptID, attemptID, latchClearedPrefix)
	if err != nil {
		return nil, err
	}
	var out []LatchClearance
	for rows.Next() {
		var body string
		var receipt c.Receipt
		v := LatchClearance{Kind: c.CancelAttempt, DaemonBoot: daemonBoot, ClearedMS: now}
		if err := rows.Scan(&v.StopID, &v.TaskID, &v.AttemptID, &body, &v.Terminal); err != nil {
			rows.Close()
			return nil, err
		}
		if err := decodeControl(body, &receipt); err != nil {
			rows.Close()
			return nil, err
		}
		v.Cause, v.Actor = receipt.Request.Cause, receipt.Actor
		out = append(out, v)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for _, v := range out {
		body, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO reconcile_reports VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING", latchClearedPrefix+v.StopID, daemonBoot, now, string(body)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ReleaseAttempt releases one attempt's reservation on retained evidence that the
// store re-verifies inside the transaction (see ReleaseBasis), moving a
// non-stopping attempt to stopping first when the protocol edge needs it, then
// through releaseDispatchTx to cancelled|expired. The release, its events, the
// evidence record and the clearing of this attempt's cancel latches commit
// together. A released dispatch replays its retained proof. Durable point: that
// commit. It never releases on a timer, a bare flag or remote_work unknown.
func (s *Store) ReleaseAttempt(ctx context.Context, attemptID string, basis ReleaseBasis) (ReleaseOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return ReleaseOutcome{}, err
	}
	defer tx.Rollback()
	d, err := attemptDispatch(ctx, tx, attemptID)
	if err != nil {
		return ReleaseOutcome{}, err
	}
	identity := d.Assignment.Identity
	state, revision, err := attemptCursor(ctx, tx, attemptID)
	if err != nil {
		return ReleaseOutcome{}, err
	}
	if d.Released {
		var body, actor string
		if err := tx.QueryRowContext(ctx, "SELECT body,actor FROM dispatch_releases WHERE dispatch_id=?", d.ID).Scan(&body, &actor); err != nil {
			return ReleaseOutcome{}, err
		}
		var proof Reconciliation
		if err := json.Unmarshal([]byte(body), &proof); err != nil {
			return ReleaseOutcome{}, g.Deny("corrupt_record", "release")
		}
		return ReleaseOutcome{Proof: proof, Actor: actor, Replayed: true}, nil
	}
	if isTerminal(state) {
		return ReleaseOutcome{}, p.InvalidTransition
	}
	if identity.Generation != s.meta.Generation {
		return ReleaseOutcome{}, p.StaleGeneration
	}
	now := s.controlStamp()
	rows, err := runtimeRows(ctx, tx, attemptID)
	if err != nil {
		return ReleaseOutcome{}, err
	}
	var leases int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM control_leases WHERE attempt_id=?", attemptID).Scan(&leases); err != nil {
		return ReleaseOutcome{}, err
	}
	to, confirmed, digest, cause, preferred := p.Cancelled, "not_started", "", basis.Cause, basis.Actor
	var stopID string
	switch basis.Kind {
	case BasisRefused:
		if d.Acknowledged {
			return ReleaseOutcome{}, g.Deny("reconciliation_required", "acknowledged")
		}
		for _, row := range rows {
			if v, ok := decodeRuntime(row.Body); row.Kind == "refused" && ok && v.Message != nil && v.Message.Kind == "refuse" && v.Message.Identity == identity {
				digest = row.EvidenceSHA256
			}
		}
		if digest == "" {
			return ReleaseOutcome{}, g.Deny("reconciliation_required", "refusal")
		}
		if cause == "" {
			cause = "refused"
		}
	case BasisNotStarted:
		if leases != 0 {
			return ReleaseOutcome{}, g.Deny("reconciliation_required", "lease_issued")
		}
		for _, row := range rows {
			switch row.Kind {
			case "launch_intent", "launched", "exit", "terminated", "completion":
				return ReleaseOutcome{}, g.Deny("reconciliation_required", "launch_evidence")
			}
		}
		var target bool
		if basis.StopID != "" {
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM control_targets WHERE stop_id=? AND attempt_id=?)", basis.StopID, attemptID).Scan(&target); err != nil {
				return ReleaseOutcome{}, err
			}
		}
		latched, err := taskLatchedTx(ctx, tx, identity.TaskID, d.Request.GrantID)
		if err != nil {
			return ReleaseOutcome{}, err
		}
		refusal, err := grantRefusal(ctx, tx, d.Request, now.Wall.UnixMilli())
		if err != nil {
			return ReleaseOutcome{}, err
		}
		session, err := lastSessionFor(ctx, tx, d.Facts.Repository.RunnerRoot.RunnerID)
		if err != nil {
			return ReleaseOutcome{}, err
		}
		// A restarted supervisor can never lease this attempt (its boot no longer
		// matches the eligibility boot), so with no lease ever issued the launch
		// capability is provably absent.
		restarted := session.SessionID != "" && session.RunnerBoot != d.Facts.RunnerBoot
		if !(state == p.Unknown || restarted || !d.Acknowledged && (target || latched || refusal != "")) {
			return ReleaseOutcome{}, g.Deny("reconciliation_required", "outbox")
		}
		if target {
			stop, err := loadStop(ctx, tx, basis.StopID)
			if err != nil {
				return ReleaseOutcome{}, err
			}
			stopID = basis.StopID
			if preferred == "" {
				preferred = stop.Actor
			}
			if cause == "" {
				cause = stop.Request.Cause
			}
		}
		switch {
		case cause != "":
		case refusal != "":
			cause = refusal
		case latched:
			cause = "operator"
		case restarted:
			cause = "runner_restarted"
		default:
			cause = "daemon_restart"
		}
		if refusal == "expired" {
			to = p.Expired
		}
		evidence := reconcileEvidence{Basis: BasisNotStarted, DispatchID: d.ID, Identity: identity, State: state, Acknowledged: d.Acknowledged, Leases: leases, StopID: stopID, Cause: cause, GrantRefusal: refusal, RunnerBoot: d.Facts.RunnerBoot, SessionBoot: session.RunnerBoot, DaemonBoot: s.meta.DaemonBoot, RecordedMS: now.Wall.UnixMilli()}
		body, err := json.Marshal(retainedRuntime{MessageID: dispatchID(d.ID, "reconcile-not-started"), Reconcile: &evidence})
		if err != nil {
			return ReleaseOutcome{}, err
		}
		if digest, err = retainRuntime(ctx, tx, attemptID, "reconcile", d.Facts.RunnerBoot, s.meta.DaemonBoot, body, now.Wall.UnixMilli()); err != nil {
			return ReleaseOutcome{}, err
		}
	case BasisObservation:
		var body string
		if err := tx.QueryRowContext(ctx, "SELECT body FROM control_observations WHERE stop_id=? AND attempt_id=?", basis.StopID, attemptID).Scan(&body); err != nil {
			return ReleaseOutcome{}, err
		}
		var evidence c.Evidence
		if err := decodeControl(body, &evidence); err != nil {
			return ReleaseOutcome{}, err
		}
		if err := tx.QueryRowContext(ctx, "SELECT body FROM control_targets WHERE stop_id=? AND attempt_id=?", basis.StopID, attemptID).Scan(&body); err != nil {
			return ReleaseOutcome{}, err
		}
		var target c.Target
		if err := decodeControl(body, &target); err != nil {
			return ReleaseOutcome{}, err
		}
		if !validControlEvidence(evidence, target) || target.DispatchID != d.ID {
			return ReleaseOutcome{}, g.Deny("corrupt_record", "observation")
		}
		var boundary *BoundaryState
		for _, row := range rows {
			if v, ok := decodeRuntime(row.Body); row.Kind == "terminated" && ok && v.Termination != nil && v.Termination.Terminated.MessageID == evidence.Terminated.MessageID {
				boundary = v.Boundary
			}
		}
		var stopCause string
		if to, stopCause, err = s.terminationCheck(ctx, tx, d, evidence.Terminated, boundary, evidence.Measurement); err != nil {
			return ReleaseOutcome{}, err
		}
		confirmed, digest, stopID = evidence.Terminated.ConfirmedProcess, evidence.Terminated.EvidenceDigest, basis.StopID
		if cause == "" {
			cause = stopCause
		}
	case BasisRetainedTermination:
		var found *retainedRuntime
		for _, row := range rows {
			if v, ok := decodeRuntime(row.Body); row.Kind == "terminated" && ok && row.EvidenceSHA256 == basis.EvidenceSHA256 && v.Termination != nil {
				found = &v
			}
		}
		if found == nil {
			return ReleaseOutcome{}, g.Deny("reconciliation_required", "termination")
		}
		m := found.Termination.Terminated
		if found.Boundary == nil {
			return ReleaseOutcome{}, g.Deny("reconciliation_required", "boundary")
		}
		var stopCause string
		if to, stopCause, err = s.terminationCheck(ctx, tx, d, m, found.Boundary, found.Termination.Measurement); err != nil {
			return ReleaseOutcome{}, err
		}
		confirmed, digest, stopID = m.ConfirmedProcess, m.EvidenceDigest, m.StopID
		if cause == "" {
			cause = stopCause
		}
	default:
		return ReleaseOutcome{}, g.Deny("malformed", "basis")
	}
	if state != p.Stopping && state != p.Unknown {
		key := stopID
		if key == "" {
			key = d.ID
		}
		if _, revision, err = fenceTx(ctx, tx, identity, state, revision, key); err != nil {
			return ReleaseOutcome{}, err
		}
	}
	actor, err := releaseActor(ctx, tx, d, preferred)
	if err != nil {
		return ReleaseOutcome{}, err
	}
	proof := Reconciliation{DispatchID: d.ID, Identity: identity, ExpectedRevision: revision, To: to, ConfirmedProcess: confirmed, RemoteWork: "quiescent", LaunchFenced: true, ArtifactsPreserved: true, EvidenceDigest: digest}
	if err := s.releaseDispatchTx(ctx, tx, actor, proof); err != nil {
		return ReleaseOutcome{}, err
	}
	who, err := principal(ctx, tx, actor)
	if err != nil {
		return ReleaseOutcome{}, err
	}
	cleared, err := clearLatchesTx(ctx, tx, "", attemptID, s.meta.DaemonBoot, now.Wall.UnixMilli())
	if err != nil {
		return ReleaseOutcome{}, err
	}
	if err := s.reconcileStep("before_release_commit"); err != nil {
		return ReleaseOutcome{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReleaseOutcome{}, err
	}
	return ReleaseOutcome{Proof: proof, Actor: who.ID, Cause: cause, Cleared: cleared}, s.reconcileStep("after_release_commit")
}

// RecoverResultPending moves an unknown attempt back to result_pending so the
// runner can upload or finalize the result it preserved: the supervised process
// is proven exited by a retained exit observation, or the runner's hello journal
// for this dispatch (exact identity and boot, not corrupt) reports result_pending
// or a receipt. Refused while a stop targets the attempt, admission is latched,
// the grant is dead or the daemon is paused. Durable point: the CAS, its event
// and the retained recovery record in one commit; replay returns the recorded
// transition. It never restarts the attempt.
func (s *Store) RecoverResultPending(ctx context.Context, attemptID string, journal execwire.Journal) (p.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return p.Message{}, err
	}
	defer tx.Rollback()
	d, err := attemptDispatch(ctx, tx, attemptID)
	if err != nil {
		return p.Message{}, err
	}
	identity := d.Assignment.Identity
	state, revision, err := attemptCursor(ctx, tx, attemptID)
	if err != nil {
		return p.Message{}, err
	}
	m := p.Message{Version: p.FencedVersion, Kind: "transition", MessageID: dispatchID(d.ID+":"+strconv.FormatInt(revision, 10), "recovered-result-pending"), Identity: identity, ExpectedRevision: &revision, From: p.Unknown, To: p.ResultPending}
	if state == p.ResultPending {
		if old, err := currentEvent(ctx, tx, attemptID, revision); err == nil && old.To == p.ResultPending && old.From == p.Unknown {
			return old, nil
		}
		return p.Message{}, p.InvalidTransition
	}
	if state != p.Unknown || d.Released {
		return p.Message{}, p.InvalidTransition
	}
	if identity.Generation != s.meta.Generation {
		return p.Message{}, p.StaleGeneration
	}
	paused, err := pausedTx(ctx, tx)
	if err != nil {
		return p.Message{}, err
	}
	if paused {
		return p.Message{}, g.Deny("paused", "recover")
	}
	var targeted bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM control_targets WHERE attempt_id=?)", attemptID).Scan(&targeted); err != nil {
		return p.Message{}, err
	}
	latched, err := taskLatchedTx(ctx, tx, identity.TaskID, d.Request.GrantID)
	if err != nil {
		return p.Message{}, err
	}
	now := s.controlNow().UnixMilli()
	refusal, err := grantRefusal(ctx, tx, d.Request, now)
	if err != nil {
		return p.Message{}, err
	}
	if targeted || latched || refusal != "" {
		return p.Message{}, g.Deny("stop_latched", "recover")
	}
	rows, err := runtimeRows(ctx, tx, attemptID)
	if err != nil {
		return p.Message{}, err
	}
	exited := false
	for _, row := range rows {
		if v, ok := decodeRuntime(row.Body); row.Kind == "exit" && ok && v.Evidence != nil && v.Evidence.Kind == "exit" && v.Evidence.PGIDEmpty {
			exited = true
		}
	}
	journaled := journal.DispatchID == d.ID && journal.Identity == identity && !journal.Corrupt && journal.RunnerBoot == d.Facts.RunnerBoot && (journal.State == p.ResultPending || journal.ReceiptID != "")
	if !exited && !journaled {
		return p.Message{}, g.Deny("reconciliation_required", "exit")
	}
	if r := p.CheckTransition(m, identity, state, revision); r != p.OK {
		return p.Message{}, r
	}
	result, err := tx.ExecContext(ctx, "UPDATE attempts SET state='result_pending',revision=revision+1 WHERE id=? AND revision=?", attemptID, revision)
	if err != nil {
		return p.Message{}, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return p.Message{}, p.RevisionConflict
	}
	if err := record(ctx, tx, m, revision+1); err != nil {
		return p.Message{}, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE tasks SET state='verifying' WHERE id=?", identity.TaskID); err != nil {
		return p.Message{}, err
	}
	basis := "exit_observation"
	if !exited {
		basis = "runner_journal"
	}
	evidence := reconcileEvidence{Basis: basis, DispatchID: d.ID, Identity: identity, State: state, Acknowledged: d.Acknowledged, Leases: 0, Cause: "recovered_result_pending", RunnerBoot: d.Facts.RunnerBoot, SessionBoot: journal.RunnerBoot, DaemonBoot: s.meta.DaemonBoot, RecordedMS: now}
	body, err := json.Marshal(retainedRuntime{MessageID: m.MessageID, Reconcile: &evidence})
	if err != nil {
		return p.Message{}, err
	}
	if _, err := retainRuntime(ctx, tx, attemptID, "recovered", d.Facts.RunnerBoot, s.meta.DaemonBoot, body, now); err != nil {
		return p.Message{}, err
	}
	if err := s.reconcileStep("before_recover_commit"); err != nil {
		return p.Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return p.Message{}, err
	}
	return m, s.reconcileStep("after_recover_commit")
}

// CompleteFinalization finalizes an attempt whose custody committed but whose
// finalize never did (daemon or runner crash in between) from stored evidence
// alone: a non-quarantined current-generation receipt and committed manifest, a
// retained exit observation whose stream_through equals the sink watermark with
// an equal chain digest, boundary receipts in attempt_usage that are all
// terminal, no stop latch, a live grant and an unpaused daemon. From unknown it
// first records unknown -> result_pending (intact validated artifacts), then the
// same terminal CAS, event id dispatchID(receipt, "terminal"), task state,
// dispatch_releases row (actor: runner principal) and result head that
// FinalizeAttempt writes, so a runner's replayed finalize returns the same
// reply. Durable point: that one commit. Nothing reruns.
func (s *Store) CompleteFinalization(ctx context.Context, attemptID string) (FinalizeReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return FinalizeReply{}, err
	}
	defer tx.Rollback()
	d, err := attemptDispatch(ctx, tx, attemptID)
	if err != nil {
		return FinalizeReply{}, err
	}
	identity := d.Assignment.Identity
	if identity.Generation != s.meta.Generation {
		return FinalizeReply{}, p.StaleGeneration
	}
	var receiptID, manifestID string
	var quarantined bool
	err = tx.QueryRowContext(ctx, "SELECT receipt_id,manifest_id,quarantined FROM artifact_results WHERE generation=? AND task_id=? AND attempt_id=? AND epoch=?", identity.Generation, identity.TaskID, identity.AttemptID, identity.Epoch).Scan(&receiptID, &manifestID, &quarantined)
	if errors.Is(err, sql.ErrNoRows) {
		return FinalizeReply{}, g.Deny("reconciliation_required", "receipt")
	}
	if err != nil {
		return FinalizeReply{}, err
	}
	state, revision, err := attemptCursor(ctx, tx, attemptID)
	if err != nil {
		return FinalizeReply{}, err
	}
	terminalID := dispatchID(receiptID, "terminal")
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
	if (state != p.Unknown && state != p.ResultPending) || d.Released {
		return FinalizeReply{}, p.ReconciliationRequired
	}
	if quarantined {
		return FinalizeReply{}, g.Deny("reconciliation_required", "quarantined")
	}
	paused, err := pausedTx(ctx, tx)
	if err != nil {
		return FinalizeReply{}, err
	}
	if paused {
		return FinalizeReply{}, g.Deny("paused", "finalize")
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT body FROM artifact_manifests WHERE manifest_id=? AND state='committed'", manifestID).Scan(&raw); err != nil {
		return FinalizeReply{}, err
	}
	manifest, _, err := canonicalManifest(raw, p.Message{Identity: identity})
	if err != nil {
		return FinalizeReply{}, g.Deny("corrupt_record", "manifest")
	}
	now := s.controlNow().UnixMilli()
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
	rows, err := runtimeRows(ctx, tx, attemptID)
	if err != nil {
		return FinalizeReply{}, err
	}
	var exit *RuntimeEvidence
	for _, row := range rows {
		if v, ok := decodeRuntime(row.Body); row.Kind == "exit" && ok && v.Evidence != nil && v.Evidence.Kind == "exit" && v.Evidence.PGIDEmpty && v.Evidence.PGID > 0 && v.Evidence.ObservedUnixNS > 0 {
			exit = v.Evidence
		}
	}
	if exit == nil {
		return FinalizeReply{}, g.Deny("reconciliation_required", "exit")
	}
	watermark, err := s.Streams().Watermark(attemptID)
	if err != nil {
		return FinalizeReply{}, err
	}
	if watermark.Through != exit.StreamThrough {
		return FinalizeReply{}, g.Deny("reconciliation_required", "stream")
	}
	digest, err := s.Streams().Digest(attemptID, exit.StreamThrough)
	if err != nil || digest != watermark.Digest {
		return FinalizeReply{}, g.Deny("reconciliation_required", "stream_digest")
	}
	usage, err := tx.QueryContext(ctx, "SELECT body FROM attempt_usage WHERE attempt_id=?", attemptID)
	if err != nil {
		return FinalizeReply{}, err
	}
	boundary := BoundaryState{}
	for usage.Next() {
		var body string
		var r UsageReceipt
		if err := usage.Scan(&body); err != nil {
			usage.Close()
			return FinalizeReply{}, err
		}
		if json.Unmarshal([]byte(body), &r) != nil {
			usage.Close()
			return FinalizeReply{}, g.Deny("corrupt_record", "usage")
		}
		boundary.Reservations++
		if r.Terminal {
			boundary.TerminalReceipts++
		}
	}
	if err := errors.Join(usage.Err(), usage.Close()); err != nil {
		return FinalizeReply{}, err
	}
	boundary.InFlight = boundary.Reservations - boundary.TerminalReceipts
	boundary.Quiescent = boundary.InFlight == 0
	if !settledBoundary(boundary) {
		return FinalizeReply{}, g.Deny("reconciliation_required", "boundary")
	}
	if state == p.Unknown {
		recovered := p.Message{Version: p.FencedVersion, Kind: "transition", MessageID: dispatchID(receiptID, "recovered"), Identity: identity, ExpectedRevision: &revision, From: p.Unknown, To: p.ResultPending}
		if r := p.CheckTransition(recovered, identity, state, revision); r != p.OK {
			return FinalizeReply{}, r
		}
		if _, err := tx.ExecContext(ctx, "UPDATE attempts SET state='result_pending',revision=revision+1 WHERE id=? AND revision=?", attemptID, revision); err != nil {
			return FinalizeReply{}, err
		}
		if err := record(ctx, tx, recovered, revision+1); err != nil {
			return FinalizeReply{}, err
		}
		state, revision = p.ResultPending, revision+1
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
	completion := Completion{Version: execwire.Version, MessageID: dispatchID(receiptID, "reconcile-finalize"), ReceiptID: receiptID, Stream: StreamWatermark{Through: exit.StreamThrough, Digest: digest}, Exit: ExitRecord{Code: exit.Code, PGID: exit.PGID, ObservedUnixNS: exit.ObservedUnixNS}, Boundary: boundary}
	body, err := json.Marshal(retainedRuntime{MessageID: completion.MessageID, Completion: &completion})
	if err != nil {
		return FinalizeReply{}, err
	}
	sum := sha256.Sum256(body)
	actor, err := releaseActor(ctx, tx, d, "")
	if err != nil {
		return FinalizeReply{}, err
	}
	who, err := principal(ctx, tx, actor)
	if err != nil {
		return FinalizeReply{}, err
	}
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
 ON CONFLICT(task_id) DO UPDATE SET generation=excluded.generation,attempt_id=excluded.attempt_id,epoch=excluded.epoch,manifest_id=excluded.manifest_id,receipt_id=excluded.receipt_id,finalized_ms=excluded.finalized_ms`, identity.TaskID, identity.Generation, attemptID, identity.Epoch, manifestID, receiptID, now); err != nil {
			return FinalizeReply{}, err
		}
	}
	if _, err := retainRuntime(ctx, tx, attemptID, "completion", d.Facts.RunnerBoot, s.meta.DaemonBoot, body, now); err != nil {
		return FinalizeReply{}, err
	}
	if err := s.reconcileStep("before_complete_commit"); err != nil {
		return FinalizeReply{}, err
	}
	if err := tx.Commit(); err != nil {
		return FinalizeReply{}, err
	}
	return FinalizeReply{Outcome: string(to), Released: true}, s.reconcileStep("after_complete_commit")
}

// ClearLatches records clearances for every cancel_attempt stop whose attempt is
// terminal and whose reservation is released, for one task or (empty) all tasks.
// Durable point: the clearance rows committed together; stops, targets and
// observations are never deleted. Task pauses, the sticky task stop, global
// stops and authority supersession are never cleared.
func (s *Store) ClearLatches(ctx context.Context, taskID string) ([]LatchClearance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if taskID != "" && !p.ValidID(taskID) {
		return nil, g.Deny("malformed", "task_id")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	cleared, err := clearLatchesTx(ctx, tx, taskID, "", s.meta.DaemonBoot, s.controlNow().UnixMilli())
	if err != nil {
		return nil, err
	}
	if len(cleared) == 0 {
		return []LatchClearance{}, nil
	}
	if err := s.reconcileStep("before_clear_commit"); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return cleared, s.reconcileStep("after_clear_commit")
}

// LatchClearances reads the clearing records of one task or (empty) all tasks.
// Durable point: none (read).
func (s *Store) LatchClearances(ctx context.Context, taskID string) ([]LatchClearance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT body FROM reconcile_reports WHERE id LIKE ?||'%' ORDER BY created_ms,rowid", latchClearedPrefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LatchClearance{}
	for rows.Next() {
		var body string
		var v LatchClearance
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(body), &v) != nil {
			return nil, g.Deny("corrupt_record", "clearance")
		}
		if taskID == "" || v.TaskID == taskID {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

// TaskLatched reports whether admission for the task is suppressed by a stop that
// has not been cleared (see taskLatchedTx). Durable point: none (read).
func (s *Store) TaskLatched(ctx context.Context, taskID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(taskID) {
		return false, g.Deny("malformed", "task_id")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var grant string
	err = tx.QueryRowContext(ctx, "SELECT grant_id FROM execution_grant_heads WHERE task_id=?", taskID).Scan(&grant)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return taskLatchedTx(ctx, tx, taskID, grant)
}

// terminalCause derives the report cause of a terminal attempt from retained
// records: the release proof, this file's reconcile evidence, the stops that
// targeted it and the termination evidence's boots.
func terminalCause(ctx context.Context, tx *sql.Tx, d Dispatch, state p.AttemptState, daemonBoot string) (string, error) {
	switch state {
	case p.Succeeded, p.Failed:
		return string(state), nil
	case p.Cancelled, p.Expired:
	default:
		return "", nil
	}
	if state == p.Expired {
		return "lease_expired", nil
	}
	rows, err := runtimeRows(ctx, tx, d.Assignment.Identity.AttemptID)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if v, ok := decodeRuntime(row.Body); ok && row.Kind == "reconcile" && v.Reconcile != nil && v.Reconcile.Cause != "" {
			return v.Reconcile.Cause, nil
		}
	}
	causes, err := attemptStopCauses(ctx, tx, d.Assignment.Identity.AttemptID)
	if err != nil {
		return "", err
	}
	for _, cause := range causes {
		if cause == "operator" {
			return "operator", nil
		}
	}
	if len(causes) > 0 {
		return causes[len(causes)-1], nil
	}
	return "cancelled", nil
}

// RetryInputs reads what a retry decision needs for one task. Durable point:
// none (read); reading admits nothing.
func (s *Store) RetryInputs(ctx context.Context, taskID string) (RetryInputs, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(taskID) {
		return RetryInputs{}, g.Deny("malformed", "task_id")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return RetryInputs{}, err
	}
	defer tx.Rollback()
	v := RetryInputs{TaskID: taskID}
	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.epoch,a.state,a.revision,COALESCE(d.id,''),
 EXISTS(SELECT 1 FROM dispatch_acks k WHERE k.dispatch_id=d.id),EXISTS(SELECT 1 FROM dispatch_releases r WHERE r.dispatch_id=d.id)
 FROM attempts a LEFT JOIN dispatches d ON d.attempt_id=a.id WHERE a.task_id=? ORDER BY a.epoch`, taskID)
	if err != nil {
		return RetryInputs{}, err
	}
	for rows.Next() {
		a := AttemptSummary{Identity: p.Identity{Generation: s.meta.Generation, TaskID: taskID}}
		if err := rows.Scan(&a.Identity.AttemptID, &a.Identity.Epoch, &a.State, &a.Revision, &a.DispatchID, &a.Acknowledged, &a.Released); err != nil {
			rows.Close()
			return RetryInputs{}, err
		}
		v.Attempts = append(v.Attempts, a)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return RetryInputs{}, err
	}
	if v.Paused, err = pausedTx(ctx, tx); err != nil {
		return RetryInputs{}, err
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(min(created_ms),0) FROM dispatches WHERE task_id=?", taskID).Scan(&v.Dispatched, &v.FirstMS); err != nil {
		return RetryInputs{}, err
	}
	grant, err := headGrant(ctx, tx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		v.GrantRefusal = "unknown_grant"
	} else if err != nil {
		return RetryInputs{}, err
	} else {
		v.Grant = grant
		if err := liveGrant(ctx, tx, grant, s.controlNow().UnixMilli()); err != nil {
			var refusal *g.Refusal
			if !errors.As(err, &refusal) {
				return RetryInputs{}, err
			}
			v.GrantRefusal = refusal.Code
		}
	}
	if v.Latched, err = taskLatchedTx(ctx, tx, taskID, grant.ID); err != nil {
		return RetryInputs{}, err
	}
	if len(v.Attempts) == 0 {
		return v, nil
	}
	last := v.Attempts[len(v.Attempts)-1]
	v.LastState = last.State
	if last.DispatchID == "" {
		return v, nil
	}
	if v.Last, err = loadDispatch(ctx, tx, last.DispatchID); err != nil {
		return RetryInputs{}, err
	}
	v.Ceiling = v.Last.Request.Envelope.Budgets
	if v.Cause, err = terminalCause(ctx, tx, v.Last, last.State, s.meta.DaemonBoot); err != nil {
		return RetryInputs{}, err
	}
	return v, nil
}

// ReconcileReport reads one retained report. Durable point: none (read).
func (s *Store) ReconcileReport(ctx context.Context, id string) (ReconcileReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(id) {
		return ReconcileReport{}, g.Deny("malformed", "report_id")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return ReconcileReport{}, err
	}
	defer tx.Rollback()
	return readReport(ctx, tx, "SELECT id,daemon_boot,created_ms,body FROM reconcile_reports WHERE id=?", id)
}

// LatestReconcileReport reads the newest retained report (clearing records are
// not reports). Durable point: none (read); sql.ErrNoRows when none exists.
func (s *Store) LatestReconcileReport(ctx context.Context) (ReconcileReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return ReconcileReport{}, err
	}
	defer tx.Rollback()
	return readReport(ctx, tx, "SELECT id,daemon_boot,created_ms,body FROM reconcile_reports WHERE id NOT LIKE ?||'%' ORDER BY created_ms DESC,rowid DESC LIMIT 1", latchClearedPrefix)
}

func readReport(ctx context.Context, tx *sql.Tx, query string, args ...any) (ReconcileReport, error) {
	var v ReconcileReport
	var body string
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&v.ID, &v.DaemonBoot, &v.CreatedMS, &body); err != nil {
		return ReconcileReport{}, err
	}
	if !json.Valid([]byte(body)) {
		return ReconcileReport{}, g.Deny("corrupt_record", "report")
	}
	v.Body = json.RawMessage(body)
	return v, nil
}

// PutReconcileReport retains a report. Durable point: the committed
// reconcile_reports row; reports are immutable and never deleted, an identical
// replay is a no-op and a changed body under the same id is identity_conflict.
func (s *Store) PutReconcileReport(ctx context.Context, report ReconcileReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(report.ID) || !p.ValidID(report.DaemonBoot) || report.CreatedMS < 1 || report.CreatedMS > p.MaxInteger || len(report.Body) == 0 || len(report.Body) > maxReconcileReport || !json.Valid(report.Body) || strings.HasPrefix(report.ID, latchClearedPrefix) {
		return g.Deny("malformed", "report")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old string
	err = tx.QueryRowContext(ctx, "SELECT body FROM reconcile_reports WHERE id=?", report.ID).Scan(&old)
	if err == nil {
		if old != string(report.Body) {
			return p.IdentityConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO reconcile_reports VALUES(?,?,?,?)", report.ID, report.DaemonBoot, report.CreatedMS, string(report.Body)); err != nil {
		return err
	}
	return tx.Commit()
}
