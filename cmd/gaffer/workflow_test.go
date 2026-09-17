package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/httpapi"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/jobs"
	"github.com/korallis/letmecook/internal/notify"
	r "github.com/korallis/letmecook/internal/repositories"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	"github.com/korallis/letmecook/internal/workflow"
)

type ownerFixture struct {
	hub                                                                                                        *notify.Hub
	s                                                                                                          *store.Store
	state, artifacts, serverCert, serverKey, serverPin, cert, key, owner, runnerCert, runnerKey, runner, brief string
	facts                                                                                                      sc.Eligibility
	profile                                                                                                    r.Profile
	policy                                                                                                     sc.AdmissionPolicy
}

func fixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, b, err)
	}
	return strings.TrimSpace(string(b))
}
func newOwnerFixture(t *testing.T) *ownerFixture {
	t.Helper()
	clearEnv(t)
	f := &ownerFixture{hub: &notify.Hub{}, policy: sc.AdmissionPolicy{DevelopmentProfiles: []string{"macos-sandbox-exec-dev"}}}
	f.cert, f.key, f.owner = generate(t, false)
	f.runnerCert, f.runnerKey, _ = generate(t, false)
	f.serverCert, f.serverKey, f.serverPin = generate(t, true)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.state, f.artifacts = filepath.Join(root, "state"), filepath.Join(root, "artifacts")
	f.s, err = store.OpenWithOptions(context.Background(), f.state, f.artifacts, store.Options{Admission: f.policy})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.s.Close() })
	ctx := context.Background()
	if err = f.s.BootstrapOwner(ctx, f.owner, false); err != nil {
		t.Fatal(err)
	}
	runnerPin, err := i.ReadCertificate(f.runnerCert)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := f.s.CreateEnrollment(ctx, f.owner, workflow.IntentID(root, "invite"), runnerPin)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := f.s.Enroll(ctx, runnerPin, invite.Token)
	if err != nil {
		t.Fatal(err)
	}
	f.runner = runner.ID
	if _, err = f.s.UpdateIdentity(ctx, f.owner, f.runner, 1, "enable", ""); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "source")
	fixtureGit(t, root, "init", "--quiet", "--template=", "--initial-branch=main", repo)
	if err = os.WriteFile(filepath.Join(repo, "file"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, repo, "add", ".")
	fixtureGit(t, repo, "commit", "--quiet", "-m", "base")
	base := fixtureGit(t, repo, "rev-parse", "HEAD")
	runnerRoot := filepath.Join(root, "runner")
	if err = os.Mkdir(runnerRoot, 0700); err != nil {
		t.Fatal(err)
	}
	remote := url.URL{Scheme: "file", Path: repo}
	f.profile = r.Profile{Version: r.Version, ID: "fixture", Revision: 1, Remote: remote.String(), Base: r.Base{Ref: "refs/heads/main", Commit: base, Policy: "pinned"}, ProtectedPaths: []string{"policy"}, ContextScope: []string{"."}, Verification: r.Verification{Name: "fixture", Commands: []r.Command{{Argv: []string{"/usr/bin/true"}, Directory: ".", TimeoutMS: 1000}}}, RunnerRoots: []r.RunnerRoot{{RunnerID: f.runner, Root: runnerRoot}}}
	if _, err = f.s.RegisterRepository(ctx, f.owner, 0, f.profile); err != nil {
		t.Fatal(err)
	}
	digest, _ := f.profile.Digest()
	hash := strings.Repeat("a", 64)
	rev := g.Revision{Number: 1, SHA256: hash}
	route := g.Route{RouteRef: "worker", ProfileRef: "fixture", RouteRevision: 1, Policy: rev, RouterBuild: hash, GraphDigest: hash, Evidence: rev, Harness: "fake", Protocol: "responses", SettingsDigest: hash, Isolation: "macos-sandbox-exec-dev", LimitsProfile: "strict-provider-output-v1", LimitsAuthority: "operator-strict", Targets: []g.Target{{Provider: "fixture", Model: "fixture", Billing: "subscription"}}}
	now := time.Now().UnixMilli()
	envelope := g.Envelope{Repository: f.profile.ID, BaseCommit: base, Brief: rev, Plan: rev, RouteDecision: rev, CriterionIDs: []string{"c1"}, TaskKinds: []string{"code"}, Paths: []string{"file"}, Operations: []string{"read", "verify", "write"}, Systems: []string{}, Runners: []string{f.runner}, Routes: []g.Route{route}, Selection: "pinned", Budgets: g.Budgets{Requests: 4, Attempts: 2, Subattempts: 8, Retries: 1, Concurrency: 1, RequestBytes: 65536, ResponseBytes: 65536, TotalMS: 120000, AttemptMS: 60000, FirstOutputMS: 30000, IdleMS: 30000, ProviderOutputTokens: 128}, NotBeforeMS: now - 1000, ExpiresMS: now + 3600000}
	f.facts = sc.Eligibility{ID: "fixture-pair", Revision: 1, Repository: r.Selection{Repository: f.profile.ID, Revision: 1, ProfileDigest: digest, Remote: f.profile.Remote, BaseCommit: base, RunnerRoot: f.profile.RunnerRoots[0]}, Enabled: true, RunnerBoot: workflow.IntentID(root, "boot"), LocalPolicy: rev, LocalEnvelope: envelope, Config: rev, Route: route, Capabilities: []string{"tools"}, Isolation: sc.IsolationProfile{ID: "macos-sandbox-exec-dev", Revision: rev, RuntimeDigest: hash, ObservedDigest: hash, Kind: "native", Qualification: "development", Supported: false, Controls: sc.DevelopmentIsolationControls()}, Capacity: sc.Resources{CPU: 1000, MemoryBytes: 4096, DiskBytes: 8192, Processes: 8}, Concurrency: 1, RouterAuthenticated: true, Availability: "unknown", ValidUntilMS: envelope.ExpiresMS, Paths: []sc.PathEvidence{{Target: route.Targets[0], Compatible: true, Capabilities: []string{"tools"}, ContextTokens: 8192, LocalBounds: true, ProviderOutputBound: true, ProviderCostBound: true}}}
	if err = f.s.PublishEligibility(ctx, f.owner, 0, f.facts); err != nil {
		t.Fatal(err)
	}
	f.brief = filepath.Join(root, "brief")
	if err = os.WriteFile(f.brief, []byte("change fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *ownerFixture) serve(t *testing.T, policy sc.AdmissionPolicy) (string, *http.Client) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	var err error
	ts.TLS, err = i.ServerTLS(f.serverCert, f.serverKey)
	if err != nil {
		t.Fatal(err)
	}
	d := httpapi.Deps{Store: f.s, Policy: policy, Hub: f.hub}
	worker, err := jobs.NewWorker(context.Background(), f.s, httpapi.WorkflowJobHandlers(d, httpapi.VerificationOptions{StateDir: f.state}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { worker.Close() })
	d.Jobs = worker
	ts.Config.Handler, err = httpapi.NewTLSWithDeps(f.s, "https://"+ts.Listener.Addr().String(), d)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.ErrorLog = log.New(io.Discard, "", 0)
	ts.StartTLS()
	t.Cleanup(ts.Close)
	client, e := ownerClient(globals{Endpoint: ts.URL, Cert: f.cert, Key: f.key, DaemonFingerprint: f.serverPin, Timeout: 10 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	return ts.URL, client
}
func postWorkflow(t *testing.T, client *http.Client, endpoint, path string, body any, want int) ([]byte, http.Header) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", endpoint+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != want {
		t.Fatalf("POST %s got %d want %d: %s (%v)", path, resp.StatusCode, want, out, err)
	}
	return out, resp.Header
}
func mutation(t *testing.T, client *http.Client, endpoint, path string, body map[string]any, want int) []byte {
	t.Helper()
	first, _ := postWorkflow(t, client, endpoint, path, body, want)
	again, h := postWorkflow(t, client, endpoint, path, body, want)
	if !bytes.Equal(first, again) || h.Get("Idempotent-Replay") != "true" {
		t.Fatalf("replay %s differs: %s / %s", path, first, again)
	}
	changed := map[string]any{}
	for k, v := range body {
		changed[k] = v
	}
	changed["version"] = "wrong-version"
	postWorkflow(t, client, endpoint, path, changed, 400)
	// A different typed body under the same UUID conflicts, not a second action.
	for _, key := range []string{"reason", "brief", "attempt_ms", "allow_development_isolation", "expected_revision", "expected_selection", "cause", "expected_grant_id"} {
		if v, ok := changed[key]; ok {
			switch v.(type) {
			case string:
				changed[key] = "changed"
			case bool:
				changed[key] = !v.(bool)
			default:
				changed[key] = int64(123)
			}
			changed["version"] = workflow.Version
			postWorkflow(t, client, endpoint, path, changed, 409)
			break
		}
	}
	postWorkflow(t, client, endpoint, "/api/v1/daemon/pause", map[string]any{"version": workflow.Version, "message_id": body["message_id"], "reason": "conflicting-intent"}, 409)
	return first
}
func commandBody(key string) map[string]any {
	return map[string]any{"version": workflow.Version, "message_id": workflow.IntentID("http-test", key)}
}
func taskBody(f *ownerFixture, key string) map[string]any {
	v := commandBody(key)
	v["repository"] = f.profile.ID
	v["base_commit"] = f.profile.Base.Commit
	v["brief"] = "change fixture"
	v["criteria"] = []workflow.Criterion{{ID: "c1", Text: "passes"}}
	v["paths"] = []string{"file"}
	v["operations"] = []string{"read", "verify", "write"}
	v["harness"] = "fake"
	v["settings"] = json.RawMessage(`{"attempts":[{"mode":"noop","edits":[]}]}`)
	return v
}
func TestWorkflowHTTPSReplayRolesDevelopmentAndStop(t *testing.T) {
	f := newOwnerFixture(t)
	endpoint, client := f.serve(t, f.policy)
	create := taskBody(f, "create")
	taskID := create["message_id"].(string)
	mutation(t, client, endpoint, "/api/v1/tasks", create, 201)
	unapproved := commandBody("unapproved")
	unapproved["grant_id"] = workflow.IntentID("http-test", "not-approved")
	unapproved["grant_revision"] = int64(1)
	unapproved["attempt_ms"] = int64(60000)
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/dispatch", unapproved, 422)
	facts := commandBody("facts")
	facts["expected_revision"] = int64(0)
	facts["facts"] = f.facts
	mutation(t, client, endpoint, "/api/v1/eligibility", facts, 201)
	proposal, err := workflow.BuildGrant(context.Background(), f.s, taskID, f.facts.ID)
	if err != nil {
		t.Fatal(err)
	}
	approve := commandBody("approve")
	approve["eligibility_id"] = f.facts.ID
	approve["expected_grant_id"] = ""
	approve["proposal_digest"] = proposal.Digests["proposal"]
	approve["allow_development_isolation"] = false
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/approve", approve, 422)
	approve["allow_development_isolation"] = true
	mutation(t, client, endpoint, "/api/v1/tasks/"+taskID+"/approve", approve, 201)
	dispatch := commandBody("dispatch")
	dispatch["grant_id"] = approve["message_id"]
	dispatch["grant_revision"] = int64(1)
	dispatch["attempt_ms"] = int64(60000)
	paused := commandBody("before-dispatch-pause")
	paused["reason"] = "admission-test"
	mutation(t, client, endpoint, "/api/v1/daemon/pause", paused, 200)
	inbox, cancelInbox := f.hub.Subscribe("execution.inbox")
	defer cancelInbox()
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/dispatch", dispatch, 409)
	select {
	case <-inbox:
		t.Fatal("refused dispatch woke inbox")
	default:
	}

	resume := commandBody("before-dispatch-resume")
	resume["reason"] = "admission-test"
	mutation(t, client, endpoint, "/api/v1/daemon/resume", resume, 200)
	select {
	case <-inbox:
	case <-time.After(time.Second):
		t.Fatal("resume failed to wake inbox")
	}
	mutation(t, client, endpoint, "/api/v1/tasks/"+taskID+"/dispatch", dispatch, 201)
	select {
	case <-inbox:
	case <-time.After(time.Second):
		t.Fatal("committed dispatch failed to wake inbox")
	}
	task, err := f.s.Task(context.Background(), taskID)
	if err != nil || len(task.Attempts) != 1 || task.Attempts[0].State != "assigned" {
		t.Fatal("not assigned", task, err)
	}
	// All owner routes reject an enrolled enabled runner before decoding bodies.
	runnerClient, e := ownerClient(globals{Endpoint: endpoint, Cert: f.runnerCert, Key: f.runnerKey, DaemonFingerprint: f.serverPin, Timeout: 10 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/api/v1/tasks", "/api/v1/tasks/" + taskID + "/approve", "/api/v1/tasks/" + taskID + "/dispatch", "/api/v1/tasks/" + taskID + "/review", "/api/v1/daemon/pause", "/api/v1/repositories", "/api/v1/eligibility"} {
		postWorkflow(t, runnerClient, endpoint, path, create, 403)
	}
	for _, name := range []string{"pause", "resume"} {
		body := commandBody(name)
		body["reason"] = "test"
		mutation(t, client, endpoint, "/api/v1/daemon/"+name, body, 200)
	}
	stop := commandBody("stop")
	mutation(t, client, endpoint, "/api/v1/tasks/"+taskID+"/stop", stop, 202)
	dispatch["message_id"] = workflow.IntentID("http-test", "after-stop")
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+taskID+"/dispatch", dispatch, 409)
	attemptID := task.Attempts[0].Identity.AttemptID
	mutation(t, client, endpoint, "/api/v1/attempts/"+attemptID+"/cancel", commandBody("cancel"), 202)
	view, err := f.s.AttemptView(context.Background(), attemptID)
	if err != nil {
		t.Fatal(err)
	}
	release := commandBody("release")
	release["proof"] = store.Reconciliation{DispatchID: view.DispatchID, Identity: view.Identity, ExpectedRevision: view.Revision, To: "cancelled", ConfirmedProcess: "not_started", RemoteWork: "quiescent", LaunchFenced: true, ArtifactsPreserved: true, EvidenceDigest: strings.Repeat("a", 64)}
	mutation(t, client, endpoint, "/api/v1/reconcile/"+attemptID+"/release", release, 200)
	// Restriction and invalidation remain explicit revision-gated owner commands.
	extra := taskBody(f, "restricted-create")
	mutation(t, client, endpoint, "/api/v1/tasks", extra, 201)
	extraID := extra["message_id"].(string)
	extraProposal, err := workflow.BuildGrant(context.Background(), f.s, extraID, f.facts.ID)
	if err != nil {
		t.Fatal(err)
	}
	extraApproval := commandBody("restricted-approve")
	extraApproval["expected_grant_id"] = ""
	extraApproval["eligibility_id"] = f.facts.ID
	extraApproval["allow_development_isolation"] = true
	extraApproval["proposal_digest"] = extraProposal.Digests["proposal"]
	mutation(t, client, endpoint, "/api/v1/tasks/"+extraID+"/approve", extraApproval, 201)
	envelope := extraProposal.Grant.Envelope
	envelope.Budgets.Requests--
	restriction := commandBody("restrict")
	restriction["expected_grant_id"] = extraApproval["message_id"]
	restriction["envelope"] = envelope
	mutation(t, client, endpoint, "/api/v1/tasks/"+extraID+"/restrict", restriction, 201)
	invalidate := commandBody("invalidate")
	invalidate["expected_grant_id"] = restriction["message_id"]
	invalidate["reason"] = "revoked"
	mutation(t, client, endpoint, "/api/v1/tasks/"+extraID+"/invalidate", invalidate, 200)
	mutation(t, client, endpoint, "/api/v1/daemon/stop", commandBody("stop-all"), 202)
	// Daemon consent cannot be supplied by a request flag.
	deniedURL, deniedClient := f.serve(t, sc.AdmissionPolicy{})
	other := taskBody(f, "denied-create")
	mutation(t, deniedClient, deniedURL, "/api/v1/tasks", other, 201)
	proposal, err = workflow.BuildGrant(context.Background(), f.s, other["message_id"].(string), f.facts.ID)
	if err != nil {
		t.Fatal(err)
	}
	approve["message_id"] = workflow.IntentID("http-test", "denied-approve")
	approve["proposal_digest"] = proposal.Digests["proposal"]
	postWorkflow(t, deniedClient, deniedURL, "/api/v1/tasks/"+other["message_id"].(string)+"/approve", approve, 422)
	// Repository long operations return durable jobs, and their submissions replay.
	reg := commandBody("repo")
	reg["profile"] = f.profile
	reg["expected_revision"] = int64(0)
	mutation(t, client, endpoint, "/api/v1/repositories", reg, 202)
	validate := commandBody("repo-validate")
	validate["profile"] = f.profile
	validate["expected_revision"] = int64(0)
	mutation(t, client, endpoint, "/api/v1/repositories/validate", validate, 202)
	verify := commandBody("verify")
	verify["expected_selection"] = ""
	mutation(t, client, endpoint, "/api/v1/tasks/"+taskID+"/verify", verify, 202)
	if err = f.s.BootstrapOwner(context.Background(), strings.Repeat("c", 64), true); err != nil {
		t.Fatal(err)
	}
	postWorkflow(t, client, endpoint, "/api/v1/tasks", create, 403)
}

func TestFlowRealHTTPSDaemonAssignedAndBlockedRoute(t *testing.T) {
	f := newOwnerFixture(t)
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "gafferd")
	build := exec.Command("go", "build", "-o", binary, filepath.Join(cwd, "../gafferd"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build daemon: %s %v", out, err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	endpoint := "https://" + addr
	logFile, err := os.CreateTemp(t.TempDir(), "daemon-log")
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd := exec.Command(binary, "--state-dir", f.state, "--artifacts-dir", f.artifacts, "--listen", addr, "--execution-listen", "127.0.0.1:0", "--endpoint", endpoint, "--tls-cert", f.serverCert, "--tls-key", f.serverKey, "--allow-development-profile", "macos-sandbox-exec-dev")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	globalsArgs := []string{"--endpoint", endpoint, "--cert", f.cert, "--key", f.key, "--daemon-fingerprint", f.serverPin, "--json", "--timeout", "15s"}
	for {
		var out, stderr bytes.Buffer
		if run(ctx, append(append([]string{}, globalsArgs...), "identity", "self"), &out, &stderr) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			logFile.Seek(0, 0)
			b, _ := io.ReadAll(logFile)
			t.Fatalf("daemon not ready %s", b)
		case <-time.After(25 * time.Millisecond):
		}
	}
	args := append(append([]string{}, globalsArgs...), "flow", "run", "--repo", f.profile.ID, "--base", f.profile.Base.Commit, "--brief-file", f.brief, "--criterion", "c1=passes", "--path", "file", "--runner", f.runner, "--harness", "fake", "--flow-id", "real-daemon", "--until", "assigned", "--allow-development-isolation")
	var first []transcript
	for n := range 2 {
		var out, stderr bytes.Buffer
		if code := run(ctx, args, &out, &stderr); code != 0 {
			t.Fatalf("flow %d: exit %d stderr %s transcript %s", n, code, stderr.String(), out.String())
		}
		var steps []transcript
		decoder := json.NewDecoder(&out)
		for decoder.More() {
			var row transcript
			if err := decoder.Decode(&row); err != nil {
				t.Fatal(err)
			}
			steps = append(steps, row)
		}
		if len(steps) != 4 {
			t.Fatal("flow steps", len(steps))
		}
		if n == 0 {
			first = steps
		} else {
			for k := range steps {
				a, _ := json.Marshal(first[k].Response)
				b, _ := json.Marshal(steps[k].Response)
				if !bytes.Equal(a, b) {
					t.Fatalf("replay step %s changed", steps[k].Step)
				}
			}
		}
		taskRaw, _ := json.Marshal(steps[3].Response)
		var task store.Task
		if json.Unmarshal(taskRaw, &task) != nil || len(task.Attempts) != 1 || task.Attempts[0].State != "assigned" {
			t.Fatalf("flow did not reach assigned: %s", taskRaw)
		}
	}
	blocked := append([]string{}, args...)
	for n := range blocked {
		if blocked[n] == "fake" {
			blocked[n] = "opencode"
		}
		if blocked[n] == "real-daemon" {
			blocked[n] = "blocked-daemon"
		}
	}
	blocked = append(blocked, "--model", "unconfigured")
	var out, stderr bytes.Buffer
	if code := run(ctx, blocked, &out, &stderr); code != 1 || !strings.Contains(stderr.String(), `"code":"no_eligible_tuple"`) {
		t.Fatal("blocked route", code, out.String(), stderr.String())
	}
	t.Log("real gafferd HTTPS create→approve→dispatch reached assigned; exact flow replay retained one attempt; blocked route refused")
}

func TestWorkflowJSONGoldenAndExitCodes(t *testing.T) {
	for _, tc := range []struct {
		status       int
		code, detail string
		exit         int
	}{{422, "execution_not_approved", "grant", 1}, {409, "identity_conflict", "message_id", 1}, {400, "usage", "scope", 2}, {503, "store_unavailable", "gateway", 3}, {409, "reconciliation_required", "attempt", 4}} {
		var out, stderr bytes.Buffer
		g := globals{JSON: true, Out: &out, Err: &stderr}
		got := fail(g, tc.status, tc.code, tc.detail)
		want := fmt.Sprintf("{\"version\":\"gaffer-cli-v1\",\"error\":{\"status\":%d,\"code\":%q,\"detail\":%q}}\n", tc.status, tc.code, tc.detail)
		if got != tc.exit || stderr.String() != want || out.Len() != 0 {
			t.Fatalf("exit/golden %d %s", got, stderr.String())
		}
	}
	var out, stderr bytes.Buffer
	g := globals{JSON: true, MessageID: testMessageID, Out: &out, Err: &stderr}
	if code := emit(g, "task.create", json.RawMessage(`{"task_id":"fixture"}`)); code != 0 {
		t.Fatal(code)
	}
	want := "{\"version\":\"gaffer-cli-v1\",\"command\":\"task.create\",\"message_id\":\"" + testMessageID + "\",\"result\":{\"task_id\":\"fixture\"}}\n"
	if out.String() != want || stderr.Len() != 0 {
		t.Fatal(out.String(), stderr.String())
	}
	if code := usage(g, "scope required"); code != 2 {
		t.Fatal("usage exit", code)
	}
}

// This transport fixture tests the CLI contract only. Store tests separately
// prove that an override cannot bypass RecordLocalDecision's verified gate.
func TestReviewOverrideGoldenStillRefused(t *testing.T) {
	f := newOwnerFixture(t)
	taskID := workflow.IntentID("review", "task")
	task := store.Task{Brief: store.TaskBrief{TaskID: taskID, Criteria: []store.Criterion{{ID: "c1", Text: "passes"}}}, Phase: "awaiting_review"}
	requested := false
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/tasks/"+taskID:
			json.NewEncoder(w).Encode(task)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/verification"):
			fmt.Fprintf(w, `{"report":{"id":%q,"candidate":{"selection_id":%q},"checks":{"checks":[]},"evidence":[],"limitations":["unqualified"]},"status":{"verified":false,"reasons":["unqualified"]}}`, workflow.IntentID("review", "report"), workflow.IntentID("review", "selection"))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/review"):
			raw, _ := io.ReadAll(r.Body)
			requested = strings.Contains(string(raw), "owner override requested: audit only")
			w.WriteHeader(422)
			io.WriteString(w, `{"version":"workflow-provisional-v1","error":"verification_required","detail":"verification_id"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	var err error
	ts.TLS, err = i.ServerTLS(f.serverCert, f.serverKey)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.ErrorLog = log.New(io.Discard, "", 0)
	ts.StartTLS()
	defer ts.Close()
	args := []string{"--endpoint", ts.URL, "--cert", f.cert, "--key", f.key, "--daemon-fingerprint", f.serverPin, "--json", "review", "accept", taskID, "--override-with-reason", "audit only"}
	var out, stderr bytes.Buffer
	if code := run(context.Background(), args, &out, &stderr); code != 1 || out.Len() != 0 || !requested {
		t.Fatal("override bypass", code, out.String(), stderr.String(), requested)
	}
	const want = "{\"version\":\"gaffer-cli-v1\",\"error\":{\"status\":422,\"code\":\"verification_required\",\"detail\":\"verification_id\"}}\n"
	if stderr.String() != want {
		t.Fatal("override golden", stderr.String())
	}
}

// A database failure after task commit but before owner receipt must not be
// acknowledged; retry recovers from the immutable domain intent key.
func TestWorkflowLostReceiptRecoversCommittedTask(t *testing.T) {
	f := newOwnerFixture(t)
	endpoint, client := f.serve(t, f.policy)
	db, err := sql.Open("sqlite", filepath.Join(f.state, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TRIGGER fail_owner_receipt BEFORE INSERT ON owner_commands BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	body := taskBody(f, "lost-receipt")
	postWorkflow(t, client, endpoint, "/api/v1/tasks", body, 503)
	task, err := f.s.Task(context.Background(), body["message_id"].(string))
	if err != nil {
		t.Fatal("domain did not commit", err)
	}
	if _, err = db.Exec("DROP TRIGGER fail_owner_receipt"); err != nil {
		t.Fatal(err)
	}
	response := mutation(t, client, endpoint, "/api/v1/tasks", body, 201)
	var replay store.Task
	if json.Unmarshal(response, &replay) != nil || replay.Brief.CreatedMS != task.Brief.CreatedMS {
		t.Fatal("lost receipt recreated task")
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM task_briefs").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
