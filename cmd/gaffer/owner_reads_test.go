package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/jobs"
	"github.com/korallis/letmecook/internal/review"
	"github.com/korallis/letmecook/internal/runstream"
	"github.com/korallis/letmecook/internal/store"
	v "github.com/korallis/letmecook/internal/verification"
	"github.com/korallis/letmecook/internal/workflow"
	p "github.com/korallis/letmecook/schemas/execution"
)

// S1's registry is not in this lane. Adapt a real durable Sink, never fabricated
// stream acknowledgements, to the structural HTTP read interface.
type fixtureSinkReader struct {
	mu      sync.Mutex
	sink    *runstream.Sink
	attempt string
}

func (s *fixtureSinkReader) Window(attempt string, after, limit int64) ([]runstream.Record, runstream.Ack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sink == nil || attempt != s.attempt {
		return nil, runstream.Ack{}, runstream.ErrUnavailable
	}
	out := []runstream.Record{}
	for _, r := range s.sink.Records() {
		if r.Sequence > after && int64(len(out)) < limit {
			out = append(out, r)
		}
	}
	stats := s.sink.Stats()
	return out, runstream.Ack{Through: s.sink.Acknowledged(), Expected: s.sink.Expected(), Bytes: stats.NativeBytes}, nil
}
func getOwner(t *testing.T, client *http.Client, endpoint, path string, want int) ([]byte, http.Header) {
	t.Helper()
	resp, err := client.Get(endpoint + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != want {
		t.Fatalf("GET %s: status %d want %d: %s (%v)", path, resp.StatusCode, want, raw, err)
	}
	return raw, resp.Header
}
func decodeOwner[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err, string(raw))
	}
	return out
}

func TestOwnerHTTPSReadsReviewVerificationAndRetry(t *testing.T) {
	f := newOwnerFixture(t)
	ctx := context.Background()
	streams := &fixtureSinkReader{}
	endpoint, client := f.serve(t, streams)
	defer client.CloseIdleConnections()
	create := taskBody(f, "reads-create")
	taskID := create["message_id"].(string)
	postWorkflow(t, client, endpoint, "/api/v1/tasks", create, 201)
	proposal, err := workflow.BuildGrant(ctx, f.s, taskID, f.facts.ID)
	if err != nil {
		t.Fatal(err)
	}
	approve := commandBody("reads-approve")
	approve["eligibility_id"] = f.facts.ID
	approve["expected_grant_id"] = ""
	approve["proposal_digest"] = proposal.Digests["proposal"]
	approve["allow_development_isolation"] = true
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/approve", approve, 201)
	dispatch := commandBody("reads-dispatch")
	dispatch["grant_id"] = approve["message_id"]
	dispatch["grant_revision"] = int64(1)
	dispatch["attempt_ms"] = int64(60000)
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/dispatch", dispatch, 201)
	task, err := f.s.Task(ctx, taskID)
	if err != nil || len(task.Attempts) != 1 {
		t.Fatal(task, err)
	}
	identity := task.Attempts[0].Identity
	attempt := identity.AttemptID

	payload := []byte("candidate fixture\n")
	source := filepath.Join(t.TempDir(), "file")
	if err = os.WriteFile(source, payload, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := store.CandidateManifest{Version: "gaffer-artifact-manifest-v1", Identity: identity, Base: store.ArtifactBase{Revision: f.profile.Base.Commit, SHA256: v.Digest([]byte(f.profile.Base.Commit))}, Outcome: "succeeded", Tracked: []store.ArtifactBlob{{Path: "file", SHA256: v.Digest(payload), Bytes: int64(len(payload))}}, Untracked: []store.ArtifactBlob{}, Binary: []store.ArtifactBlob{}, Recovery: []store.ArtifactBlob{}, Deleted: []string{}}
	body, _ := json.Marshal(manifest)
	ref := p.Manifest{ManifestID: v.ID(), SHA256: v.Digest(body), Bytes: int64(len(body))}
	result := p.Message{Version: p.FencedVersion, MessageID: v.ID(), Kind: "result", Identity: identity, Manifest: &ref}
	receipt, err := f.s.CustodyResult(ctx, store.CustodyRequest{Result: result, Manifest: body, Sources: []store.ArtifactSource{{Path: "file", File: source}}, RetainUntilMS: time.Now().Add(time.Hour).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(f.state, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Synthetic finalization HEAD only: S1 finalization is absent on this branch.
	// Bytes/custody/selection/reports all use real store APIs; no execution is claimed.
	_, err = db.Exec(`INSERT INTO artifact_result_heads(task_id,generation,attempt_id,epoch,manifest_id,receipt_id,finalized_ms) VALUES(?,?,?,?,?,?,?)`, taskID, identity.Generation, attempt, identity.Epoch, ref.ManifestID, receipt.Receipt.ReceiptID, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := f.s.SelectVerificationCandidate(ctx, "", ref)
	if err != nil {
		t.Fatal(err)
	}
	report := syntheticOwnerReport(candidate)
	if err = f.s.SaveVerification(ctx, report); err != nil {
		t.Fatal(err)
	}
	if !v.Evaluate(report, candidate).Verified {
		t.Fatal("synthetic metadata fixture must evaluate verified")
	}

	streamRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spool, err := runstream.CreateSpool(filepath.Join(streamRoot, "spool"), identity, runstream.MinLimit)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	sink, err := runstream.CreateSink(filepath.Join(streamRoot, "sink"), identity, runstream.MinLimit)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	record, err := spool.Append(runstream.Native{Version: "fixture-v1", Kind: "stdout", Data: []byte("fixture")}, runstream.Normalized{Stream: "stdout", Text: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sink.Receive(record); err != nil {
		t.Fatal(err)
	}
	streams.mu.Lock()
	streams.sink = sink
	streams.attempt = attempt
	streams.mu.Unlock()
	for _, path := range []string{
		"/api/v1/repositories/" + f.profile.ID, "/api/v1/eligibility/" + f.facts.ID, "/api/v1/runners", "/api/v1/tasks", "/api/v1/tasks?limit=1&after=", "/api/v1/tasks/" + taskID,
		"/api/v1/tasks/" + taskID + "/proposal?eligibility=" + f.facts.ID, "/api/v1/attempts/" + attempt, "/api/v1/attempts/" + attempt + "/usage", "/api/v1/events", "/api/v1/events?after=0&limit=1&task_id=" + taskID, "/api/v1/daemon",
		"/api/v1/verifications/" + report.ID, "/api/v1/tasks/" + taskID + "/verification", "/api/v1/tasks/" + taskID + "/review",
	} {
		t.Run(path, func(t *testing.T) { getOwner(t, client, endpoint, path, 200) })
	}
	raw, _ := getOwner(t, client, endpoint, "/api/v1/attempts/"+attempt+"/artifacts", 200)
	artifacts := decodeOwner[[]store.CandidateManifest](t, raw)
	if len(artifacts) != 1 || artifacts[0].Identity != identity {
		t.Fatal(string(raw))
	}
	raw, _ = getOwner(t, client, endpoint, "/api/v1/attempts/"+attempt+"/stream?after=0&limit=1", 200)
	var window struct {
		Records []runstream.Record `json:"records"`
		Ack     runstream.Ack      `json:"ack"`
	}
	window = decodeOwner[struct {
		Records []runstream.Record `json:"records"`
		Ack     runstream.Ack      `json:"ack"`
	}](t, raw)
	if len(window.Records) != 1 || window.Records[0].Digest != record.Digest || window.Ack.Through != 1 {
		t.Fatal(string(raw))
	}
	raw, header := getOwner(t, client, endpoint, "/api/v1/artifacts/blobs/"+v.Digest(payload), 200)
	if !bytes.Equal(raw, payload) || header.Get("Content-Type") != "application/octet-stream" {
		t.Fatal(string(raw), header)
	}

	for _, path := range []string{"/api/v1/daemon?unknown=1", "/api/v1/events?limit=1&limit=2", "/api/v1/events?after=-1", "/api/v1/events?after=9007199254740992", "/api/v1/events?limit=no", "/api/v1/events?limit=0", "/api/v1/events?limit=999999999999999999999", "/api/v1/events?task_id=bad", "/api/v1/tasks?limit=129", "/api/v1/tasks/" + taskID + "/proposal", "/api/v1/stops/" + v.ID(), "/api/v1/attempts/" + attempt + "/stream?limit=0", "/api/v1/attempts/" + attempt + "/stream?after=-1"} {
		getOwner(t, client, endpoint, path, 400)
	}
	for _, path := range []string{"/api/v1/tasks/bad", "/api/v1/attempts/bad", "/api/v1/jobs/bad", "/api/v1/verifications/bad", "/api/v1/repositories/bad%20id", "/api/v1/eligibility/bad%20id"} {
		getOwner(t, client, endpoint, path, 400)
	}

	accept := commandBody("reads-accept")
	accept["verification_id"] = report.ID
	accept["selection_id"] = candidate.SelectionID
	accept["action"] = "accept"
	accept["coverage"] = []review.Coverage{{CriterionID: "c1", Status: "partial", EvidenceIDs: review.EvidenceIDs(report), Explanation: "synthetic metadata fixture, no checks executed"}}
	accept["limitations"] = report.Limitations
	accept["notes"] = ""
	raw, _ = postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/review", accept, 201)
	if !decodeOwner[review.Current](t, raw).Accepted {
		t.Fatal(string(raw))
	}
	again, h := postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/review", accept, 201)
	if !bytes.Equal(raw, again) || h.Get("Idempotent-Replay") != "true" {
		t.Fatal("review replay mismatch")
	}
	accept["message_id"] = v.ID()
	accept["selection_id"] = v.ID()
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/review", accept, 409)
	getOwner(t, client, endpoint, "/api/v1/tasks/"+taskID+"/review", 200)

	verify := commandBody("reads-verify")
	verify["expected_selection"] = candidate.SelectionID
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/verify", verify, 202)
	var job jobs.Job
	deadline := time.Now().Add(15 * time.Second)
	for {
		raw, _ = getOwner(t, client, endpoint, "/api/v1/jobs/"+verify["message_id"].(string), 200)
		job = decodeOwner[jobs.Job](t, raw)
		if job.State == jobs.Succeeded || job.State == jobs.Failed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("verify job timeout", job)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job.State != jobs.Succeeded {
		t.Fatalf("Prepare → Run → SaveVerification: %+v", job)
	}
	verifiedID := workflow.IntentID(job.ID, "verification")
	got, err := f.s.Verification(ctx, verifiedID)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := f.profile.Digest()
	if got.RecreationFailure != "" || got.Candidate != candidate || got.Checks.ID != digest || len(got.Evidence) != 1 || got.Evidence[0].Refusal == nil || got.Evidence[0].Refusal.Code != "unqualified_profile" {
		t.Fatalf("verification must reach refusal after successful private checkout/recreation: %+v", got)
	}
	entries, err := os.ReadDir(filepath.Join(f.state, "verify"))
	if err != nil || len(entries) != 0 {
		t.Fatal("private verification roots not cleaned", entries, err)
	}
	raw, _ = getOwner(t, client, endpoint, "/api/v1/tasks/"+taskID+"/verification", 200)
	current := decodeOwner[v.Summary](t, raw)
	if current.Status.Verified || current.ID != verifiedID {
		t.Fatal(string(raw))
	}
	accept["message_id"] = v.ID()
	accept["verification_id"] = verifiedID
	accept["selection_id"] = candidate.SelectionID
	accept["limitations"] = got.Limitations
	accept["coverage"] = []review.Coverage{{CriterionID: "c1", Status: "not-covered", EvidenceIDs: review.EvidenceIDs(got), Explanation: "unqualified profile refused"}}
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/review", accept, 422)
	// A historical self-valid report cannot override the server's current status.
	g := globals{Endpoint: endpoint, Cert: f.cert, Key: f.key, DaemonFingerprint: f.serverPin, Timeout: 10 * time.Second, MessageID: v.ID()}
	if _, e := reviewRequest(ctx, g, taskID, "accept", report.ID, "", "", ""); e == nil || e.Code != "verification_required" || e.Status != 422 {
		t.Fatal("CLI trusted historical self-evaluation", e)
	}

	var before, after int
	if err = db.QueryRow("SELECT count(*) FROM dispatches").Scan(&before); err != nil {
		t.Fatal(err)
	}
	raw, _ = postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/retry", commandBody("reads-retry"), 422)
	if !bytes.Contains(raw, []byte(`"error":"retry_unavailable"`)) || !bytes.Contains(raw, []byte(`"detail":"reconcile"`)) {
		t.Fatal(string(raw))
	}
	if err = db.QueryRow("SELECT count(*) FROM dispatches").Scan(&after); err != nil || before != after {
		t.Fatal("retry created a dispatch", before, after, err)
	}
	stop := commandBody("reads-stop")
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/stop", stop, 202)
	getOwner(t, client, endpoint, "/api/v1/stops/"+stop["message_id"].(string)+"?attempt_id="+attempt, 200)
	// Model an expired retained suffix only in this disposable SQLite fixture.
	// Production events stay append-only; restore its trigger immediately.
	_, err = db.Exec(`DROP TRIGGER events_no_update; UPDATE events SET sequence=sequence+100; CREATE TRIGGER events_no_update BEFORE UPDATE ON events BEGIN SELECT RAISE(ABORT,'events are append-only'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = getOwner(t, client, endpoint, "/api/v1/events?after=1", 410)
	if !bytes.Contains(raw, []byte(`"error":"cursor_expired"`)) {
		t.Fatal(string(raw))
	}
}

func syntheticOwnerReport(candidate v.Candidate) v.Report {
	check := v.Check{Name: "synthetic", Argv: []string{"/usr/bin/true"}, Env: map[string]string{"PATH": "/no-docker-in-test-environment"}, CWD: ".", Timeout: time.Second, Required: true}
	zero := 0
	at := time.Now().UTC()
	evidence := v.Evidence{ID: v.ID(), CandidateDigest: candidate.Manifest.SHA256, BaseCommit: candidate.BaseCommit, ProfileID: "test-only-unconfined", CheckName: check.Name, Argv: check.Argv, EnvKeys: []string{"PATH"}, CWD: ".", ExitCode: &zero, Stdout: v.Stream{Prefix: []byte{}, SHA256: v.Digest(nil)}, Stderr: v.Stream{Prefix: []byte{}, SHA256: v.Digest(nil)}, Started: at, Ended: at, Verifier: "synthetic-store-fixture", Environment: v.Environment{OS: "synthetic", Arch: "synthetic", DockerBinary: "absent", DockerSearchPath: check.Env["PATH"], ExpectedConfinement: "test-only-unconfined", ObservedConfinement: "test-only-unconfined"}}
	return v.Report{ID: v.ID(), Candidate: candidate, Checks: v.TrustedChecks{ID: "test-approved", ApprovedBy: "operator", ApprovalRef: "test-approval", Checks: []v.Check{check}}, Evidence: []v.Evidence{evidence}, Suggestions: []v.Suggestion{}, Limitations: []string{v.ContentLimitation, "synthetic persistence fixture; no actual checks executed"}}
}
