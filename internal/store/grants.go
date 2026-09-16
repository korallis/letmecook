package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	p "github.com/korallis/letmecook/schemas/execution"
)

const grantSchema = `
CREATE TABLE execution_grants (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 body TEXT NOT NULL CHECK(length(CAST(body AS BLOB))<=65536),
 expires_ms INTEGER NOT NULL CHECK(expires_ms BETWEEN 1 AND 9007199254740991),
 UNIQUE(task_id,revision), UNIQUE(id,task_id)
) STRICT;
CREATE TABLE execution_grant_heads (
 task_id TEXT PRIMARY KEY, grant_id TEXT NOT NULL UNIQUE,
 FOREIGN KEY(grant_id,task_id) REFERENCES execution_grants(id,task_id)
) STRICT;
CREATE TABLE execution_invalidations (
 sequence INTEGER PRIMARY KEY CHECK(sequence BETWEEN 1 AND 9007199254740991),
 grant_id TEXT NOT NULL UNIQUE REFERENCES execution_grants(id),
 reason TEXT NOT NULL CHECK(reason IN ('superseded','revoked','expired')),
 actor TEXT NOT NULL, at_ms INTEGER NOT NULL CHECK(at_ms BETWEEN 1 AND 9007199254740991)
) STRICT;
CREATE TRIGGER grants_no_update BEFORE UPDATE ON execution_grants BEGIN SELECT RAISE(ABORT,'grants are immutable'); END;
CREATE TRIGGER grants_no_delete BEFORE DELETE ON execution_grants BEGIN SELECT RAISE(ABORT,'grants are immutable'); END;
CREATE TRIGGER invalidations_no_update BEFORE UPDATE ON execution_invalidations BEGIN SELECT RAISE(ABORT,'invalidations are immutable'); END;
CREATE TRIGGER invalidations_no_delete BEFORE DELETE ON execution_invalidations BEGIN SELECT RAISE(ABORT,'invalidations are immutable'); END;
PRAGMA user_version=3;
`

// ApproveExecution is a TRUSTED operator/standing-policy entry point. Actor is an
// audit reference, NOT authentication. Never expose this method to a worker/model
// or HTTP until an authenticated owner checks approval policy. No such endpoint
// exists. Approval grants execute only, never acceptance/publication/merge.
// expectedID is a CAS head (empty on first approval); IDs/revisions are immutable.
func (s *Store) ApproveExecution(ctx context.Context, expectedID string, grant g.Grant) (g.Grant, error) {
	return s.changeExecution(ctx, expectedID, grant, true)
}

// RestrictExecution requires existing live authority and can only narrow it.
// An unchanged envelope returns the existing grant without another approval,
// revision, invalidation or budget reset. It cannot approve a model proposal.
func (s *Store) RestrictExecution(ctx context.Context, expectedID string, grant g.Grant) (g.Grant, error) {
	return s.changeExecution(ctx, expectedID, grant, false)
}

func (s *Store) grantTransaction(ctx context.Context) (*sql.Tx, error) {
	if s.db == nil {
		return nil, g.Deny("store_closed", "store")
	}
	if s.fixture {
		return nil, g.Deny("fixture_only", "store")
	}
	return s.db.BeginTx(ctx, nil)
}

func loadGrant(ctx context.Context, tx *sql.Tx, id string) (g.Grant, error) {
	var body string
	if err := tx.QueryRowContext(ctx, "SELECT body FROM execution_grants WHERE id=?", id).Scan(&body); err != nil {
		return g.Grant{}, err
	}
	grant, err := g.DecodeGrant(body)
	if err == nil && grant.ID != id {
		return g.Grant{}, g.Deny("identity_conflict", "stored_grant")
	}
	return grant, err
}

func headGrant(ctx context.Context, tx *sql.Tx, taskID string) (g.Grant, error) {
	var id string
	if err := tx.QueryRowContext(ctx, "SELECT grant_id FROM execution_grant_heads WHERE task_id=?", taskID).Scan(&id); err != nil {
		return g.Grant{}, err
	}
	return loadGrant(ctx, tx, id)
}

func liveGrant(ctx context.Context, tx *sql.Tx, grant g.Grant, now int64) error {
	var reason string
	err := tx.QueryRowContext(ctx, "SELECT reason FROM execution_invalidations WHERE grant_id=?", grant.ID).Scan(&reason)
	if err == nil {
		return g.Deny(reason, "grant")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if now >= grant.Envelope.ExpiresMS {
		return g.Deny("expired", "grant")
	}
	return nil
}

func invalidateGrant(ctx context.Context, tx *sql.Tx, id, reason, actor string, now int64) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO execution_invalidations(grant_id,reason,actor,at_ms) VALUES(?,?,?,?)", id, reason, actor, now)
	return err
}

func (s *Store) changeExecution(ctx context.Context, expectedID string, grant g.Grant, approval bool) (g.Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := grant.Validate(); err != nil {
		return g.Grant{}, err
	}
	if expectedID != "" && !p.ValidID(expectedID) {
		return g.Grant{}, g.Deny("malformed", "expected_grant")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return g.Grant{}, err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	old, err := headGrant(ctx, tx, grant.TaskID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return g.Grant{}, err
	}
	// Check retained ID before no-op comparison: aliases cannot hide conflicts.
	sameID, idErr := loadGrant(ctx, tx, grant.ID)
	if idErr != nil && !errors.Is(idErr, sql.ErrNoRows) {
		return g.Grant{}, idErr
	}
	if idErr == nil {
		if !reflect.DeepEqual(sameID, grant) {
			return g.Grant{}, g.Deny("identity_conflict", "grant")
		}
		if old.ID != grant.ID {
			return g.Grant{}, g.Deny("superseded", "grant")
		}
		if err := liveGrant(ctx, tx, old, now); err != nil {
			return g.Grant{}, err
		}
		// Replay returns retained authority, never a new admission or renewed budget.
		return old, nil
	}
	if old.ID != expectedID {
		return g.Grant{}, g.Deny("revision_conflict", "expected_grant")
	}
	if old.ID == "" {
		if !approval {
			return g.Grant{}, g.Deny("approval_required", "grant")
		}
		if grant.Revision != 1 {
			return g.Grant{}, g.Deny("revision_conflict", "grant")
		}
	} else {
		liveErr := liveGrant(ctx, tx, old, now)
		var refusal *g.Refusal
		if liveErr != nil && (!approval || !errors.As(liveErr, &refusal)) {
			return g.Grant{}, liveErr
		}
		if old.Revision == p.MaxInteger || grant.Revision != old.Revision+1 {
			return g.Grant{}, g.Deny("revision_conflict", "grant")
		}
		if liveErr == nil && grant.Actor == old.Actor && reflect.DeepEqual(grant.Envelope, old.Envelope) {
			return old, nil
		}
		rows, err := tx.QueryContext(ctx, "SELECT body FROM execution_grants WHERE task_id=? ORDER BY revision DESC", grant.TaskID)
		if err != nil {
			return g.Grant{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var body string
			if err := rows.Scan(&body); err != nil {
				return g.Grant{}, err
			}
			retained, err := g.DecodeGrant(body)
			if err != nil {
				return g.Grant{}, err
			}
			if err := g.Replacement(grant.Envelope, retained.Envelope); err != nil {
				return g.Grant{}, err
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return g.Grant{}, err
		}
		if !approval {
			if grant.Actor != old.Actor {
				return g.Grant{}, g.Deny("approval_required", "actor")
			}
			if err := g.Within(grant.Envelope, old.Envelope); err != nil {
				return g.Grant{}, err
			}
		}
		var invalidated int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM execution_invalidations WHERE grant_id=?", old.ID).Scan(&invalidated); err != nil {
			return g.Grant{}, err
		}
		if invalidated == 0 {
			reason := "superseded"
			if now >= old.Envelope.ExpiresMS {
				reason = "expired"
			}
			if err := invalidateGrant(ctx, tx, old.ID, reason, grant.Actor, now); err != nil {
				return g.Grant{}, err
			}
		}
	}
	if now >= grant.Envelope.ExpiresMS {
		return g.Grant{}, g.Deny("expired", "grant")
	}
	body, err := json.Marshal(grant)
	if err != nil {
		return g.Grant{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO execution_grants VALUES(?,?,?,?,?)", grant.ID, grant.TaskID, grant.Revision, string(body), grant.Envelope.ExpiresMS); err != nil {
		return g.Grant{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO execution_grant_heads VALUES(?,?) ON CONFLICT(task_id) DO UPDATE SET grant_id=excluded.grant_id", grant.TaskID, grant.ID); err != nil {
		return g.Grant{}, err
	}
	if err := tx.Commit(); err != nil {
		return g.Grant{}, err
	}
	return grant, nil
}

// InvalidateExecution latches operator revocation or an unapproved material edit.
// Supersession without replacement holds the task until explicitly reapproved.
// Repeating the exact cause/actor is idempotent; a competing head/cause refuses.
func (s *Store) InvalidateExecution(ctx context.Context, taskID, expectedID, actor, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(taskID) || !p.ValidID(expectedID) || !g.ValidActor(actor) || reason != "revoked" && reason != "superseded" {
		return g.Deny("malformed", "invalidation")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	grant, err := headGrant(ctx, tx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return g.Deny("unknown_grant", "task")
	}
	if err != nil {
		return err
	}
	if grant.ID != expectedID {
		return g.Deny("revision_conflict", "expected_grant")
	}
	var oldReason, oldActor string
	err = tx.QueryRowContext(ctx, "SELECT reason,actor FROM execution_invalidations WHERE grant_id=?", expectedID).Scan(&oldReason, &oldActor)
	if err == nil {
		if oldReason == reason && oldActor == actor {
			return nil
		}
		return g.Deny("identity_conflict", "invalidation")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := invalidateGrant(ctx, tx, grant.ID, reason, actor, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// CheckExecution is an authority check, NOT a dispatch/admission token. #15 must
// call checkExecution in its reservation/attempt/outbox transaction, then validate
// current full-graph capability/data/runner policy. Never check now and dispatch
// later: invalidation could commit between them. No dispatcher exists here.
func (s *Store) CheckExecution(ctx context.Context, request g.Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(request.GrantID) || !p.ValidID(request.TaskID) {
		return g.Deny("malformed", "request")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if err = expireGrants(ctx, tx, now); err != nil {
		return err
	}
	decision := checkExecution(ctx, tx, request, now)
	// Expiry signal is durable even when this request refuses. Commit failure
	// takes precedence: never return a positive check on an uncertain write.
	if err = tx.Commit(); err != nil {
		return err
	}
	return decision
}

func checkExecution(ctx context.Context, tx *sql.Tx, request g.Request, now int64) error {
	grant, err := loadGrant(ctx, tx, request.GrantID)
	if errors.Is(err, sql.ErrNoRows) {
		return g.Deny("unknown_grant", "grant")
	}
	if err != nil {
		return err
	}
	if err = liveGrant(ctx, tx, grant, now); err != nil {
		return err
	}
	current, err := headGrant(ctx, tx, request.TaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return g.Deny("unknown_grant", "task")
	}
	if err != nil {
		return err
	}
	if current.ID != grant.ID {
		return g.Deny("superseded", "grant")
	}
	return g.Check(grant, request, now)
}

func expireGrants(ctx context.Context, tx *sql.Tx, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO execution_invalidations(grant_id,reason,actor,at_ms)
SELECT g.id,'expired','clock',? FROM execution_grants g JOIN execution_grant_heads h ON h.grant_id=g.id
WHERE g.expires_ms<=? AND NOT EXISTS(SELECT 1 FROM execution_invalidations i WHERE i.grant_id=g.id) ORDER BY g.id`, now, now)
	return err
}

// Invalidation is a durable, replayable signal for #19 stop/reconciliation, NOT
// termination evidence. Consumers retain their cursor; no retention TTL exists.
type Invalidation struct {
	Sequence int64  `json:"sequence"`
	GrantID  string `json:"grant_id"`
	TaskID   string `json:"task_id"`
	Revision int64  `json:"revision"`
	Reason   string `json:"reason"`
	Actor    string `json:"actor"`
	AtMS     int64  `json:"at_ms"`
}

func (s *Store) ExecutionInvalidations(ctx context.Context, after int64, limit int) ([]Invalidation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if after < 0 || after > p.MaxInteger || limit < 1 || limit > 128 {
		return nil, g.Deny("malformed", "cursor/limit")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := expireGrants(ctx, tx, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.sequence,i.grant_id,g.task_id,g.revision,i.reason,i.actor,i.at_ms FROM execution_invalidations i JOIN execution_grants g ON g.id=i.grant_id WHERE i.sequence>? ORDER BY i.sequence LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	out := []Invalidation{}
	for rows.Next() {
		var v Invalidation
		if err := rows.Scan(&v.Sequence, &v.GrantID, &v.TaskID, &v.Revision, &v.Reason, &v.Actor, &v.AtMS); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, v)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// ExecutionGrant returns immutable history, including invalidated records. A
// successful read is not authority; CheckExecution always checks the current head.
func (s *Store) ExecutionGrant(ctx context.Context, id string) (g.Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.ValidID(id) {
		return g.Grant{}, g.Deny("malformed", "grant_id")
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return g.Grant{}, err
	}
	defer tx.Rollback()
	grant, err := loadGrant(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return g.Grant{}, g.Deny("unknown_grant", "grant")
	}
	return grant, err
}
