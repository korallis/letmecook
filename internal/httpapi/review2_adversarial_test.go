// Adopted from the second cross-model review of the execution channel (S1,
// #115): each case reproduced a defect on the reviewed head and must keep passing.

package httpapi

import (
	"context"
	"github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
	"strings"
	"testing"
)

func TestReview2AcceptLostReceiptReplaysWhenPaused(t *testing.T) {
	h := newExecutionHarness(t)
	handler := h.recorder(t)
	session := h.sessionID(t)
	sess, err := h.s.ExecutionSession(context.Background(), pin(t, h.runner), session)
	if err != nil {
		t.Fatal(err)
	}
	db := h.db(t)
	if _, err := db.Exec("CREATE TRIGGER fail_receipt BEFORE INSERT ON runtime_observations WHEN NEW.kind='receipt' BEGIN SELECT RAISE(ABORT,'interrupted'); END"); err != nil {
		t.Fatal(err)
	}
	accept := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newTestID(), Identity: h.d.Assignment.Identity, AssignmentID: h.d.Assignment.AssignmentID, RunnerBoot: h.facts.RunnerBoot, DaemonBoot: sess.DaemonBoot}
	body := string(encode(t, execwire.MessageEnvelope{Version: execwire.Version, MessageID: accept.MessageID, DispatchID: h.d.ID, Message: accept}))
	first := h.do(t, handler, "POST", "/x/v1/messages", session, body)
	if first.Code != 503 || count(t, db, "SELECT count(*) FROM dispatch_acks") != 1 {
		t.Fatalf("setup: %d %s", first.Code, first.Body.String())
	}
	if _, err := db.Exec("DROP TRIGGER fail_receipt; UPDATE daemon_state SET paused=1"); err != nil {
		t.Fatal(err)
	}
	replay := h.do(t, handler, "POST", "/x/v1/messages", session, body)
	if replay.Code != 200 || !strings.Contains(replay.Body.String(), "acknowledged") {
		t.Fatalf("durable accept cannot replay after lost receipt + pause: %d %s", replay.Code, replay.Body.String())
	}
}
