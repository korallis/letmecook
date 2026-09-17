package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/workflow"
)

func TestWorkflowTaskDurabilityReplayAndClosedSettings(t *testing.T) {
	s, artifacts := persistent(t)
	profile, owner, runner := repositoryFixture(t, s)
	if _, err := s.RegisterRepository(ctx, owner, 0, profile); err != nil {
		t.Fatal(err)
	}
	brief := TaskBrief{TaskID: newID(), Repository: profile.ID, BaseCommit: profile.Base.Commit, Brief: "change fixture", Criteria: []Criterion{{"c1", "fixture changed"}}, Paths: []string{"file"}, Operations: []string{"write", "read", "verify"}, Harness: "fake", Settings: json.RawMessage(`{"attempts":[{"mode":"edit","edits":[{"content":"changed","path":"file"}]}]}`)}
	if _, err := s.CreateTask(ctx, runner, brief); !errors.Is(err, i.Denied) {
		t.Fatal("runner", err)
	}
	first, err := s.CreateTask(ctx, owner, brief)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Task(ctx, brief.TaskID)
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("read %#v, %v", got, err)
	}
	replay, err := s.CreateTask(ctx, owner, brief)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatal("replay", err)
	}
	brief.Settings = json.RawMessage(`{ "attempts": [ {"edits":[{"path":"file","content":"changed"}],"mode":"edit"} ] }`)
	if _, err = s.CreateTask(ctx, owner, brief); err != nil {
		t.Fatal("canonical replay", err)
	}
	if BriefDigest(got.Brief) != got.Brief.BriefSHA256 {
		t.Fatal("digest differs")
	}
	changed := brief
	changed.Brief = "different"
	_, err = s.CreateTask(ctx, owner, changed)
	requireReason(t, err, "identity_conflict")
	for _, raw := range []string{`{"attempts":[],"extra":true}`, `{"attempts":null}`, `{"attempts":[],"attempts":[]}`, `{"attempts":[{"mode":"shell","edits":[]}]}`, `{"attempts":[{"mode":"edit","edits":[{"path":"../outside","content":"x"}]}]}`} {
		bad := brief
		bad.TaskID = newID()
		bad.Settings = json.RawMessage(raw)
		_, err = s.CreateTask(ctx, owner, bad)
		requireReason(t, err, "malformed")
	}
	// Authentication observed before the mutation cannot authorize a revoked owner.
	if _, err = s.Authenticate(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err = s.BootstrapOwner(ctx, strings.Repeat("c", 64), true); err != nil {
		t.Fatal(err)
	}
	brief.TaskID = newID()
	if _, err = s.CreateTask(OwnerContext(ctx, owner), owner, brief); !errors.Is(err, i.Denied) {
		t.Fatal("revoked commit", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err = reopened.Task(ctx, first.Brief.TaskID)
	if err != nil || got.Brief.BriefSHA256 != first.Brief.BriefSHA256 {
		t.Fatal("restart", err)
	}
}

func TestWorkflowJobsAndPauseReceipts(t *testing.T) {
	s, _ := persistent(t)
	profile, owner, _ := repositoryFixture(t, s)
	_ = profile
	now := time.Now().UnixMilli()
	job := Job{ID: newID(), Kind: "verify", State: "queued", DaemonBoot: s.meta.DaemonBoot, CreatedMS: now, Result: `{"request":{}}`}
	if _, err := s.PutJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutJob(ctx, job); err != nil {
		t.Fatal("replay", err)
	}
	conflict := job
	conflict.Kind = "backup"
	_, err := s.PutJob(ctx, conflict)
	requireReason(t, err, "identity_conflict")
	job.State = "running"
	job.StartedMS = now
	if _, err = s.PutJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	pending, err := s.RecoverJobs(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	retained, err := s.Job(ctx, job.ID)
	if err != nil || retained.State != "failed" || retained.Error != "daemon_restart" {
		t.Fatal(retained, err)
	}
	job.State = "succeeded"
	job.FinishedMS = now
	_, err = s.PutJob(ctx, job)
	requireReason(t, err, "revision_conflict")
	id := newID()
	paused, err := s.SetPaused(ctx, owner, id, true, "maintenance", false)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.SetPaused(ctx, owner, id, true, "maintenance", false)
	if err != nil || again != paused {
		t.Fatal("pause replay", again, err)
	}
	_, err = s.SetPaused(ctx, owner, id, false, "maintenance", false)
	requireReason(t, err, "identity_conflict")
	if state, err := s.SetPaused(ctx, owner, newID(), false, "", false); err != nil || state.Paused {
		t.Fatal(state, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.PutJob(cancelled, Job{}); err == nil {
		t.Fatal("cancelled write acknowledged")
	}
}

func TestWorkflowProposalBindsPersistedTask(t *testing.T) {
	f := dispatchFixtureFor(t, func(f *dispatchFixture) {
		f.facts.Route.Harness = "fake"
		f.facts.LocalEnvelope.Routes[0].Harness = "fake"
		f.grant.Envelope.Routes[0].Harness = "fake"
		f.request.Decision.Selected.Harness = "fake"
	})
	brief := TaskBrief{TaskID: newID(), Repository: f.grant.Envelope.Repository, BaseCommit: f.grant.Envelope.BaseCommit, Brief: "fixture change", Criteria: []Criterion{{"c1", "passes"}}, Paths: []string{"a.txt"}, Operations: []string{"read", "verify", "write"}, Harness: "fake", Settings: json.RawMessage(`{"attempts":[{"mode":"edit","edits":[]}]}`)}
	task, err := f.s.CreateTask(ctx, f.owner, brief)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := workflow.BuildGrant(ctx, f.s, brief.TaskID, f.facts.ID)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Grant.Envelope.Brief.SHA256 != task.Brief.BriefSHA256 {
		t.Fatal("brief hash not bound")
	}
}

func TestWorkflowEnvelopeFailureAndStaleCandidateBlockAcceptance(t *testing.T) {
	s, _ := persistent(t)
	report, decision := verificationFixture(t, s)
	report.RecreationFailure = "envelope_violation"
	mustSaveVerification(t, s, report)
	if err := s.RecordLocalDecision(ctx, decision); err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatal("accepted envelope violation", err)
	}
	replacementCandidate(t, s, report.Candidate)
	decision.Action = "reject"
	if err := s.RecordLocalDecision(ctx, decision); err == nil {
		t.Fatal("stale candidate accepted as current decision")
	}
}

func TestWorkflowGatewayConfigurationNeverReadsCredential(t *testing.T) {
	s, _ := persistent(t)
	_, owner, runner := repositoryFixture(t, s)
	raw := []byte(`{"version":"gateway-config-v1","gateway_id":"fixture","base_url":"https://gateway.invalid/v1","credential_ref":{"kind":"file","path":"/nonexistent-test-only-credential"},"protocols":["responses"],"models":["model"]}`)
	if _, err := s.PutGatewayProfile(ctx, runner, raw); !errors.Is(err, i.Denied) {
		t.Fatal("runner wrote profile", err)
	}
	first, err := s.PutGatewayProfile(ctx, owner, raw)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.PutGatewayProfile(ctx, owner, raw)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatal("gateway replay", err)
	}
	for _, bad := range []string{strings.Replace(string(raw), `["model"]`, `["model","model"]`, 1), strings.Replace(string(raw), `https://gateway.invalid/v1`, `https://gateway.invalid/v1?`, 1), strings.Replace(string(raw), `["responses"]`, `["bad protocol"]`, 1)} {
		_, err := s.PutGatewayProfile(ctx, owner, []byte(bad))
		requireReason(t, err, "malformed")
	}
}
func TestWorkflowDurableEventCursor(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	admitted(t, f)
	first, err := f.s.Events(ctx, 0, 1, "")
	if err != nil || len(first.Events) != 1 || first.NextAfter == 0 {
		t.Fatal(first, err)
	}
	next, err := f.s.Events(ctx, first.NextAfter, 1, "")
	if err != nil || len(next.Events) != 0 || next.NextAfter != first.NextAfter {
		t.Fatal(next, err)
	}
	// Test-only corruption of the disposable fixture models an already-retained
	// suffix. Production events remain append-only; no retention policy is added.
	if _, err = f.s.db.Exec("DROP TRIGGER events_no_update; UPDATE events SET sequence=sequence+100"); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Events(ctx, first.NextAfter, 1, "")
	requireReason(t, err, "cursor_expired")
}
