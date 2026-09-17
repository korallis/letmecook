package main

import (
	"bytes"
	"context"
	"database/sql"
	i "github.com/korallis/letmecook/internal/identity"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"encoding/json"
	"github.com/korallis/letmecook/internal/httpapi"
	"github.com/korallis/letmecook/internal/jobs"
	"github.com/korallis/letmecook/internal/store"
	v "github.com/korallis/letmecook/internal/verification"
	"github.com/korallis/letmecook/internal/workflow"
	p "github.com/korallis/letmecook/schemas/execution"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPSDevelopmentIdentityCannotBypassConsent(t *testing.T) {
	for _, qualification := range []string{"", "unqualified", "development", "test-only"} {
		for _, supported := range []bool{false, true} {
			if qualification == "development" && !supported {
				continue
			}
			t.Run(qualification+map[bool]string{true: "-supported", false: "-unsupported"}[supported], func(t *testing.T) {
				f := newOwnerFixture(t)
				endpoint, client := f.serve(t)
				create := taskBody(f, "mislabel-task")
				postWorkflow(t, client, endpoint, "/api/v1/tasks", create, 201)
				facts := f.facts
				facts.Revision = 2
				facts.Isolation.Qualification, facts.Isolation.Supported = qualification, supported
				body := commandBody("mislabel-publish")
				body["expected_revision"], body["facts"] = 1, facts
				raw, _ := postWorkflow(t, client, endpoint, "/api/v1/eligibility", body, 422)
				if !strings.Contains(string(raw), "development_isolation_refused") {
					t.Fatal(string(raw))
				}
				// Model a historical row admitted by the old validator. Do not weaken the
				// immutable-row trigger or claim this is a supported write path.
				db, err := sql.Open("sqlite", filepath.Join(f.state, "state.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				encoded, _ := json.Marshal(facts)
				if _, err = db.Exec(`INSERT INTO dispatch_eligibility SELECT id,2,?,actor,repository_id,runner_id,root FROM dispatch_eligibility WHERE id=? AND revision=1`, string(encoded), facts.ID); err != nil {
					t.Fatal(err)
				}
				approval := commandBody("mislabel-approve")
				approval["eligibility_id"], approval["proposal_digest"], approval["allow_development_isolation"] = facts.ID, strings.Repeat("a", 64), false
				raw, _ = postWorkflow(t, client, endpoint, "/api/v1/tasks/"+create["message_id"].(string)+"/approve", approval, 400)
				if !strings.Contains(string(raw), "isolation_qualification") {
					t.Fatal(string(raw))
				}
			})
		}
	}
}

func TestHTTPSTaskResumeReplaysDurableClearances(t *testing.T) {
	f := newOwnerFixture(t)
	endpoint, client := f.serve(t)
	create := taskBody(f, "resume-task")
	postWorkflow(t, client, endpoint, "/api/v1/tasks", create, 201)
	task := create["message_id"].(string)
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+task+"/stop", commandBody("resume-stop"), 202)
	body := commandBody("resume-command")
	first := mutation(t, client, endpoint, "/api/v1/tasks/"+task+"/resume", body, 200)
	var response struct {
		Cleared []string `json:"cleared_latches"`
	}
	if decodeReply(first, &response) != nil || len(response.Cleared) != 2 {
		t.Fatal(string(first))
	}
	var stdout, stderr bytes.Buffer
	args := []string{"--endpoint", endpoint, "--cert", f.cert, "--key", f.key, "--daemon-fingerprint", f.serverPin, "--message-id", body["message_id"].(string), "--json", "task", "resume", task}
	if code := run(context.Background(), args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "task.resume") || stderr.Len() != 0 {
		t.Fatal(code, stdout.String(), stderr.String())
	}
	// Both the ordinary task stop and legacy StopDispatch marker are retained.
	db, err := sql.Open("sqlite", filepath.Join(f.state, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range response.Cleared {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM reconcile_reports WHERE id=?", "latch-cleared:"+id).Scan(&n); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	// TODO(integration): fresh dispatch after resume depends on the integrator's
	// suppression changes; this test proves durable marker writes, not admission.
}

func TestProposalUnknownFieldsRefusedBeforeApproval(t *testing.T) {
	f := newOwnerFixture(t)
	posted := false
	endpoint := cliTLSServer(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			posted = true
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"digests":{"proposal":"present"},"unexpected_authority":true}`)
	}), nil)
	global := globals{Endpoint: endpoint, Cert: f.cert, Key: f.key, DaemonFingerprint: f.serverPin, Timeout: 10 * time.Second, JSON: true, MessageID: v.ID()}
	if _, err := approve(context.Background(), global, v.ID(), f.facts.ID, "", true, nil, 0); err == nil || err.Code != "invalid_response" || posted {
		t.Fatal(err, posted)
	}
}

func TestTaskHarnessRequiredGolden(t *testing.T) {
	f := newOwnerFixture(t)
	for _, family := range []string{"task", "flow"} {
		sub := "create"
		if family == "flow" {
			sub = "run"
		}
		args := []string{"--json", family, sub, "--repo", f.profile.ID, "--base", f.profile.Base.Commit, "--brief-file", f.brief, "--criterion", "c1=passes", "--path", "file"}
		if family == "flow" {
			args = append(args, "--runner", f.runner, "--flow-id", "missing-harness")
		}
		var out, stderr bytes.Buffer
		code := run(context.Background(), args, &out, &stderr)
		want := "{\"version\":\"gaffer-cli-v1\",\"error\":{\"status\":0,\"code\":\"invalid_arguments\",\"detail\":\"invalid task brief, scope or harness settings\"}}\n"
		if code != 2 || out.Len() != 0 || stderr.String() != want {
			t.Fatal(family, code, stderr.String())
		}
	}
}

func TestHTTPSTaskEnvelopeBoundaryAndBareIdentityConflict(t *testing.T) {
	f := newOwnerFixture(t)
	endpoint, client := f.serve(t)
	body := taskBody(f, "maximum-task")
	body["brief"] = ""
	raw, _ := json.Marshal(body)
	body["brief"] = strings.Repeat("x", 65536-len(raw))
	raw, _ = json.Marshal(body)
	if len(raw) != 65536 {
		t.Fatal(len(raw))
	}
	mutation(t, client, endpoint, "/api/v1/tasks", body, 201)
	body["brief"] = body["brief"].(string) + "x"
	postWorkflow(t, client, endpoint, "/api/v1/tasks", body, 413)
	body = taskBody(f, "bare-task")
	db, err := sql.Open("sqlite", filepath.Join(f.state, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("INSERT INTO tasks VALUES(?,'ready')", body["message_id"]); err != nil {
		t.Fatal(err)
	}
	raw, _ = postWorkflow(t, client, endpoint, "/api/v1/tasks", body, 409)
	if !strings.Contains(string(raw), "identity_conflict") {
		t.Fatal(string(raw))
	}
}

// Metadata fixture: real approval/dispatch/custody, with an explicitly synthetic
// finalized head. It does not claim harness execution or process termination.
func round2Candidate(t *testing.T, f *ownerFixture, path string) (string, v.Candidate) {
	t.Helper()
	ctx := context.Background()
	endpoint, client := f.serve(t)
	body := taskBody(f, "candidate-"+path)
	task := body["message_id"].(string)
	postWorkflow(t, client, endpoint, "/api/v1/tasks", body, 201)
	proposal, err := workflow.BuildGrant(ctx, f.s, task, f.facts.ID)
	if err != nil {
		t.Fatal(err)
	}
	approval := commandBody("candidate-approve")
	approval["eligibility_id"], approval["proposal_digest"], approval["allow_development_isolation"] = f.facts.ID, proposal.Digests["proposal"], true
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+task+"/approve", approval, 201)
	dispatch := commandBody("candidate-dispatch")
	dispatch["grant_id"], dispatch["grant_revision"], dispatch["attempt_ms"] = approval["message_id"], 1, 60000
	raw, _ := postWorkflow(t, client, endpoint, "/api/v1/tasks/"+task+"/dispatch", dispatch, 201)
	admitted := decodeOwner[store.Dispatch](t, raw)
	identity := admitted.Assignment.Identity
	payload := []byte("candidate fixture\n")
	source := filepath.Join(t.TempDir(), "blob")
	if err = os.WriteFile(source, payload, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := store.CandidateManifest{Version: "gaffer-artifact-manifest-v1", Identity: identity, Base: store.ArtifactBase{Revision: f.profile.Base.Commit, SHA256: v.Digest([]byte(f.profile.Base.Commit))}, Outcome: "succeeded", Tracked: []store.ArtifactBlob{{Path: path, SHA256: v.Digest(payload), Bytes: int64(len(payload))}}, Untracked: []store.ArtifactBlob{}, Binary: []store.ArtifactBlob{}, Recovery: []store.ArtifactBlob{}, Deleted: []string{}}
	if path != "file" {
		manifest.Deleted = []string{"file"}
	}
	encoded, _ := json.Marshal(manifest)
	ref := p.Manifest{ManifestID: v.ID(), SHA256: v.Digest(encoded), Bytes: int64(len(encoded))}
	result := p.Message{Version: p.FencedVersion, MessageID: v.ID(), Kind: "result", Identity: identity, Manifest: &ref}
	receipt, err := f.s.CustodyResult(ctx, store.CustodyRequest{Result: result, Manifest: encoded, Sources: []store.ArtifactSource{{Path: path, File: source}}, RetainUntilMS: time.Now().Add(time.Hour).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(f.state, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO artifact_result_heads VALUES(?,?,?,?,?,?,?)`, task, identity.Generation, identity.AttemptID, identity.Epoch, ref.ManifestID, receipt.Receipt.ReceiptID, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	candidate, err := f.s.SelectVerificationCandidate(ctx, "", ref)
	if err != nil {
		t.Fatal(err)
	}
	return task, candidate
}

func TestHTTPSLargeReportDoesNotPoisonTaskViews(t *testing.T) {
	f := newOwnerFixture(t)
	task, candidate := round2Candidate(t, f, "file")
	report := syntheticOwnerReport(candidate)
	report.Suggestions = []v.Suggestion{{Source: "synthetic-large-report-test", Text: strings.Repeat("x", (1<<20)+1)}}
	if err := f.s.SaveVerification(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	// The fixture server already has a worker; use a second read-only transport,
	// not a second job worker, to inspect the same durable state.
	// Existing fixture serve returned its endpoint to the helper; the actual read
	// server below is composed without a jobs worker.
	endpoint, client := round2ReadServer(t, f)
	for _, path := range []string{"/api/v1/tasks/" + task, "/api/v1/tasks", "/api/v1/tasks/" + task + "/verification"} {
		raw, _ := getOwner(t, client, endpoint, path, 200)
		if len(raw) > maxReply || bytes.Contains(raw, []byte("synthetic-large-report-test")) {
			t.Fatal("report body leaked into task view", len(raw))
		}
		if !bytes.Contains(raw, []byte(report.ID)) {
			t.Fatal("report reference missing")
		}
	}
	global := globals{Endpoint: endpoint, Cert: f.cert, Key: f.key, DaemonFingerprint: f.serverPin, Timeout: 10 * time.Second}
	raw, e := call(context.Background(), global, "GET", "/api/v1/verifications/"+report.ID, nil)
	if e != nil || len(raw) <= maxReply {
		t.Fatal("dedicated large report", len(raw), e)
	}
	var got v.Report
	if decodeReplyBound(raw, &got, v.MaxReportBytes) != nil || got.Suggestions[0].Text != report.Suggestions[0].Text {
		t.Fatal("full report truncated")
	}
}

func TestVerificationUsesDispatchedEnvelopeAfterWiderApproval(t *testing.T) {
	f := newOwnerFixture(t)
	task, candidate := round2Candidate(t, f, "outside")
	current, err := f.s.Task(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := f.s.ExecutionGrant(context.Background(), current.GrantHead)
	if err != nil {
		t.Fatal(err)
	}
	old := grant.ID
	grant.ID, grant.Revision = v.ID(), grant.Revision+1
	grant.Envelope.Paths = []string{"file", "outside"}
	if _, err = f.s.ApproveExecution(context.Background(), old, grant); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"actor": f.owner, "task_id": task, "command": map[string]any{"version": workflow.Version, "message_id": v.ID(), "expected_selection": candidate.SelectionID}})
	job := jobs.Job{ID: v.ID(), Result: payload}
	handlers := httpapi.WorkflowJobHandlers(httpapi.Deps{Store: f.s}, httpapi.VerificationOptions{StateDir: f.state})
	if _, err = handlers["verify"](context.Background(), job); err != nil {
		t.Fatal(err)
	}
	report, err := f.s.Verification(context.Background(), workflow.IntentID(job.ID, "verification"))
	if err != nil || report.RecreationFailure != "envelope_violation" || len(report.Evidence) != 0 {
		t.Fatal("later approval expanded candidate authority", report, err)
	}
}

func round2ReadServer(t *testing.T, f *ownerFixture) (string, *http.Client) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	var err error
	ts.TLS, err = i.ServerTLS(f.serverCert, f.serverKey)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler, err = httpapi.NewTLSWithDeps(f.s, "https://"+ts.Listener.Addr().String(), httpapi.Deps{Store: f.s})
	if err != nil {
		t.Fatal(err)
	}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	client, err := ownerClient(globals{Endpoint: ts.URL, Cert: f.cert, Key: f.key, DaemonFingerprint: f.serverPin, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return ts.URL, client
}
