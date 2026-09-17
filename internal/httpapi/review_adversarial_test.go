// Adopted from the cross-model review of the execution channel (S1, #115):
// each case reproduced a defect on the reviewed head and must keep passing.

package httpapi

import (
	"context"
	"database/sql"
	"github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func TestReviewPausedAccept(t *testing.T) {
	h := newExecutionHarness(t)
	client, base := h.serve(t)
	session := h.sessionID(t)
	sess, err := h.s.ExecutionSession(context.Background(), pin(t, h.runner), session)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(h.root, "state", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE daemon_state SET paused=1"); err != nil {
		t.Fatal(err)
	}
	m := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newTestID(), Identity: h.d.Assignment.Identity, AssignmentID: h.d.Assignment.AssignmentID, RunnerBoot: h.facts.RunnerBoot, DaemonBoot: sess.DaemonBoot}
	w := call(t, client, base, "POST", "/x/v1/messages", session, encode(t, execwire.MessageEnvelope{Version: execwire.Version, MessageID: m.MessageID, DispatchID: h.d.ID, Message: m}))
	if w.status != 409 {
		t.Fatalf("paused daemon accepted new assignment: status=%d body=%s", w.status, w.body)
	}
}

func TestReviewInboxCurrentBoot(t *testing.T) {
	h := newExecutionHarness(t)
	client, base := h.serve(t)
	profile, err := h.s.RepositoryProfile(context.Background(), pin(t, h.owner), h.facts.Repository.Repository)
	if err != nil {
		t.Fatal(err)
	}
	profile.ID = "second-repository"
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(dir, "second.git")
	gitRun(t, dir, "clone", "--quiet", "--bare", profile.Remote, remote)
	u := url.URL{Scheme: "file", Path: remote}
	profile.Remote = u.String()
	if _, err := h.s.RegisterRepository(context.Background(), pin(t, h.owner), 0, profile); err != nil {
		t.Fatal(err)
	}
	digest, err := profile.Digest()
	if err != nil {
		t.Fatal(err)
	}
	facts := h.facts
	facts.ID = "fresh-boot-other-pair"
	facts.RunnerBoot = newTestID()
	facts.Repository.Repository = profile.ID
	facts.Repository.ProfileDigest = digest
	facts.Repository.Remote = profile.Remote
	facts.LocalEnvelope.Repository = profile.ID
	if err := h.s.PublishEligibility(context.Background(), pin(t, h.owner), 0, facts); err != nil {
		t.Fatal(err)
	}
	sess, err := h.s.RunnerSession(context.Background(), pin(t, h.runner), store.HelloRecord{Version: execwire.Version, MessageID: newTestID(), RunnerBoot: facts.RunnerBoot, EligibilityID: facts.ID, EligibilityRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Mode != "normal" {
		t.Fatalf("fixture should get normal session: %+v", sess)
	}
	w := call(t, client, base, "GET", "/x/v1/inbox", sess.SessionID, nil)
	var inbox execwire.Inbox
	w.decode(t, &inbox)
	if len(inbox.Assignments) > 0 {
		t.Fatalf("session boot %s received assignment bound to boot %s", sess.RunnerBoot, inbox.Assignments[0].Facts.RunnerBoot)
	}
}

func TestReviewOuterMessageIdentity(t *testing.T) {
	h := newExecutionHarness(t)
	client, base := h.serve(t)
	session := h.sessionID(t)
	sess, err := h.s.ExecutionSession(context.Background(), pin(t, h.runner), session)
	if err != nil {
		t.Fatal(err)
	}
	accept := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newTestID(), Identity: h.d.Assignment.Identity, AssignmentID: h.d.Assignment.AssignmentID, RunnerBoot: h.facts.RunnerBoot, DaemonBoot: sess.DaemonBoot}
	if err := h.s.AcknowledgeAssignment(context.Background(), pin(t, h.runner), h.d.ID, accept); err != nil {
		t.Fatal(err)
	}
	sent := time.Now().UnixMilli()
	request := p.Message{Version: p.FencedVersion, Kind: "lease_request", MessageID: newTestID(), Identity: h.d.Assignment.Identity, Nonce: newTestID(), RunnerBoot: h.facts.RunnerBoot, DaemonBoot: sess.DaemonBoot, SentMS: &sent}
	if _, err := h.s.IssueLease(context.Background(), pin(t, h.runner), session, h.d.ID, p.FencedVersion, request); err != nil {
		t.Fatal(err)
	}
	starting := h.transition(t, p.Starting, execwire.Evidence{Kind: "launch_intent", Workspace: "/private/tmp/ws", BoundaryPort: 1, Nonce: request.Nonce})
	a := call(t, client, base, "POST", "/x/v1/messages", session, encode(t, starting))
	if a.status != 200 {
		t.Fatalf("setup: %s", a.body)
	}
	running := h.transition(t, p.Running, execwire.Evidence{Kind: "launched", GuardianPID: 100, PID: 101, PGID: 101, StartUnixNS: 1})
	running.MessageID = starting.MessageID
	b := call(t, client, base, "POST", "/x/v1/messages", session, encode(t, running))
	st, err := h.s.ExecutionState(context.Background(), pin(t, h.runner), session, h.d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.status != 409 {
		t.Fatalf("same outer message_id advances revision twice: revision=%d state=%s status=%d", st.Revision, st.AttemptState, b.status)
	}
}
