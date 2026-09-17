package reconcile

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

// barrier is comfortably past the reopened store's 30 s maximum validity plus
// the 7 s retained lease margin.
const barrier = 45 * time.Second

func TestNilStoreIsNoOpAndPlanRetryRefuses(t *testing.T) {
	for _, fn := range []func() (Report, error){
		func() (Report, error) { return Startup(ctx, Deps{}) },
		func() (Report, error) { return OnHello(ctx, Deps{}, "", execwire.Hello{}) },
		func() (Report, error) { return Sweep(ctx, Deps{}) },
	} {
		if report, err := fn(); err != nil || len(report.Entries) != 0 {
			t.Fatal(report, err)
		}
	}
	if _, err := PlanRetry(ctx, Deps{}, uuid()); !errors.Is(err, ErrNoStore) {
		t.Fatal("nil store planned a retry", err)
	}
	if _, err := (StoreReader{}).Read(ctx); !errors.Is(err, ErrNoStore) {
		t.Fatal(err)
	}
}

// An assignment that was never acknowledged and becomes unknown at restart has
// no launch capability: no ack, no lease. Startup releases it not_started with
// cause daemon_restart; with --auto-retry the task is re-dispatched exactly once.
func TestStartupReleasesUndeliveredAssignmentAtRestart(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint("auto=", auto), func(t *testing.T) {
			f := newFixture(t)
			k := f.newTask(t)
			k.dispatch(t)
			f.reopen(t)
			report := f.startup(t, auto)
			e := entryFor(t, report, k.id().AttemptID)
			if e.Classification != AssignedUndelivered || !e.Released || e.Cause != CauseDaemonRestart || e.State != p.Cancelled || e.ActionRequired {
				t.Fatalf("%+v", e)
			}
			if !f.released(t, k.d.ID) {
				t.Fatal("reservation still held")
			}
			retried := entriesOf(report, RetryDispatched)
			if auto {
				if len(retried) != 1 || retried[0].Epoch != 2 || retried[0].Cause != CauseDaemonRestart || retried[0].TaskID != k.id().TaskID {
					t.Fatalf("auto-retry entries %+v", report.Entries)
				}
				active := f.nonTerminal(t)
				if len(active) != 1 || active[0].Identity.Epoch != 2 {
					t.Fatal("expected exactly the retried attempt active", active)
				}
				// A second startup pass does not dispatch again: the new attempt is
				// live and in the outbox.
				again := f.startup(t, true)
				if len(entriesOf(again, RetryDispatched)) != 0 || entryFor(t, again, active[0].Identity.AttemptID).Classification != AssignedUndelivered {
					t.Fatalf("%+v", again.Entries)
				}
				if active := f.nonTerminal(t); len(active) != 1 {
					t.Fatal("two active attempts", active)
				}
				return
			}
			if len(retried) != 0 || len(f.nonTerminal(t)) != 0 {
				t.Fatal("retry without --auto-retry", report.Entries)
			}
			request, err := PlanRetry(ctx, f.deps(), k.id().TaskID)
			if err != nil || request.ID == k.request.ID || request.Request.TaskID != k.id().TaskID {
				t.Fatal(request, err)
			}
			d, err := f.s.Dispatch(ctx, request)
			if err != nil || d.Assignment.Identity.Epoch != 2 {
				t.Fatal("planned retry refused", d, err)
			}
			latest, err := NewReader(f.s).Read(ctx)
			if err != nil || latest.Trigger != "retry" || len(latest.Entries) != 1 || latest.Entries[0].Classification != RetryPlanned || latest.Entries[0].Cause != CauseDaemonRestart || !strings.Contains(latest.Entries[0].Detail, request.ID) {
				t.Fatalf("%+v %v", latest, err)
			}
		})
	}
}

// A live unacknowledged assignment stays in the outbox until a stop closes it;
// then reconcile releases it not_started with the stop requester as actor and the
// cancel latch is cleared in the same transaction.
func TestLiveAssignmentStaysInOutboxUntilStopped(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.dispatch(t)
	report := f.sweep(t, true)
	e := entryFor(t, report, k.id().AttemptID)
	if e.Classification != AssignedUndelivered || e.Released || e.ActionRequired || len(entriesOf(report, RetryDispatched)) != 0 {
		t.Fatalf("%+v", e)
	}
	if _, err := PlanRetry(ctx, f.deps(), k.id().TaskID); err == nil || !strings.Contains(err.Error(), "current_assignment") {
		t.Fatal("retry planned over a live attempt", err)
	}
	stop := k.cancel(t)
	owner, err := f.s.Authenticate(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	report = f.sweep(t, true)
	e = entryFor(t, report, k.id().AttemptID)
	if e.Classification != AssignedUndelivered || !e.Released || e.State != p.Cancelled || e.Cause != CauseOperator || !strings.Contains(e.Detail, owner.ID) {
		t.Fatalf("%+v", e)
	}
	if len(entriesOf(report, RetryDispatched)) != 0 {
		t.Fatal("operator stop auto-retried")
	}
	latched, err := f.s.TaskLatched(ctx, k.id().TaskID)
	if err != nil || latched {
		t.Fatal("cancel latch not cleared with the release", latched, err)
	}
	cleared, err := f.s.LatchClearances(ctx, k.id().TaskID)
	if err != nil || len(cleared) != 1 || cleared[0].StopID != stop.ID || cleared[0].Terminal != p.Cancelled {
		t.Fatal(cleared, err)
	}
	if view, err := f.s.StopStatus(ctx, stop.ID, k.id().AttemptID); err != nil || view.Receipt.Request.ID != stop.ID {
		t.Fatal("stop history lost", view, err)
	}
	if _, err := PlanRetry(ctx, f.deps(), k.id().TaskID); err != nil {
		t.Fatal("retry refused after clearing", err)
	}
}

// A runner refusal before accept releases the reservation not_started; the
// cause is never auto-retried but the owner may retry, and no latch is left.
func TestRefusedBeforeAcceptReleasesWithoutLatch(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.dispatch(t)
	k.hello(t)
	refusal := p.Message{Version: p.FencedVersion, Kind: "refuse", MessageID: uuid(), Identity: k.id(), InReplyTo: k.d.Assignment.MessageID, Reason: p.ReconciliationRequired}
	if err := f.s.RecordRefusal(ctx, f.runner, k.session.SessionID, p.FencedVersion, k.d.ID, refusal); err != nil {
		t.Fatal(err)
	}
	report := f.sweep(t, true)
	e := entryFor(t, report, k.id().AttemptID)
	if e.Classification != RefusedBeforeAccept || !e.Released || e.State != p.Cancelled || e.Cause != CauseRefused || len(entriesOf(report, RetryDispatched)) != 0 {
		t.Fatalf("%+v", report.Entries)
	}
	request, err := PlanRetry(ctx, f.deps(), k.id().TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if d, err := f.s.Dispatch(ctx, request); err != nil || d.Assignment.Identity.Epoch != 2 {
		t.Fatal(d, err)
	}
}

// A lapsed lease without termination evidence never releases: the attempt is
// fenced to stopping under the lease-clock stop, stays action_required and keeps
// the global reservation, with or without --auto-retry.
func TestLeaseLapseWithoutEvidenceStaysBlocked(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	lease := k.start(t)
	f.reopen(t)
	report := f.startup(t, true)
	e := entryFor(t, report, k.id().AttemptID)
	if e.Classification != AwaitingEvidence || e.Released || e.ActionRequired || e.State != p.Unknown {
		t.Fatalf("%+v", e)
	}
	f.advance(barrier)
	for pass := range 2 {
		report = f.sweep(t, true)
		e = entryFor(t, report, k.id().AttemptID)
		if e.Classification != LeaseLapsedUnconfirmed || e.Released || !e.ActionRequired || e.State != p.Stopping || e.Cause != CauseLeaseExpired {
			t.Fatalf("pass %d: %+v", pass, e)
		}
		if len(entriesOf(report, RetryDispatched)) != 0 {
			t.Fatal("unconfirmed lapse auto-retried")
		}
	}
	in, err := f.s.ReconciliationInputs(ctx, k.id().AttemptID)
	if err != nil || len(in.StopTargets) != 1 || in.StopRequests[0].Request.Cause != "lease_expired" || in.StopRequests[0].Actor != "lease-clock" || in.Dispatch.Released {
		t.Fatalf("%+v %v", in, err)
	}
	if in.StopRequests[0].Request.ID != in.StopTargets[0].Cancel.StopID || in.LastLease.Request.Nonce != lease.Request.Nonce {
		t.Fatal("fence latched a foreign stop")
	}
	other := f.newTask(t)
	if _, err := f.s.Dispatch(ctx, other.request); err == nil || !strings.Contains(err.Error(), "concurrency_ceiling") {
		t.Fatal("held reservation did not block new work", err)
	}
	if _, err := PlanRetry(ctx, f.deps(), k.id().TaskID); err == nil || !strings.Contains(err.Error(), "current_assignment") {
		t.Fatal(err)
	}
}

// Old-boot termination evidence releases only after the replacement barrier:
// an operator cancel becomes cancelled (never auto-retried); a supervisor
// lease-expiry stop becomes expired and, with --auto-retry, one new attempt.
func TestOldBootTerminationReleasesAfterBarrier(t *testing.T) {
	for _, mode := range []string{"operator", "lease_expired"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			k := f.newTask(t)
			lease := k.start(t)
			var stopID string
			if mode == "operator" {
				stopID = k.cancel(t).ID
				k.propose(t, p.Stopping, store.RuntimeEvidence{Kind: "stop"})
			} else {
				stopID = execwire.ExpiryStopID(lease.Request.Nonce)
			}
			oldBoot := f.boot
			f.reopen(t)
			k.hello(t)
			// The restarted runner replays its journal: evidence bound to the boot
			// it observed under, which the daemon may only retain.
			evidence := k.terminated(stopID, "terminated", "quiescent")
			evidence.Terminated.DaemonBoot = oldBoot
			if reply := k.report(t, evidence, quiescent()); reply.Outcome != "retained" || reply.Released {
				t.Fatal(reply)
			}
			report := f.sweep(t, true)
			if e := entryFor(t, report, k.id().AttemptID); e.Classification != AwaitingEvidence || e.Released || e.ActionRequired {
				t.Fatalf("released before the barrier: %+v", e)
			}
			f.advance(barrier)
			report = f.sweep(t, true)
			e := entryFor(t, report, k.id().AttemptID)
			if e.Classification != TerminatedOldBoot || !e.Released {
				t.Fatalf("%+v", e)
			}
			retried := entriesOf(report, RetryDispatched)
			if mode == "operator" {
				if e.State != p.Cancelled || e.Cause != CauseOperator || len(retried) != 0 || len(f.nonTerminal(t)) != 0 {
					t.Fatalf("%+v %+v", e, retried)
				}
				return
			}
			if e.State != p.Expired || e.Cause != CauseLeaseExpired || len(retried) != 1 || retried[0].Epoch != 2 {
				t.Fatalf("%+v %+v", e, retried)
			}
			if active := f.nonTerminal(t); len(active) != 1 || active[0].Identity.Epoch != 2 {
				t.Fatal(active)
			}
			// The supervisor's expiry stop is history through the retained report and
			// the release proof, not through a cancel latch that would block retry.
			in, err := f.s.ReconciliationInputs(ctx, k.id().AttemptID)
			if err != nil || len(in.StopRequests) != 0 || !in.Dispatch.Released || in.State != p.Expired {
				t.Fatalf("%+v %v", in.StopRequests, err)
			}
			retainedTerminated := 0
			for _, row := range in.RuntimeObservations {
				if row.Kind == "terminated" {
					retainedTerminated++
				}
			}
			if retainedTerminated != 1 {
				t.Fatal("retained termination evidence lost", in.RuntimeObservations)
			}
		})
	}
}

// remote_work unknown blocks forever: no barrier, restart or sweep releases it.
func TestRemoteWorkUnknownNeverReleases(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.start(t)
	stop := k.cancel(t)
	k.propose(t, p.Stopping, store.RuntimeEvidence{Kind: "stop"})
	if reply := k.report(t, k.terminated(stop.ID, "terminated", "unknown"), store.BoundaryState{Reservations: 2, TerminalReceipts: 1, InFlight: 1}); reply.Released {
		t.Fatal(reply)
	}
	check := func(report Report) {
		t.Helper()
		e := entryFor(t, report, k.id().AttemptID)
		if e.Classification != RemoteWorkUnknown || e.Released || !e.ActionRequired || len(entriesOf(report, RetryDispatched)) != 0 {
			t.Fatalf("%+v", e)
		}
	}
	check(f.sweep(t, true))
	f.advance(barrier)
	check(f.sweep(t, true))
	f.reopen(t)
	f.advance(barrier)
	check(f.startup(t, true))
	if f.released(t, k.d.ID) || len(f.nonTerminal(t)) != 1 {
		t.Fatal("remote_work unknown released")
	}
}

// A current-boot quiescent observation for an attempt the runner never moved to
// stopping is released by reconcile: fence to stopping, then cancelled.
func TestTerminatedConfirmedReleasesFromRunning(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.start(t)
	stop := k.cancel(t)
	if reply := k.report(t, k.terminated(stop.ID, "terminated", "quiescent"), quiescent()); reply.Outcome != "observed" || reply.Released {
		t.Fatal(reply)
	}
	report := f.sweep(t, false)
	e := entryFor(t, report, k.id().AttemptID)
	if e.Classification != TerminatedConfirmed || !e.Released || e.State != p.Cancelled || e.Cause != CauseOperator {
		t.Fatalf("%+v", e)
	}
	if !f.released(t, k.d.ID) || len(f.nonTerminal(t)) != 0 {
		t.Fatal("not released")
	}
	// Replaying the sweep changes nothing and the terminal attempt is not listed.
	again := f.sweep(t, false)
	if len(again.Entries) != 0 {
		t.Fatalf("%+v", again.Entries)
	}
}

// Custody without finalization is completed from retained evidence only with a
// positive quiescence proof. The fake harness never had an inference boundary
// (launch intent boundary_port 0), so its result finalizes at startup and the
// runner's replayed finalize agrees; a real boundary with no runner attestation
// stays held (blocking once the barrier elapses) and a usage reservation without
// a terminal receipt is proof of work still in flight. Received usage rows never
// stand in for the runner's attestation.
func TestCustodyCommittedPendingFinalization(t *testing.T) {
	for _, mode := range []string{"no_proof", "inflight", "fake_succeeded", "fake_failed"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			k := f.newTask(t)
			fake := strings.HasPrefix(mode, "fake_")
			outcome := "succeeded"
			if fake {
				outcome = strings.TrimPrefix(mode, "fake_")
				k.runFake(t)
			} else {
				k.run(t)
			}
			if mode == "inflight" {
				k.usage(t, store.UsageReceipt{RequestID: "req-1", Protocol: "openai-chat", Model: "model-a", Source: "boundary", StartedMS: 1})
			}
			custody := k.upload(t, outcome, map[string]string{"greeting.txt": "hello, gaffer\n"})
			f.reopen(t)
			report := f.startup(t, true)
			e := entryFor(t, report, k.id().AttemptID)
			if e.Classification != CustodyCommittedPendingFinalization {
				t.Fatalf("%+v", e)
			}
			in, err := f.s.ReconciliationInputs(ctx, k.id().AttemptID)
			if err != nil {
				t.Fatal(err)
			}
			if !fake {
				if e.Released || e.ActionRequired || in.State != p.Unknown || in.Dispatch.Released {
					t.Fatalf("released without quiescence proof: %+v", e)
				}
				f.advance(barrier)
				for pass := range 2 {
					report = f.sweep(t, true)
					e = entryFor(t, report, k.id().AttemptID)
					if e.Released || !e.ActionRequired || len(entriesOf(report, RetryDispatched)) != 0 {
						t.Fatalf("pass %d: %+v", pass, e)
					}
				}
				if in, err = f.s.ReconciliationInputs(ctx, k.id().AttemptID); err != nil || in.Dispatch.Released || in.State != p.Unknown {
					t.Fatalf("%+v %v", in, err)
				}
				other := f.newTask(t)
				if _, err := f.s.Dispatch(ctx, other.request); err == nil || !strings.Contains(err.Error(), "concurrency_ceiling") {
					t.Fatal("held reservation did not block new work", err)
				}
				return
			}
			if !e.Released || string(e.State) != outcome || e.Cause != outcome || !strings.Contains(e.Detail, store.QuiescenceNoBoundary) || len(entriesOf(report, RetryDispatched)) != 0 {
				t.Fatalf("%+v", report.Entries)
			}
			if in.State != p.AttemptState(outcome) || !in.Dispatch.Released || (outcome == "succeeded") != (in.Head.AttemptID == k.id().AttemptID) {
				t.Fatalf("%+v", in)
			}
			k.hello(t)
			digest, err := f.s.Streams().Digest(k.id().AttemptID, 0)
			if err != nil {
				t.Fatal(err)
			}
			// The fake harness ran without a boundary; its finalize attests an empty,
			// quiescent one and so matches the retained completion.
			completion := store.Completion{Version: execwire.Version, MessageID: uuid(), ReceiptID: custody.Receipt.ReceiptID, Stream: store.StreamWatermark{Through: 0, Digest: digest}, Exit: store.ExitRecord{Code: 0, PGID: 101, ObservedUnixNS: exited(0).ObservedUnixNS}, Boundary: store.BoundaryState{Quiescent: true}}
			reply, err := f.s.FinalizeAttempt(ctx, f.runner, k.session.SessionID, k.id().AttemptID, completion)
			if err != nil || reply.Outcome != outcome || !reply.Released {
				t.Fatal("runner replay after reconcile finalization", reply, err)
			}
			// The owner may retry a failed attempt; automation never does.
			if _, err := PlanRetry(ctx, f.deps(), k.id().TaskID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// An exited process whose result never reached custody is result_pending on
// the runner's side: reconcile restores result_pending from the exit
// observation so the runner's upload and finalize succeed through the channel.
func TestResultPendingRemoteRestoresForUpload(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.run(t)
	f.reopen(t)
	report := f.startup(t, true)
	e := entryFor(t, report, k.id().AttemptID)
	if e.Classification != ResultPendingRemote || e.Released || e.ActionRequired || e.State != p.ResultPending {
		t.Fatalf("%+v", e)
	}
	k.hello(t)
	custody := k.upload(t, "succeeded", map[string]string{"greeting.txt": "hello, gaffer\n"})
	digest, err := f.s.Streams().Digest(k.id().AttemptID, 0)
	if err != nil {
		t.Fatal(err)
	}
	completion := store.Completion{Version: execwire.Version, MessageID: uuid(), ReceiptID: custody.Receipt.ReceiptID, Stream: store.StreamWatermark{Through: 0, Digest: digest}, Exit: store.ExitRecord{Code: 0, PGID: 101, ObservedUnixNS: exited(0).ObservedUnixNS}, Boundary: quiescent()}
	reply, err := f.s.FinalizeAttempt(ctx, f.runner, k.session.SessionID, k.id().AttemptID, completion)
	if err != nil || reply.Outcome != "succeeded" {
		t.Fatal(reply, err)
	}
}

// A hello journal that reports corruption blocks the attempt; OnHello only
// classifies the calling runner's attempts and confirms a preserved result.
func TestOnHelloJournalsScopeAndCorruption(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.start(t)
	f.reopen(t)
	hello := execwire.Hello{Version: execwire.Version, MessageID: uuid(), RunnerBoot: f.facts.RunnerBoot, EligibilityID: f.facts.ID, EligibilityRevision: 1, Journals: []execwire.Journal{{DispatchID: k.d.ID, Identity: k.id(), RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.boot, State: p.Running, Corrupt: true}}}
	report, err := OnHello(ctx, f.deps(), f.runnerID, hello)
	if err != nil {
		t.Fatal(err)
	}
	if e := entryFor(t, report, k.id().AttemptID); e.Classification != JournalCorrupt || !e.ActionRequired || e.Released || report.Trigger != "hello" || report.RunnerID != f.runnerID {
		t.Fatalf("%+v", report)
	}
	if other, err := OnHello(ctx, f.deps(), uuid(), hello); err != nil || len(other.Entries) != 0 {
		t.Fatal("another runner's hello classified this runner's attempt", other, err)
	}
	// Without corruption, a journal that says result_pending is a hint only: the
	// daemon never observed the exit, so the attempt stays unknown and waits.
	hello.Journals[0].Corrupt, hello.Journals[0].State = false, p.ResultPending
	report, err = OnHello(ctx, f.deps(), f.runnerID, hello)
	if err != nil {
		t.Fatal(err)
	}
	if e := entryFor(t, report, k.id().AttemptID); e.Classification != ResultPendingRemote || e.State != p.Unknown || e.Released || e.ActionRequired {
		t.Fatalf("%+v", e)
	}
	// A corrupt journal persisted through the session row blocks later sweeps
	// that carry no journals, until a later hello reports the journal intact.
	corrupt := f.helloRecord()
	raw, err := json.Marshal(execwire.Journal{DispatchID: k.d.ID, Identity: k.id(), RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.boot, State: p.Running, Corrupt: true})
	if err != nil {
		t.Fatal(err)
	}
	corrupt.Journals = []json.RawMessage{raw}
	if _, err := f.s.RunnerSession(ctx, f.runner, corrupt); err != nil {
		t.Fatal(err)
	}
	if e := entryFor(t, f.sweep(t, true), k.id().AttemptID); e.Classification != JournalCorrupt || !e.ActionRequired {
		t.Fatalf("%+v", e)
	}
	in, err := f.s.ReconciliationInputs(ctx, k.id().AttemptID)
	if err != nil || !in.JournalFound || !in.Journal.Corrupt {
		t.Fatalf("%+v %v", in.Journal, err)
	}
	repaired := f.helloRecord()
	raw, _ = json.Marshal(execwire.Journal{DispatchID: k.d.ID, Identity: k.id(), RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.boot, State: p.Running})
	repaired.Journals = []json.RawMessage{raw}
	if _, err := f.s.RunnerSession(ctx, f.runner, repaired); err != nil {
		t.Fatal(err)
	}
	if e := entryFor(t, f.sweep(t, true), k.id().AttemptID); e.Classification == JournalCorrupt {
		t.Fatalf("repaired journal still blocks: %+v", e)
	}
}

// Latch clearing after an S1 release is what lets task retry proceed; PauseTask
// (task stop) is never cleared automatically.
func TestLatchClearingAfterCancelAndNotForPause(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.start(t)
	stop := k.cancel(t)
	k.propose(t, p.Stopping, store.RuntimeEvidence{Kind: "stop"})
	if reply := k.report(t, k.terminated(stop.ID, "terminated", "quiescent"), quiescent()); !reply.Released {
		t.Fatal(reply)
	}
	if latched, err := f.s.TaskLatched(ctx, k.id().TaskID); err != nil || !latched {
		t.Fatal("S1 release cleared the latch itself", latched, err)
	}
	request, err := PlanRetry(ctx, f.deps(), k.id().TaskID)
	if err != nil || request.Request.TaskID != k.id().TaskID {
		t.Fatal(request, err)
	}
	if latched, err := f.s.TaskLatched(ctx, k.id().TaskID); err != nil || latched {
		t.Fatal("PlanRetry did not clear the eligible latch", latched, err)
	}
	cleared, err := f.s.LatchClearances(ctx, "")
	if err != nil || len(cleared) != 1 || cleared[0].StopID != stop.ID || cleared[0].Cause != "operator" || cleared[0].Terminal != p.Cancelled {
		t.Fatal(cleared, err)
	}
	if again, err := f.s.ClearLatches(ctx, ""); err != nil || len(again) != 0 {
		t.Fatal("clearing is not idempotent", again, err)
	}
	// Task-level pause stays latched after the attempt is released.
	paused := f.newTask(t)
	paused.dispatch(t)
	pause := c.Request{ID: uuid(), Kind: c.PauseTask, TaskID: paused.id().TaskID, Cause: "operator"}
	if _, err := f.s.RequestStop(ctx, f.owner, pause); err != nil {
		t.Fatal(err)
	}
	report := f.sweep(t, true)
	if e := entryFor(t, report, paused.id().AttemptID); e.Classification != AssignedUndelivered || !e.Released || e.Cause != CauseOperator {
		t.Fatalf("%+v", e)
	}
	if latched, err := f.s.TaskLatched(ctx, paused.id().TaskID); err != nil || !latched {
		t.Fatal("pause_task was cleared", latched, err)
	}
	if _, err := PlanRetry(ctx, f.deps(), paused.id().TaskID); err == nil || !strings.Contains(err.Error(), "stop_latched") {
		t.Fatal("retry planned under a task pause", err)
	}
	if len(entriesOf(report, LatchCleared)) != 0 {
		t.Fatalf("pause reported as cleared: %+v", report.Entries)
	}
}

// PlanRetry honours attempt ceilings, grant invalidation and the released
// reservation; store.Dispatch enforces the same ceilings independently.
func TestPlanRetryCeilingsAndAuthority(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	refuse := func() {
		t.Helper()
		k.hello(t)
		refusal := p.Message{Version: p.FencedVersion, Kind: "refuse", MessageID: uuid(), Identity: k.id(), InReplyTo: k.d.Assignment.MessageID, Reason: p.ReconciliationRequired}
		if err := f.s.RecordRefusal(ctx, f.runner, k.session.SessionID, p.FencedVersion, k.d.ID, refusal); err != nil {
			t.Fatal(err)
		}
		f.sweep(t, false)
	}
	k.dispatch(t)
	refuse()
	for epoch := int64(2); epoch <= 3; epoch++ {
		request, err := PlanRetry(ctx, f.deps(), k.id().TaskID)
		if err != nil {
			t.Fatal(epoch, err)
		}
		k.request = request
		if d := k.dispatch(t); d.Assignment.Identity.Epoch != epoch {
			t.Fatal(d.Assignment.Identity)
		}
		refuse()
	}
	_, err := PlanRetry(ctx, f.deps(), k.id().TaskID)
	requireCode(t, err, "attempt_ceiling")
	k.request.ID = uuid()
	_, err = f.s.Dispatch(ctx, k.request)
	requireCode(t, err, "attempt_ceiling")
	// A revoked grant refuses retry with the authority code.
	other := f.newTask(t)
	other.dispatch(t)
	other.hello(t)
	refusal := p.Message{Version: p.FencedVersion, Kind: "refuse", MessageID: uuid(), Identity: other.id(), InReplyTo: other.d.Assignment.MessageID, Reason: p.ReconciliationRequired}
	if err := f.s.RecordRefusal(ctx, f.runner, other.session.SessionID, p.FencedVersion, other.d.ID, refusal); err != nil {
		t.Fatal(err)
	}
	f.sweep(t, false)
	if err := f.s.InvalidateExecution(ctx, other.id().TaskID, other.grant.ID, f.owner, "revoked"); err != nil {
		t.Fatal(err)
	}
	_, err = PlanRetry(ctx, f.deps(), other.id().TaskID)
	if err == nil || !(strings.Contains(err.Error(), "revoked") || strings.Contains(err.Error(), "stop_latched")) {
		t.Fatal("retry planned under a revoked grant", err)
	}
	// An unknown task has no prior attempt to retry.
	_, err = PlanRetry(ctx, f.deps(), uuid())
	requireCode(t, err, "reconciliation_required")
}

// Concurrent sweeps with --auto-retry after an expiry release never create two
// active attempts: the store's one_current_attempt index and reservation
// ceiling refuse the second dispatch.
func TestAutoRetryNeverCreatesTwoActiveAttempts(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	lease := k.start(t)
	stopID := execwire.ExpiryStopID(lease.Request.Nonce)
	oldBoot := f.boot
	f.reopen(t)
	k.hello(t)
	evidence := k.terminated(stopID, "terminated", "quiescent")
	evidence.Terminated.DaemonBoot = oldBoot
	k.report(t, evidence, quiescent())
	f.advance(barrier)
	var wg sync.WaitGroup
	reports := make([]Report, 4)
	errs := make([]error, 4)
	for j := range reports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reports[j], errs[j] = Sweep(ctx, Deps{Store: f.s, Now: time.Now, AutoRetry: true})
		}()
	}
	wg.Wait()
	dispatched := 0
	for j, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
		dispatched += len(entriesOf(reports[j], RetryDispatched))
	}
	if dispatched != 1 {
		t.Fatalf("auto-retry dispatched %d attempts", dispatched)
	}
	if active := f.nonTerminal(t); len(active) != 1 || active[0].Identity.Epoch != 2 {
		t.Fatal(active)
	}
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var indexed, active int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='one_current_attempt'").Scan(&indexed); err != nil || indexed != 1 {
		t.Fatal("one_current_attempt index missing", indexed, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM attempts WHERE state NOT IN ('succeeded','failed','cancelled','expired')").Scan(&active); err != nil || active != 1 {
		t.Fatal(active, err)
	}
}

// Idle sweeps do not accrete identical reports; a changed outcome does.
func TestSweepPersistsOnlyChanges(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.dispatch(t)
	first := f.sweep(t, false)
	latest, err := f.s.LatestReconcileReport(ctx)
	if err != nil || latest.ID != first.ID {
		t.Fatal(latest, err)
	}
	f.sweep(t, false)
	if again, err := f.s.LatestReconcileReport(ctx); err != nil || again.ID != first.ID {
		t.Fatal("unchanged sweep persisted", again, err)
	}
	k.cancel(t)
	changed := f.sweep(t, false)
	if again, err := f.s.LatestReconcileReport(ctx); err != nil || again.ID != changed.ID || again.ID == first.ID {
		t.Fatal("changed sweep not persisted", again, err)
	}
	read, err := NewReader(f.s).Read(ctx)
	if err != nil || read.ID != changed.ID || len(read.Entries) != len(changed.Entries) {
		t.Fatal(read, err)
	}
}

// A restored store (new generation, paused) classifies every retained attempt as
// stale_generation and neither releases, finalizes nor dispatches anything.
func TestRestoredStoreClassifiesWithoutActing(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.run(t)
	k.usage(t, store.UsageReceipt{RequestID: "req-1", Protocol: "openai-chat", Model: "model-a", Source: "boundary", Terminal: true, Status: 200})
	k.upload(t, "succeeded", map[string]string{"greeting.txt": "hello\n"})
	f.restore(t)
	f.advance(barrier)
	report := f.startup(t, true)
	e := entryFor(t, report, k.id().AttemptID)
	if e.Classification != StaleGeneration || e.Released || !e.ActionRequired || e.State != p.Unknown || len(entriesOf(report, RetryDispatched)) != 0 {
		t.Fatalf("%+v", report.Entries)
	}
	if f.released(t, k.d.ID) || len(f.nonTerminal(t)) != 1 {
		t.Fatal("restored daemon acted on an old-generation attempt")
	}
	if _, err := PlanRetry(ctx, f.deps(), k.id().TaskID); err == nil {
		t.Fatal("retry planned while paused on a stale attempt")
	}
}

// One hundred unresolved attempts (acknowledged, never leased, all unknown
// after the restart) classify and release at startup well under five seconds:
// 100 snapshot reads, 100 not_started releases with their events, evidence
// records and clearing scans, and one retained report.
func TestHundredAttemptStartupUnderFiveSeconds(t *testing.T) {
	f := newFixture(t)
	const attempts = 100
	seeded := f.seedUnresolved(t, attempts)
	if active := f.nonTerminal(t); len(active) != attempts {
		t.Fatalf("seeded %d unresolved attempts, store lists %d", attempts, len(active))
	}
	for _, id := range seeded[:3] {
		if in, err := f.s.ReconciliationInputs(ctx, id); err != nil || in.Dispatch.ID == "" || !in.Acknowledged || in.State != p.Assigned {
			t.Fatalf("seeded attempt is not a genuine dispatch: %+v %v", in.State, err)
		}
	}
	f.reopen(t)
	started := time.Now()
	report := f.startup(t, false)
	elapsed := time.Since(started)
	if elapsed > 5*time.Second {
		t.Fatalf("startup classification took %s", elapsed)
	}
	released := 0
	for _, e := range report.Entries {
		if e.Classification == AssignedUndelivered && e.Released && e.State == p.Cancelled && e.Cause == CauseDaemonRestart {
			released++
		}
	}
	if released != attempts || len(f.nonTerminal(t)) != 0 {
		t.Fatalf("released %d of %d; %d still active", released, attempts, len(f.nonTerminal(t)))
	}
	if latest, err := NewReader(f.s).Read(ctx); err != nil || latest.ID != report.ID || len(latest.Entries) != attempts {
		t.Fatal(latest.ID, len(latest.Entries), err)
	}
	t.Logf("startup over %d unresolved attempts: %s (%d entries)", attempts, elapsed, len(report.Entries))
}

func TestRunSweepsStopsWithContext(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.dispatch(t)
	deadline, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if err := RunSweeps(deadline, f.deps(), 20*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	read, err := NewReader(f.s).Read(ctx)
	if err != nil || read.Trigger != "sweep" || len(read.Entries) != 1 {
		t.Fatal(read, err)
	}
}

// Crash between the committed termination observation and the release: the
// child process drives a real attempt to running, latches a cancel and records
// the supervisor's quiescent report (which S1 observes but cannot release from
// running), then is SIGKILLed while the store is open. The reopened daemon's
// startup retains the observation as old-boot evidence and releases it only
// after the replacement barrier.
func TestCrashBetweenObservationAndReleaseIsRepairedAtStartup(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	request, err := json.Marshal(k.request)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(deadline, os.Args[0], "-test.run=^TestReconcileCrashChild$")
	cmd.Env = append(os.Environ(), "GAFFER_RECONCILE_CHILD=1", "GAFFER_RECONCILE_DIR="+f.dir, "GAFFER_RECONCILE_ARTIFACTS="+f.artifacts, "GAFFER_RECONCILE_OWNER="+f.owner, "GAFFER_RECONCILE_RUNNER="+f.runner, "GAFFER_RECONCILE_RUNNER_BOOT="+f.facts.RunnerBoot, "GAFFER_RECONCILE_ELIGIBILITY="+f.facts.ID, "GAFFER_RECONCILE_REQUEST="+string(request))
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "observed ") {
		lines := []string{scanner.Text()}
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		t.Fatalf("child: %q (%v)", strings.Join(lines, "|"), scanner.Err())
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) != 3 {
		t.Fatal(fields)
	}
	attemptID, dispatchID := fields[1], fields[2]
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed child exited successfully")
	}
	s, err := store.Open(ctx, f.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	t.Cleanup(func() { s.Close() })
	f.refreshBoot(t)
	report := f.startup(t, true)
	e := entryFor(t, report, attemptID)
	if e.Classification != AwaitingEvidence || e.Released {
		t.Fatalf("released before the barrier: %+v", e)
	}
	in, err := f.s.ReconciliationInputs(ctx, attemptID)
	if err != nil || len(in.Observations) != 1 || in.Observations[0].Terminated.DaemonBoot == f.boot || in.State != p.Unknown || in.Dispatch.Released {
		t.Fatalf("observation not retained across the crash: %+v %v", in, err)
	}
	f.advance(barrier)
	report = f.sweep(t, true)
	e = entryFor(t, report, attemptID)
	if e.Classification != TerminatedOldBoot || !e.Released || e.State != p.Cancelled || e.Cause != CauseOperator || len(entriesOf(report, RetryDispatched)) != 0 {
		t.Fatalf("%+v", report.Entries)
	}
	if !f.released(t, dispatchID) || len(f.nonTerminal(t)) != 0 {
		t.Fatal("repair did not release")
	}
}

// TestReconcileCrashChild is the SIGKILLed child of the crash test above; it
// runs only when spawned with the fixture's environment.
func TestReconcileCrashChild(t *testing.T) {
	if os.Getenv("GAFFER_RECONCILE_CHILD") == "" {
		return
	}
	s, err := store.Open(ctx, os.Getenv("GAFFER_RECONCILE_DIR"), os.Getenv("GAFFER_RECONCILE_ARTIFACTS"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, runner := os.Getenv("GAFFER_RECONCILE_OWNER"), os.Getenv("GAFFER_RECONCILE_RUNNER")
	var request store.DispatchRequest
	if err := json.Unmarshal([]byte(os.Getenv("GAFFER_RECONCILE_REQUEST")), &request); err != nil {
		t.Fatal(err)
	}
	_, boot := s.Boot()
	d, err := s.Dispatch(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	identity := d.Assignment.Identity
	runnerBoot := os.Getenv("GAFFER_RECONCILE_RUNNER_BOOT")
	sess, err := s.RunnerSession(ctx, runner, store.HelloRecord{Version: execwire.Version, MessageID: uuid(), RunnerBoot: runnerBoot, EligibilityID: os.Getenv("GAFFER_RECONCILE_ELIGIBILITY"), EligibilityRevision: 1, PolicyDigest: strings.Repeat("a", 64), Journals: []json.RawMessage{}})
	if err != nil || sess.Mode != "normal" {
		t.Fatal(sess, err)
	}
	ack := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: uuid(), Identity: identity, AssignmentID: d.Assignment.AssignmentID, RunnerBoot: runnerBoot, DaemonBoot: boot}
	if err := s.AcknowledgeAssignment(ctx, runner, d.ID, ack); err != nil {
		t.Fatal(err)
	}
	sent := time.Now().UnixMilli()
	lease, err := s.IssueLease(ctx, runner, sess.SessionID, d.ID, p.FencedVersion, p.Message{Version: p.FencedVersion, Kind: "lease_request", MessageID: uuid(), Identity: identity, Nonce: uuid(), RunnerBoot: runnerBoot, DaemonBoot: boot, SentMS: &sent})
	if err != nil {
		t.Fatal(err)
	}
	revision := int64(1)
	for _, step := range []struct {
		to       p.AttemptState
		evidence store.RuntimeEvidence
	}{{p.Starting, launchIntent(lease.Request.Nonce)}, {p.Running, launched()}} {
		from := p.Assigned
		if step.to == p.Running {
			from = p.Starting
		}
		m := p.Message{Version: p.FencedVersion, Kind: "transition", MessageID: uuid(), Identity: identity, ExpectedRevision: &revision, From: from, To: step.to}
		if _, err := s.ProposeTransition(ctx, runner, sess.SessionID, p.FencedVersion, m, step.evidence); err != nil {
			t.Fatal(step.to, err)
		}
		revision++
	}
	stop := c.Request{ID: uuid(), Kind: c.CancelAttempt, TaskID: identity.TaskID, AttemptID: identity.AttemptID, Cause: "operator"}
	if _, err := s.RequestStop(ctx, owner, stop); err != nil {
		t.Fatal(err)
	}
	wall := time.Now().UTC()
	duration := int64(time.Millisecond)
	evidence := c.Evidence{Terminated: p.Message{Version: p.FencedVersion, Kind: "terminated", MessageID: uuid(), Identity: identity, StopID: stop.ID, RunnerBoot: runnerBoot, DaemonBoot: boot, ConfirmedProcess: "terminated", RemoteWork: "quiescent", EvidenceDigest: strings.Repeat("e", 64)}, Measurement: c.Measurement{RequestedAt: wall, AcknowledgedAt: wall, ObservedAt: &wall, RequestToAckNS: duration, AckToObservedNS: &duration}}
	reply, err := s.ReportTermination(ctx, runner, sess.SessionID, p.FencedVersion, evidence, quiescent())
	if err != nil || reply.Outcome != "observed" || reply.Released {
		t.Fatal("observation from running released", reply, err)
	}
	fmt.Println("observed", identity.AttemptID, d.ID)
	// Hold the store open until the parent kills this process.
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
	t.Fatal("crash child escaped")
}
