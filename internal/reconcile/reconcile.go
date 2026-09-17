// Package reconcile classifies every non-terminal attempt from retained
// evidence before any retry (docs/decisions/0002 §8, issue #20). It runs at
// daemon startup before the listeners open, inside POST /x/v1/session after the
// runner's hello is durable, and on a 5 s sweep. Each run reads one snapshot per
// attempt (store.ReconciliationInputs), decides a classification, asks the store
// to act through an evidence-typed writer that re-verifies the evidence inside
// its own transaction, and retains the outcome as an immutable report.
//
// Releases need machine-verifiable evidence: an unacknowledged refusal, the
// durable absence of any launch capability, or a supervisor termination report
// whose remote work is quiescent (from this boot, or from another boot once the
// replacement barrier has elapsed). A lapsed lease without evidence fences the
// attempt and blocks; remote_work unknown blocks; unknown is visible and
// blocking. PlanRetry yields a new dispatch request only for a released
// reservation under a live grant with headroom and no active latch; automatic
// retry is opt-in and limited to infrastructure causes.
package reconcile

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

// ErrNotImplemented is retained for callers that tolerated the S0 stub; no
// function in this package returns it any more.
var ErrNotImplemented = errors.New("reconcile_not_implemented")

// ErrNoStore is returned by PlanRetry when Deps carries no store: an empty
// dispatch request must never look like a plan.
var ErrNoStore = errors.New("reconcile: no store")

// Deps is the daemon composition: the store, the wall clock and the operator's
// --auto-retry opt-in (default off).
type Deps struct {
	Store     *store.Store
	Now       func() time.Time
	AutoRetry bool
}

// Classifications persisted in reconcile_reports (record §8) plus the labels
// this package adds: awaiting_evidence (nothing provable yet, not blocking),
// stale_generation (an attempt of another generation after restore; blocking),
// latch_cleared and the retry outcomes.
const (
	RefusedBeforeAccept                 = "refused_before_accept"
	AssignedUndelivered                 = "assigned_undelivered"
	TerminatedConfirmed                 = "terminated_confirmed"
	TerminatedOldBoot                   = "terminated_old_boot"
	LeaseLapsedUnconfirmed              = "lease_lapsed_unconfirmed"
	RemoteWorkUnknown                   = "remote_work_unknown"
	CustodyCommittedPendingFinalization = "custody_committed_pending_finalization"
	ResultPendingRemote                 = "result_pending_remote"
	JournalCorrupt                      = "journal_corrupt"
	AwaitingEvidence                    = "awaiting_evidence"
	StaleGeneration                     = "stale_generation"
	LatchCleared                        = "latch_cleared"
	RetryPlanned                        = "retry_planned"
	RetryDispatched                     = "retry_dispatched"
	RetryRefused                        = "retry_refused"
)

// Causes recorded on entries and consulted by the auto-retry allowlist.
const (
	CauseLeaseExpired    = "lease_expired"
	CauseDaemonRestart   = "daemon_restart"
	CauseRunnerRestarted = "runner_restarted"
	CauseHarnessCrash    = "harness_crash"
	CauseRefused         = "refused"
	CauseOperator        = "operator"
)

// autoRetryCauses is the closed allowlist --auto-retry acts on. failed, rejected,
// refused, operator stops, remote_work unknown and authority refusals are never
// retried automatically.
var autoRetryCauses = map[string]bool{CauseHarnessCrash: true, CauseLeaseExpired: true, CauseRunnerRestarted: true, CauseDaemonRestart: true}

// AutoRetryable reports whether --auto-retry may act on a cause.
func AutoRetryable(cause string) bool { return autoRetryCauses[cause] }

// Entry is one attempt's classification in a report. State is the attempt state
// after this run acted; Released is true when this run released the
// reservation; ActionRequired marks a blocking outcome that needs the owner.
type Entry struct {
	AttemptID      string         `json:"attempt_id"`
	TaskID         string         `json:"task_id,omitempty"`
	Epoch          int64          `json:"epoch,omitempty"`
	State          p.AttemptState `json:"state,omitempty"`
	Classification string         `json:"classification"`
	Cause          string         `json:"cause,omitempty"`
	ActionRequired bool           `json:"action_required"`
	Released       bool           `json:"released,omitempty"`
	Detail         string         `json:"detail"`
}

// Report is one run's retained outcome. Trigger is startup|hello|sweep|retry;
// RunnerID names the runner whose hello triggered it.
type Report struct {
	ID         string  `json:"id"`
	DaemonBoot string  `json:"daemon_boot"`
	CreatedMS  int64   `json:"created_ms"`
	Trigger    string  `json:"trigger,omitempty"`
	RunnerID   string  `json:"runner_id,omitempty"`
	Entries    []Entry `json:"entries"`
}

// Reader exposes a persisted report without granting mutation authority.
type Reader interface {
	Read(ctx context.Context) (Report, error)
}

// StoreReader reads the latest retained report; an empty store yields an empty
// report, never an error.
type StoreReader struct{ Store *store.Store }

// NewReader returns the Reader the owner API serves GET /api/v1/reconcile from.
func NewReader(s *store.Store) Reader { return StoreReader{Store: s} }

func (r StoreReader) Read(ctx context.Context) (Report, error) {
	if r.Store == nil {
		return Report{}, ErrNoStore
	}
	row, err := r.Store.LatestReconcileReport(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Report{Entries: []Entry{}}, nil
	}
	if err != nil {
		return Report{}, err
	}
	return decodeReport(row)
}

func decodeReport(row store.ReconcileReport) (Report, error) {
	var v Report
	if err := json.Unmarshal(row.Body, &v); err != nil {
		return Report{}, g.Deny("corrupt_record", "reconcile_report")
	}
	if v.Entries == nil {
		v.Entries = []Entry{}
	}
	return v, nil
}

// Startup classifies every non-terminal attempt after store.Open and before the
// listeners open: unknown attempts recovered from the previous boot are released
// or finalized only on retained evidence, otherwise they stay visible and
// blocking. Eligible cancel latches are cleared and, with --auto-retry, released
// infrastructure failures are re-dispatched. The report is always retained.
func Startup(ctx context.Context, d Deps) (Report, error) {
	if d.Store == nil {
		return Report{}, nil
	}
	return run(ctx, d, "startup", "", nil)
}

// OnHello runs inside POST /x/v1/session after the session row is durable: the
// runner's journals mark corrupt attempts and confirm preserved results, and the
// runner's non-terminal attempts are classified. The report is always retained.
func OnHello(ctx context.Context, d Deps, runnerID string, h execwire.Hello) (Report, error) {
	if d.Store == nil {
		return Report{}, nil
	}
	journals := map[string]execwire.Journal{}
	for _, j := range h.Journals {
		journals[j.DispatchID] = j
	}
	return run(ctx, d, "hello", runnerID, journals)
}

// Sweep is the 5 s pass: it re-classifies every non-terminal attempt, clears
// eligible latches and retains a report only when the outcome changed since the
// last retained report of this boot.
func Sweep(ctx context.Context, d Deps) (Report, error) {
	if d.Store == nil {
		return Report{}, nil
	}
	return run(ctx, d, "sweep", "", nil)
}

// RunSweeps calls Sweep every interval (5 s when non-positive) until ctx ends;
// a failed sweep is logged and retried on the next tick. It returns ctx.Err().
func RunSweeps(ctx context.Context, d Deps, interval time.Duration) error {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := Sweep(ctx, d); err != nil && ctx.Err() == nil {
				log.Printf("reconcile sweep: %v", err)
			}
		}
	}
}

func (d Deps) now() time.Time {
	if d.Now == nil {
		return time.Now()
	}
	return d.Now()
}

// action is what a classification asks the store to do.
type action int

const (
	actNone action = iota
	actRelease
	actFence
	actRecoverResultPending
	actCompleteFinalization
)

type decision struct {
	entry   Entry
	act     action
	basis   store.ReleaseBasis
	journal execwire.Journal
}

// run is one reconcile pass. Store refusals for one attempt are recorded on its
// entry; infrastructure errors abort the run so a startup failure keeps the
// listeners closed.
func run(ctx context.Context, d Deps, trigger, runnerID string, journals map[string]execwire.Journal) (Report, error) {
	generation, boot := d.Store.Boot()
	report := Report{ID: newID(), DaemonBoot: boot, CreatedMS: d.now().UnixMilli(), Trigger: trigger, RunnerID: runnerID, Entries: []Entry{}}
	attempts, err := d.Store.NonTerminalAttempts(ctx)
	if fixtureOnly(err) {
		// A --fixture store holds synthetic assignments and no reservations,
		// grants or leases; there is nothing to reconcile and nothing to retain.
		return report, nil
	}
	if err != nil {
		return Report{}, err
	}
	retry := map[string]string{}
	for _, a := range attempts {
		in, err := d.Store.ReconciliationInputs(ctx, a.Identity.AttemptID)
		if err != nil {
			return Report{}, err
		}
		if runnerID != "" && in.Dispatch.ID != "" && in.Dispatch.Facts.Repository.RunnerRoot.RunnerID != runnerID {
			continue
		}
		var journal *execwire.Journal
		if j, ok := journals[in.Dispatch.ID]; ok && in.Dispatch.ID != "" {
			journal = &j
		}
		dec := classify(in, journal, generation)
		entry, err := apply(ctx, d, in, dec)
		if err != nil {
			return Report{}, err
		}
		report.Entries = append(report.Entries, entry)
		if entry.Released && AutoRetryable(entry.Cause) {
			retry[entry.TaskID] = entry.Cause
		}
	}
	cleared, err := d.Store.ClearLatches(ctx, "")
	if err != nil {
		return Report{}, err
	}
	for _, v := range cleared {
		entry := Entry{AttemptID: v.AttemptID, TaskID: v.TaskID, State: v.Terminal, Classification: LatchCleared, Cause: v.Cause, Detail: "cancel latch " + v.StopID + " cleared: attempt terminal and reservation released"}
		report.Entries = append(report.Entries, entry)
		if cause, err := releasedCause(ctx, d, v.TaskID); err != nil {
			return Report{}, err
		} else if AutoRetryable(cause) {
			retry[v.TaskID] = cause
		}
	}
	if d.AutoRetry {
		for _, taskID := range sortedKeys(retry) {
			entry, err := autoRetry(ctx, d, taskID, retry[taskID])
			if err != nil {
				return Report{}, err
			}
			report.Entries = append(report.Entries, entry)
		}
	}
	if trigger == "sweep" {
		same, err := unchanged(ctx, d, report)
		if err != nil {
			return Report{}, err
		}
		if same {
			return report, nil
		}
	}
	return report, persist(ctx, d, report)
}

// releasedCause is the terminal cause of a task's last attempt, used when a
// latch clearing (after an S1 release) is what makes the task retryable.
func releasedCause(ctx context.Context, d Deps, taskID string) (string, error) {
	in, err := d.Store.RetryInputs(ctx, taskID)
	if err != nil {
		return "", err
	}
	if in.Last.ID == "" || !in.Last.Released {
		return "", nil
	}
	return in.Cause, nil
}

func persist(ctx context.Context, d Deps, report Report) error {
	body, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return d.Store.PutReconcileReport(ctx, store.ReconcileReport{ID: report.ID, DaemonBoot: report.DaemonBoot, CreatedMS: report.CreatedMS, Body: body})
}

// unchanged reports whether the last retained report of this boot carries the
// same entries, so idle sweeps do not accrete identical rows.
func unchanged(ctx context.Context, d Deps, report Report) (bool, error) {
	row, err := d.Store.LatestReconcileReport(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	last, err := decodeReport(row)
	if err != nil || last.DaemonBoot != report.DaemonBoot {
		return false, nil
	}
	return entriesDigest(last.Entries) == entriesDigest(report.Entries), nil
}

func entriesDigest(entries []Entry) string {
	b, _ := json.Marshal(entries)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// retained is the decoded runtime_observations body.
type retained struct {
	MessageID   string                 `json:"message_id"`
	Message     *p.Message             `json:"message,omitempty"`
	Evidence    *store.RuntimeEvidence `json:"evidence,omitempty"`
	Termination *c.Evidence            `json:"termination,omitempty"`
	Boundary    *store.BoundaryState   `json:"boundary,omitempty"`
}

func decodeRetained(row store.RuntimeObservation) (retained, bool) {
	var v retained
	if json.Unmarshal(row.Body, &v) != nil {
		return retained{}, false
	}
	return v, true
}

func settled(b *store.BoundaryState) bool {
	return b == nil || b.Quiescent && b.InFlight == 0 && b.Reservations == b.TerminalReceipts && b.Reservations >= 0
}

// termination is the strongest retained termination evidence for an attempt.
type termination struct {
	found     bool
	current   bool // reported under this daemon boot
	observed  bool // a control_observations row (validated on arrival)
	quiescent bool // confirmed process and quiescent, settled remote work
	stopID    string
	sha       string // runtime_observations key for retained reports
	confirmed string
	remote    string
}

// terminationEvidence prefers a current-boot observation, then any quiescent
// report (observed or retained), then a non-quiescent one.
func terminationEvidence(in store.ReconciliationInputs) termination {
	best := termination{}
	consider := func(t termination) {
		if !best.found || t.quiescent && !best.quiescent || t.quiescent == best.quiescent && t.current && !best.current {
			best = t
		}
	}
	boundaries := map[string]*store.BoundaryState{}
	for _, row := range in.RuntimeObservations {
		if v, ok := decodeRetained(row); row.Kind == "terminated" && ok && v.Termination != nil {
			boundaries[v.Termination.Terminated.MessageID] = v.Boundary
		}
	}
	for _, ev := range in.Observations {
		m := ev.Terminated
		if m.Identity != in.Dispatch.Assignment.Identity {
			continue
		}
		ok := (m.ConfirmedProcess == "terminated" || m.ConfirmedProcess == "not_started") && m.RemoteWork == "quiescent" && settled(boundaries[m.MessageID])
		consider(termination{found: true, current: m.DaemonBoot == in.DaemonBoot, observed: true, quiescent: ok, stopID: m.StopID, confirmed: m.ConfirmedProcess, remote: m.RemoteWork})
	}
	for _, row := range in.RuntimeObservations {
		v, ok := decodeRetained(row)
		if row.Kind != "terminated" || !ok || v.Termination == nil {
			continue
		}
		m := v.Termination.Terminated
		if m.Identity != in.Dispatch.Assignment.Identity {
			continue
		}
		observed := false
		for _, ev := range in.Observations {
			if ev.Terminated.MessageID == m.MessageID {
				observed = true
			}
		}
		if observed {
			continue
		}
		quiet := (m.ConfirmedProcess == "terminated" || m.ConfirmedProcess == "not_started") && m.RemoteWork == "quiescent" && v.Boundary != nil && settled(v.Boundary)
		// A retained report is never current confirmation, whichever boot it
		// names: S1 retains exactly the reports whose boots did not match.
		consider(termination{found: true, current: false, quiescent: quiet, stopID: m.StopID, sha: row.EvidenceSHA256, confirmed: m.ConfirmedProcess, remote: m.RemoteWork})
	}
	return best
}

func hasRuntime(in store.ReconciliationInputs, kind string) (store.RuntimeObservation, bool) {
	for _, row := range in.RuntimeObservations {
		if row.Kind == kind {
			return row, true
		}
	}
	return store.RuntimeObservation{}, false
}

func stopCause(in store.ReconciliationInputs) (cause, actor, stopID string) {
	for _, r := range in.StopRequests {
		if r.Request.Kind == c.CancelAttempt || cause == "" {
			cause, actor, stopID = r.Request.Cause, r.Actor, r.Request.ID
		}
	}
	return cause, actor, stopID
}

// classify decides one attempt's classification and the action that follows
// from it. It reads only the snapshot; every action is re-verified by the store.
func classify(in store.ReconciliationInputs, journal *execwire.Journal, generation string) decision {
	id := in.Dispatch.Assignment.Identity
	entry := Entry{AttemptID: id.AttemptID, TaskID: id.TaskID, Epoch: id.Epoch, State: in.State}
	dec := decision{entry: entry}
	block := func(class, detail string) decision {
		dec.entry.Classification, dec.entry.ActionRequired, dec.entry.Detail = class, true, detail
		return dec
	}
	wait := func(class, detail string) decision {
		dec.entry.Classification, dec.entry.Detail = class, detail
		return dec
	}
	if in.Dispatch.ID == "" {
		return block(JournalCorrupt, "attempt has no dispatch record; no reservation to release")
	}
	if id.Generation != generation {
		return block(StaleGeneration, "attempt belongs to generation "+id.Generation+"; the restored daemon neither launches nor releases it")
	}
	if journal != nil && journal.Corrupt {
		return block(JournalCorrupt, "runner journal for dispatch "+in.Dispatch.ID+" reports corruption")
	}
	if _, refused := hasRuntime(in, "refused"); refused && !in.Acknowledged {
		dec.entry.Classification, dec.entry.Cause, dec.entry.Detail = RefusedBeforeAccept, CauseRefused, "runner refused the assignment before accepting; release not_started"
		dec.act, dec.basis = actRelease, store.ReleaseBasis{Kind: store.BasisRefused, Cause: CauseRefused}
		return dec
	}
	term := terminationEvidence(in)
	if term.found {
		if !term.quiescent {
			return block(RemoteWorkUnknown, fmt.Sprintf("termination reported (process %s, remote work %s) without quiescent boundary accounting; reservation held", term.confirmed, term.remote))
		}
		if term.current {
			dec.entry.Classification, dec.entry.Detail = TerminatedConfirmed, "current-boot termination observed with quiescent remote work; release"
			dec.act, dec.basis = actRelease, store.ReleaseBasis{Kind: store.BasisObservation, StopID: term.stopID}
			return dec
		}
		if !in.LeaseBarrierPassed {
			return wait(AwaitingEvidence, "termination retained from another boot; waiting for the replacement barrier")
		}
		dec.entry.Classification, dec.entry.Detail = TerminatedOldBoot, "termination retained from another boot and the replacement barrier elapsed; release"
		if term.observed {
			dec.act, dec.basis = actRelease, store.ReleaseBasis{Kind: store.BasisObservation, StopID: term.stopID}
		} else {
			dec.act, dec.basis = actRelease, store.ReleaseBasis{Kind: store.BasisRetainedTermination, EvidenceSHA256: term.sha}
		}
		return dec
	}
	if in.Receipt.ReceiptID != "" && !in.Quarantined {
		dec.entry.Classification = CustodyCommittedPendingFinalization
		if in.State == p.Unknown || in.State == p.ResultPending && in.LeaseBarrierPassed {
			dec.entry.Detail = "custody committed without finalization; completing from retained evidence"
			dec.act = actCompleteFinalization
			return dec
		}
		return wait(CustodyCommittedPendingFinalization, "custody committed; awaiting the runner's finalize")
	}
	cause, actor, stopID := stopCause(in)
	if !in.Acknowledged {
		switch {
		case in.State == p.Assigned && len(in.StopTargets) == 0 && !in.Latched && in.GrantRefusal == "":
			return wait(AssignedUndelivered, "assignment retained in the outbox awaiting delivery")
		case in.State == p.Assigned:
			if cause == "" {
				cause = in.GrantRefusal
			}
			if cause == "" {
				cause = CauseOperator
			}
			dec.entry.Detail = "unacknowledged assignment closed by a stop or dead grant; release not_started"
		default:
			if cause == "" {
				cause = CauseDaemonRestart
			}
			dec.entry.Detail = "unacknowledged assignment became unknown at restart; no launch capability was ever issued; release not_started"
		}
		dec.entry.Classification, dec.entry.Cause = AssignedUndelivered, cause
		dec.act, dec.basis = actRelease, store.ReleaseBasis{Kind: store.BasisNotStarted, StopID: stopID, Cause: cause, Actor: actor}
		return dec
	}
	_, exited := hasRuntime(in, "exit")
	if exited || journal != nil && (journal.State == p.ResultPending || journal.ReceiptID != "") {
		dec.entry.Classification = ResultPendingRemote
		if in.State == p.Unknown && len(in.StopTargets) == 0 && !in.Latched && in.GrantRefusal == "" && !in.Paused {
			dec.entry.Detail = "process exited and result preserved by the runner; restoring result_pending for upload"
			dec.act = actRecoverResultPending
			if journal != nil {
				dec.journal = *journal
			}
			return dec
		}
		return wait(ResultPendingRemote, "process exited; awaiting the runner's upload and finalize")
	}
	if in.LeaseCount == 0 {
		restarted := in.LastSession.SessionID != "" && in.LastSession.RunnerBoot != in.Dispatch.Facts.RunnerBoot
		if in.State == p.Unknown || restarted {
			if cause == "" {
				cause = CauseDaemonRestart
				if restarted {
					cause = CauseRunnerRestarted
				}
			}
			dec.entry.Classification, dec.entry.Cause, dec.entry.Detail = AssignedUndelivered, cause, "accepted without any lease; no launch capability was ever issued; release not_started"
			dec.act, dec.basis = actRelease, store.ReleaseBasis{Kind: store.BasisNotStarted, StopID: stopID, Cause: cause, Actor: actor}
			return dec
		}
		return wait(AwaitingEvidence, "accepted; awaiting the runner's lease request")
	}
	if in.LeaseBarrierPassed {
		dec.entry.Classification, dec.entry.Cause, dec.entry.ActionRequired = LeaseLapsedUnconfirmed, CauseLeaseExpired, true
		dec.entry.Detail = "every lease lapsed past the replacement barrier without termination evidence; fenced to stopping, reservation held until a supervisor report or owner release"
		dec.act = actFence
		return dec
	}
	return wait(AwaitingEvidence, fmt.Sprintf("%s under lease %s; replacement barrier pending", in.State, in.LastLease.Request.Nonce))
}

// apply performs the decided action and records its outcome on the entry.
// Store refusals become the entry's detail (blocking); other errors propagate.
func apply(ctx context.Context, d Deps, in store.ReconciliationInputs, dec decision) (Entry, error) {
	entry := dec.entry
	var err error
	switch dec.act {
	case actNone:
		return entry, nil
	case actRelease:
		var out store.ReleaseOutcome
		out, err = d.Store.ReleaseAttempt(ctx, entry.AttemptID, dec.basis)
		if err == nil {
			entry.State, entry.Released = out.Proof.To, !out.Replayed
			if out.Cause != "" {
				entry.Cause = out.Cause
			}
			entry.Detail += fmt.Sprintf("; released to %s (actor %s)", out.Proof.To, out.Actor)
		}
	case actFence:
		var m p.Message
		m, err = d.Store.FenceAttempt(ctx, entry.AttemptID, CauseLeaseExpired)
		if err == nil {
			entry.State = m.To
		}
	case actRecoverResultPending:
		var m p.Message
		m, err = d.Store.RecoverResultPending(ctx, entry.AttemptID, dec.journal)
		if err == nil {
			entry.State = m.To
		}
	case actCompleteFinalization:
		var reply store.FinalizeReply
		reply, err = d.Store.CompleteFinalization(ctx, entry.AttemptID)
		if err == nil {
			entry.State, entry.Released, entry.Cause = p.AttemptState(reply.Outcome), reply.Released, reply.Outcome
			entry.Detail += "; finalized " + reply.Outcome
		}
	}
	if err == nil {
		return entry, nil
	}
	if !refusal(err) {
		return Entry{}, err
	}
	entry.ActionRequired = true
	entry.Detail += "; store refused: " + err.Error()
	return entry, nil
}

// fixtureOnly reports the store's refusal to open a persistent transaction on a
// disposable --fixture store.
func fixtureOnly(err error) bool {
	var deny *g.Refusal
	return errors.As(err, &deny) && deny.Code == "fixture_only"
}

// refusal reports whether an error is a store decision (recorded on the entry)
// rather than an infrastructure failure (which aborts the run).
func refusal(err error) bool {
	var deny *g.Refusal
	var reason p.Refusal
	return errors.As(err, &deny) || errors.As(err, &reason) || errors.Is(err, c.ErrFenced) || errors.Is(err, i.Denied) || errors.Is(err, sql.ErrNoRows)
}

// PlanRetry plans a new attempt for a task: it first clears eligible cancel
// latches for the task, then requires every prior attempt terminal, the last
// reservation released, an unpaused daemon, no active latch, a live head grant
// and headroom under the task's attempt/retry ceilings. It yields a
// DispatchRequest with a fresh intent key that rebinds the last request to the
// head grant, retains a retry_planned report entry naming the prior attempt and
// its cause, and admits nothing itself: store.Dispatch re-checks everything and
// creates the attempt.
func PlanRetry(ctx context.Context, d Deps, taskID string) (store.DispatchRequest, error) {
	if d.Store == nil {
		return store.DispatchRequest{}, ErrNoStore
	}
	if !p.ValidID(taskID) {
		return store.DispatchRequest{}, g.Deny("malformed", "task_id")
	}
	if _, err := d.Store.ClearLatches(ctx, taskID); err != nil {
		return store.DispatchRequest{}, err
	}
	request, cause, in, err := plan(ctx, d, taskID)
	if err != nil {
		return store.DispatchRequest{}, err
	}
	_, boot := d.Store.Boot()
	last := in.Attempts[len(in.Attempts)-1]
	report := Report{ID: newID(), DaemonBoot: boot, CreatedMS: d.now().UnixMilli(), Trigger: "retry", Entries: []Entry{{
		AttemptID: last.Identity.AttemptID, TaskID: taskID, Epoch: last.Identity.Epoch, State: last.State, Classification: RetryPlanned, Cause: cause,
		Detail: fmt.Sprintf("retry planned as dispatch intent %s (epoch %d) after %s attempt %s", request.ID, last.Identity.Epoch+1, last.State, last.Identity.AttemptID),
	}}}
	return request, persist(ctx, d, report)
}

func plan(ctx context.Context, d Deps, taskID string) (store.DispatchRequest, string, store.RetryInputs, error) {
	in, err := d.Store.RetryInputs(ctx, taskID)
	if err != nil {
		return store.DispatchRequest{}, "", in, err
	}
	if len(in.Attempts) == 0 || in.Last.ID == "" {
		return store.DispatchRequest{}, "", in, g.Deny("reconciliation_required", "no_prior_attempt")
	}
	for _, a := range in.Attempts {
		if !terminal(a.State) {
			return store.DispatchRequest{}, "", in, g.Deny("current_assignment", "task")
		}
	}
	if !in.Last.Released {
		return store.DispatchRequest{}, "", in, g.Deny("reconciliation_required", "reservation")
	}
	if in.Paused {
		return store.DispatchRequest{}, "", in, g.Deny("paused", "retry")
	}
	if in.Latched {
		return store.DispatchRequest{}, "", in, g.Deny("stop_latched", "task")
	}
	if in.GrantRefusal != "" {
		return store.DispatchRequest{}, "", in, g.Deny(in.GrantRefusal, "grant")
	}
	if in.Dispatched >= in.Ceiling.Attempts || in.Dispatched > in.Ceiling.Retries {
		return store.DispatchRequest{}, "", in, g.Deny("attempt_ceiling", "task")
	}
	now := d.now().UnixMilli()
	if in.FirstMS != 0 && (now < in.FirstMS || now-in.FirstMS >= in.Ceiling.TotalMS || in.Last.Allowance.AttemptMS > in.Ceiling.TotalMS-(now-in.FirstMS)) {
		return store.DispatchRequest{}, "", in, g.Deny("duration_ceiling", "task")
	}
	envelope := in.Grant.Envelope
	if in.Grant.ID == in.Last.Request.GrantID {
		envelope = in.Last.Request.Envelope
	} else {
		routes := []g.Route{}
		for _, route := range in.Grant.Envelope.Routes {
			if route.RouteRef == in.Last.Decision.Selected.RouteRef {
				routes = append(routes, route)
			}
		}
		envelope.Routes = routes
	}
	request := store.DispatchRequest{ID: newID(), Request: g.Request{GrantID: in.Grant.ID, TaskID: taskID, GrantRevision: in.Grant.Revision, Action: "execute", Envelope: envelope}, Decision: in.Last.Decision, Allowance: in.Last.Allowance}
	return request, in.Cause, in, nil
}

// autoRetry plans and dispatches one task under --auto-retry; the outcome is an
// entry, never an error, unless the store itself failed.
func autoRetry(ctx context.Context, d Deps, taskID, cause string) (Entry, error) {
	entry := Entry{TaskID: taskID, Classification: RetryRefused, Cause: cause}
	if !AutoRetryable(cause) {
		entry.Detail = "cause is not automatically retryable"
		return entry, nil
	}
	request, planned, in, err := plan(ctx, d, taskID)
	if err != nil {
		if !refusal(err) {
			return Entry{}, err
		}
		entry.Detail = "retry refused: " + err.Error()
		if len(in.Attempts) > 0 {
			entry.AttemptID, entry.Epoch, entry.State = in.Attempts[len(in.Attempts)-1].Identity.AttemptID, in.Attempts[len(in.Attempts)-1].Identity.Epoch, in.LastState
		}
		return entry, nil
	}
	last := in.Attempts[len(in.Attempts)-1]
	entry.AttemptID, entry.Epoch, entry.Cause = last.Identity.AttemptID, last.Identity.Epoch, planned
	dispatched, err := d.Store.Dispatch(ctx, request)
	if err != nil {
		if !refusal(err) {
			return Entry{}, err
		}
		entry.State, entry.Detail = last.State, "auto-retry refused by admission: "+err.Error()
		return entry, nil
	}
	next := dispatched.Assignment.Identity
	entry.AttemptID, entry.Epoch, entry.State, entry.Classification = next.AttemptID, next.Epoch, p.Assigned, RetryDispatched
	entry.Detail = fmt.Sprintf("auto-retry dispatched %s (epoch %d) after %s attempt %s", dispatched.ID, next.Epoch, last.State, last.Identity.AttemptID)
	return entry, nil
}

func terminal(state p.AttemptState) bool {
	return state == p.Succeeded || state == p.Failed || state == p.Cancelled || state == p.Expired
}

func sortedKeys(m map[string]string) []string { return slices.Sorted(maps.Keys(m)) }

// newID mints a UUIDv4 report or intent key; crypto/rand.Read either fills the
// buffer or terminates the process, so there is no weak fallback.
func newID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
