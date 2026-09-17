package store

// Owner workflow seam (docs/decisions/0002 §5, /api/v1 routes under owner mTLS).
// S4 (#22) replaces the bodies in this file only; until then every method returns
// ErrNotImplemented. actor is always the authenticated owner credential
// fingerprint. Owner message_id values are the domain intent keys, so a duplicate
// request replays the same result even when its owner_commands receipt was lost.

import (
	"context"
	"encoding/json"

	r "github.com/korallis/letmecook/internal/review"
	sc "github.com/korallis/letmecook/internal/scheduler"
	v "github.com/korallis/letmecook/internal/verification"
	p "github.com/korallis/letmecook/schemas/execution"
)

// Criterion is one acceptance criterion of a task brief.
type Criterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// TaskBrief mirrors one task_briefs row. TaskID is the create request's
// message_id; BriefSHA256/PlanSHA256 are the revision digests grants bind.
type TaskBrief struct {
	TaskID      string      `json:"task_id"`
	Revision    int64       `json:"revision"`
	Repository  string      `json:"repository"`
	BaseCommit  string      `json:"base_commit"`
	Brief       string      `json:"brief"`
	Criteria    []Criterion `json:"criteria,omitempty"`
	Paths       []string    `json:"paths,omitempty"`
	Operations  []string    `json:"operations,omitempty"`
	BriefSHA256 string      `json:"brief_sha256"`
	PlanSHA256  string      `json:"plan_sha256"`
	Actor       string      `json:"actor"`
	CreatedMS   int64       `json:"created_ms"`
}

// AttemptSummary is one attempt of a task as the owner API lists it.
type AttemptSummary struct {
	Identity     p.Identity     `json:"identity"`
	State        p.AttemptState `json:"state"`
	Revision     int64          `json:"revision"`
	DispatchID   string         `json:"dispatch_id,omitempty"`
	Acknowledged bool           `json:"acknowledged"`
	Released     bool           `json:"released"`
}

// Task is the composed GET /api/v1/tasks/{id} view.
type Task struct {
	Brief        TaskBrief        `json:"brief"`
	Phase        p.TaskState      `json:"phase"`
	GrantHead    string           `json:"grant_head,omitempty"`
	Attempts     []AttemptSummary `json:"attempts,omitempty"`
	Head         ResultHead       `json:"head,omitzero"`
	Verification v.Report         `json:"verification,omitzero"`
	Review       r.Current        `json:"review,omitzero"`
}

// DaemonState mirrors the daemon_state singleton.
type DaemonState struct {
	Paused    bool   `json:"paused"`
	Reason    string `json:"reason,omitempty"`
	UpdatedMS int64  `json:"updated_ms"`
}

// Job mirrors one jobs row. State is queued|running|succeeded|failed; a job
// still running at daemon start becomes failed with Error "daemon_restart".
type Job struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	SubjectID  string `json:"subject_id,omitempty"`
	State      string `json:"state"`
	DaemonBoot string `json:"daemon_boot"`
	CreatedMS  int64  `json:"created_ms"`
	StartedMS  int64  `json:"started_ms,omitempty"`
	FinishedMS int64  `json:"finished_ms,omitempty"`
	Result     string `json:"result_ref,omitempty"`
	Error      string `json:"error,omitempty"`
}

// OwnerCommand mirrors one owner_commands receipt: the retained response for a
// message_id, served on replay with Idempotent-Replay: true. A different
// request_sha256 under the same message_id is identity_conflict.
type OwnerCommand struct {
	MessageID     string          `json:"message_id"`
	Generation    string          `json:"generation"`
	PrincipalID   string          `json:"principal_id"`
	Route         string          `json:"route"`
	RequestSHA256 string          `json:"request_sha256"`
	Status        int             `json:"status"`
	Response      json.RawMessage `json:"response,omitempty"`
	CreatedMS     int64           `json:"created_ms"`
}

// GatewayProfile mirrors one gateway_profiles row: the operator's gateway-config-v1
// body keyed by its digest, which authority routes cite as router_build.
type GatewayProfile struct {
	Digest    string          `json:"digest"`
	Body      json.RawMessage `json:"body,omitempty"`
	CreatedMS int64           `json:"created_ms"`
}

// CreateTask records a brief. Durable point: the tasks row (ready) and the
// task_briefs row in one committed transaction; brief.TaskID is the intent key,
// so an identical replay returns the same Task and a different body is
// identity_conflict. The repository must be registered.
func (s *Store) CreateTask(ctx context.Context, actor string, brief TaskBrief) (Task, error) {
	return Task{}, ErrNotImplemented
}

// Task composes one task: brief, phase, grant head, attempts, result head,
// current verification and review. Durable point: none (read).
func (s *Store) Task(ctx context.Context, id string) (Task, error) {
	return Task{}, ErrNotImplemented
}

// Tasks pages tasks by ID after the cursor (limit 1..readapi.MaxItems).
// Durable point: none (read).
func (s *Store) Tasks(ctx context.Context, after string, limit int) ([]Task, error) {
	return nil, ErrNotImplemented
}

// Eligibility reads the latest revision of one published eligibility record.
// Durable point: none (read); reading never admits.
func (s *Store) Eligibility(ctx context.Context, id string) (sc.Eligibility, error) {
	return sc.Eligibility{}, ErrNotImplemented
}

// SetPaused flips the daemon_state singleton for POST /api/v1/daemon/pause and
// /resume. messageID is the owner intent key (replay returns the same state; a
// different body under the same key is identity_conflict). Durable point: the
// committed update; while paused, Dispatch, Delivery, IssueLease,
// ProposeTransition and FinalizeAttempt refuse paused. Resuming a store whose
// latest restore_history entry is newer than its last resume requires
// confirmSourceFenced, the owner's acknowledgement that the source daemon is
// stopped, every runner restarted and facts re-imported; otherwise refused.
func (s *Store) SetPaused(ctx context.Context, actor, messageID string, paused bool, reason string, confirmSourceFenced bool) (DaemonState, error) {
	return DaemonState{}, ErrNotImplemented
}

// Paused reads the daemon_state singleton. Durable point: none (read).
func (s *Store) Paused(ctx context.Context) (DaemonState, error) {
	return DaemonState{}, ErrNotImplemented
}

// Job reads one jobs row. Durable point: none (read).
func (s *Store) Job(ctx context.Context, id string) (Job, error) {
	return Job{}, ErrNotImplemented
}

// PutJob inserts or advances a job. Durable point: the committed row; identity
// columns are immutable and a terminal job cannot change (schema triggers).
func (s *Store) PutJob(ctx context.Context, job Job) (Job, error) {
	return Job{}, ErrNotImplemented
}

// RecordOwnerCommand writes the receipt for an owner mutation after its domain
// commit. Durable point: the committed owner_commands row; it is immutable.
func (s *Store) RecordOwnerCommand(ctx context.Context, command OwnerCommand) error {
	return ErrNotImplemented
}

// OwnerCommand reads a retained receipt by message_id. Durable point: none (read).
func (s *Store) OwnerCommand(ctx context.Context, messageID string) (OwnerCommand, error) {
	return OwnerCommand{}, ErrNotImplemented
}

// PutGatewayProfile retains a validated gateway-config-v1 body under its digest.
// Durable point: the committed gateway_profiles row; the same digest is a no-op.
func (s *Store) PutGatewayProfile(ctx context.Context, actor string, body []byte) (GatewayProfile, error) {
	return GatewayProfile{}, ErrNotImplemented
}
