package store

// Owner workflow seam (docs/decisions/0002 §5, /api/v1 routes under owner mTLS).
// actor is always the authenticated owner credential
// fingerprint. Owner message_id values are the domain intent keys, so a duplicate
// request replays the same result even when its owner_commands receipt was lost.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/closedjson"
	c "github.com/korallis/letmecook/internal/control"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/workflow"
	a "github.com/korallis/letmecook/schemas/readapi"

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
	TaskID      string          `json:"task_id"`
	Revision    int64           `json:"revision"`
	Repository  string          `json:"repository"`
	BaseCommit  string          `json:"base_commit"`
	Brief       string          `json:"brief"`
	Criteria    []Criterion     `json:"criteria,omitempty"`
	Paths       []string        `json:"paths,omitempty"`
	Operations  []string        `json:"operations,omitempty"`
	Harness     string          `json:"harness,omitempty"`
	Settings    json.RawMessage `json:"settings,omitempty"`
	BriefSHA256 string          `json:"brief_sha256"`
	PlanSHA256  string          `json:"plan_sha256"`
	Actor       string          `json:"actor"`
	CreatedMS   int64           `json:"created_ms"`
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
	Verification v.Summary        `json:"verification,omitzero"`
	Review       r.Current        `json:"review,omitzero"`
}

// DaemonState mirrors the daemon_state singleton.
type DaemonState struct {
	Paused         bool     `json:"paused"`
	Reason         string   `json:"reason,omitempty"`
	UpdatedMS      int64    `json:"updated_ms"`
	ClearedLatches []string `json:"cleared_latches,omitempty"`
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

// OwnerContext marks an authenticated owner request for a commit-time check.
// The transport, not any JSON field, supplies the credential fingerprint.
func OwnerContext(ctx context.Context, fingerprint string) context.Context {
	return context.WithValue(ctx, ownerContextKey{}, fingerprint)
}

type ownerContextKey struct{}
type decisionContextKey struct{}

func workflowOwner(ctx context.Context, tx *sql.Tx) error {
	if fp, ok := ctx.Value(ownerContextKey{}).(string); ok {
		who, err := principal(ctx, tx, fp)
		if err != nil {
			return err
		}
		if who.Role != "owner" || !who.Enabled || who.Revoked {
			return i.Denied
		}
	}
	return nil
}

// DecisionContext binds a validated decision to the same approval transaction.
func DecisionContext(ctx context.Context, decision sc.Decision) context.Context {
	return context.WithValue(ctx, decisionContextKey{}, decision)
}
func workflowDecisionTx(ctx context.Context, tx *sql.Tx, grant g.Grant) error {
	d, ok := ctx.Value(decisionContextKey{}).(sc.Decision)
	if !ok {
		return nil
	}
	if err := d.Validate(); err != nil {
		return err
	}
	digest, err := sc.Digest(d)
	if err != nil {
		return err
	}
	if grant.TaskID != d.Assessment.TaskID || grant.Envelope.RouteDecision != (g.Revision{Number: d.Revision, SHA256: digest}) {
		return g.Deny("stale_decision", "approval")
	}
	body, _ := json.Marshal(d)
	duplicate, err := identicalRecord(ctx, tx, "SELECT body FROM execution_decisions WHERE grant_id=?", grant.ID, body)
	if err != nil || duplicate {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO execution_decisions VALUES(?,?,?)", grant.ID, string(body), digest)
	return err
}

// RetainExecutionDecision repairs an interrupted response on the pre-integration
// seams branch. Integrated approval calls workflowDecisionTx before its commit.
func (s *Store) RetainExecutionDecision(ctx context.Context, actor, grantID string, d sc.Decision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = owner(ctx, tx, actor); err != nil {
		return err
	}
	grant, err := loadGrant(ctx, tx, grantID)
	if err != nil {
		return err
	}
	if err = workflowDecisionTx(DecisionContext(ctx, d), tx, grant); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ExecutionDecision(ctx context.Context, grantID string) (sc.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return sc.Decision{}, err
	}
	defer tx.Rollback()
	var raw, digest string
	var d sc.Decision
	err = tx.QueryRowContext(ctx, "SELECT body,sha256 FROM execution_decisions WHERE grant_id=?", grantID).Scan(&raw, &digest)
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal([]byte(raw), &d); err != nil {
		return d, err
	}
	got, err := sc.Digest(d)
	if err != nil || got != digest {
		return d, g.Deny("corrupt_record", "decision")
	}
	return d, d.Validate()
}
func inputBrief(b TaskBrief) workflow.TaskInput {
	cs := make([]workflow.Criterion, len(b.Criteria))
	for n, c := range b.Criteria {
		cs[n] = workflow.Criterion{ID: c.ID, Text: c.Text}
	}
	return workflow.TaskInput{Version: workflow.Version, MessageID: b.TaskID, Repository: b.Repository, BaseCommit: b.BaseCommit, Brief: b.Brief, Criteria: cs, Paths: b.Paths, Operations: b.Operations, Harness: b.Harness, Settings: b.Settings}
}

// BriefInput is the owner-side canonical brief view behind grant building; the
// runner-facing TaskInput in execution.go serves GET /x/v1/input.
func (s *Store) BriefInput(ctx context.Context, id string) (workflow.TaskInput, error) {
	t, err := s.Task(ctx, id)
	return inputBrief(t.Brief), err
}
func (s *Store) CreateTask(ctx context.Context, actor string, brief TaskBrief) (Task, error) {
	in, err := workflow.Normalize(inputBrief(brief))
	if err != nil {
		return Task{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	if err = owner(ctx, tx, actor); err != nil {
		return Task{}, err
	}
	who, err := principal(ctx, tx, actor)
	if err != nil {
		return Task{}, err
	}
	old, err := s.taskTx(ctx, tx, brief.TaskID, s.meta.Generation)
	if err == nil {
		if !reflect.DeepEqual(inputBrief(old.Brief), in) || old.Brief.Actor != who.ID {
			return Task{}, g.Deny("identity_conflict", "task")
		}
		// Creation replays the immutable creation view, not later attempt state.
		return Task{Brief: old.Brief, Phase: "ready"}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Task{}, err
	}
	var taskExists bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tasks WHERE id=?)", brief.TaskID).Scan(&taskExists); err != nil {
		return Task{}, err
	}
	if taskExists {
		return Task{}, g.Deny("identity_conflict", "task")
	}
	profile, err := repositoryProfile(ctx, tx, in.Repository)
	if err != nil {
		return Task{}, err
	}
	if profile.Base.Commit != in.BaseCommit {
		return Task{}, g.Deny("repository_ineligible", "base_commit")
	}
	if err = profile.CheckChanges(in.Paths); err != nil {
		return Task{}, err
	}
	brief = TaskBrief{TaskID: in.MessageID, Revision: 1, Repository: in.Repository, BaseCommit: in.BaseCommit, Brief: in.Brief, Paths: in.Paths, Operations: in.Operations, Harness: in.Harness, Settings: in.Settings, Actor: who.ID, CreatedMS: time.Now().UnixMilli()}
	for _, c := range in.Criteria {
		brief.Criteria = append(brief.Criteria, Criterion{ID: c.ID, Text: c.Text})
	}
	brief.BriefSHA256, brief.PlanSHA256 = workflow.TaskDigests(in)
	if _, err = tx.ExecContext(ctx, "INSERT INTO tasks VALUES(?,'ready')", brief.TaskID); err != nil {
		return Task{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO task_briefs(task_id,revision,repository_id,base_commit,brief,criteria,paths,operations,brief_sha256,plan_sha256,actor,created_ms,harness,settings) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", brief.TaskID, brief.Revision, brief.Repository, brief.BaseCommit, brief.Brief, controlJSON(brief.Criteria), controlJSON(brief.Paths), controlJSON(brief.Operations), brief.BriefSHA256, brief.PlanSHA256, brief.Actor, brief.CreatedMS, brief.Harness, string(brief.Settings)); err != nil {
		return Task{}, err
	}
	if err = tx.Commit(); err != nil {
		return Task{}, err
	}
	return Task{Brief: brief, Phase: "ready"}, nil
}
func (s *Store) taskTx(ctx context.Context, tx *sql.Tx, id, generation string) (Task, error) {
	var t Task
	var criteria, paths, operations, settings string
	err := tx.QueryRowContext(ctx, `SELECT b.task_id,b.revision,b.repository_id,b.base_commit,b.brief,b.criteria,b.paths,b.operations,b.brief_sha256,b.plan_sha256,b.actor,b.created_ms,t.state,b.harness,b.settings FROM task_briefs b JOIN tasks t ON t.id=b.task_id WHERE b.task_id=?`, id).Scan(&t.Brief.TaskID, &t.Brief.Revision, &t.Brief.Repository, &t.Brief.BaseCommit, &t.Brief.Brief, &criteria, &paths, &operations, &t.Brief.BriefSHA256, &t.Brief.PlanSHA256, &t.Brief.Actor, &t.Brief.CreatedMS, &t.Phase, &t.Brief.Harness, &settings)
	if err != nil {
		return t, err
	}
	t.Brief.Settings = json.RawMessage(settings)
	for _, pair := range []struct {
		raw string
		dst any
	}{{criteria, &t.Brief.Criteria}, {paths, &t.Brief.Paths}, {operations, &t.Brief.Operations}} {
		if err = json.Unmarshal([]byte(pair.raw), pair.dst); err != nil {
			return t, err
		}
	}
	err = tx.QueryRowContext(ctx, "SELECT grant_id FROM execution_grant_heads WHERE task_id=?", id).Scan(&t.GrantHead)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return t, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.epoch,a.state,a.revision,COALESCE(d.id,''),EXISTS(SELECT 1 FROM dispatch_acks k WHERE k.dispatch_id=d.id),EXISTS(SELECT 1 FROM dispatch_releases r WHERE r.dispatch_id=d.id) FROM attempts a LEFT JOIN dispatches d ON d.attempt_id=a.id WHERE a.task_id=? ORDER BY a.epoch`, id)
	if err != nil {
		return t, err
	}
	for rows.Next() {
		a := AttemptSummary{Identity: p.Identity{TaskID: id, Generation: generation}}
		if err = rows.Scan(&a.Identity.AttemptID, &a.Identity.Epoch, &a.State, &a.Revision, &a.DispatchID, &a.Acknowledged, &a.Released); err != nil {
			rows.Close()
			return t, err
		}
		t.Attempts = append(t.Attempts, a)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return t, err
	}
	for n := range t.Attempts {
		if t.Attempts[n].DispatchID != "" {
			d, err := loadDispatch(ctx, tx, t.Attempts[n].DispatchID)
			if err != nil {
				return t, err
			}
			t.Attempts[n].Identity = d.Assignment.Identity
		}
	}
	err = tx.QueryRowContext(ctx, "SELECT task_id,generation,attempt_id,epoch,manifest_id,receipt_id,finalized_ms FROM artifact_result_heads WHERE task_id=?", id).Scan(&t.Head.TaskID, &t.Head.Generation, &t.Head.AttemptID, &t.Head.Epoch, &t.Head.ManifestID, &t.Head.ReceiptID, &t.Head.FinalizedMS)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return t, err
	}
	candidate, err := currentCandidate(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	report, err := latestVerification(ctx, tx, candidate.SelectionID)
	if errors.Is(err, sql.ErrNoRows) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	history, err := decisionHistory(ctx, tx, id)
	if err != nil {
		return t, err
	}
	status := v.Evaluate(report, candidate)
	if candidate.Identity.Generation != generation || t.Head.ManifestID != "" && t.Head.ManifestID != candidate.Manifest.ManifestID {
		status.Verified = false
		status.Reasons = append(status.Reasons, "candidate generation or head stale")
	}
	if status.Verified {
		if err = s.verifyCandidateContent(ctx, tx, candidate); err != nil {
			status.Verified = false
			status.Reasons = append(status.Reasons, "candidate content unavailable")
		}
	}
	t.Verification = v.Summarize(report.ID, status)
	t.Review = r.Derive(candidate, report, history)
	t.Review.Reasons = v.Summarize("", v.Status{Reasons: t.Review.Reasons}).Status.Reasons
	if !status.Verified {
		t.Review.Accepted = false
	}
	if candidate.Identity.Generation != generation || t.Head.ManifestID != "" && t.Head.ManifestID != candidate.Manifest.ManifestID {
		t.Review.Accepted = false
		t.Review.Reasons = append(t.Review.Reasons, "candidate replaced; acceptance stale")
	}
	return t, nil
}
func (s *Store) Task(ctx context.Context, id string) (Task, error) {
	if !p.ValidID(id) {
		return Task{}, g.Deny("invalid_id", "task_id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	return s.taskTx(ctx, tx, id, s.meta.Generation)
}
func (s *Store) Tasks(ctx context.Context, after string, limit int) ([]Task, error) {
	if limit < 1 || limit > a.MaxItems || after != "" && !p.ValidID(after) {
		return nil, g.Deny("invalid_query", "after/limit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT task_id FROM task_briefs WHERE task_id>? ORDER BY task_id LIMIT ?", after, limit)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	out := []Task{}
	for _, id := range ids {
		t, err := s.taskTx(ctx, tx, id, s.meta.Generation)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
func (s *Store) Eligibility(ctx context.Context, id string) (sc.Eligibility, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return sc.Eligibility{}, err
	}
	defer tx.Rollback()
	return eligibility(ctx, tx, id)
}
func pausedTx(ctx context.Context, tx *sql.Tx) (DaemonState, error) {
	var d DaemonState
	err := tx.QueryRowContext(ctx, "SELECT paused,reason,updated_ms FROM daemon_state WHERE singleton=1").Scan(&d.Paused, &d.Reason, &d.UpdatedMS)
	return d, err
}
func (s *Store) Paused(ctx context.Context) (DaemonState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return DaemonState{}, err
	}
	defer tx.Rollback()
	return pausedTx(ctx, tx)
}
func (s *Store) SetPaused(ctx context.Context, actor, messageID string, paused bool, reason string, confirmSourceFenced bool) (DaemonState, error) {
	if !p.ValidID(messageID) || len(reason) > 256 {
		return DaemonState{}, g.Deny("malformed", "pause")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return DaemonState{}, err
	}
	defer tx.Rollback()
	if err = owner(ctx, tx, actor); err != nil {
		return DaemonState{}, err
	}
	who, err := principal(ctx, tx, actor)
	if err != nil {
		return DaemonState{}, err
	}
	messageID = dispatchID(messageID, "daemon-state")
	request := struct {
		Paused  bool
		Reason  string
		Confirm bool
	}{paused, reason, confirmSourceFenced}
	digest, _ := sc.Digest(request)
	old, err := ownerCommandTx(ctx, tx, messageID)
	if err == nil {
		if old.PrincipalID != who.ID || old.RequestSHA256 != digest || old.Route != "daemon-state" {
			return DaemonState{}, g.Deny("identity_conflict", "message_id")
		}
		if old.Generation != s.meta.Generation {
			return DaemonState{}, g.Deny("stale_generation", "message_id")
		}
		var d DaemonState
		err = json.Unmarshal(old.Response, &d)
		return d, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DaemonState{}, err
	}
	state, err := pausedTx(ctx, tx)
	if err != nil {
		return state, err
	}
	var restored int64
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(restored_ms),0) FROM restore_history").Scan(&restored); err != nil {
		return state, err
	}
	if !paused && state.Paused && restored > 0 && !confirmSourceFenced {
		return state, g.Deny("reconciliation_required", "confirm_source_fenced")
	}
	state = DaemonState{Paused: paused, Reason: reason, UpdatedMS: time.Now().UnixMilli()}
	if !paused {
		state.ClearedLatches, err = ownerClearLatchesTx(ctx, tx, "", s.meta.DaemonBoot, state.UpdatedMS)
		if err != nil {
			return DaemonState{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE daemon_state SET paused=?,reason=?,updated_ms=? WHERE singleton=1", state.Paused, state.Reason, state.UpdatedMS); err != nil {
		return state, err
	}
	body, _ := json.Marshal(state)
	if err = recordOwnerCommandTx(ctx, tx, OwnerCommand{MessageID: messageID, Generation: s.meta.Generation, PrincipalID: who.ID, Route: "daemon-state", RequestSHA256: digest, Status: 200, Response: body, CreatedMS: state.UpdatedMS}); err != nil {
		return state, err
	}
	return state, tx.Commit()
}
func jobTx(ctx context.Context, tx *sql.Tx, id string) (Job, error) {
	var j Job
	err := tx.QueryRowContext(ctx, "SELECT id,kind,subject_id,state,daemon_boot,created_ms,started_ms,finished_ms,result,error FROM jobs WHERE id=?", id).Scan(&j.ID, &j.Kind, &j.SubjectID, &j.State, &j.DaemonBoot, &j.CreatedMS, &j.StartedMS, &j.FinishedMS, &j.Result, &j.Error)
	return j, err
}
func (s *Store) Job(ctx context.Context, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	return jobTx(ctx, tx, id)
}
func (s *Store) PutJob(ctx context.Context, j Job) (Job, error) {
	if !p.ValidID(j.ID) || !g.ValidActor(j.Kind) || len(j.SubjectID) > 256 || !slices.Contains([]string{"queued", "running", "succeeded", "failed"}, j.State) || !p.ValidID(j.DaemonBoot) || j.CreatedMS <= 0 || j.StartedMS < 0 || j.FinishedMS < 0 || len(j.Result) > 65536 || len(j.Error) > 4096 {
		return Job{}, g.Deny("malformed", "job")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	if err = workflowOwner(ctx, tx); err != nil {
		return Job{}, err
	}
	old, err := jobTx(ctx, tx, j.ID)
	if errors.Is(err, sql.ErrNoRows) {
		if j.State != "queued" || j.StartedMS != 0 || j.FinishedMS != 0 {
			return Job{}, g.Deny("revision_conflict", "job")
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO jobs VALUES(?,?,?,?,?,?,?,?,?,?)", j.ID, j.Kind, j.SubjectID, j.State, j.DaemonBoot, j.CreatedMS, j.StartedMS, j.FinishedMS, j.Result, j.Error)
	} else if err == nil {
		if old == j {
			return old, nil
		}
		if old.ID != j.ID || old.Kind != j.Kind || old.SubjectID != j.SubjectID || old.CreatedMS != j.CreatedMS {
			return Job{}, g.Deny("identity_conflict", "job")
		}
		if old.State == "succeeded" || old.State == "failed" || old.State == "queued" && j.State != "running" && j.State != "failed" || old.State == "running" && j.State != "succeeded" && j.State != "failed" {
			return Job{}, g.Deny("revision_conflict", "job")
		}
		_, err = tx.ExecContext(ctx, "UPDATE jobs SET state=?,daemon_boot=?,started_ms=?,finished_ms=?,result=?,error=? WHERE id=?", j.State, j.DaemonBoot, j.StartedMS, j.FinishedMS, j.Result, j.Error, j.ID)
	}
	if err != nil {
		return Job{}, err
	}
	return j, tx.Commit()
}

// RecoverJobs fails interrupted work before a worker can accept new jobs. Queued
// jobs keep their durable input and are returned for replay by the single worker.
func (s *Store) RecoverJobs(ctx context.Context) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE jobs SET state='failed',error='daemon_restart',finished_ms=? WHERE state='running'", time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM jobs WHERE state='queued' ORDER BY created_ms,id")
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	out := []Job{}
	for _, id := range ids {
		j, err := jobTx(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, tx.Commit()
}
func ownerCommandTx(ctx context.Context, tx *sql.Tx, id string) (OwnerCommand, error) {
	var c OwnerCommand
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT message_id,generation,principal_id,route,request_sha256,status,response,created_ms FROM owner_commands WHERE message_id=?", id).Scan(&c.MessageID, &c.Generation, &c.PrincipalID, &c.Route, &c.RequestSHA256, &c.Status, &raw, &c.CreatedMS)
	c.Response = json.RawMessage(raw)
	return c, err
}
func recordOwnerCommandTx(ctx context.Context, tx *sql.Tx, c OwnerCommand) error {
	old, err := ownerCommandTx(ctx, tx, c.MessageID)
	if err == nil {
		if old.Generation != c.Generation || old.PrincipalID != c.PrincipalID || old.Route != c.Route || old.RequestSHA256 != c.RequestSHA256 || old.Status != c.Status || string(old.Response) != string(c.Response) {
			return g.Deny("identity_conflict", "message_id")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO owner_commands VALUES(?,?,?,?,?,?,?,?)", c.MessageID, c.Generation, c.PrincipalID, c.Route, c.RequestSHA256, c.Status, string(c.Response), c.CreatedMS)
	return err
}
func (s *Store) RecordOwnerCommand(ctx context.Context, c OwnerCommand) error {
	if !p.ValidID(c.MessageID) || !p.ValidID(c.Generation) || !p.ValidID(c.PrincipalID) || len(c.Route) == 0 || len(c.Route) > 256 || !v.IsDigest(c.RequestSHA256) || c.Status < 100 || c.Status > 599 || !json.Valid(c.Response) || len(c.Response) > 1<<20 {
		return g.Deny("malformed", "owner_command")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = workflowOwner(ctx, tx); err != nil {
		return err
	}
	if c.CreatedMS == 0 {
		c.CreatedMS = time.Now().UnixMilli()
	}
	if c.Generation != s.meta.Generation {
		return g.Deny("stale_generation", "command")
	}
	if err = recordOwnerCommandTx(ctx, tx, c); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) OwnerCommand(ctx context.Context, id string) (OwnerCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return OwnerCommand{}, err
	}
	defer tx.Rollback()
	return ownerCommandTx(ctx, tx, id)
}
func (s *Store) PutGatewayProfile(ctx context.Context, actor string, body []byte) (GatewayProfile, error) {
	var config struct {
		Version       string `json:"version"`
		ID            string `json:"gateway_id"`
		BaseURL       string `json:"base_url"`
		CredentialRef struct {
			Kind string `json:"kind"`
			Path string `json:"path"`
		} `json:"credential_ref"`
		Protocols []string `json:"protocols"`
		Models    []string `json:"models"`
	}
	if closedjson.Decode(body, &config, 16384, nil) != nil || config.Version != "gateway-config-v1" || !gatewayLabel(config.ID, 64) || config.CredentialRef.Kind != "file" || !filepath.IsAbs(config.CredentialRef.Path) || filepath.Clean(config.CredentialRef.Path) != config.CredentialRef.Path || strings.ContainsRune(config.CredentialRef.Path, 0) || !gatewayList(config.Protocols, 16) || !gatewayList(config.Models, 256) {
		return GatewayProfile{}, g.Deny("malformed", "gateway")
	}
	u, err := url.Parse(config.BaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.String() != config.BaseURL {
		return GatewayProfile{}, g.Deny("malformed", "gateway")
	}
	body, _ = json.Marshal(config)
	out := GatewayProfile{Digest: v.Digest(body), Body: body, CreatedMS: time.Now().UnixMilli()}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return GatewayProfile{}, err
	}
	defer tx.Rollback()
	if err = owner(ctx, tx, actor); err != nil {
		return GatewayProfile{}, err
	}
	err = tx.QueryRowContext(ctx, "SELECT created_ms FROM gateway_profiles WHERE digest=?", out.Digest).Scan(&out.CreatedMS)
	if err == nil {
		return out, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO gateway_profiles VALUES(?,?,?)", out.Digest, string(body), out.CreatedMS); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// Match the explicit gateway-config-v1 shape without opening its credential.
func gatewayLabel(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}
func gatewayList(values []string, limit int) bool {
	if len(values) == 0 || len(values) > limit {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !gatewayLabel(value, 256) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

// AttemptView retains reservation and acknowledgement separately from process
// observations. An assigned or stopped row never claims a process was killed.
type AttemptView struct {
	AttemptSummary
	Dispatch Dispatch `json:"dispatch"`
}

func (s *Store) AttemptView(ctx context.Context, id string) (AttemptView, error) {
	if !p.ValidID(id) {
		return AttemptView{}, g.Deny("invalid_id", "attempt_id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return AttemptView{}, err
	}
	defer tx.Rollback()
	var out AttemptView
	out.Identity.Generation = s.meta.Generation
	err = tx.QueryRowContext(ctx, "SELECT a.task_id,a.id,a.epoch,a.state,a.revision,d.id FROM attempts a JOIN dispatches d ON d.attempt_id=a.id WHERE a.id=?", id).Scan(&out.Identity.TaskID, &out.Identity.AttemptID, &out.Identity.Epoch, &out.State, &out.Revision, &out.DispatchID)
	if err != nil {
		return out, err
	}
	out.Dispatch, err = loadDispatch(ctx, tx, out.DispatchID)
	if err == nil {
		out.Identity = out.Dispatch.Assignment.Identity
	}
	out.Acknowledged = out.Dispatch.Acknowledged
	out.Released = out.Dispatch.Released
	return out, err
}

type RunnerView struct {
	Principal   i.Principal      `json:"principal"`
	Session     json.RawMessage  `json:"session,omitempty"`
	Eligibility []sc.Eligibility `json:"eligibility"`
}

func (s *Store) Runners(ctx context.Context) ([]RunnerView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id,role,enabled,revoked,revision FROM principals WHERE role='runner' ORDER BY id")
	if err != nil {
		return nil, err
	}
	out := []RunnerView{}
	for rows.Next() {
		r := RunnerView{Principal: i.Principal{Version: i.Version}, Eligibility: []sc.Eligibility{}}
		if err = rows.Scan(&r.Principal.ID, &r.Principal.Role, &r.Principal.Enabled, &r.Principal.Revoked, &r.Principal.Revision); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for n := range out {
		var session struct {
			ID         string `json:"session_id"`
			RunnerBoot string `json:"runner_boot"`
			DaemonBoot string `json:"daemon_boot"`
			Generation string `json:"generation"`
			Mode       string `json:"mode"`
		}
		err = tx.QueryRowContext(ctx, "SELECT id,runner_boot,daemon_boot,generation,mode FROM runner_sessions WHERE runner_id=? ORDER BY created_ms DESC,id DESC LIMIT 1", out[n].Principal.ID).Scan(&session.ID, &session.RunnerBoot, &session.DaemonBoot, &session.Generation, &session.Mode)
		if err == nil {
			out[n].Session, _ = json.Marshal(session)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, "SELECT body FROM dispatch_eligibility e WHERE runner_id=? AND revision=(SELECT MAX(revision) FROM dispatch_eligibility WHERE id=e.id) ORDER BY id", out[n].Principal.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw string
			var facts sc.Eligibility
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			if err = json.Unmarshal([]byte(raw), &facts); err != nil {
				rows.Close()
				return nil, err
			}
			out[n].Eligibility = append(out[n].Eligibility, facts)
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	return out, nil
}

type EventPage struct {
	NextAfter   int64     `json:"next_after"`
	Events      []a.Event `json:"events"`
	PollAfterMS int64     `json:"poll_after_ms"`
}

func (s *Store) Events(ctx context.Context, after int64, limit int, task string) (EventPage, error) {
	out := EventPage{NextAfter: after, Events: []a.Event{}, PollAfterMS: 500}
	if after < 0 || after > p.MaxInteger || limit < 1 || limit > a.MaxItems || task != "" && !p.ValidID(task) {
		return out, g.Deny("invalid_query", "events")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var first int64
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MIN(sequence),0) FROM events").Scan(&first); err != nil {
		return out, err
	}
	if after > 0 && first > after+1 {
		return out, g.Deny("cursor_expired", "after")
	}
	rows, err := tx.QueryContext(ctx, "SELECT sequence,revision,message FROM events WHERE sequence>? AND (?='' OR task_id=?) ORDER BY sequence LIMIT ?", after, task, task, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e a.Event
		var raw string
		if err = rows.Scan(&e.Sequence, &e.Revision, &raw); err != nil {
			return out, err
		}
		if e.Message, err = p.Decode([]byte(raw)); err != nil {
			return out, err
		}
		out.Events = append(out.Events, e)
		out.NextAfter = e.Sequence
	}
	return out, rows.Err()
}
func (s *Store) AttemptArtifacts(ctx context.Context, id string) ([]json.RawMessage, error) {
	return s.workflowJSONRows(ctx, "SELECT m.body FROM artifact_manifests m JOIN artifact_results r ON r.manifest_id=m.manifest_id WHERE r.attempt_id=? ORDER BY r.receipt_id", id)
}

// AttemptUsageRows is the owner read view of retained receipts as raw JSON; the
// execution channel's AttemptUsage returns the typed receipts.
func (s *Store) AttemptUsageRows(ctx context.Context, id string) ([]json.RawMessage, error) {
	return s.workflowJSONRows(ctx, "SELECT body FROM attempt_usage WHERE attempt_id=? ORDER BY request_id", id)
}
func (s *Store) workflowJSONRows(ctx context.Context, query, id string) ([]json.RawMessage, error) {
	if !p.ValidID(id) {
		return nil, g.Deny("invalid_id", "attempt_id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT 1 FROM attempts WHERE id=?", id).Scan(&exists); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if !json.Valid(raw) {
			return nil, g.Deny("corrupt_record", "json")
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, rows.Err()
}

// OpenWorkflowBlob exposes only committed, catalogued content, never arbitrary
// paths; opening precedes releasing the store lock so collection cannot race it.
func (s *Store) OpenWorkflowBlob(ctx context.Context, digest string) (io.ReadCloser, int64, error) {
	if !v.IsDigest(digest) {
		return nil, 0, g.Deny("invalid_id", "sha256")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var size int64
	if err = tx.QueryRowContext(ctx, "SELECT bytes FROM artifact_blobs WHERE digest=? AND state='committed'", digest).Scan(&size); err != nil {
		return nil, 0, err
	}
	root, err := os.OpenRoot(s.artifacts)
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	path := filepath.Join("blobs", digest[:2], digest)
	info, err := root.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return nil, 0, fmt.Errorf("artifact unavailable")
	}
	f, err := root.Open(path)
	return f, size, err
}

// HeadManifest returns the exact finalized candidate, never the newest upload.
func (s *Store) HeadManifest(ctx context.Context, task string) (p.Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return p.Manifest{}, err
	}
	defer tx.Rollback()
	var m p.Manifest
	err = tx.QueryRowContext(ctx, `SELECT m.manifest_id,m.sha256,m.bytes FROM artifact_result_heads h JOIN artifact_manifests m ON m.manifest_id=h.manifest_id WHERE h.task_id=? AND h.generation=?`, task, s.meta.Generation).Scan(&m.ManifestID, &m.SHA256, &m.Bytes)
	return m, err
}

// WorkflowRestoreHistory reads recovery provenance without depending on the
// backup implementation. It never changes generation or resumes execution.
func (s *Store) WorkflowRestoreHistory(ctx context.Context) ([]RestoreEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id,old_generation,new_generation,backup_id,backup_created_ms,restored_ms,manifest_sha256 FROM restore_history ORDER BY restored_ms,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RestoreEntry{}
	for rows.Next() {
		var r RestoreEntry
		if err = rows.Scan(&r.ID, &r.OldGeneration, &r.NewGeneration, &r.BackupID, &r.BackupCreatedMS, &r.RestoredMS, &r.ManifestSHA256); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// BriefDigest binds the exact input served on the authenticated execution channel.
func BriefDigest(brief TaskBrief) string {
	digest, _ := workflow.TaskDigests(inputBrief(brief))
	return digest
}
func (s *Store) TaskBindings(ctx context.Context, id string) (int64, string, string, error) {
	t, err := s.Task(ctx, id)
	return t.Brief.Revision, t.Brief.BriefSHA256, t.Brief.PlanSHA256, err
}

// ownerLatchClearance uses S5's durable latch-cleared:<stop_id> contract. Actor
// and Cause describe the retained stop; the owner command authenticates resume.
// Empty Terminal is intentional: owner resume is NOT termination evidence.
type ownerLatchClearance struct {
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

func ownerClearLatchesTx(ctx context.Context, tx *sql.Tx, taskID, boot string, now int64) ([]string, error) {
	kind := c.GlobalStop
	if taskID != "" {
		kind = c.PauseTask
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM control_stops s WHERE kind=? AND task_id=? AND NOT EXISTS(SELECT 1 FROM reconcile_reports r WHERE r.id='latch-cleared:'||s.id) ORDER BY id`, kind, taskID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for _, id := range ids {
		stop, err := loadStop(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if stop.Request.Kind != kind || stop.Request.TaskID != taskID {
			return nil, g.Deny("corrupt_record", "stop")
		}
		record := ownerLatchClearance{StopID: id, TaskID: taskID, Kind: kind, Cause: stop.Request.Cause, Actor: stop.Actor, DaemonBoot: boot, ClearedMS: now}
		body, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO reconcile_reports VALUES(?,?,?,?)", "latch-cleared:"+id, boot, now, string(body)); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// ResumeLatches clears only global stops (empty taskID) or this task's pauses.
// A domain receipt in the same transaction freezes the cleared list even if the
// HTTP receipt is lost and newer stops arrive before a retry. No attempt,
// reservation, cancellation, authority or stop-history row is modified.
func (s *Store) ResumeLatches(ctx context.Context, actor, taskID, messageID string) ([]string, error) {
	if !p.ValidID(messageID) || taskID != "" && !p.ValidID(taskID) {
		return nil, g.Deny("invalid_id", "resume")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = owner(ctx, tx, actor); err != nil {
		return nil, err
	}
	who, err := principal(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	id := dispatchID(messageID, "owner-resume")
	digest, _ := sc.Digest(struct{ TaskID string }{taskID})
	old, err := ownerCommandTx(ctx, tx, id)
	if err == nil {
		if old.PrincipalID != who.ID || old.RequestSHA256 != digest || old.Route != "owner-resume" {
			return nil, g.Deny("identity_conflict", "message_id")
		}
		if old.Generation != s.meta.Generation {
			return nil, g.Deny("stale_generation", "message_id")
		}
		var ids []string
		err = json.Unmarshal(old.Response, &ids)
		return ids, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if taskID != "" {
		var exists int
		if err = tx.QueryRowContext(ctx, "SELECT 1 FROM tasks WHERE id=?", taskID).Scan(&exists); err != nil {
			return nil, err
		}
	}
	now := time.Now().UnixMilli()
	ids, err := ownerClearLatchesTx(ctx, tx, taskID, s.meta.DaemonBoot, now)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(ids)
	if err = recordOwnerCommandTx(ctx, tx, OwnerCommand{MessageID: id, Generation: s.meta.Generation, PrincipalID: who.ID, Route: "owner-resume", RequestSHA256: digest, Status: 200, Response: body, CreatedMS: now}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}
