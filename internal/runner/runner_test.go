package runner

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	repo "github.com/korallis/letmecook/internal/repositories"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

type fixture struct {
	r     *Runner
	o     Options
	d     store.Dispatch
	local sc.Eligibility
	now   int64
	path  string
}

func setupAt(t *testing.T, path string) *fixture {
	t.Helper()
	f := &fixture{now: 1000, path: path}
	f.o = Options{Session: Session{Version: p.FencedVersion, Peer: strings.Repeat("a", 64), Generation: id(), DaemonBoot: id(), RunnerID: id()}, Bounds: Bounds{100, 5000}, Policy: func() (sc.Eligibility, error) { return clone(f.local), nil }, MonotonicMS: func() int64 { return f.now }, WallTime: func() time.Time { return time.UnixMilli(100000) }}
	var err error
	f.r, err = Create(path, f.o)
	if err != nil {
		t.Fatal(err)
	}
	h := strings.Repeat("a", 64)
	rev := g.Revision{Number: 1, SHA256: h}
	cost := int64(1000)
	route := g.Route{RouteRef: "worker", ProfileRef: "default", RouteRevision: 1, Policy: rev, RouterBuild: h, GraphDigest: h, Evidence: rev, Harness: "fixture-harness", Protocol: "responses", SettingsDigest: h, Isolation: "fixture-isolation", LimitsProfile: "strict-provider-output-v1", LimitsAuthority: "operator-strict", Targets: []g.Target{{Provider: "provider", Model: "model", Billing: "subscription"}}}
	env := g.Envelope{Repository: "fixture", BaseCommit: strings.Repeat("b", 40), Brief: rev, Plan: rev, RouteDecision: rev, CriterionIDs: []string{"c1"}, TaskKinds: []string{"code"}, Paths: []string{"a.txt"}, Operations: []string{"read", "verify", "write"}, Systems: []string{}, Runners: []string{f.o.Session.RunnerID}, Routes: []g.Route{route}, Selection: "pinned", Budgets: g.Budgets{Requests: 5, Attempts: 3, Subattempts: 10, Retries: 2, Concurrency: 1, RequestBytes: 1024, ResponseBytes: 4096, TotalMS: 60000, AttemptMS: 30000, FirstOutputMS: 5000, IdleMS: 5000, ProviderOutputTokens: 128, ProviderCostMicros: &cost}, NotBeforeMS: 1, ExpiresMS: 1000000}
	f.local = sc.Eligibility{ID: "fixture-pair", Revision: 1, Repository: repo.Selection{Repository: "fixture", Revision: 1, ProfileDigest: h, Remote: "https://example.invalid/fixture.git", BaseCommit: env.BaseCommit, RunnerRoot: repo.RunnerRoot{RunnerID: f.o.Session.RunnerID, Root: "/fixture/owned"}}, Enabled: true, RunnerBoot: f.r.Status().RunnerBoot, LocalPolicy: rev, LocalEnvelope: clone(env), Config: rev, Route: route, Paths: []sc.PathEvidence{{Target: route.Targets[0], Compatible: true, Capabilities: []string{"tools"}, ContextTokens: 8192, LocalBounds: true, ProviderOutputBound: true, ProviderCostBound: true}}, Capabilities: []string{"tools"}, Isolation: sc.IsolationProfile{ID: "fixture-isolation", Revision: rev, RuntimeDigest: h, ObservedDigest: h, Kind: "dedicated-vm", Supported: true, Controls: sc.RequiredIsolationControls()}, Capacity: sc.Resources{CPU: 1000, MemoryBytes: 4096, DiskBytes: 8192, Processes: 8}, Concurrency: 1, RouterAuthenticated: true, Availability: "unknown", ValidUntilMS: 1000000}
	task := id()
	decision := sc.Decision{ID: id(), Revision: 1, Assessment: sc.Assessment{TaskID: task, Brief: rev, Plan: rev, ContextDigest: h, Role: "worker", WorkClass: "code", Ambiguity: "low", Consequence: "high", Required: []string{"tools"}, Preferences: []string{}, EvidenceRefs: []string{"operator"}, Unknowns: []string{}, ContextTokens: 4096, AssessorVersion: "operator-v1", SelectorVersion: "deterministic-v1"}, EligibilityID: f.local.ID, Selected: route, RankedAlternatives: []string{}, Authorization: "operator", Reason: "pinned", Resources: sc.Resources{CPU: 500, MemoryBytes: 2048, DiskBytes: 4096, Processes: 4}}
	fh, _ := sc.Digest(f.local)
	decision.Eligibility = g.Revision{Number: 1, SHA256: fh}
	dh, _ := sc.Digest(decision)
	env.RouteDecision = g.Revision{Number: 1, SHA256: dh}
	allowance := clone(env.Budgets)
	allowance.Attempts = 1
	allowance.Retries = 0
	allowance.TotalMS = allowance.AttemptMS
	f.d = store.Dispatch{DispatchRequest: store.DispatchRequest{ID: id(), Request: g.Request{GrantID: id(), TaskID: task, GrantRevision: 1, Action: "execute", Envelope: env}, Decision: decision, Allowance: allowance}, Facts: clone(f.local)}
	ih, _ := inputDigest(f.d)
	f.d.Assignment = p.Message{Version: p.FencedVersion, MessageID: id(), Kind: "assign", Identity: p.Identity{Generation: f.o.Session.Generation, TaskID: task, AttemptID: id(), Epoch: 1}, AssignmentID: id(), InputDigest: ih, Route: &p.Route{RouteRef: route.RouteRef, DecisionDigest: dh, PolicyDigest: h, LimitsProfile: route.LimitsProfile}}
	if err := sc.CheckDispatch(f.d.Request, f.d.Decision, f.local, 100000); err != nil {
		t.Fatal("bad test fixture", err)
	}
	return f
}
func setup(t *testing.T) *fixture {
	t.Helper()
	path, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	f := setupAt(t, filepath.Join(path, "runner"))
	t.Cleanup(func() { f.r.Close() })
	return f
}
func accept(t *testing.T, f *fixture) p.Message {
	t.Helper()
	ack, e := f.r.Accept(f.o.Session, f.d)
	if e != nil {
		t.Fatal(e)
	}
	return ack
}
func reply(req p.Message) p.Message {
	v := int64(30000)
	return p.Message{Version: p.FencedVersion, MessageID: id(), Kind: "lease_reply", Identity: req.Identity, Nonce: req.Nonce, RunnerBoot: req.RunnerBoot, DaemonBoot: req.DaemonBoot, ValidityMS: &v}
}
func apply(r *Runner, s Session, m p.Message) error {
	b, _ := json.Marshal(m)
	return r.ApplyLease(s, b)
}

func TestConcurrentAcceptanceLostAckAndCollision(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	acks := make([]p.Message, 8)
	errs := make([]error, 8)
	for i := range acks {
		wg.Go(func() { acks[i], errs[i] = f.r.Accept(f.o.Session, f.d) })
	}
	wg.Wait()
	for i := range acks {
		if errs[i] != nil || !reflect.DeepEqual(acks[0], acks[i]) {
			t.Fatal("duplicate acceptance", errs[i])
		}
	}
	changed := clone(f.d)
	changed.Assignment.MessageID = id()
	ack, e := f.r.Accept(f.o.Session, changed)
	if e != nil || !reflect.DeepEqual(ack, acks[0]) {
		t.Fatal("lost ack", e)
	}
	changed.Assignment.AssignmentID = id()
	if _, e = f.r.Accept(f.o.Session, changed); e == nil {
		t.Fatal("second assignment for attempt")
	}
	changed = clone(f.d)
	changed.Assignment.Identity.AttemptID = id()
	if _, e = f.r.Accept(f.o.Session, changed); e == nil {
		t.Fatal("concurrent writer")
	}
	if !errors.Is(f.r.Launch(), ErrExecutionDisabled) || f.r.Status().ExecutionEnabled {
		t.Fatal("production launch enabled")
	}
	// Persisted acceptance contains immutable input and ack, not only a hash.
	rows := f.r.log.Records()
	var event event
	json.Unmarshal(rows[1], &event)
	if event.Input == nil || !reflect.DeepEqual(*event.Input, f.d) || !reflect.DeepEqual(*event.Ack, acks[0]) {
		t.Fatal("acceptance not retained")
	}
}
func TestSessionAndIndependentLocalPolicy(t *testing.T) {
	for _, name := range []string{"peer", "version", "generation", "boot", "root", "route", "disabled", "input", "allowance"} {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			session := f.o.Session
			d := clone(f.d)
			switch name {
			case "peer":
				session.Peer = strings.Repeat("b", 64)
			case "version":
				session.Version = p.Version
			case "generation":
				session.Generation = id()
			case "boot":
				session.DaemonBoot = id()
			case "root":
				f.local.Repository.RunnerRoot.Root = "/wrong"
			case "route":
				d.Assignment.Route.RouteRef = "other"
			case "disabled":
				f.local.Enabled = false
			case "input":
				d.Assignment.InputDigest = strings.Repeat("c", 64)
			case "allowance":
				d.Allowance.Attempts = 2
			}
			if _, e := f.r.Accept(session, d); e == nil {
				t.Fatal("accepted mismatch")
			}
			if len(f.r.log.Records()) != 1 {
				t.Fatal("persisted refused assignment")
			}
		})
	}
}
func TestLeaseSendAnchoringDuplicatesAndLateRenewal(t *testing.T) {
	f := setup(t)
	accept(t, f)
	req, e := f.r.RequestLease(f.o.Session)
	if e != nil {
		t.Fatal(e)
	}
	f.now = 1100
	retry, e := f.r.RequestLease(f.o.Session)
	if e != nil || !reflect.DeepEqual(req, retry) {
		t.Fatal("request reset clock", e)
	}
	rep := reply(req)
	f.now = 1200
	if e = apply(f.r, f.o.Session, rep); e != nil {
		t.Fatal(e)
	}
	if *f.r.Status().StopByMS != 25900 {
		t.Fatal("receipt anchored deadline")
	}
	f.now = 1300
	if e = apply(f.r, f.o.Session, rep); !errors.Is(e, p.NonceMismatch) {
		t.Fatal("reply reused", e)
	}
	f.now = 6000
	req, e = f.r.RequestLease(f.o.Session)
	if e != nil {
		t.Fatal(e)
	}
	rep = reply(req)
	f.now = 25900
	if e = apply(f.r, f.o.Session, rep); !errors.Is(e, ErrStopped) {
		t.Fatal("expired renewal accepted", e)
	}
	if !f.r.Status().Quarantined || f.r.Status().StopByMS != nil {
		t.Fatal("expiry not sticky")
	}
	f.now = 6001
	if _, e = f.r.RequestLease(f.o.Session); !errors.Is(e, ErrStopped) {
		t.Fatal("clock rollback resurrected lease", e)
	}
}
func TestRenewalWrongBootAndClockStop(t *testing.T) {
	f := setup(t)
	accept(t, f)
	req, _ := f.r.RequestLease(f.o.Session)
	rep := reply(req)
	rep.DaemonBoot = id()
	if e := apply(f.r, f.o.Session, rep); !errors.Is(e, p.BootMismatch) {
		t.Fatal(e)
	}
	rep = reply(req)
	f.now = 1200
	if e := apply(f.r, f.o.Session, rep); e != nil {
		t.Fatal(e)
	}
	f.now = 6000
	req, _ = f.r.RequestLease(f.o.Session)
	f.now = 6200
	if e := apply(f.r, f.o.Session, reply(req)); e != nil {
		t.Fatal(e)
	}
	if *f.r.Status().StopByMS != 30900 {
		t.Fatal("wrong renewed cutoff")
	}
	f.now = 6000
	if _, e := f.r.Tick(); !errors.Is(e, ErrStopped) {
		t.Fatal("clock regression", e)
	}
}
func TestLocalPolicyChangeAndJournalLossStop(t *testing.T) {
	for _, name := range []string{"policy", "journal"} {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			accept(t, f)
			if name == "policy" {
				f.local.Enabled = false
			} else {
				os.Rename(f.path, f.path+"-lost")
			}
			if _, e := f.r.RequestLease(f.o.Session); e == nil {
				t.Fatal("continued after lost trust")
			}
			if !f.r.Status().StopRequired {
				t.Fatal("missing stop signal")
			}
		})
	}
}
func TestRestartAlwaysQuarantinesAndDropsMonotonicLease(t *testing.T) {
	f := setup(t)
	accept(t, f)
	req, _ := f.r.RequestLease(f.o.Session)
	f.now = 1200
	rep := reply(req)
	if e := apply(f.r, f.o.Session, rep); e != nil {
		t.Fatal(e)
	}
	boot := f.r.Status().RunnerBoot
	f.r.Close()
	f.now = 0
	var e error
	f.r, e = Open(f.path, f.o)
	if e != nil {
		t.Fatal(e)
	}
	s := f.r.Status()
	if !s.Quarantined || s.StopByMS != nil || s.RunnerBoot == boot {
		t.Fatal("resumed old clock domain", s)
	}
	if e = apply(f.r, f.o.Session, rep); !errors.Is(e, ErrStopped) {
		t.Fatal(e)
	}
	if _, e = f.r.Accept(f.o.Session, f.d); !errors.Is(e, ErrStopped) {
		t.Fatal("replayed acceptance after restart", e)
	}
}
func TestCrashHelper(t *testing.T) {
	path := os.Getenv("GAFFER_RUNNER_CRASH_PATH")
	if path == "" {
		return
	}
	f := setupAt(t, path)
	accept(t, f)
	req, e := f.r.RequestLease(f.o.Session)
	if e != nil {
		os.Exit(3)
	}
	f.now = 1200
	if e = apply(f.r, f.o.Session, reply(req)); e != nil {
		os.Exit(4)
	}
	self, _ := os.FindProcess(os.Getpid())
	_ = self.Kill()
	os.Exit(99)
}
func TestRealProcessLossAfterLeaseNeverResumes(t *testing.T) {
	parent, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(parent, "crash")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), "GAFFER_RUNNER_CRASH_PATH="+path)
	output, e := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != -1 {
		t.Fatalf("%v %s", e, output)
	}
	f := setup(t)
	r, e := Open(path, f.o)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if s := r.Status(); !s.Quarantined || s.AssignmentID == "" || s.StopByMS != nil {
		t.Fatal("process loss resumed", s)
	}
	if _, e = r.RequestLease(f.o.Session); e == nil {
		t.Fatal("new session silently replaced authority")
	}
}

func TestInitialDelayNonceAndGlobalMessageIdentity(t *testing.T) {
	f := setup(t)
	accept(t, f)
	req, _ := f.r.RequestLease(f.o.Session)
	wrong := reply(req)
	wrong.Nonce = id()
	if e := apply(f.r, f.o.Session, wrong); !errors.Is(e, p.NonceMismatch) {
		t.Fatal(e)
	}
	wrong = reply(req)
	wrong.MessageID = f.d.Assignment.MessageID
	if e := apply(f.r, f.o.Session, wrong); !errors.Is(e, p.IdentityConflict) {
		t.Fatal(e)
	}
	f.now = 25900
	if e := apply(f.r, f.o.Session, reply(req)); !errors.Is(e, ErrStopped) {
		t.Fatal("late initial reply", e)
	}
	if !f.r.Status().Quarantined {
		t.Fatal("late reply reopened initial lease")
	}
}

func TestFiniteAttemptAndGrantExpiry(t *testing.T) {
	f := setup(t)
	accept(t, f)
	req, _ := f.r.RequestLease(f.o.Session)
	f.now = 1200
	if e := apply(f.r, f.o.Session, reply(req)); e != nil {
		t.Fatal(e)
	}
	f.now = 25000
	req, _ = f.r.RequestLease(f.o.Session)
	f.now = 25100
	if e := apply(f.r, f.o.Session, reply(req)); e != nil {
		t.Fatal(e)
	}
	if *f.r.Status().StopByMS != 31000 {
		t.Fatal("renewal widened attempt duration")
	}
	f.now = 31000
	if _, e := f.r.Tick(); !errors.Is(e, ErrStopped) {
		t.Fatal(e)
	}
	g := setup(t)
	accept(t, g)
	g.r.options.WallTime = func() time.Time { return time.UnixMilli(1000000) }
	if _, e := g.r.RequestLease(g.o.Session); !errors.Is(e, ErrStopped) {
		t.Fatal("expired authority continued", e)
	}
}

func TestLocalResourceCeilingWithConsistentlyBoundDaemonInput(t *testing.T) {
	f := setup(t)
	f.local.Capacity.CPU = 100
	f.d.Facts = clone(f.local)
	fh, _ := sc.Digest(f.local)
	f.d.Decision.Eligibility.SHA256 = fh
	dh, _ := sc.Digest(f.d.Decision)
	f.d.Request.Envelope.RouteDecision.SHA256 = dh
	f.d.Assignment.Route.DecisionDigest = dh
	ih, _ := inputDigest(f.d)
	f.d.Assignment.InputDigest = ih
	if _, e := f.r.Accept(f.o.Session, f.d); !errors.Is(e, ErrPolicy) {
		t.Fatal("daemon exceeded independently loaded capacity", e)
	}
}
