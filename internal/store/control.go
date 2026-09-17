package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	c "github.com/korallis/letmecook/internal/control"
	p "github.com/korallis/letmecook/schemas/execution"
)

var _ c.Backend = (*Store)(nil)

const controlSchema = `
CREATE TABLE control_stops (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, task_id TEXT NOT NULL, attempt_id TEXT NOT NULL, grant_id TEXT NOT NULL,
 body TEXT NOT NULL CHECK(length(body)<=8192)
) STRICT;
CREATE TABLE control_acks (stop_id TEXT PRIMARY KEY REFERENCES control_stops(id), body TEXT NOT NULL CHECK(length(body)<=8192)) STRICT;
CREATE TABLE control_targets (
 stop_id TEXT NOT NULL REFERENCES control_stops(id), attempt_id TEXT NOT NULL REFERENCES attempts(id),
 body TEXT NOT NULL CHECK(length(body)<=8192), PRIMARY KEY(stop_id,attempt_id)
) STRICT;
CREATE TABLE control_leases (
 nonce TEXT PRIMARY KEY, attempt_id TEXT NOT NULL REFERENCES attempts(id), body TEXT NOT NULL CHECK(length(body)<=16384)
) STRICT;
CREATE TABLE control_revocations (
 nonce TEXT PRIMARY KEY REFERENCES control_leases(nonce), stop_id TEXT NOT NULL REFERENCES control_stops(id)
) STRICT;
CREATE TABLE control_fenced (
 message_id TEXT PRIMARY KEY, body TEXT NOT NULL CHECK(length(body)<=8192), reason TEXT NOT NULL, at_ms INTEGER NOT NULL
) STRICT;
CREATE TABLE control_messages (message_id TEXT PRIMARY KEY, body TEXT NOT NULL CHECK(length(body)<=8192)) STRICT;
CREATE TABLE control_observations (
 stop_id TEXT NOT NULL, attempt_id TEXT NOT NULL, body TEXT NOT NULL CHECK(length(body)<=16384),
 PRIMARY KEY(stop_id,attempt_id), FOREIGN KEY(stop_id,attempt_id) REFERENCES control_targets(stop_id,attempt_id)
) STRICT;
CREATE TRIGGER control_stops_no_update BEFORE UPDATE ON control_stops BEGIN SELECT RAISE(ABORT,'stop is sticky'); END;
CREATE TRIGGER control_stops_no_delete BEFORE DELETE ON control_stops BEGIN SELECT RAISE(ABORT,'stop is sticky'); END;
CREATE TRIGGER control_acks_no_update BEFORE UPDATE ON control_acks BEGIN SELECT RAISE(ABORT,'ack is immutable'); END;
CREATE TRIGGER control_acks_no_delete BEFORE DELETE ON control_acks BEGIN SELECT RAISE(ABORT,'ack is retained'); END;
CREATE TRIGGER control_targets_no_update BEFORE UPDATE ON control_targets BEGIN SELECT RAISE(ABORT,'target is immutable'); END;
CREATE TRIGGER control_targets_no_delete BEFORE DELETE ON control_targets BEGIN SELECT RAISE(ABORT,'target is retained'); END;
CREATE TRIGGER control_leases_no_update BEFORE UPDATE ON control_leases BEGIN SELECT RAISE(ABORT,'lease is immutable'); END;
CREATE TRIGGER control_leases_no_delete BEFORE DELETE ON control_leases BEGIN SELECT RAISE(ABORT,'lease is retained'); END;
CREATE TRIGGER control_revocations_no_update BEFORE UPDATE ON control_revocations BEGIN SELECT RAISE(ABORT,'revocation is sticky'); END;
CREATE TRIGGER control_revocations_no_delete BEFORE DELETE ON control_revocations BEGIN SELECT RAISE(ABORT,'revocation is sticky'); END;
CREATE TRIGGER control_fenced_no_update BEFORE UPDATE ON control_fenced BEGIN SELECT RAISE(ABORT,'late evidence is immutable'); END;
CREATE TRIGGER control_fenced_no_delete BEFORE DELETE ON control_fenced BEGIN SELECT RAISE(ABORT,'late evidence is retained'); END;
CREATE TRIGGER control_messages_no_update BEFORE UPDATE ON control_messages BEGIN SELECT RAISE(ABORT,'message identity is immutable'); END;
CREATE TRIGGER control_messages_no_delete BEFORE DELETE ON control_messages BEGIN SELECT RAISE(ABORT,'message identity is retained'); END;
CREATE TRIGGER control_observations_no_update BEFORE UPDATE ON control_observations BEGIN SELECT RAISE(ABORT,'observation is immutable'); END;
CREATE TRIGGER control_observations_no_delete BEFORE DELETE ON control_observations BEGIN SELECT RAISE(ABORT,'observation is retained'); END;
PRAGMA user_version=8;
`

func controlJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func decodeControl[T any](body string, out *T) error {
	if len(body) > 16384 || json.Unmarshal([]byte(body), out) != nil || controlJSON(out) != body {
		return g.Deny("corrupt_record", "control")
	}
	return nil
}

func (s *Store) controlStamp() c.Stamp {
	return s.controlStampAt(s.controlNow())
}

func (s *Store) controlStampAt(now time.Time) c.Stamp {
	return c.Stamp{Boot: s.meta.DaemonBoot, Wall: now.UTC(), ElapsedNS: now.Sub(s.controlStart).Nanoseconds()}
}

func loadStop(ctx context.Context, tx *sql.Tx, id string) (c.Receipt, error) {
	var r c.Receipt
	var body string
	if err := tx.QueryRowContext(ctx, "SELECT body FROM control_stops WHERE id=?", id).Scan(&body); err != nil {
		return r, err
	}
	if err := decodeControl(body, &r); err != nil {
		return r, err
	}
	if r.Request.ID != id || r.Request.Validate() != nil || !p.ValidID(r.Requested.Boot) || r.Requested.ElapsedNS < 0 || r.Requested.Wall.IsZero() {
		return r, g.Deny("corrupt_record", "stop")
	}
	err := tx.QueryRowContext(ctx, "SELECT body FROM control_acks WHERE stop_id=?", id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	var ack c.Acknowledgement
	if err := decodeControl(body, &ack); err != nil {
		return r, err
	}
	if !p.ValidID(ack.At.Boot) || ack.At.ElapsedNS < 0 || ack.At.Wall.IsZero() ||
		(ack.At.Boot == r.Requested.Boot) != (ack.RequestToAckNS != nil) ||
		ack.RequestToAckNS != nil && (*ack.RequestToAckNS < 0 || *ack.RequestToAckNS != ack.At.ElapsedNS-r.Requested.ElapsedNS) {
		return r, g.Deny("corrupt_record", "stop_ack")
	}
	r.Acknowledged = &ack
	return r, nil
}

// latchStop is the single transaction path for operator stops and #12 invalidation.
// It never deletes history, releases reservations or claims process termination.
func latchStop(ctx context.Context, tx *sql.Tx, actor string, request c.Request, at c.Stamp) (c.Receipt, error) {
	if err := request.Validate(); err != nil {
		return c.Receipt{}, err
	}
	old, err := loadStop(ctx, tx, request.ID)
	if err == nil {
		if old.Request != request || old.Actor != actor {
			return c.Receipt{}, p.IdentityConflict
		}
		return old, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c.Receipt{}, err
	}
	r := c.Receipt{Request: request, Actor: actor, Requested: at}
	if _, err := tx.ExecContext(ctx, "INSERT INTO control_stops VALUES(?,?,?,?,?,?)", request.ID, request.Kind, request.TaskID, request.AttemptID, request.GrantID, controlJSON(r)); err != nil {
		return c.Receipt{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id FROM dispatches d JOIN attempts a ON a.id=d.attempt_id
 WHERE a.state NOT IN ('succeeded','failed','cancelled','expired') AND
 (?='global_stop' OR (?='pause_task' AND d.task_id=?) OR (?='cancel_attempt' AND d.task_id=? AND d.attempt_id=?) OR (?='authority_supersession' AND d.grant_id=?)) ORDER BY d.id`, request.Kind, request.Kind, request.TaskID, request.Kind, request.TaskID, request.AttemptID, request.Kind, request.GrantID)
	if err != nil {
		return c.Receipt{}, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return c.Receipt{}, err
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return c.Receipt{}, err
	}
	if request.Kind == c.CancelAttempt && len(ids) == 0 {
		return c.Receipt{}, p.StaleAttempt
	}
	var boot string
	if err := tx.QueryRowContext(ctx, "SELECT daemon_boot FROM metadata WHERE singleton=1").Scan(&boot); err != nil {
		return c.Receipt{}, err
	}
	for _, id := range ids {
		d, err := loadDispatch(ctx, tx, id)
		if err != nil {
			return c.Receipt{}, err
		}
		target := c.Target{DispatchID: id, Cancel: p.Message{Version: p.FencedVersion, Kind: "cancel", MessageID: dispatchID(request.ID, d.Assignment.Identity.AttemptID), Identity: d.Assignment.Identity, StopID: request.ID, RunnerBoot: d.Facts.RunnerBoot, DaemonBoot: boot}}
		if reason := p.CheckSession(target.Cancel, d.Assignment.Identity, p.FencedVersion); reason != p.OK {
			return c.Receipt{}, reason
		}
		if err := controlMessage(ctx, tx, target.Cancel); err != nil {
			return c.Receipt{}, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO control_targets VALUES(?,?,?)", request.ID, d.Assignment.Identity.AttemptID, controlJSON(target)); err != nil {
			return c.Receipt{}, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO control_revocations SELECT nonce,? FROM control_leases WHERE attempt_id=? ON CONFLICT(nonce) DO NOTHING", request.ID, d.Assignment.Identity.AttemptID); err != nil {
			return c.Receipt{}, err
		}
	}
	return r, nil
}

// RequestStop returns a positive acknowledgement only after latch/revocation/outbox
// commit AND persistence of its post-commit measurement. A lost response replays
// that exact measurement. A crash before measurement leaves durable request-only
// history; another boot records an explicitly unknown request-to-ack duration.
func (s *Store) RequestStop(ctx context.Context, actor string, request c.Request) (c.Receipt, error) {
	received := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return c.Receipt{}, err
	}
	defer tx.Rollback()
	if err := owner(ctx, tx, actor); err != nil {
		return c.Receipt{}, err
	}
	if request.Kind == c.AuthoritySupersession {
		return c.Receipt{}, g.Deny("invalidation_required", "stop")
	}
	r, err := latchStop(ctx, tx, actor, request, s.controlStampAt(received))
	if err != nil {
		return c.Receipt{}, err
	}
	if s.controlHook != nil {
		if err := s.controlHook("before_latch_commit"); err != nil {
			return c.Receipt{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return c.Receipt{}, err
	}
	if s.controlHook != nil {
		if err := s.controlHook("after_latch_commit"); err != nil {
			return c.Receipt{}, err
		}
	}
	if r.Acknowledged != nil {
		return r, nil
	}
	ack := c.Acknowledgement{At: s.controlStamp()}
	if ack.At.Boot == r.Requested.Boot {
		elapsed := ack.At.ElapsedNS - r.Requested.ElapsedNS
		if elapsed < 0 {
			return c.Receipt{}, p.ReconciliationRequired
		}
		ack.RequestToAckNS = &elapsed
	}
	tx, err = s.grantTransaction(ctx)
	if err != nil {
		return c.Receipt{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO control_acks VALUES(?,?)", request.ID, controlJSON(ack)); err != nil {
		return c.Receipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return c.Receipt{}, err
	}
	r.Acknowledged = &ack
	return r, nil
}

func controlSuppressed(ctx context.Context, tx *sql.Tx, task, grant string) (bool, error) {
	var stopped bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM control_stops s WHERE
 `+rcActiveStopFilter+` AND
 (kind='global_stop' OR (kind='pause_task' AND task_id=?) OR
 (kind='cancel_attempt' AND task_id=?) OR
 (kind='authority_supersession' AND (grant_id=? OR (task_id=? AND EXISTS(SELECT 1 FROM control_targets t WHERE t.stop_id=s.id))))))`, task, task, grant, task).Scan(&stopped)
	return stopped, err
}

func controlResultFenced(ctx context.Context, tx *sql.Tx, identity p.Identity) (bool, error) {
	var grant string
	err := tx.QueryRowContext(ctx, "SELECT grant_id FROM dispatches WHERE attempt_id=? AND task_id=?", identity.AttemptID, identity.TaskID).Scan(&grant)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	stopped, err := controlSuppressed(ctx, tx, identity.TaskID, grant)
	if err != nil || stopped {
		return stopped, err
	}
	stopped, err = dispatchStopped(ctx, tx, identity.TaskID)
	if err != nil || stopped {
		return stopped, err
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM execution_invalidations WHERE grant_id=?) OR
 EXISTS(SELECT 1 FROM execution_grants WHERE id=? AND expires_ms<=?)`, grant, grant, time.Now().UnixMilli()).Scan(&stopped)
	return stopped, err
}

// latchInvalidations consumes the existing #12 invalidation journal in the SAME
// transaction as invalidation. Stable IDs make startup/backfill and replay safe.
func latchInvalidations(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT i.grant_id,g.task_id,i.reason,i.actor,i.at_ms FROM execution_invalidations i JOIN execution_grants g ON g.id=i.grant_id WHERE NOT EXISTS(SELECT 1 FROM control_stops s WHERE s.grant_id=i.grant_id AND s.kind='authority_supersession') ORDER BY i.sequence`)
	if err != nil {
		return err
	}
	var pending []Invalidation
	for rows.Next() {
		var v Invalidation
		if err := rows.Scan(&v.GrantID, &v.TaskID, &v.Reason, &v.Actor, &v.AtMS); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, v)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	var boot string
	if err := tx.QueryRowContext(ctx, "SELECT daemon_boot FROM metadata WHERE singleton=1").Scan(&boot); err != nil {
		return err
	}
	for _, v := range pending {
		request := c.Request{ID: dispatchID(v.GrantID, "authority-stop"), Kind: c.AuthoritySupersession, TaskID: v.TaskID, GrantID: v.GrantID, Cause: v.Reason}
		// Invalidation already owns the durable request timestamp. No fabricated
		// monotonic acknowledgement is exposed for this internally generated latch.
		if _, err := latchStop(ctx, tx, v.Actor, request, c.Stamp{Boot: boot, Wall: time.UnixMilli(v.AtMS).UTC()}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) StopTargets(ctx context.Context, stopID string) ([]c.Target, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := loadStop(ctx, tx, stopID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT body FROM control_targets WHERE stop_id=? ORDER BY attempt_id", stopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []c.Target{}
	for rows.Next() {
		var body string
		var target c.Target
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := decodeControl(body, &target); err != nil {
			return nil, err
		}
		if target.Cancel.StopID != stopID || p.CheckSession(target.Cancel, target.Cancel.Identity, p.FencedVersion) != p.OK {
			return nil, g.Deny("corrupt_record", "target")
		}
		out = append(out, target)
	}
	return out, rows.Err()
}

// StopRequests discovers retained latch/outbox IDs after restart. Cursor is a
// stable UUID, not a deletable queue offset. Reading history grants no delivery.
func (s *Store) StopRequests(ctx context.Context, after string, limit int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if after != "" && !p.ValidID(after) || limit < 1 || limit > 128 {
		return nil, p.Malformed
	}
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id FROM control_stops WHERE id>? ORDER BY id LIMIT ?", after, limit)
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
		if !p.ValidID(id) {
			return nil, g.Deny("corrupt_record", "stop_id")
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func controlFence(ctx context.Context, tx *sql.Tx, m p.Message, reason string) error {
	if err := controlMessage(ctx, tx, m); err != nil {
		return err
	}
	body := controlJSON(m)
	var old, oldReason string
	err := tx.QueryRowContext(ctx, "SELECT body,reason FROM control_fenced WHERE message_id=?", m.MessageID).Scan(&old, &oldReason)
	if err == nil {
		if old != body {
			return p.IdentityConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO control_fenced VALUES(?,?,?,?)", m.MessageID, body, reason, time.Now().UnixMilli())
	return err
}

// One retained semantic body per message ID, including across control kinds and
// the existing assignment/event/ack namespaces. Changing a refusal cause later
// does not rewrite the original fenced receipt or create another semantic message.
func controlMessage(ctx context.Context, tx *sql.Tx, m p.Message) error {
	body := controlJSON(m)
	rows, err := tx.QueryContext(ctx, `SELECT message FROM events WHERE message_id=? UNION ALL SELECT body FROM dispatch_acks WHERE message_id=? UNION ALL SELECT body FROM control_messages WHERE message_id=?`, m.MessageID, m.MessageID, m.MessageID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var old string
		if err := rows.Scan(&old); err != nil {
			rows.Close()
			return err
		}
		if old != body {
			rows.Close()
			return p.IdentityConflict
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO control_messages VALUES(?,?) ON CONFLICT(message_id) DO NOTHING", m.MessageID, body)
	return err
}

func validControlLease(v c.Lease) bool {
	return p.ValidID(v.DispatchID) && v.Request.Kind == "lease_request" && v.Reply.Kind == "lease_reply" &&
		p.CheckSession(v.Request, v.Request.Identity, p.FencedVersion) == p.OK && p.CheckSession(v.Reply, v.Request.Identity, p.FencedVersion) == p.OK &&
		v.Request.Nonce == v.Reply.Nonce && v.Request.RunnerBoot == v.Reply.RunnerBoot && v.Request.DaemonBoot == v.Reply.DaemonBoot &&
		v.Issued.Boot == v.Reply.DaemonBoot && !v.Issued.Wall.IsZero() && v.Issued.ElapsedNS >= 0 &&
		v.DeadlineNS > v.Issued.ElapsedNS && v.DeadlineNS-v.Issued.ElapsedNS == *v.Reply.ValidityMS*int64(time.Millisecond) && v.MarginNS > 0 && v.MarginNS <= int64(time.Minute)
}

func lastControlLease(ctx context.Context, tx *sql.Tx, attempt string) (c.Lease, error) {
	var v c.Lease
	// Insertion order, never a runner-chosen timestamp, defines the latest issuance.
	var body string
	if err := tx.QueryRowContext(ctx, "SELECT body FROM control_leases WHERE attempt_id=? ORDER BY rowid DESC LIMIT 1", attempt).Scan(&body); err != nil {
		return v, err
	}
	err := decodeControl(body, &v)
	if err == nil && (!validControlLease(v) || v.Request.Identity.AttemptID != attempt) {
		err = g.Deny("corrupt_record", "lease")
	}
	return v, err
}

// RecordControlLease records potential issuance before reply delivery. This
// provisional owner-only import is NOT an issuance service or execution token.
// Margin must be explicitly supplied (0 < margin <= 1 minute); no measured runtime
// default exists. Every renewal rechecks admission and the previous full deadline.
func (s *Store) RecordControlLease(ctx context.Context, actor, dispatchKey, selected string, request, reply p.Message, margin time.Duration) (c.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return c.Lease{}, err
	}
	defer tx.Rollback()
	if err := owner(ctx, tx, actor); err != nil {
		return c.Lease{}, err
	}
	v, err := s.issueLeaseTx(ctx, tx, actor, dispatchKey, selected, request, reply, margin)
	if !leaseCommitted(err) {
		return c.Lease{}, err
	}
	// Issuance, exact replay and fenced refusals are durable before they are reported.
	if commitErr := tx.Commit(); commitErr != nil {
		return c.Lease{}, commitErr
	}
	return v, err
}

// FenceControlMessage retains late result/renewal metadata before returning a
// refusal. Result bytes have independent custody; this never acknowledges them or
// selects/accepts a result. Even current results await the separate acceptance owner.
func (s *Store) FenceControlMessage(ctx context.Context, fingerprint, selected string, m p.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	who, err := principal(ctx, tx, fingerprint)
	if err != nil {
		return err
	}
	var id string
	if err := tx.QueryRowContext(ctx, "SELECT id FROM dispatches WHERE attempt_id=?", m.Identity.AttemptID).Scan(&id); err != nil {
		return err
	}
	d, err := loadDispatch(ctx, tx, id)
	if err != nil {
		return err
	}
	if who.Role != "runner" || who.ID != d.Facts.Repository.RunnerRoot.RunnerID {
		return g.Deny("runner_disabled", "control")
	}
	if r := p.CheckSession(m, d.Assignment.Identity, selected); r != p.OK {
		return r
	}
	if m.Identity.Generation != s.meta.Generation {
		return p.StaleGeneration
	}
	if m.Kind != "result" && m.Kind != "lease_request" {
		return p.Malformed
	}
	if m.Kind == "lease_request" && (m.RunnerBoot != d.Facts.RunnerBoot || m.DaemonBoot != s.meta.DaemonBoot) {
		if err := controlFence(ctx, tx, m, "boot_mismatch"); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return p.BootMismatch
	}
	if err := expireGrants(ctx, tx, time.Now().UnixMilli()); err != nil {
		return err
	}
	reason := "acceptance_unavailable"
	if err := dispatchAllowed(ctx, tx, d.Request, time.Now().UnixMilli()); err != nil {
		var refusal *g.Refusal
		if !errors.As(err, &refusal) && !errors.Is(err, c.ErrFenced) {
			return err
		}
		reason = "revoked_or_expired"
	}
	if err := controlFence(ctx, tx, m, reason); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if reason == "revoked_or_expired" {
		return c.ErrFenced
	}
	return p.ReconciliationRequired
}

func (s *Store) StopStatus(ctx context.Context, stopID, attemptID string) (c.View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return c.View{}, err
	}
	defer tx.Rollback()
	r, err := loadStop(ctx, tx, stopID)
	if err != nil {
		return c.View{}, err
	}
	v := c.View{Receipt: r, Status: c.StopRequested, ConfirmedProcess: "unknown", RemoteWork: "unknown", Quarantined: true}
	if r.Acknowledged != nil {
		v.Status = c.StopAcknowledged
	}
	if attemptID == "" {
		return v, nil
	}
	var body string
	if err := tx.QueryRowContext(ctx, "SELECT body FROM control_targets WHERE stop_id=? AND attempt_id=?", stopID, attemptID).Scan(&body); err != nil {
		return c.View{}, err
	}
	if err := decodeControl(body, &v.Target); err != nil {
		return c.View{}, err
	}
	if v.Target.Cancel.StopID != stopID || v.Target.Cancel.Identity.AttemptID != attemptID || v.Target.Cancel.Kind != "cancel" || p.CheckSession(v.Target.Cancel, v.Target.Cancel.Identity, p.FencedVersion) != p.OK {
		return c.View{}, g.Deny("corrupt_record", "target")
	}
	v.Status = c.TerminationUnconfirmed
	expired, err := controlExpiry(ctx, tx, attemptID, s.controlStamp())
	if err != nil {
		return c.View{}, err
	}
	if expired {
		v.Status = c.TerminationExpired
	}
	err = tx.QueryRowContext(ctx, "SELECT body FROM control_observations WHERE stop_id=? AND attempt_id=?", stopID, attemptID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return c.View{}, err
	}
	var evidence c.Evidence
	if err := decodeControl(body, &evidence); err != nil {
		return c.View{}, err
	}
	if !validControlEvidence(evidence, v.Target) {
		return c.View{}, g.Deny("corrupt_record", "observation")
	}
	v.Evidence = &evidence
	// Old-boot observations remain history, not current confirmation.
	if evidence.Terminated.DaemonBoot == s.meta.DaemonBoot {
		v.Status = c.TerminationObserved
		v.ConfirmedProcess = evidence.Terminated.ConfirmedProcess
		v.RemoteWork = evidence.Terminated.RemoteWork
	}
	return v, nil
}

// Every potentially delivered issuance contributes, even a lost reply. A later,
// shorter lease or smaller margin cannot shorten an earlier conservative bound.
func controlExpiry(ctx context.Context, tx *sql.Tx, attempt string, now c.Stamp) (bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT body FROM control_leases WHERE attempt_id=?", attempt)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	barrier := int64(-1)
	for rows.Next() {
		var body string
		var lease c.Lease
		if err := rows.Scan(&body); err != nil {
			return false, err
		}
		if err := decodeControl(body, &lease); err != nil {
			return false, err
		}
		if !validControlLease(lease) || lease.Request.Identity.AttemptID != attempt {
			return false, g.Deny("corrupt_record", "lease")
		}
		deadline := lease.DeadlineNS
		// Old monotonic domains are unusable; wait full maximum validity from
		// this store's exclusive reopen, with each retained configured margin.
		if lease.Issued.Boot != now.Boot {
			deadline = int64(30 * time.Second)
		}
		barrier = max(barrier, deadline+lease.MarginNS)
	}
	return barrier >= 0 && now.ElapsedNS > barrier, rows.Err()
}

func validControlEvidence(e c.Evidence, target c.Target) bool {
	m := e.Terminated
	return e.Measurement.Validate(true) == nil && m.Kind == "terminated" && m.StopID == target.Cancel.StopID &&
		p.CheckSession(m, target.Cancel.Identity, p.FencedVersion) == p.OK && m.RunnerBoot == target.Cancel.RunnerBoot && m.DaemonBoot == target.Cancel.DaemonBoot
}

// ObserveTermination is owner-verified evidence import, not an untrusted runner
// endpoint. The caller verifies digest and complete containment before this call.
func (s *Store) ObserveTermination(ctx context.Context, actor, selected string, evidence c.Evidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.grantTransaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := owner(ctx, tx, actor); err != nil {
		return err
	}
	if _, err := s.observeTerminationTx(ctx, tx, selected, evidence); err != nil {
		return err
	}
	return tx.Commit()
}
