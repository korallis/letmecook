package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/korallis/letmecook/internal/closedjson"
	c "github.com/korallis/letmecook/internal/control"
	h "github.com/korallis/letmecook/internal/harness"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/isolation"
	"github.com/korallis/letmecook/internal/repositories"
	p "github.com/korallis/letmecook/schemas/execution"
)

type LaunchRequest struct {
	Profile                        isolation.Profile
	Harness                        h.Harness
	Boundary                       inference.Scope
	Checkout                       repositories.Checkout
	Executable, RecoveryDir, Brief string
	Settings                       json.RawMessage
}
type LaunchRecord struct {
	BoundaryPort           int                 `json:"boundary_port"`
	Workspace              isolation.Workspace `json:"workspace"`
	ReceiptPath            string              `json:"receipt_path"`
	Profile                string              `json:"profile"`
	Qualification          string              `json:"qualification"`
	Supported              bool                `json:"supported"`
	PID, PGID, GuardianPID int
	StartUnixNS            int64
	StartToken             string
	Observation            isolation.Observation `json:"observation"`
}
type OutboxEntry struct {
	Key, Kind    string
	Body         json.RawMessage
	Acknowledged bool
}
type preparedLaunch struct {
	req              LaunchRequest
	record           LaunchRecord
	launcher         isolation.Launcher
	boundary         inference.Boundary
	receipts         *receiptJournal
	handle           h.RunHandle
	releaseAttempted bool
	releaseError     error
}

func reduceRuntime(s state, e event) (bool, state, error) {
	switch e.Kind {
	case "launch_intent", "starting_ack", "launched", "guardian_observed", "outbox", "outbox_ack", "fenced":
		if e.At < s.Last {
			return true, s, p.DelayedReply
		}
	}
	switch e.Kind {
	case "launch_intent":
		if s.Stopped || s.Assignment == nil || s.Runtime != nil || e.Runtime == nil || e.Runtime.Qualification == isolation.QualificationUnqualified || e.Runtime.Supported {
			return true, s, p.Malformed
		}
		s.Runtime = e.Runtime
	case "starting_ack":
		if s.Stopped || s.Assignment == nil || e.Message == nil || e.Message.To != p.Starting || e.Message.Identity != s.Assignment.Identity {
			return true, s, p.Malformed
		}
		s.Starting = true
	case "launched":
		if s.Stopped || !s.Starting || s.Runtime == nil || e.Runtime == nil || e.Runtime.PID <= 1 || e.Runtime.PGID <= 1 || e.Runtime.GuardianPID <= 1 || e.Runtime.StartUnixNS <= 0 {
			return true, s, p.Malformed
		}
		s.Runtime = e.Runtime
	case "guardian_observed":
		if s.Runtime == nil || e.Guardian == nil || e.Guardian.PID <= 1 {
			return true, s, p.Malformed
		}
		s.Guardian = e.Guardian
	case "outbox":
		if e.Outbox == nil || e.Outbox.Key == "" || !json.Valid(e.Outbox.Body) {
			return true, s, p.Malformed
		}
		for _, old := range s.Outbox {
			if old.Key == e.Outbox.Key {
				if old.Kind != e.Outbox.Kind || !reflect.DeepEqual(old.Body, e.Outbox.Body) {
					return true, s, p.IdentityConflict
				}
				return true, s, nil
			}
		}
		s.Outbox = append(s.Outbox, *e.Outbox)
	case "outbox_ack":
		found := false
		for i := range s.Outbox {
			if s.Outbox[i].Key == e.Key {
				s.Outbox[i].Acknowledged = true
				found = true
			}
		}
		if !found {
			return true, s, p.Malformed
		}
	case "fenced":
		s.Stopped = true
		s.Reason = e.Reason
		s.StopBy = nil
	default:
		return false, s, nil
	}
	s.Last = e.At
	return true, s, nil
}
func (r *Runner) Enqueue(kind, key string, body any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return r.commit(event{Kind: "outbox", At: r.options.MonotonicMS(), Outbox: &OutboxEntry{Key: key, Kind: kind, Body: b}})
}
func (r *Runner) Acknowledge(key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commit(event{Kind: "outbox_ack", At: r.options.MonotonicMS(), Key: key})
}
func (r *Runner) Outbox() []OutboxEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return clone(r.state.Outbox)
}
func (r *Runner) Fence(reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commit(event{Kind: "fenced", At: r.options.MonotonicMS(), Reason: reason})
}
func (r *Runner) StartingAcknowledged(m p.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commit(event{Kind: "starting_ack", At: r.options.MonotonicMS(), Message: &m})
}

// PrepareLaunch starts the credential boundary but does not launch repository code.
// All runtime files are siblings of the candidate, outside Pack's root.
func (r *Runner) PrepareLaunch(ctx context.Context, req LaunchRequest) (LaunchRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.check(); err != nil {
		return LaunchRecord{}, err
	}
	if r.state.StopBy == nil || req.Profile == nil || req.Harness == nil || req.Profile.Qualification() != isolation.QualificationDevelopment || !r.options.Admission.Development(req.Profile.ID()) {
		return LaunchRecord{}, ErrExecutionDisabled
	}
	if r.prepared != nil {
		return r.prepared.record, nil
	}
	if r.state.Input == nil || req.Profile.ID() != r.state.Input.Decision.Selected.Isolation || req.Checkout.BaseCommit != r.state.Input.Facts.Repository.BaseCommit || req.Checkout.ProfileDigest != r.state.Input.Facts.Repository.ProfileDigest {
		return LaunchRecord{}, ErrPolicy
	}
	if !filepath.IsAbs(req.Executable) || !filepath.IsAbs(req.RecoveryDir) {
		return LaunchRecord{}, ErrPolicy
	}
	root, err := filepath.EvalSymlinks(req.Checkout.Path)
	if err != nil || root != req.Checkout.Path {
		return LaunchRecord{}, ErrPolicy
	}
	parent := filepath.Dir(root)
	private, err := os.MkdirTemp(parent, "gaffer-runtime-")
	if err != nil {
		return LaunchRecord{}, err
	}
	w := isolation.Workspace{Root: root, PrivateHome: filepath.Join(private, "home"), TempDir: filepath.Join(private, "tmp"), RuntimeDir: filepath.Join(private, "runtime")}
	for _, path := range []string{w.PrivateHome, w.TempDir, w.RuntimeDir} {
		if err := os.Mkdir(path, 0700); err != nil {
			return LaunchRecord{}, err
		}
	}
	desc, err := req.Harness.Describe(ctx)
	if err != nil {
		return LaunchRecord{}, err
	}
	if desc.Name != r.state.Input.Decision.Selected.Harness {
		return LaunchRecord{}, ErrPolicy
	}
	sink := &receiptJournal{runner: r}
	// Fake never makes inference requests, including once S3 replaces the stub.
	// Authentication remains a facts/admission requirement, not a fake boundary.
	var boundary inference.Boundary
	if desc.Name != "fake" {
		boundary, err = inference.Start(ctx, req.Boundary, sink)
		if err != nil {
			return LaunchRecord{}, err
		}
		if boundary == nil {
			return LaunchRecord{}, errors.New("inference boundary missing")
		}
	}
	if boundary != nil {
		if configurable, ok := req.Profile.(interface {
			WithBoundary(string) isolation.Profile
		}); ok {
			req.Profile = configurable.WithBoundary(boundary.Addr())
		} else {
			boundary.Close(ctx)
			return LaunchRecord{}, errors.New("profile cannot pin boundary")
		}
	}
	launcher, err := req.Profile.Prepare(ctx, w)
	if err != nil {
		if boundary != nil {
			boundary.Close(ctx)
		}
		return LaunchRecord{}, err
	}
	port := 0
	if boundary != nil {
		_, p, e := net.SplitHostPort(boundary.Addr())
		if e != nil {
			return LaunchRecord{}, e
		}
		port, e = strconv.Atoi(p)
		if e != nil {
			return LaunchRecord{}, e
		}
	}
	rec := LaunchRecord{BoundaryPort: port, Workspace: w, ReceiptPath: filepath.Join(req.RecoveryDir, "guardian.json"), Profile: req.Profile.ID(), Qualification: req.Profile.Qualification(), Supported: false, Observation: launcher.Observation()}
	if err = r.commit(event{Kind: "launch_intent", At: r.options.MonotonicMS(), Runtime: &rec}); err != nil {
		launcher.Cleanup()
		return LaunchRecord{}, err
	}
	r.prepared = &preparedLaunch{req: req, record: rec, launcher: launcher, boundary: boundary, receipts: sink}
	return rec, nil
}

// Launch requires acknowledged starting, durable intent, a current lease and the
// independently admitted development profile. The zero/unqualified request refuses.
func (r *Runner) Launch(ctx context.Context, req LaunchRequest) error {
	if req.Profile == nil || req.Harness == nil || req.Profile.Qualification() == isolation.QualificationUnqualified {
		return ErrExecutionDisabled
	}
	r.mu.Lock()
	if _, err := r.check(); err != nil {
		r.mu.Unlock()
		return err
	}
	if r.state.StopBy == nil || !r.state.Starting || req.Profile == nil || req.Harness == nil || r.prepared == nil {
		r.mu.Unlock()
		return ErrExecutionDisabled
	}
	prep := r.prepared
	if req.Profile.ID() != prep.req.Profile.ID() || req.Checkout != prep.req.Checkout || r.state.Runtime.PID != 0 {
		r.mu.Unlock()
		return ErrPolicy
	}
	remaining := *r.state.StopBy - r.options.MonotonicMS()
	limits := ResourceLimits{OpenFiles: 1024, FileBytes: 64 << 20, CPUSeconds: uint64((r.state.Input.Allowance.AttemptMS+999)/1000) + 5}
	gl := &guardianLauncher{inner: prep.launcher, executable: prep.req.Executable, receipt: prep.record.ReceiptPath, deadline: func() int64 { return remaining }, limits: limits}
	r.guardian = gl
	in := r.state.Input
	assignment := *r.state.Assignment
	request := h.RunRequest{Identity: assignment.Identity, AssignmentID: assignment.AssignmentID, InputDigest: assignment.InputDigest, BaseCommit: in.Facts.Repository.BaseCommit, ContextDigest: in.Decision.Assessment.ContextDigest, Role: in.Decision.Assessment.Role, Workspace: prep.record.Workspace, Route: in.Decision.Selected, Brief: prep.req.Brief, Settings: prep.req.Settings, Limits: h.Limits{AttemptMS: in.Allowance.AttemptMS, IdleMS: in.Allowance.IdleMS, FirstOutputMS: in.Allowance.FirstOutputMS, MaxStdoutBytes: 4 << 20}, Launcher: gl}
	if prep.boundary != nil {
		request.Boundary = h.BoundaryHandle{URL: "http://" + strings.TrimPrefix(prep.boundary.Addr(), "http://"), Token: prep.req.Boundary.Token, Models: prep.req.Boundary.Models, Protocol: in.Decision.Selected.Protocol}
	}
	r.mu.Unlock()
	handle, err := prep.req.Harness.Start(ctx, request)
	if err != nil {
		gl.CloseInput()
		return err
	}
	started, err := gl.startedHandle(ctx)
	if err != nil {
		gl.CloseInput()
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := prep.record
	rec.PID = started.PID
	rec.PGID = started.PGID
	rec.GuardianPID = handle.PID
	rec.StartUnixNS = started.StartUnixNS
	rec.StartToken = started.StartToken
	if started.BoundedResources {
		rec.Observation.Controls = append(rec.Observation.Controls, "bounded-resources")
	}
	if err = r.commit(event{Kind: "launched", At: r.options.MonotonicMS(), Runtime: &rec}); err != nil {
		gl.CloseInput()
		return err
	}
	handle.PID = started.PID
	handle.PGID = started.PGID
	handle.GuardianPID = rec.GuardianPID
	handle.StartUnixNS = started.StartUnixNS
	prep.handle = handle
	prep.record = rec
	return nil
}
func (r *Runner) Events(ctx context.Context) (h.EventStream, error) {
	r.mu.Lock()
	prep := r.prepared
	r.mu.Unlock()
	if prep == nil {
		return nil, ErrExecutionDisabled
	}
	return prep.req.Harness.Events(ctx, prep.handle, 0)
}
func (r *Runner) RenewGuardian() error {
	r.mu.Lock()
	g := r.guardian
	var remain int64
	if r.state.StopBy != nil {
		remain = *r.state.StopBy - r.options.MonotonicMS()
	}
	r.mu.Unlock()
	if g == nil {
		return nil
	}
	return g.Renew(max(0, remain))
}
func (r *Runner) BoundaryState() inference.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.prepared == nil || r.prepared.boundary == nil {
		return inference.State{Quiescent: true}
	}
	return r.prepared.boundary.State()
}
func (r *Runner) CloseBoundary(ctx context.Context) inference.State {
	r.mu.Lock()
	prep := r.prepared
	r.mu.Unlock()
	if prep == nil || prep.boundary == nil {
		return inference.State{Quiescent: true}
	}
	return prep.boundary.Close(ctx)
}
func (r *Runner) WaitGuardian(ctx context.Context) (GuardianReport, error) {
	r.mu.Lock()
	g := r.guardian
	r.mu.Unlock()
	if g == nil {
		return GuardianReport{}, ErrExecutionDisabled
	}
	select {
	case <-ctx.Done():
		return GuardianReport{}, ctx.Err()
	case <-g.done:
	}
	g.mu.Lock()
	rep := g.report
	g.mu.Unlock()
	if rep.PID <= 1 {
		return rep, ErrTerminationUnconfirmed
	}
	r.mu.Lock()
	err := r.commit(event{Kind: "guardian_observed", At: r.options.MonotonicMS(), Guardian: &rep})
	r.mu.Unlock()
	return rep, err
}
func (r *Runner) StopGuardian() {
	r.mu.Lock()
	g := r.guardian
	r.mu.Unlock()
	if g != nil {
		g.CloseInput()
	}
}
func (r *Runner) recoverLaunch() error {
	if r.state.Runtime == nil || r.state.Guardian != nil {
		return nil
	}
	rec := r.state.Runtime
	deadline := time.Now().Add(6 * time.Second)
	for {
		b, err := os.ReadFile(rec.ReceiptPath)
		var rep GuardianReport
		if err == nil && closedjson.Decode(b, &rep, 65536, nil) == nil && rep.PID > 1 && (rec.PID == 0 || rep.PID == rec.PID && rep.StartUnixNS == rec.StartUnixNS) {
			return r.commit(event{Kind: "guardian_observed", At: max(r.state.Last, r.options.MonotonicMS()), Guardian: &rep})
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	// Never signal a reused PID, nor infer tree termination from a missing leader.
	if rec.PID > 1 && rec.StartToken != "" && processToken(rec.PID) == rec.StartToken {
		_, _ = terminateGroup(rec.PGID, 2*time.Second, time.Now().Add(5*time.Second))
	}
	return nil
}
func (r *Runner) GuardianEvidence(cancel p.Message) (c.Evidence, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rep := r.state.Guardian
	if rep == nil || !rep.PGIDEmpty || rep.Escaped || rep.ObservedUnixNS <= 0 || rep.StopUnixNS <= 0 || rep.StopToObservedNS < 0 {
		return c.Evidence{}, ErrTerminationUnconfirmed
	}
	observed := time.Unix(0, rep.ObservedUnixNS).UTC()
	start := time.Unix(0, rep.StopUnixNS).UTC()
	elapsed := rep.StopToObservedNS
	b, _ := json.Marshal(rep)
	sum := sha256.Sum256(b)
	m := p.Message{Version: p.FencedVersion, MessageID: id(), Identity: cancel.Identity, Kind: "terminated", StopID: cancel.StopID, RunnerBoot: cancel.RunnerBoot, DaemonBoot: cancel.DaemonBoot, ConfirmedProcess: "terminated", RemoteWork: "unknown", EvidenceDigest: hex.EncodeToString(sum[:])}
	return c.Evidence{Terminated: m, Measurement: c.Measurement{RequestedAt: start, AcknowledgedAt: start, ObservedAt: &observed, AckToObservedNS: &elapsed, Escalated: rep.Escalated}}, nil
}

type receiptJournal struct {
	runner *Runner
	mu     sync.Mutex
	rows   []inference.Receipt
}

func (s *receiptJournal) Reserve(ctx context.Context, receipt inference.Receipt) error {
	if err := s.runner.Enqueue("usage-reserve", receipt.RequestID+"/reserve", receipt); err != nil {
		return err
	}
	s.mu.Lock()
	s.rows = append(s.rows, receipt)
	s.mu.Unlock()
	return nil
}
func (s *receiptJournal) Complete(ctx context.Context, receipt inference.Receipt) error {
	if err := s.runner.Enqueue("usage-complete", receipt.RequestID+"/complete", receipt); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rows {
		if s.rows[i].RequestID == receipt.RequestID {
			s.rows[i] = receipt
			return nil
		}
	}
	return errors.New("usage without reservation")
}
func (r *Runner) Usage() []inference.Receipt {
	r.mu.Lock()
	prep := r.prepared
	r.mu.Unlock()
	if prep == nil {
		return []inference.Receipt{}
	}
	prep.receipts.mu.Lock()
	defer prep.receipts.mu.Unlock()
	return clone(prep.receipts.rows)
}

// RecoveredBoundaryState derives conservative remote-work evidence from durable
// receipt reservations, never from a dead supervisor's vanished HTTP connections.
func (r *Runner) RecoveredBoundaryState() inference.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	reserved := map[string]bool{}
	terminal := map[string]bool{}
	for _, e := range r.state.Outbox {
		if e.Kind != "usage-reserve" && e.Kind != "usage-complete" {
			continue
		}
		var receipt inference.Receipt
		if json.Unmarshal(e.Body, &receipt) != nil {
			continue
		}
		if e.Kind == "usage-reserve" {
			reserved[receipt.RequestID] = true
		}
		if e.Kind == "usage-complete" && receipt.Terminal {
			terminal[receipt.RequestID] = true
		}
	}
	state := inference.State{Reservations: int64(len(reserved))}
	for id := range reserved {
		if terminal[id] {
			state.TerminalReceipts++
		}
	}
	state.InFlight = state.Reservations - state.TerminalReceipts
	state.Quiescent = state.InFlight == 0
	return state
}

// RecordHarnessWatermark binds the last complete harness event to an already
// acknowledged spool. It is not itself permission to discard adapter memory.
func (r *Runner) RecordHarnessWatermark(through int64) error {
	if through < 0 {
		return p.Malformed
	}
	return r.Enqueue("harness-spooled", "harness-spooled", struct {
		Through int64 `json:"through"`
	}{through})
}
func (r *Runner) releasableHarness() (int64, bool) {
	terminal, spooled := false, false
	var through int64
	for _, entry := range r.state.Outbox {
		if entry.Kind == "harness-spooled" {
			var v struct {
				Through int64 `json:"through"`
			}
			if json.Unmarshal(entry.Body, &v) == nil {
				through = v.Through
				spooled = true
			}
		}
		if !entry.Acknowledged {
			continue
		}
		if strings.HasPrefix(entry.Kind, "finalize:") {
			terminal = true
		}
		if entry.Kind == "message" {
			var v struct {
				Message p.Message `json:"message"`
			}
			if json.Unmarshal(entry.Body, &v) == nil && v.Message.Kind == "terminated" {
				terminal = true
			}
		}
	}
	return through, terminal && spooled && r.state.Guardian != nil && r.state.Guardian.PGIDEmpty && !r.state.Guardian.Escaped
}

// ReleaseHarness calls an adapter's optional release exactly once, only after
// complete spooling and an acknowledged terminal outcome. Missing interfaces
// are harmless; incomplete output or recovery stays retained and fail-closed.
func (r *Runner) ReleaseHarness(ctx context.Context, through int64) error {
	r.mu.Lock()
	prep := r.prepared
	if prep == nil {
		r.mu.Unlock()
		return nil
	}
	releaser, ok := prep.req.Harness.(interface {
		Release(context.Context, h.RunHandle, int64) error
	})
	if !ok {
		r.mu.Unlock()
		return nil
	}
	if prep.releaseAttempted {
		err := prep.releaseError
		r.mu.Unlock()
		return err
	}
	watermark, ready := r.releasableHarness()
	if !ready || watermark != through {
		r.mu.Unlock()
		return ErrStopped
	}
	prep.releaseAttempted = true
	r.mu.Unlock()
	err := releaser.Release(ctx, prep.handle, through)
	r.mu.Lock()
	prep.releaseError = err
	r.mu.Unlock()
	return err
}
