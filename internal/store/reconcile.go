package store

// Reconcile seam (docs/decisions/0002 §8). S5 (#20) replaces the bodies in this
// file only; until then every method returns ErrNotImplemented. Reconcile
// classifies every non-terminal attempt from retained evidence and releases a
// reservation only through releaseDispatchTx with machine-verifiable proof;
// unknown remains a visible, blocking outcome and never releases anything.

import (
	"context"
	"encoding/json"

	c "github.com/korallis/letmecook/internal/control"
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
// the latest lease and whether the old-lease barrier has passed, the cancel
// targets and any termination observations, runtime observations, the custody
// receipt (if any) and whether it is quarantined, the task's result head and the
// runner's last session. Absent parts are omitted or zero values, never null; a
// present Dispatch always carries validated, non-nil envelope sets.
type ReconciliationInputs struct {
	Dispatch            Dispatch             `json:"dispatch,omitzero"`
	Acknowledged        bool                 `json:"acknowledged"`
	LastLease           c.Lease              `json:"last_lease,omitzero"`
	LeaseBarrierPassed  bool                 `json:"lease_barrier_passed"`
	StopTargets         []c.Target           `json:"stop_targets,omitempty"`
	Observations        []c.Evidence         `json:"observations,omitempty"`
	RuntimeObservations []RuntimeObservation `json:"runtime_observations,omitempty"`
	Receipt             p.Receipt            `json:"receipt,omitzero"`
	Quarantined         bool                 `json:"quarantined"`
	Head                ResultHead           `json:"head,omitzero"`
	LastSession         SessionRecord        `json:"last_session,omitzero"`
}

// ReconciliationInputs reads the classification inputs for one attempt in one
// read transaction. Durable point: none (read); reading never releases.
func (s *Store) ReconciliationInputs(ctx context.Context, attemptID string) (ReconciliationInputs, error) {
	return ReconciliationInputs{}, ErrNotImplemented
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
