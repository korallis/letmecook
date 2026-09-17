// Package runner owns durable admission and explicitly development-only supervision.
package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	j "github.com/korallis/letmecook/internal/runnerjournal"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

var (
	ErrStopped           = errors.New("runner_reconciliation_required")
	ErrPolicy            = errors.New("runner_local_policy_refused")
	ErrExecutionDisabled = errors.New("runner_execution_unqualified")
)

// Session comes from authenticated control-plane reconciliation, never a wire
// assignment. Peer is the authenticated daemon credential fingerprint.
type Session struct {
	Version    string `json:"version"`
	Peer       string `json:"peer"`
	Generation string `json:"generation"`
	DaemonBoot string `json:"daemon_boot"`
	RunnerID   string `json:"runner_id"`
}
type Bounds struct {
	DriftMS       int64 `json:"drift_ms"`
	TerminationMS int64 `json:"termination_ms"`
}
type Options struct {
	Session Session
	Bounds  Bounds
	// Policy must independently load trusted runner-local facts. Never return
	// the daemon's supplied Facts here. Changes/expiry stop accepted metadata.
	Policy func() (sc.Eligibility, error)
	// MonotonicMS is one supervisor incarnation's trusted elapsed clock. Suspend
	// must be included or explicitly invalidate via Stop; no measured clock ships.
	MonotonicMS func() int64
	WallTime    func() time.Time
	// Admission is the supervisor's own operator-authorized development-profile
	// policy (gaffer-runner --isolation-profile). Accept rechecks the daemon's
	// admission against it; the zero value refuses every development profile.
	Admission sc.AdmissionPolicy
	// Boot, when set, is the supervisor process boot that Create records instead of
	// minting one, so every journal this process creates shares the boot it
	// published in its facts. Open ignores it: a reopen always mints a restart boot.
	Boot string
}

type event struct {
	Kind         string             `json:"kind"`
	Session      *Session           `json:"session,omitempty"`
	Bounds       *Bounds            `json:"bounds,omitempty"`
	Boot         string             `json:"boot,omitempty"`
	Message      *p.Message         `json:"message,omitempty"`
	Ack          *p.Message         `json:"ack,omitempty"`
	Input        *store.Dispatch    `json:"input,omitempty"`
	PolicyDigest string             `json:"policy_digest,omitempty"`
	At           int64              `json:"at"`
	Reason       string             `json:"reason,omitempty"`
	Termination  *TerminationRecord `json:"termination,omitempty"`
	Runtime      *LaunchRecord      `json:"runtime,omitempty"`
	Guardian     *GuardianReport    `json:"guardian,omitempty"`
	Outbox       *OutboxEntry       `json:"outbox,omitempty"`
	Key          string             `json:"key,omitempty"`
}
type state struct {
	Session      Session
	Bounds       Bounds
	Boot         string
	Assignment   *p.Message
	Ack          *p.Message
	Input        *store.Dispatch
	Pending      *p.Message
	PolicyDigest string
	StopBy       *int64
	AttemptBy    *int64
	Stopped      bool
	Reason       string
	Last         int64
	Messages     map[string]p.Message
	Termination  *TerminationRecord
	Runtime      *LaunchRecord
	Guardian     *GuardianReport
	Starting     bool
	Outbox       []OutboxEntry
}

type Runner struct {
	mu                      sync.Mutex
	log                     *j.Journal
	options                 Options
	state                   state
	failed                  bool
	prepared                *preparedLaunch
	guardian                *guardianLauncher
	beforeTerminationCommit func() // instance-local failure seam; nil in normal use
}
type Status struct {
	RunnerBoot   string
	AssignmentID string
	StopByMS     *int64
	StopRequired bool
	Quarantined  bool
	Reason       string
	// True only after a locally authorized development launch.
	ExecutionEnabled bool
	Termination      *TerminationRecord
	Runtime          *LaunchRecord
	Guardian         *GuardianReport
}

func id() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func validOptions(o Options) bool {
	return o.Policy != nil && o.MonotonicMS != nil && o.WallTime != nil && validConfiguration(o.Session, o.Bounds)
}
func validConfiguration(s Session, bounds Bounds) bool {
	if s.Version != p.FencedVersion || !p.ValidID(s.Generation) || !p.ValidID(s.DaemonBoot) || !p.ValidID(s.RunnerID) || len(s.Peer) != 64 {
		return false
	}
	for _, c := range s.Peer {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return bounds.DriftMS > 0 && bounds.TerminationMS > 0 && bounds.DriftMS < 30000 && bounds.TerminationMS < 30000 && bounds.DriftMS+bounds.TerminationMS < 30000
}

// Create requires a new private journal directory and explicit trusted inputs.
func Create(path string, o Options) (*Runner, error) {
	if !validOptions(o) || o.Boot != "" && !p.ValidID(o.Boot) {
		return nil, p.Malformed
	}
	boot := o.Boot
	if boot == "" {
		boot = id()
	}
	e := event{Kind: "init", Session: &o.Session, Bounds: &o.Bounds, Boot: boot, At: o.MonotonicMS()}
	s, err := reduce(state{}, e)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(e)
	log, err := j.Create(path, b)
	if err != nil {
		return nil, err
	}
	return &Runner{log: log, options: o, state: s}, nil
}

// Open never resumes. Every reopen persists a fresh supervisor boot and sticky
// quarantine before returning, even if the prior owner closed cleanly or had no
// process. Old deadlines and nonces are not usable in the new clock domain.
func Open(path string, o Options) (*Runner, error) {
	if !validOptions(o) {
		return nil, p.Malformed
	}
	log, err := j.Open(path)
	if err != nil {
		return nil, err
	}
	r := &Runner{log: log, options: o}
	for _, b := range log.Records() {
		var e event
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&e) != nil {
			log.Close()
			return nil, j.ErrUnavailable
		}
		canonical, _ := json.Marshal(e)
		if !bytes.Equal(canonical, b) {
			log.Close()
			return nil, j.ErrUnavailable
		}
		r.state, err = reduce(r.state, e)
		if err != nil {
			log.Close()
			return nil, err
		}
	}
	if r.state.Boot == "" {
		log.Close()
		return nil, j.ErrUnavailable
	}
	if err = r.recoverLaunch(); err != nil {
		log.Close()
		return nil, err
	}
	if err = r.commit(event{Kind: "restart", Boot: id(), At: o.MonotonicMS(), Reason: "supervisor_restart"}); err != nil {
		log.Close()
		return nil, err
	}
	return r, nil
}

func clone[T any](v T) T { b, _ := json.Marshal(v); var out T; _ = json.Unmarshal(b, &out); return out }

func reduce(old state, e event) (state, error) {
	// Events cross a caller ownership boundary. Retained pointers/slices must be
	// detached as well as the previous state; Go structs do not copy nested data.
	e = clone(e)
	s := clone(old)
	if e.At < 0 || e.At > p.MaxInteger {
		return old, p.Malformed
	}
	if e.Kind == "init" {
		if s.Boot != "" || e.Session == nil || e.Bounds == nil || !p.ValidID(e.Boot) {
			return old, p.Malformed
		}
		if !validConfiguration(*e.Session, *e.Bounds) {
			return old, p.Malformed
		}
		s.Session = *e.Session
		s.Bounds = *e.Bounds
		s.Boot = e.Boot
		s.Last = e.At
		s.Messages = make(map[string]p.Message)
		return s, nil
	}
	if s.Boot == "" {
		return old, p.Malformed
	}
	if handled, next, err := reduceRuntime(s, e); handled {
		return next, err
	}
	if e.Kind == "restart" {
		if !p.ValidID(e.Boot) || e.Boot == s.Boot {
			return old, p.BootMismatch
		}
		s.Boot = e.Boot
		s.Stopped = true
		s.Reason = "supervisor_restart"
		s.StopBy = nil
		s.AttemptBy = nil
		s.Pending = nil
		s.Last = e.At
		return s, nil
	}
	if e.Kind == "stop" {
		s.Stopped = true
		s.Reason = e.Reason
		s.Pending = nil
		s.StopBy = nil
		s.Last = e.At
		return s, nil
	}
	if e.Kind == "termination" {
		return reduceTermination(s, e)
	}
	if s.Stopped {
		return old, ErrStopped
	}
	if e.At < s.Last {
		return old, p.DelayedReply
	}
	s.Last = e.At
	for _, message := range []*p.Message{e.Message, e.Ack} {
		if message == nil {
			continue
		}
		if previous, exists := s.Messages[message.MessageID]; exists && !reflect.DeepEqual(previous, *message) {
			return old, p.IdentityConflict
		}
		s.Messages[message.MessageID] = clone(*message)
	}
	if e.Kind == "replay" {
		if s.Assignment == nil || e.Message == nil || p.CheckReplay(*e.Message, *s.Assignment, s.Assignment.Identity) != p.Duplicate {
			return old, p.IdentityConflict
		}
		return s, nil
	}
	if e.Kind == "accept" {
		if s.Assignment != nil || e.Message == nil || e.Ack == nil || e.Message.Kind != "assign" || e.Message.Identity.Generation != s.Session.Generation {
			return old, p.IdentityConflict
		}
		if p.CheckSession(*e.Message, e.Message.Identity, s.Session.Version) != p.OK || p.CheckSession(*e.Ack, e.Message.Identity, s.Session.Version) != p.OK {
			return old, p.Malformed
		}
		if e.Ack.Kind != "accept" || e.Ack.AssignmentID != e.Message.AssignmentID || e.Ack.RunnerBoot != s.Boot || e.Ack.DaemonBoot != s.Session.DaemonBoot || len(e.PolicyDigest) != 64 {
			return old, p.IdentityConflict
		}
		if e.Input == nil || !reflect.DeepEqual(e.Input.Assignment, *e.Message) {
			return old, p.IdentityConflict
		}
		inputHash, err := inputDigest(*e.Input)
		policyHash, pe := sc.Digest(e.Input.Facts)
		if err != nil || pe != nil || inputHash != e.Message.InputDigest || policyHash != e.PolicyDigest {
			return old, p.IdentityConflict
		}
		s.Assignment = e.Message
		s.Ack = e.Ack
		s.Input = e.Input
		s.PolicyDigest = e.PolicyDigest
		return s, nil
	}
	if s.Assignment == nil || e.Message == nil {
		return old, p.Malformed
	}
	if reason := p.CheckSession(*e.Message, s.Assignment.Identity, s.Session.Version); reason != p.OK {
		return old, reason
	}
	if e.Message.RunnerBoot != s.Boot || e.Message.DaemonBoot != s.Session.DaemonBoot {
		return old, p.BootMismatch
	}
	if s.StopBy != nil && e.At >= *s.StopBy {
		return old, p.DelayedReply
	}
	switch e.Kind {
	case "request":
		if s.Pending != nil || e.Message.Kind != "lease_request" || e.Message.SentMS == nil || *e.Message.SentMS != e.At {
			return old, p.NonceMismatch
		}
		s.Pending = e.Message
		if s.AttemptBy == nil {
			if s.Input == nil || s.Input.Allowance.AttemptMS < 1 || e.At > p.MaxInteger-s.Input.Allowance.AttemptMS {
				return old, p.Malformed
			}
			bound := e.At + s.Input.Allowance.AttemptMS
			s.AttemptBy = &bound
		}
	case "reply":
		if s.Pending == nil {
			return old, p.NonceMismatch
		}
		checked := p.CheckLease(*s.Pending, *e.Message, s.Assignment.Identity, p.Timing{RunnerBoot: s.Boot, DaemonBoot: s.Session.DaemonBoot, ReceivedMS: e.At, DriftMS: s.Bounds.DriftMS, TerminationMS: s.Bounds.TerminationMS, PriorStopByMS: s.StopBy, NonceActive: true})
		if checked.Reason != p.OK {
			return old, checked.Reason
		}
		s.StopBy = checked.StopByMS
		if s.AttemptBy != nil && *s.StopBy > *s.AttemptBy {
			cutoff := *s.AttemptBy
			s.StopBy = &cutoff
		}
		s.Pending = nil
	default:
		return old, p.Malformed
	}
	return s, nil
}

func (r *Runner) commit(e event) error {
	if r.failed || r.log == nil {
		return j.ErrUnavailable
	}
	s, err := reduce(r.state, e)
	if err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err = r.log.Append(b); err != nil {
		r.failed = true
		return err
	}
	r.state = s
	return nil
}
func (r *Runner) session(s Session) error {
	if r.failed || r.log == nil {
		return j.ErrUnavailable
	}
	if s.Version != p.FencedVersion {
		return p.UnknownVersion
	}
	if s.Generation != r.state.Session.Generation {
		return p.StaleGeneration
	}
	if s.DaemonBoot != r.state.Session.DaemonBoot {
		return p.BootMismatch
	}
	if s != r.state.Session {
		return ErrPolicy
	}
	return nil
}
func (r *Runner) stop(at int64, reason string) error {
	if r.state.Stopped {
		return ErrStopped
	}
	if at < 0 || at > p.MaxInteger {
		at = r.state.Last
	}
	if err := r.commit(event{Kind: "stop", At: at, Reason: reason}); err != nil {
		return err
	}
	return ErrStopped
}
func (r *Runner) check() (int64, error) {
	if r.failed || r.log == nil {
		return 0, j.ErrUnavailable
	}
	if r.state.Stopped {
		return 0, ErrStopped
	}
	if err := r.log.Check(); err != nil {
		r.failed = true
		return 0, err
	}
	now := r.options.MonotonicMS()
	if now < r.state.Last || now < 0 || now > p.MaxInteger {
		return now, r.stop(now, "clock_invalid")
	}
	if r.state.StopBy != nil && now >= *r.state.StopBy {
		return now, r.stop(now, "lease_expired")
	}
	if r.state.Assignment != nil {
		local, err := r.options.Policy()
		hash, he := sc.Digest(local)
		if err != nil || he != nil || local.Validate() != nil || !local.Enabled || local.ValidUntilMS <= r.options.WallTime().UnixMilli() || hash != r.state.PolicyDigest {
			return now, r.stop(now, "local_policy_changed")
		}
		wall := r.options.WallTime().UnixMilli()
		if r.state.Input == nil || wall < r.state.Input.Request.Envelope.NotBeforeMS || wall >= r.state.Input.Request.Envelope.ExpiresMS {
			return now, r.stop(now, "grant_expired")
		}
		if r.state.AttemptBy != nil && now >= *r.state.AttemptBy {
			return now, r.stop(now, "attempt_budget_expired")
		}
	}
	r.state.Last = now
	return now, nil
}

func inputDigest(dispatch store.Dispatch) (string, error) {
	input := struct {
		store.DispatchRequest
		Facts sc.Eligibility `json:"facts"`
	}{dispatch.DispatchRequest, dispatch.Facts}
	return sc.Digest(input)
}

// Accept consumes authenticated Delivery output; Assignment() history alone is
// not delivery authority. Local facts must be loaded independently through Policy.
// Success means durable acceptance only, not a lease or a launch permission.
func (r *Runner) Accept(session Session, dispatch store.Dispatch) (p.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.session(session); err != nil {
		return p.Message{}, err
	}
	now, err := r.check()
	if err != nil {
		return p.Message{}, err
	}
	m := dispatch.Assignment
	if m.Kind != "assign" {
		return p.Message{}, p.Malformed
	}
	if m.Identity.Generation != session.Generation {
		return p.Message{}, p.StaleGeneration
	}
	if reason := p.CheckSession(m, m.Identity, session.Version); reason != p.OK {
		return p.Message{}, reason
	}
	local, err := r.options.Policy()
	if err != nil {
		return p.Message{}, ErrPolicy
	}
	if !reflect.DeepEqual(local, dispatch.Facts) || local.RunnerBoot != r.state.Boot || local.Repository.RunnerRoot.RunnerID != session.RunnerID {
		return p.Message{}, ErrPolicy
	}
	// The supervisor's own policy decides whether a development profile the daemon
	// admitted is acceptable here; a mismatch refuses rather than trusting the daemon.
	if err = sc.CheckDispatchWithPolicy(dispatch.Request, dispatch.Decision, local, r.options.WallTime().UnixMilli(), r.options.Admission); err != nil {
		return p.Message{}, err
	}
	capacity := local.Capacity.Values()
	for i, requested := range dispatch.Decision.Resources.Values() {
		if requested > capacity[i] {
			return p.Message{}, ErrPolicy
		}
	}
	if dispatch.Released {
		return p.Message{}, ErrPolicy
	}
	wall := r.options.WallTime().UnixMilli()
	if wall < dispatch.Request.Envelope.NotBeforeMS || wall >= dispatch.Request.Envelope.ExpiresMS {
		return p.Message{}, ErrPolicy
	}
	if !p.ValidID(dispatch.ID) || !p.ValidID(dispatch.Request.GrantID) || dispatch.Request.GrantRevision < 1 || dispatch.Request.Action != "execute" {
		return p.Message{}, p.Malformed
	}
	bounded := dispatch.Request.Envelope
	bounded.Budgets = dispatch.Allowance
	if err := g.Within(bounded, dispatch.Request.Envelope); err != nil {
		return p.Message{}, err
	}
	if dispatch.Allowance.Attempts != 1 || dispatch.Allowance.Retries != 0 || dispatch.Allowance.Concurrency != 1 || dispatch.Allowance.TotalMS != dispatch.Allowance.AttemptMS {
		return p.Message{}, ErrPolicy
	}
	hash, err := inputDigest(dispatch)
	if err != nil || m.InputDigest != hash || m.Identity.TaskID != dispatch.Request.TaskID {
		return p.Message{}, p.IdentityConflict
	}
	want := p.Route{RouteRef: dispatch.Decision.Selected.RouteRef, DecisionDigest: dispatch.Request.Envelope.RouteDecision.SHA256, PolicyDigest: dispatch.Decision.Selected.Policy.SHA256, LimitsProfile: dispatch.Decision.Selected.LimitsProfile}
	if m.Route == nil || *m.Route != want {
		return p.Message{}, p.IdentityConflict
	}
	if r.state.Assignment != nil {
		if reason := p.CheckReplay(m, *r.state.Assignment, r.state.Assignment.Identity); reason != p.Duplicate {
			return p.Message{}, p.IdentityConflict
		}
		if previous, seen := r.state.Messages[m.MessageID]; seen {
			if !reflect.DeepEqual(previous, m) {
				return p.Message{}, p.IdentityConflict
			}
		} else if err := r.commit(event{Kind: "replay", At: now, Message: &m}); err != nil {
			return p.Message{}, err
		}
		return clone(*r.state.Ack), nil
	}
	ack := p.Message{Version: p.FencedVersion, MessageID: id(), Kind: "accept", Identity: m.Identity, AssignmentID: m.AssignmentID, RunnerBoot: r.state.Boot, DaemonBoot: session.DaemonBoot}
	policyDigest, _ := sc.Digest(local)
	if err = r.commit(event{Kind: "accept", At: now, Message: &m, Ack: &ack, Input: &dispatch, PolicyDigest: policyDigest}); err != nil {
		return p.Message{}, err
	}
	return clone(ack), nil
}

// RequestLease journals the nonce and send time before returning a request.
// Retrying an outstanding request returns its original time and nonce.
func (r *Runner) RequestLease(session Session) (p.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.session(session); err != nil {
		return p.Message{}, err
	}
	now, err := r.check()
	if err != nil {
		return p.Message{}, err
	}
	if r.state.Assignment == nil {
		return p.Message{}, p.StaleAttempt
	}
	if r.state.Pending != nil {
		return clone(*r.state.Pending), nil
	}
	m := p.Message{Version: p.FencedVersion, MessageID: id(), Kind: "lease_request", Identity: r.state.Assignment.Identity, Nonce: id(), RunnerBoot: r.state.Boot, DaemonBoot: session.DaemonBoot, SentMS: &now}
	if err = r.commit(event{Kind: "request", At: now, Message: &m}); err != nil {
		return p.Message{}, err
	}
	return clone(m), nil
}

func (r *Runner) ApplyLease(session Session, wire []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.session(session); err != nil {
		return err
	}
	now, err := r.check()
	if err != nil {
		return err
	}
	m, err := p.Decode(wire)
	if err != nil {
		return err
	}
	if r.state.Assignment == nil {
		return p.StaleAttempt
	}
	if m.Kind != "lease_reply" {
		return p.Malformed
	}
	err = r.commit(event{Kind: "reply", At: now, Message: &m})
	if errors.Is(err, p.DelayedReply) {
		return r.stop(now, "delayed_lease")
	}
	if err == nil {
		_, err = r.check()
	}
	return err
}

// Tick checks metadata expiry only. It is not a scheduled or independent watchdog.
// Any actual supervisor must enforce the installed cutoff outside untrusted jobs.
func (r *Runner) Tick() (Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.check()
	return r.status(), err
}
func (r *Runner) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed || r.log == nil {
		return j.ErrUnavailable
	}
	return r.stop(r.options.MonotonicMS(), "operator_stop")
}
func (r *Runner) status() Status {
	s := Status{Runtime: clone(r.state.Runtime), Guardian: clone(r.state.Guardian), ExecutionEnabled: !r.state.Stopped && !r.failed && r.state.Runtime != nil && r.state.Runtime.PID > 0 && r.state.Guardian == nil, RunnerBoot: r.state.Boot, StopRequired: r.state.Stopped || r.failed, Quarantined: r.state.Stopped || r.failed, Reason: r.state.Reason}
	if r.state.Termination != nil {
		v := clone(*r.state.Termination)
		s.Termination = &v
	}
	if r.failed {
		s.Reason = "journal_unavailable"
	}
	if r.state.Assignment != nil {
		s.AssignmentID = r.state.Assignment.AssignmentID
	}
	if r.state.StopBy != nil {
		v := *r.state.StopBy
		s.StopByMS = &v
	}
	return s
}
func (r *Runner) Status() Status { r.mu.Lock(); defer r.mu.Unlock(); return r.status() }
func (r *Runner) Close() error {
	r.StopGuardian()
	// Revoke the scoped boundary on every error path while its receipt callbacks
	// can still journal terminal accounting. Close never implies quiescence.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	r.CloseBoundary(ctx)
	r.mu.Lock()
	through, releasable := r.releasableHarness()
	r.mu.Unlock()
	if releasable {
		_ = r.ReleaseHarness(ctx, through)
	}
	cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = true
	if r.log == nil {
		return nil
	}
	err := r.log.Close()
	r.log = nil
	return err
}
