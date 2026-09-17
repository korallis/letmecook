package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/runstream"
	p "github.com/korallis/letmecook/schemas/execution"
)

// executionFixture is an admitted, acknowledged dispatch with a normal-mode
// runner session: the state every /x/v1 route after accept starts from.
type executionFixture struct {
	dispatchFixture
	d       Dispatch
	session SessionRecord
}

func helloFor(f dispatchFixture) HelloRecord {
	return HelloRecord{Version: execwire.Version, MessageID: newID(), RunnerBoot: f.facts.RunnerBoot, EligibilityID: f.facts.ID, EligibilityRevision: f.facts.Revision, PolicyDigest: strings.Repeat("a", 64), Journals: []json.RawMessage{json.RawMessage(`{"corrupt":false}`)}}
}

func sessionFor(t *testing.T, f dispatchFixture) SessionRecord {
	t.Helper()
	sess, err := f.s.RunnerSession(ctx, f.runner, helloFor(f))
	if err != nil || sess.Mode != "normal" {
		t.Fatal(sess, err)
	}
	return sess
}

func acceptFor(f dispatchFixture, d Dispatch) p.Message {
	return p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newID(), Identity: d.Assignment.Identity, AssignmentID: d.Assignment.AssignmentID, RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.s.meta.DaemonBoot}
}

func executionFixtureFor(t *testing.T, change func(*dispatchFixture)) executionFixture {
	t.Helper()
	f := dispatchFixtureFor(t, change)
	d := admitted(t, f)
	sess := sessionFor(t, f)
	if err := f.s.AcknowledgeAssignment(ctx, f.runner, d.ID, acceptFor(f, d)); err != nil {
		t.Fatal(err)
	}
	return executionFixture{dispatchFixture: f, d: d, session: sess}
}

func attemptRow(t *testing.T, s *Store, id string) (p.AttemptState, int64) {
	t.Helper()
	var state p.AttemptState
	var revision int64
	if err := s.db.QueryRow("SELECT state,revision FROM attempts WHERE id=?", id).Scan(&state, &revision); err != nil {
		t.Fatal(err)
	}
	return state, revision
}

func taskRow(t *testing.T, s *Store, id string) p.TaskState {
	t.Helper()
	var state p.TaskState
	if err := s.db.QueryRow("SELECT state FROM tasks WHERE id=?", id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func (x *executionFixture) leaseRequest() p.Message {
	sent := time.Now().UnixMilli()
	return p.Message{Version: p.FencedVersion, Kind: "lease_request", MessageID: newID(), Identity: x.d.Assignment.Identity, Nonce: newID(), RunnerBoot: x.facts.RunnerBoot, DaemonBoot: x.s.meta.DaemonBoot, SentMS: &sent}
}

func (x *executionFixture) lease(t *testing.T) c.Lease {
	t.Helper()
	lease, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, x.leaseRequest())
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func (x *executionFixture) proposal(t *testing.T, to p.AttemptState) p.Message {
	t.Helper()
	state, revision := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID)
	return p.Message{Version: p.FencedVersion, Kind: "transition", MessageID: newID(), Identity: x.d.Assignment.Identity, ExpectedRevision: &revision, From: state, To: to}
}

func (x *executionFixture) propose(t *testing.T, to p.AttemptState, evidence RuntimeEvidence) p.Message {
	t.Helper()
	got, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, to), evidence)
	if err != nil {
		t.Fatalf("propose %s: %v", to, err)
	}
	return got
}

func launchIntent(nonce string) RuntimeEvidence {
	return RuntimeEvidence{Kind: "launch_intent", Workspace: "/private/tmp/gaffer-ws", BoundaryPort: 40001, Nonce: nonce}
}
func launchedEvidence() RuntimeEvidence {
	return RuntimeEvidence{Kind: "launched", GuardianPID: 100, PID: 101, PGID: 101, StartUnixNS: 1700000000000000001}
}
func exitEvidence(through int64) RuntimeEvidence {
	return RuntimeEvidence{Kind: "exit", Code: 0, PGID: 101, PGIDEmpty: true, ObservedUnixNS: 1700000000000000002, StreamThrough: through}
}

// run drives assigned -> starting -> running -> result_pending with a real lease
// and, when texts are given, a real stream through the daemon's sink.
func (x *executionFixture) run(t *testing.T, texts ...string) c.Lease {
	t.Helper()
	lease := x.lease(t)
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	through := int64(0)
	if len(texts) > 0 {
		records := spooledRecords(t, x.d.Assignment.Identity, texts...)
		ack, err := x.s.sinks().Receive(x.d.Assignment.Identity.AttemptID, x.d.Assignment.Identity, records)
		if err != nil {
			t.Fatal(err)
		}
		through = ack.Through
	}
	x.propose(t, p.ResultPending, exitEvidence(through))
	return lease
}

func spooledRecords(t *testing.T, identity p.Identity, texts ...string) []runstream.Record {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spool, err := runstream.CreateSpool(filepath.Join(base, "spool"), identity, runstream.MinLimit)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	out := []runstream.Record{}
	for _, text := range texts {
		r, err := spool.Append(runstream.Native{Version: "fixture", Kind: "text", Data: []byte(text)}, runstream.Normalized{Stream: "stdout", Text: text})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// secondRunner enrols and enables another runner so cross-runner refusals are
// exercised against a real principal, not a malformed fingerprint.
func secondRunner(t *testing.T, f dispatchFixture) (string, SessionRecord) {
	t.Helper()
	fingerprint := strings.Repeat("c", 64)
	invite, err := f.s.CreateEnrollment(ctx, f.owner, newID(), fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := f.s.Enroll(ctx, fingerprint, invite.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.UpdateIdentity(ctx, f.owner, principal.ID, 1, "enable", ""); err != nil {
		t.Fatal(err)
	}
	sess, err := f.s.RunnerSession(ctx, fingerprint, helloFor(f))
	if err != nil || sess.Mode != "recovery_only" {
		t.Fatal("other runner's eligibility admitted", sess, err)
	}
	return fingerprint, sess
}

func terminatedFor(x *executionFixture, stopID, remoteWork string) c.Evidence {
	m := p.Message{Version: p.FencedVersion, Kind: "terminated", MessageID: newID(), Identity: x.d.Assignment.Identity, StopID: stopID, RunnerBoot: x.facts.RunnerBoot, DaemonBoot: x.s.meta.DaemonBoot, ConfirmedProcess: "terminated", RemoteWork: remoteWork, EvidenceDigest: strings.Repeat("e", 64)}
	wall := time.Now().UTC()
	duration := int64(time.Millisecond)
	return c.Evidence{Terminated: m, Measurement: c.Measurement{RequestedAt: wall, AcknowledgedAt: wall, ObservedAt: &wall, RequestToAckNS: duration, AckToObservedNS: &duration}}
}

func quiescent() BoundaryState {
	return BoundaryState{Reservations: 2, TerminalReceipts: 2, Quiescent: true}
}

func reopen(t *testing.T, x *executionFixture) {
	t.Helper()
	if err := x.s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, x.s.dir, x.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	x.s = s
}

func TestRunnerSessionModesReplayAndCommitOrder(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	hello := helloFor(f)
	sess, err := f.s.RunnerSession(ctx, f.runner, hello)
	if err != nil || sess.Mode != "normal" || sess.DaemonBoot != f.s.meta.DaemonBoot || sess.Generation != f.s.meta.Generation || sess.DriftMS != 2000 || sess.TerminationMS != 5000 || sess.LeaseValidityMS != 20000 || sess.RenewEveryMS != 5000 || sess.Paused || sess.SessionID != dispatchID(hello.MessageID, "session") {
		t.Fatal(sess, err)
	}
	replay, err := f.s.RunnerSession(ctx, f.runner, hello)
	if err != nil || !reflect.DeepEqual(replay, sess) {
		t.Fatal("replay changed session", replay, err)
	}
	changed := hello
	changed.PolicyDigest = strings.Repeat("b", 64)
	if _, err := f.s.RunnerSession(ctx, f.runner, changed); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed hello under the same message id", err)
	}
	rowCount(t, f.s, "runner_sessions", 1)
	for name, bad := range map[string]HelloRecord{
		"version":   {Version: "other", MessageID: newID(), RunnerBoot: hello.RunnerBoot, EligibilityID: hello.EligibilityID, EligibilityRevision: 1},
		"boot":      {Version: execwire.Version, MessageID: newID(), RunnerBoot: "not-a-boot", EligibilityID: hello.EligibilityID, EligibilityRevision: 1},
		"revision":  {Version: execwire.Version, MessageID: newID(), RunnerBoot: hello.RunnerBoot, EligibilityID: hello.EligibilityID, EligibilityRevision: 0},
		"journal":   {Version: execwire.Version, MessageID: newID(), RunnerBoot: hello.RunnerBoot, EligibilityID: hello.EligibilityID, EligibilityRevision: 1, Journals: []json.RawMessage{json.RawMessage(`{`)}},
		"policy":    {Version: execwire.Version, MessageID: newID(), RunnerBoot: hello.RunnerBoot, EligibilityID: hello.EligibilityID, EligibilityRevision: 1, PolicyDigest: "short"},
		"oversized": {Version: execwire.Version, MessageID: newID(), RunnerBoot: hello.RunnerBoot, EligibilityID: hello.EligibilityID, EligibilityRevision: 1, Journals: []json.RawMessage{json.RawMessage(`"` + strings.Repeat("j", 70000) + `"`)}},
	} {
		if _, err := f.s.RunnerSession(ctx, f.runner, bad); err == nil {
			t.Fatalf("%s hello accepted", name)
		}
	}
	if _, err := f.s.RunnerSession(ctx, f.owner, helloFor(f)); !errors.Is(err, i.Denied) {
		t.Fatal("owner opened a runner session", err)
	}
	rowCount(t, f.s, "runner_sessions", 1)
	// A restarted runner (new boot) or a stale eligibility revision is admitted
	// only for evidence until the owner re-imports facts.
	restarted := helloFor(f)
	restarted.RunnerBoot = newID()
	if sess, err := f.s.RunnerSession(ctx, f.runner, restarted); err != nil || sess.Mode != "recovery_only" {
		t.Fatal("new boot admitted without facts", sess, err)
	}
	stale := helloFor(f)
	stale.EligibilityRevision = 2
	if sess, err := f.s.RunnerSession(ctx, f.runner, stale); err != nil || sess.Mode != "recovery_only" {
		t.Fatal("unpublished revision admitted", sess, err)
	}
	// Commit before reply: a failure before the commit leaves no row; after the
	// commit the row is durable even though the reply was withheld.
	for _, step := range []string{"before_session_commit", "after_session_commit"} {
		next := helloFor(f)
		f.s.controlHook = func(got string) error {
			if got == step {
				return errors.New("crash")
			}
			return nil
		}
		if _, err := f.s.RunnerSession(ctx, f.runner, next); err == nil {
			t.Fatal("reply after injected failure")
		}
		f.s.controlHook = nil
		var n int
		if err := f.s.db.QueryRow("SELECT count(*) FROM runner_sessions WHERE id=?", dispatchID(next.MessageID, "session")).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if want := map[string]int{"before_session_commit": 0, "after_session_commit": 1}[step]; n != want {
			t.Fatalf("%s: %d rows", step, n)
		}
		if _, err := f.s.RunnerSession(ctx, f.runner, next); err != nil {
			t.Fatal("retry after injected failure", err)
		}
	}
	// Sessions are immutable history.
	if _, err := f.s.db.Exec("DELETE FROM runner_sessions"); err == nil {
		t.Fatal("sessions deletable")
	}
	// A daemon restart invalidates every session; the same hello replays the old
	// row (old boot) and only a new hello opens a usable session.
	x := executionFixture{dispatchFixture: f, session: sess}
	reopen(t, &x)
	_, err = x.s.ExecutionSession(ctx, f.runner, sess.SessionID)
	requireReason(t, err, "session_stale")
	_, err = x.s.PendingCancels(ctx, f.runner, sess.SessionID)
	requireReason(t, err, "session_stale")
	old, err := x.s.RunnerSession(ctx, f.runner, hello)
	if err != nil || old.DaemonBoot == x.s.meta.DaemonBoot {
		t.Fatal("replay minted a new session", old, err)
	}
	fresh := sessionFor(t, x.dispatchFixture)
	if fresh.DaemonBoot != x.s.meta.DaemonBoot {
		t.Fatal(fresh)
	}
	if _, err := x.s.ExecutionSession(ctx, f.runner, fresh.SessionID); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionStateAndPendingCancels(t *testing.T) {
	x := executionFixtureFor(t, nil)
	st, err := x.s.ExecutionState(ctx, x.runner, x.session.SessionID, x.d.ID)
	if err != nil || st.AttemptState != p.Assigned || st.Revision != 1 || !st.Acknowledged || st.Released || st.LastLease.Reply.Kind != "" || st.Stream.Through != 0 || st.ReceiptID != "" || st.Head.ReceiptID != "" || len(st.StopTargets) != 0 || st.Paused || st.Identity != x.d.Assignment.Identity {
		t.Fatal(st, err)
	}
	lease := x.run(t, "hello", "world")
	st, err = x.s.ExecutionState(ctx, x.runner, x.session.SessionID, x.d.ID)
	if err != nil || st.AttemptState != p.ResultPending || st.Revision != 4 || !reflect.DeepEqual(st.LastLease, lease) || st.Stream.Through != 2 || st.Stream.Digest == "" {
		t.Fatal(st, err)
	}
	cancels, err := x.s.PendingCancels(ctx, x.runner, x.session.SessionID)
	if err != nil || len(cancels) != 0 {
		t.Fatal(cancels, err)
	}
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	cancels, err = x.s.PendingCancels(ctx, x.runner, x.session.SessionID)
	if err != nil || len(cancels) != 1 || cancels[0].Kind != "cancel" || cancels[0].StopID != stop.ID {
		t.Fatal(cancels, err)
	}
	st, err = x.s.ExecutionState(ctx, x.runner, x.session.SessionID, x.d.ID)
	if err != nil || len(st.StopTargets) != 1 || st.StopTargets[0].Cancel.StopID != stop.ID {
		t.Fatal(st, err)
	}
	other, otherSession := secondRunner(t, x.dispatchFixture)
	_, err = x.s.ExecutionState(ctx, other, otherSession.SessionID, x.d.ID)
	requireReason(t, err, "runner_disabled")
	if cancels, err := x.s.PendingCancels(ctx, other, otherSession.SessionID); err != nil || len(cancels) != 0 {
		t.Fatal("other runner saw a cancel", cancels, err)
	}
	if _, err := x.s.ExecutionState(ctx, x.runner, x.session.SessionID, newID()); err == nil {
		t.Fatal("unknown dispatch answered")
	}
	if _, err := x.s.ExecutionState(ctx, x.runner, otherSession.SessionID, x.d.ID); err == nil {
		t.Fatal("foreign session accepted")
	}
}

// insertBrief persists a task_briefs row for the fixture's task the way S4's
// CreateTask will, so brief-bound behaviour is exercised against real columns.
func insertBrief(t *testing.T, x executionFixture, harness, brief string, criteria []Criterion, paths, operations []string, settings, digest string) {
	t.Helper()
	var owner string
	if err := x.s.db.QueryRow("SELECT id FROM principals WHERE role='owner'").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	criteriaJSON, _ := json.Marshal(criteria)
	pathsJSON, _ := json.Marshal(paths)
	operationsJSON, _ := json.Marshal(operations)
	if _, err := x.s.db.Exec("INSERT INTO task_briefs VALUES(?,1,?,?,?,?,?,?,?,?,?,?,?,?)", x.d.Request.TaskID, x.facts.Repository.Repository, x.d.Request.Envelope.BaseCommit, brief, string(criteriaJSON), string(pathsJSON), string(operationsJSON), harness, settings, digest, strings.Repeat("a", 64), owner, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func TestProposeTransitionFakeHarnessLaunchesWithoutBoundary(t *testing.T) {
	x := executionFixtureFor(t, nil)
	insertBrief(t, x, "fake", "brief", nil, nil, nil, "{}", strings.Repeat("a", 64))
	lease := x.lease(t)
	withPort := launchIntent(lease.Request.Nonce)
	if _, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, p.Starting), withPort); !errors.Is(err, p.Malformed) {
		t.Fatal("fake harness intent with a boundary port accepted", err)
	}
	withPort.BoundaryPort = 0
	x.propose(t, p.Starting, withPort)
	if state, _ := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID); state != p.Starting {
		t.Fatal(state)
	}
}

func TestPausedDaemonStillDrainsEvidence(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t, "before pause")
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	sqlExec(t, x.s, "UPDATE daemon_state SET paused=1")
	if sess, err := x.s.ExecutionSession(ctx, x.runner, x.session.SessionID); err != nil || !sess.Paused {
		t.Fatal("paused flag not read live", sess, err)
	}
	if _, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, x.leaseRequest()); err == nil {
		t.Fatal("paused daemon issued a lease")
	}
	// Stream records, the stop proposal, usage and the termination report all
	// land while paused; only new work is refused.
	identity, err := x.s.StreamIdentity(ctx, x.runner, x.session.SessionID, x.d.Assignment.Identity.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	records := spooledRecords(t, identity, "before pause", "after pause")
	if ack, err := x.s.sinks().Receive(identity.AttemptID, identity, records); err != nil || ack.Through != 2 {
		t.Fatal(ack, err)
	}
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	if err := x.s.RecordUsage(ctx, x.runner, x.session.SessionID, UsageReport{Version: execwire.Version, MessageID: newID(), Identity: identity, Receipts: []UsageReceipt{{RequestID: "r", Terminal: true}}}); err != nil {
		t.Fatal(err)
	}
	reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, stop.ID, "quiescent"), quiescent())
	if err != nil || !reply.Released {
		t.Fatal(reply, err)
	}
	if state, _ := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID); state != p.Cancelled {
		t.Fatal(state)
	}
}

func TestTaskInputBindsBriefDigest(t *testing.T) {
	brief := "Change greeting.txt to say hello, gaffer."
	criteria := []Criterion{{ID: "c1", Text: "greeting updated"}, {ID: "c2", Text: "tests pass"}}
	paths := []string{"greeting.txt"}
	operations := []string{"read", "write"}
	settings := json.RawMessage(`{"edits":[{"content":"hello, gaffer\n","path":"greeting.txt"}],"mode":"edit"}`)
	digest, err := briefDigest(brief, criteria, paths, operations, "fake", settings)
	if err != nil {
		t.Fatal(err)
	}
	x := executionFixtureFor(t, func(f *dispatchFixture) {
		f.grant.Envelope.Brief.SHA256 = digest
		f.facts.LocalEnvelope.Brief.SHA256 = digest
		f.request.Decision.Assessment.Brief.SHA256 = digest
	})
	_, err = x.s.TaskInput(ctx, x.runner, x.session.SessionID, x.d.ID)
	requireReason(t, err, "reconciliation_required")
	// Stored with unsorted keys and whitespace: the canonical form is what binds.
	stored := `{"mode": "edit", "edits": [{"path": "greeting.txt", "content": "hello, gaffer\n"}]}`
	insertBrief(t, x, "fake", brief, criteria, paths, operations, stored, digest)
	in, err := x.s.TaskInput(ctx, x.runner, x.session.SessionID, x.d.ID)
	if err != nil || in.BriefSHA256 != digest || in.Brief != brief || !reflect.DeepEqual(in.Criteria, criteria) || !reflect.DeepEqual(in.Paths, paths) || !reflect.DeepEqual(in.Operations, operations) || in.Harness != "fake" || string(in.Settings) != string(settings) || in.BaseCommit != x.d.Request.Envelope.BaseCommit || in.Repository != x.facts.Repository.Repository || in.DispatchID != x.d.ID || in.TaskID != x.d.Request.TaskID {
		t.Fatalf("%+v %v", in, err)
	}
	again, err := x.s.TaskInput(ctx, x.runner, x.session.SessionID, x.d.ID)
	if err != nil || !reflect.DeepEqual(again, in) {
		t.Fatal("replay differs", err)
	}
	other, otherSession := secondRunner(t, x.dispatchFixture)
	_, err = x.s.TaskInput(ctx, other, otherSession.SessionID, x.d.ID)
	requireReason(t, err, "runner_disabled")
	if _, err := x.s.TaskInput(ctx, x.runner, x.session.SessionID, newID()); err == nil {
		t.Fatal("unknown dispatch answered")
	}
	// A brief revision that no longer matches the grant-bound digest is refused.
	sqlExec(t, x.s, "UPDATE task_briefs SET brief='edited after approval', revision=2 WHERE task_id='"+x.d.Request.TaskID+"'")
	_, err = x.s.TaskInput(ctx, x.runner, x.session.SessionID, x.d.ID)
	requireReason(t, err, "reconciliation_required")
}

func TestProposeTransitionRefusals(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.lease(t)
	before := snapshot(t, x.s)
	other, otherSession := secondRunner(t, x.dispatchFixture)
	restarted := helloFor(x.dispatchFixture)
	restarted.RunnerBoot = newID()
	rebooted, err := x.s.RunnerSession(ctx, x.runner, restarted)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		runner   string
		session  string
		to       p.AttemptState
		evidence RuntimeEvidence
		mutate   func(*p.Message)
		want     error
		reason   string
	}{
		{"bad edge", x.runner, x.session.SessionID, p.Running, launchedEvidence(), nil, p.InvalidTransition, ""},
		{"terminal target", x.runner, x.session.SessionID, p.Succeeded, RuntimeEvidence{}, nil, p.InvalidTransition, ""},
		{"unknown target", x.runner, x.session.SessionID, p.Unknown, RuntimeEvidence{}, nil, p.InvalidTransition, ""},
		{"foreign nonce", x.runner, x.session.SessionID, p.Starting, launchIntent(newID()), nil, p.ReconciliationRequired, ""},
		{"guardian pid in intent", x.runner, x.session.SessionID, p.Starting, RuntimeEvidence{Kind: "launch_intent", Workspace: "/ws", BoundaryPort: 1, GuardianPID: 7, Nonce: lease.Request.Nonce}, nil, p.Malformed, ""},
		{"intent without workspace", x.runner, x.session.SessionID, p.Starting, RuntimeEvidence{Kind: "launch_intent", BoundaryPort: 1, Nonce: lease.Request.Nonce}, nil, p.Malformed, ""},
		{"intent port zero without fake harness", x.runner, x.session.SessionID, p.Starting, RuntimeEvidence{Kind: "launch_intent", Workspace: "/ws", BoundaryPort: 0, Nonce: lease.Request.Nonce}, nil, p.Malformed, ""},
		{"intent port out of range", x.runner, x.session.SessionID, p.Starting, RuntimeEvidence{Kind: "launch_intent", Workspace: "/ws", BoundaryPort: 70000, Nonce: lease.Request.Nonce}, nil, p.Malformed, ""},
		{"intent wrong kind", x.runner, x.session.SessionID, p.Starting, launchedEvidence(), nil, p.Malformed, ""},
		{"no evidence", x.runner, x.session.SessionID, p.Starting, RuntimeEvidence{}, nil, p.Malformed, ""},
		{"wrong runner", other, otherSession.SessionID, p.Starting, launchIntent(lease.Request.Nonce), nil, nil, "runner_disabled"},
		{"stale session", x.runner, newID(), p.Starting, launchIntent(lease.Request.Nonce), nil, nil, "session_stale"},
		{"boot mismatch", x.runner, rebooted.SessionID, p.Starting, launchIntent(lease.Request.Nonce), nil, p.BootMismatch, ""},
		{"stale generation", x.runner, x.session.SessionID, p.Starting, launchIntent(lease.Request.Nonce), func(m *p.Message) { m.Identity.Generation = newID() }, p.StaleGeneration, ""},
		{"stale attempt", x.runner, x.session.SessionID, p.Starting, launchIntent(lease.Request.Nonce), func(m *p.Message) { m.Identity.Epoch = 2 }, p.StaleAttempt, ""},
		{"revision", x.runner, x.session.SessionID, p.Starting, launchIntent(lease.Request.Nonce), func(m *p.Message) { r := int64(9); m.ExpectedRevision = &r }, p.RevisionConflict, ""},
		{"version", x.runner, x.session.SessionID, p.Starting, launchIntent(lease.Request.Nonce), func(m *p.Message) { m.Version = p.Version }, p.UnknownVersion, ""},
		{"stop without latch", x.runner, x.session.SessionID, p.Stopping, RuntimeEvidence{Kind: "stop", Nonce: newID()}, nil, p.ReconciliationRequired, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := x.proposal(t, tc.to)
			if tc.mutate != nil {
				tc.mutate(&m)
			}
			_, err := x.s.ProposeTransition(ctx, tc.runner, tc.session, p.FencedVersion, m, tc.evidence)
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("want %v got %v", tc.want, err)
			}
			if tc.reason != "" {
				requireReason(t, err, tc.reason)
			}
			if !reflect.DeepEqual(before, snapshot(t, x.s)) {
				t.Fatal("refusal changed attempt history")
			}
			rowCount(t, x.s, "runtime_observations", 0)
		})
	}
	// launched must carry every process fact.
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	for _, zero := range []func(*RuntimeEvidence){func(e *RuntimeEvidence) { e.GuardianPID = 0 }, func(e *RuntimeEvidence) { e.PID = 0 }, func(e *RuntimeEvidence) { e.PGID = 0 }, func(e *RuntimeEvidence) { e.StartUnixNS = 0 }} {
		e := launchedEvidence()
		zero(&e)
		if _, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, p.Running), e); !errors.Is(err, p.Malformed) {
			t.Fatal("launched with a zero fact accepted", err)
		}
	}
	running := x.propose(t, p.Running, launchedEvidence())
	// exit must carry the empty-pgid observation and the sink watermark.
	if _, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, p.ResultPending), exitEvidence(3)); !errors.Is(err, p.ReconciliationRequired) {
		t.Fatal("watermark drift accepted", err)
	}
	e := exitEvidence(0)
	e.PGIDEmpty = false
	if _, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, p.ResultPending), e); !errors.Is(err, p.Malformed) {
		t.Fatal("exit without pgid observation accepted", err)
	}
	// An equal replay returns the recorded message without a second revision; an
	// unequal replay (same message, different evidence) conflicts.
	again, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, running, launchedEvidence())
	if err != nil || !reflect.DeepEqual(again, running) {
		t.Fatal("replay", again, err)
	}
	changed := launchedEvidence()
	changed.PID = 202
	if _, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, running, changed); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("unequal replay accepted", err)
	}
	if state, revision := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID); state != p.Running || revision != 3 {
		t.Fatal(state, revision)
	}
	rowCount(t, x.s, "runtime_observations WHERE kind<>'receipt'", 2)
	rowCount(t, x.s, "runtime_observations WHERE kind='receipt'", 2)
	// A latched stop refuses forward proposals with stop_latched but admits the
	// stop proposal itself; paused refuses everything.
	if _, err := x.s.RequestStop(ctx, x.owner, stopRequest(c.CancelAttempt, x.d)); err != nil {
		t.Fatal(err)
	}
	_, err = x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, p.ResultPending), exitEvidence(0))
	requireReason(t, err, "stop_latched")
	sqlExec(t, x.s, "UPDATE daemon_state SET paused=1")
	_, err = x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, p.ResultPending), exitEvidence(0))
	requireReason(t, err, "paused")
	// A paused daemon still drains stops.
	stopping := x.propose(t, p.Stopping, RuntimeEvidence{})
	sqlExec(t, x.s, "UPDATE daemon_state SET paused=0")
	if stopping.To != p.Stopping || taskRow(t, x.s, x.d.Request.TaskID) != p.TaskReconciling {
		t.Fatal("stop proposal", stopping)
	}
	if _, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, x.proposal(t, p.Stopping), RuntimeEvidence{}); !errors.Is(err, p.InvalidTransition) {
		t.Fatal("stopping accepted a further proposal", err)
	}
	for _, query := range []string{"UPDATE runtime_observations SET kind='x'", "DELETE FROM runtime_observations"} {
		if _, err := x.s.db.Exec(query); err == nil {
			t.Fatal("mutable evidence", query)
		}
	}
}

func TestProposeTransitionTaskStatesHooksAndLeaseLapse(t *testing.T) {
	x := executionFixtureFor(t, nil)
	lease := x.lease(t)
	for _, step := range []string{"before_transition_commit", "after_transition_commit"} {
		x.s.controlHook = func(got string) error {
			if got == step {
				return errors.New("crash")
			}
			return nil
		}
		m := x.proposal(t, p.Starting)
		if _, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, m, launchIntent(lease.Request.Nonce)); err == nil {
			t.Fatal("reply after injected failure")
		}
		x.s.controlHook = nil
		state, _ := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID)
		if step == "before_transition_commit" {
			if state != p.Assigned {
				t.Fatal("uncommitted transition visible", state)
			}
			rowCount(t, x.s, "runtime_observations", 0)
			continue
		}
		if state != p.Starting {
			t.Fatal("committed transition lost", state)
		}
		rowCount(t, x.s, "runtime_observations WHERE kind<>'receipt'", 1)
		got, err := x.s.ProposeTransition(ctx, x.runner, x.session.SessionID, p.FencedVersion, m, launchIntent(lease.Request.Nonce))
		if err != nil || !reflect.DeepEqual(got, m) {
			t.Fatal("lost reply not replayed", got, err)
		}
	}
	x.propose(t, p.Running, launchedEvidence())
	if taskRow(t, x.s, x.d.Request.TaskID) != p.TaskReady {
		t.Fatal("running changed task phase")
	}
	// The runner's own lease lapse: stopping cites the last nonce, no latch exists
	// yet, and the terminated report then latches it under the lease clock.
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop", Nonce: lease.Request.Nonce})
	if taskRow(t, x.s, x.d.Request.TaskID) != p.TaskReconciling {
		t.Fatal("stopping did not park the task")
	}
	rowCount(t, x.s, "control_targets", 0)
	stopID := execwire.ExpiryStopID(lease.Request.Nonce)
	reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, stopID, "quiescent"), quiescent())
	if err != nil || reply.Outcome != "observed" || !reply.Released {
		t.Fatal(reply, err)
	}
	receipt, err := x.s.StopStatus(ctx, stopID, x.d.Assignment.Identity.AttemptID)
	if err != nil || receipt.Receipt.Actor != "lease-clock" || receipt.Receipt.Request.Cause != "lease_expired" || receipt.Status != c.TerminationObserved {
		t.Fatal(receipt, err)
	}
	if state, _ := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID); state != p.Expired {
		t.Fatal("lease lapse did not expire the attempt", state)
	}
	var actor string
	if err := x.s.db.QueryRow("SELECT actor FROM dispatch_releases WHERE dispatch_id=?", x.d.ID).Scan(&actor); err != nil || actor != x.session.RunnerID {
		t.Fatal("release actor", actor, err)
	}
}

func TestIssueLeaseReplayFencingAndCommitOrder(t *testing.T) {
	x := executionFixtureFor(t, nil)
	request := x.leaseRequest()
	lease, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, request)
	if err != nil || *lease.Reply.ValidityMS != 20000 || lease.MarginNS != int64(7*time.Second) || lease.Reply.Nonce != request.Nonce || lease.Reply.MessageID != dispatchID(request.Nonce, "lease-reply") || lease.DispatchID != x.d.ID {
		t.Fatal(lease, err)
	}
	if p.CheckLease(request, lease.Reply, x.d.Assignment.Identity, p.Timing{RunnerBoot: x.facts.RunnerBoot, DaemonBoot: x.s.meta.DaemonBoot, ReceivedMS: *request.SentMS, DriftMS: 2000, TerminationMS: 5000, NonceActive: true}).Reason != p.OK {
		t.Fatal("issued reply fails the runner's own check")
	}
	again, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, request)
	if err != nil || !reflect.DeepEqual(again, lease) {
		t.Fatal("same nonce changed the retained reply", again, err)
	}
	rowCount(t, x.s, "control_leases", 1)
	renewal := x.leaseRequest()
	second, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, renewal)
	if err != nil || second.Request.Nonce != renewal.Nonce {
		t.Fatal(second, err)
	}
	rowCount(t, x.s, "control_leases", 2)
	// Every refusal is fenced and committed before it is reported.
	fenced := 0
	refuse := func(name string, request p.Message, want error, reason string) {
		t.Helper()
		_, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, request)
		if want != nil && !errors.Is(err, want) {
			t.Fatalf("%s: want %v got %v", name, want, err)
		}
		if reason != "" {
			requireReason(t, err, reason)
		}
		fenced++
		rowCount(t, x.s, "control_fenced", fenced)
		var got string
		if err := x.s.db.QueryRow("SELECT reason FROM control_fenced WHERE message_id=?", request.MessageID).Scan(&got); err != nil {
			t.Fatalf("%s: fence not committed: %v", name, err)
		}
	}
	delayed := x.leaseRequest()
	sent := time.Now().Add(-20 * time.Second).UnixMilli()
	delayed.SentMS = &sent
	refuse("delayed", delayed, p.DelayedReply, "")
	wrongBoot := x.leaseRequest()
	wrongBoot.RunnerBoot = newID()
	refuse("boot", wrongBoot, p.BootMismatch, "")
	staleGeneration := x.leaseRequest()
	staleGeneration.Identity.Generation = newID()
	refuse("generation", staleGeneration, p.StaleGeneration, "")
	sqlExec(t, x.s, "UPDATE daemon_state SET paused=1")
	refuse("paused", x.leaseRequest(), nil, "paused")
	sqlExec(t, x.s, "UPDATE daemon_state SET paused=0")
	rowCount(t, x.s, "control_leases", 2)
	// Owner and foreign runner credentials never obtain a lease.
	if _, err := x.s.IssueLease(ctx, x.owner, x.session.SessionID, x.d.ID, p.FencedVersion, x.leaseRequest()); !errors.Is(err, i.Denied) {
		t.Fatal("owner leased", err)
	}
	other, otherSession := secondRunner(t, x.dispatchFixture)
	_, err = x.s.IssueLease(ctx, other, otherSession.SessionID, x.d.ID, p.FencedVersion, x.leaseRequest())
	requireReason(t, err, "runner_disabled")
	_, err = x.s.IssueLease(ctx, x.runner, otherSession.SessionID, x.d.ID, p.FencedVersion, x.leaseRequest())
	requireReason(t, err, "session_stale")
	// A retained nonce replays its reply even after revocation (never extended),
	// a changed request under it conflicts, and a fresh nonce under the latched
	// stop is fenced as revoked_or_expired.
	if _, err := x.s.RequestStop(ctx, x.owner, stopRequest(c.GlobalStop, x.d)); err != nil {
		t.Fatal(err)
	}
	if replayed, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, renewal); err != nil || !reflect.DeepEqual(replayed, second) {
		t.Fatal("retained nonce not replayed after revocation", replayed, err)
	}
	revoked := x.leaseRequest()
	revoked.Nonce = renewal.Nonce
	if _, err := x.s.IssueLease(ctx, x.runner, x.session.SessionID, x.d.ID, p.FencedVersion, revoked); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed request under a retained nonce accepted", err)
	}
	refuse("latched", x.leaseRequest(), c.ErrFenced, "")
	rowCount(t, x.s, "control_leases", 2)
	// Commit before reply on issuance.
	y := executionFixtureFor(t, nil)
	for _, step := range []string{"before_lease_commit", "after_lease_commit"} {
		request := y.leaseRequest()
		y.s.controlHook = func(got string) error {
			if got == step {
				return errors.New("crash")
			}
			return nil
		}
		if _, err := y.s.IssueLease(ctx, y.runner, y.session.SessionID, y.d.ID, p.FencedVersion, request); err == nil {
			t.Fatal("lease replied after injected failure")
		}
		y.s.controlHook = nil
		var n int
		if err := y.s.db.QueryRow("SELECT count(*) FROM control_leases WHERE nonce=?", request.Nonce).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if want := map[string]int{"before_lease_commit": 0, "after_lease_commit": 1}[step]; n != want {
			t.Fatalf("%s: %d leases", step, n)
		}
		if _, err := y.s.IssueLease(ctx, y.runner, y.session.SessionID, y.d.ID, p.FencedVersion, request); err != nil {
			t.Fatal("retry after injected failure", err)
		}
	}
}

func TestReportTerminationReleasesOnlyWithQuiescentEvidence(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	if cancels, err := x.s.PendingCancels(ctx, x.runner, x.session.SessionID); err != nil || len(cancels) != 1 {
		t.Fatal(cancels, err)
	}
	evidence := terminatedFor(&x, stop.ID, "unknown")
	busy := BoundaryState{Reservations: 2, TerminalReceipts: 1, InFlight: 1}
	reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, busy)
	if err != nil || reply.Outcome != "observed" || reply.Released {
		t.Fatal("unquiescent boundary released", reply, err)
	}
	if state, _ := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID); state != p.Stopping {
		t.Fatal(state)
	}
	if cancels, err := x.s.PendingCancels(ctx, x.runner, x.session.SessionID); err != nil || len(cancels) != 0 {
		t.Fatal("observed cancel still pending", cancels, err)
	}
	rowCount(t, x.s, "dispatch_releases", 0)
	rowCount(t, x.s, "runtime_observations WHERE kind='terminated'", 1)
	// The first report under a message id decides: changed evidence, a changed
	// boundary or changed remote_work under it are identity conflicts, and the
	// identical report replays the same reply.
	changed := evidence
	changed.Terminated.EvidenceDigest = strings.Repeat("f", 64)
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, changed, quiescent()); !errors.Is(err, p.IdentityConflict) {
		t.Fatal(err)
	}
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, quiescent()); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed boundary under the same message accepted", err)
	}
	settled := terminatedFor(&x, stop.ID, "quiescent")
	settled.Terminated.MessageID = evidence.Terminated.MessageID
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, settled, quiescent()); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed remote_work under the same message accepted", err)
	}
	if same, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, busy); err != nil || !reflect.DeepEqual(same, reply) {
		t.Fatal("identical report replay changed", same, err)
	}
	other, otherSession := secondRunner(t, x.dispatchFixture)
	_, err = x.s.ReportTermination(ctx, other, otherSession.SessionID, p.FencedVersion, settled, quiescent())
	requireReason(t, err, "runner_disabled")
	// A later stop with an inconsistent or unknown-remote-work report still does
	// not release; only a settled, quiescent, confirmed report does.
	second := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, second); err != nil {
		t.Fatal(err)
	}
	unsettled := terminatedFor(&x, second.ID, "quiescent")
	reply, err = x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, unsettled, BoundaryState{Reservations: 2, TerminalReceipts: 1, Quiescent: true})
	if err != nil || reply.Released {
		t.Fatal("inconsistent boundary released", reply, err)
	}
	third := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, third); err != nil {
		t.Fatal(err)
	}
	reply, err = x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, third.ID, "unknown"), quiescent())
	if err != nil || reply.Released {
		t.Fatal("remote_work unknown released", reply, err)
	}
	fourth := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, fourth); err != nil {
		t.Fatal(err)
	}
	settled = terminatedFor(&x, fourth.ID, "quiescent")
	for range 2 {
		reply, err = x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, settled, quiescent())
		if err != nil || reply.Outcome != "observed" || !reply.Released {
			t.Fatal(reply, err)
		}
	}
	if state, _ := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID); state != p.Cancelled {
		t.Fatal(state)
	}
	var actor string
	if err := x.s.db.QueryRow("SELECT actor FROM dispatch_releases WHERE dispatch_id=?", x.d.ID).Scan(&actor); err != nil || actor != x.session.RunnerID {
		t.Fatal("release actor", actor, err)
	}
	retained, err := x.s.Assignment(ctx, x.d.ID)
	if err != nil || !retained.Released {
		t.Fatal(err)
	}
	// Global capacity is free again (the cancelled task itself stays latched).
	if _, err := x.s.Dispatch(ctx, otherTaskRequest(t, x.dispatchFixture)); err != nil {
		t.Fatal("release did not free capacity", err)
	}
}

func TestReportTerminationRetainsOldBootEvidence(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t)
	stop := stopRequest(c.CancelAttempt, x.d)
	if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
		t.Fatal(err)
	}
	x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	evidence := terminatedFor(&x, stop.ID, "quiescent")
	reopen(t, &x)
	x.session = sessionFor(t, x.dispatchFixture)
	reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, quiescent())
	if err != nil || reply.Outcome != "retained" || reply.Released {
		t.Fatal("old-boot evidence promoted", reply, err)
	}
	rowCount(t, x.s, "runtime_observations WHERE kind='terminated'", 1)
	rowCount(t, x.s, "control_observations", 0)
	rowCount(t, x.s, "dispatch_releases", 0)
	if state, _ := attemptRow(t, x.s, x.d.Assignment.Identity.AttemptID); state != p.Unknown {
		t.Fatal(state)
	}
	// Current-boot evidence for a target latched under the old boot is a boot
	// mismatch, not a release: only reconcile may act on it.
	current := terminatedFor(&x, stop.ID, "quiescent")
	if _, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, current, quiescent()); !errors.Is(err, p.BootMismatch) {
		t.Fatal(err)
	}
	rowCount(t, x.s, "dispatch_releases", 0)
}

func TestRecordRefusalAndUsage(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	d := admitted(t, f)
	sess := sessionFor(t, f)
	refusal := p.Message{Version: p.FencedVersion, Kind: "refuse", MessageID: newID(), Identity: d.Assignment.Identity, InReplyTo: d.Assignment.MessageID, Reason: p.ReconciliationRequired}
	for range 2 {
		if err := f.s.RecordRefusal(ctx, f.runner, sess.SessionID, p.FencedVersion, d.ID, refusal); err != nil {
			t.Fatal(err)
		}
	}
	rowCount(t, f.s, "runtime_observations WHERE kind='refused'", 1)
	wrong := refusal
	wrong.InReplyTo = newID()
	if err := f.s.RecordRefusal(ctx, f.runner, sess.SessionID, p.FencedVersion, d.ID, wrong); !errors.Is(err, p.IdentityConflict) {
		t.Fatal(err)
	}
	if err := f.s.AcknowledgeAssignment(ctx, f.runner, d.ID, acceptFor(f, d)); err != nil {
		t.Fatal(err)
	}
	late := refusal
	late.MessageID = newID()
	if err := f.s.RecordRefusal(ctx, f.runner, sess.SessionID, p.FencedVersion, d.ID, late); !errors.Is(err, p.ReconciliationRequired) {
		t.Fatal("refusal after accept recorded", err)
	}
	x := executionFixture{dispatchFixture: f, d: d, session: sess}
	reservation := UsageReceipt{RequestID: "req-1", Protocol: "openai-chat", Model: "model-a", Source: "boundary", StartedMS: 1}
	report := UsageReport{Version: execwire.Version, MessageID: newID(), Identity: d.Assignment.Identity, Receipts: []UsageReceipt{reservation}}
	for range 2 {
		if err := x.s.RecordUsage(ctx, x.runner, sess.SessionID, report); err != nil {
			t.Fatal(err)
		}
	}
	terminal := reservation
	terminal.Terminal, terminal.Status, terminal.EndedMS, terminal.PromptTokens, terminal.CompletionTokens = true, 200, 2, 10, 20
	report.MessageID, report.Receipts = newID(), []UsageReceipt{terminal, {RequestID: "req-2", Source: "boundary"}}
	if err := x.s.RecordUsage(ctx, x.runner, sess.SessionID, report); err != nil {
		t.Fatal(err)
	}
	// A late reservation never regresses a terminal receipt.
	report.MessageID, report.Receipts = newID(), []UsageReceipt{reservation}
	if err := x.s.RecordUsage(ctx, x.runner, sess.SessionID, report); err != nil {
		t.Fatal(err)
	}
	got, err := x.s.AttemptUsage(ctx, d.Assignment.Identity.AttemptID)
	if err != nil || len(got) != 2 || !reflect.DeepEqual(got[0], terminal) || got[1].RequestID != "req-2" {
		t.Fatal(got, err)
	}
	stale := report
	stale.Identity.Generation = newID()
	if err := x.s.RecordUsage(ctx, x.runner, sess.SessionID, stale); !errors.Is(err, p.StaleAttempt) {
		t.Fatal(err)
	}
	bad := report
	bad.Receipts = []UsageReceipt{{RequestID: ""}}
	if err := x.s.RecordUsage(ctx, x.runner, sess.SessionID, bad); !errors.Is(err, p.Malformed) {
		t.Fatal(err)
	}
	if _, err := x.s.db.Exec("DELETE FROM attempt_usage"); err == nil {
		t.Fatal("usage deletable")
	}
}

// TestExecutionOwnedProcess is the SIGKILLed daemon: it opens the store, opens
// a session, runs one execution-channel write with the named crash step and
// blocks until killed. Recovery has already moved the attempt to unknown, so
// the proposals it makes are the unknown-edge recovery proposals.
func TestExecutionOwnedProcess(t *testing.T) {
	mode := os.Getenv("GAFFER_EXECUTION_TEST_CHILD")
	if mode == "" {
		return
	}
	s, err := Open(ctx, os.Getenv("GAFFER_EXECUTION_TEST_DIR"), os.Getenv("GAFFER_EXECUTION_TEST_ARTIFACTS"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var hello HelloRecord
	if err := json.Unmarshal([]byte(os.Getenv("GAFFER_EXECUTION_TEST_HELLO")), &hello); err != nil {
		t.Fatal(err)
	}
	runner := os.Getenv("GAFFER_EXECUTION_TEST_RUNNER")
	sess, err := s.RunnerSession(ctx, runner, hello)
	if err != nil {
		t.Fatal(err)
	}
	var identity p.Identity
	if err := json.Unmarshal([]byte(os.Getenv("GAFFER_EXECUTION_TEST_IDENTITY")), &identity); err != nil {
		t.Fatal(err)
	}
	kind, step, _ := strings.Cut(mode, ":")
	s.controlHook = func(got string) error {
		if got == step {
			fmt.Println("interrupted")
			_, _ = bufio.NewReader(os.Stdin).ReadByte()
		}
		return nil
	}
	state, revision := attemptRow(t, s, identity.AttemptID)
	if state != p.Unknown {
		t.Fatal("recovery did not quarantine the attempt", state)
	}
	m := p.Message{Version: p.FencedVersion, Kind: "transition", MessageID: newID(), Identity: identity, ExpectedRevision: &revision, From: p.Unknown, To: p.Stopping}
	switch kind {
	case "transition":
		_, err = s.ProposeTransition(ctx, runner, sess.SessionID, p.FencedVersion, m, RuntimeEvidence{Kind: "stop", Nonce: os.Getenv("GAFFER_EXECUTION_TEST_NONCE")})
	case "finalize":
		m.To = p.ResultPending
		if _, err := s.ProposeTransition(ctx, runner, sess.SessionID, p.FencedVersion, m, exitEvidence(0)); err != nil {
			t.Fatal(err)
		}
		digest, err := s.Streams().Digest(identity.AttemptID, 0)
		if err != nil {
			t.Fatal(err)
		}
		completion := Completion{Version: execwire.Version, MessageID: newID(), ReceiptID: os.Getenv("GAFFER_EXECUTION_TEST_RECEIPT"), Stream: StreamWatermark{Digest: digest}, Exit: ExitRecord{PGID: 101, ObservedUnixNS: exitEvidence(0).ObservedUnixNS}, Boundary: quiescent()}
		_, err = s.FinalizeAttempt(ctx, runner, sess.SessionID, identity.AttemptID, completion)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash test escaped barrier")
}

func executionChild(t *testing.T, x executionFixture, mode string, extra ...string) {
	t.Helper()
	hello, _ := json.Marshal(helloFor(x.dispatchFixture))
	identity, _ := json.Marshal(x.d.Assignment.Identity)
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(deadline, os.Args[0], "-test.run=^TestExecutionOwnedProcess$")
	cmd.Env = append(os.Environ(), "GAFFER_EXECUTION_TEST_CHILD="+mode, "GAFFER_EXECUTION_TEST_DIR="+x.s.dir, "GAFFER_EXECUTION_TEST_ARTIFACTS="+x.artifacts, "GAFFER_EXECUTION_TEST_RUNNER="+x.runner, "GAFFER_EXECUTION_TEST_HELLO="+string(hello), "GAFFER_EXECUTION_TEST_IDENTITY="+string(identity))
	cmd.Env = append(cmd.Env, extra...)
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
	if !scanner.Scan() || scanner.Text() != "interrupted" {
		lines := []string{scanner.Text()}
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		t.Fatalf("child %s: %q (%v)", mode, strings.Join(lines, "|"), scanner.Err())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed child exited successfully")
	}
}

func TestExecutionSIGKILLAroundTransitionCommit(t *testing.T) {
	for _, step := range []string{"before_transition_commit", "after_transition_commit"} {
		t.Run(step, func(t *testing.T) {
			x := executionFixtureFor(t, nil)
			lease := x.lease(t)
			if err := x.s.Close(); err != nil {
				t.Fatal(err)
			}
			executionChild(t, x, "transition:"+step, "GAFFER_EXECUTION_TEST_NONCE="+lease.Request.Nonce)
			s, err := Open(ctx, x.s.dir, x.artifacts)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			state, revision := attemptRow(t, s, x.d.Assignment.Identity.AttemptID)
			if step == "before_transition_commit" {
				// Recovery moved assigned to unknown; the interrupted proposal left nothing.
				if state != p.Unknown || revision != 2 {
					t.Fatal("uncommitted proposal survived SIGKILL", state, revision)
				}
				rowCount(t, s, "runtime_observations", 0)
				return
			}
			// The committed stop is revision 3; this restart's recovery then moved the
			// stopping attempt to unknown (revision 4) without touching that history.
			var message string
			if err := s.db.QueryRow("SELECT message FROM events WHERE attempt_id=? AND revision=3", x.d.Assignment.Identity.AttemptID).Scan(&message); err != nil {
				t.Fatal("committed proposal lost after SIGKILL", state, revision, err)
			}
			if stop, err := p.Decode([]byte(message)); err != nil || stop.To != p.Stopping || state != p.Unknown || revision != 4 || taskRow(t, s, x.d.Request.TaskID) != p.TaskReconciling {
				t.Fatal("committed proposal lost after SIGKILL", state, revision, message, err)
			}
			rowCount(t, s, "runtime_observations WHERE kind='stop'", 1)
			consistent(t, s)
		})
	}
}
