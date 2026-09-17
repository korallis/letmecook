package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/korallis/letmecook/internal/closedjson"
	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execclient"
	w "github.com/korallis/letmecook/internal/execwire"
	h "github.com/korallis/letmecook/internal/harness"
	"github.com/korallis/letmecook/internal/harness/fake"
	"github.com/korallis/letmecook/internal/harness/opencode"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/isolation"
	"github.com/korallis/letmecook/internal/repositories"
	"github.com/korallis/letmecook/internal/runner"
	"github.com/korallis/letmecook/internal/runstream"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

type supervisor struct {
	executable string
	probeKey   string
	cfg        config
	client     *execclient.Client
	session    w.Session
	boot       string
	clock      time.Time
	profile    isolation.Profile
	harness    h.Harness
	out        io.Writer
	cancel     chan p.Message
	mu         sync.Mutex
}

func (s *supervisor) options() runner.Options {
	return runner.Options{Session: runner.Session{Version: p.FencedVersion, Peer: s.cfg.Fingerprint, Generation: s.session.Generation, DaemonBoot: s.session.DaemonBoot, RunnerID: s.session.RunnerID}, Bounds: runner.Bounds{DriftMS: s.session.DriftMS, TerminationMS: s.session.TerminationMS}, Boot: s.boot, Policy: func() (sc.Eligibility, error) { return loadPolicy(s.cfg.Policy) }, Admission: sc.AdmissionPolicy{DevelopmentProfiles: []string{s.cfg.Isolation}}, MonotonicMS: func() int64 { return time.Since(s.clock).Milliseconds() }, WallTime: time.Now}
}
func serve(ctx context.Context, cfg config, out io.Writer) error {
	if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return err
	}
	info, e := os.Stat(cfg.StateDir)
	if e != nil || info.Mode().Perm() != 0700 {
		return errors.New("state-dir must have mode 0700")
	}
	real, err := filepath.EvalSymlinks(cfg.StateDir)
	if err != nil || real != cfg.StateDir {
		return errors.New("state-dir must be canonical")
	}
	lock, err := os.OpenFile(filepath.Join(cfg.StateDir, "serve.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("runner already owns state-dir")
	}
	boot := bootRecord{uuid(), os.Getpid(), time.Now().UTC()}
	if err = runner.DurableFile(filepath.Join(cfg.StateDir, "boot.json"), boot); err != nil {
		return err
	}
	if err = runner.DurableFile(filepath.Join(cfg.StateDir, "serve.json"), cfg); err != nil {
		return err
	}
	profile, err := profileFor(cfg)
	if err != nil {
		return err
	}
	adapter, err := harnessFor(cfg)
	if err != nil {
		return err
	}
	client, err := openClient(cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	s := &supervisor{cfg: cfg, client: client, boot: boot.RunnerBoot, clock: time.Now(), profile: profile, harness: adapter, out: out, cancel: make(chan p.Message, 64)}
	// No work is admitted until the operator updates/imports this boot's local facts.
	var recovery []*runner.Runner
	defer func() {
		for _, r := range recovery {
			r.Close()
		}
	}()
	for ctx.Err() == nil {
		local, err := loadPolicy(cfg.Policy)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}
		if err = s.probe(ctx, local); err != nil {
			return err
		}
		hash, _ := sc.Digest(local)
		hello := w.Hello{Version: w.Version, MessageID: uuid(), RunnerBoot: boot.RunnerBoot, EligibilityID: local.ID, EligibilityRevision: local.Revision, PolicyDigest: hash, Journals: []w.Journal{}}
		// Bootstrap session is required to supply validated options for old journals.
		sess, err := client.Hello(ctx, hello)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		s.session = sess
		if recovery == nil {
			rows, _ := filepath.Glob(filepath.Join(cfg.StateDir, "attempts", "*", "journal"))
			recovery = []*runner.Runner{}
			for _, path := range rows {
				r, e := runner.Open(path, s.options())
				if e != nil {
					hello.Journals = append(hello.Journals, w.Journal{DispatchID: filepath.Base(filepath.Dir(path)), Corrupt: true})
					continue
				}
				recovery = append(recovery, r)
				var meta attemptMeta
				if b, e := os.ReadFile(filepath.Join(filepath.Dir(path), "attempt.json")); e == nil && closedjson.Decode(b, &meta, 65536, map[string]bool{"provider_cost_micros": true}) == nil {
					hello.Journals = append(hello.Journals, journalSummary(r, meta, filepath.Dir(path)))
				}
				if err = s.replay(ctx, r); err != nil && !execclient.IsFence(err) {
					return err
				}
				if meta.Dispatch.ID != "" {
					if err = s.recoverTermination(ctx, r, meta); err != nil && !execclient.IsFence(err) && !isRefusal(err) && !errors.Is(err, runner.ErrTerminationUnconfirmed) {
						return err
					}
				}
			}
			if len(hello.Journals) > 0 {
				hello.MessageID = uuid()
				if s.session, err = client.Hello(ctx, hello); err != nil {
					return err
				}
			}
		}
		if sess.Mode == "normal" && local.RunnerBoot == boot.RunnerBoot {
			break
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(500 * time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	_ = writeJSON(out, map[string]any{"runner_boot": boot.RunnerBoot, "qualification": profile.Qualification(), "supported": false, "state": "ready"})
	assignments := make(chan w.Dispatch, 16)
	pollErr := make(chan error, 1)
	pollCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		for pollCtx.Err() == nil {
			in, err := client.Inbox(pollCtx, 1000)
			if err != nil {
				if execclient.IsFence(err) {
					select {
					case pollErr <- err:
					default:
					}
					return
				}
				select {
				case <-pollCtx.Done():
					return
				case <-time.After(100 * time.Millisecond):
					continue
				}
			}
			for _, m := range in.Cancels {
				select {
				case s.cancel <- m:
				case <-pollCtx.Done():
					return
				}
			}
			if !in.Paused {
				for _, d := range in.Assignments {
					select {
					case assignments <- d:
					case <-pollCtx.Done():
						return
					}
				}
			}
			select {
			case <-pollCtx.Done():
				return
			case <-time.After(time.Duration(max(10, min(in.PollAfterMS, 1000))) * time.Millisecond):
			}
		}
	}()
	seen := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-pollErr:
			return err
		case <-s.cancel:
			// No live attempt owns this target. Its durable observation was already
			// sent (or remains in its journal); pending cancels cannot block inbox.

		case d := <-assignments:
			if seen[d.ID] {
				continue
			}
			err := s.attempt(ctx, d)
			// Inbox delivery is one-time. Retry transient pre-admission failures
			// locally; no attempt directory/authority exists yet.
			for transientInputError(err) && ctx.Err() == nil {
				if _, statErr := os.Stat(filepath.Join(cfg.StateDir, "attempts", d.ID)); !os.IsNotExist(statErr) {
					break
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(500 * time.Millisecond):
				}
				err = s.attempt(ctx, d)
			}
			seen[d.ID] = true
			if err != nil {
				_ = writeJSON(out, map[string]any{"dispatch_id": d.ID, "error": err.Error(), "supported": false})
				if execclient.IsFence(err) {
					return err
				}
			}
		}
	}
}

func transientInputError(err error) bool {
	if err == nil || isRefusal(err) {
		return false
	}
	var remote *execclient.Error
	if errors.As(err, &remote) {
		return remote.Status >= 500
	}
	var network interface{ Timeout() bool }
	return errors.As(err, &network)
}

type attemptMeta struct {
	Dispatch   store.Dispatch `json:"dispatch"`
	RunnerBoot string         `json:"runner_boot"`
	DaemonBoot string         `json:"daemon_boot"`
}

func (s *supervisor) message(ctx context.Context, r *runner.Runner, d string, m p.Message, e *w.Evidence, b *inference.State, measurement *c.Measurement) (execclient.MessageReply, error) {
	in := w.MessageEnvelope{Version: w.Version, MessageID: m.MessageID, DispatchID: d, Message: m, Evidence: e, Boundary: b, Measurement: measurement}
	if err := r.Enqueue("message", m.MessageID, in); err != nil {
		return execclient.MessageReply{}, err
	}
	reply, err := s.client.Message(ctx, in)
	if err == nil {
		err = r.Acknowledge(m.MessageID)
	} else {
		err = s.recordRefusal(r, m.MessageID, err)
	}
	return reply, err
}
func (s *supervisor) lease(ctx context.Context, r *runner.Runner, d string) error {
	req, err := r.RequestLease(s.options().Session)
	if err != nil {
		return err
	}
	in := w.LeaseEnvelope{Version: w.Version, MessageID: req.MessageID, DispatchID: d, Request: req}
	if err = r.Enqueue("lease", req.MessageID, in); err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	reply, err := s.client.Lease(bounded, in)
	if err != nil {
		if execclient.IsFence(err) {
			_ = r.Fence(err.Error())
		}
		return err
	}
	b, _ := json.Marshal(reply)
	if err = r.ApplyLease(s.options().Session, b); err != nil {
		return err
	}
	if err = r.Acknowledge(req.MessageID); err != nil {
		return err
	}
	return r.RenewGuardian()
}
func (s *supervisor) transition(ctx context.Context, r *runner.Runner, d store.Dispatch, from, to p.AttemptState, revision int64, e w.Evidence, stopID string) (p.Message, error) {
	if to == p.Stopping {
		for _, cause := range w.LocalStopCauses {
			if stopID == w.LocalStopID(d.Assignment.Identity.AttemptID, cause) {
				e.Cause = cause
				break
			}
		}
	}
	m := p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "transition", Identity: d.Assignment.Identity, ExpectedRevision: &revision, From: from, To: to}
	reply, err := s.message(ctx, r, d.ID, m, &e, nil, nil)
	if err == nil && (reply.Message == nil || !messagesEqual(m, *reply.Message)) {
		return m, p.IdentityConflict
	}
	return m, err
}
func messagesEqual(a, b p.Message) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(aa) == string(bb)
}
func (s *supervisor) attempt(ctx context.Context, wire w.Dispatch) (result error) {
	if local, e := loadPolicy(s.cfg.Policy); e != nil {
		return e
	} else if e = s.probe(ctx, local); e != nil {
		return e
	}
	raw, _ := json.Marshal(wire)
	var d store.Dispatch
	if err := w.Decode(raw, &d); err != nil {
		return err
	}
	if !p.ValidID(d.ID) || !p.ValidID(d.Assignment.Identity.AttemptID) {
		return p.Malformed
	}
	input, inputErr := s.client.Input(ctx, d.ID)
	if inputErr != nil {
		return inputErr
	}
	if err := input.Validate(d); err != nil {
		return err
	}
	if input.Harness != s.cfg.Harness {
		return runner.ErrPolicy
	}
	dir := filepath.Join(s.cfg.StateDir, "attempts", d.ID)
	if _, err := os.Stat(dir); err == nil {
		return errors.New("retained attempt requires reconciliation")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	r, err := runner.Create(filepath.Join(dir, "journal"), s.options())
	if err != nil {
		return err
	}
	defer r.Close()
	defer r.StopGuardian()
	if err = runner.DurableFile(filepath.Join(dir, "attempt.json"), attemptMeta{d, s.boot, s.session.DaemonBoot}); err != nil {
		return err
	}
	if err = r.Enqueue("task_input", input.BriefSHA256, input); err != nil {
		return err
	}
	var repo repositories.Profile
	data, policyErr := os.ReadFile(s.cfg.RepositoryProfile)
	if policyErr == nil {
		policyErr = closedjson.Decode(data, &repo, 65536, nil)
	}
	if policyErr == nil {
		policyErr = repo.Select(d.Facts.Repository)
	}
	if policyErr == nil && input.Harness == "opencode" {
		if len(d.Decision.Selected.Targets) == 0 {
			policyErr = runner.ErrPolicy
		} else {
			policyErr = opencode.ValidateSettings(input.Settings, d.Decision.Selected.Targets[0].Model)
		}
	}
	if policyErr != nil || d.Facts.Repository.RunnerRoot.Root != s.cfg.RepositoryRoot || d.Facts.Repository.RunnerRoot.RunnerID != s.session.RunnerID {
		refusal := p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "refuse", Identity: d.Assignment.Identity, InReplyTo: d.Assignment.MessageID, Reason: p.LocalPolicyDenied}
		if _, err := s.message(ctx, r, d.ID, refusal, nil, nil, nil); err != nil {
			return err
		}
		return errors.Join(runner.ErrPolicy, policyErr)
	}
	accepted, running, launchAttempted, stopHandled, jobFinished := false, false, false, false, false
	phase, revision := p.Assigned, int64(1)
	defer func() {
		if result != nil && accepted && !stopHandled && (!running || ctx.Err() != nil && !jobFinished || isRefusal(result) && !custodyAccepted(r)) {
			cause := "launch_failed"
			if ctx.Err() != nil {
				cause = "runner_shutdown"
			}
			stopErr := s.failedLocal(r, d, phase, revision, launchAttempted, cause, result)
			if ctx.Err() != nil && stopErr == nil {
				result = nil
			} else {
				result = errors.Join(result, stopErr)
			}
		}
	}()
	ack, err := r.Accept(s.options().Session, d)
	if err != nil {
		if errors.Is(err, p.StaleGeneration) || errors.Is(err, p.BootMismatch) {
			_ = r.Fence(err.Error())
		}
		return err
	}
	accepted = true
	if _, err = s.message(ctx, r, d.ID, ack, nil, nil, nil); err != nil {
		return err
	}
	if err = s.lease(ctx, r, d.ID); err != nil {
		return err
	}
	checkout, err := repositories.Prepare(ctx, repo, d.Facts.Repository)
	if err != nil {
		return err
	}
	if input.Harness == "opencode" && d.Allowance.Requests < 2 {
		return errors.New("opencode requires at least two granted requests")
	}
	settings := input.Settings
	if s.cfg.Harness == "fake" {
		spec, e := fake.Decode(input.Settings, int(d.Assignment.Identity.Epoch)-1)
		if e != nil {
			return e
		}
		settings, _ = json.Marshal(spec)
	}
	brief := input.Brief
	exe := s.executable
	if exe == "" {
		exe, _ = os.Executable()
	}
	scope := inference.Scope{Identity: d.Assignment.Identity, Token: uuid(), Protocols: []string{d.Decision.Selected.Protocol}, Limits: inference.Limits{Requests: d.Allowance.Requests, RequestBytes: d.Allowance.RequestBytes, ResponseBytes: d.Allowance.ResponseBytes, RequestTimeout: time.Duration(d.Allowance.AttemptMS) * time.Millisecond}}
	for _, target := range d.Decision.Selected.Targets {
		scope.Models = append(scope.Models, target.Model)
	}
	if s.cfg.GatewayConfig != "" {
		scope.Gateway, err = inference.LoadGatewayConfig(s.cfg.GatewayConfig)
		if err != nil {
			return err
		}
	}
	req := runner.LaunchRequest{Profile: s.profile, Harness: s.harness, Boundary: scope, Checkout: checkout, Executable: exe, RecoveryDir: dir, Settings: settings, Brief: brief}
	launch, err := r.PrepareLaunch(ctx, req)
	if err != nil {
		return err
	} // The active nonce is retained in the first lease outbox.
	nonce := lastNonce(r)
	m, err := s.transition(ctx, r, d, p.Assigned, p.Starting, 1, w.Evidence{Kind: "launch_intent", Workspace: launch.Workspace.Root, BoundaryPort: launch.BoundaryPort, Nonce: nonce}, "")
	if err != nil {
		return err
	}
	phase, revision = p.Starting, 2
	if err = r.StartingAcknowledged(m); err != nil {
		return err
	}
	launchAttempted = true
	if err = r.Launch(ctx, req); err != nil {
		return err
	}
	runtime := r.Status().Runtime
	_, err = s.transition(ctx, r, d, p.Starting, p.Running, 2, w.Evidence{Kind: "launched", PID: runtime.PID, PGID: runtime.PGID, GuardianPID: runtime.GuardianPID, StartUnixNS: runtime.StartUnixNS}, "")
	if err != nil {
		return err
	}
	running = true
	phase, revision = p.Running, 3
	spool, err := runstream.CreateSpool(filepath.Join(dir, "spool"), d.Assignment.Identity, s.cfg.SpoolLimit)
	if err != nil {
		return err
	}
	defer spool.Close()
	events, err := r.Events(ctx)
	if err != nil {
		return err
	}
	eventCtx, stopEvents := context.WithCancel(context.Background())
	defer stopEvents()
	eventCh := make(chan eventResult, 1)
	eventDone := make(chan struct{})
	var interrupted *eventResult
	go func() {
		defer close(eventDone)
		for {
			ev, e := events.Next(eventCtx)
			select {
			case eventCh <- eventResult{ev, e}:
			case <-eventCtx.Done():
				// Preserve an event already removed from a destructive stream.
				interrupted = &eventResult{ev, e}
				return
			}
			if e != nil {
				return
			}
		}
	}()
	var stopProducerOnce sync.Once
	stopProducer := func() { stopProducerOnce.Do(func() { stopEvents(); <-eventDone }) }
	lastSequence := int64(0)
	spoolFull, fullySpooled := false, false
	retain := func(item eventResult) error {
		if item.err != nil {
			if item.err == io.EOF {
				fullySpooled = !spoolFull
				return nil
			}
			if errors.Is(item.err, context.Canceled) {
				return nil
			}
			return item.err
		}
		full, e := appendHarnessEvent(spool, item.event)
		if e != nil {
			return e
		}
		if full {
			spoolFull = true
		} else {
			lastSequence = item.event.Sequence
		}
		return nil
	}
	drain := func(ctx context.Context) error {
		stopProducer()
		if spoolFull {
			return nil
		} // Never release incomplete native output.
		select {
		case item := <-eventCh:
			if err := retain(item); err != nil {
				return err
			}
		default:
		}
		if interrupted != nil && !spoolFull {
			if err := retain(*interrupted); err != nil {
				return err
			}
		}
		for !fullySpooled && !spoolFull {
			ev, e := events.Next(ctx)
			if err := retain(eventResult{ev, e}); err != nil {
				return err
			}
			if e != nil && e != io.EOF {
				return e
			}
		}
		if err := s.flush(ctx, spool, d.Assignment.Identity.AttemptID); err != nil {
			return err
		}
		if fullySpooled {
			return r.RecordHarnessWatermark(lastSequence)
		}
		return nil
	}
	cancelAndDrain := func(cancel p.Message, cause string) error {
		stopHandled = true
		stopProducer()
		err := s.cancelAttempt(context.Background(), r, d, p.Running, 3, spool, cancel, cause, drain)
		if err != nil {
			return err
		}
		if fullySpooled {
			return r.ReleaseHarness(context.Background(), lastSequence)
		}
		return nil
	}
	renew := time.NewTicker(time.Duration(s.session.RenewEveryMS) * time.Millisecond)
	defer renew.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	failure := ""
	finished := false
	for !finished {
		select {
		case <-ctx.Done():
			// EOF/output delivery can lag the trusted exit receipt. A finished job
			// is custody work, not a new cancellation on supervisor shutdown.
			peek, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
			rep, e := r.WaitGuardian(peek)
			stop()
			if e == nil && rep.PGIDEmpty && !rep.Escaped && rep.Cause == "exit" {
				finished = true
				break
			}
			return cancelAndDrain(s.localCancel(d, "runner_shutdown"), "supervisor_shutdown")
		case cancel := <-s.cancel:
			if cancel.Identity == d.Assignment.Identity {
				return cancelAndDrain(cancel, "cancel")
			}
		case <-renew.C:
			if err := s.lease(ctx, r, d.ID); execclient.IsFence(err) {
				return err
			}
		case <-tick.C:
			status, e := r.Tick()
			if e != nil && status.StopRequired {
				cancel, cause := s.tickCancel(r, d, e)
				return cancelAndDrain(cancel, cause)
			}
		case item := <-eventCh:
			if item.err != nil {
				if item.err != io.EOF {
					failure = "harness_crash"
					r.StopGuardian()
				} else {
					fullySpooled = true
				}
				finished = true
				break
			}
			ev := item.event
			full, appendErr := appendHarnessEvent(spool, ev)
			if appendErr != nil {
				return appendErr
			}
			if full {
				spoolFull = true
				failure = "spool_full"
				r.StopGuardian()
				finished = true
			} else {
				lastSequence = ev.Sequence
				if ev.Kind == h.ApprovalRequired {
					failure = "approval_required"
					r.StopGuardian()
					finished = true
				}
				if ev.Kind == h.Failed {
					failure = "harness_crash"
					r.StopGuardian()
					finished = true
				}
			}
			if err = s.flush(ctx, spool, d.Assignment.Identity.AttemptID); err != nil {
				return err
			}
		}
	}
	stopProducer()
	guardCtx, stopGuard := context.WithTimeout(context.Background(), 6*time.Second)
	defer stopGuard()
	report, err := r.WaitGuardian(guardCtx)
	if err != nil {
		return err
	}
	if !report.PGIDEmpty || report.Escaped {
		return cancelAndDrain(s.localCancel(d, "containment_unconfirmed"), "containment_unconfirmed")
	}
	jobFinished = true
	if err = drain(guardCtx); err != nil {
		return err
	}
	if spoolFull && failure == "" {
		failure = "spool_full"
	}
	if report.ExitCode != 0 && failure == "" {
		failure = "harness_crash"
	}
	if err = checkEnvelope(guardCtx, checkout.Path, checkout.BaseCommit, d.Request.Envelope, repo); err != nil {
		failure = "envelope_violation"
		_, err = spool.Append(runstream.Native{Version: "harness-v1", Kind: "failed", Data: []byte(`{"reason":"envelope_violation"}`)}, runstream.Normalized{Stream: "status", Text: failure})
		if err != nil {
			return err
		}
	}
	if err = s.flush(guardCtx, spool, d.Assignment.Identity.AttemptID); err != nil {
		return err
	}
	boundary := r.CloseBoundary(guardCtx)
	if !boundary.Quiescent {
		return runner.ErrTerminationUnconfirmed
	}
	if err = s.upload(ctx, r, d, dir, checkout, spool, report, boundary, failure); err != nil {
		return err
	}
	if fullySpooled {
		if err = r.ReleaseHarness(ctx, lastSequence); err != nil {
			return err
		}
	}
	if err = os.RemoveAll(checkout.Path); err != nil {
		return err
	}
	if err = os.RemoveAll(filepath.Dir(launch.Workspace.RuntimeDir)); err != nil {
		return err
	}
	return writeJSON(s.out, map[string]any{"dispatch_id": d.ID, "outcome": map[bool]string{true: "succeeded", false: "failed"}[failure == ""], "reason": failure, "supported": false, "qualification": "development"})
}

// Native chunks stay below the 64KiB channel envelope after base64 framing.
// Concatenating their Data restores the exact native event bytes.
func appendHarnessEvent(spool *runstream.Spool, ev h.Event) (bool, error) {
	raw := []byte(ev.Raw)
	stream := "stdout"
	if ev.Native {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return false, err
		}
		raw = []byte(text)
		stream = "stderr"
	}
	if len(raw) == 0 {
		raw, _ = json.Marshal(map[string]string{"kind": ev.Kind})
	}
	text := strings.ToValidUTF8(ev.Summary, "\uFFFD")
	if len(text) > runstream.MaxText {
		text = text[:runstream.MaxText]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	for len(raw) > 0 {
		n := min(len(raw), 16<<10)
		stats := spool.Stats()
		// The spool itself reserves 25% for ACKs. Keep a further 8KiB
		// for a durable terminal status, never discard acknowledged bytes.
		if stats.UsedBytes+int64(n*2+len(text)+2048) > stats.LimitBytes*3/4-8192 {
			_, err := spool.Append(runstream.Native{Version: "harness-v1", Kind: "failed", Data: []byte(`{"reason":"spool_full"}`)}, runstream.Normalized{Stream: "status", Text: "spool_full"})
			return true, err
		}
		_, err := spool.Append(runstream.Native{Version: "harness-v1", Kind: ev.Kind, Data: raw[:n]}, runstream.Normalized{Stream: stream, Text: text})
		if err != nil {
			return false, err
		}
		raw = raw[n:]
		text = ""
	}
	return false, nil
}

type eventResult struct {
	event h.Event
	err   error
}

func lastNonce(r *runner.Runner) string {
	out := r.Outbox()
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Kind == "lease" && out[i].Acknowledged {
			var l w.LeaseEnvelope
			_ = json.Unmarshal(out[i].Body, &l)
			return l.Request.Nonce
		}
	}
	return ""
}
func (s *supervisor) localCancel(d store.Dispatch, cause string) p.Message {
	return p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "cancel", Identity: d.Assignment.Identity, StopID: w.LocalStopID(d.Assignment.Identity.AttemptID, cause), RunnerBoot: s.boot, DaemonBoot: s.session.DaemonBoot}
}

// Only an expired lease may claim the daemon's lease-clock stop identity.
func (s *supervisor) tickCancel(r *runner.Runner, d store.Dispatch, err error) (p.Message, string) {
	if errors.Is(err, runner.ErrLeaseExpired) {
		return s.expiryCancel(r, d), "lease_expired"
	}
	// Policy/grant/budget/clock invalidation all revoke local launch authority.
	return s.localCancel(d, "local_policy_drift"), "local_policy_drift"
}
func (s *supervisor) expiryCancel(r *runner.Runner, d store.Dispatch) p.Message {
	return p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "cancel", Identity: d.Assignment.Identity, StopID: w.ExpiryStopID(lastNonce(r)), RunnerBoot: s.boot, DaemonBoot: s.session.DaemonBoot}
}
func (s *supervisor) flush(ctx context.Context, spool *runstream.Spool, id string) error {
	for _, record := range spool.Pending() {
		reply, err := s.client.Stream(ctx, id, w.StreamBatch{Version: w.Version, Records: []runstream.Record{record}})
		if err != nil {
			return err
		}
		if reply.Through < record.Sequence || reply.Expected != reply.Through+1 {
			return runstream.ErrConflict
		}
		if err = spool.Acknowledge(reply.Through); err != nil {
			return err
		}
	}
	return nil
}
func (s *supervisor) cancelAttempt(ctx context.Context, r *runner.Runner, d store.Dispatch, phase p.AttemptState, revision int64, spool *runstream.Spool, cancel p.Message, cause string, drain func(context.Context) error) error {
	if err := r.Enqueue("cancel", cancel.MessageID, cancel); err != nil {
		return err
	}
	r.StopGuardian() // durable cancel must stop locally even if the daemon is unavailable
	_, err := s.transition(ctx, r, d, phase, p.Stopping, revision, w.Evidence{Kind: "stop", Nonce: lastNonce(r)}, cancel.StopID)
	if err != nil && !execclient.IsFence(err) && !isRefusal(err) {
		return err
	}
	runtime := r.Status().Runtime
	if runtime == nil {
		return runner.ErrTerminationUnconfirmed
	}
	// The existing journalled termination path is authoritative when the group is live.
	rec, terminationErr := r.TerminateProcessGroup(s.options().Session, cancel, runtime.PGID, runner.TerminationBounds{Grace: 2 * time.Second, Timeout: 5 * time.Second})
	r.StopGuardian()
	wait, stopWait := context.WithTimeout(context.Background(), 6*time.Second)
	defer stopWait()
	report, ge := r.WaitGuardian(wait)
	boundary := r.CloseBoundary(wait)
	if ge != nil || report.Escaped || !report.PGIDEmpty {
		measurement := rec.Measurement
		if measurement.RequestedAt.IsZero() {
			measurement = c.Measurement{RequestedAt: time.Now().UTC(), AcknowledgedAt: time.Now().UTC()}
		}
		measurement.ObservedAt = nil
		measurement.AckToObservedNS = nil
		_, sendErr := s.containment(ctx, r, d, cancel, "unknown", boundary, measurement, report, cause)
		return errors.Join(runner.ErrTerminationUnconfirmed, sendErr)
	}
	if err := drain(wait); err != nil {
		return err
	}
	var evidence cEvidence
	if terminationErr == nil && rec.Terminated != nil && (!report.Escalated || rec.Measurement.Escalated) {
		evidence = cEvidence{*rec.Terminated, rec.Measurement}
	} else {
		ev, e := r.GuardianEvidence(cancel)
		if e != nil {
			return e
		}
		evidence = cEvidence{ev.Terminated, ev.Measurement}
	}
	if boundary.Quiescent {
		evidence.message.RemoteWork = "quiescent"
	}
	reply, err := s.message(ctx, r, d.ID, evidence.message, nil, &boundary, &evidence.measurement)
	if err != nil {
		return err
	}
	return writeJSON(s.out, map[string]any{"dispatch_id": d.ID, "cause": cause, "confirmed_process": "terminated", "remote_work": evidence.message.RemoteWork, "released": reply.Released, "escalated": report.Escalated, "supported": false})
}

type cEvidence struct {
	message     p.Message
	measurement c.Measurement
}

// A failed local preparation/start is not permission to wait silently for lease
// lapse. Retain and send stop evidence using the daemon-validated local stop ID.
func (s *supervisor) failedLocal(r *runner.Runner, d store.Dispatch, phase p.AttemptState, revision int64, launchAttempted bool, stopCause string, cause error) error {
	ctx, cancelContext := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancelContext()
	cancel := s.localCancel(d, stopCause)
	if err := r.Enqueue("local_failure", cancel.MessageID+"/failure", map[string]any{"cause": cause.Error(), "launch_attempted": launchAttempted}); err != nil {
		return err
	}
	if launchAttempted {
		r.StopGuardian()
	}
	// A refused or lost response may hide a committed edge or owner stop. Use
	// authenticated state and the existing stop target rather than inventing one.
	alreadyStopping := false
	current, stateErr := s.client.State(ctx, d.ID)
	if stateErr == nil {
		if current.Released {
			return nil
		}
		phase, revision = current.AttemptState, current.Revision
		alreadyStopping = phase == p.Stopping || phase == p.Unknown
		for _, target := range current.StopTargets {
			if target.DispatchID == d.ID && target.Cancel.Identity == d.Assignment.Identity {
				cancel = target.Cancel
				break
			}
		}
	}
	if err := r.Enqueue("cancel", cancel.MessageID, cancel); err != nil {
		return err
	}
	var transitionErr error
	if !alreadyStopping {
		_, transitionErr = s.transition(ctx, r, d, phase, p.Stopping, revision, w.Evidence{Kind: "stop", Nonce: lastNonce(r)}, cancel.StopID)
	}
	now := time.Now().UTC()
	measurement := c.Measurement{RequestedAt: now, AcknowledgedAt: now}
	confirmed := "not_started"
	report := runner.GuardianReport{}
	if launchAttempted {
		confirmed = "unknown"
		r.StopGuardian()
		wait, stop := context.WithTimeout(ctx, 6*time.Second)
		rep, err := r.WaitGuardian(wait)
		stop()
		report = rep
		if err == nil && rep.PGIDEmpty && !rep.Escaped {
			if ev, e := r.GuardianEvidence(cancel); e == nil {
				confirmed = "terminated"
				measurement = ev.Measurement
			}
		}
	} else {
		elapsed := int64(0)
		measurement.ObservedAt = &now
		measurement.AckToObservedNS = &elapsed
	}
	boundary := r.CloseBoundary(ctx)
	_, err := s.containment(ctx, r, d, cancel, confirmed, boundary, measurement, report, cause.Error())
	if confirmed == "unknown" {
		return errors.Join(runner.ErrTerminationUnconfirmed, transitionErr, err)
	}
	return errors.Join(transitionErr, err)
}
func (s *supervisor) containment(ctx context.Context, r *runner.Runner, d store.Dispatch, cancel p.Message, confirmed string, boundary inference.State, measurement c.Measurement, report runner.GuardianReport, cause string) (execclient.MessageReply, error) {
	body, _ := json.Marshal(struct {
		Report      runner.GuardianReport
		Measurement c.Measurement
		Cause       string
	}{report, measurement, cause})
	sum := sha256.Sum256(body)
	remote := "unknown"
	if boundary.Quiescent {
		remote = "quiescent"
	}
	message := p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "terminated", Identity: cancel.Identity, StopID: cancel.StopID, RunnerBoot: cancel.RunnerBoot, DaemonBoot: cancel.DaemonBoot, ConfirmedProcess: confirmed, RemoteWork: remote, EvidenceDigest: hex.EncodeToString(sum[:])}
	return s.message(ctx, r, d.ID, message, nil, &boundary, &measurement)
}
