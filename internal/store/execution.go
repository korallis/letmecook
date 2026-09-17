package store

// Execution-channel seam (docs/decisions/0002 §4). Each method is the store half
// of one runner-mTLS /x/v1 route. S1 (#115) replaces the bodies in this file
// only, reusing issueLeaseTx, observeTerminationTx and releaseDispatchTx from
// execution_tx.go; until then every method returns ErrNotImplemented.
//
// Record types are defined here, inside package store, with the contract's wire
// field names so internal/execwire can marshal them directly; S1/S4 may alias
// them to execwire types. Wire rule: no nullable fields. Optional structs use
// omitzero, optional lists omitempty, and a list that is sent must be non-nil.
// fingerprint is always the authenticated runner credential from the TLS
// connection; session is the X-Gaffer-Session header; selected is the ALPN
// protocol read from the connection, never a body field.

import (
	"context"
	"encoding/json"

	c "github.com/korallis/letmecook/internal/control"
	p "github.com/korallis/letmecook/schemas/execution"
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
// Mode is "normal" or "recovery_only" (evidence accepted, no launch).
type SessionRecord struct {
	SessionID         string `json:"session_id"`
	Generation        string `json:"generation"`
	DaemonBoot        string `json:"daemon_boot"`
	DaemonFingerprint string `json:"daemon_fingerprint"`
	RunnerID          string `json:"runner_id"`
	Mode              string `json:"mode"`
	DriftMS           int64  `json:"drift_ms"`
	TerminationMS     int64  `json:"termination_ms"`
	LeaseValidityMS   int64  `json:"lease_validity_ms"`
	RenewEveryMS      int64  `json:"renew_every_ms"`
	Paused            bool   `json:"paused"`
}

// StreamWatermark is the runstream sink position: records acknowledged through
// and the digest of everything up to it.
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
// runtime_observations. Kind selects the populated fields: launch_intent
// (workspace, boundary_port, guardian_pid, nonce), launched (pid, pgid,
// start_unix_ns) and exit (code, pgid_empty, observed_unix_ns, stream_through).
type RuntimeEvidence struct {
	Kind           string `json:"kind"`
	Workspace      string `json:"workspace,omitempty"`
	BoundaryPort   int    `json:"boundary_port,omitempty"`
	GuardianPID    int    `json:"guardian_pid,omitempty"`
	Nonce          string `json:"nonce,omitempty"`
	PID            int    `json:"pid,omitempty"`
	PGID           int    `json:"pgid,omitempty"`
	StartUnixNS    int64  `json:"start_unix_ns,omitempty"`
	Code           int    `json:"code,omitempty"`
	PGIDEmpty      bool   `json:"pgid_empty,omitempty"`
	ObservedUnixNS int64  `json:"observed_unix_ns,omitempty"`
	StreamThrough  int64  `json:"stream_through,omitempty"`
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

// RunnerSession binds an authenticated runner hello to a session. Durable point:
// the runner_sessions row (mode normal|recovery_only) committed before the reply;
// the same message_id returns the same row. It is the store half of
// POST /x/v1/session; reconcile.OnHello runs after it in the handler.
func (s *Store) RunnerSession(ctx context.Context, fingerprint string, hello HelloRecord) (SessionRecord, error) {
	return SessionRecord{}, ErrNotImplemented
}

// ExecutionState is the read behind GET /x/v1/state: attempt state and revision,
// acknowledgement/release, last lease, stream watermark, receipt, head, pending
// stop targets and the paused flag. Durable point: none (read).
func (s *Store) ExecutionState(ctx context.Context, fingerprint, session, dispatchID string) (ExecutionState, error) {
	return ExecutionState{}, ErrNotImplemented
}

// PendingCancels lists the retained cancel outbox messages (control_targets) for
// the runner's non-terminal attempts that have no termination observation yet.
// Durable point: none (read); delivery is not acknowledgement.
func (s *Store) PendingCancels(ctx context.Context, fingerprint, session string) ([]p.Message, error) {
	return nil, ErrNotImplemented
}

// ProposeTransition applies a runner transition proposal with its evidence.
// Refuses stop_latched, boot_mismatch, stale_generation, a wrong runner, a bad
// edge, missing evidence, paused, and an unequal replay (identity_conflict).
// Durable point: the attempts CAS, its event, the runtime_observations row and
// the tasks.state update in one committed transaction; the recorded message is
// returned. Proposals to terminal states or unknown are invalid_transition.
func (s *Store) ProposeTransition(ctx context.Context, fingerprint, session, selected string, m p.Message, evidence RuntimeEvidence) (p.Message, error) {
	return p.Message{}, ErrNotImplemented
}

// IssueLease is RecordControlLease without owner(): the daemon builds the
// lease_reply (validity 20000 ms, in_reply_to), runs p.CheckLease with the
// contract timing, and records issuance through issueLeaseTx. Durable point:
// the control_leases row committed before the reply is returned; a refusal is
// fenced (controlFence) and committed before the error (BootMismatch,
// c.ErrFenced, DelayedReply) is returned. The same nonce returns the retained reply.
func (s *Store) IssueLease(ctx context.Context, fingerprint, session, dispatchID, selected string, request p.Message) (c.Lease, error) {
	return c.Lease{}, ErrNotImplemented
}

// ReportTermination records runner termination evidence through
// observeTerminationTx. For stop_id == ExpiryStopID(nonce) it first latches the
// lease_expired cancel (actor "lease-clock"). Durable point: the
// control_observations row; and, only when boundary.Quiescent and the attempt is
// stopping|unknown, the releaseDispatchTx release to cancelled|expired with the
// runner principal as actor, all in one committed transaction. Otherwise the
// attempt stays stopping|unknown with remote_work unknown and Released is false.
// Old-boot evidence is retained in runtime_observations (outcome retained).
func (s *Store) ReportTermination(ctx context.Context, fingerprint, session, selected string, evidence c.Evidence, boundary BoundaryState) (TerminationReply, error) {
	return TerminationReply{}, ErrNotImplemented
}

// RecordUsage upserts boundary receipts into attempt_usage. Durable point: the
// upsert commit; the call is replayable by message_id and idempotent per request_id.
func (s *Store) RecordUsage(ctx context.Context, fingerprint, session string, usage UsageReport) error {
	return ErrNotImplemented
}
