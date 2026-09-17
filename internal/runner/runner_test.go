package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/korallis/letmecook/internal/isolation"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/inference"
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
	if !errors.Is(f.r.Launch(context.Background(), LaunchRequest{}), ErrExecutionDisabled) || f.r.Status().ExecutionEnabled {
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
	if self.Kill() != nil {
		os.Exit(99)
	}
	// Do not race normal exit against asynchronous SIGKILL delivery.
	for {
		time.Sleep(time.Second)
	}
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

func TestAcceptedInputDoesNotAliasCallerMemory(t *testing.T) {
	f := setup(t)
	original := clone(f.d)
	ack := accept(t, f)
	f.d.Assignment.Route.RouteRef = "caller-change"
	f.d.Request.Envelope.Paths[0] = "caller-change.txt"
	f.d.Facts.Paths[0].Capabilities[0] = "caller-change"
	if !reflect.DeepEqual(*f.r.state.Input, original) || !reflect.DeepEqual(*f.r.state.Assignment, original.Assignment) {
		t.Fatal("caller mutated retained accepted authority without a journal append")
	}
	replayed, err := f.r.Accept(f.o.Session, original)
	if err != nil || !reflect.DeepEqual(replayed, ack) {
		t.Fatal("caller mutation changed durable retransmission", err)
	}
	var retained event
	if err = json.Unmarshal(f.r.log.Records()[1], &retained); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retained.Input, f.r.state.Input) {
		t.Fatal("memory differs from committed acceptance")
	}
	request, err := f.r.RequestLease(f.o.Session)
	if err != nil {
		t.Fatal(err)
	}
	sent := *request.SentMS
	*request.SentMS = sent + 5000
	retry, err := f.r.RequestLease(f.o.Session)
	if err != nil || *retry.SentMS != sent {
		t.Fatal("returned message mutated retained lease request", err)
	}
}

// A boundary's Close may synchronously persist terminal receipt accounting.
// Closing/locking the journal first would lose evidence or deadlock this path.
type closingBoundary struct{ onClose func() }

func (*closingBoundary) Addr() string           { return "127.0.0.1:1" }
func (*closingBoundary) State() inference.State { return inference.State{} }
func (b *closingBoundary) Close(context.Context) inference.State {
	if b.onClose != nil {
		b.onClose()
		b.onClose = nil
	}
	return inference.State{Quiescent: true}
}
func TestCloseBoundaryBeforeJournalAndRuntimeClockRegression(t *testing.T) {
	f := setup(t)
	accept(t, f)
	if err := f.r.Enqueue("evidence", "first", map[string]string{"state": "retained"}); err != nil {
		t.Fatal(err)
	}
	f.now--
	if err := f.r.Enqueue("evidence", "regressed", map[string]bool{"bad": true}); !errors.Is(err, p.DelayedReply) {
		t.Fatal("clock regression accepted", err)
	}
	f.now++
	f.r.prepared = &preparedLaunch{boundary: &closingBoundary{onClose: func() {
		if err := f.r.Enqueue("usage-complete", "closing", map[string]bool{"terminal": true}); err != nil {
			t.Error(err)
		}
	}}}
	if err := f.r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(f.path, f.o)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	found := false
	for _, entry := range r.Outbox() {
		found = found || entry.Key == "closing"
		if entry.Key == "regressed" {
			t.Fatal("regressed event persisted")
		}
	}
	if !found {
		t.Fatal("boundary completion not durable")
	}
}

func TestOpenIntentWithoutStartingDoesNotWaitForGuardian(t *testing.T) {
	f := setup(t)
	accept(t, f)
	if err := f.r.commit(event{Kind: "launch_intent", At: f.now, Runtime: &LaunchRecord{Qualification: "development", ReceiptPath: filepath.Join(t.TempDir(), "never-created.json")}}); err != nil {
		t.Fatal(err)
	}
	if err := f.r.Close(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	reopened, err := Open(f.path, f.o)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("intent-only recovery waited for nonexistent guardian: %s", elapsed)
	}
	if reopened.Status().ExecutionEnabled {
		t.Fatal("old intent resumed")
	}
}

type guardianTestLauncher struct{}

func (guardianTestLauncher) Wrap(*exec.Cmd) error               { return nil }
func (guardianTestLauncher) Observation() isolation.Observation { return isolation.Observation{} }
func (guardianTestLauncher) Cleanup() error                     { return nil }
func TestGuardianInitialSpecSerializedWithRenewals(t *testing.T) {
	receipt := filepath.Join(t.TempDir(), "receipt")
	l := &guardianLauncher{inner: guardianTestLauncher{}, executable: "/usr/bin/true", receipt: receipt, deadline: func() int64 { return 1000 }, limits: ResourceLimits{1024, 64 << 20, 10}}
	cmd := exec.Command("/bin/sleep", "60")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LARGE=" + strings.Repeat("x", 128<<10)}
	if err := l.Wrap(cmd); err != nil {
		t.Fatal(err)
	}
	// Duplicate the child's ends, like Start does; closing the parent copies must
	// not terminate this independent decoder/control writer.
	inputFD, err := syscall.Dup(int(l.childInput.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	controlFD, err := syscall.Dup(int(l.childControl.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	input := os.NewFile(uintptr(inputFD), "child-in")
	control := os.NewFile(uintptr(controlFD), "child-control")
	defer input.Close()
	defer control.Close()
	defer l.CloseInput()
	if err := l.Renew(999); !errors.Is(err, ErrStopped) {
		t.Fatal("renewal preceded launch frame", err)
	}
	decoded := make(chan error, 1)
	go func() {
		dec := json.NewDecoder(input)
		var got LaunchSpec
		if err := dec.Decode(&got); err != nil {
			decoded <- err
			return
		}
		if len(got.Env) != 2 || len(got.Env[1]) != (128<<10)+6 {
			decoded <- errors.New("initial spec corrupted")
			return
		}
		enc := json.NewEncoder(control)
		enc.Encode(GuardianStarted{PID: 2, PGID: 2})
		for i := 0; i < 16; i++ {
			var renewal Renewal
			if err := dec.Decode(&renewal); err != nil {
				decoded <- err
				return
			}
			if renewal.DeadlineMS != 999 {
				decoded <- errors.New("bad renewal")
				return
			}
		}
		enc.Encode(GuardianReport{GuardianStarted: GuardianStarted{PID: 2, PGID: 2}, PGIDEmpty: true})
		decoded <- nil
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := l.startedHandle(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.Renew(999); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	select {
	case err := <-decoded:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	<-l.done
}

func TestGuardianFailedStartClosesParentPipes(t *testing.T) {
	l := &guardianLauncher{inner: guardianTestLauncher{}, executable: "/usr/bin/true", receipt: filepath.Join(t.TempDir(), "receipt"), deadline: func() int64 { return 1000 }, limits: ResourceLimits{1024, 64 << 20, 10}}
	cmd := exec.Command("/bin/sleep", "60")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Dir = t.TempDir()
	if err := l.Wrap(cmd); err != nil {
		t.Fatal(err)
	}
	files := []*os.File{l.pipe, l.childInput, l.control, l.childControl}
	l.abortStart()
	l.abortStart()
	for _, f := range files {
		if _, err := f.Stat(); err == nil {
			t.Fatal("failed Start retained parent descriptor")
		}
	}
	select {
	case <-l.done:
	default:
		t.Fatal("failed Start still awaiting nonexistent guardian")
	}
}

func TestTickRetainsDistinctStopErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		want   error
		mutate func(*fixture)
	}{
		{"lease_expired", ErrLeaseExpired, func(f *fixture) { f.now = *f.r.state.StopBy }},
		{"local_policy_changed", ErrPolicyChanged, func(f *fixture) { f.local.Enabled = false }},
		{"grant_expired", ErrGrantExpired, func(f *fixture) { f.r.state.Input.Request.Envelope.ExpiresMS = 99999 }},
		{"attempt_budget_expired", ErrAttemptBudgetExpired, func(f *fixture) { f.now = *f.r.state.AttemptBy }},
		{"clock_invalid", ErrClockInvalid, func(f *fixture) { f.now = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			accept(t, f)
			req, err := f.r.RequestLease(f.o.Session)
			if err != nil {
				t.Fatal(err)
			}
			if err = apply(f.r, f.o.Session, reply(req)); err != nil {
				t.Fatal(err)
			}
			tc.mutate(f)
			for range 2 {
				status, err := f.r.Tick()
				if !errors.Is(err, tc.want) || !status.StopRequired || status.Reason != tc.name {
					t.Fatalf("want %v, got %v %+v", tc.want, err, status)
				}
			}
		})
	}
}

func TestWaitGuardianRequiresPrivateMatchingReceipt(t *testing.T) {
	f := setup(t)
	now := time.Now().UnixNano()
	rec := &LaunchRecord{ReceiptPath: filepath.Join(t.TempDir(), "guardian.json"), PID: 42, PGID: 42, StartUnixNS: now, StartToken: "unique"}
	rep := GuardianReport{GuardianStarted: GuardianStarted{PID: 42, PGID: 42, StartUnixNS: now, StartToken: "unique"}, ObservedUnixNS: now + 1000, StopUnixNS: now + 1, StopToObservedNS: 999, PGIDEmpty: true}
	f.r.state.Runtime = rec
	f.r.guardian = &guardianLauncher{done: make(chan struct{}), report: rep}
	close(f.r.guardian.done)
	if _, err := f.r.WaitGuardian(context.Background()); !errors.Is(err, ErrTerminationUnconfirmed) {
		t.Fatal("pipe alone trusted", err)
	}
	wrong := rep
	wrong.StartUnixNS++
	if err := DurableFile(rec.ReceiptPath, wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.WaitGuardian(context.Background()); !errors.Is(err, ErrTerminationUnconfirmed) {
		t.Fatal("wrong launch receipt trusted", err)
	}
	if err := DurableFile(rec.ReceiptPath, rep); err != nil {
		t.Fatal(err)
	}
	if got, err := f.r.WaitGuardian(context.Background()); err != nil || got != rep {
		t.Fatalf("private receipt ignored: %+v %v", got, err)
	}
}

func TestGuardianDeadlineRecomputedAfterAdapterStart(t *testing.T) {
	remaining := int64(1000)
	l := &guardianLauncher{inner: guardianTestLauncher{}, executable: "/usr/bin/true", receipt: filepath.Join(t.TempDir(), "receipt"), deadline: func() int64 { return remaining }}
	cmd := exec.Command("/bin/sleep", "60")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if err := l.Wrap(cmd); err != nil {
		t.Fatal(err)
	}
	inputFD, _ := syscall.Dup(int(l.childInput.Fd()))
	controlFD, _ := syscall.Dup(int(l.childControl.Fd()))
	input := os.NewFile(uintptr(inputFD), "input")
	control := os.NewFile(uintptr(controlFD), "control")
	defer input.Close()
	defer control.Close()
	defer l.CloseInput()
	remaining = 100 // model time spent in Harness.Start after Wrap
	got := make(chan LaunchSpec, 1)
	go func() {
		var spec LaunchSpec
		json.NewDecoder(input).Decode(&spec)
		got <- spec
		json.NewEncoder(control).Encode(GuardianStarted{PID: 42, PGID: 42})
		control.Close()
	}()
	if _, err := l.startedHandle(context.Background()); err != nil {
		t.Fatal(err)
	}
	spec := <-got
	if spec.DeadlineMS != 100 || time.Until(time.Unix(0, spec.CutoffUnixNS)) > 100*time.Millisecond {
		t.Fatalf("startup latency extended deadline: %+v", spec)
	}
	<-l.done
}

func TestOutboxRefusalPersistsWithoutAcknowledging(t *testing.T) {
	f := setup(t)
	if err := f.r.Enqueue("message", "request", map[string]string{"value": "retained"}); err != nil {
		t.Fatal(err)
	}
	original := f.r.Outbox()[0].Body
	if err := f.r.Refuse("request", "409 revision_conflict"); err != nil {
		t.Fatal(err)
	}
	if err := f.r.Acknowledge("request"); err == nil {
		t.Fatal("refusal became ACK")
	}
	f.r.Close()
	r, err := Open(f.path, f.o)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	e := r.Outbox()[0]
	if e.Acknowledged || e.Refused != "409 revision_conflict" || !reflect.DeepEqual(e.Body, original) {
		t.Fatalf("refusal changed bytes/outcome: %+v", e)
	}
}

func TestGuardianControlRejectsUnknownFields(t *testing.T) {
	l := &guardianLauncher{inner: guardianTestLauncher{}, executable: "/usr/bin/true", receipt: filepath.Join(t.TempDir(), "receipt"), deadline: func() int64 { return 1000 }}
	cmd := exec.Command("/bin/sleep", "60")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if err := l.Wrap(cmd); err != nil {
		t.Fatal(err)
	}
	inputFD, _ := syscall.Dup(int(l.childInput.Fd()))
	controlFD, _ := syscall.Dup(int(l.childControl.Fd()))
	input := os.NewFile(uintptr(inputFD), "input")
	control := os.NewFile(uintptr(controlFD), "control")
	defer input.Close()
	defer control.Close()
	defer l.CloseInput()
	go func() {
		var spec LaunchSpec
		json.NewDecoder(input).Decode(&spec)
		control.Write([]byte("{\"pid\":42,\"pgid\":42,\"untrusted\":true}\n"))
		control.Close()
	}()
	if _, err := l.startedHandle(context.Background()); err == nil {
		t.Fatal("unknown control field accepted")
	}
	<-l.done
}

func TestStoppedClockStillRetainsTerminalEvidence(t *testing.T) {
	for _, now := range []int64{-1, 1, p.MaxInteger + 1} {
		t.Run(fmt.Sprint(now), func(t *testing.T) {
			f := setup(t)
			accept(t, f)
			last := f.r.state.Last
			f.now = now
			if _, err := f.r.Tick(); !errors.Is(err, ErrClockInvalid) {
				t.Fatal("clock did not stop authority", err)
			}
			if f.r.state.Last != last {
				t.Fatal("invalid clock changed retained evidence time")
			}
			if err := f.r.Enqueue("termination", "after-stop", map[string]string{"cause": "clock_invalid"}); err != nil {
				t.Fatal("stopped clock prevented terminal evidence", err)
			}
			if err := f.r.Acknowledge("after-stop"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.r.RequestLease(f.o.Session); !errors.Is(err, ErrClockInvalid) {
				t.Fatal("evidence restored execution authority", err)
			}
		})
	}
}
