package store

// Reconcile seam (docs/decisions/0002 §8). S5 (#20) replaces the bodies in this
// file only; until then every method returns ErrNotImplemented. Reconcile
// classifies every non-terminal attempt from retained evidence and releases a
// reservation only through releaseDispatchTx with machine-verifiable proof;
// unknown remains a visible, blocking outcome and never releases anything.

import (
	"context"
	"encoding/json"

	p "github.com/korallis/letmecook/schemas/execution"
)

// ReconcileReport mirrors one reconcile_reports row: one Startup, OnHello or
// Sweep run's classifications, keyed by the daemon boot that produced them.
type ReconcileReport struct {
	ID         string          `json:"id"`
	DaemonBoot string          `json:"daemon_boot"`
	CreatedMS  int64           `json:"created_ms"`
	Body       json.RawMessage `json:"body,omitempty"`
}

// FenceAttempt moves an assigned|starting|running|result_pending|unknown attempt to
// stopping when the old-lease barrier has passed without evidence
// (lease_lapsed_unconfirmed) and latches its cancel with cause. Durable point:
// the attempts CAS and its event committed together; the recorded transition is
// returned. It never releases the reservation.
func (s *Store) FenceAttempt(ctx context.Context, attemptID, cause string) (p.Message, error) {
	return p.Message{}, ErrNotImplemented
}

// NonTerminalAttempts lists every attempt outside succeeded|failed|cancelled|
// expired with its dispatch binding, using the attempts_active index.
// Durable point: none (read).
func (s *Store) NonTerminalAttempts(ctx context.Context) ([]AttemptSummary, error) {
	return nil, ErrNotImplemented
}

// ReconcileReport reads one retained report. Durable point: none (read).
func (s *Store) ReconcileReport(ctx context.Context, id string) (ReconcileReport, error) {
	return ReconcileReport{}, ErrNotImplemented
}

// PutReconcileReport retains a report. Durable point: the committed
// reconcile_reports row; reports are immutable and never deleted.
func (s *Store) PutReconcileReport(ctx context.Context, report ReconcileReport) error {
	return ErrNotImplemented
}
