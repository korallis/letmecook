package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
)

// advanceControl moves the store's control clock past the reopened barrier
// (30 s maximum validity plus the 7 s retained margin).
func advanceControl(s *Store, d time.Duration) {
	s.SetControlClock(func() time.Time { return time.Now().Add(d) })
}

func releases(t *testing.T, s *Store, want int) {
	t.Helper()
	rowCount(t, s, "dispatch_releases", want)
}

// retainedSHA finds the runtime_observations key of the terminated report that
// carried messageID.
func retainedSHA(t *testing.T, s *Store, attempt, messageID string) string {
	t.Helper()
	rows, err := s.db.Query("SELECT evidence_sha256,body FROM runtime_observations WHERE attempt_id=? AND kind='terminated'", attempt)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var sha, body string
		if err := rows.Scan(&sha, &body); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body, messageID) {
			return sha
		}
	}
	t.Fatal("retained report not found", messageID)
	return ""
}

func TestReconciliationInputsSnapshot(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.run(t)
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, stop.ID, "unknown"), BoundaryState{Reservations: 1, InFlight: 1}); err != nil {
		t.Fatal(err)
	}
	attempt := x.d.Assignment.Identity.AttemptID
	in, err := x.s.ReconciliationInputs(ctx, attempt)
	if err != nil {
		t.Fatal(err)
	}
	if in.Dispatch.ID != x.d.ID || !in.Acknowledged || in.Dispatch.Released || in.State != p.Stopping || in.Revision != 5 || in.LastLease.Request.Nonce != lease.Request.Nonce || in.LeaseCount != 1 || in.LeaseBarrierPassed {
		t.Fatalf("%+v", in)
	}
	if len(in.StopTargets) != 1 || in.StopTargets[0].Cancel.StopID != stop.ID || len(in.StopRequests) != 1 || in.StopRequests[0].Actor != x.owner || in.StopRequests[0].Request.Cause != "operator" {
		t.Fatalf("%+v %+v", in.StopTargets, in.StopRequests)
	}
	if len(in.Observations) != 1 || in.Observations[0].Terminated.RemoteWork != "unknown" || in.Receipt.ReceiptID != "" || in.Quarantined || in.Head.AttemptID != "" {
		t.Fatalf("%+v", in)
	}
	kinds := map[string]int{}
	for _, row := range in.RuntimeObservations {
		kinds[row.Kind]++
	}
	// Runner request receipts (kind receipt) accompany every replayable route.
	if kinds["receipt"] == 0 {
		t.Fatal("no runner receipts retained", kinds)
	}
	delete(kinds, "receipt")
	if !reflect.DeepEqual(kinds, map[string]int{"launch_intent": 1, "launched": 1, "exit": 1, "stop": 1, "terminated": 1}) {
		t.Fatal(kinds)
	}
	if !in.Latched || in.GrantRefusal != "" || in.Paused || in.Generation != x.s.meta.Generation || in.DaemonBoot != x.s.meta.DaemonBoot || in.LastSession.SessionID != x.session.SessionID {
		t.Fatalf("%+v", in)
	}
	// After a reopen the attempt is unknown and the barrier restarts on the new
	// clock domain; only the injected clock advances it.
	boot := x.s.meta.DaemonBoot
	reopen(t, &x)
	in, err = x.s.ReconciliationInputs(ctx, attempt)
	if err != nil || in.State != p.Unknown || in.LeaseBarrierPassed || in.DaemonBoot == boot {
		t.Fatalf("%+v %v", in, err)
	}
	advanceControl(x.s, 40*time.Second)
	if in, err = x.s.ReconciliationInputs(ctx, attempt); err != nil || !in.LeaseBarrierPassed {
		t.Fatal("barrier did not pass on the injected clock", err)
	}
	// A synthetic attempt without a dispatch is readable but has no reservation.
	m := assignment(x.s)
	if err := x.s.assign(ctx, m); err != nil {
		t.Fatal(err)
	}
	orphan, err := x.s.ReconciliationInputs(ctx, m.Identity.AttemptID)
	if err != nil || orphan.Dispatch.ID != "" || orphan.State != p.Assigned || orphan.LeaseCount != 0 || !orphan.LeaseBarrierPassed {
		t.Fatalf("%+v %v", orphan, err)
	}
	active, err := x.s.NonTerminalAttempts(ctx)
	if err != nil || len(active) != 2 {
		t.Fatal(active, err)
	}
	for _, a := range active {
		if a.Identity.AttemptID == attempt && (a.DispatchID != x.d.ID || !a.Acknowledged || a.Released || a.State != p.Unknown) {
			t.Fatalf("%+v", a)
		}
		if a.Identity.AttemptID == m.Identity.AttemptID && a.DispatchID != "" {
			t.Fatalf("%+v", a)
		}
	}
	if _, err := x.s.ReconciliationInputs(ctx, newID()); err == nil {
		t.Fatal("unknown attempt read")
	}
}

// Every release basis is re-verified in the store: a caller's classification
// never releases without the retained evidence behind it.
func TestReleaseAttemptRefusesWithoutEvidence(t *testing.T) {
	x := executionFixtureFor(t, nil)
	attempt := x.d.Assignment.Identity.AttemptID
	refuse := func(basis ReleaseBasis, code string) {
		t.Helper()
		_, err := x.s.ReleaseAttempt(ctx, attempt, basis)
		if err == nil {
			t.Fatalf("%+v released without evidence", basis)
		}
		if code != "" && !strings.Contains(err.Error(), code) {
			t.Fatalf("%+v: want %s, got %v", basis, code, err)
		}
		releases(t, x.s, 0)
	}
	refuse(ReleaseBasis{Kind: "bogus"}, "malformed")
	refuse(ReleaseBasis{Kind: BasisRefused}, "acknowledged")
	refuse(ReleaseBasis{Kind: BasisNotStarted}, "outbox")
	refuse(ReleaseBasis{Kind: BasisObservation, StopID: newID()}, "")
	refuse(ReleaseBasis{Kind: BasisRetainedTermination, EvidenceSHA256: strings.Repeat("0", 64)}, "termination")
	lease := x.lease(t)
	refuse(ReleaseBasis{Kind: BasisNotStarted}, "lease_issued")
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	// An observed report with remote work unknown is retained but never releases.
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, stop.ID, "unknown"), quiescent()); err != nil {
		t.Fatal(err)
	}
	refuse(ReleaseBasis{Kind: BasisObservation, StopID: stop.ID}, "remote_work")
	// A quiescent report from another boot waits for the barrier.
	oldBoot := x.s.meta.DaemonBoot
	reopen(t, &x)
	x.session = sessionFor(t, x.dispatchFixture)
	evidence := terminatedFor(&x, stop.ID, "quiescent")
	evidence.Terminated.DaemonBoot = oldBoot
	if reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, quiescent()); err != nil || reply.Outcome != "retained" {
		t.Fatal(reply, err)
	}
	sha := retainedSHA(t, x.s, attempt, evidence.Terminated.MessageID)
	refuse(ReleaseBasis{Kind: BasisRetainedTermination, EvidenceSHA256: sha}, "lease_barrier")
	advanceControl(x.s, 40*time.Second)
	out, err := x.s.ReleaseAttempt(ctx, attempt, ReleaseBasis{Kind: BasisRetainedTermination, EvidenceSHA256: sha})
	if err != nil || out.Replayed || out.Proof.To != p.Cancelled || out.Cause != "operator" || out.Proof.EvidenceDigest != evidence.Terminated.EvidenceDigest || out.Actor != x.session.RunnerID || len(out.Cleared) != 1 || out.Cleared[0].StopID != stop.ID {
		t.Fatalf("%+v %v", out, err)
	}
	releases(t, x.s, 1)
	if state, _ := attemptRow(t, x.s, attempt); state != p.Cancelled {
		t.Fatal(state)
	}
	again, err := x.s.ReleaseAttempt(ctx, attempt, ReleaseBasis{Kind: BasisNotStarted})
	if err != nil || !again.Replayed || again.Proof != out.Proof {
		t.Fatal("replay differs", again, err)
	}
	if latched, err := x.s.TaskLatched(ctx, x.d.Request.TaskID); err != nil || latched {
		t.Fatal("latch not cleared with the release", latched, err)
	}
	consistent(t, x.s)
}

// not_started releases rest on the durable absence of a launch capability and a
// closed delivery path; the evidence snapshot is retained and digested.
func TestReleaseAttemptNotStartedBases(t *testing.T) {
	// Unacknowledged plus a stop: actor is the stop requester.
	f := dispatchFixtureFor(t, nil)
	d := admitted(t, f)
	attempt := d.Assignment.Identity.AttemptID
	stop := stopRequest(c.CancelAttempt, d)
	if _, err := f.s.RequestStop(ctx, f.owner, stop); err != nil {
		t.Fatal(err)
	}
	owner, err := f.s.Authenticate(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.s.ReleaseAttempt(ctx, attempt, ReleaseBasis{Kind: BasisNotStarted, StopID: stop.ID})
	if err != nil || out.Proof.ConfirmedProcess != "not_started" || out.Proof.To != p.Cancelled || out.Cause != "operator" || out.Actor != owner.ID || len(out.Cleared) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	rowCount(t, f.s, "runtime_observations WHERE kind='reconcile'", 1)
	var digest string
	if err := f.s.db.QueryRow("SELECT evidence_sha256 FROM runtime_observations WHERE kind='reconcile'").Scan(&digest); err != nil || digest != out.Proof.EvidenceDigest {
		t.Fatal("proof digest does not reference the retained evidence", digest, err)
	}
	// Two fence and release events: assigned -> stopping -> cancelled.
	if state, revision := attemptRow(t, f.s, attempt); state != p.Cancelled || revision != 3 {
		t.Fatal(state, revision)
	}
	// Acknowledged at restart, never leased: cause daemon_restart, actor runner.
	y := executionFixtureFor(t, nil)
	reopen(t, &y)
	out, err = y.s.ReleaseAttempt(ctx, y.d.Assignment.Identity.AttemptID, ReleaseBasis{Kind: BasisNotStarted})
	if err != nil || out.Cause != "daemon_restart" || out.Proof.To != p.Cancelled || out.Actor != y.session.RunnerID {
		t.Fatalf("%+v %v", out, err)
	}
	// Refused before accept: the refusal row is the evidence.
	z := dispatchFixtureFor(t, nil)
	zd := admitted(t, z)
	sess := sessionFor(t, z)
	refusal := p.Message{Version: p.FencedVersion, Kind: "refuse", MessageID: newID(), Identity: zd.Assignment.Identity, InReplyTo: zd.Assignment.MessageID, Reason: p.ReconciliationRequired}
	if err := z.s.RecordRefusal(ctx, z.runner, sess.SessionID, p.FencedVersion, zd.ID, refusal); err != nil {
		t.Fatal(err)
	}
	var sha string
	if err := z.s.db.QueryRow("SELECT evidence_sha256 FROM runtime_observations WHERE kind='refused'").Scan(&sha); err != nil {
		t.Fatal(err)
	}
	out, err = z.s.ReleaseAttempt(ctx, zd.Assignment.Identity.AttemptID, ReleaseBasis{Kind: BasisRefused})
	if err != nil || out.Cause != "refused" || out.Proof.EvidenceDigest != sha || out.Proof.ConfirmedProcess != "not_started" || len(out.Cleared) != 0 {
		t.Fatalf("%+v %v", out, err)
	}
	// The next epoch is admissible: no latch was left behind.
	z.request.ID = newID()
	if next, err := z.s.Dispatch(ctx, z.request); err != nil || next.Assignment.Identity.Epoch != 2 {
		t.Fatal(next, err)
	}
}

// Fencing moves a live attempt to stopping under the lease-clock stop id that a
// late renewal would also latch, revokes its leases and never releases.
func TestFenceAttempt(t *testing.T) {
	x := executionFixtureFor(t, nil)
	attempt := x.d.Assignment.Identity.AttemptID
	if _, err := x.s.FenceAttempt(ctx, attempt, "bogus"); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatal(err)
	}
	if _, err := x.s.FenceAttempt(ctx, attempt, "lease_expired"); err == nil || !strings.Contains(err.Error(), "reconciliation_required") {
		t.Fatal("fenced a lease that was never issued", err)
	}
	lease := x.lease(t)
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	m, err := x.s.FenceAttempt(ctx, attempt, "lease_expired")
	if err != nil || m.From != p.Running || m.To != p.Stopping {
		t.Fatal(m, err)
	}
	stopID := dispatchID(lease.Request.Nonce, "expired")
	stop, err := x.s.StopStatus(ctx, stopID, attempt)
	if err != nil || stop.Receipt.Actor != "lease-clock" || stop.Receipt.Request.Cause != "lease_expired" || stop.Status != c.TerminationUnconfirmed {
		t.Fatalf("%+v %v", stop, err)
	}
	rowCount(t, x.s, "control_revocations", 1)
	if state, revision := attemptRow(t, x.s, attempt); state != p.Stopping || revision != 4 || taskRow(t, x.s, x.d.Request.TaskID) != p.TaskReconciling {
		t.Fatal(state, revision)
	}
	releases(t, x.s, 0)
	again, err := x.s.FenceAttempt(ctx, attempt, "lease_expired")
	if err != nil || !reflect.DeepEqual(again, m) {
		t.Fatal("replay differs", again, err)
	}
	if _, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, x.leaseRequest()); err == nil {
		t.Fatal("fenced attempt leased again")
	}
	// The stop the runner reports against is the same id, so its termination
	// evidence releases through the ordinary path.
	reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, stopID, "quiescent"), quiescent())
	if err != nil || !reply.Released {
		t.Fatal(reply, err)
	}
	if state, _ := attemptRow(t, x.s, attempt); state != p.Expired {
		t.Fatal(state)
	}
	if _, err := x.s.FenceAttempt(ctx, attempt, "lease_expired"); !errors.Is(err, p.InvalidTransition) {
		t.Fatal("terminal attempt fenced", err)
	}
	// operator fences carry their own stop id and work from assigned.
	y := executionFixtureFor(t, nil)
	fence, err := y.s.FenceAttempt(ctx, y.d.Assignment.Identity.AttemptID, "operator")
	if err != nil || fence.From != p.Assigned || fence.To != p.Stopping {
		t.Fatal(fence, err)
	}
	if _, err := y.s.StopStatus(ctx, dispatchID(y.d.Assignment.Identity.AttemptID, "reconcile-fence"), y.d.Assignment.Identity.AttemptID); err != nil {
		t.Fatal(err)
	}
}

// Finalization from stored evidence needs the receipt, the exit observation at
// exactly the sink watermark, all-terminal boundary receipts, no latch and a
// live grant; it writes what FinalizeAttempt writes so the runner's replay agrees.
func TestCompleteFinalizationEvidence(t *testing.T) {
	x := executionFixtureFor(t, nil)
	attempt := x.d.Assignment.Identity.AttemptID
	x.run(t, "first", "second")
	if _, err := x.s.CompleteFinalization(ctx, attempt); err == nil || !strings.Contains(err.Error(), "receipt") {
		t.Fatal("finalized without custody", err)
	}
	if err := x.s.RecordUsage(ctx, x.runner, x.session.SessionID, UsageReport{Version: execwire.Version, MessageID: newID(), Identity: x.d.Assignment.Identity, Receipts: []UsageReceipt{{RequestID: "req-1", Protocol: "openai-chat", Model: "model-a", Source: "boundary", StartedMS: 1}}}); err != nil {
		t.Fatal(err)
	}
	_, custody := x.upload(t, "succeeded", map[string]string{"greeting.txt": "hello, gaffer\n"})
	if _, err := x.s.CompleteFinalization(ctx, attempt); err == nil || !strings.Contains(err.Error(), "boundary") {
		t.Fatal("finalized with an in-flight reservation", err)
	}
	if err := x.s.RecordUsage(ctx, x.runner, x.session.SessionID, UsageReport{Version: execwire.Version, MessageID: newID(), Identity: x.d.Assignment.Identity, Receipts: []UsageReceipt{{RequestID: "req-1", Protocol: "openai-chat", Model: "model-a", Source: "boundary", StartedMS: 1, EndedMS: 2, Terminal: true, Status: 200}}}); err != nil {
		t.Fatal(err)
	}
	// Records appended after the exit observation break the watermark equality.
	extra := spooledRecords(t, x.d.Assignment.Identity, "first", "second", "late")
	if _, err := x.s.Streams().Receive(attempt, x.d.Assignment.Identity, extra[2:]); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.CompleteFinalization(ctx, attempt); err == nil || !strings.Contains(err.Error(), "stream") {
		t.Fatal("finalized past the exit watermark", err)
	}
	releases(t, x.s, 0)
	// A fresh fixture with matching evidence finalizes from unknown after a reopen.
	y := executionFixtureFor(t, nil)
	yAttempt := y.d.Assignment.Identity.AttemptID
	y.run(t, "only")
	_, yCustody := y.upload(t, "succeeded", map[string]string{"greeting.txt": "hello\n"})
	stop := stopRequest(c.CancelAttempt, y.d)
	if _, err := y.s.RequestStop(ctx, y.owner, stop); err != nil {
		t.Fatal(err)
	}
	if _, err := y.s.CompleteFinalization(ctx, yAttempt); err == nil || !strings.Contains(err.Error(), "stop_latched") {
		t.Fatal("finalized under a stop latch", err)
	}
	_ = custody
	z := executionFixtureFor(t, nil)
	zAttempt := z.d.Assignment.Identity.AttemptID
	z.run(t, "only")
	_, zCustody := z.upload(t, "failed", map[string]string{"recovery.log": "it broke\n"})
	reopen(t, &z)
	for _, step := range []string{"before_complete_commit", "after_complete_commit"} {
		z.s.controlHook = func(got string) error {
			if got == step {
				return errors.New("crash")
			}
			return nil
		}
		if _, err := z.s.CompleteFinalization(ctx, zAttempt); err == nil {
			t.Fatal("reply after injected failure")
		}
		z.s.controlHook = nil
		state, _ := attemptRow(t, z.s, zAttempt)
		if step == "before_complete_commit" {
			if state != p.Unknown {
				t.Fatal("uncommitted finalization visible", state)
			}
			releases(t, z.s, 0)
			continue
		}
		if state != p.Failed {
			t.Fatal("committed finalization lost", state)
		}
	}
	reply, err := z.s.CompleteFinalization(ctx, zAttempt)
	if err != nil || reply.Outcome != "failed" || !reply.Released {
		t.Fatal(reply, err)
	}
	releases(t, z.s, 1)
	rowCount(t, z.s, "artifact_result_heads", 0)
	rowCount(t, z.s, "runtime_observations WHERE kind='completion'", 1)
	var terminal int
	if err := z.s.db.QueryRow("SELECT count(*) FROM events WHERE message_id=? AND attempt_id=?", dispatchID(zCustody.Receipt.ReceiptID, "terminal"), zAttempt).Scan(&terminal); err != nil || terminal != 1 {
		t.Fatal("terminal event id differs from FinalizeAttempt's", terminal, err)
	}
	var recovered string
	if err := z.s.db.QueryRow("SELECT message FROM events WHERE attempt_id=? AND revision=6", zAttempt).Scan(&recovered); err != nil {
		t.Fatal(err)
	}
	if m, err := p.Decode([]byte(recovered)); err != nil || m.From != p.Unknown || m.To != p.ResultPending {
		t.Fatal("unknown -> result_pending not recorded", recovered, err)
	}
	if taskRow(t, z.s, z.d.Request.TaskID) != p.TaskReconciling {
		t.Fatal(taskRow(t, z.s, z.d.Request.TaskID))
	}
	// The runner's replayed finalize under a new session returns the same reply
	// when it attests the same stream, exit and boundary; this runner posted no
	// usage receipts, so its boundary is empty and quiescent.
	z.session = sessionFor(t, z.dispatchFixture)
	replay := z.completion(t, zCustody.Receipt.ReceiptID, 1)
	replay.Boundary = BoundaryState{Quiescent: true}
	if again, err := z.s.FinalizeAttempt(ctx, z.runner, z.session.SessionID, zAttempt, replay); err != nil || !reflect.DeepEqual(again, reply) {
		t.Fatal(again, err)
	}
	// A different boundary attestation is not the same completion.
	if _, err := z.s.FinalizeAttempt(ctx, z.runner, z.session.SessionID, zAttempt, z.completion(t, zCustody.Receipt.ReceiptID, 1)); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("divergent attestation accepted after reconcile finalization", err)
	}
	_ = yCustody
	consistent(t, z.s)
}

// The release transaction commits before it is reported, and nothing of it is
// visible when the commit is interrupted.
func TestReleaseAttemptCrashHooks(t *testing.T) {
	for _, step := range []string{"before_release_commit", "after_release_commit"} {
		t.Run(step, func(t *testing.T) {
			x := executionFixtureFor(t, nil)
			attempt := x.d.Assignment.Identity.AttemptID
			lease := x.lease(t)
			x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
			x.propose(t, p.Running, launchedEvidence())
			stop := stopRequest(c.CancelAttempt, x.d)
			if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
				t.Fatal(err)
			}
			if reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, stop.ID, "quiescent"), quiescent()); err != nil || reply.Released {
				t.Fatal("observation from running released", reply, err)
			}
			x.s.controlHook = func(got string) error {
				if got == step {
					return errors.New("crash")
				}
				return nil
			}
			if _, err := x.s.ReleaseAttempt(ctx, attempt, ReleaseBasis{Kind: BasisObservation, StopID: stop.ID}); err == nil {
				t.Fatal("reply after injected failure")
			}
			x.s.controlHook = nil
			state, revision := attemptRow(t, x.s, attempt)
			if step == "before_release_commit" {
				if state != p.Running || revision != 3 {
					t.Fatal("uncommitted release visible", state, revision)
				}
				releases(t, x.s, 0)
				if cleared, err := x.s.LatchClearances(ctx, ""); err != nil || len(cleared) != 0 {
					t.Fatal(cleared, err)
				}
				return
			}
			if state != p.Cancelled || revision != 5 {
				t.Fatal("committed release lost", state, revision)
			}
			releases(t, x.s, 1)
			if cleared, err := x.s.LatchClearances(ctx, ""); err != nil || len(cleared) != 1 {
				t.Fatal(cleared, err)
			}
		})
	}
}

// Reports are immutable, replay-safe and read newest-first; clearing records
// live in the same table under their own key and never count as reports.
func TestReconcileReportsAndClearances(t *testing.T) {
	x := executionFixtureFor(t, nil)
	if _, err := x.s.LatestReconcileReport(ctx); err == nil {
		t.Fatal("empty store has a latest report")
	}
	report := ReconcileReport{ID: newID(), DaemonBoot: x.s.meta.DaemonBoot, CreatedMS: 1000, Body: []byte(`{"entries":[]}`)}
	for _, bad := range []ReconcileReport{
		{},
		{ID: "latch-cleared:" + newID(), DaemonBoot: report.DaemonBoot, CreatedMS: 1, Body: report.Body},
		{ID: newID(), DaemonBoot: report.DaemonBoot, CreatedMS: 1, Body: []byte(`{`)},
		{ID: newID(), DaemonBoot: report.DaemonBoot, CreatedMS: 0, Body: report.Body},
		{ID: newID(), DaemonBoot: "", CreatedMS: 1, Body: report.Body},
	} {
		if err := x.s.PutReconcileReport(ctx, bad); err == nil {
			t.Fatalf("malformed report retained: %+v", bad)
		}
	}
	for range 2 {
		if err := x.s.PutReconcileReport(ctx, report); err != nil {
			t.Fatal(err)
		}
	}
	changed := report
	changed.Body = []byte(`{"entries":[{}]}`)
	if err := x.s.PutReconcileReport(ctx, changed); !errors.Is(err, p.IdentityConflict) {
		t.Fatal(err)
	}
	got, err := x.s.ReconcileReport(ctx, report.ID)
	if err != nil || got.ID != report.ID || string(got.Body) != string(report.Body) || got.CreatedMS != 1000 {
		t.Fatal(got, err)
	}
	newer := ReconcileReport{ID: newID(), DaemonBoot: report.DaemonBoot, CreatedMS: 2000, Body: []byte(`{"entries":[]}`)}
	if err := x.s.PutReconcileReport(ctx, newer); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"UPDATE reconcile_reports SET body='{}'", "DELETE FROM reconcile_reports"} {
		if _, err := x.s.db.Exec(query); err == nil {
			t.Fatal("report mutable", query)
		}
	}
	// A released cancel latch clears once; the clearing record is not a report.
	x.run(t)
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	if cleared, err := x.s.ClearLatches(ctx, ""); err != nil || len(cleared) != 0 {
		t.Fatal("cleared a held reservation", cleared, err)
	}
	if reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, stop.ID, "quiescent"), quiescent()); err != nil || !reply.Released {
		t.Fatal(reply, err)
	}
	if latched, err := x.s.TaskLatched(ctx, x.d.Request.TaskID); err != nil || !latched {
		t.Fatal(latched, err)
	}
	cleared, err := x.s.ClearLatches(ctx, x.d.Request.TaskID)
	if err != nil || len(cleared) != 1 || cleared[0].StopID != stop.ID || cleared[0].AttemptID != x.d.Assignment.Identity.AttemptID || cleared[0].Terminal != p.Cancelled || cleared[0].Actor != x.owner {
		t.Fatalf("%+v %v", cleared, err)
	}
	if again, err := x.s.ClearLatches(ctx, ""); err != nil || len(again) != 0 {
		t.Fatal(again, err)
	}
	if latched, err := x.s.TaskLatched(ctx, x.d.Request.TaskID); err != nil || latched {
		t.Fatal(latched, err)
	}
	list, err := x.s.LatchClearances(ctx, "")
	if err != nil || len(list) != 1 || list[0].StopID != stop.ID {
		t.Fatal(list, err)
	}
	latest, err := x.s.LatestReconcileReport(ctx)
	if err != nil || latest.ID != newer.ID {
		t.Fatal("clearing record served as the latest report", latest, err)
	}
	rowCount(t, x.s, "control_stops", 1)
	rowCount(t, x.s, "reconcile_reports", 3)
	if _, err := x.s.ReconcileReport(ctx, "latch-cleared:"+stop.ID); err == nil {
		t.Fatal("clearing record readable as a report")
	}
}

// RetryInputs derives the last attempt's cause from retained records.
func TestRetryInputsCauses(t *testing.T) {
	x := executionFixtureFor(t, nil)
	task := x.d.Request.TaskID
	in, err := x.s.RetryInputs(ctx, task)
	if err != nil || len(in.Attempts) != 1 || in.Last.ID != x.d.ID || in.Last.Released || in.LastState != p.Assigned || in.Cause != "" || in.Grant.ID != x.grant.ID || in.GrantRefusal != "" || in.Dispatched != 1 || in.Ceiling.Attempts != 3 {
		t.Fatalf("%+v %v", in, err)
	}
	// Lease lapse reported by the runner under the same boot: expired.
	lease := x.run(t)
	expiry := execwire.ExpiryStopID(lease.Request.Nonce)
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop", Nonce: lease.Request.Nonce})
	if reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, expiry, "quiescent"), quiescent()); err != nil || !reply.Released {
		t.Fatal(reply, err)
	}
	if in, err = x.s.RetryInputs(ctx, task); err != nil || in.LastState != p.Expired || in.Cause != "lease_expired" || !in.Last.Released || !in.Latched {
		t.Fatalf("%+v %v", in, err)
	}
	if cleared, err := x.s.ClearLatches(ctx, task); err != nil || len(cleared) != 1 || cleared[0].Cause != "lease_expired" || cleared[0].Actor != "lease-clock" {
		t.Fatal(cleared, err)
	}
	if in, err = x.s.RetryInputs(ctx, task); err != nil || in.Latched {
		t.Fatal(in.Latched, err)
	}
	// A finalized attempt reports its outcome as the cause (never auto-retried).
	// The same-boot expiry latch above still suppresses store.Dispatch until
	// control.go consults the clearing record, so the finalized case uses its own
	// task.
	y := executionFixtureFor(t, nil)
	y.run(t)
	_, custody := y.upload(t, "succeeded", map[string]string{"greeting.txt": "hello\n"})
	y.finalize(t, y.completion(t, custody.Receipt.ReceiptID, 0))
	if in, err = y.s.RetryInputs(ctx, y.d.Request.TaskID); err != nil || in.LastState != p.Succeeded || in.Cause != "succeeded" || in.Dispatched != 1 || !in.Last.Released {
		t.Fatalf("%+v %v", in, err)
	}
	if _, err := x.s.RetryInputs(ctx, "nope"); err == nil {
		t.Fatal("malformed task id accepted")
	}
	if in, err := x.s.RetryInputs(ctx, newID()); err != nil || len(in.Attempts) != 0 || in.GrantRefusal != "unknown_grant" {
		t.Fatal(in, err)
	}
}

// unknown -> result_pending needs proof the process exited (the exit observation
// or the runner's exact journal) and no stop; replay returns the same event.
func TestRecoverResultPending(t *testing.T) {
	x := executionFixtureFor(t, nil)
	attempt := x.d.Assignment.Identity.AttemptID
	lease := x.lease(t)
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	if _, err := x.s.RecoverResultPending(ctx, attempt, execwire.Journal{}); !errors.Is(err, p.InvalidTransition) {
		t.Fatal("recovered a live attempt", err)
	}
	reopen(t, &x)
	if _, err := x.s.RecoverResultPending(ctx, attempt, execwire.Journal{}); err == nil || !strings.Contains(err.Error(), "exit") {
		t.Fatal("recovered without exit evidence", err)
	}
	journal := execwire.Journal{DispatchID: x.d.ID, Identity: x.d.Assignment.Identity, RunnerBoot: x.facts.RunnerBoot, DaemonBoot: x.s.meta.DaemonBoot, State: p.ResultPending}
	corrupt := journal
	corrupt.Corrupt = true
	if _, err := x.s.RecoverResultPending(ctx, attempt, corrupt); err == nil {
		t.Fatal("recovered on a corrupt journal")
	}
	foreign := journal
	foreign.RunnerBoot = newID()
	if _, err := x.s.RecoverResultPending(ctx, attempt, foreign); err == nil {
		t.Fatal("recovered on another boot's journal")
	}
	m, err := x.s.RecoverResultPending(ctx, attempt, journal)
	if err != nil || m.From != p.Unknown || m.To != p.ResultPending {
		t.Fatal(m, err)
	}
	if state, revision := attemptRow(t, x.s, attempt); state != p.ResultPending || revision != 5 || taskRow(t, x.s, x.d.Request.TaskID) != p.TaskVerifying {
		t.Fatal(state, revision)
	}
	rowCount(t, x.s, "runtime_observations WHERE kind='recovered'", 1)
	again, err := x.s.RecoverResultPending(ctx, attempt, journal)
	if err != nil || !reflect.DeepEqual(again, m) {
		t.Fatal("replay differs", again, err)
	}
	// With a stop latched, a second unknown attempt is not recovered.
	y := executionFixtureFor(t, nil)
	y.run(t)
	stop := stopRequest(c.CancelAttempt, y.d)
	if _, err := y.s.RequestStop(ctx, y.owner, stop); err != nil {
		t.Fatal(err)
	}
	reopen(t, &y)
	if _, err := y.s.RecoverResultPending(ctx, y.d.Assignment.Identity.AttemptID, execwire.Journal{}); err == nil || !strings.Contains(err.Error(), "stop_latched") {
		t.Fatal("recovered under a stop latch", err)
	}
}
