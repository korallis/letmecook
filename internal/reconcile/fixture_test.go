package reconcile

// Test fixture built only from the store's exported API: a persistent SQLite
// store with a bootstrapped owner, an enrolled and enabled runner, a registered
// file:// repository, published eligibility and, per task, an approved grant
// bound to a route decision. Every attempt state the tests classify is reached
// through the same store methods the daemon's routes call; nothing is inserted
// behind the store's back.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	r "github.com/korallis/letmecook/internal/repositories"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
	_ "modernc.org/sqlite"
)

var ctx = context.Background()

const manifestVersion = "gaffer-artifact-manifest-v1"

func uuid() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type fixture struct {
	s              *store.Store
	dir, artifacts string
	owner, runner  string
	runnerID       string
	profile        r.Profile
	selection      r.Selection
	facts          sc.Eligibility
	generation     string
	boot           string
	offset         time.Duration
	nowMS          int64 // one validity base for every grant, so scope never widens
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

// newFixture opens a fresh persistent store and seeds identity, repository and
// eligibility through the owner API.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{dir: filepath.Join(root, "state"), artifacts: filepath.Join(root, "artifacts"), owner: strings.Repeat("a", 64), runner: strings.Repeat("b", 64), nowMS: time.Now().UnixMilli()}
	s, err := store.Open(ctx, f.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	t.Cleanup(func() { f.s.Close() })
	if err := s.BootstrapOwner(ctx, f.owner, false); err != nil {
		t.Fatal(err)
	}
	invite, err := s.CreateEnrollment(ctx, f.owner, uuid(), f.runner)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := s.Enroll(ctx, f.runner, invite.Token)
	if err != nil {
		t.Fatal(err)
	}
	f.runnerID = identity.ID
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(base, "remote.git")
	gitRun(t, base, "init", "--quiet", "--template=", "--initial-branch=main", remote)
	if err := os.WriteFile(filepath.Join(remote, "file"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, remote, "add", ".")
	gitRun(t, remote, "commit", "--quiet", "-m", "base")
	commit := gitRun(t, remote, "rev-parse", "HEAD")
	u := url.URL{Scheme: "file", Path: remote}
	runnerRoot := filepath.Join(base, "runner")
	if err := os.Mkdir(runnerRoot, 0700); err != nil {
		t.Fatal(err)
	}
	f.profile = r.Profile{Version: r.Version, ID: "fixture", Revision: 1, Remote: u.String(), Base: r.Base{Ref: "refs/heads/main", Commit: commit, Policy: "pinned"}, ProtectedPaths: []string{"policy"}, ContextScope: []string{"."}, Verification: r.Verification{Name: "fixture", Commands: []r.Command{{Argv: []string{"go", "test", "./..."}, Directory: ".", TimeoutMS: 1000}}}, RunnerRoots: []r.RunnerRoot{{RunnerID: identity.ID, Root: runnerRoot}}}
	if _, err := s.RegisterRepository(ctx, f.owner, 0, f.profile); err != nil {
		t.Fatal(err)
	}
	digest, err := f.profile.Digest()
	if err != nil {
		t.Fatal(err)
	}
	f.selection = r.Selection{Repository: f.profile.ID, Revision: f.profile.Revision, ProfileDigest: digest, Remote: f.profile.Remote, BaseCommit: f.profile.Base.Commit, RunnerRoot: f.profile.RunnerRoots[0]}
	if _, err := s.UpdateIdentity(ctx, f.owner, identity.ID, 1, "enable", ""); err != nil {
		t.Fatal(err)
	}
	grant := f.grant()
	rev := grant.Envelope.Brief
	f.facts = sc.Eligibility{ID: "fixture-pair", Revision: 1, Repository: f.selection, Enabled: true, RunnerBoot: uuid(), LocalPolicy: rev, LocalEnvelope: grant.Envelope, Config: rev, Route: grant.Envelope.Routes[0], Capabilities: []string{"tools", "vision"}, Isolation: sc.IsolationProfile{ID: "fixture-isolation", Revision: rev, RuntimeDigest: rev.SHA256, ObservedDigest: rev.SHA256, Kind: "dedicated-vm", Supported: true, Controls: sc.RequiredIsolationControls()}, Capacity: sc.Resources{CPU: 1000, MemoryBytes: 4096, DiskBytes: 8192, Processes: 8}, Concurrency: 1, RouterAuthenticated: true, Availability: "unknown", ValidUntilMS: grant.Envelope.ExpiresMS}
	for _, target := range f.facts.Route.Targets {
		f.facts.Paths = append(f.facts.Paths, sc.PathEvidence{Target: target, Compatible: true, Capabilities: []string{"tools", "vision"}, ContextTokens: 8192, LocalBounds: true, ProviderOutputBound: true, ProviderCostBound: true})
	}
	if err := s.PublishEligibility(ctx, f.owner, 0, f.facts); err != nil {
		t.Fatal(err)
	}
	f.refreshBoot(t)
	return f
}

func (f *fixture) refreshBoot(t *testing.T) {
	t.Helper()
	f.generation, f.boot = f.s.Boot()
}

// grant is the approved envelope shape every task uses; ExpiresMS is far enough
// out for a test and Attempts/Retries bound the retry tests.
func (f *fixture) grant() g.Grant {
	hash := strings.Repeat("a", 64)
	rev := g.Revision{Number: 1, SHA256: hash}
	route := g.Route{RouteRef: "worker", ProfileRef: "default", RouteRevision: 1, Policy: rev, RouterBuild: hash, GraphDigest: hash, Evidence: rev, Harness: "fixture-harness", Protocol: "responses", SettingsDigest: hash, Isolation: "fixture-isolation", LimitsProfile: "strict-provider-output-v1", LimitsAuthority: "operator-strict", Targets: []g.Target{{Provider: "provider-a", Model: "model-a", Billing: "subscription"}, {Provider: "provider-b", Model: "model-b", Billing: "metered"}}}
	now := f.nowMS
	cost := int64(1000)
	return g.Grant{ID: uuid(), TaskID: uuid(), Revision: 1, Actor: "operator", Envelope: g.Envelope{
		Repository: f.profile.ID, BaseCommit: f.profile.Base.Commit, Brief: rev, Plan: rev, RouteDecision: rev,
		CriterionIDs: []string{"c1", "c2"}, TaskKinds: []string{"code"}, Paths: []string{"a.txt", "src/main.go"}, Operations: []string{"read", "verify", "write"}, Systems: []string{"fixture-search"}, Runners: []string{f.selection.RunnerRoot.RunnerID}, Routes: []g.Route{route}, Selection: "pinned",
		Budgets: g.Budgets{Requests: 5, Attempts: 3, Subattempts: 10, Retries: 2, Concurrency: 1, RequestBytes: 1024, ResponseBytes: 4096, TotalMS: 3600000, AttemptMS: 30000, FirstOutputMS: 5000, IdleMS: 5000, ProviderOutputTokens: 128, ProviderCostMicros: &cost}, NotBeforeMS: now - 1000, ExpiresMS: now + 3600000,
	}}
}

// task is one approved task with its dispatch request; d and session are filled
// as the test drives it.
type task struct {
	f       *fixture
	grant   g.Grant
	request store.DispatchRequest
	d       store.Dispatch
	session store.SessionRecord
}

// newTask approves a grant whose route decision digest binds the fixture's
// eligibility, mirroring the owner API's approve step.
func (f *fixture) newTask(t *testing.T) *task {
	t.Helper()
	grant := f.grant()
	rev := grant.Envelope.Brief
	decision := sc.Decision{ID: uuid(), Revision: 1, Assessment: sc.Assessment{TaskID: grant.TaskID, Brief: rev, Plan: rev, ContextDigest: rev.SHA256, Role: "worker", WorkClass: "code", Ambiguity: "low", Consequence: "high", Required: []string{"tools", "vision"}, Preferences: []string{}, EvidenceRefs: []string{"operator-brief"}, Unknowns: []string{"subscription-headroom"}, ContextTokens: 4096, AssessorVersion: "operator-v1", SelectorVersion: "deterministic-v1"}, EligibilityID: f.facts.ID, Selected: f.facts.Route, RankedAlternatives: []string{}, Authorization: "operator", Reason: "pinned", Resources: sc.Resources{CPU: 500, MemoryBytes: 2048, DiskBytes: 4096, Processes: 4}}
	hash, err := sc.Digest(f.facts)
	if err != nil {
		t.Fatal(err)
	}
	decision.Eligibility = g.Revision{Number: f.facts.Revision, SHA256: hash}
	if hash, err = sc.Digest(decision); err != nil {
		t.Fatal(err)
	}
	grant.Envelope.RouteDecision = g.Revision{Number: decision.Revision, SHA256: hash}
	approved, err := f.s.ApproveExecution(ctx, "", grant)
	if err != nil {
		t.Fatal(err)
	}
	request := store.DispatchRequest{ID: uuid(), Request: g.Request{GrantID: approved.ID, TaskID: approved.TaskID, GrantRevision: approved.Revision, Action: "execute", Envelope: approved.Envelope}, Decision: decision, Allowance: approved.Envelope.Budgets}
	b := &request.Allowance
	b.Attempts, b.Retries, b.Concurrency, b.TotalMS = 1, 0, 1, 10000
	b.Requests, b.Subattempts, b.AttemptMS, b.ProviderOutputTokens = 1, 2, 10000, 32
	cost := int64(100)
	b.ProviderCostMicros = &cost
	return &task{f: f, grant: approved, request: request}
}

func (k *task) id() p.Identity { return k.d.Assignment.Identity }

func (k *task) dispatch(t *testing.T) store.Dispatch {
	t.Helper()
	d, err := k.f.s.Dispatch(ctx, k.request)
	if err != nil {
		t.Fatal(err)
	}
	k.d = d
	return d
}

// redispatch admits the next epoch with a fresh intent key (as PlanRetry does).
func (k *task) redispatch(t *testing.T) store.Dispatch {
	t.Helper()
	k.request.ID = uuid()
	return k.dispatch(t)
}

func (f *fixture) helloRecord() store.HelloRecord {
	return store.HelloRecord{Version: execwire.Version, MessageID: uuid(), RunnerBoot: f.facts.RunnerBoot, EligibilityID: f.facts.ID, EligibilityRevision: f.facts.Revision, PolicyDigest: strings.Repeat("a", 64), Journals: []json.RawMessage{}}
}

// hello opens a normal-mode session for the fixture runner under the current boot.
func (k *task) hello(t *testing.T) store.SessionRecord {
	t.Helper()
	sess, err := k.f.s.RunnerSession(ctx, k.f.runner, k.f.helloRecord())
	if err != nil || sess.Mode != "normal" {
		t.Fatal(sess, err)
	}
	k.session = sess
	return sess
}

func (k *task) accept(t *testing.T) {
	t.Helper()
	ack := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: uuid(), Identity: k.id(), AssignmentID: k.d.Assignment.AssignmentID, RunnerBoot: k.f.facts.RunnerBoot, DaemonBoot: k.f.boot}
	if err := k.f.s.AcknowledgeAssignment(ctx, k.f.runner, k.d.ID, ack); err != nil {
		t.Fatal(err)
	}
	k.d.Acknowledged = true
}

func (k *task) lease(t *testing.T) c.Lease {
	t.Helper()
	sent := time.Now().UnixMilli()
	request := p.Message{Version: p.FencedVersion, Kind: "lease_request", MessageID: uuid(), Identity: k.id(), Nonce: uuid(), RunnerBoot: k.f.facts.RunnerBoot, DaemonBoot: k.f.boot, SentMS: &sent}
	lease, err := k.f.s.IssueLease(ctx, k.f.runner, k.session.SessionID, k.d.ID, p.FencedVersion, request)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func (k *task) state(t *testing.T) (p.AttemptState, int64) {
	t.Helper()
	in, err := k.f.s.ReconciliationInputs(ctx, k.id().AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	return in.State, in.Revision
}

func (k *task) propose(t *testing.T, to p.AttemptState, evidence store.RuntimeEvidence) p.Message {
	t.Helper()
	state, revision := k.state(t)
	m := p.Message{Version: p.FencedVersion, Kind: "transition", MessageID: uuid(), Identity: k.id(), ExpectedRevision: &revision, From: state, To: to}
	got, err := k.f.s.ProposeTransition(ctx, k.f.runner, k.session.SessionID, p.FencedVersion, m, evidence)
	if err != nil {
		t.Fatalf("propose %s: %v", to, err)
	}
	return got
}

func launchIntent(nonce string) store.RuntimeEvidence {
	return store.RuntimeEvidence{Kind: "launch_intent", Workspace: "/private/tmp/gaffer-ws", BoundaryPort: 40001, Nonce: nonce}
}
func launched() store.RuntimeEvidence {
	return store.RuntimeEvidence{Kind: "launched", GuardianPID: 100, PID: 101, PGID: 101, StartUnixNS: 1700000000000000001}
}
func exited(through int64) store.RuntimeEvidence {
	return store.RuntimeEvidence{Kind: "exit", Code: 0, PGID: 101, PGIDEmpty: true, ObservedUnixNS: 1700000000000000002, StreamThrough: through}
}

// start drives dispatch -> hello -> accept -> lease -> starting -> running.
func (k *task) start(t *testing.T) c.Lease {
	t.Helper()
	k.dispatch(t)
	k.hello(t)
	k.accept(t)
	lease := k.lease(t)
	k.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	k.propose(t, p.Running, launched())
	return lease
}

// run continues start through result_pending with an empty stream.
func (k *task) run(t *testing.T) c.Lease {
	t.Helper()
	lease := k.start(t)
	k.propose(t, p.ResultPending, exited(0))
	return lease
}

// fakeBrief seeds the task's brief naming the fake harness through a second
// connection while the store is open: CreateTask is still a stub on this base,
// and ProposeTransition admits a launch intent with boundary_port 0 only for
// that harness. Only the brief row is written; every attempt edge still goes
// through the store.
func (k *task) fakeBrief(t *testing.T) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(k.f.dir, "state.db"), RawQuery: "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var owner string
	if err := db.QueryRow("SELECT id FROM principals WHERE role='owner'").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO task_briefs VALUES(?,1,?,?,?,?,?,?,?,?,?,?,?,?)", k.id().TaskID, k.f.profile.ID, k.d.Request.Envelope.BaseCommit, "brief", "[]", "[]", "[]", "fake", "{}", strings.Repeat("a", 64), strings.Repeat("a", 64), owner, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

// startFake is start for a fake-harness task: the launch intent names no
// boundary port because no inference boundary is ever created.
func (k *task) startFake(t *testing.T) c.Lease {
	t.Helper()
	k.dispatch(t)
	k.hello(t)
	k.accept(t)
	k.fakeBrief(t)
	lease := k.lease(t)
	intent := launchIntent(lease.Request.Nonce)
	intent.BoundaryPort = 0
	k.propose(t, p.Starting, intent)
	k.propose(t, p.Running, launched())
	return lease
}

// runFake continues startFake through result_pending with an empty stream.
func (k *task) runFake(t *testing.T) c.Lease {
	t.Helper()
	lease := k.startFake(t)
	k.propose(t, p.ResultPending, exited(0))
	return lease
}

func (k *task) cancel(t *testing.T) c.Request {
	t.Helper()
	stop := c.Request{ID: uuid(), Kind: c.CancelAttempt, TaskID: k.id().TaskID, AttemptID: k.id().AttemptID, Cause: "operator"}
	if _, err := k.f.s.RequestStop(ctx, k.f.owner, stop); err != nil {
		t.Fatal(err)
	}
	return stop
}

// terminated builds supervisor termination evidence under the fixture's current
// boot for stopID.
func (k *task) terminated(stopID, confirmed, remote string) c.Evidence {
	m := p.Message{Version: p.FencedVersion, Kind: "terminated", MessageID: uuid(), Identity: k.id(), StopID: stopID, RunnerBoot: k.f.facts.RunnerBoot, DaemonBoot: k.f.boot, ConfirmedProcess: confirmed, RemoteWork: remote, EvidenceDigest: strings.Repeat("e", 64)}
	wall := time.Now().UTC()
	duration := int64(time.Millisecond)
	return c.Evidence{Terminated: m, Measurement: c.Measurement{RequestedAt: wall, AcknowledgedAt: wall, ObservedAt: &wall, RequestToAckNS: duration, AckToObservedNS: &duration}}
}

func quiescent() store.BoundaryState {
	return store.BoundaryState{Reservations: 2, TerminalReceipts: 2, Quiescent: true}
}

func (k *task) report(t *testing.T, evidence c.Evidence, boundary store.BoundaryState) store.TerminationReply {
	t.Helper()
	reply, err := k.f.s.ReportTermination(ctx, k.f.runner, k.session.SessionID, p.FencedVersion, evidence, boundary)
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

// upload packs an in-memory candidate and runs begin, every PUT and commit.
func (k *task) upload(t *testing.T, outcome string, files map[string]string) store.CommitReply {
	t.Helper()
	identity := k.id()
	m := store.CandidateManifest{Version: manifestVersion, Identity: identity, Base: store.ArtifactBase{Revision: k.d.Request.Envelope.BaseCommit, SHA256: sha([]byte(k.d.Request.Envelope.BaseCommit))}, Outcome: outcome, Tracked: []store.ArtifactBlob{}, Untracked: []store.ArtifactBlob{}, Binary: []store.ArtifactBlob{}, Recovery: []store.ArtifactBlob{}, Deleted: []string{}}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	blobs := map[string][]byte{}
	for _, name := range names {
		body := []byte(files[name])
		m.Tracked = append(m.Tracked, store.ArtifactBlob{Path: name, SHA256: sha(body), Bytes: int64(len(body))})
		blobs[sha(body)] = body
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	result := p.Message{Version: p.FencedVersion, Kind: "result", MessageID: uuid(), Identity: identity, Manifest: &p.Manifest{ManifestID: uuid(), SHA256: sha(raw), Bytes: int64(len(raw))}}
	begin := store.UploadBegin{Version: execwire.Version, MessageID: uuid(), Result: result, Manifest: raw}
	session, err := k.f.s.BeginUpload(ctx, k.f.runner, k.session.SessionID, identity.AttemptID, begin)
	if err != nil {
		t.Fatal(err)
	}
	for _, missing := range session.Missing {
		if _, err := k.f.s.RecordUploadedBlob(ctx, k.f.runner, k.session.SessionID, session.UploadID, missing.SHA256, bytes.NewReader(blobs[missing.SHA256]), -1); err != nil {
			t.Fatal(err)
		}
	}
	reply, err := k.f.s.CommitUpload(ctx, k.f.runner, k.session.SessionID, session.UploadID, uuid())
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func (k *task) usage(t *testing.T, receipts ...store.UsageReceipt) {
	t.Helper()
	if err := k.f.s.RecordUsage(ctx, k.f.runner, k.session.SessionID, store.UsageReport{Version: execwire.Version, MessageID: uuid(), Identity: k.id(), Receipts: receipts}); err != nil {
		t.Fatal(err)
	}
}

// derive mirrors the store's dispatchID: a UUIDv4 shape over SHA-256(key:purpose).
func derive(key, purpose string) string {
	b := sha256.Sum256([]byte(key + ":" + purpose))
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// seedUnresolved leaves n acknowledged, never-leased attempts across n approved
// tasks in the store: the shape a concurrent scheduler (or n interrupted
// sequential runs) would leave behind. Sequential admission allows one
// unresolved attempt at a time, so this population cannot be produced through
// store.Dispatch; the rows are written directly, but each satisfies every
// integrity check loadDispatch applies (input digest over the same canonical
// input, assignment identity and route, foreign keys), so reconcile reads them
// as genuine dispatches. Every task's grant is approved through the API.
func (f *fixture) seedUnresolved(t *testing.T, n int) []string {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(f.dir, "state.db"), RawQuery: "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	attempts := make([]string, 0, n)
	for range n {
		k := f.newTask(t)
		request := k.request
		input := struct {
			store.DispatchRequest
			Facts sc.Eligibility `json:"facts"`
		}{request, f.facts}
		hash, err := sc.Digest(input)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		route := request.Decision.Selected
		identity := p.Identity{Generation: f.generation, TaskID: request.Request.TaskID, AttemptID: derive(request.ID, "attempt"), Epoch: 1}
		m := p.Message{Version: p.FencedVersion, Kind: "assign", MessageID: derive(request.ID, "message"), AssignmentID: derive(request.ID, "assignment"), Identity: identity, InputDigest: hash, Route: &p.Route{RouteRef: route.RouteRef, DecisionDigest: request.Request.Envelope.RouteDecision.SHA256, PolicyDigest: route.Policy.SHA256, LimitsProfile: route.LimitsProfile}}
		assignment, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		ack := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: uuid(), Identity: identity, AssignmentID: m.AssignmentID, RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.boot}
		ackBody, err := json.Marshal(ack)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UnixMilli()
		for _, stmt := range []struct {
			query string
			args  []any
		}{
			{"INSERT INTO tasks VALUES(?,'ready')", []any{identity.TaskID}},
			{"INSERT INTO attempts VALUES(?,?,1,'assigned',1,?)", []any{identity.AttemptID, identity.TaskID, m.AssignmentID}},
			{"INSERT INTO events(message_id,attempt_id,task_id,epoch,revision,message) VALUES(?,?,?,1,1,?)", []any{m.MessageID, identity.AttemptID, identity.TaskID, string(assignment)}},
			{"INSERT INTO dispatches VALUES(?,?,?,?,?,?,?,?,?,?,?)", []any{request.ID, identity.AttemptID, identity.TaskID, request.Request.GrantID, f.runnerID, f.facts.ID, f.facts.Revision, hash, string(body), string(assignment), now}},
			{"INSERT INTO dispatch_acks VALUES(?,?,?)", []any{request.ID, ack.MessageID, string(ackBody)}},
		} {
			if _, err := tx.Exec(stmt.query, stmt.args...); err != nil {
				tx.Rollback()
				t.Fatal(stmt.query, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		attempts = append(attempts, identity.AttemptID)
	}
	return attempts
}

// ownerClears writes the owner's clearing record for a pause_task or global_stop
// latch the way the owner API's resume commands do: a reconcile_reports row
// keyed latch-cleared:<stop_id> with a LatchClearance body, through a second
// connection while the store is open (those commands are not on this base).
// Reconcile itself never writes such a record for these kinds.
func (f *fixture) ownerClears(t *testing.T, stop c.Request, attemptID string) {
	t.Helper()
	body, err := json.Marshal(store.LatchClearance{StopID: stop.ID, TaskID: stop.TaskID, AttemptID: attemptID, Kind: stop.Kind, Cause: stop.Cause, Actor: f.owner, DaemonBoot: f.boot, ClearedMS: time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(f.dir, "state.db"), RawQuery: "_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO reconcile_reports VALUES(?,?,?,?)", "latch-cleared:"+stop.ID, f.boot, time.Now().UnixMilli(), string(body)); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) latched(t *testing.T, taskID string) bool {
	t.Helper()
	latched, err := f.s.TaskLatched(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	return latched
}

// reopen simulates a daemon restart: close and open the same state directory.
// The new store gets a fresh boot and a fresh control clock domain.
func (f *fixture) reopen(t *testing.T) {
	t.Helper()
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, f.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	f.offset = 0
	t.Cleanup(func() { s.Close() })
	f.refreshBoot(t)
}

// advance moves the store's control clock forward so the replacement barrier
// (30 s maximum validity plus every retained margin after a reopen) elapses
// without waiting; the test clock is the only clock the barrier reads.
func (f *fixture) advance(d time.Duration) {
	f.offset += d
	offset := f.offset
	f.s.SetControlClock(func() time.Time { return time.Now().Add(offset) })
}

// restore simulates S6's restore outcome on this store: a new generation and the
// paused flag, applied to the closed database file. OpenRestored is not on this
// branch, so the mutation is direct; the reopened store then sees every retained
// attempt as another generation's.
func (f *fixture) restore(t *testing.T) {
	t.Helper()
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE metadata SET generation=? WHERE singleton=1", uuid()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE daemon_state SET paused=1,reason='restored',updated_ms=? WHERE singleton=1", time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, f.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	t.Cleanup(func() { s.Close() })
	f.refreshBoot(t)
}

func (f *fixture) deps() Deps { return Deps{Store: f.s, Now: time.Now} }

func (f *fixture) startup(t *testing.T, auto bool) Report {
	t.Helper()
	report, err := Startup(ctx, Deps{Store: f.s, Now: time.Now, AutoRetry: auto})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func (f *fixture) sweep(t *testing.T, auto bool) Report {
	t.Helper()
	report, err := Sweep(ctx, Deps{Store: f.s, Now: time.Now, AutoRetry: auto})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func entryFor(t *testing.T, report Report, attemptID string) Entry {
	t.Helper()
	for _, e := range report.Entries {
		if e.AttemptID == attemptID && e.Classification != LatchCleared {
			return e
		}
	}
	t.Fatalf("no entry for %s in %+v", attemptID, report.Entries)
	return Entry{}
}

func entriesOf(report Report, class string) []Entry {
	var out []Entry
	for _, e := range report.Entries {
		if e.Classification == class {
			out = append(out, e)
		}
	}
	return out
}

func (f *fixture) nonTerminal(t *testing.T) []store.AttemptSummary {
	t.Helper()
	out, err := f.s.NonTerminalAttempts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *fixture) released(t *testing.T, dispatchID string) bool {
	t.Helper()
	d, err := f.s.Assignment(ctx, dispatchID)
	if err != nil {
		t.Fatal(err)
	}
	return d.Released
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var refusal *g.Refusal
	if !errors.As(err, &refusal) || refusal.Code != code {
		t.Fatalf("want %s refusal, got %v", code, err)
	}
}
