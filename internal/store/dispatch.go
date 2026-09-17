package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	c "github.com/korallis/letmecook/internal/control"
	sc "github.com/korallis/letmecook/internal/scheduler"
	p "github.com/korallis/letmecook/schemas/execution"
)

const dispatchSchema = `
CREATE TABLE attempts_next (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id),
 epoch INTEGER NOT NULL CHECK(epoch BETWEEN 1 AND 9007199254740991),
 state TEXT NOT NULL CHECK(state IN ('assigned','starting','running','result_pending','stopping','unknown','succeeded','failed','cancelled','expired')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 assignment_id TEXT NOT NULL UNIQUE,
 UNIQUE(task_id,epoch), UNIQUE(id,task_id,epoch)
) STRICT;
INSERT INTO attempts_next SELECT * FROM attempts;
CREATE TABLE events_next (
 sequence INTEGER PRIMARY KEY CHECK(sequence BETWEEN 1 AND 9007199254740991),
 message_id TEXT NOT NULL UNIQUE,
 attempt_id TEXT NOT NULL, task_id TEXT NOT NULL, epoch INTEGER NOT NULL,
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 message TEXT NOT NULL CHECK(length(message)<=8192),
 UNIQUE(attempt_id,revision),
 FOREIGN KEY(attempt_id,task_id,epoch) REFERENCES attempts_next(id,task_id,epoch)
) STRICT;
INSERT INTO events_next SELECT * FROM events;
DROP TABLE events;
DROP TABLE attempts;
ALTER TABLE attempts_next RENAME TO attempts;
ALTER TABLE events_next RENAME TO events;
CREATE UNIQUE INDEX one_current_attempt ON attempts(task_id) WHERE state NOT IN ('succeeded','failed','cancelled','expired');
CREATE TRIGGER events_no_update BEFORE UPDATE ON events BEGIN SELECT RAISE(ABORT,'events are append-only'); END;
CREATE TRIGGER events_no_delete BEFORE DELETE ON events BEGIN SELECT RAISE(ABORT,'events are append-only'); END;
CREATE TRIGGER attempts_identity BEFORE UPDATE OF id,task_id,epoch,assignment_id ON attempts BEGIN SELECT RAISE(ABORT,'attempt identity is immutable'); END;
CREATE TRIGGER attempts_terminal BEFORE UPDATE ON attempts WHEN OLD.state IN ('succeeded','failed','cancelled','expired') BEGIN SELECT RAISE(ABORT,'terminal attempt is immutable'); END;
CREATE TRIGGER attempts_no_delete BEFORE DELETE ON attempts BEGIN SELECT RAISE(ABORT,'attempts are retained'); END;
CREATE TABLE dispatch_eligibility (
 id TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 body TEXT NOT NULL CHECK(length(CAST(body AS BLOB))<=65536),
 actor TEXT NOT NULL REFERENCES principals(id),
 repository_id TEXT NOT NULL REFERENCES repositories(id), runner_id TEXT NOT NULL REFERENCES principals(id), root TEXT NOT NULL,
 PRIMARY KEY(id,revision), UNIQUE(repository_id,runner_id,root,revision)
) STRICT;
CREATE TRIGGER eligibility_no_update BEFORE UPDATE ON dispatch_eligibility BEGIN SELECT RAISE(ABORT,'eligibility is immutable'); END;
CREATE TRIGGER eligibility_no_delete BEFORE DELETE ON dispatch_eligibility BEGIN SELECT RAISE(ABORT,'eligibility is retained'); END;
-- One immutable row IS the reservation, input/decision record and assignment outbox.
-- Acknowledgement and release have independent immutable receipts; neither erases charges.
CREATE TABLE dispatches (
 id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL UNIQUE REFERENCES attempts(id),
 task_id TEXT NOT NULL REFERENCES tasks(id), grant_id TEXT NOT NULL REFERENCES execution_grants(id),
 runner_id TEXT NOT NULL REFERENCES principals(id),
 eligibility_id TEXT NOT NULL, eligibility_revision INTEGER NOT NULL,
 input_digest TEXT NOT NULL CHECK(length(input_digest)=64),
 input TEXT NOT NULL CHECK(length(CAST(input AS BLOB))<=65536),
 assignment TEXT NOT NULL CHECK(length(assignment)<=8192),
 created_ms INTEGER NOT NULL CHECK(created_ms BETWEEN 1 AND 9007199254740991),
 FOREIGN KEY(eligibility_id,eligibility_revision) REFERENCES dispatch_eligibility(id,revision)
) STRICT;
CREATE TRIGGER dispatches_no_update BEFORE UPDATE ON dispatches BEGIN SELECT RAISE(ABORT,'dispatch is immutable'); END;
CREATE TRIGGER dispatches_no_delete BEFORE DELETE ON dispatches BEGIN SELECT RAISE(ABORT,'dispatch is retained'); END;
CREATE TABLE dispatch_acks (
 dispatch_id TEXT PRIMARY KEY REFERENCES dispatches(id), message_id TEXT NOT NULL UNIQUE,
 body TEXT NOT NULL CHECK(length(body)<=8192)
) STRICT;
CREATE TRIGGER dispatch_acks_no_update BEFORE UPDATE ON dispatch_acks BEGIN SELECT RAISE(ABORT,'ack is immutable'); END;
CREATE TRIGGER dispatch_acks_no_delete BEFORE DELETE ON dispatch_acks BEGIN SELECT RAISE(ABORT,'ack is retained'); END;
CREATE TABLE dispatch_releases (
 dispatch_id TEXT PRIMARY KEY REFERENCES dispatches(id), body TEXT NOT NULL CHECK(length(body)<=8192),
 actor TEXT NOT NULL REFERENCES principals(id)
) STRICT;
CREATE TRIGGER dispatch_releases_no_update BEFORE UPDATE ON dispatch_releases BEGIN SELECT RAISE(ABORT,'release is immutable'); END;
CREATE TRIGGER dispatch_releases_no_delete BEFORE DELETE ON dispatch_releases BEGIN SELECT RAISE(ABORT,'release is retained'); END;
CREATE TABLE dispatch_stops (
 task_id TEXT PRIMARY KEY, actor TEXT NOT NULL REFERENCES principals(id)
) STRICT;
CREATE TRIGGER dispatch_stops_no_update BEFORE UPDATE ON dispatch_stops BEGIN SELECT RAISE(ABORT,'stop is sticky'); END;
CREATE TRIGGER dispatch_stops_no_delete BEFORE DELETE ON dispatch_stops BEGIN SELECT RAISE(ABORT,'stop is sticky'); END;
PRAGMA user_version=6;
`

// PublishEligibility is a trusted owner-only evidence import. actor is a proven
// credential fingerprint, never a request-supplied identity. The owner must verify
// runner-local policy and full-graph/runtime evidence before importing this record.
// It is NOT a capability probe; no production execution profile is shipped.
func (s *Store) PublishEligibility(ctx context.Context, actor string, expected int64, v sc.Eligibility) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected < 0 || expected >= p.MaxInteger {
		return g.Deny("malformed", "revision")
	}
	if err := v.Validate(); err != nil {
		return err
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := owner(ctx, tx, actor); err != nil {
		return err
	}
	who, err := principal(ctx, tx, actor)
	if err != nil {
		return err
	}
	profile, err := repositoryProfile(ctx, tx, v.Repository.Repository)
	if errors.Is(err, sql.ErrNoRows) {
		return g.Deny("repository_ineligible", "repository")
	}
	if err != nil {
		return err
	}
	if profile.Select(v.Repository) != nil {
		return g.Deny("repository_ineligible", "selection")
	}
	old, err := eligibility(ctx, tx, v.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if reflect.DeepEqual(old, v) && (expected == old.Revision || expected == old.Revision-1) {
		return nil
	}
	if old.Revision != expected || v.Revision != expected+1 {
		return g.Deny("revision_conflict", "eligibility")
	}
	if old.ID != "" && (old.Repository.Repository != v.Repository.Repository || old.Repository.RunnerRoot != v.Repository.RunnerRoot) {
		return g.Deny("identity_conflict", "eligibility_pairing")
	}
	var alias bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM dispatch_eligibility WHERE repository_id=? AND runner_id=? AND root=? AND id<>?)", v.Repository.Repository, v.Repository.RunnerRoot.RunnerID, v.Repository.RunnerRoot.Root, v.ID).Scan(&alias); err != nil {
		return err
	}
	if alias {
		return g.Deny("identity_conflict", "eligibility_pairing")
	}
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO dispatch_eligibility VALUES(?,?,?,?,?,?,?)", v.ID, v.Revision, string(body), who.ID, v.Repository.Repository, v.Repository.RunnerRoot.RunnerID, v.Repository.RunnerRoot.Root); err != nil {
		return err
	}
	return tx.Commit()
}

func eligibility(ctx context.Context, tx *sql.Tx, id string) (sc.Eligibility, error) {
	var v sc.Eligibility
	var body string
	err := tx.QueryRowContext(ctx, "SELECT body FROM dispatch_eligibility WHERE id=? ORDER BY revision DESC LIMIT 1", id).Scan(&body)
	if err != nil {
		return v, err
	}
	if len(body) > g.MaxBytes || json.Unmarshal([]byte(body), &v) != nil {
		return v, g.Deny("corrupt_record", "eligibility")
	}
	if err := v.Validate(); err != nil {
		return v, err
	}
	canonical, err := json.Marshal(v)
	if err != nil || string(canonical) != body || v.ID != id {
		return v, g.Deny("corrupt_record", "eligibility")
	}
	return v, nil
}

type DispatchRequest struct {
	ID        string      `json:"id"` // stable intent key; reconnect reuses it, new attempt uses a new key.
	Request   g.Request   `json:"request"`
	Decision  sc.Decision `json:"decision"`
	Allowance g.Budgets   `json:"allowance"` // finite single-attempt reservation, within task ceilings.
}

type Dispatch struct {
	DispatchRequest
	Facts        sc.Eligibility `json:"facts"`
	Assignment   p.Message      `json:"assignment"`
	Acknowledged bool           `json:"acknowledged"`
	Released     bool           `json:"released"`
}

type dispatchInput struct {
	DispatchRequest
	Facts sc.Eligibility `json:"facts"`
}

func dispatchID(key, purpose string) string {
	b := sha256.Sum256([]byte(key + ":" + purpose))
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Dispatch atomically checks current stored facts/authority, charges worst-case
// task allowances, reserves capacity and commits the v2 assignment before return.
// This is metadata admission only: assignment is not a lease or launch permission.
func (s *Store) Dispatch(ctx context.Context, request DispatchRequest) (Dispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(request.ID) || !p.ValidID(request.Request.GrantID) || !p.ValidID(request.Request.TaskID) {
		return Dispatch{}, g.Deny("malformed", "dispatch")
	}
	if err := request.Decision.Validate(); err != nil {
		return Dispatch{}, err
	}
	if err := request.Request.Envelope.Validate(); err != nil {
		return Dispatch{}, err
	}
	bounded := request.Request.Envelope
	bounded.Budgets = request.Allowance
	if err := g.Within(bounded, request.Request.Envelope); err != nil {
		return Dispatch{}, err
	}
	if request.Allowance.Attempts != 1 || request.Allowance.Retries != 0 || request.Allowance.Concurrency != 1 || request.Allowance.TotalMS != request.Allowance.AttemptMS {
		return Dispatch{}, g.Deny("malformed", "attempt_allowance")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return Dispatch{}, err
	}
	defer tx.Rollback()
	// Exact retained replay is a read, not new authority or delivery permission.
	old, err := loadDispatch(ctx, tx, request.ID)
	if err == nil {
		if !reflect.DeepEqual(old.DispatchRequest, request) {
			return Dispatch{}, g.Deny("identity_conflict", "dispatch")
		}
		return old, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Dispatch{}, err
	}
	now := time.Now().UnixMilli()
	if err := expireGrants(ctx, tx, now); err != nil {
		return Dispatch{}, err
	}
	if err := dispatchAllowed(ctx, tx, request.Request, now); err != nil {
		// Keep elapsed grant expiry sticky, including refused admission.
		if commitErr := tx.Commit(); commitErr != nil {
			return Dispatch{}, commitErr
		}
		return Dispatch{}, err
	}
	facts, err := eligibility(ctx, tx, request.Decision.EligibilityID)
	if errors.Is(err, sql.ErrNoRows) {
		return Dispatch{}, g.Deny("no_eligible_tuple", "eligibility")
	}
	if err != nil {
		return Dispatch{}, err
	}
	if err := sc.CheckDispatchWithPolicy(request.Request, request.Decision, facts, now, s.admission); err != nil {
		return Dispatch{}, err
	}
	if err := placement(ctx, tx, facts); err != nil {
		return Dispatch{}, err
	}
	var active int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM attempts WHERE task_id=? AND state NOT IN ('succeeded','failed','cancelled','expired')", request.Request.TaskID).Scan(&active); err != nil {
		return Dispatch{}, err
	}
	if active != 0 {
		return Dispatch{}, g.Deny("current_assignment", "task")
	}
	if err := reserve(ctx, tx, request, facts, now); err != nil {
		return Dispatch{}, err
	}
	var epoch int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(max(epoch),0) FROM attempts WHERE task_id=?", request.Request.TaskID).Scan(&epoch); err != nil {
		return Dispatch{}, err
	}
	if epoch == p.MaxInteger {
		return Dispatch{}, g.Deny("epoch_exhausted", "task")
	}
	input := dispatchInput{DispatchRequest: request, Facts: facts}
	hash, err := sc.Digest(input)
	if err != nil {
		return Dispatch{}, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return Dispatch{}, err
	}
	route := request.Decision.Selected
	m := p.Message{Version: p.FencedVersion, Kind: "assign", MessageID: dispatchID(request.ID, "message"), AssignmentID: dispatchID(request.ID, "assignment"), Identity: p.Identity{Generation: s.meta.Generation, TaskID: request.Request.TaskID, AttemptID: dispatchID(request.ID, "attempt"), Epoch: epoch + 1}, InputDigest: hash, Route: &p.Route{RouteRef: route.RouteRef, DecisionDigest: request.Request.Envelope.RouteDecision.SHA256, PolicyDigest: route.Policy.SHA256, LimitsProfile: route.LimitsProfile}}
	if r := p.CheckCurrent(m, m.Identity); r != p.OK {
		return Dispatch{}, r
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO tasks VALUES(?,'ready') ON CONFLICT(id) DO UPDATE SET state='ready'", m.Identity.TaskID); err != nil {
		return Dispatch{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO attempts VALUES(?,?,?,'assigned',1,?)", m.Identity.AttemptID, m.Identity.TaskID, m.Identity.Epoch, m.AssignmentID); err != nil {
		return Dispatch{}, err
	}
	if err := record(ctx, tx, m, 1); err != nil {
		return Dispatch{}, err
	}
	assignment, err := json.Marshal(m)
	if err != nil {
		return Dispatch{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO dispatches VALUES(?,?,?,?,?,?,?,?,?,?,?)", request.ID, m.Identity.AttemptID, m.Identity.TaskID, request.Request.GrantID, facts.Repository.RunnerRoot.RunnerID, facts.ID, facts.Revision, hash, string(body), string(assignment), now); err != nil {
		return Dispatch{}, err
	}
	if err := tx.Commit(); err != nil {
		return Dispatch{}, err
	}
	return Dispatch{DispatchRequest: request, Facts: facts, Assignment: m}, nil
}

func dispatchAllowed(ctx context.Context, tx *sql.Tx, request g.Request, now int64) error {
	var stopped bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM dispatch_stops WHERE task_id=?)", request.TaskID).Scan(&stopped); err != nil {
		return err
	}
	if stopped {
		return g.Deny("stopped", "task")
	}
	if err := checkExecution(ctx, tx, request, now); err != nil {
		return err
	}
	stopped, err := controlSuppressed(ctx, tx, request.TaskID, request.GrantID)
	if err != nil {
		return err
	}
	if stopped {
		return g.Deny("stop_latched", "dispatch")
	}
	return nil
}

func placement(ctx context.Context, tx *sql.Tx, facts sc.Eligibility) error {
	profile, err := repositoryProfile(ctx, tx, facts.Repository.Repository)
	if errors.Is(err, sql.ErrNoRows) {
		return g.Deny("repository_ineligible", "repository")
	}
	if err != nil {
		return err
	}
	if profile.Select(facts.Repository) != nil {
		return g.Deny("repository_policy_drift", "selection")
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM principals WHERE id=? AND role='runner' AND enabled=1 AND revoked=0)", facts.Repository.RunnerRoot.RunnerID).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return g.Deny("runner_disabled", "runner")
	}
	return nil
}

// reserve deliberately charges the complete requested attempt allowance at commit.
// No refund, guessed quota, or timeout release. New grant revisions retain charges.
// ponytail: one unresolved attempt globally for M1 sequential dispatch; parallel
// scheduling needs measured shared capacity and project/global policies first.
func reserve(ctx context.Context, tx *sql.Tx, request DispatchRequest, facts sc.Eligibility, now int64) error {
	// Task ceilings have already been intersected with the grant and local policy.
	ceiling := request.Request.Envelope.Budgets
	want := request.Allowance
	resources, capacity := request.Decision.Resources.Values(), facts.Capacity.Values()
	for j, n := range resources {
		if n > capacity[j] {
			return g.Deny("resource_ceiling", "runner")
		}
	}
	var active int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM attempts WHERE state NOT IN ('succeeded','failed','cancelled','expired')").Scan(&active); err != nil {
		return err
	}
	if active >= 1 {
		return g.Deny("concurrency_ceiling", "sequential_dispatch")
	}
	rows, err := tx.QueryContext(ctx, "SELECT input,created_ms,EXISTS(SELECT 1 FROM dispatch_releases r WHERE r.dispatch_id=d.id) FROM dispatches d WHERE task_id=?", request.Request.TaskID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var count, first int64
	used := [5]int64{}
	for rows.Next() {
		var raw string
		var created int64
		var released bool
		if err := rows.Scan(&raw, &created, &released); err != nil {
			return err
		}
		if !released {
			return g.Deny("reconciliation_required", "reservation")
		}
		var previous dispatchInput
		if len(raw) > g.MaxBytes || json.Unmarshal([]byte(raw), &previous) != nil {
			return g.Deny("corrupt_record", "reservation")
		}
		b := previous.Allowance
		if previous.Decision.Selected.LimitsProfile == "native-subscription-local-v1" && ceiling.ProviderOutputTokens > 0 {
			return g.Deny("budget_unknown", "prior_output_tokens")
		}
		cost := int64(0)
		if b.ProviderCostMicros != nil {
			cost = *b.ProviderCostMicros
		} else if ceiling.ProviderCostMicros != nil {
			return g.Deny("budget_unknown", "prior_cost")
		}
		for j, n := range [5]int64{b.Requests, b.Subattempts, b.AttemptMS, b.ProviderOutputTokens, cost} {
			if n > p.MaxInteger-used[j] {
				return g.Deny("budget_exhausted", "task")
			}
			used[j] += n
		}
		count++
		if first == 0 || created < first {
			first = created
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count >= ceiling.Attempts || count > ceiling.Retries {
		return g.Deny("attempt_ceiling", "task")
	}
	if first != 0 && (now < first || now-first >= ceiling.TotalMS || want.AttemptMS > ceiling.TotalMS-(now-first)) {
		return g.Deny("duration_ceiling", "task")
	}
	if want.AttemptMS > request.Request.Envelope.ExpiresMS-now || want.AttemptMS > facts.ValidUntilMS-now || want.AttemptMS > facts.LocalEnvelope.ExpiresMS-now {
		return g.Deny("duration_ceiling", "validity")
	}
	cost, maxCost := int64(0), p.MaxInteger
	if want.ProviderCostMicros != nil {
		cost = *want.ProviderCostMicros
	}
	if ceiling.ProviderCostMicros != nil {
		maxCost = *ceiling.ProviderCostMicros
	}
	for j, pair := range [5][2]int64{{want.Requests, ceiling.Requests}, {want.Subattempts, ceiling.Subattempts}, {want.AttemptMS, ceiling.TotalMS}, {want.ProviderOutputTokens, ceiling.ProviderOutputTokens}, {cost, maxCost}} {
		if used[j] > pair[1] || pair[0] > pair[1]-used[j] {
			return g.Deny("budget_exhausted", "task")
		}
	}
	return nil
}

func loadDispatch(ctx context.Context, tx *sql.Tx, id string) (Dispatch, error) {
	var input, assignment, hash string
	var v Dispatch
	if err := tx.QueryRowContext(ctx, `SELECT input,assignment,input_digest,EXISTS(SELECT 1 FROM dispatch_acks a WHERE a.dispatch_id=d.id),EXISTS(SELECT 1 FROM dispatch_releases r WHERE r.dispatch_id=d.id) FROM dispatches d WHERE id=?`, id).Scan(&input, &assignment, &hash, &v.Acknowledged, &v.Released); err != nil {
		return v, err
	}
	var in dispatchInput
	if len(input) > g.MaxBytes || json.Unmarshal([]byte(input), &in) != nil {
		return v, g.Deny("corrupt_record", "dispatch")
	}
	digest, err := sc.Digest(in)
	if err != nil || digest != hash || in.ID != id {
		return v, g.Deny("corrupt_record", "input_digest")
	}
	v.DispatchRequest, v.Facts = in.DispatchRequest, in.Facts
	v.Assignment, err = p.Decode([]byte(assignment))
	if err != nil {
		return v, err
	}
	if v.Assignment.InputDigest != hash || v.Assignment.Identity.TaskID != in.Request.TaskID {
		return v, g.Deny("corrupt_record", "assignment")
	}
	return v, nil
}

// Assignment reads retained input/outbox history, including after restart, stop or
// release. It conveys NO permission to deliver/launch; Delivery rechecks admission.
func (s *Store) Assignment(ctx context.Context, id string) (Dispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(id) {
		return Dispatch{}, g.Deny("malformed", "dispatch_id")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return Dispatch{}, err
	}
	defer tx.Rollback()
	return loadDispatch(ctx, tx, id)
}

// PendingAssignments enumerates retained, unacknowledged/unreleased outbox keys
// after restart. Cursor is the last returned UUID, not a deletable queue offset.
// Reads do not authorize delivery; unknown attempts stay reconciliation-only.
func (s *Store) PendingAssignments(ctx context.Context, after string, limit int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if after != "" && !p.ValidID(after) || limit < 1 || limit > 128 {
		return nil, g.Deny("malformed", "cursor/limit")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM dispatches d WHERE id>? AND NOT EXISTS(SELECT 1 FROM dispatch_acks a WHERE a.dispatch_id=d.id) AND NOT EXISTS(SELECT 1 FROM dispatch_releases r WHERE r.dispatch_id=d.id) ORDER BY id LIMIT ?`, after, limit)
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

// Delivery selects one unchanged outbox message for an authenticated current
// runner. Callers negotiate v2 separately; no network sender or lease exists.
// Unknown/restarted attempts need reconciliation, not another launch.
func (s *Store) Delivery(ctx context.Context, fingerprint, id string) (p.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return p.Message{}, err
	}
	defer tx.Rollback()
	if err := expireGrants(ctx, tx, time.Now().UnixMilli()); err != nil {
		return p.Message{}, err
	}
	v, decision := deliverable(ctx, tx, fingerprint, id, s.meta.Generation, s.admission)
	if err := tx.Commit(); err != nil {
		return p.Message{}, err
	}
	if decision != nil {
		return p.Message{}, decision
	}
	return v.Assignment, nil
}

func deliverable(ctx context.Context, tx *sql.Tx, fingerprint, id, generation string, policy sc.AdmissionPolicy) (Dispatch, error) {
	if !p.ValidID(id) {
		return Dispatch{}, g.Deny("malformed", "dispatch_id")
	}
	who, err := principal(ctx, tx, fingerprint)
	if err != nil {
		return Dispatch{}, err
	}
	v, err := loadDispatch(ctx, tx, id)
	if err != nil {
		return Dispatch{}, err
	}
	if who.Role != "runner" || !who.Enabled || who.ID != v.Facts.Repository.RunnerRoot.RunnerID {
		return Dispatch{}, g.Deny("runner_disabled", "delivery")
	}
	if v.Released {
		return Dispatch{}, g.Deny("reconciliation_required", "released")
	}
	if v.Assignment.Identity.Generation != generation {
		return Dispatch{}, p.StaleGeneration
	}
	var state p.AttemptState
	if err := tx.QueryRowContext(ctx, "SELECT state FROM attempts WHERE id=?", v.Assignment.Identity.AttemptID).Scan(&state); err != nil {
		return Dispatch{}, err
	}
	if state != p.Assigned {
		return Dispatch{}, g.Deny("reconciliation_required", "attempt")
	}
	now := time.Now().UnixMilli()
	if err := dispatchAllowed(ctx, tx, v.Request, now); err != nil {
		return Dispatch{}, err
	}
	facts, err := eligibility(ctx, tx, v.Facts.ID)
	if err != nil {
		return Dispatch{}, err
	}
	if err := sc.CheckDispatchWithPolicy(v.Request, v.Decision, facts, now, policy); err != nil {
		return Dispatch{}, err
	}
	if err := placement(ctx, tx, facts); err != nil {
		return Dispatch{}, err
	}
	return v, nil
}

// AcknowledgeAssignment accepts only protocol-validated exact assignment/boots
// from the enrolled runner; commit failure never reports a positive acknowledgement.
// Exact retained receipts replay before mutable admission checks, including old
// boots after restart. Replay is history, never delivery or launch authority.
func (s *Store) AcknowledgeAssignment(ctx context.Context, fingerprint, id string, ack p.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(id) || ack.Kind != "accept" || ack.Version != p.FencedVersion || p.CheckCurrent(ack, ack.Identity) != p.OK {
		return p.Malformed
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	who, err := principal(ctx, tx, fingerprint)
	if err != nil {
		return err
	}
	retained, err := loadDispatch(ctx, tx, id)
	if err != nil {
		return err
	}
	if who.Role != "runner" || who.ID != retained.Facts.Repository.RunnerRoot.RunnerID {
		return g.Deny("runner_disabled", "delivery")
	}
	if retained.Assignment.Identity.Generation != s.meta.Generation {
		return p.StaleGeneration
	}
	body, err := json.Marshal(ack)
	if err != nil {
		return err
	}
	var old string
	err = tx.QueryRowContext(ctx, "SELECT body FROM dispatch_acks WHERE dispatch_id=?", id).Scan(&old)
	if err == nil {
		if old != string(body) {
			return p.IdentityConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := expireGrants(ctx, tx, time.Now().UnixMilli()); err != nil {
		return err
	}
	v, err := deliverable(ctx, tx, fingerprint, id, s.meta.Generation, s.admission)
	if err != nil {
		if commitErr := tx.Commit(); commitErr != nil {
			return commitErr
		}
		return err
	}
	if ack.Identity != v.Assignment.Identity || ack.AssignmentID != v.Assignment.AssignmentID {
		return p.IdentityConflict
	}
	if ack.RunnerBoot != v.Facts.RunnerBoot || ack.DaemonBoot != s.meta.DaemonBoot {
		return p.BootMismatch
	}
	var conflict bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM events WHERE message_id=?) OR EXISTS(SELECT 1 FROM dispatch_acks WHERE message_id=?)", ack.MessageID, ack.MessageID).Scan(&conflict); err != nil {
		return err
	}
	if conflict {
		return p.IdentityConflict
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO dispatch_acks VALUES(?,?,?)", id, ack.MessageID, string(body)); err != nil {
		return err
	}
	return tx.Commit()
}

// StopDispatch is sticky per task and serialized with admission and delivery. It
// does not confirm termination, clear reservations or replace #19 lease fencing.
func (s *Store) StopDispatch(ctx context.Context, actor, taskID string) error {
	if !p.ValidID(taskID) {
		return g.Deny("malformed", "task_id")
	}
	return s.identityWrite(ctx, func(tx *sql.Tx) error {
		if err := owner(ctx, tx, actor); err != nil {
			return err
		}
		who, err := principal(ctx, tx, actor)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO dispatch_stops VALUES(?,?) ON CONFLICT(task_id) DO NOTHING", taskID, who.ID); err != nil {
			return err
		}
		if _, err := latchStop(ctx, tx, actor, c.Request{ID: dispatchID(taskID, "legacy-stop"), Kind: c.PauseTask, TaskID: taskID, Cause: "operator"}, s.controlStamp()); err != nil {
			return err
		}
		m := p.Message{Version: p.FencedVersion, Kind: "transition", Identity: p.Identity{Generation: s.meta.Generation, TaskID: taskID}, From: p.Assigned, To: p.Stopping}
		var id string
		var revision int64
		err = tx.QueryRowContext(ctx, "SELECT d.id,a.id,a.epoch,a.revision FROM attempts a JOIN dispatches d ON d.attempt_id=a.id WHERE a.task_id=? AND a.state='assigned'", taskID).Scan(&id, &m.Identity.AttemptID, &m.Identity.Epoch, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		m.MessageID, m.ExpectedRevision = dispatchID(id, "stop"), &revision
		if r := p.CheckTransition(m, m.Identity, p.Assigned, revision); r != p.OK {
			return r
		}
		if _, err := tx.ExecContext(ctx, "UPDATE attempts SET state='stopping',revision=revision+1 WHERE id=? AND revision=?", m.Identity.AttemptID, revision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE tasks SET state='reconciling' WHERE id=?", taskID); err != nil {
			return err
		}
		return record(ctx, tx, m, revision+1)
	})
}

// Reconciliation is a trusted owner-reviewed evidence reference, NOT a runner
// report or self-attestation. No timer, local exit or remote-unknown can release.
type Reconciliation struct {
	DispatchID         string         `json:"dispatch_id"`
	Identity           p.Identity     `json:"identity"`
	ExpectedRevision   int64          `json:"expected_revision"`
	To                 p.AttemptState `json:"to"`
	ConfirmedProcess   string         `json:"confirmed_process"`
	RemoteWork         string         `json:"remote_work"`
	LaunchFenced       bool           `json:"launch_fenced"`
	ArtifactsPreserved bool           `json:"artifacts_preserved"`
	EvidenceDigest     string         `json:"evidence_digest"`
}

// ReconcileDispatch only implements unknown/stopping -> cancelled/expired. Success
// and failure await real result custody. Reservations release in the same terminal
// CAS/event transaction, but all committed budget charges remain consumed.
func (s *Store) ReconcileDispatch(ctx context.Context, actor string, proof Reconciliation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validReconciliation(proof); err != nil {
		return err
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := owner(ctx, tx, actor); err != nil {
		return err
	}
	if err := s.releaseDispatchTx(ctx, tx, actor, proof); err != nil {
		return err
	}
	return tx.Commit()
}
