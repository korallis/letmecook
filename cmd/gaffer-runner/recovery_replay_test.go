package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	w "github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/runner"
	p "github.com/korallis/letmecook/schemas/execution"
)

func TestRecoverTerminationReplaysUndeliveredOldBoot(t *testing.T) {
	for _, refused := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "session-stale-refused"}[refused], func(t *testing.T) {
			f := newFixture(t, "hang")
			path := filepath.Join(f.root, "replay-journal")
			r, err := runner.Create(path, f.s.options())
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			duration := int64(1)
			measurement := c.Measurement{RequestedAt: now, AcknowledgedAt: now, ObservedAt: &now, RequestToAckNS: duration, AckToObservedNS: &duration}
			boundary := inference.State{Quiescent: true}
			m := p.Message{Version: p.FencedVersion, Kind: "terminated", MessageID: uuid(), Identity: f.dispatch.Assignment.Identity, StopID: w.LocalStopID(f.dispatch.Assignment.Identity.AttemptID, "runner_shutdown"), RunnerBoot: f.s.boot, DaemonBoot: f.s.session.DaemonBoot, ConfirmedProcess: "terminated", RemoteWork: "quiescent", EvidenceDigest: strings.Repeat("e", 64)}
			envelope := w.MessageEnvelope{Version: w.Version, MessageID: m.MessageID, DispatchID: f.dispatch.ID, Message: m, Boundary: &boundary, Measurement: &measurement}
			if err := r.Enqueue("message", m.MessageID, envelope); err != nil {
				t.Fatal(err)
			}
			if refused {
				if err := r.Refuse(m.MessageID, "execution 409 session_stale: session"); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			f.s.boot = uuid()
			f.s.session, err = f.s.client.Hello(f.ctx, w.Hello{Version: w.Version, MessageID: uuid(), RunnerBoot: f.s.boot, EligibilityID: f.local.ID, EligibilityRevision: f.local.Revision, PolicyDigest: strings.Repeat("a", 64), Journals: []w.Journal{}})
			if err != nil {
				t.Fatal(err)
			}
			r, err = runner.Open(path, f.s.options())
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			f.d.mu.Lock()
			meta := attemptMeta{f.d.dispatch, m.RunnerBoot, m.DaemonBoot}
			f.d.mu.Unlock()
			if err := f.s.recoverTermination(f.ctx, r, meta); err != nil {
				t.Fatal(err)
			}
			f.d.mu.Lock()
			got := f.d.termination
			f.d.mu.Unlock()
			if got == nil || got.Message.RunnerBoot != m.RunnerBoot || got.Message.DaemonBoot != m.DaemonBoot || got.Message.EvidenceDigest != m.EvidenceDigest || got.Message.StopID != m.StopID {
				t.Fatal("fresh session did not replay retained old-boot evidence", got)
			}
			if !outboxEntry(r, got.MessageID).Acknowledged {
				t.Fatal("successful delivery was not durably acknowledged")
			}
			if refused && (got.MessageID == m.MessageID || outboxEntry(r, m.MessageID).Acknowledged || outboxEntry(r, m.MessageID).Refused == "") {
				t.Fatal("terminal refusal was rewritten as an acknowledgement")
			}
			want, _ := json.Marshal(envelope)
			if string(outboxEntry(r, m.MessageID).Body) != string(want) {
				t.Fatal("original evidence bytes changed")
			}
			count := len(r.Outbox())
			if err := f.s.recoverTermination(f.ctx, r, meta); err != nil || len(r.Outbox()) != count {
				t.Fatal("acknowledged termination was regenerated", err)
			}
		})
	}
}

func TestRestoreQuarantinedCommitReplayFencesWithoutExiting(t *testing.T) {
	f := newFixture(t, "edit")
	f.d.blockStage = "commit_ack"
	f.d.stageWaiting = make(chan struct{}, 1)
	first := startServeBinary(t, f, true)
	select {
	case <-f.d.stageWaiting:
	case <-time.After(10 * time.Second):
		t.Fatal("commit acknowledgement gap not reached", first.stderr.String())
	}
	first.kill()
	f.d.mu.Lock()
	f.d.blockStage = ""
	f.d.receipt.Quarantined = true
	f.d.session.Generation = uuid()
	f.d.session.DaemonBoot = uuid()
	hellosBefore := len(f.d.hellos)
	f.d.mu.Unlock()
	second := startServeBinary(t, f, false)
	journal := filepath.Join(f.s.cfg.StateDir, "attempts", f.dispatch.ID, "journal")
	deadline := time.Now().Add(8 * time.Second)
	for !journalHas(journal, "fenced", "") {
		if time.Now().After(deadline) {
			t.Fatal("quarantined replay did not retain a fence", second.stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A subsequent hello proves serve remained in its recovery-only loop,
	// instead of returning a fatal custody acknowledgement mismatch.
	for {
		f.d.mu.Lock()
		hellos := len(f.d.hellos)
		finalized, released := f.d.finalized, f.d.released
		f.d.mu.Unlock()
		if finalized || released {
			t.Fatal("quarantined old-generation custody was finalized")
		}
		if hellos >= hellosBefore+3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runner did not remain in recovery-only mode", second.stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := second.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("runner exited after quarantine", err)
	}
	second.kill()
	r, err := runner.Open(journal, f.s.options())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var plan custodyPlan
	if err := json.Unmarshal(outboxEntry(r, "custody").Body, &plan); err != nil {
		t.Fatal(err)
	}
	entry := outboxEntry(r, plan.Commit.MessageID)
	if entry.Acknowledged || !strings.Contains(entry.Refused, "stale_generation") || outboxEntry(r, plan.Commit.MessageID+"/receipt").Body != nil {
		t.Fatal("quarantined reply was promoted or not fenced", entry.Refused)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(plan.BlobDir), "receipt.json")); !os.IsNotExist(err) {
		t.Fatal("quarantined receipt was promoted on disk", err)
	}
}
