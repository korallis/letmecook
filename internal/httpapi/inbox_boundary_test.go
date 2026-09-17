package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/execwire"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

func (h *executionHarness) paddedEligibility(t *testing.T, paths []string) sc.Eligibility {
	t.Helper()
	facts := h.facts
	facts.Revision = 2
	facts.RunnerBoot = newTestID()
	facts.LocalEnvelope.Paths = paths
	if err := h.s.PublishEligibility(context.Background(), pin(t, h.owner), 1, facts); err != nil {
		t.Fatal(err)
	}
	return facts
}

func padPaths(n, size int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("pad/%04d/%s", i, strings.Repeat("a", size)))
	}
	return out
}

func fetchInbox(t *testing.T, handler http.Handler, cert tls.Certificate, session string) *httptest.ResponseRecorder {
	t.Helper()
	req := routeRequest(cert, true, "GET", "/x/v1/inbox?wait_ms=0", "")
	req.Header.Set("X-Gaffer-Session", session)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// releaseFixtureDispatch stops and reconciles the harness fixture dispatch so
// the probe's own padded dispatch is the only unresolved attempt (M1 admits at
// most one globally; reserve() counts attempts in any non-terminal state).
func (h *executionHarness) releaseFixtureDispatch(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := h.s.StopDispatch(ctx, pin(t, h.owner), h.grant.TaskID); err != nil {
		t.Fatal(err)
	}
	proof := store.Reconciliation{DispatchID: h.d.ID, Identity: h.d.Assignment.Identity, ExpectedRevision: 2, To: p.Cancelled, ConfirmedProcess: "not_started", RemoteWork: "quiescent", LaunchFenced: true, ArtifactsPreserved: true, EvidenceDigest: strings.Repeat("b", 64)}
	if err := h.s.ReconcileDispatch(ctx, pin(t, h.owner), proof); err != nil {
		t.Fatal(err)
	}
}

// tryDispatch admits one dispatch on the given facts with a fresh task whose
// grant envelope carries the given paths, returning the admission error.
func (h *executionHarness) tryDispatch(t *testing.T, facts sc.Eligibility, paths []string) (store.Dispatch, error) {
	t.Helper()
	ctx := context.Background()
	grant := h.grant
	grant.ID, grant.TaskID = newTestID(), newTestID()
	grant.Envelope.Paths = paths
	request := h.request
	request.ID = newTestID()
	request.Decision.ID = newTestID()
	request.Decision.EligibilityID = facts.ID
	request.Decision.Assessment.TaskID = grant.TaskID
	factsDigest, err := sc.Digest(facts)
	if err != nil {
		t.Fatal(err)
	}
	request.Decision.Eligibility = g.Revision{Number: facts.Revision, SHA256: factsDigest}
	decisionDigest, err := sc.Digest(request.Decision)
	if err != nil {
		t.Fatal(err)
	}
	grant.Envelope.RouteDecision = g.Revision{Number: 1, SHA256: decisionDigest}
	request.Request = g.Request{GrantID: grant.ID, TaskID: grant.TaskID, GrantRevision: 1, Action: "execute", Envelope: grant.Envelope}
	if _, err := h.s.ApproveExecution(ctx, "", grant); err != nil {
		t.Fatal(err)
	}
	return h.s.Dispatch(ctx, request)
}

// Inverted fourth-review probe: over-limit dispatches must fail before they
// reserve an attempt; the largest admitted dispatch must fit the closed decoder.
func TestS1Review4InboxSingleAssignmentWireBoundary(t *testing.T) {
	for _, name := range []string{"58x474", "maximal", "over-maximal"} {
		t.Run(name, func(t *testing.T) {
			h := newExecutionHarness(t)
			handler := h.recorder(t)
			h.releaseFixtureDispatch(t)
			paths := padPaths(58, 474)
			if name != "58x474" {
				paths = padPaths(58, 460)
				candidate := h.d
				candidate.Request.Envelope.Paths = paths
				candidate.Facts.LocalEnvelope.Paths = paths
				raw, err := json.Marshal(wireDispatch(candidate))
				if err != nil {
					t.Fatal(err)
				}
				// Each path appears twice (request and local eligibility).
				extra := (execwire.MaxBytes - execwire.InboxWrapperBytes - len(raw)) / 2
				if name == "over-maximal" {
					extra++
				}
				if extra < 0 {
					t.Fatal("fixture already too large")
				}
				for n := range paths {
					padding := extra / (len(paths) - n)
					paths[n] += strings.Repeat("a", padding)
					extra -= padding
				}
			}
			facts := h.paddedEligibility(t, paths)
			d, err := h.tryDispatch(t, facts, paths)
			if name != "maximal" {
				var refusal *g.Refusal
				if !errors.As(err, &refusal) || refusal.Code != "oversized" || refusal.Field != "dispatch_record" {
					t.Fatalf("oversized assignment not refused at admission: %v", err)
				}
				// Rejection must not consume the global sequential reservation.
				if _, err := h.tryDispatch(t, facts, []string{paths[0]}); err != nil {
					t.Fatal("rejected dispatch left an admission reservation", err)
				}
				return
			}
			if err != nil {
				t.Fatal("maximal dispatch refused", err)
			}
			sess, err := h.s.RunnerSession(context.Background(), pin(t, h.runner), helloRecord(facts.RunnerBoot, facts.ID, facts.Revision))
			if err != nil {
				t.Fatal(err)
			}
			w := fetchInbox(t, handler, h.runner, sess.SessionID)
			if w.Code != 200 || w.Body.Len() > execwire.MaxBytes || w.Body.Len() < execwire.MaxBytes-1 {
				t.Fatalf("maximal reply status=%d bytes=%d", w.Code, w.Body.Len())
			}
			var inbox execwire.Inbox
			if err := execwire.Decode(w.Body.Bytes(), &inbox); err != nil || len(inbox.Assignments) != 1 || inbox.Assignments[0].ID != d.ID {
				t.Fatal("maximal reply failed runner closed decoding", err)
			}
			t.Logf("maximal admitted inbox: %d bytes, closed decoder accepted", w.Body.Len())
		})
	}
}

func TestInboxWrapperAllowance(t *testing.T) {
	raw, err := json.Marshal(execwire.Inbox{Assignments: []execwire.Dispatch{}, Cancels: []p.Message{}, Paused: false, PollAfterMS: inboxPollAfterMS})
	if err != nil || len(raw) != execwire.InboxWrapperBytes {
		t.Fatalf("inbox wrapper drift: %s (%d bytes), reserved %d: %v", raw, len(raw), execwire.InboxWrapperBytes, err)
	}
}

func TestExecutionReplyWireLimit(t *testing.T) {
	h := newExecutionHarness(t)
	isolatedRegistry(t)
	Register("execution", func(Deps) []Route {
		return []Route{{Method: "GET", Pattern: "/x/v1/oversized-test", Role: "runner", Handle: func(context.Context, Actor, Request) (any, *Error) {
			return map[string]string{"padding": strings.Repeat("a", execwire.MaxBytes)}, nil
		}}}
	})
	handler := h.recorder(t)
	req := routeRequest(h.runner, true, "GET", "/x/v1/oversized-test", "")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assertRouteError(t, w, 503, "store_unavailable")
	if err := execwire.Decode(w.Body.Bytes(), &execwire.ErrorBody{}); err != nil {
		t.Fatal("overflow refusal is not bounded wire JSON", err)
	}
}
