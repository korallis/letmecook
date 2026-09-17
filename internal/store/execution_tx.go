package store

// Transaction cores shared by the owner-only imports in control.go/dispatch.go and
// the S1 execution channel (execution.go). Each helper runs inside the caller's
// transaction and never commits: the caller decides what becomes durable, and a
// fenced refusal must be committed before it is reported. Factored without
// behaviour change from RecordControlLease, ObserveTermination and ReconcileDispatch
// so no later slice edits control.go or dispatch.go.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	c "github.com/korallis/letmecook/internal/control"
	p "github.com/korallis/letmecook/schemas/execution"
)

// leaseCommitted reports whether an issueLeaseTx outcome carries durable state the
// caller must commit even though it is a refusal (a fenced request receipt).
func leaseCommitted(err error) bool {
	return err == nil || errors.Is(err, p.BootMismatch) || errors.Is(err, c.ErrFenced)
}

// issueLeaseTx records a potentially delivered lease reply. provenance is the
// authenticated caller's credential fingerprint: RecordControlLease passes the
// verified owner, the S1 lease service passes the runner, and a runner may only
// obtain leases for the dispatch bound to its own principal. Every renewal rechecks
// admission and the previous full deadline; a lapsed prior lease latches a
// lease_expired stop (actor "lease-clock") before the request is fenced.
func (s *Store) issueLeaseTx(ctx context.Context, tx *sql.Tx, provenance, dispatchKey, selected string, request, reply p.Message, margin time.Duration) (c.Lease, error) {
	d, err := loadDispatch(ctx, tx, dispatchKey)
	if err != nil {
		return c.Lease{}, err
	}
	who, err := principal(ctx, tx, provenance)
	if err != nil {
		return c.Lease{}, err
	}
	if who.Role == "runner" && (!who.Enabled || who.ID != d.Facts.Repository.RunnerRoot.RunnerID) {
		return c.Lease{}, g.Deny("runner_disabled", "lease")
	}
	for _, m := range []p.Message{request, reply} {
		if r := p.CheckSession(m, d.Assignment.Identity, selected); r != p.OK {
			return c.Lease{}, r
		}
	}
	if request.Kind != "lease_request" || reply.Kind != "lease_reply" || request.Nonce != reply.Nonce || margin <= 0 || margin > time.Minute {
		return c.Lease{}, p.Malformed
	}
	if request.DaemonBoot != s.meta.DaemonBoot || reply.DaemonBoot != request.DaemonBoot || request.RunnerBoot != d.Facts.RunnerBoot || reply.RunnerBoot != request.RunnerBoot {
		if err := controlFence(ctx, tx, request, "boot_mismatch"); err != nil {
			return c.Lease{}, err
		}
		return c.Lease{}, p.BootMismatch
	}
	if err := expireGrants(ctx, tx, time.Now().UnixMilli()); err != nil {
		return c.Lease{}, err
	}
	now := s.controlStamp()
	decision := dispatchAllowed(ctx, tx, d.Request, now.Wall.UnixMilli())
	if decision != nil {
		var refusal *g.Refusal
		if !errors.As(decision, &refusal) {
			return c.Lease{}, decision
		}
	}
	prior, priorErr := lastControlLease(ctx, tx, request.Identity.AttemptID)
	if priorErr != nil && !errors.Is(priorErr, sql.ErrNoRows) {
		return c.Lease{}, priorErr
	}
	if priorErr == nil && (prior.Issued.Boot != now.Boot || now.ElapsedNS < prior.Issued.ElapsedNS || now.ElapsedNS >= prior.DeadlineNS) {
		stop := c.Request{ID: dispatchID(prior.Request.Nonce, "expired"), Kind: c.CancelAttempt, TaskID: request.Identity.TaskID, AttemptID: request.Identity.AttemptID, Cause: "lease_expired"}
		if _, err := latchStop(ctx, tx, "lease-clock", stop, now); err != nil {
			return c.Lease{}, err
		}
		decision = c.ErrFenced
	}
	if decision != nil {
		if err := controlFence(ctx, tx, request, "revoked_or_expired"); err != nil {
			return c.Lease{}, err
		}
		return c.Lease{}, c.ErrFenced
	}
	var body string
	err = tx.QueryRowContext(ctx, "SELECT body FROM control_leases WHERE nonce=?", request.Nonce).Scan(&body)
	if err == nil {
		var old c.Lease
		if err := decodeControl(body, &old); err != nil {
			return c.Lease{}, err
		}
		if !validControlLease(old) {
			return c.Lease{}, g.Deny("corrupt_record", "lease")
		}
		if old.DispatchID != dispatchKey || !reflect.DeepEqual(old.Request, request) || !reflect.DeepEqual(old.Reply, reply) || old.MarginNS != int64(margin) {
			return c.Lease{}, p.IdentityConflict
		}
		return old, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c.Lease{}, err
	}
	if !d.Acknowledged || d.Released {
		return c.Lease{}, p.ReconciliationRequired
	}
	var state p.AttemptState
	if err := tx.QueryRowContext(ctx, "SELECT state FROM attempts WHERE id=?", request.Identity.AttemptID).Scan(&state); err != nil {
		return c.Lease{}, err
	}
	if state != p.Assigned && state != p.Starting && state != p.Running {
		return c.Lease{}, p.ReconciliationRequired
	}
	v := c.Lease{DispatchID: dispatchKey, Request: request, Reply: reply, Issued: now, DeadlineNS: now.ElapsedNS + *reply.ValidityMS*int64(time.Millisecond), MarginNS: int64(margin)}
	if !validControlLease(v) {
		return c.Lease{}, p.ReconciliationRequired
	}
	for _, m := range []p.Message{request, reply} {
		if err := controlMessage(ctx, tx, m); err != nil {
			return c.Lease{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO control_leases VALUES(?,?,?)", request.Nonce, request.Identity.AttemptID, controlJSON(v)); err != nil {
		return c.Lease{}, err
	}
	return v, nil
}

// observeTerminationTx validates and retains termination evidence for a latched
// stop target: exact target, current generation and boots, complete measurement.
// An identical replay is a no-op; a changed body is IdentityConflict. It returns
// the target so a caller may release the dispatch in the same transaction. It
// never releases anything itself; remote uncertainty stays recorded.
func (s *Store) observeTerminationTx(ctx context.Context, tx *sql.Tx, selected string, evidence c.Evidence) (c.Target, error) {
	m := evidence.Terminated
	if m.Kind != "terminated" || evidence.Measurement.Validate(true) != nil {
		return c.Target{}, p.Malformed
	}
	var body string
	if err := tx.QueryRowContext(ctx, "SELECT body FROM control_targets WHERE stop_id=? AND attempt_id=?", m.StopID, m.Identity.AttemptID).Scan(&body); err != nil {
		return c.Target{}, err
	}
	var target c.Target
	if err := decodeControl(body, &target); err != nil {
		return c.Target{}, err
	}
	if r := p.CheckSession(m, target.Cancel.Identity, selected); r != p.OK {
		return c.Target{}, r
	}
	if m.Identity.Generation != s.meta.Generation {
		return c.Target{}, p.StaleGeneration
	}
	if m.RunnerBoot != target.Cancel.RunnerBoot || m.DaemonBoot != target.Cancel.DaemonBoot || m.DaemonBoot != s.meta.DaemonBoot {
		return c.Target{}, p.BootMismatch
	}
	if !validControlEvidence(evidence, target) {
		return c.Target{}, p.Malformed
	}
	if err := controlMessage(ctx, tx, m); err != nil {
		return c.Target{}, err
	}
	err := tx.QueryRowContext(ctx, "SELECT body FROM control_observations WHERE stop_id=? AND attempt_id=?", m.StopID, m.Identity.AttemptID).Scan(&body)
	if err == nil {
		if body != controlJSON(evidence) {
			return c.Target{}, p.IdentityConflict
		}
		return target, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c.Target{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO control_observations VALUES(?,?,?)", m.StopID, m.Identity.AttemptID, controlJSON(evidence)); err != nil {
		return c.Target{}, err
	}
	return target, nil
}

// validReconciliation is the evidence shape a release must carry: fenced launch,
// preserved artifacts, quiescent remote work and a confirmed process outcome.
func validReconciliation(proof Reconciliation) error {
	if !p.ValidID(proof.DispatchID) || (proof.To != p.Cancelled && proof.To != p.Expired) || !proof.LaunchFenced || !proof.ArtifactsPreserved || proof.RemoteWork != "quiescent" || (proof.ConfirmedProcess != "not_started" && proof.ConfirmedProcess != "terminated") || len(proof.EvidenceDigest) != 64 || strings.Trim(proof.EvidenceDigest, "0123456789abcdef") != "" {
		return g.Deny("reconciliation_required", "evidence")
	}
	return nil
}

// releaseDispatchTx moves stopping/unknown to cancelled/expired with the terminal
// CAS, its event and the immutable dispatch_releases receipt in one transaction.
// actor is the releasing caller's credential fingerprint (owner for
// ReconcileDispatch, runner principal for the S1/S5 evidence paths); its principal
// is recorded on the release. Budget charges stay consumed. An identical replay is
// a no-op; a changed proof is IdentityConflict.
func (s *Store) releaseDispatchTx(ctx context.Context, tx *sql.Tx, actor string, proof Reconciliation) error {
	if err := validReconciliation(proof); err != nil {
		return err
	}
	// Protocol validation below also validates full identity and numeric bounds.
	m := p.Message{Version: p.FencedVersion, Kind: "transition", MessageID: dispatchID(proof.DispatchID, "terminal"), Identity: proof.Identity, ExpectedRevision: &proof.ExpectedRevision, To: proof.To}
	v, err := loadDispatch(ctx, tx, proof.DispatchID)
	if err != nil {
		return err
	}
	if proof.Identity != v.Assignment.Identity || proof.Identity.Generation != s.meta.Generation {
		return p.StaleAttempt
	}
	raw, err := json.Marshal(proof)
	if err != nil {
		return err
	}
	body := string(raw)
	var old string
	err = tx.QueryRowContext(ctx, "SELECT body FROM dispatch_releases WHERE dispatch_id=?", proof.DispatchID).Scan(&old)
	if err == nil {
		if old != body {
			return p.IdentityConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, "SELECT state,revision FROM attempts WHERE id=?", proof.Identity.AttemptID).Scan(&m.From, &revision); err != nil {
		return err
	}
	if r := p.CheckTransition(m, proof.Identity, m.From, revision); r != p.OK {
		return r
	}
	if _, err := tx.ExecContext(ctx, "UPDATE attempts SET state=?,revision=revision+1 WHERE id=? AND revision=?", proof.To, proof.Identity.AttemptID, revision); err != nil {
		return err
	}
	if err := record(ctx, tx, m, revision+1); err != nil {
		return err
	}
	who, err := principal(ctx, tx, actor)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO dispatch_releases VALUES(?,?,?)", proof.DispatchID, body, who.ID)
	return err
}
