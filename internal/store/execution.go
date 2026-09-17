package store

// Execution-channel store half (docs/decisions/0002 §4). Each method is the
// store side of one runner-mTLS /x/v1 route, reusing issueLeaseTx,
// observeTerminationTx and releaseDispatchTx from execution_tx.go. Every
// durable point commits before its reply is returned; a refusal that leaves
// evidence (a fenced lease request, retained old-boot termination) commits that
// evidence before the refusal is reported. Late or stale input is retained as
// history, never promoted to current state.
//
// Record types are defined here, inside package store, with the contract's wire
// field names so internal/execwire can marshal them directly. Wire rule: no
// nullable fields. Optional structs use omitzero, optional lists omitempty, and
// a list that is sent must be non-nil. fingerprint is always the authenticated
// runner credential from the TLS connection; session is the X-Gaffer-Session
// header, checked against runner_sessions for this daemon boot and generation;
// selected is the ALPN protocol read from the connection, never a body field.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/runstream"
	p "github.com/korallis/letmecook/schemas/execution"
)

// Session constants the daemon publishes in every Session reply (record §4).
const (
	SessionDriftMS       int64 = 2000
	SessionTerminationMS int64 = 5000
	LeaseValidityMS      int64 = 20000
	LeaseRenewEveryMS    int64 = 5000
	leaseMargin                = 7 * time.Second
	maxRuntimeBody             = 16384
	maxHelloBytes              = 65536
)

// HelloRecord is the runner's POST /x/v1/session body as persisted in
// runner_sessions.hello (≤ 65536 bytes). Journals are the supervisor's journal
// summaries, retained verbatim for reconcile.OnHello.
type HelloRecord struct {
	Version             string            `json:"version"`
	MessageID           string            `json:"message_id"`
	RunnerBoot          string            `json:"runner_boot"`
	EligibilityID       string            `json:"eligibility_id"`
	EligibilityRevision int64             `json:"eligibility_revision"`
	PolicyDigest        string            `json:"policy_digest"`
	Journals            []json.RawMessage `json:"journals,omitempty"`
}

// SessionRecord is the daemon's Session reply and the runner_sessions row.
// Mode is "normal" or "recovery_only" (evidence accepted, no launch). RunnerBoot
// is the boot the hello was bound to; every later route compares it with the
// dispatch's eligibility boot. Paused is read live, never from the row.
type SessionRecord struct {
	SessionID         string `json:"session_id"`
	Generation        string `json:"generation"`
	DaemonBoot        string `json:"daemon_boot"`
	DaemonFingerprint string `json:"daemon_fingerprint"`
	RunnerID          string `json:"runner_id"`
	RunnerBoot        string `json:"runner_boot"`
	// EligibilityID and EligibilityRevision are the facts the hello cited; a
	// session may only be shown dispatches admitted under exactly those facts.
	EligibilityID       string `json:"eligibility_id"`
	EligibilityRevision int64  `json:"eligibility_revision"`
	Mode                string `json:"mode"`
	DriftMS             int64  `json:"drift_ms"`
	TerminationMS       int64  `json:"termination_ms"`
	LeaseValidityMS     int64  `json:"lease_validity_ms"`
	RenewEveryMS        int64  `json:"renew_every_ms"`
	Paused              bool   `json:"paused"`
}

// StreamWatermark is the runstream sink position: records acknowledged through
// and the chain digest (runstream.StreamDigest) of everything up to it.
type StreamWatermark struct {
	Through int64  `json:"through"`
	Digest  string `json:"digest,omitempty"`
}

// ResultHead mirrors one artifact_result_heads row: the task's current finalized
// result. It is set only by FinalizeAttempt for a succeeded attempt.
type ResultHead struct {
	TaskID      string `json:"task_id"`
	Generation  string `json:"generation"`
	AttemptID   string `json:"attempt_id"`
	Epoch       int64  `json:"epoch"`
	ManifestID  string `json:"manifest_id"`
	ReceiptID   string `json:"receipt_id"`
	FinalizedMS int64  `json:"finalized_ms"`
}

// ExecutionState is the GET /x/v1/state reply for one dispatch.
type ExecutionState struct {
	Identity     p.Identity      `json:"identity"`
	AttemptState p.AttemptState  `json:"attempt_state"`
	Revision     int64           `json:"revision"`
	Acknowledged bool            `json:"acknowledged"`
	Released     bool            `json:"released"`
	LastLease    c.Lease         `json:"last_lease,omitzero"`
	Stream       StreamWatermark `json:"stream"`
	ReceiptID    string          `json:"receipt_id,omitempty"`
	Head         ResultHead      `json:"head,omitzero"`
	StopTargets  []c.Target      `json:"stop_targets,omitempty"`
	Paused       bool            `json:"paused"`
}

// RuntimeEvidence accompanies a transition proposal and is retained verbatim in
// runtime_observations. Kind selects the meaningful fields: launch_intent
// (workspace, boundary_port, nonce; guardian_pid must be 0 because the guardian
// is not spawned yet, and boundary_port is 0 exactly when the task brief names
// the fake harness), launched (guardian_pid, pid, pgid, start_unix_ns, all
// non-zero), exit (code, pgid, pgid_empty, observed_unix_ns, stream_through) and
// stop (optional nonce naming the lapsed lease). Numeric and boolean fields are
// always emitted, zero included, so an exit code of 0 or a stream watermark of 0
// is evidence rather than an omission; only the two strings are optional.
type RuntimeEvidence struct {
	Kind           string `json:"kind"`
	Workspace      string `json:"workspace,omitempty"`
	BoundaryPort   int    `json:"boundary_port"`
	GuardianPID    int    `json:"guardian_pid"`
	Nonce          string `json:"nonce,omitempty"`
	PID            int    `json:"pid"`
	PGID           int    `json:"pgid"`
	StartUnixNS    int64  `json:"start_unix_ns"`
	Code           int    `json:"code"`
	PGIDEmpty      bool   `json:"pgid_empty"`
	ObservedUnixNS int64  `json:"observed_unix_ns"`
	StreamThrough  int64  `json:"stream_through"`
	Cause          string `json:"cause,omitempty"`
}

// BoundaryState mirrors the inference boundary's reservation accounting at the
// time of a report. Quiescent means reservations equal terminal receipts and
// nothing is in flight; only then may remote work be called complete.
type BoundaryState struct {
	Reservations     int64 `json:"reservations"`
	TerminalReceipts int64 `json:"terminal_receipts"`
	InFlight         int64 `json:"in_flight"`
	Quiescent        bool  `json:"quiescent"`
}

// settled reports whether the boundary's own accounting agrees with its flag.
func (b BoundaryState) settled() bool {
	return b.Quiescent && b.InFlight == 0 && b.Reservations == b.TerminalReceipts && b.Reservations >= 0
}

// TerminationReply is the POST /x/v1/messages reply for kind terminated:
// outcome observed|retained, released true only after releaseDispatchTx committed.
type TerminationReply struct {
	Outcome  string `json:"outcome"`
	Released bool   `json:"released"`
}

// UsageReceipt is one boundary receipt as posted to POST /x/v1/usage and stored
// in attempt_usage (upsert by request_id; a terminal receipt supersedes its
// reservation).
type UsageReceipt struct {
	RequestID        string `json:"request_id"`
	Protocol         string `json:"protocol"`
	Model            string `json:"model"`
	Status           int    `json:"status"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	BytesIn          int64  `json:"bytes_in"`
	BytesOut         int64  `json:"bytes_out"`
	StartedMS        int64  `json:"started_ms"`
	EndedMS          int64  `json:"ended_ms"`
	Terminal         bool   `json:"terminal"`
	Source           string `json:"source"`
}

// UsageReport is the POST /x/v1/usage body.
type UsageReport struct {
	Version   string         `json:"version"`
	MessageID string         `json:"message_id"`
	Identity  p.Identity     `json:"identity"`
	Receipts  []UsageReceipt `json:"receipts,omitempty"`
}

// TaskInput is the GET /x/v1/input reply: the retained task brief the dispatch
// was admitted for, bound to the grant by BriefSHA256. Harness is the dispatch
// route's harness (grant-bound); Settings is the retained harness settings object.
type TaskInput struct {
	DispatchID  string          `json:"dispatch_id"`
	TaskID      string          `json:"task_id"`
	BriefSHA256 string          `json:"brief_sha256"`
	Brief       string          `json:"brief"`
	Criteria    []Criterion     `json:"criteria"`
	Paths       []string        `json:"paths"`
	Operations  []string        `json:"operations"`
	Harness     string          `json:"harness"`
	Settings    json.RawMessage `json:"settings"`
	BaseCommit  string          `json:"base_commit"`
	Repository  string          `json:"repository"`
}

// runtimeRecord is the retained runtime_observations body: the runner message
// the evidence arrived with plus exactly one evidence payload.
type runtimeRecord struct {
	MessageID   string           `json:"message_id"`
	Message     *p.Message       `json:"message,omitempty"`
	Evidence    *RuntimeEvidence `json:"evidence,omitempty"`
	Termination *c.Evidence      `json:"termination,omitempty"`
	Boundary    *BoundaryState   `json:"boundary,omitempty"`
	Completion  *Completion      `json:"completion,omitempty"`
	Receipt     *runnerReceipt   `json:"receipt,omitempty"`
}

// runnerReceipt is the one replay rule for runner requests: per (route,
// message_id) the digest of the canonical request and the response it earned,
// retained in the transaction that applied it. An identical request replays the
// response; a changed request under the same id is identity_conflict and is
// never applied.
type runnerReceipt struct {
	Route         string          `json:"route"`
	RequestSHA256 string          `json:"request_sha256"`
	Response      json.RawMessage `json:"response"`
}

const (
	ReceiptMessages = "messages"
	ReceiptUsage    = "usage"
	ReceiptFinalize = "finalize"
)

func requestDigest(v any) (string, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return "", p.Malformed
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// retainedReceipt finds the receipt for (attempt, route, messageID). found is
// false when none exists; a retained request with another digest is
// identity_conflict.
func retainedReceipt(ctx context.Context, tx *sql.Tx, attemptID, route, messageID, digest string) (json.RawMessage, bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT body FROM runtime_observations WHERE attempt_id=? AND kind='receipt'", attemptID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		var v runtimeRecord
		if err := rows.Scan(&body); err != nil {
			return nil, false, err
		}
		if json.Unmarshal([]byte(body), &v) != nil || v.Receipt == nil || v.MessageID != messageID || v.Receipt.Route != route {
			continue
		}
		if v.Receipt.RequestSHA256 != digest {
			return nil, false, p.IdentityConflict
		}
		return v.Receipt.Response, true, nil
	}
	return nil, false, rows.Err()
}

func recordReceipt(ctx context.Context, tx *sql.Tx, attemptID, route, messageID, digest string, response any, runnerBoot, daemonBoot string, now int64) error {
	raw, err := json.Marshal(response)
	if err != nil {
		return p.Malformed
	}
	body, err := runtimeBody(runtimeRecord{MessageID: messageID, Receipt: &runnerReceipt{Route: route, RequestSHA256: digest, Response: raw}})
	if err != nil {
		return err
	}
	_, err = recordRuntime(ctx, tx, attemptID, "receipt", runnerBoot, daemonBoot, body, now)
	return err
}

// Binds reports whether a session may see or acknowledge a dispatch: the
// dispatch must have been admitted under the session's runner boot and the
// exact eligibility revision the hello cited. Anything else belongs to another
// runner incarnation and is reconcile's, never this session's.
func (v SessionRecord) Binds(d Dispatch) error {
	if d.Facts.RunnerBoot != v.RunnerBoot {
		return p.BootMismatch
	}
	if d.Facts.ID != v.EligibilityID || d.Facts.Revision != v.EligibilityRevision {
		return g.Deny("reconciliation_required", "eligibility")
	}
	return nil
}

func sessionStale() error { return g.Deny("session_stale", "session") }

// step is the instance-local crash/failure seam shared with control.go: tests
// return an error at a named durable boundary to prove commit-before-reply.
func (s *Store) step(name string) error {
	if s.controlHook != nil {
		return s.controlHook(name)
	}
	return nil
}

func daemonPaused(ctx context.Context, tx *sql.Tx) (bool, error) {
	var paused bool
	err := tx.QueryRowContext(ctx, "SELECT paused FROM daemon_state WHERE singleton=1").Scan(&paused)
	return paused, err
}

func terminalState(state p.AttemptState) bool {
	return state == p.Succeeded || state == p.Failed || state == p.Cancelled || state == p.Expired
}

// runnerSession authenticates the runner and binds the request to a session
// created under this daemon boot and generation by the same principal. Any other
// session is stale: the runner must hello again.
func (s *Store) runnerSession(ctx context.Context, tx *sql.Tx, fingerprint, session string) (i.Principal, SessionRecord, error) {
	who, err := principal(ctx, tx, fingerprint)
	if err != nil {
		return who, SessionRecord{}, err
	}
	if who.Role != "runner" || !who.Enabled {
		return who, SessionRecord{}, i.Denied
	}
	if !p.ValidID(session) {
		return who, SessionRecord{}, sessionStale()
	}
	v := SessionRecord{DriftMS: SessionDriftMS, TerminationMS: SessionTerminationMS, LeaseValidityMS: LeaseValidityMS, RenewEveryMS: LeaseRenewEveryMS}
	var hello string
	err = tx.QueryRowContext(ctx, "SELECT id,runner_id,runner_boot,daemon_boot,generation,mode,hello FROM runner_sessions WHERE id=?", session).Scan(&v.SessionID, &v.RunnerID, &v.RunnerBoot, &v.DaemonBoot, &v.Generation, &v.Mode, &hello)
	if errors.Is(err, sql.ErrNoRows) {
		return who, SessionRecord{}, sessionStale()
	}
	if err != nil {
		return who, SessionRecord{}, err
	}
	if v.RunnerID != who.ID || v.DaemonBoot != s.meta.DaemonBoot || v.Generation != s.meta.Generation {
		return who, SessionRecord{}, sessionStale()
	}
	var cited HelloRecord
	if json.Unmarshal([]byte(hello), &cited) != nil {
		return who, SessionRecord{}, g.Deny("corrupt_record", "session")
	}
	v.EligibilityID, v.EligibilityRevision = cited.EligibilityID, cited.EligibilityRevision
	if v.Paused, err = daemonPaused(ctx, tx); err != nil {
		return who, SessionRecord{}, err
	}
	return who, v, nil
}

// runnerDispatch loads the dispatch behind attemptID and checks the runner owns it.
func runnerDispatch(ctx context.Context, tx *sql.Tx, who i.Principal, attemptID string) (Dispatch, error) {
	if !p.ValidID(attemptID) {
		return Dispatch{}, g.Deny("malformed", "attempt_id")
	}
	var id string
	err := tx.QueryRowContext(ctx, "SELECT id FROM dispatches WHERE attempt_id=?", attemptID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Dispatch{}, p.StaleAttempt
	}
	if err != nil {
		return Dispatch{}, err
	}
	return dispatchOwnedBy(ctx, tx, who, id)
}

func dispatchOwnedBy(ctx context.Context, tx *sql.Tx, who i.Principal, dispatchID string) (Dispatch, error) {
	if !p.ValidID(dispatchID) {
		return Dispatch{}, g.Deny("malformed", "dispatch_id")
	}
	d, err := loadDispatch(ctx, tx, dispatchID)
	if err != nil {
		return Dispatch{}, err
	}
	if who.Role != "runner" || who.ID != d.Facts.Repository.RunnerRoot.RunnerID {
		return Dispatch{}, g.Deny("runner_disabled", "dispatch")
	}
	return d, nil
}

func attemptState(ctx context.Context, tx *sql.Tx, attemptID string) (p.AttemptState, int64, error) {
	var state p.AttemptState
	var revision int64
	err := tx.QueryRowContext(ctx, "SELECT state,revision FROM attempts WHERE id=?", attemptID).Scan(&state, &revision)
	return state, revision, err
}

func resultHead(ctx context.Context, tx *sql.Tx, taskID string) (ResultHead, error) {
	v := ResultHead{TaskID: taskID}
	err := tx.QueryRowContext(ctx, "SELECT generation,attempt_id,epoch,manifest_id,receipt_id,finalized_ms FROM artifact_result_heads WHERE task_id=?", taskID).Scan(&v.Generation, &v.AttemptID, &v.Epoch, &v.ManifestID, &v.ReceiptID, &v.FinalizedMS)
	if errors.Is(err, sql.ErrNoRows) {
		return ResultHead{}, nil
	}
	return v, err
}

// recordRuntime retains one evidence body keyed by its digest. An identical body
// is a no-op, so replays never duplicate history; a changed body is a new row.
func recordRuntime(ctx context.Context, tx *sql.Tx, attemptID, kind, runnerBoot, daemonBoot string, body []byte, now int64) (string, error) {
	if len(body) > maxRuntimeBody {
		return "", p.Oversized
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	_, err := tx.ExecContext(ctx, "INSERT INTO runtime_observations VALUES(?,?,?,?,?,?,?) ON CONFLICT(attempt_id,evidence_sha256) DO NOTHING", attemptID, digest, kind, runnerBoot, daemonBoot, string(body), now)
	return digest, err
}

func runtimeBody(v runtimeRecord) ([]byte, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, p.Malformed
	}
	if len(body) > maxRuntimeBody {
		return nil, p.Oversized
	}
	return body, nil
}

// Streams is the read-only face of the daemon's per-attempt output sink set at
// <state-dir>/streams. Each persistent Store owns its sinks through Close;
// fixture stores have no execution sinks. Only AppendStream writes, serialized
// per attempt with finalization.
func (s *Store) Streams() runstream.SinkView {
	if s.streams == nil {
		return nil
	}
	return s.streams.View()
}

// sinks is writable only inside the store's serialized append/finalize paths.
func (s *Store) sinks() *runstream.Sinks { return s.streams }

// RunnerSession binds an authenticated runner hello to a session. Durable point:
// the runner_sessions row (mode normal|recovery_only) committed before the reply;
// the same message_id returns the same row and a changed hello under it is
// identity_conflict. Mode is normal only when the hello cites the runner's
// latest published eligibility revision and that record carries the hello's
// runner_boot; otherwise the session is recovery_only (evidence accepted, no
// launch) until the owner re-imports facts. It is the store half of
// POST /x/v1/session; reconcile.OnHello runs after it in the handler.
func (s *Store) RunnerSession(ctx context.Context, fingerprint string, hello HelloRecord) (SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if hello.Version != execwire.Version || !p.ValidID(hello.MessageID) || !p.ValidID(hello.RunnerBoot) || !g.ValidActor(hello.EligibilityID) || hello.EligibilityRevision < 1 || hello.EligibilityRevision > p.MaxInteger || (hello.PolicyDigest != "" && !validHex(hello.PolicyDigest)) {
		return SessionRecord{}, p.Malformed
	}
	for _, journal := range hello.Journals {
		if !json.Valid(journal) {
			return SessionRecord{}, p.Malformed
		}
	}
	body, err := json.Marshal(hello)
	if err != nil {
		return SessionRecord{}, p.Malformed
	}
	if len(body) > maxHelloBytes {
		return SessionRecord{}, p.Oversized
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return SessionRecord{}, err
	}
	defer tx.Rollback()
	who, err := principal(ctx, tx, fingerprint)
	if err != nil {
		return SessionRecord{}, err
	}
	if who.Role != "runner" || !who.Enabled {
		return SessionRecord{}, i.Denied
	}
	paused, err := daemonPaused(ctx, tx)
	if err != nil {
		return SessionRecord{}, err
	}
	v := SessionRecord{SessionID: dispatchID(hello.MessageID, "session"), RunnerID: who.ID, RunnerBoot: hello.RunnerBoot, EligibilityID: hello.EligibilityID, EligibilityRevision: hello.EligibilityRevision, DriftMS: SessionDriftMS, TerminationMS: SessionTerminationMS, LeaseValidityMS: LeaseValidityMS, RenewEveryMS: LeaseRenewEveryMS, Paused: paused}
	var oldRunner, oldHello string
	err = tx.QueryRowContext(ctx, "SELECT runner_id,runner_boot,daemon_boot,generation,mode,hello FROM runner_sessions WHERE id=?", v.SessionID).Scan(&oldRunner, &v.RunnerBoot, &v.DaemonBoot, &v.Generation, &v.Mode, &oldHello)
	if err == nil {
		if oldRunner != who.ID || oldHello != string(body) {
			return SessionRecord{}, p.IdentityConflict
		}
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, err
	}
	v.DaemonBoot, v.Generation, v.Mode = s.meta.DaemonBoot, s.meta.Generation, "recovery_only"
	facts, err := eligibility(ctx, tx, hello.EligibilityID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, err
	}
	if err == nil && facts.Enabled && facts.Repository.RunnerRoot.RunnerID == who.ID && facts.Revision == hello.EligibilityRevision && facts.RunnerBoot == hello.RunnerBoot {
		v.Mode = "normal"
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO runner_sessions VALUES(?,?,?,?,?,?,?,?)", v.SessionID, who.ID, hello.RunnerBoot, s.meta.DaemonBoot, s.meta.Generation, v.Mode, string(body), time.Now().UnixMilli()); err != nil {
		return SessionRecord{}, err
	}
	if err := s.step("before_session_commit"); err != nil {
		return SessionRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionRecord{}, err
	}
	if err := s.step("after_session_commit"); err != nil {
		return SessionRecord{}, err
	}
	return v, nil
}

// ExecutionSession validates the session header for a route and returns the
// bound session with the live paused flag. Durable point: none (read).
func (s *Store) ExecutionSession(ctx context.Context, fingerprint, session string) (SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return SessionRecord{}, err
	}
	defer tx.Rollback()
	_, v, err := s.runnerSession(ctx, tx, fingerprint, session)
	return v, err
}

// ExecutionState is the read behind GET /x/v1/state: attempt state and revision,
// acknowledgement/release, last lease, stream watermark, receipt, head, pending
// stop targets and the paused flag. Durable point: none (read).
func (s *Store) ExecutionState(ctx context.Context, fingerprint, session, dispatchID string) (ExecutionState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return ExecutionState{}, err
	}
	defer tx.Rollback()
	who, sess, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return ExecutionState{}, err
	}
	d, err := dispatchOwnedBy(ctx, tx, who, dispatchID)
	if err != nil {
		return ExecutionState{}, err
	}
	identity := d.Assignment.Identity
	v := ExecutionState{Identity: identity, Acknowledged: d.Acknowledged, Released: d.Released, Paused: sess.Paused}
	if v.AttemptState, v.Revision, err = attemptState(ctx, tx, identity.AttemptID); err != nil {
		return ExecutionState{}, err
	}
	lease, err := lastControlLease(ctx, tx, identity.AttemptID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ExecutionState{}, err
	}
	if err == nil {
		v.LastLease = lease
	}
	err = tx.QueryRowContext(ctx, "SELECT receipt_id FROM artifact_results WHERE generation=? AND task_id=? AND attempt_id=? AND epoch=? AND quarantined=0", identity.Generation, identity.TaskID, identity.AttemptID, identity.Epoch).Scan(&v.ReceiptID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ExecutionState{}, err
	}
	if v.Head, err = resultHead(ctx, tx, identity.TaskID); err != nil {
		return ExecutionState{}, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT body FROM control_targets WHERE attempt_id=? ORDER BY rowid", identity.AttemptID)
	if err != nil {
		return ExecutionState{}, err
	}
	for rows.Next() {
		var body string
		var target c.Target
		if err := rows.Scan(&body); err != nil {
			rows.Close()
			return ExecutionState{}, err
		}
		if err := decodeControl(body, &target); err != nil {
			rows.Close()
			return ExecutionState{}, err
		}
		v.StopTargets = append(v.StopTargets, target)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return ExecutionState{}, err
	}
	watermark, err := s.sinks().Watermark(identity.AttemptID)
	if err != nil {
		return ExecutionState{}, err
	}
	v.Stream = StreamWatermark{Through: watermark.Through, Digest: watermark.Digest}
	return v, nil
}

// TaskInput reads the retained brief behind a dispatch for GET /x/v1/input. The
// brief revision the grant bound (Envelope.Brief.SHA256) must equal the stored
// brief_sha256 and the digest recomputed from the stored fields; any drift is
// reconciliation_required. Durable point: none (read); identical on replay.
func (s *Store) TaskInput(ctx context.Context, fingerprint, session, dispatchID string) (TaskInput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return TaskInput{}, err
	}
	defer tx.Rollback()
	who, _, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return TaskInput{}, err
	}
	d, err := dispatchOwnedBy(ctx, tx, who, dispatchID)
	if err != nil {
		return TaskInput{}, err
	}
	v := TaskInput{DispatchID: d.ID, TaskID: d.Request.TaskID}
	var criteria, paths, operations, settings, stored string
	err = tx.QueryRowContext(ctx, "SELECT repository_id,base_commit,brief,criteria,paths,operations,harness,settings,brief_sha256 FROM task_briefs WHERE task_id=?", v.TaskID).Scan(&v.Repository, &v.BaseCommit, &v.Brief, &criteria, &paths, &operations, &v.Harness, &settings, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskInput{}, g.Deny("reconciliation_required", "task_brief")
	}
	if err != nil {
		return TaskInput{}, err
	}
	if json.Unmarshal([]byte(criteria), &v.Criteria) != nil || json.Unmarshal([]byte(paths), &v.Paths) != nil || json.Unmarshal([]byte(operations), &v.Operations) != nil {
		return TaskInput{}, g.Deny("corrupt_record", "task_brief")
	}
	if v.Settings, err = canonicalSettings(settings); err != nil {
		return TaskInput{}, g.Deny("corrupt_record", "task_brief")
	}
	if v.Criteria == nil {
		v.Criteria = []Criterion{}
	}
	if v.Paths == nil {
		v.Paths = []string{}
	}
	if v.Operations == nil {
		v.Operations = []string{}
	}
	digest := BriefDigest(TaskBrief{Brief: v.Brief, Criteria: v.Criteria, Paths: v.Paths,
		Operations: v.Operations, Harness: v.Harness, Settings: v.Settings})
	envelope := d.Request.Envelope
	if digest != stored || digest != envelope.Brief.SHA256 || v.Repository != envelope.Repository || v.BaseCommit != envelope.BaseCommit {
		return TaskInput{}, g.Deny("reconciliation_required", "brief_digest")
	}
	v.BriefSHA256 = digest
	return v, nil
}

// canonicalSettings renders a stored settings document as one JSON object with
// sorted keys and no whitespace; number tokens are preserved verbatim.
func canonicalSettings(raw string) (json.RawMessage, error) {
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	if _, ok := v.(map[string]any); !ok {
		return nil, errors.New("settings must be an object")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("trailing settings content")
	}
	return json.Marshal(v)
}

// PendingCancels lists the retained cancel outbox messages (control_targets) for
// the runner's non-terminal attempts that have no termination observation yet.
// Durable point: none (read); delivery is not acknowledgement.
func (s *Store) PendingCancels(ctx context.Context, fingerprint, session string) ([]p.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	who, _, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT t.body FROM control_targets t JOIN dispatches d ON d.attempt_id=t.attempt_id JOIN attempts a ON a.id=t.attempt_id
 WHERE d.runner_id=? AND a.state NOT IN ('succeeded','failed','cancelled','expired')
 AND NOT EXISTS(SELECT 1 FROM control_observations o WHERE o.stop_id=t.stop_id AND o.attempt_id=t.attempt_id) ORDER BY t.rowid`, who.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []p.Message{}
	for rows.Next() {
		var body string
		var target c.Target
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := decodeControl(body, &target); err != nil {
			return nil, err
		}
		if p.CheckSession(target.Cancel, target.Cancel.Identity, p.FencedVersion) != p.OK {
			return nil, g.Deny("corrupt_record", "target")
		}
		out = append(out, target.Cancel)
	}
	return out, rows.Err()
}

// PendingAssignmentsFor lists the unacknowledged, unreleased dispatches this
// session may be offered: admitted for its runner under the eligibility revision
// and runner boot the hello cited, in id order, at most limit. Filtering in the
// query means a backlog for other runners or incarnations, however large, never
// hides them. Durable point: none (read); delivery is a separate admission.
func (s *Store) PendingAssignmentsFor(ctx context.Context, fingerprint, session string, limit int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit < 1 || limit > 128 {
		return nil, g.Deny("malformed", "limit")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	who, sess, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM dispatches d WHERE runner_id=? AND eligibility_id=? AND eligibility_revision=? AND json_extract(input,'$.facts.runner_boot')=?
 AND NOT EXISTS(SELECT 1 FROM dispatch_acks a WHERE a.dispatch_id=d.id) AND NOT EXISTS(SELECT 1 FROM dispatch_releases r WHERE r.dispatch_id=d.id) ORDER BY id LIMIT ?`, who.ID, sess.EligibilityID, sess.EligibilityRevision, sess.RunnerBoot, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// StreamIdentity binds POST /x/v1/streams/{attempt_id} to the attempt's real
// identity: the runner must own the dispatch, the generation must be current
// and the attempt non-terminal. Durable point: none (read).
func (s *Store) StreamIdentity(ctx context.Context, fingerprint, session, attemptID string) (p.Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return p.Identity{}, err
	}
	defer tx.Rollback()
	who, _, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return p.Identity{}, err
	}
	d, err := runnerDispatch(ctx, tx, who, attemptID)
	if err != nil {
		return p.Identity{}, err
	}
	if d.Assignment.Identity.Generation != s.meta.Generation {
		return p.Identity{}, p.StaleGeneration
	}
	state, _, err := attemptState(ctx, tx, attemptID)
	if err != nil {
		return p.Identity{}, err
	}
	if terminalState(state) {
		return p.Identity{}, p.StaleAttempt
	}
	return d.Assignment.Identity, nil
}

// AppendStream is the store half of POST /x/v1/streams/{attempt_id}. The
// per-attempt lock is held across the append (so finalization of that attempt
// cannot interleave with it) while the store lock covers only the admission
// transaction, so slow storage never blocks unrelated attempts, leases or
// stops. After the attempt is terminal only retained records replay their
// acknowledgement; new records are refused stale_attempt. Durable point: the
// sink's fsync per record; the acknowledgement follows it.
func (s *Store) AppendStream(ctx context.Context, fingerprint, session, attemptID string, records []runstream.Record) (runstream.Ack, error) {
	if !p.ValidID(attemptID) || len(records) == 0 {
		return runstream.Ack{}, p.Malformed
	}
	unlock := s.sinks().Serialize(attemptID)
	defer unlock()
	identity, terminal, err := s.appendAdmit(ctx, fingerprint, session, attemptID)
	if err != nil {
		return runstream.Ack{}, err
	}
	if err := s.step("append_stream_io"); err != nil {
		return runstream.Ack{}, err
	}
	if terminal {
		watermark, err := s.sinks().Watermark(attemptID)
		if err != nil {
			return runstream.Ack{}, err
		}
		if records[len(records)-1].Sequence > watermark.Through {
			return runstream.Ack{}, p.StaleAttempt
		}
	}
	return s.sinks().Receive(attemptID, identity, records)
}

// appendAdmit binds a batch to its attempt under the store lock only.
func (s *Store) appendAdmit(ctx context.Context, fingerprint, session, attemptID string) (p.Identity, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return p.Identity{}, false, err
	}
	defer tx.Rollback()
	who, _, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return p.Identity{}, false, err
	}
	d, err := runnerDispatch(ctx, tx, who, attemptID)
	if err != nil {
		return p.Identity{}, false, err
	}
	state, _, err := attemptState(ctx, tx, attemptID)
	if err != nil {
		return p.Identity{}, false, err
	}
	if d.Assignment.Identity.Generation != s.meta.Generation {
		return p.Identity{}, false, p.StaleGeneration
	}
	return d.Assignment.Identity, terminalState(state), nil
}

// terminationRevision reports whether a later terminated message (new message
// id) attests the same or stronger termination as the retained observation: the
// same stop, identity, boots, digest and confirmed process, with remote_work
// allowed to move from unknown to quiescent, never back.
func terminationRevision(retained, now c.Evidence) bool {
	a, b := retained.Terminated, now.Terminated
	a.MessageID, b.MessageID = "", ""
	if a.RemoteWork == "unknown" && b.RemoteWork == "quiescent" {
		a.RemoteWork = "quiescent"
	}
	return a == b && reflect.DeepEqual(retained.Measurement, now.Measurement)
}

// strongestTermination returns the strongest termination retained for a stop:
// the immutable control observation upgraded by any accepted revision under
// the current daemon boot that attested quiescent remote work. Revisions are
// validated against it, so a weaker later report never regresses history.
func (s *Store) strongestTermination(ctx context.Context, tx *sql.Tx, attemptID, stopID string, base c.Evidence) (c.Evidence, error) {
	rows, err := tx.QueryContext(ctx, "SELECT body FROM runtime_observations WHERE attempt_id=? AND kind='terminated'", attemptID)
	if err != nil {
		return base, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		var v runtimeRecord
		if err := rows.Scan(&body); err != nil {
			return base, err
		}
		if json.Unmarshal([]byte(body), &v) != nil || v.Termination == nil {
			continue
		}
		t := v.Termination.Terminated
		if t.StopID != stopID || t.DaemonBoot != s.meta.DaemonBoot || t.ConfirmedProcess == "unknown" || t.RemoteWork != "quiescent" {
			continue
		}
		if terminationRevision(base, *v.Termination) {
			return *v.Termination, nil
		}
	}
	return base, rows.Err()
}

// TerminationView is the effective termination of one stop: the immutable
// control observation upgraded by its strongest accepted revision. StopStatus
// uses the same selection without rewriting the first observation's history.
// Durable point: none (read).
func (s *Store) TerminationView(ctx context.Context, stopID, attemptID string) (c.Evidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(stopID) || !p.ValidID(attemptID) {
		return c.Evidence{}, g.Deny("malformed", "stop")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return c.Evidence{}, err
	}
	defer tx.Rollback()
	var body string
	if err := tx.QueryRowContext(ctx, "SELECT body FROM control_observations WHERE stop_id=? AND attempt_id=?", stopID, attemptID).Scan(&body); err != nil {
		return c.Evidence{}, err
	}
	var base c.Evidence
	if err := decodeControl(body, &base); err != nil {
		return c.Evidence{}, err
	}
	return s.strongestTermination(ctx, tx, attemptID, stopID, base)
}

// RecordRefusal retains a runner's refuse of its assignment as a
// runtime_observations row of kind refused, which reconcile classifies as
// refused_before_accept. Durable point: the committed row; identical replays
// are no-ops. A refusal after acknowledgement is reconciliation_required.
func (s *Store) RecordRefusal(ctx context.Context, fingerprint, session, selected, dispatchID string, m p.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.Kind != "refuse" {
		return p.Malformed
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	who, sess, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return err
	}
	d, err := dispatchOwnedBy(ctx, tx, who, dispatchID)
	if err != nil {
		return err
	}
	if r := p.CheckSession(m, d.Assignment.Identity, selected); r != p.OK {
		return r
	}
	if d.Assignment.Identity.Generation != s.meta.Generation {
		return p.StaleGeneration
	}
	digest, err := requestDigest(m)
	if err != nil {
		return err
	}
	if _, found, err := retainedReceipt(ctx, tx, d.Assignment.Identity.AttemptID, ReceiptMessages, m.MessageID, digest); err != nil || found {
		return err
	}
	if m.InReplyTo != d.Assignment.MessageID {
		return p.IdentityConflict
	}
	if d.Acknowledged {
		return p.ReconciliationRequired
	}
	if err := controlMessage(ctx, tx, m); err != nil {
		return err
	}
	body, err := runtimeBody(runtimeRecord{MessageID: m.MessageID, Message: &m})
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if _, err := recordRuntime(ctx, tx, d.Assignment.Identity.AttemptID, "refused", sess.RunnerBoot, s.meta.DaemonBoot, body, now); err != nil {
		return err
	}
	if err := recordReceipt(ctx, tx, d.Assignment.Identity.AttemptID, ReceiptMessages, m.MessageID, digest, map[string]string{"outcome": "recorded"}, sess.RunnerBoot, s.meta.DaemonBoot, now); err != nil {
		return err
	}
	if err := s.step("before_refusal_commit"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.step("after_refusal_commit")
}

// transitionEvidence checks the evidence a proposal must carry for its target
// state against durable state: launch_intent needs the current lease's nonce
// and no guardian pid yet; launched needs every process fact; exit needs the
// empty-pgid observation and the sink watermark; stopping needs a latched cancel
// target or the runner's own last lease nonce (lease lapse).
func (s *Store) transitionEvidence(ctx context.Context, tx *sql.Tx, taskID, attemptID string, to p.AttemptState, e RuntimeEvidence, now c.Stamp) error {
	switch to {
	case p.Starting:
		// The fake harness performs no inference and runs without a boundary, so
		// its launch intent names port 0; every other harness names a real port.
		var harness string
		err := tx.QueryRowContext(ctx, "SELECT harness FROM task_briefs WHERE task_id=?", taskID).Scan(&harness)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		port := e.BoundaryPort >= 1 && e.BoundaryPort <= 65535
		if harness == "fake" {
			port = e.BoundaryPort == 0
		}
		if e.Kind != "launch_intent" || e.Workspace == "" || len(e.Workspace) > 1024 || !port || e.GuardianPID != 0 || !p.ValidID(e.Nonce) {
			return p.Malformed
		}
		lease, err := lastControlLease(ctx, tx, attemptID)
		if errors.Is(err, sql.ErrNoRows) {
			return p.ReconciliationRequired
		}
		if err != nil {
			return err
		}
		var revoked bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM control_revocations WHERE nonce=?)", lease.Request.Nonce).Scan(&revoked); err != nil {
			return err
		}
		if lease.Request.Nonce != e.Nonce || lease.Issued.Boot != now.Boot || now.ElapsedNS >= lease.DeadlineNS || revoked {
			return p.ReconciliationRequired
		}
	case p.Running:
		if e.Kind != "launched" || e.GuardianPID <= 0 || e.PID <= 0 || e.PGID <= 0 || e.StartUnixNS <= 0 {
			return p.Malformed
		}
	case p.ResultPending:
		if e.Kind != "exit" || !e.PGIDEmpty || e.PGID <= 0 || e.ObservedUnixNS <= 0 || e.StreamThrough < 0 {
			return p.Malformed
		}
		watermark, err := s.sinks().Watermark(attemptID)
		if err != nil {
			return err
		}
		if watermark.Through != e.StreamThrough {
			return p.ReconciliationRequired
		}
	case p.Stopping:
		if e.Kind != "stop" {
			return p.Malformed
		}
		if e.Cause != "" {
			// A runner-local stop names its cause; the daemon latches the derived
			// cancel (actor runner-local) so the later terminated report observes
			// and releases against a retained target.
			if !slices.Contains(execwire.LocalStopCauses, e.Cause) {
				return p.Malformed
			}
			stopID := execwire.LocalStopID(attemptID, e.Cause)
			var latched bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM control_targets WHERE stop_id=? AND attempt_id=?)", stopID, attemptID).Scan(&latched); err != nil {
				return err
			}
			if latched {
				return nil
			}
			_, err := latchStop(ctx, tx, "runner-local", c.Request{ID: stopID, Kind: c.CancelAttempt, TaskID: taskID, AttemptID: attemptID, Cause: e.Cause}, now)
			return err
		}
		var latched bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM control_targets WHERE attempt_id=?)", attemptID).Scan(&latched); err != nil {
			return err
		}
		if latched {
			return nil
		}
		lease, err := lastControlLease(ctx, tx, attemptID)
		if errors.Is(err, sql.ErrNoRows) {
			return p.ReconciliationRequired
		}
		if err != nil {
			return err
		}
		if !p.ValidID(e.Nonce) || lease.Request.Nonce != e.Nonce {
			return p.ReconciliationRequired
		}
	default:
		return p.InvalidTransition
	}
	return nil
}

// ProposeTransition applies a runner transition proposal with its evidence.
// Refuses stop_latched, boot_mismatch, stale_generation, a wrong runner, a bad
// edge, missing evidence, paused (non-stopping edges), and an unequal replay
// (identity_conflict).
// Durable point: the attempts CAS, its event, the runtime_observations row and
// the tasks.state update in one committed transaction; the recorded message is
// returned. Proposals to terminal states or unknown are invalid_transition.
func (s *Store) ProposeTransition(ctx context.Context, fingerprint, session, selected string, m p.Message, evidence RuntimeEvidence) (p.Message, error) {
	if m.Kind != "transition" || m.ExpectedRevision == nil || !p.ValidID(m.Identity.AttemptID) {
		return p.Message{}, p.Malformed
	}
	// Exit evidence is checked against the sink: hold the attempt's append lock
	// (before the store lock, as AppendStream does) so no append interleaves.
	unlock := s.sinks().Serialize(m.Identity.AttemptID)
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if terminalState(m.To) || m.To == p.Unknown {
		return p.Message{}, p.InvalidTransition
	}
	if evidence.Kind == "" && m.To == p.Stopping {
		evidence.Kind = "stop"
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return p.Message{}, err
	}
	defer tx.Rollback()
	who, sess, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return p.Message{}, err
	}
	d, err := runnerDispatch(ctx, tx, who, m.Identity.AttemptID)
	if err != nil {
		return p.Message{}, err
	}
	identity := d.Assignment.Identity
	if r := p.CheckSession(m, identity, selected); r != p.OK {
		return p.Message{}, r
	}
	// The dispatch's own identity is historical; only the current generation may
	// change state, whatever the edge.
	if identity.Generation != s.meta.Generation {
		return p.Message{}, p.StaleGeneration
	}
	requestSHA, err := requestDigest(struct {
		Message  p.Message       `json:"message"`
		Evidence RuntimeEvidence `json:"evidence"`
	}{m, evidence})
	if err != nil {
		return p.Message{}, err
	}
	if response, found, err := retainedReceipt(ctx, tx, identity.AttemptID, ReceiptMessages, m.MessageID, requestSHA); err != nil {
		return p.Message{}, err
	} else if found {
		return p.Decode(response)
	}
	// A paused daemon admits no new work but still drains stops: dispatchAllowed
	// refuses paused inside this transaction for every non-stopping edge.
	if sess.RunnerBoot != d.Facts.RunnerBoot {
		return p.Message{}, p.BootMismatch
	}
	body, err := runtimeBody(runtimeRecord{MessageID: m.MessageID, Message: &m, Evidence: &evidence})
	if err != nil {
		return p.Message{}, err
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT message FROM events WHERE message_id=?", m.MessageID).Scan(&previous)
	if err == nil {
		old, decodeErr := p.Decode([]byte(previous))
		if decodeErr != nil {
			return p.Message{}, decodeErr
		}
		if r := p.CheckReplay(m, old, identity); r != p.Duplicate {
			return p.Message{}, r
		}
		var retained bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM runtime_observations WHERE attempt_id=? AND evidence_sha256=?)", identity.AttemptID, digest).Scan(&retained); err != nil {
			return p.Message{}, err
		}
		if !retained {
			return p.Message{}, p.IdentityConflict
		}
		return old, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return p.Message{}, err
	}
	var aliased bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM dispatch_acks WHERE message_id=?) OR EXISTS(SELECT 1 FROM control_messages WHERE message_id=?)", m.MessageID, m.MessageID).Scan(&aliased); err != nil {
		return p.Message{}, err
	}
	if aliased {
		return p.Message{}, p.IdentityConflict
	}
	state, revision, err := attemptState(ctx, tx, identity.AttemptID)
	if err != nil {
		return p.Message{}, err
	}
	if terminalState(state) {
		return p.Message{}, p.InvalidTransition
	}
	now := s.controlStamp()
	if m.To != p.Stopping {
		if err := expireGrants(ctx, tx, now.Wall.UnixMilli()); err != nil {
			return p.Message{}, err
		}
		if err := dispatchAllowed(ctx, tx, d.Request, now.Wall.UnixMilli()); err != nil {
			var refusal *g.Refusal
			if !errors.As(err, &refusal) {
				return p.Message{}, err
			}
			// Elapsed grant expiry stays sticky, including under a refused proposal.
			if commitErr := tx.Commit(); commitErr != nil {
				return p.Message{}, commitErr
			}
			return p.Message{}, err
		}
	}
	if err := s.transitionEvidence(ctx, tx, identity.TaskID, identity.AttemptID, m.To, evidence, now); err != nil {
		return p.Message{}, err
	}
	if r := p.CheckTransition(m, identity, state, revision); r != p.OK {
		return p.Message{}, r
	}
	result, err := tx.ExecContext(ctx, "UPDATE attempts SET state=?,revision=revision+1 WHERE id=? AND revision=?", m.To, identity.AttemptID, revision)
	if err != nil {
		return p.Message{}, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return p.Message{}, p.RevisionConflict
	}
	if err := record(ctx, tx, m, revision+1); err != nil {
		return p.Message{}, err
	}
	switch m.To {
	case p.ResultPending:
		_, err = tx.ExecContext(ctx, "UPDATE tasks SET state='verifying' WHERE id=?", identity.TaskID)
	case p.Stopping:
		_, err = tx.ExecContext(ctx, "UPDATE tasks SET state='reconciling' WHERE id=?", identity.TaskID)
	}
	if err != nil {
		return p.Message{}, err
	}
	if _, err := recordRuntime(ctx, tx, identity.AttemptID, evidence.Kind, sess.RunnerBoot, s.meta.DaemonBoot, body, now.Wall.UnixMilli()); err != nil {
		return p.Message{}, err
	}
	if err := recordReceipt(ctx, tx, identity.AttemptID, ReceiptMessages, m.MessageID, requestSHA, m, sess.RunnerBoot, s.meta.DaemonBoot, now.Wall.UnixMilli()); err != nil {
		return p.Message{}, err
	}
	if err := s.step("before_transition_commit"); err != nil {
		return p.Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return p.Message{}, err
	}
	if err := s.step("after_transition_commit"); err != nil {
		return p.Message{}, err
	}
	return m, nil
}

// IssueLease is RecordControlLease without owner(): the daemon builds the
// lease_reply (validity 20000 ms, nonce-derived message id), runs p.CheckLease
// with the contract timing (drift 2000 ms, termination 5000 ms, the prior
// lease's runner cutoff, nonce activity) using sent_ms as the runner-domain
// baseline. The runner checks actual reply delay. Issuance uses the daemon's
// separate clock through issueLeaseTx with a 7 s margin. Durable point: the control_leases row
// committed before the reply is returned; a refusal is fenced (controlFence)
// and committed before the error (BootMismatch, c.ErrFenced, DelayedReply,
// NonceMismatch, paused) is returned. The same nonce returns the retained reply.
func (s *Store) IssueLease(ctx context.Context, fingerprint, session, dispatchKey, selected string, request p.Message) (c.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.Kind != "lease_request" || request.SentMS == nil || !p.ValidID(request.Nonce) || !p.ValidID(request.MessageID) {
		return c.Lease{}, p.Malformed
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return c.Lease{}, err
	}
	defer tx.Rollback()
	who, sess, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return c.Lease{}, err
	}
	d, err := dispatchOwnedBy(ctx, tx, who, dispatchKey)
	if err != nil {
		return c.Lease{}, err
	}
	identity := d.Assignment.Identity
	fence := func(reason string, refusal error) (c.Lease, error) {
		if err := controlFence(ctx, tx, request, reason); err != nil {
			return c.Lease{}, err
		}
		if err := tx.Commit(); err != nil {
			return c.Lease{}, err
		}
		return c.Lease{}, refusal
	}
	if r := p.CheckSession(request, identity, selected); r != p.OK {
		if r == p.StaleGeneration || r == p.StaleAttempt {
			return fence(string(r), r)
		}
		return c.Lease{}, r
	}
	if identity.Generation != s.meta.Generation {
		return fence(string(p.StaleGeneration), p.StaleGeneration)
	}
	// A retained nonce answers with its original reply before any timing check:
	// a lost reply is replayed, never re-timed, and never extended.
	var retained string
	err = tx.QueryRowContext(ctx, "SELECT body FROM control_leases WHERE nonce=?", request.Nonce).Scan(&retained)
	if err == nil {
		var old c.Lease
		if err := decodeControl(retained, &old); err != nil {
			return c.Lease{}, err
		}
		if !validControlLease(old) {
			return c.Lease{}, g.Deny("corrupt_record", "lease")
		}
		if old.DispatchID != dispatchKey || !reflect.DeepEqual(old.Request, request) {
			return c.Lease{}, p.IdentityConflict
		}
		return old, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c.Lease{}, err
	}
	if sess.Paused {
		return fence("paused", g.Deny("paused", "lease"))
	}
	validity := LeaseValidityMS
	reply := p.Message{Version: p.FencedVersion, Kind: "lease_reply", MessageID: dispatchID(request.Nonce, "lease-reply"), Identity: identity, Nonce: request.Nonce, RunnerBoot: request.RunnerBoot, DaemonBoot: request.DaemonBoot, ValidityMS: &validity}
	// sent_ms and the prior cutoff belong to one runner boot's monotonic
	// clock, not this daemon's wall clock. Use S as the validation baseline:
	// only the runner can check the actual reply arrival R against S and its
	// cutoff. issueLeaseTx records the independent daemon issuance/expiry stamp.
	timing := p.Timing{RunnerBoot: d.Facts.RunnerBoot, DaemonBoot: s.meta.DaemonBoot, ReceivedMS: *request.SentMS, DriftMS: SessionDriftMS, TerminationMS: SessionTerminationMS, NonceActive: true}
	if sess.RunnerBoot != request.RunnerBoot {
		timing.RunnerBoot = sess.RunnerBoot
	}
	prior, err := lastControlLease(ctx, tx, identity.AttemptID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c.Lease{}, err
	}
	if err == nil && prior.Request.Nonce != request.Nonce {
		stopBy := *prior.Request.SentMS + *prior.Reply.ValidityMS - SessionDriftMS - SessionTerminationMS
		timing.PriorStopByMS = &stopBy
	}
	var revoked bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM control_revocations WHERE nonce=?)", request.Nonce).Scan(&revoked); err != nil {
		return c.Lease{}, err
	}
	timing.NonceActive = !revoked
	if check := p.CheckLease(request, reply, identity, timing); check.Reason != p.OK {
		switch check.Reason {
		case p.BootMismatch, p.DelayedReply, p.NonceMismatch, p.StaleGeneration, p.StaleAttempt:
			return fence(string(check.Reason), check.Reason)
		}
		return c.Lease{}, check.Reason
	}
	lease, err := s.issueLeaseTx(ctx, tx, fingerprint, dispatchKey, selected, request, reply, leaseMargin)
	if !leaseCommitted(err) {
		return c.Lease{}, err
	}
	if hookErr := s.step("before_lease_commit"); hookErr != nil {
		return c.Lease{}, hookErr
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return c.Lease{}, commitErr
	}
	if hookErr := s.step("after_lease_commit"); hookErr != nil {
		return c.Lease{}, hookErr
	}
	if err != nil {
		return c.Lease{}, err
	}
	return lease, nil
}

// ReportTermination records runner termination evidence through
// observeTerminationTx. For stop_id == ExpiryStopID(nonce) it first latches the
// lease_expired cancel (actor "lease-clock"); for stop_id == LocalStopID(attempt,
// cause) with a LocalStopCauses cause it latches that cancel (actor
// "runner-local"). Durable point: the
// control_observations row; and, only when boundary.Quiescent and the attempt is
// stopping|unknown, the releaseDispatchTx release to cancelled|expired with the
// runner principal as actor, all in one committed transaction. Otherwise the
// attempt stays stopping|unknown with remote_work unknown and Released is false.
// Old-boot evidence is retained in runtime_observations (outcome retained), and
// an unconfirmed report (confirmed_process unknown) is retained without a
// control observation or release, leaving the cancel pending.
func (s *Store) ReportTermination(ctx context.Context, fingerprint, session, selected string, evidence c.Evidence, boundary BoundaryState) (TerminationReply, error) {
	m := evidence.Terminated
	if !p.ValidID(m.Identity.AttemptID) {
		return TerminationReply{}, p.Malformed
	}
	// Join append/finalize's attempt-lock -> store-lock order. A previously
	// admitted append must be durable before release makes backup pinning legal.
	unlock := s.sinks().Serialize(m.Identity.AttemptID)
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	// An unconfirmed containment report carries no observation timestamp; every
	// confirmed one must.
	unconfirmed := m.ConfirmedProcess == "unknown"
	if m.Kind != "terminated" || evidence.Measurement.Validate(!unconfirmed) != nil || !p.ValidID(m.StopID) {
		return TerminationReply{}, p.Malformed
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return TerminationReply{}, err
	}
	defer tx.Rollback()
	who, sess, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return TerminationReply{}, err
	}
	d, err := runnerDispatch(ctx, tx, who, m.Identity.AttemptID)
	if err != nil {
		return TerminationReply{}, err
	}
	identity := d.Assignment.Identity
	if r := p.CheckSession(m, identity, selected); r != p.OK {
		return TerminationReply{}, r
	}
	if identity.Generation != s.meta.Generation {
		return TerminationReply{}, p.StaleGeneration
	}
	requestSHA, err := requestDigest(struct {
		Termination c.Evidence    `json:"termination"`
		Boundary    BoundaryState `json:"boundary"`
	}{evidence, boundary})
	if err != nil {
		return TerminationReply{}, err
	}
	if response, found, err := retainedReceipt(ctx, tx, identity.AttemptID, ReceiptMessages, m.MessageID, requestSHA); err != nil {
		return TerminationReply{}, err
	} else if found {
		var reply TerminationReply
		if json.Unmarshal(response, &reply) != nil {
			return TerminationReply{}, g.Deny("corrupt_record", "receipt")
		}
		return reply, nil
	}
	body, err := runtimeBody(runtimeRecord{MessageID: m.MessageID, Termination: &evidence, Boundary: &boundary})
	if err != nil {
		return TerminationReply{}, err
	}
	now := s.controlStamp()
	commit := func(reply TerminationReply) (TerminationReply, error) {
		if _, err := recordRuntime(ctx, tx, identity.AttemptID, "terminated", m.RunnerBoot, m.DaemonBoot, body, now.Wall.UnixMilli()); err != nil {
			return TerminationReply{}, err
		}
		if err := recordReceipt(ctx, tx, identity.AttemptID, ReceiptMessages, m.MessageID, requestSHA, reply, m.RunnerBoot, m.DaemonBoot, now.Wall.UnixMilli()); err != nil {
			return TerminationReply{}, err
		}
		if err := s.step("before_termination_commit"); err != nil {
			return TerminationReply{}, err
		}
		if err := tx.Commit(); err != nil {
			return TerminationReply{}, err
		}
		if err := s.step("after_termination_commit"); err != nil {
			return TerminationReply{}, err
		}
		return reply, nil
	}
	if m.DaemonBoot != s.meta.DaemonBoot || m.RunnerBoot != d.Facts.RunnerBoot || sess.RunnerBoot != m.RunnerBoot {
		return commit(TerminationReply{Outcome: "retained"})
	}
	// Derived stop identities a runner may report without an owner stop: the
	// lease clock (ExpiryStopID of its last lease, cause lease_expired, actor
	// lease-clock) and its own local stops (LocalStopID of the attempt and one of
	// LocalStopCauses, actor runner-local). Each is latched on first sight so the
	// cancel is visible history; any other unknown stop id has no target and is
	// reconciliation_required below.
	lease, err := lastControlLease(ctx, tx, identity.AttemptID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return TerminationReply{}, err
	}
	derived := c.Request{ID: m.StopID, Kind: c.CancelAttempt, TaskID: identity.TaskID, AttemptID: identity.AttemptID}
	actor := ""
	if err == nil && m.StopID == execwire.ExpiryStopID(lease.Request.Nonce) {
		derived.Cause, actor = "lease_expired", "lease-clock"
	}
	for _, cause := range execwire.LocalStopCauses {
		if m.StopID == execwire.LocalStopID(identity.AttemptID, cause) {
			derived.Cause, actor = cause, "runner-local"
		}
	}
	if actor != "" {
		var latched bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM control_targets WHERE stop_id=? AND attempt_id=?)", m.StopID, identity.AttemptID).Scan(&latched); err != nil {
			return TerminationReply{}, err
		}
		if !latched {
			if _, err := latchStop(ctx, tx, actor, derived, now); err != nil {
				return TerminationReply{}, err
			}
		}
	}
	if unconfirmed {
		// The runner could not confirm containment (detached child, EPERM,
		// escape): the report is retained as history and the cancel stays
		// pending, with no control observation and no release, until a
		// confirmed report under a new message id arrives.
		var targetBody string
		err := tx.QueryRowContext(ctx, "SELECT body FROM control_targets WHERE stop_id=? AND attempt_id=?", m.StopID, identity.AttemptID).Scan(&targetBody)
		if errors.Is(err, sql.ErrNoRows) {
			return TerminationReply{}, p.ReconciliationRequired
		}
		if err != nil {
			return TerminationReply{}, err
		}
		var target c.Target
		if err := decodeControl(targetBody, &target); err != nil {
			return TerminationReply{}, err
		}
		if r := p.CheckSession(m, target.Cancel.Identity, selected); r != p.OK {
			return TerminationReply{}, r
		}
		if m.RunnerBoot != target.Cancel.RunnerBoot || m.DaemonBoot != target.Cancel.DaemonBoot {
			return TerminationReply{}, p.BootMismatch
		}
		if err := controlMessage(ctx, tx, m); err != nil {
			return TerminationReply{}, err
		}
		return commit(TerminationReply{Outcome: "observed"})
	}
	// The first observation of a stop is immutable. A later report under a new
	// message id that attests the same or stronger termination is an observation
	// revision: retained in runtime_observations, released on its own boundary.
	var target c.Target
	var retainedBody string
	err = tx.QueryRowContext(ctx, "SELECT body FROM control_observations WHERE stop_id=? AND attempt_id=?", m.StopID, identity.AttemptID).Scan(&retainedBody)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return TerminationReply{}, err
	}
	var retained c.Evidence
	if err == nil {
		if err := decodeControl(retainedBody, &retained); err != nil {
			return TerminationReply{}, err
		}
	}
	if err == nil && retained.Terminated.MessageID != m.MessageID {
		strongest, err := s.strongestTermination(ctx, tx, identity.AttemptID, m.StopID, retained)
		if err != nil {
			return TerminationReply{}, err
		}
		if !terminationRevision(strongest, evidence) {
			return TerminationReply{}, p.IdentityConflict
		}
		var targetBody string
		if err := tx.QueryRowContext(ctx, "SELECT body FROM control_targets WHERE stop_id=? AND attempt_id=?", m.StopID, identity.AttemptID).Scan(&targetBody); err != nil {
			return TerminationReply{}, err
		}
		if err := decodeControl(targetBody, &target); err != nil {
			return TerminationReply{}, err
		}
		if r := p.CheckSession(m, target.Cancel.Identity, selected); r != p.OK {
			return TerminationReply{}, r
		}
		if m.RunnerBoot != target.Cancel.RunnerBoot || m.DaemonBoot != target.Cancel.DaemonBoot {
			return TerminationReply{}, p.BootMismatch
		}
		if err := controlMessage(ctx, tx, m); err != nil {
			return TerminationReply{}, err
		}
	} else {
		target, err = s.observeTerminationTx(ctx, tx, selected, evidence)
		if errors.Is(err, sql.ErrNoRows) {
			return TerminationReply{}, p.ReconciliationRequired
		}
		if err != nil {
			return TerminationReply{}, err
		}
	}
	state, revision, err := attemptState(ctx, tx, identity.AttemptID)
	if err != nil {
		return TerminationReply{}, err
	}
	released := d.Released
	if !released && boundary.settled() && m.RemoteWork == "quiescent" && (m.ConfirmedProcess == "terminated" || m.ConfirmedProcess == "not_started") && (state == p.Stopping || state == p.Unknown) {
		stop, err := loadStop(ctx, tx, m.StopID)
		if err != nil {
			return TerminationReply{}, err
		}
		to := p.Cancelled
		if stop.Request.Cause == "lease_expired" {
			to = p.Expired
		}
		proof := Reconciliation{DispatchID: target.DispatchID, Identity: identity, ExpectedRevision: revision, To: to, ConfirmedProcess: m.ConfirmedProcess, RemoteWork: "quiescent", LaunchFenced: true, ArtifactsPreserved: true, EvidenceDigest: m.EvidenceDigest}
		if err := s.releaseDispatchTx(ctx, tx, fingerprint, proof); err != nil {
			return TerminationReply{}, err
		}
		released = true
	}
	return commit(TerminationReply{Outcome: "observed", Released: released})
}

// RecordUsage upserts boundary receipts into attempt_usage. Durable point: the
// upsert commit; the call is replayable by message_id and idempotent per
// request_id. A terminal receipt supersedes its reservation; a reservation
// never overwrites a retained terminal receipt.
func (s *Store) RecordUsage(ctx context.Context, fingerprint, session string, usage UsageReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if usage.Version != execwire.Version || !p.ValidID(usage.MessageID) {
		return p.Malformed
	}
	for _, r := range usage.Receipts {
		if r.RequestID == "" || len(r.RequestID) > 128 || r.Status < 0 || r.PromptTokens < 0 || r.CompletionTokens < 0 || r.BytesIn < 0 || r.BytesOut < 0 || r.StartedMS < 0 || r.EndedMS < 0 {
			return p.Malformed
		}
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	who, sess, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return err
	}
	d, err := runnerDispatch(ctx, tx, who, usage.Identity.AttemptID)
	if err != nil {
		return err
	}
	if usage.Identity != d.Assignment.Identity {
		return p.StaleAttempt
	}
	if usage.Identity.Generation != s.meta.Generation {
		return p.StaleGeneration
	}
	requestSHA, err := requestDigest(usage)
	if err != nil {
		return err
	}
	if _, found, err := retainedReceipt(ctx, tx, usage.Identity.AttemptID, ReceiptUsage, usage.MessageID, requestSHA); err != nil || found {
		return err
	}
	for _, r := range usage.Receipts {
		body, err := json.Marshal(r)
		if err != nil {
			return p.Malformed
		}
		if len(body) > 8192 {
			return p.Oversized
		}
		var old string
		err = tx.QueryRowContext(ctx, "SELECT body FROM attempt_usage WHERE attempt_id=? AND request_id=?", usage.Identity.AttemptID, r.RequestID).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if old == string(body) {
				continue
			}
			var retained UsageReceipt
			if json.Unmarshal([]byte(old), &retained) != nil {
				return g.Deny("corrupt_record", "usage")
			}
			// A terminal receipt is immutable per request id; only a reservation
			// may be superseded, and only by a later reservation or its terminal.
			if retained.Terminal {
				if r.Terminal {
					return p.IdentityConflict
				}
				continue
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO attempt_usage VALUES(?,?,?) ON CONFLICT(attempt_id,request_id) DO UPDATE SET body=excluded.body", usage.Identity.AttemptID, r.RequestID, string(body)); err != nil {
			return err
		}
	}
	if err := recordReceipt(ctx, tx, usage.Identity.AttemptID, ReceiptUsage, usage.MessageID, requestSHA, map[string]string{"outcome": "recorded"}, sess.RunnerBoot, s.meta.DaemonBoot, time.Now().UnixMilli()); err != nil {
		return err
	}
	if err := s.step("before_usage_commit"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.step("after_usage_commit")
}

// RunnerReceipt reads the retained receipt for (attempt, route, messageID):
// sql.ErrNoRows when none exists, identity_conflict when the retained request
// differs from request, else the retained response bytes. Durable point: none.
func (s *Store) RunnerReceipt(ctx context.Context, fingerprint, session, attemptID, route, messageID string, request any) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(attemptID) || !p.ValidID(messageID) || route == "" {
		return nil, p.Malformed
	}
	digest, err := requestDigest(request)
	if err != nil {
		return nil, err
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	who, _, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return nil, err
	}
	if _, err := runnerDispatch(ctx, tx, who, attemptID); err != nil {
		return nil, err
	}
	response, found, err := retainedReceipt(ctx, tx, attemptID, route, messageID, digest)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, sql.ErrNoRows
	}
	return response, nil
}

// RecordRunnerReceipt retains the receipt of a request another store method
// applied in its own transaction (assignment acknowledgement). Durable point:
// the committed row; a crash before it leaves the applied state replayable by
// its own identity and the next identical request re-records the receipt.
func (s *Store) RecordRunnerReceipt(ctx context.Context, fingerprint, session, attemptID, route, messageID string, request, response any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(attemptID) || !p.ValidID(messageID) || route == "" {
		return p.Malformed
	}
	digest, err := requestDigest(request)
	if err != nil {
		return err
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	who, sess, err := s.runnerSession(ctx, tx, fingerprint, session)
	if err != nil {
		return err
	}
	if _, err := runnerDispatch(ctx, tx, who, attemptID); err != nil {
		return err
	}
	if err := recordReceipt(ctx, tx, attemptID, route, messageID, digest, response, sess.RunnerBoot, s.meta.DaemonBoot, time.Now().UnixMilli()); err != nil {
		return err
	}
	if err := s.step("before_receipt_commit"); err != nil {
		return err
	}
	return tx.Commit()
}

// AttemptUsage reads the retained receipts of one attempt in request order.
// Durable point: none (read).
func (s *Store) AttemptUsage(ctx context.Context, attemptID string) ([]UsageReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(attemptID) {
		return nil, g.Deny("malformed", "attempt_id")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT body FROM attempt_usage WHERE attempt_id=? ORDER BY request_id", attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsageReceipt{}
	for rows.Next() {
		var body string
		var r UsageReceipt
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(body), &r) != nil {
			return nil, g.Deny("corrupt_record", "usage")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
