package httpapi

// Regressions for the cross-model review of the execution routes: durable
// fences behind refusals, failure injection on acknowledgement, and the
// daemon-side inbox writer this lane owns.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
)

func (h *executionHarness) db(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(h.root, "state", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func count(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

// A refused lease request is fenced durably before the 409 leaves the daemon,
// and the fence survives a daemon restart.
func TestLeaseRefusalFenceIsDurable(t *testing.T) {
	h := newExecutionHarness(t)
	handler := h.recorder(t)
	session := h.sessionID(t)
	db := h.db(t)
	sent := time.Now().Add(-time.Minute).UnixMilli()
	request := p.Message{Version: p.FencedVersion, Kind: "lease_request", MessageID: newTestID(), Identity: h.d.Assignment.Identity, Nonce: newTestID(), RunnerBoot: h.facts.RunnerBoot, DaemonBoot: newTestID(), SentMS: &sent}
	body := string(encode(t, execwire.LeaseEnvelope{Version: execwire.Version, MessageID: newTestID(), DispatchID: h.d.ID, Request: request}))
	w := h.do(t, handler, "POST", "/x/v1/lease", session, body)
	assertRouteError(t, w, 409, "boot_mismatch")
	var reason string
	if err := db.QueryRow("SELECT reason FROM control_fenced WHERE message_id=?", request.MessageID).Scan(&reason); err != nil || reason != "boot_mismatch" {
		t.Fatal("refusal not fenced before the reply", reason, err)
	}
	if count(t, db, "SELECT count(*) FROM control_leases") != 0 {
		t.Fatal("refused request issued a lease")
	}
	// The same message with the same body is the same fence; a changed body under
	// the id conflicts instead of re-fencing.
	w = h.do(t, handler, "POST", "/x/v1/lease", session, body)
	assertRouteError(t, w, 409, "boot_mismatch")
	changed := request
	changed.Nonce = newTestID()
	w = h.do(t, handler, "POST", "/x/v1/lease", session, string(encode(t, execwire.LeaseEnvelope{Version: execwire.Version, MessageID: newTestID(), DispatchID: h.d.ID, Request: changed})))
	assertRouteError(t, w, 409, "identity_conflict")
	if count(t, db, "SELECT count(*) FROM control_fenced") != 1 {
		t.Fatal("fence rows", count(t, db, "SELECT count(*) FROM control_fenced"))
	}
}

// Acknowledgement persistence that fails never acknowledges and leaves no
// receipt; the retried request then acknowledges and retains its receipt.
func TestAcknowledgementFailureInjection(t *testing.T) {
	h := newExecutionHarness(t)
	handler := h.recorder(t)
	session := h.sessionID(t)
	sess, err := h.s.ExecutionSession(context.Background(), pin(t, h.runner), session)
	if err != nil {
		t.Fatal(err)
	}
	db := h.db(t)
	if _, err := db.Exec("CREATE TRIGGER fail_ack BEFORE INSERT ON dispatch_acks BEGIN SELECT RAISE(ABORT,'interrupted'); END"); err != nil {
		t.Fatal(err)
	}
	accept := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newTestID(), Identity: h.d.Assignment.Identity, AssignmentID: h.d.Assignment.AssignmentID, RunnerBoot: h.facts.RunnerBoot, DaemonBoot: sess.DaemonBoot}
	body := string(encode(t, execwire.MessageEnvelope{Version: execwire.Version, MessageID: accept.MessageID, DispatchID: h.d.ID, Message: accept}))
	w := h.do(t, handler, "POST", "/x/v1/messages", session, body)
	assertRouteError(t, w, 503, "store_unavailable")
	if count(t, db, "SELECT count(*) FROM dispatch_acks") != 0 || count(t, db, "SELECT count(*) FROM runtime_observations WHERE kind='receipt'") != 0 {
		t.Fatal("failed acknowledgement left state")
	}
	if _, err := db.Exec("DROP TRIGGER fail_ack"); err != nil {
		t.Fatal(err)
	}
	w = h.do(t, handler, "POST", "/x/v1/messages", session, body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "acknowledged") {
		t.Fatal(w.Code, w.Body.String())
	}
	if count(t, db, "SELECT count(*) FROM dispatch_acks") != 1 || count(t, db, "SELECT count(*) FROM runtime_observations WHERE kind='receipt'") != 1 {
		t.Fatal("acknowledgement or receipt missing")
	}
	// Byte-identical replay; a changed accept under the same id conflicts
	// without touching the retained acknowledgement.
	again := h.do(t, handler, "POST", "/x/v1/messages", session, body)
	if again.Code != 200 || again.Body.String() != w.Body.String() {
		t.Fatal("replay differs", again.Body.String())
	}
	changed := accept
	changed.RunnerBoot = newTestID()
	w = h.do(t, handler, "POST", "/x/v1/messages", session, string(encode(t, execwire.MessageEnvelope{Version: execwire.Version, MessageID: accept.MessageID, DispatchID: h.d.ID, Message: changed})))
	assertRouteError(t, w, 409, "identity_conflict")
	if count(t, db, "SELECT count(*) FROM dispatch_acks") != 1 {
		t.Fatal("changed replay altered acknowledgements")
	}
	// Once acknowledged, any other accept for the dispatch is an identity
	// conflict, whichever session sends it.
	other := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newTestID(), Identity: h.d.Assignment.Identity, AssignmentID: h.d.Assignment.AssignmentID, RunnerBoot: newTestID(), DaemonBoot: sess.DaemonBoot}
	rebooted, err := h.s.RunnerSession(context.Background(), pin(t, h.runner), helloRecord(other.RunnerBoot, h.facts.ID, 1))
	if err != nil {
		t.Fatal(err)
	}
	w = h.do(t, handler, "POST", "/x/v1/messages", rebooted.SessionID, string(encode(t, execwire.MessageEnvelope{Version: execwire.Version, MessageID: other.MessageID, DispatchID: h.d.ID, Message: other})))
	assertRouteError(t, w, 409, "identity_conflict")
	// A dispatch not yet acknowledged cannot be acknowledged by a session of
	// another boot.
	g := newExecutionHarness(t)
	ghandler := g.recorder(t)
	gsess, err := g.s.RunnerSession(context.Background(), pin(t, g.runner), helloRecord(newTestID(), g.facts.ID, 1))
	if err != nil {
		t.Fatal(err)
	}
	fresh := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newTestID(), Identity: g.d.Assignment.Identity, AssignmentID: g.d.Assignment.AssignmentID, RunnerBoot: gsess.RunnerBoot, DaemonBoot: gsess.DaemonBoot}
	w = g.do(t, ghandler, "POST", "/x/v1/messages", gsess.SessionID, string(encode(t, execwire.MessageEnvelope{Version: execwire.Version, MessageID: fresh.MessageID, DispatchID: g.d.ID, Message: fresh})))
	assertRouteError(t, w, 409, "boot_mismatch")
	if count(t, g.db(t), "SELECT count(*) FROM dispatch_acks") != 0 {
		t.Fatal("foreign-boot session acknowledged")
	}
}

// The daemon-side writers this lane owns notify the inbox hub through the
// production handlers: a termination report that latches the lease clock, and a
// renewal fenced under a latched stop. The inbox re-reads durable state on each
// wakeup, so the signal itself is what these assert.
func TestDaemonSideWritersNotifyTheInbox(t *testing.T) {
	h := newExecutionHarness(t)
	handler := h.recorder(t)
	session := h.sessionID(t)
	ctx := context.Background()
	sess, err := h.s.ExecutionSession(ctx, pin(t, h.runner), session)
	if err != nil {
		t.Fatal(err)
	}
	accept := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newTestID(), Identity: h.d.Assignment.Identity, AssignmentID: h.d.Assignment.AssignmentID, RunnerBoot: h.facts.RunnerBoot, DaemonBoot: sess.DaemonBoot}
	if err := h.s.AcknowledgeAssignment(ctx, pin(t, h.runner), h.d.ID, accept); err != nil {
		t.Fatal(err)
	}
	sent := time.Now().UnixMilli()
	request := p.Message{Version: p.FencedVersion, Kind: "lease_request", MessageID: newTestID(), Identity: h.d.Assignment.Identity, Nonce: newTestID(), RunnerBoot: h.facts.RunnerBoot, DaemonBoot: sess.DaemonBoot, SentMS: &sent}
	if _, err := h.s.IssueLease(ctx, pin(t, h.runner), session, h.d.ID, p.FencedVersion, request); err != nil {
		t.Fatal(err)
	}
	wake, cancel := h.hub.Subscribe(InboxKey)
	defer cancel()
	expect := func(what string) {
		t.Helper()
		select {
		case <-wake:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not notify the inbox", what)
		}
	}
	terminated := p.Message{Version: p.FencedVersion, Kind: "terminated", MessageID: newTestID(), Identity: h.d.Assignment.Identity, StopID: execwire.ExpiryStopID(request.Nonce), RunnerBoot: h.facts.RunnerBoot, DaemonBoot: sess.DaemonBoot, ConfirmedProcess: "not_started", RemoteWork: "quiescent", EvidenceDigest: strings.Repeat("e", 64)}
	wall := time.Now().UTC()
	duration := int64(time.Millisecond)
	measurement := c.Measurement{RequestedAt: wall, AcknowledgedAt: wall, ObservedAt: &wall, RequestToAckNS: duration, AckToObservedNS: &duration}
	raw, _ := json.Marshal(struct {
		Version     string         `json:"version"`
		MessageID   string         `json:"message_id"`
		DispatchID  string         `json:"dispatch_id"`
		Message     p.Message      `json:"message"`
		Boundary    map[string]any `json:"boundary"`
		Measurement c.Measurement  `json:"measurement"`
	}{execwire.Version, terminated.MessageID, h.d.ID, terminated, map[string]any{"reservations": 0, "terminal_receipts": 0, "in_flight": 0, "quiescent": true}, measurement})
	w := h.do(t, handler, "POST", "/x/v1/messages", session, string(raw))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	expect("termination report")
	// The lease clock latched a cancel and observed it in the same transaction,
	// so a woken inbox re-reads and finds nothing pending; a renewal under the
	// latched stop is fenced and notifies again.
	db := h.db(t)
	if count(t, db, "SELECT count(*) FROM control_targets") != 1 || count(t, db, "SELECT count(*) FROM control_observations") != 1 {
		t.Fatal("lease-clock latch not recorded")
	}
	inbox := h.do(t, handler, "GET", "/x/v1/inbox", session, "")
	var parsed execwire.Inbox
	if inbox.Code != 200 || execwire.Decode(inbox.Body.Bytes(), &parsed) != nil || len(parsed.Cancels) != 0 {
		t.Fatalf("observed cancel still pending: %d %s", inbox.Code, inbox.Body.String())
	}
	renewal := request
	renewal.MessageID, renewal.Nonce = newTestID(), newTestID()
	w = h.do(t, handler, "POST", "/x/v1/lease", session, string(encode(t, execwire.LeaseEnvelope{Version: execwire.Version, MessageID: newTestID(), DispatchID: h.d.ID, Request: renewal})))
	assertRouteError(t, w, 409, "revoked_or_expired")
	expect("fenced renewal")
	if count(t, db, "SELECT count(*) FROM control_fenced") != 1 {
		t.Fatal("fenced renewal not durable")
	}
}

// Second review: a pending backlog that is not deliverable to this session
// (other runners or incarnations, corrupt or foreign rows) cannot starve an
// eligible dispatch that sorts after the first page.
func TestInboxPagesPastAnIneligibleBacklog(t *testing.T) {
	h := newExecutionHarness(t)
	handler := h.recorder(t)
	session := h.sessionID(t)
	db := h.db(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for n := range 130 {
		task := fmt.Sprintf("00000000-0000-4000-8000-%012d", n*3+1)
		attempt := fmt.Sprintf("00000000-0000-4000-8000-%012d", n*3+2)
		dispatch := fmt.Sprintf("00000000-0000-4000-8000-%012d", n*3+3)
		if _, err := tx.Exec("INSERT INTO tasks VALUES(?,'ready')", task); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec("INSERT INTO attempts VALUES(?,?,1,'assigned',1,?)", attempt, task, newTestID()); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec("INSERT INTO dispatches VALUES(?,?,?,?,?,?,1,?,'{}','{}',1)", dispatch, attempt, task, h.grant.ID, h.runnerID, h.facts.ID, strings.Repeat("0", 64)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	w := h.do(t, handler, "GET", "/x/v1/inbox", session, "")
	var inbox execwire.Inbox
	if w.Code != 200 || execwire.Decode(w.Body.Bytes(), &inbox) != nil || len(inbox.Assignments) != 1 || inbox.Assignments[0].ID != h.d.ID {
		t.Fatalf("eligible dispatch starved by the backlog: %d %s", w.Code, w.Body.String())
	}
}
