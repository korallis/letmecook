package reconcile

// Regression probes adopted verbatim from the S5 delta review of c29707a
// (/Users/leebarry/.claude/jobs/13633b12/tmp/s5-review2/reconcile_probe_test.go):
// each either reproduced an unsafe outcome before its fix or pins a rule the
// review confirmed, and passes once reconcile enforces it. The expiry probe now
// distinguishes canonical new stops from retained legacy daemon stops.

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

func review2Hello(t *testing.T, f *fixture, journals ...execwire.Journal) execwire.Hello {
	t.Helper()
	record := f.helloRecord()
	record.Journals = []json.RawMessage{}
	for _, j := range journals {
		raw, err := json.Marshal(j)
		if err != nil {
			t.Fatal(err)
		}
		record.Journals = append(record.Journals, raw)
	}
	if _, err := f.s.RunnerSession(ctx, f.runner, record); err != nil {
		t.Fatal(err)
	}
	return execwire.Hello{Version: record.Version, MessageID: record.MessageID, RunnerBoot: record.RunnerBoot, EligibilityID: record.EligibilityID, EligibilityRevision: record.EligibilityRevision, PolicyDigest: record.PolicyDigest, Journals: journals}
}

func TestS5Review2AutoRetrySnapshotRace(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.dispatch(t)
	first := k.id().AttemptID
	f.reopen(t)
	released := entryFor(t, f.startup(t, false), first)
	if !released.Released || released.Cause != CauseDaemonRestart {
		t.Fatalf("fixture did not release restart attempt: %+v", released)
	}
	in, err := f.s.RetryInputs(ctx, k.id().TaskID)
	if err != nil || in.Cause != CauseDaemonRestart {
		t.Fatal(in.Cause, err)
	}
	interleaved := false
	d := f.deps()
	// plan invokes Now after reading RetryInputs and before returning its request.
	// This inserts an owner retry exactly into that legal scheduling gap, with
	// no altered production code and every state change through the public API.
	d.Now = func() time.Time {
		if interleaved {
			return time.Now()
		}
		interleaved = true
		next, err := PlanRetry(ctx, f.deps(), k.id().TaskID)
		if err != nil {
			t.Fatal(err)
		}
		k.request = next
		k.run(t)
		custody := k.upload(t, "failed", map[string]string{"result.txt": "owner retry failed\n"})
		w, err := f.s.Streams().Watermark(k.id().AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		reply, err := f.s.FinalizeAttempt(ctx, f.runner, k.session.SessionID, k.id().AttemptID, store.Completion{Version: execwire.Version, MessageID: uuid(), ReceiptID: custody.Receipt.ReceiptID, Stream: store.StreamWatermark{Through: w.Through, Digest: w.Digest}, Exit: store.ExitRecord{Code: 0, PGID: 101, ObservedUnixNS: exited(0).ObservedUnixNS}, Boundary: quiescent()})
		if err != nil || reply.Outcome != "failed" {
			t.Fatal(reply, err)
		}
		latest, err := f.s.RetryInputs(ctx, k.id().TaskID)
		if err != nil || latest.Cause != "failed" || latest.LastState != p.Failed {
			t.Fatal(latest.Cause, latest.LastState, err)
		}
		t.Logf("interleaved owner retry finished: epoch=%d state=%s durable_cause=%s", k.id().Epoch, latest.LastState, latest.Cause)
		return time.Now()
	}
	entry, err := autoRetryAfter(ctx, d, k.id().TaskID, first, CauseDaemonRestart)
	if err != nil {
		t.Fatal(err)
	}
	active := f.nonTerminal(t)
	if entry.Classification == RetryDispatched {
		t.Fatalf("ALLOWLIST BYPASS: stale predecessor dispatched epoch=%d cause=%s after owner failure; active_attempts=%d", entry.Epoch, entry.Cause, len(active))
	}
}

func TestS5Review2DuplicateJournalDoesNotEraseCorruption(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.start(t)
	stop := k.cancel(t)
	if got := k.report(t, k.terminated(stop.ID, "terminated", "quiescent"), quiescent()); got.Released {
		t.Fatal("fixture released early")
	}
	intact := execwire.Journal{DispatchID: k.d.ID, Identity: k.id(), RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.boot, State: p.Running}
	corrupt := intact
	corrupt.Corrupt = true
	hello := review2Hello(t, f, intact, corrupt)
	wire, err := execwire.Encode(hello)
	if err != nil {
		t.Fatalf("not a valid wire hello: %v", err)
	}
	var decoded execwire.Hello
	if err := execwire.Decode(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	report, err := OnHello(ctx, f.deps(), f.runnerID, decoded)
	if err != nil {
		t.Fatal(err)
	}
	first := entryFor(t, report, k.id().AttemptID)
	if first.Classification != JournalCorrupt || !first.ActionRequired {
		t.Fatal(first)
	}
	t.Logf("same accepted hello: classification=%s action_required=%t", first.Classification, first.ActionRequired)
	in, err := f.s.ReconciliationInputs(ctx, k.id().AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	report = f.sweep(t, false)
	second := entryFor(t, report, k.id().AttemptID)
	t.Logf("next sweep without another hello: durable_corrupt=%t classification=%s released=%t", in.Journal.Corrupt, second.Classification, second.Released)
	if second.Released {
		t.Fatal("UNSAFE: conflicting duplicate journals let the sweep release an attempt the hello classified journal_corrupt")
	}
}

func TestS5Review2JournalHintAfterBarrierRemainsBlocking(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.start(t)
	f.reopen(t)
	hello := review2Hello(t, f, execwire.Journal{DispatchID: k.d.ID, Identity: k.id(), RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.boot, State: p.ResultPending})
	if _, err := OnHello(ctx, f.deps(), f.runnerID, hello); err != nil {
		t.Fatal(err)
	}
	f.advance(barrier)
	report := f.sweep(t, false)
	entry := entryFor(t, report, k.id().AttemptID)
	in, err := f.s.ReconciliationInputs(ctx, k.id().AttemptID)
	if err != nil || !in.LeaseBarrierPassed {
		t.Fatal(in.LeaseBarrierPassed, err)
	}
	t.Logf("no exit and barrier passed: state=%s classification=%s action_required=%t released=%t stop_targets=%d", entry.State, entry.Classification, entry.ActionRequired, entry.Released, len(in.StopTargets))
	if !entry.ActionRequired {
		t.Fatal("HIDDEN BLOCK: bare result_pending journal suppresses lease-lapse fencing/action_required indefinitely")
	}
}

func TestS5Review2QuiescenceBlockAndForeignReceipt(t *testing.T) {
	f := newFixture(t)
	first := f.newTask(t)
	first.run(t)
	firstCustody := first.upload(t, "failed", map[string]string{"result.txt": "failed\n"})
	w, err := f.s.Streams().Watermark(first.id().AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.FinalizeAttempt(ctx, f.runner, first.session.SessionID, first.id().AttemptID, store.Completion{Version: execwire.Version, MessageID: uuid(), ReceiptID: firstCustody.Receipt.ReceiptID, Stream: store.StreamWatermark{Through: w.Through, Digest: w.Digest}, Exit: store.ExitRecord{Code: 0, PGID: 101, ObservedUnixNS: exited(0).ObservedUnixNS}, Boundary: quiescent()}); err != nil {
		t.Fatal(err)
	}
	second := f.newTask(t)
	second.run(t)
	f.reopen(t)
	j := execwire.Journal{DispatchID: second.d.ID, Identity: second.id(), RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.boot, State: p.ResultPending, ReceiptID: firstCustody.Receipt.ReceiptID}
	if _, err := f.s.RecoverResultPending(ctx, second.id().AttemptID, j); err == nil || !strings.Contains(err.Error(), "identity_conflict") {
		t.Fatalf("different task receipt accepted: %v", err)
	}
	if _, err := f.s.RecoverResultPending(ctx, second.id().AttemptID, execwire.Journal{}); err != nil {
		t.Fatal(err)
	}
	second.hello(t)
	second.upload(t, "succeeded", map[string]string{"result.txt": "ok\n"})
	f.advance(barrier)
	report := f.sweep(t, false)
	entry := entryFor(t, report, second.id().AttemptID)
	if entry.Classification != CustodyCommittedPendingFinalization || !entry.ActionRequired || entry.Released {
		t.Fatalf("missing quiescence proof not blocked: %+v", entry)
	}
	t.Log("existing other-task receipt refused; real-boundary custody without attestation visibly blocked after barrier")
}

func TestS5Review2HelloRetentionAcrossScopes(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.dispatch(t)
	otherFP := strings.Repeat("c", 64)
	invite, err := f.s.CreateEnrollment(ctx, f.owner, uuid(), otherFP)
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.s.Enroll(ctx, otherFP, invite.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.UpdateIdentity(ctx, f.owner, other.ID, 1, "enable", ""); err != nil {
		t.Fatal(err)
	}
	record := f.helloRecord()
	if _, err := f.s.RunnerSession(ctx, otherFP, record); err != nil {
		t.Fatal(err)
	}
	hello := execwire.Hello{Version: record.Version, MessageID: record.MessageID, RunnerBoot: record.RunnerBoot, EligibilityID: record.EligibilityID, EligibilityRevision: record.EligibilityRevision, PolicyDigest: record.PolicyDigest, Journals: []execwire.Journal{}}
	for range 20 {
		if _, err := OnHello(ctx, f.deps(), other.ID, hello); err != nil {
			t.Fatal(err)
		}
		f.sweep(t, false)
	}
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows int
	if err := db.QueryRow("SELECT count(*) FROM reconcile_reports WHERE id NOT LIKE 'latch-cleared:%'").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	t.Logf("20 identical idle-runner hello/global-sweep pairs: report_rows=%d", rows)
	if rows > 2 {
		t.Fatal("GROWTH: comparison against a different report scope retains every unchanged hello and sweep")
	}
}

func TestS5Review2FirstHelloAfterRestartIsRetained(t *testing.T) {
	f := newFixture(t)
	hello := review2Hello(t, f)
	if _, err := OnHello(ctx, f.deps(), f.runnerID, hello); err != nil {
		t.Fatal(err)
	}
	before, err := f.s.LatestReconcileReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	hello = review2Hello(t, f)
	if _, err := OnHello(ctx, f.deps(), f.runnerID, hello); err != nil {
		t.Fatal(err)
	}
	after, err := f.s.LatestReconcileReport(ctx)
	if err != nil || after.ID == before.ID || after.DaemonBoot == before.DaemonBoot {
		t.Fatal("lost first report in new boot", err)
	}
	t.Log("first empty hello after restart retained under new daemon boot")
}

func TestS5Review2BothLeaseExpiryStopIDs(t *testing.T) {
	for _, kind := range []string{"runner_expiry", "daemon_expiry", "legacy_daemon_expiry"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			k := f.newTask(t)
			lease := k.start(t)
			stopID := execwire.ExpiryStopID(lease.Request.Nonce)
			if kind == "daemon_expiry" {
				if _, err := f.s.FenceAttempt(ctx, k.id().AttemptID, CauseLeaseExpired); err != nil {
					t.Fatal(err)
				}
				in, err := f.s.ReconciliationInputs(ctx, k.id().AttemptID)
				if err != nil || len(in.StopTargets) != 1 || in.StopTargets[0].Cancel.StopID != stopID {
					t.Fatalf("new daemon expiry must use the runner's canonical ID: %+v %v", in.StopTargets, err)
				}
			} else if kind == "legacy_daemon_expiry" {
				// Model a retained pre-integration stop through the trusted stop API.
				// A legacy ID counts only with its actual immutable target; creating a
				// new canonical stop cannot authorize evidence for this different ID.
				stopID = derive(lease.Request.Nonce, "expired")
				stop := c.Request{ID: stopID, Kind: c.CancelAttempt, TaskID: k.id().TaskID, AttemptID: k.id().AttemptID, Cause: CauseLeaseExpired}
				if _, err := f.s.RequestStop(ctx, f.owner, stop); err != nil {
					t.Fatal(err)
				}
				k.propose(t, p.Stopping, store.RuntimeEvidence{Kind: "stop"})
			}
			ev := k.terminated(stopID, "terminated", "quiescent")
			if kind != "runner_expiry" {
				ev.Terminated.DaemonBoot = uuid()
			}
			if got := k.report(t, ev, quiescent()); got.Released {
				t.Fatal("expected observation without release")
			}
			if kind != "runner_expiry" {
				f.advance(barrier)
			}
			entry := entryFor(t, f.sweep(t, false), k.id().AttemptID)
			if !entry.Released || entry.State != p.Expired || entry.Cause != CauseLeaseExpired {
				t.Fatalf("classification: %+v", entry)
			}
			if _, err := PlanRetry(ctx, f.deps(), k.id().TaskID); err != nil {
				t.Fatal(err)
			}
			report, err := NewReader(f.s).Read(ctx)
			if err != nil || len(report.Entries) != 1 || report.Entries[0].Classification != RetryPlanned || report.Entries[0].Cause != CauseLeaseExpired {
				t.Fatalf("retry cause: %+v %v", report, err)
			}
			t.Logf("%s: classification=%s terminal=expired PlanRetry_cause=lease_expired", kind, entry.Classification)
		})
	}
}

func TestS5Review2AutoRetryRejectsStaleAndNonterminalLatest(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.dispatch(t)
	first := k.id().AttemptID
	f.reopen(t)
	f.startup(t, false)
	next, err := PlanRetry(ctx, f.deps(), k.id().TaskID)
	if err != nil {
		t.Fatal(err)
	}
	k.request = next
	k.dispatch(t)
	got, err := autoRetryAfter(ctx, f.deps(), k.id().TaskID, first, CauseDaemonRestart)
	if err != nil || got.Classification != RetryRefused || !strings.Contains(got.Detail, "current_assignment") {
		t.Fatal(got, err)
	}
	if active := f.nonTerminal(t); len(active) != 1 || active[0].Identity.Epoch != 2 {
		t.Fatal(active)
	}
	// Finish epoch 2 with another allowlisted cause. A candidate for epoch 1
	// must still refuse even though its observed cause happens to match.
	f.reopen(t)
	f.startup(t, false)
	got, err = autoRetryAfter(ctx, f.deps(), k.id().TaskID, first, CauseDaemonRestart)
	if err != nil || got.Classification != RetryRefused || !strings.Contains(got.Detail, "no longer") {
		t.Fatal(got, err)
	}
	t.Log("candidate created before owner retry refused for both active and already-released later attempts")
}
