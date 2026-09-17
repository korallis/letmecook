package backup

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

func TestRestoreIssueLeaseFencesOldGeneration(t *testing.T) {
	f := testFixture(t, true)
	f.retainedDispatch(t)
	assignment, err := f.s.Assignment(ctx, f.dispatch)
	must(t, err)
	status, err := f.s.Status(ctx)
	must(t, err)
	hello := store.HelloRecord{Version: "execution-channel-provisional-v1", MessageID: uuid(), RunnerBoot: assignment.Facts.RunnerBoot, EligibilityID: assignment.Facts.ID, EligibilityRevision: assignment.Facts.Revision, PolicyDigest: strings.Repeat("a", 64)}
	session, err := f.s.RunnerSession(ctx, f.runner, hello)
	must(t, err)
	if session.Mode != "normal" {
		t.Fatal("source session did not bind enabled eligibility", session)
	}
	must(t, session.Binds(assignment))
	out := f.backup(t)
	state, art := filepath.Join(f.root, "issue-lease-state"), filepath.Join(f.root, "issue-lease-artifacts")
	_, err = Restore(ctx, out, state, art)
	must(t, err)
	s, err := store.Open(ctx, state, art)
	must(t, err)
	defer s.Close()
	sent := time.Now().UnixMilli()
	request := p.Message{Version: p.FencedVersion, Kind: "lease_request", MessageID: uuid(), Identity: f.identity, RunnerBoot: assignment.Facts.RunnerBoot, DaemonBoot: status.DaemonBoot, Nonce: uuid(), SentMS: &sent}
	_, err = s.IssueLease(ctx, f.runner, session.SessionID, f.dispatch, p.FencedVersion, request)
	var refusal *authority.Refusal
	if !errors.As(err, &refusal) || refusal.Code != "session_stale" {
		t.Fatal("pre-restore session not fenced", err)
	}
	// A retired session is refused before generation checks. Reconnect through
	// the real API so the old dispatch/request reaches the generation fence.
	hello.MessageID = uuid()
	fresh, err := s.RunnerSession(ctx, f.runner, hello)
	must(t, err)
	if fresh.Mode != "normal" || fresh.SessionID == session.SessionID || fresh.Generation == status.Generation || fresh.DaemonBoot == status.DaemonBoot {
		t.Fatal("restored session did not freshly bind enabled eligibility", fresh)
	}
	must(t, fresh.Binds(assignment))
	request.DaemonBoot = fresh.DaemonBoot
	_, err = s.IssueLease(ctx, f.runner, fresh.SessionID, f.dispatch, p.FencedVersion, request)
	if !errors.Is(err, p.StaleGeneration) {
		t.Fatal("old-generation lease issuance not fenced", err)
	}
	// Checkpoint before immutable inspection, otherwise the no-lease assertion
	// could miss a row committed only to the live store's WAL.
	must(t, s.Close())
	db, err := openDatabase(filepath.Join(state, "state.db"), true)
	must(t, err)
	defer db.Close()
	var reason string
	must(t, db.QueryRow("SELECT reason FROM control_fenced WHERE message_id=?", request.MessageID).Scan(&reason))
	if reason != string(p.StaleGeneration) {
		t.Fatal("generation fence not durable", reason)
	}
	var count int
	must(t, db.QueryRow("SELECT count(*) FROM control_leases").Scan(&count))
	if count != 0 {
		t.Fatal("stale lease was issued", count)
	}
}
