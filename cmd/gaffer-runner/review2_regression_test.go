package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/execclient"
	w "github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/runner"
	p "github.com/korallis/letmecook/schemas/execution"
)

// Inverts probe_replay_test: an obsolete running proposal is a retained terminal
// refusal, not an instruction to abort every subsequent serve incarnation.
func TestRefusedReplayDoesNotPoisonRestart(t *testing.T) {
	f := newFixture(t, "hang")
	f.d.blockRunning = true
	f.d.runningWaiting = make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- f.s.attempt(f.ctx, f.dispatch) }()
	select {
	case <-f.d.runningWaiting:
	case <-time.After(8 * time.Second):
		t.Fatal("running gap")
	}
	f.cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown timeout")
	}
	journal := filepath.Join(f.s.cfg.StateDir, "attempts", f.dispatch.ID, "journal")
	f.s.boot = uuid()
	r, err := runner.Open(journal, f.s.options())
	if err != nil {
		t.Fatal(err)
	}
	var key string
	var original []byte
	for _, e := range r.Outbox() {
		if e.Kind == "message" && !e.Acknowledged {
			var env w.MessageEnvelope
			json.Unmarshal(e.Body, &env)
			if env.Message.To == p.Running {
				key = e.Key
				original = append([]byte(nil), e.Body...)
			}
		}
	}
	if key == "" {
		t.Fatal("no lost running proposal")
	}
	_, err = f.s.client.Hello(context.Background(), w.Hello{Version: w.Version, MessageID: uuid(), RunnerBoot: f.s.boot, EligibilityID: f.local.ID, EligibilityRevision: 1, PolicyDigest: strings.Repeat("a", 64), Journals: []w.Journal{}})
	if err != nil {
		t.Fatal(err)
	}
	// A later valid message must still be replayed after the permanently refused one.
	var term w.MessageEnvelope
	f.d.mu.Lock()
	term = *f.d.termination
	f.d.mu.Unlock()
	term.MessageID = uuid()
	term.Message.MessageID = term.MessageID
	if err := r.Enqueue("message", term.MessageID, term); err != nil {
		t.Fatal(err)
	}
	if err := f.s.replay(context.Background(), r); err != nil {
		t.Fatal("refusal aborted replay", err)
	}
	e := outboxEntry(r, key)
	if e.Refused == "" || e.Acknowledged || !bytes.Equal(e.Body, original) {
		t.Fatalf("lost refusal/bytes: %+v", e)
	}
	if !outboxEntry(r, term.MessageID).Acknowledged {
		t.Fatal("later replay skipped")
	}
	r.Close()
	proc := startServeBinary(t, f, false)
	// Let two recovery-only session polls prove serve continued rather than exit 1.
	deadline := time.Now().Add(4 * time.Second)
	for {
		f.d.mu.Lock()
		count := len(f.d.hellos)
		f.d.mu.Unlock()
		if count >= 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("serve did not survive replay", proc.stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := proc.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("serve exited", err, proc.stderr.String())
	}
	proc.kill()
	if !journalHas(journal, "outbox_refused", key) {
		t.Fatal("terminal refusal not durable")
	}
}

// Inverts probe_cause_test and checks each typed Tick cause's wire identity.
func TestLocalTickCauseIdentities(t *testing.T) {
	f := newFixture(t, "hang")
	for _, err := range []error{runner.ErrPolicyChanged, runner.ErrGrantExpired, runner.ErrAttemptBudgetExpired, runner.ErrClockInvalid} {
		cancel, cause := f.s.tickCancel(nil, f.d.dispatch, err)
		if cause != "local_policy_drift" || cancel.StopID != w.LocalStopID(f.dispatch.Assignment.Identity.AttemptID, cause) {
			t.Fatalf("%v impersonated expiry", err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- f.s.attempt(f.ctx, f.dispatch) }()
	waitState(t, f, p.Running)
	changed := f.local
	changed.ValidUntilMS = time.Now().UnixMilli() - 1
	if err := runner.DurableFile(f.s.cfg.Policy, changed); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("policy stop timeout")
	}
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	if f.d.termination == nil || f.d.termination.Message.StopID != w.LocalStopID(f.dispatch.Assignment.Identity.AttemptID, "local_policy_drift") {
		t.Fatal("policy drift misreported")
	}
	found := false
	for _, b := range f.d.request {
		var env w.MessageEnvelope
		if json.Unmarshal(b, &env) == nil && env.Message.To == p.Stopping {
			found = env.Evidence != nil && env.Evidence.Cause == "local_policy_drift"
		}
	}
	if !found {
		t.Fatal("stopping evidence omitted cause")
	}
}

// Inverts probe_latched_test with explicit fixture refusal knobs.
func TestRefusedResultEdgeStillReportsTermination(t *testing.T) {
	for _, code := range []string{"stop_latched", "revision_conflict"} {
		t.Run(code, func(t *testing.T) {
			f := newFixture(t, "edit")
			f.d.resultRefusal = code
			cancel := p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "cancel", Identity: f.d.dispatch.Assignment.Identity, StopID: uuid(), RunnerBoot: f.s.boot, DaemonBoot: f.s.session.DaemonBoot}
			f.d.cancel = &cancel
			err := f.s.attempt(f.ctx, f.dispatch)
			var remote *execclient.Error
			if !errors.As(err, &remote) || remote.Code != code {
				t.Fatalf("refusal not returned: %v", err)
			}
			f.d.mu.Lock()
			term := f.d.termination
			released := f.d.released
			f.d.mu.Unlock()
			if term == nil || term.Message.StopID != cancel.StopID || term.Message.ConfirmedProcess != "terminated" || !released {
				t.Fatalf("refused result stranded exit evidence: %+v released=%v", term, released)
			}
			r, err := runner.Open(filepath.Join(f.s.cfg.StateDir, "attempts", f.dispatch.ID, "journal"), f.s.options())
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			found := false
			for _, e := range r.Outbox() {
				var env w.MessageEnvelope
				if e.Kind == "message" && json.Unmarshal(e.Body, &env) == nil && env.Message.To == p.ResultPending {
					found = e.Refused != "" && !e.Acknowledged
				}
			}
			if !found {
				t.Fatal("refused edge not terminally journaled")
			}
		})
	}
}

func TestShutdownCustodyResumesAfterRestart(t *testing.T) {
	for _, stage := range []string{"begin", "blob", "commit", "commit_ack", "usage", "finalize"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t, "edit")
			f.d.blockStage = stage
			f.d.stageWaiting = make(chan struct{}, 1)
			first := startServeBinary(t, f, true)
			select {
			case <-f.d.stageWaiting:
			case <-time.After(10 * time.Second):
				t.Fatal("custody stage not reached", stage, first.stderr.String())
			}
			if err := first.cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- first.cmd.Wait() }()
			select {
			case <-exited:
				first.reaped = true
			case <-time.After(10 * time.Second):
				t.Fatal("clean shutdown blocked")
			}
			f.d.mu.Lock()
			if f.d.state != p.ResultPending || f.d.termination != nil || contains(f.d.calls, "stopping") {
				t.Fatalf("finished job converted to cancellation: %v", f.d.calls)
			}
			f.d.blockStage = ""
			f.d.mu.Unlock()
			journal := filepath.Join(f.s.cfg.StateDir, "attempts", f.dispatch.ID, "journal")
			r, err := runner.Open(journal, f.s.options())
			if err != nil {
				t.Fatal(err)
			}
			var plan custodyPlan
			for _, e := range r.Outbox() {
				if e.Kind == "custody" {
					json.Unmarshal(e.Body, &plan)
				}
			}
			r.Close()
			if plan.Begin.MessageID == "" {
				t.Fatal("no durable custody plan")
			}
			second := startServeBinary(t, f, false)
			deadline := time.Now().Add(8 * time.Second)
			for !journalHas(journal, "outbox_ack", plan.Completion.MessageID) {
				if time.Now().After(deadline) {
					t.Fatal("restart failed to finalize", second.stderr.String(), second.stdout.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			for {
				f.d.mu.Lock()
				seen := false
				for _, hello := range f.d.hellos {
					for _, j := range hello.Journals {
						seen = seen || j.DispatchID == f.dispatch.ID
					}
				}
				f.d.mu.Unlock()
				if seen {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("recovery summary not sent")
				}
				time.Sleep(10 * time.Millisecond)
			}
			second.kill()
			f.d.mu.Lock()
			defer f.d.mu.Unlock()
			if !f.d.finalized || !f.d.released || f.d.termination != nil || f.d.manifest.Outcome != "succeeded" {
				t.Fatal("custody did not finish from evidence", f.d.calls)
			}
			if f.d.begin.MessageID != plan.Begin.MessageID {
				t.Fatal("begin identity changed")
			}
			found := false
			for _, hello := range f.d.hellos {
				for _, j := range hello.Journals {
					if j.DispatchID == f.dispatch.ID {
						found = true
						if j.Corrupt {
							t.Fatal("valid retained journal labelled corrupt")
						}
					}
				}
			}
			if !found {
				t.Fatal("recovery journal omitted")
			}
		})
	}
}

func TestTransientInputFailureDoesNotStrandDispatch(t *testing.T) {
	f := newFixture(t, "edit")
	f.d.inputFailures = 3
	if err := f.s.attempt(f.ctx, f.dispatch); err == nil {
		t.Fatal("input outage ignored")
	}
	if _, err := os.Stat(filepath.Join(f.s.cfg.StateDir, "attempts", f.dispatch.ID)); !os.IsNotExist(err) {
		t.Fatal("input failure created retained attempt", err)
	}
	if err := f.s.attempt(f.ctx, f.dispatch); err != nil {
		t.Fatal("retry stranded", err)
	}
}

func TestLiveContainmentUnconfirmedUsesLocalStop(t *testing.T) {
	f := newFixture(t, "detached_child")
	done := make(chan error, 1)
	go func() { done <- f.s.attempt(f.ctx, f.dispatch) }()
	waitState(t, f, p.Running)
	// Wait for actual child activity and give the guardian multiple ancestry samples.
	deadline := time.Now().Add(3 * time.Second)
	for {
		found := false
		for _, rec := range f.d.sink.Records() {
			var v struct{ PID int }
			if json.Unmarshal(rec.Native.Data, &v) == nil && v.PID > 1 {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached child activity missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(350 * time.Millisecond)
	f.d.mu.Lock()
	pid := 0
	for _, body := range f.d.request {
		var env w.MessageEnvelope
		if json.Unmarshal(body, &env) == nil && env.Message.To == p.Running {
			pid = env.Evidence.PID
		}
	}
	f.d.mu.Unlock()
	if pid <= 1 {
		t.Fatal("job pid missing")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	} // not an owner cancellation
	select {
	case err := <-done:
		if !errors.Is(err, runner.ErrTerminationUnconfirmed) {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("live containment timeout")
	}
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	if f.d.termination == nil || f.d.termination.Message.StopID != w.LocalStopID(f.dispatch.Assignment.Identity.AttemptID, "containment_unconfirmed") || f.d.termination.Message.ConfirmedProcess != "unknown" || f.d.released {
		t.Fatal("live escape released or wrong cause", f.d.calls)
	}
	found := false
	for _, body := range f.d.request {
		var env w.MessageEnvelope
		if json.Unmarshal(body, &env) == nil && env.Message.To == p.Stopping {
			found = env.Evidence.Cause == "containment_unconfirmed"
		}
	}
	if !found {
		t.Fatal("live containment cause missing")
	}
}

func TestEveryTickCauseCarriesMatchingStoppingEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"policy", runner.ErrPolicyChanged}, {"grant", runner.ErrGrantExpired}, {"attempt_budget", runner.ErrAttemptBudgetExpired}, {"clock", runner.ErrClockInvalid}, {"lease", runner.ErrLeaseExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "noop")
			dir := filepath.Join(f.s.cfg.StateDir, "cause-journal")
			r, err := runner.Create(dir, f.s.options())
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			ack, err := r.Accept(f.s.options().Session, f.d.dispatch)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.message(f.ctx, r, f.dispatch.ID, ack, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if err = f.s.lease(f.ctx, r, f.dispatch.ID); err != nil {
				t.Fatal(err)
			}
			cancel, cause := f.s.tickCancel(r, f.d.dispatch, tc.err)
			want := w.LocalStopID(f.dispatch.Assignment.Identity.AttemptID, "local_policy_drift")
			if tc.name == "lease" {
				want = w.ExpiryStopID(lastNonce(r))
			}
			if cancel.StopID != want {
				t.Fatalf("wrong stop ID for %s", tc.name)
			}
			msg, err := f.s.transition(f.ctx, r, f.d.dispatch, p.Assigned, p.Stopping, 1, w.Evidence{Kind: "stop", Nonce: lastNonce(r)}, cancel.StopID)
			if err != nil {
				t.Fatal(err)
			}
			f.d.mu.Lock()
			body := append([]byte(nil), f.d.request[msg.MessageID]...)
			f.d.mu.Unlock()
			var env w.MessageEnvelope
			if err := w.Decode(body, &env); err != nil {
				t.Fatal(err)
			}
			if env.Evidence == nil {
				t.Fatal("missing stop evidence")
			}
			if tc.name == "lease" {
				if cause != "lease_expired" || env.Evidence.Cause != "" {
					t.Fatal("expiry incorrectly claims local cause")
				}
			} else if env.Evidence.Cause != "local_policy_drift" {
				t.Fatal("local cause omitted", env.Evidence)
			}
		})
	}
}

func TestServeRetriesOneTimeDispatchAfterInputOutage(t *testing.T) {
	f := newFixture(t, "edit")
	f.d.inputFailures = 4
	proc := startServeBinary(t, f, true)
	deadline := time.Now().Add(12 * time.Second)
	for {
		f.d.mu.Lock()
		finalized := f.d.finalized
		f.d.mu.Unlock()
		if finalized {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("one-time input dispatch stranded", proc.stderr.String(), proc.stdout.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	proc.kill()
}

func TestHelloSummaryRetainsReceiptAndStream(t *testing.T) {
	f := newFixture(t, "edit")
	if err := f.s.attempt(f.ctx, f.dispatch); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.s.cfg.StateDir, "attempts", f.dispatch.ID)
	r, err := runner.Open(filepath.Join(dir, "journal"), f.s.options())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	summary := journalSummary(r, attemptMeta{f.d.dispatch, f.s.boot, f.s.session.DaemonBoot}, dir)
	if summary.State != p.ResultPending || summary.ReceiptID != f.d.receipt.Receipt.ReceiptID || summary.StreamThrough != f.d.sink.Acknowledged() || summary.StreamThrough <= 0 || summary.Corrupt {
		t.Fatalf("incomplete retained summary: %+v", summary)
	}
}
