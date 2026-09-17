package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	p "github.com/korallis/letmecook/schemas/execution"
	"modernc.org/sqlite"
)

func grantFixture() g.Grant {
	hash := strings.Repeat("a", 64)
	rev := g.Revision{Number: 1, SHA256: hash}
	route := g.Route{RouteRef: "worker", ProfileRef: "default", RouteRevision: 1, Policy: rev, RouterBuild: hash, GraphDigest: hash, Evidence: rev, Harness: "fixture-harness", Protocol: "responses", SettingsDigest: hash, Isolation: "fixture-isolation", LimitsProfile: "strict-provider-output-v1", LimitsAuthority: "operator-strict", Targets: []g.Target{{Provider: "provider-a", Model: "model-a", Billing: "subscription"}, {Provider: "provider-b", Model: "model-b", Billing: "metered"}}}
	other := route
	other.RouteRef = "worker-backup"
	now := time.Now().UnixMilli()
	cost := int64(1000)
	return g.Grant{ID: newID(), TaskID: newID(), Revision: 1, Actor: "operator", Envelope: g.Envelope{
		Repository: "public-fixture", BaseCommit: strings.Repeat("b", 40), Brief: rev, Plan: rev, RouteDecision: rev,
		CriterionIDs: []string{"c1", "c2"}, TaskKinds: []string{"code"}, Paths: []string{"a.txt", "src/main.go"}, Operations: []string{"read", "verify", "write"}, Systems: []string{"fixture-search"}, Runners: []string{newID()}, Routes: []g.Route{route, other}, Selection: "within-envelope",
		Budgets: g.Budgets{Requests: 5, Attempts: 3, Subattempts: 10, Retries: 2, Concurrency: 1, RequestBytes: 1024, ResponseBytes: 4096, TotalMS: 60000, AttemptMS: 30000, FirstOutputMS: 5000, IdleMS: 5000, ProviderOutputTokens: 128, ProviderCostMicros: &cost}, NotBeforeMS: now - 1000, ExpiresMS: now + 3600000,
	}}
}

func cloneGrant(t *testing.T, grant g.Grant) g.Grant {
	t.Helper()
	b, err := json.Marshal(grant)
	if err != nil {
		t.Fatal(err)
	}
	var out g.Grant
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func requestFor(t *testing.T, grant g.Grant) g.Request {
	grant = cloneGrant(t, grant)
	grant.Envelope.Routes = grant.Envelope.Routes[:1]
	return g.Request{GrantID: grant.ID, TaskID: grant.TaskID, GrantRevision: grant.Revision, Action: "execute", Envelope: grant.Envelope}
}
func requireReason(t *testing.T, err error, code string) {
	t.Helper()
	var refusal *g.Refusal
	if !errors.As(err, &refusal) || refusal.Code != code || refusal.Field == "" {
		t.Fatalf("want %s refusal, got %v", code, err)
	}
}
func approve(t *testing.T, s *Store, old string, grant g.Grant) g.Grant {
	t.Helper()
	got, err := s.ApproveExecution(ctx, old, grant)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func invalidations(t *testing.T, s *Store) []Invalidation {
	t.Helper()
	got, err := s.ExecutionInvalidations(ctx, 0, 128)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestExecutionGrantBindings(t *testing.T) {
	s, _ := persistent(t)
	grant := approve(t, s, "", grantFixture())
	if err := s.CheckExecution(ctx, requestFor(t, grant)); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, reason string
		change       func(*g.Request)
	}{
		{"repository", "material_change", func(r *g.Request) { r.Envelope.Repository = "private" }},
		{"base", "material_change", func(r *g.Request) { r.Envelope.BaseCommit = strings.Repeat("c", 40) }},
		{"brief hash", "stale_revision", func(r *g.Request) { r.Envelope.Brief.SHA256 = strings.Repeat("c", 64) }},
		{"brief revision", "stale_revision", func(r *g.Request) { r.Envelope.Brief.Number++ }},
		{"plan", "stale_revision", func(r *g.Request) { r.Envelope.Plan.Number++ }},
		{"decision", "stale_revision", func(r *g.Request) { r.Envelope.RouteDecision.Number++ }},
		{"grant revision", "stale_revision", func(r *g.Request) { r.GrantRevision++ }},
		{"grant id", "unknown_grant", func(r *g.Request) { r.GrantID = newID() }},
		{"task id", "unknown_grant", func(r *g.Request) { r.TaskID = newID() }},
		{"criteria", "widened_scope", func(r *g.Request) { r.Envelope.CriterionIDs = []string{"unapproved"} }},
		{"task kind", "widened_scope", func(r *g.Request) { r.Envelope.TaskKinds = []string{"research"} }},
		{"paths", "widened_scope", func(r *g.Request) { r.Envelope.Paths = []string{"private.txt"} }},
		{"systems", "widened_scope", func(r *g.Request) { r.Envelope.Systems = []string{"internet"} }},
		{"runner", "widened_scope", func(r *g.Request) { r.Envelope.Runners = []string{newID()} }},
		{"expiry", "widened_scope", func(r *g.Request) { r.Envelope.ExpiresMS++ }},
		{"start", "widened_scope", func(r *g.Request) { r.Envelope.NotBeforeMS-- }},
		{"request count", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.Requests++ }},
		{"attempt count", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.Attempts++ }},
		{"subattempt count", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.Subattempts++ }},
		{"retry count", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.Retries++ }},
		{"concurrency", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.Concurrency++ }},
		{"request bytes", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.RequestBytes++ }},
		{"response bytes", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.ResponseBytes++ }},
		{"total time", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.TotalMS++ }},
		{"attempt time", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.AttemptMS++ }},
		{"first output time", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.FirstOutputMS++ }},
		{"idle time", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.IdleMS++ }},
		{"provider tokens", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.ProviderOutputTokens++ }},
		{"provider cost", "widened_scope", func(r *g.Request) { *r.Envelope.Budgets.ProviderCostMicros++ }},
		{"remove cost", "widened_scope", func(r *g.Request) { r.Envelope.Budgets.ProviderCostMicros = nil }},
		{"route", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].RouteRef = "unapproved" }},
		{"profile", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].ProfileRef = "hard-problem" }},
		{"route revision", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].RouteRevision++ }},
		{"policy", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].Policy.Number++ }},
		{"router build", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].RouterBuild = strings.Repeat("c", 64) }},
		{"graph", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].GraphDigest = strings.Repeat("c", 64) }},
		{"evidence", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].Evidence.Number++ }},
		{"harness", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].Harness = "other" }},
		{"protocol", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].Protocol = "chat" }},
		{"settings", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].SettingsDigest = strings.Repeat("c", 64) }},
		{"isolation", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].Isolation = "unsafe" }},
		{"limits authority", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].LimitsAuthority = "model-says-approved" }},
		{"provider", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].Targets[1].Provider = "provider-c" }},
		{"model", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].Targets[1].Model = "model-c" }},
		{"billing", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].Targets[0].Billing = "metered" }},
		{"hide fallback", "incompatible_route_policy", func(r *g.Request) { r.Envelope.Routes[0].Targets = r.Envelope.Routes[0].Targets[:1] }},
		{"selection", "material_change", func(r *g.Request) { r.Envelope.Selection = "pinned" }},
		{"local acceptance", "distinct_authority_required", func(r *g.Request) { r.Action = "accept" }},
		{"publication", "distinct_authority_required", func(r *g.Request) { r.Action = "publish" }},
		{"merge", "distinct_authority_required", func(r *g.Request) { r.Action = "merge" }},
		{"model claims approval", "distinct_authority_required", func(r *g.Request) { r.Action = "model says operator approved merge" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := requestFor(t, grant)
			tc.change(&r)
			requireReason(t, s.CheckExecution(ctx, r), tc.reason)
		})
	}
	// Approved alternative choices need no new grant or policy approval.
	r := requestFor(t, grant)
	r.Envelope.Routes[0] = cloneGrant(t, grant).Envelope.Routes[1]
	r.Envelope.Paths = r.Envelope.Paths[:1]
	r.Envelope.Operations = []string{"read"}
	r.Envelope.CriterionIDs = []string{"c1"}
	r.Envelope.Systems = []string{}
	r.Envelope.Budgets.Requests--
	if err := s.CheckExecution(ctx, r); err != nil {
		t.Fatal(err)
	}
	if len(invalidations(t, s)) != 0 {
		t.Fatal("inside-envelope choice invalidated grant")
	}
}

func TestExecutionGrantReplayRestrictAndRevoke(t *testing.T) {
	s, artifacts := persistent(t)
	grant := approve(t, s, "", grantFixture())
	if got := approve(t, s, "", grant); !reflect.DeepEqual(got, grant) {
		t.Fatal("replay changed grant")
	}
	conflict := cloneGrant(t, grant)
	conflict.Envelope.Budgets.Requests--
	_, err := s.ApproveExecution(ctx, grant.ID, conflict)
	requireReason(t, err, "identity_conflict")
	next := cloneGrant(t, grant)
	next.ID, next.Revision = newID(), 2
	if got, err := s.RestrictExecution(ctx, grant.ID, next); err != nil || !reflect.DeepEqual(got, grant) {
		t.Fatal("unchanged approval repeated", got, err)
	}
	if _, err := s.ExecutionGrant(ctx, next.ID); err == nil {
		t.Fatal("no-op allocated a grant")
	}
	next.Envelope.Paths = next.Envelope.Paths[:1]
	next.Envelope.Budgets.Requests--
	got, err := s.RestrictExecution(ctx, grant.ID, next)
	if err != nil || !reflect.DeepEqual(got, next) {
		t.Fatal(got, err)
	}
	requireReason(t, s.CheckExecution(ctx, requestFor(t, grant)), "superseded")
	_, err = s.ApproveExecution(ctx, "", grant)
	requireReason(t, err, "superseded")
	old, err := s.ExecutionGrant(ctx, grant.ID)
	if err != nil || !reflect.DeepEqual(old, grant) {
		t.Fatal("immutable record lost", err)
	}
	events := invalidations(t, s)
	if len(events) != 1 || events[0].GrantID != grant.ID || events[0].Reason != "superseded" {
		t.Fatal(events)
	}
	if err := s.InvalidateExecution(ctx, next.TaskID, next.ID, "operator", "revoked"); err != nil {
		t.Fatal(err)
	}
	if err := s.InvalidateExecution(ctx, next.TaskID, next.ID, "operator", "revoked"); err != nil {
		t.Fatal("replay", err)
	}
	requireReason(t, s.InvalidateExecution(ctx, next.TaskID, next.ID, "model", "revoked"), "identity_conflict")
	requireReason(t, s.CheckExecution(ctx, requestFor(t, next)), "revoked")
	if _, err := s.RestrictExecution(ctx, next.ID, next); err == nil {
		t.Fatal("revoked grant restored")
	}
	before := invalidations(t, s)
	if len(before) != 2 {
		t.Fatal(before)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	requireReason(t, r.CheckExecution(ctx, requestFor(t, next)), "revoked")
	if after := invalidations(t, r); !reflect.DeepEqual(before, after) {
		t.Fatal("invalidation replay lost", after)
	}
	page, err := r.ExecutionInvalidations(ctx, before[0].Sequence, 1)
	if err != nil || len(page) != 1 || page[0] != before[1] {
		t.Fatal(page, err)
	}
	for _, query := range []string{"UPDATE execution_grants SET expires_ms=1", "DELETE FROM execution_grants", "UPDATE execution_invalidations SET reason='expired'", "DELETE FROM execution_invalidations", "UPDATE execution_grant_heads SET grant_id='missing'"} {
		if _, err := r.db.Exec(query); err == nil {
			t.Fatal("constraint missing", query)
		}
	}
}

func TestExecutionGrantMaterialAndConcurrentRevisions(t *testing.T) {
	s, _ := persistent(t)
	grant := approve(t, s, "", grantFixture())
	proposal := cloneGrant(t, grant)
	proposal.ID, proposal.Revision = newID(), 2
	proposal.Envelope.Brief.Number++
	proposal.Envelope.Brief.SHA256 = strings.Repeat("c", 64)
	_, err := s.RestrictExecution(ctx, grant.ID, proposal)
	requireReason(t, err, "stale_revision")
	proposal.Envelope = cloneGrant(t, grant).Envelope
	proposal.Envelope.Paths = append(proposal.Envelope.Paths, "z-private.txt")
	_, err = s.RestrictExecution(ctx, grant.ID, proposal)
	requireReason(t, err, "widened_scope")
	// Material operator approval and narrowing race for the same immutable head.
	left, right := cloneGrant(t, proposal), cloneGrant(t, grant)
	right.ID, right.Revision = newID(), 2
	right.Envelope.Paths = right.Envelope.Paths[:1]
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, candidate := range []g.Grant{left, right} {
		wg.Add(1)
		go func(v g.Grant) {
			defer wg.Done()
			<-start
			_, err := s.ApproveExecution(ctx, grant.ID, v)
			results <- err
		}(candidate)
	}
	close(start)
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else {
			requireReason(t, err, "revision_conflict")
			conflicts++
		}
	}
	if wins != 1 || conflicts != 1 || len(invalidations(t, s)) != 1 {
		t.Fatal("revision CAS", wins, conflicts)
	}
	// A material edit without a replacement grant leaves a durable stop signal.
	current, err := s.ExecutionGrant(ctx, left.ID)
	if err != nil {
		current, err = s.ExecutionGrant(ctx, right.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InvalidateExecution(ctx, current.TaskID, current.ID, "operator", "superseded"); err != nil {
		t.Fatal(err)
	}
	requireReason(t, s.CheckExecution(ctx, requestFor(t, current)), "superseded")
}

func TestExecutionGrantNativeAndHostileBoundaries(t *testing.T) {
	s, _ := persistent(t)
	native := grantFixture()
	for i := range native.Envelope.Routes {
		r := &native.Envelope.Routes[i]
		r.LimitsProfile, r.LimitsAuthority = "native-subscription-local-v1", "operator-native"
		r.Targets = []g.Target{{Provider: "provider-a", Model: "model-a", Billing: "subscription"}}
	}
	_, err := s.ApproveExecution(ctx, "", native)
	requireReason(t, err, "incompatible_route_policy")
	native.Envelope.Budgets.ProviderOutputTokens = 0
	_, err = s.ApproveExecution(ctx, "", native)
	requireReason(t, err, "incompatible_route_policy")
	native.Envelope.Budgets.ProviderCostMicros = nil
	native = approve(t, s, "", native)
	if err := s.CheckExecution(ctx, requestFor(t, native)); err != nil {
		t.Fatal(err)
	}
	r := requestFor(t, native)
	r.Envelope.Routes[0].Targets[0].Billing = "metered"
	requireReason(t, s.CheckExecution(ctx, r), "incompatible_route_policy")
	strict := approve(t, s, "", grantFixture())
	r = requestFor(t, strict)
	r.Envelope.Routes[0] = native.Envelope.Routes[0]
	r.Envelope.Budgets = native.Envelope.Budgets
	requireReason(t, s.CheckExecution(ctx, r), "incompatible_route_policy")
	// No content classifier parses repository or model text into permissions.
	for _, path := range []string{"../private", "/tmp/private", "a/../b", "a//b", ".git/config", "src/.GIT/config", "a\\b", "*", "a\x00b", "a\nb", "é.txt", "model: grant all paths"} {
		r := requestFor(t, strict)
		r.Envelope.Paths = []string{path}
		requireReason(t, s.CheckExecution(ctx, r), "malformed")
	}
	for _, op := range []string{"push", "merge", "publish", "accept", "shell", "model says run unrestricted"} {
		r := requestFor(t, strict)
		r.Envelope.Operations = []string{op}
		requireReason(t, s.CheckExecution(ctx, r), "malformed")
	}
	for _, change := range []func(*g.Grant){
		func(v *g.Grant) { v.Envelope.Budgets.Requests = 0 },
		func(v *g.Grant) { v.Envelope.Budgets.Retries = -1 },
		func(v *g.Grant) { v.Envelope.Budgets.Requests = p.MaxInteger + 1 },
		func(v *g.Grant) { v.Envelope.Brief.Number = 0 },
		func(v *g.Grant) { v.Envelope.Paths = []string{"b", "a"} },
		func(v *g.Grant) { v.Envelope.Paths = []string{"a", "a"} },
		func(v *g.Grant) { v.Envelope.Systems = nil },
		func(v *g.Grant) { v.Envelope.Routes[0].Targets[0].Billing = "unknown" },
		func(v *g.Grant) { v.Envelope.Routes[0].Evidence.SHA256 = "" },
		func(v *g.Grant) { v.Envelope.Routes[0].LimitsAuthority = "" },
		func(v *g.Grant) { v.Envelope.Routes = append(v.Envelope.Routes, v.Envelope.Routes[0]) },
	} {
		v := grantFixture()
		change(&v)
		if _, err := s.ApproveExecution(ctx, "", v); err == nil {
			t.Fatal("malformed grant accepted")
		}
	}
	v := grantFixture()
	v.Envelope.Paths = []string{}
	for i := 0; i < 128; i++ {
		v.Envelope.Paths = append(v.Envelope.Paths, fmt.Sprintf("p%03d/", i)+strings.Repeat("x", 500))
	}
	_, err = s.ApproveExecution(ctx, "", v)
	requireReason(t, err, "oversized")
	fixture := owned(t)
	_, err = fixture.ApproveExecution(ctx, "", grantFixture())
	requireReason(t, err, "fixture_only")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	requireReason(t, s.CheckExecution(ctx, requestFor(t, strict)), "store_closed")
}

func TestExecutionGrantExpiryAndFailureAtomicity(t *testing.T) {
	s, artifacts := persistent(t)
	grant := approve(t, s, "", grantFixture())
	next := cloneGrant(t, grant)
	next.ID, next.Revision = newID(), 2
	next.Envelope.Budgets.Requests--
	// Real SQLite capacity failure after old invalidation/new grant writes; every
	// write must roll back, retaining the only authoritative revision.
	sqlExec(t, s, "CREATE TABLE pressure(payload BLOB) STRICT")
	sqlExec(t, s, "CREATE TRIGGER grant_full BEFORE UPDATE ON execution_grant_heads BEGIN INSERT INTO pressure VALUES(zeroblob(1048576)); END")
	var pages int
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA max_page_count=" + strconv.Itoa(pages)); err != nil {
		t.Fatal(err)
	}
	_, err := s.RestrictExecution(ctx, grant.ID, next)
	var full *sqlite.Error
	if !errors.As(err, &full) || full.Code() != 13 {
		t.Fatal("expected SQLITE_FULL", err)
	}
	if len(invalidations(t, s)) != 0 {
		t.Fatal("partial invalidation")
	}
	if _, err := s.ExecutionGrant(ctx, next.ID); err == nil {
		t.Fatal("partial grant")
	}
	if err := s.CheckExecution(ctx, requestFor(t, grant)); err != nil {
		t.Fatal("old authority lost", err)
	}
	sqlExec(t, s, "DROP TRIGGER grant_full")
	sqlExec(t, s, "PRAGMA max_page_count=1073741823")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.RestrictExecution(cancelled, grant.ID, next); err == nil {
		t.Fatal("cancelled revision committed")
	}
	sqlExec(t, s, "CREATE TRIGGER grant_revoke_failure BEFORE INSERT ON execution_invalidations BEGIN SELECT RAISE(ABORT,'interrupted'); END")
	if err := s.InvalidateExecution(ctx, grant.TaskID, grant.ID, "operator", "revoked"); err == nil {
		t.Fatal("failed revocation acknowledged")
	}
	sqlExec(t, s, "DROP TRIGGER grant_revoke_failure")
	if err := s.CheckExecution(ctx, requestFor(t, grant)); err != nil {
		t.Fatal(err)
	}
	// Seed exact expired immutable records through the real schema. No sleeps or
	// production clock override; test g.Check at the exact inclusive cutoff too.
	expired := grantFixture()
	expired.Envelope.ExpiresMS = time.Now().UnixMilli() - 10
	expired.Envelope.NotBeforeMS = expired.Envelope.ExpiresMS - 1000
	body, _ := json.Marshal(expired)
	if _, err := s.db.Exec("INSERT INTO execution_grants VALUES(?,?,?,?,?)", expired.ID, expired.TaskID, expired.Revision, string(body), expired.Envelope.ExpiresMS); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO execution_grant_heads VALUES(?,?)", expired.TaskID, expired.ID); err != nil {
		t.Fatal(err)
	}
	requireReason(t, g.Check(expired, requestFor(t, expired), expired.Envelope.ExpiresMS), "expired")
	if err := g.Check(expired, requestFor(t, expired), expired.Envelope.ExpiresMS-1); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	events := invalidations(t, r)
	if len(events) != 1 || events[0].Reason != "expired" || events[0].GrantID != expired.ID {
		t.Fatal(events)
	}
	requireReason(t, r.CheckExecution(ctx, requestFor(t, expired)), "expired")
	if after := invalidations(t, r); !reflect.DeepEqual(after, events) {
		t.Fatal("expiry not sticky")
	}
}

func TestExecutionGrantSchemaThreeMigration(t *testing.T) {
	s, artifacts := persistent(t)
	before := snapshot(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(s.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(dropDispatchSchema + "DROP TABLE repository_profiles; DROP TABLE repositories; DROP TABLE execution_grant_heads; DROP TABLE execution_invalidations; DROP TABLE execution_grants; PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	after := snapshot(t, r)
	if after.SchemaVersion != 8 || after.Generation != before.Generation || len(after.Events) != 0 {
		t.Fatal(after)
	}
	grant := approve(t, r, "", grantFixture())
	if err := r.CheckExecution(ctx, requestFor(t, grant)); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionGrantProcessCrash(t *testing.T) {
	for _, mode := range []string{"grant-crash", "grant-commit"} {
		t.Run(mode, func(t *testing.T) {
			s, artifacts := persistent(t)
			grant := approve(t, s, "", grantFixture())
			t.Setenv("GAFFER_OWNED_TEST_ARTIFACTS", artifacts)
			t.Setenv("GAFFER_OWNED_TEST_GRANT", grant.ID)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			want := "interrupted"
			if mode == "grant-commit" {
				want = "committed"
			}
			child(t, s.dir, mode, want, true)
			r, err := Open(ctx, s.dir, artifacts)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			events := invalidations(t, r)
			if mode == "grant-crash" {
				if len(events) != 0 {
					t.Fatal("uncommitted invalidation survived", events)
				}
				if err := r.CheckExecution(ctx, requestFor(t, grant)); err != nil {
					t.Fatal(err)
				}
			} else {
				if len(events) != 1 || events[0].GrantID != grant.ID {
					t.Fatal("committed invalidation lost", events)
				}
				requireReason(t, r.CheckExecution(ctx, requestFor(t, grant)), "superseded")
			}
			var count int
			if err := r.db.QueryRow("SELECT count(*) FROM execution_grants").Scan(&count); err != nil {
				t.Fatal(err)
			}
			wantCount := 1
			if mode == "grant-commit" {
				wantCount = 2
			}
			if count != wantCount {
				t.Fatal("partial grant transaction", count)
			}
		})
	}
}

func TestExecutionGrantReintroducedRouteHistory(t *testing.T) {
	s, artifacts := persistent(t)
	grant := grantFixture()
	grant.Envelope.Routes[0].RouteRevision = 2
	grant.Envelope.Routes[0].Policy.Number = 2
	grant.Envelope.Routes[0].Evidence.Number = 2
	grant = approve(t, s, "", grant)
	narrowed := cloneGrant(t, grant)
	narrowed.ID, narrowed.Revision = newID(), 2
	narrowed.Envelope.Routes = narrowed.Envelope.Routes[1:]
	narrowed, err := s.RestrictExecution(ctx, grant.ID, narrowed)
	if err != nil {
		t.Fatal(err)
	}
	before := invalidations(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	third := cloneGrant(t, grant)
	third.ID, third.Revision = newID(), 3
	third.Envelope.RouteDecision = g.Revision{Number: 2, SHA256: strings.Repeat("c", 64)}
	for _, tc := range []struct {
		name   string
		change func(*g.Route)
	}{
		{"route rollback", func(v *g.Route) { v.RouteRevision = 1 }},
		{"same revision changed content", func(v *g.Route) { v.SettingsDigest = strings.Repeat("d", 64) }},
		{"policy rollback", func(v *g.Route) { v.RouteRevision++; v.Policy.Number-- }},
		{"policy hash conflict", func(v *g.Route) { v.RouteRevision++; v.Policy.SHA256 = strings.Repeat("d", 64) }},
		{"evidence rollback", func(v *g.Route) { v.RouteRevision++; v.Evidence.Number-- }},
		{"evidence hash conflict", func(v *g.Route) { v.RouteRevision++; v.Evidence.SHA256 = strings.Repeat("d", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := cloneGrant(t, third)
			candidate.ID = newID()
			tc.change(&candidate.Envelope.Routes[0])
			_, err := r.ApproveExecution(ctx, narrowed.ID, candidate)
			requireReason(t, err, "stale_revision")
			_, err = r.ExecutionGrant(ctx, candidate.ID)
			requireReason(t, err, "unknown_grant")
			requireReason(t, r.CheckExecution(ctx, requestFor(t, candidate)), "unknown_grant")
			if err := r.CheckExecution(ctx, requestFor(t, narrowed)); err != nil {
				t.Fatal("rejection lost current authority", err)
			}
			if after := invalidations(t, r); !reflect.DeepEqual(before, after) {
				t.Fatal("rejection changed invalidations", after)
			}
		})
	}
	third = approve(t, r, narrowed.ID, third)
	if err := r.CheckExecution(ctx, requestFor(t, third)); err != nil {
		t.Fatal("unchanged retained route refused", err)
	}
	fourth := cloneGrant(t, third)
	fourth.ID, fourth.Revision = newID(), 4
	fourth.Envelope.RouteDecision = g.Revision{Number: 3, SHA256: strings.Repeat("d", 64)}
	fourth.Envelope.Routes[0].RouteRevision++
	fourth.Envelope.Routes[0].Policy = fourth.Envelope.RouteDecision
	fourth.Envelope.Routes[0].Evidence = fourth.Envelope.RouteDecision
	fourth = approve(t, r, third.ID, fourth)
	if err := r.CheckExecution(ctx, requestFor(t, fourth)); err != nil {
		t.Fatal("fresh route revisions refused", err)
	}
}

func TestExecutionGrantRevisionContinuityAndValidity(t *testing.T) {
	s, _ := persistent(t)
	grant := grantFixture()
	grant.Envelope.NotBeforeMS += 60000
	grant = approve(t, s, "", grant)
	requireReason(t, s.CheckExecution(ctx, requestFor(t, grant)), "not_yet_valid")
	if got := approve(t, s, "", grant); !reflect.DeepEqual(got, grant) {
		t.Fatal("scheduled replay changed")
	}
	next := cloneGrant(t, grant)
	next.ID, next.Revision = newID(), 2
	next.Envelope.NotBeforeMS -= 60000
	next.Envelope.Brief.SHA256 = strings.Repeat("c", 64)
	_, err := s.ApproveExecution(ctx, grant.ID, next)
	requireReason(t, err, "stale_revision")
	next.Envelope.Brief.Number++
	next = approve(t, s, grant.ID, next)
	if err := s.CheckExecution(ctx, requestFor(t, next)); err != nil {
		t.Fatal(err)
	}
	third := cloneGrant(t, next)
	third.ID, third.Revision = newID(), 3
	third.Envelope.Brief = grant.Envelope.Brief
	_, err = s.ApproveExecution(ctx, next.ID, third)
	requireReason(t, err, "stale_revision")
	third.Envelope.Brief = next.Envelope.Brief
	third.Envelope.Routes[0].SettingsDigest = strings.Repeat("d", 64)
	_, err = s.ApproveExecution(ctx, next.ID, third)
	requireReason(t, err, "stale_revision")
	third.Envelope.Routes[0].RouteRevision++
	_, err = s.ApproveExecution(ctx, next.ID, third)
	requireReason(t, err, "stale_revision")
	third.Envelope.RouteDecision.Number++
	third.Envelope.RouteDecision.SHA256 = strings.Repeat("e", 64)
	third = approve(t, s, next.ID, third)
	r := requestFor(t, third)
	r.Envelope.ExpiresMS = time.Now().UnixMilli() - 1
	r.Envelope.NotBeforeMS = r.Envelope.ExpiresMS - 100
	requireReason(t, s.CheckExecution(ctx, r), "outside_validity")
}
