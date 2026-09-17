package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
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
	"reflect"
	"strings"
	"testing"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/notify"
	r "github.com/korallis/letmecook/internal/repositories"
	"github.com/korallis/letmecook/internal/runstream"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
	_ "modernc.org/sqlite"
)

func newTestID() string {
	b := [16]byte{}
	rand.Read(b[:])
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

// executionHarness is a store with an enrolled, enabled runner and one admitted
// dispatch, built only through exported store APIs the way gafferd's owners do.
type executionHarness struct {
	s             *store.Store
	root          string
	owner, runner tls.Certificate
	runnerID      string
	facts         sc.Eligibility
	grant         g.Grant
	request       store.DispatchRequest
	d             store.Dispatch
	hub           *notify.Hub
}

func newExecutionHarness(t *testing.T) *executionHarness {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(root, "state"), filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	owner, _, _ := certificate(t, false)
	if err := s.BootstrapOwner(ctx, pin(t, owner), false); err != nil {
		t.Fatal(err)
	}
	runner, _, _ := certificate(t, false)
	invite, err := s.CreateEnrollment(ctx, pin(t, owner), newTestID(), pin(t, runner))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := s.Enroll(ctx, pin(t, runner), invite.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateIdentity(ctx, pin(t, owner), principal.ID, principal.Revision, "enable", ""); err != nil {
		t.Fatal(err)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(base, "remote.git")
	gitRun(t, base, "init", "--quiet", "--template=", "--initial-branch=main", remote)
	if err := os.WriteFile(filepath.Join(remote, "file"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, remote, "add", ".")
	gitRun(t, remote, "commit", "--quiet", "-m", "base")
	sha := gitRun(t, remote, "rev-parse", "HEAD")
	runnerRoot := filepath.Join(base, "runner")
	if err := os.Mkdir(runnerRoot, 0700); err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "file", Path: remote}
	profile := r.Profile{Version: r.Version, ID: "fixture", Revision: 1, Remote: u.String(), Base: r.Base{Ref: "refs/heads/main", Commit: sha, Policy: "pinned"}, ProtectedPaths: []string{"policy"}, ContextScope: []string{"."}, Verification: r.Verification{Name: "fixture", Commands: []r.Command{{Argv: []string{"go", "test", "./..."}, Directory: ".", TimeoutMS: 1000}}}, RunnerRoots: []r.RunnerRoot{{RunnerID: principal.ID, Root: runnerRoot}}}
	if _, err := s.RegisterRepository(ctx, pin(t, owner), 0, profile); err != nil {
		t.Fatal(err)
	}
	digest, err := profile.Digest()
	if err != nil {
		t.Fatal(err)
	}
	selection := r.Selection{Repository: profile.ID, Revision: profile.Revision, ProfileDigest: digest, Remote: profile.Remote, BaseCommit: profile.Base.Commit, RunnerRoot: profile.RunnerRoots[0]}
	hash := strings.Repeat("a", 64)
	rev := g.Revision{Number: 1, SHA256: hash}
	route := g.Route{RouteRef: "worker", ProfileRef: "default", RouteRevision: 1, Policy: rev, RouterBuild: hash, GraphDigest: hash, Evidence: rev, Harness: "fixture-harness", Protocol: "responses", SettingsDigest: hash, Isolation: "fixture-isolation", LimitsProfile: "strict-provider-output-v1", LimitsAuthority: "operator-strict", Targets: []g.Target{{Provider: "provider-a", Model: "model-a", Billing: "subscription"}, {Provider: "provider-b", Model: "model-b", Billing: "metered"}}}
	now := time.Now().UnixMilli()
	cost := int64(1000)
	grant := g.Grant{ID: newTestID(), TaskID: newTestID(), Revision: 1, Actor: "operator", Envelope: g.Envelope{Repository: profile.ID, BaseCommit: sha, Brief: rev, Plan: rev, RouteDecision: rev, CriterionIDs: []string{"c1", "c2"}, TaskKinds: []string{"code"}, Paths: []string{"a.txt", "src/main.go"}, Operations: []string{"read", "verify", "write"}, Systems: []string{"fixture-search"}, Runners: []string{principal.ID}, Routes: []g.Route{route}, Selection: "pinned", Budgets: g.Budgets{Requests: 5, Attempts: 3, Subattempts: 10, Retries: 2, Concurrency: 1, RequestBytes: 1024, ResponseBytes: 4096, TotalMS: 60000, AttemptMS: 30000, FirstOutputMS: 5000, IdleMS: 5000, ProviderOutputTokens: 128, ProviderCostMicros: &cost}, NotBeforeMS: now - 1000, ExpiresMS: now + 3600000}}
	clone := func(v g.Grant) g.Grant {
		b, _ := json.Marshal(v)
		var out g.Grant
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	facts := sc.Eligibility{ID: "fixture-pair", Revision: 1, Repository: selection, Enabled: true, RunnerBoot: newTestID(), LocalPolicy: rev, LocalEnvelope: clone(grant).Envelope, Config: rev, Route: route, Capabilities: []string{"tools", "vision"}, Isolation: sc.IsolationProfile{ID: "fixture-isolation", Revision: rev, RuntimeDigest: hash, ObservedDigest: hash, Kind: "dedicated-vm", Supported: true, Controls: sc.RequiredIsolationControls()}, Capacity: sc.Resources{CPU: 1000, MemoryBytes: 4096, DiskBytes: 8192, Processes: 8}, Concurrency: 1, RouterAuthenticated: true, Availability: "unknown", ValidUntilMS: grant.Envelope.ExpiresMS}
	for _, target := range route.Targets {
		facts.Paths = append(facts.Paths, sc.PathEvidence{Target: target, Compatible: true, Capabilities: []string{"tools", "vision"}, ContextTokens: 8192, LocalBounds: true, ProviderOutputBound: true, ProviderCostBound: true})
	}
	request := store.DispatchRequest{ID: newTestID(), Decision: sc.Decision{ID: newTestID(), Revision: 1, Assessment: sc.Assessment{TaskID: grant.TaskID, Brief: rev, Plan: rev, ContextDigest: hash, Role: "worker", WorkClass: "code", Ambiguity: "low", Consequence: "high", Required: []string{"tools", "vision"}, Preferences: []string{}, EvidenceRefs: []string{"operator-brief"}, Unknowns: []string{"subscription-headroom"}, ContextTokens: 4096, AssessorVersion: "operator-v1", SelectorVersion: "deterministic-v1"}, EligibilityID: facts.ID, Selected: route, RankedAlternatives: []string{}, Authorization: "operator", Reason: "pinned", Resources: sc.Resources{CPU: 500, MemoryBytes: 2048, DiskBytes: 4096, Processes: 4}}}
	factsDigest, err := sc.Digest(facts)
	if err != nil {
		t.Fatal(err)
	}
	request.Decision.Eligibility = g.Revision{Number: 1, SHA256: factsDigest}
	decisionDigest, err := sc.Digest(request.Decision)
	if err != nil {
		t.Fatal(err)
	}
	grant.Envelope.RouteDecision = g.Revision{Number: 1, SHA256: decisionDigest}
	request.Request = g.Request{GrantID: grant.ID, TaskID: grant.TaskID, GrantRevision: 1, Action: "execute", Envelope: clone(grant).Envelope}
	request.Allowance = request.Request.Envelope.Budgets
	b := &request.Allowance
	b.Attempts, b.Retries, b.Concurrency, b.TotalMS = 1, 0, 1, 10000
	b.Requests, b.Subattempts, b.AttemptMS, b.ProviderOutputTokens = 1, 2, 10000, 32
	allowance := int64(100)
	b.ProviderCostMicros = &allowance
	if err := s.PublishEligibility(ctx, pin(t, owner), 0, facts); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveExecution(ctx, "", grant); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	return &executionHarness{s: s, root: root, owner: owner, runner: runner, runnerID: principal.ID, facts: facts, grant: grant, request: request, d: d, hub: &notify.Hub{}}
}

func (h *executionHarness) hello() execwire.Hello {
	return execwire.Hello{Version: execwire.Version, MessageID: newTestID(), RunnerBoot: h.facts.RunnerBoot, EligibilityID: h.facts.ID, EligibilityRevision: 1, PolicyDigest: strings.Repeat("a", 64), Journals: []execwire.Journal{}}
}

func (h *executionHarness) sessionID(t *testing.T) string {
	t.Helper()
	sess, err := h.s.RunnerSession(context.Background(), pin(t, h.runner), store.HelloRecord{Version: execwire.Version, MessageID: newTestID(), RunnerBoot: h.facts.RunnerBoot, EligibilityID: h.facts.ID, EligibilityRevision: 1, PolicyDigest: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	return sess.SessionID
}

// serve runs the execution route set on a real ExecutionListener and returns a
// runner-authenticated HTTP/1.1 client pinned to the execution ALPN.
func (h *executionHarness) serve(t *testing.T) (*http.Client, string) {
	t.Helper()
	serverCert, certFile, keyFile := certificate(t, true)
	cfg, err := i.ExecutionTLS(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := ExecutionListener(raw, cfg, 5*time.Second)
	handler, err := NewExecution(h.s, Deps{Store: h.s, Hub: h.hub})
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ConnContext: ExecutionConnContext, ReadHeaderTimeout: 5 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Close(); <-done })
	roots := x509.NewCertPool()
	roots.AddCert(serverCert.Leaf)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{h.runner}, NextProtos: []string{p.FencedVersion}}, ForceAttemptHTTP2: false, Proxy: nil}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(transport.CloseIdleConnections)
	return client, "https://" + listener.Addr().String()
}

type wireResponse struct {
	status int
	body   []byte
}

func (w wireResponse) code(t *testing.T) string {
	t.Helper()
	var body execwire.ErrorBody
	if err := json.Unmarshal(w.body, &body); err != nil {
		t.Fatalf("not an error body: %d %s", w.status, w.body)
	}
	if body.Version != execwire.Version {
		t.Fatalf("error envelope version: %s", w.body)
	}
	return body.Error
}

func (w wireResponse) decode(t *testing.T, dst any) {
	t.Helper()
	if w.status < 200 || w.status > 299 {
		t.Fatalf("status %d: %s", w.status, w.body)
	}
	if err := execwire.Decode(w.body, dst); err != nil {
		t.Fatalf("reply %s does not decode as closed JSON: %v", w.body, err)
	}
}

func call(t *testing.T, client *http.Client, base, method, path, session string, body []byte) wireResponse {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if session != "" {
		req.Header.Set("X-Gaffer-Session", session)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return wireResponse{res.StatusCode, b}
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (h *executionHarness) transition(t *testing.T, to p.AttemptState, evidence execwire.Evidence) execwire.MessageEnvelope {
	t.Helper()
	st, err := h.s.ExecutionState(context.Background(), pin(t, h.runner), h.sessionID(t), h.d.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision := st.Revision
	m := p.Message{Version: p.FencedVersion, Kind: "transition", MessageID: newTestID(), Identity: h.d.Assignment.Identity, ExpectedRevision: &revision, From: st.AttemptState, To: to}
	return execwire.MessageEnvelope{Version: execwire.Version, MessageID: m.MessageID, DispatchID: h.d.ID, Message: m, Evidence: &evidence}
}

func spooled(t *testing.T, identity p.Identity, texts ...string) []runstream.Record {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spool, err := runstream.CreateSpool(filepath.Join(base, "spool"), identity, runstream.MinLimit)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	out := []runstream.Record{}
	for _, text := range texts {
		rec, err := spool.Append(runstream.Native{Version: "fixture", Kind: "text", Data: []byte(text)}, runstream.Normalized{Stream: "stdout", Text: text})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rec)
	}
	return out
}

func (h *executionHarness) manifest(t *testing.T, outcome string, files map[string]string) (execwire.UploadBegin, map[string][]byte) {
	t.Helper()
	identity := h.d.Assignment.Identity
	m := store.CandidateManifest{Version: "gaffer-artifact-manifest-v1", Identity: identity, Base: store.ArtifactBase{Revision: h.grant.Envelope.BaseCommit, SHA256: sha256Hex([]byte(h.grant.Envelope.BaseCommit))}, Outcome: outcome, Tracked: []store.ArtifactBlob{}, Untracked: []store.ArtifactBlob{}, Binary: []store.ArtifactBlob{}, Recovery: []store.ArtifactBlob{}, Deleted: []string{}}
	blobs := map[string][]byte{}
	for name, content := range files {
		body := []byte(content)
		m.Tracked = append(m.Tracked, store.ArtifactBlob{Path: name, SHA256: sha256Hex(body), Bytes: int64(len(body))})
		blobs[sha256Hex(body)] = body
	}
	raw := encode(t, m)
	result := p.Message{Version: p.FencedVersion, Kind: "result", MessageID: newTestID(), Identity: identity, Manifest: &p.Manifest{ManifestID: newTestID(), SHA256: sha256Hex(raw), Bytes: int64(len(raw))}}
	return execwire.UploadBegin{Version: execwire.Version, MessageID: newTestID(), Result: result, ManifestBase64: base64.StdEncoding.EncodeToString(raw)}, blobs
}

func TestExecutionRoutesEndToEndOverTLS(t *testing.T) {
	h := newExecutionHarness(t)
	client, base := h.serve(t)
	attempt := h.d.Assignment.Identity.AttemptID
	// Session: the only route without X-Gaffer-Session.
	var sess execwire.Session
	call(t, client, base, "POST", "/x/v1/session", "", encode(t, h.hello())).decode(t, &sess)
	if sess.Mode != "normal" || sess.RunnerID != h.runnerID || sess.LeaseValidityMS != 20000 || sess.DriftMS != 2000 || sess.TerminationMS != 5000 || sess.RenewEveryMS != 5000 || sess.Paused || !p.ValidID(sess.SessionID) {
		t.Fatalf("session: %+v", sess)
	}
	session := sess.SessionID
	if got := call(t, client, base, "GET", "/x/v1/state?dispatch_id="+h.d.ID, newTestID(), nil); got.status != 409 || got.code(t) != "session_stale" {
		t.Fatal("unknown session accepted", got.status, string(got.body))
	}
	// Input: no brief is retained yet, so the dispatch cannot be described.
	if got := call(t, client, base, "GET", "/x/v1/input?dispatch_id="+h.d.ID, session, nil); got.status != 409 || got.code(t) != "reconciliation_required" {
		t.Fatal(got.status, string(got.body))
	}
	if got := call(t, client, base, "GET", "/x/v1/input?dispatch_id="+newTestID(), session, nil); got.status != 404 || got.code(t) != "not_found" {
		t.Fatal(got.status, string(got.body))
	}
	// Inbox delivers the retained assignment until it is acknowledged.
	var inbox execwire.Inbox
	call(t, client, base, "GET", "/x/v1/inbox", session, nil).decode(t, &inbox)
	if len(inbox.Assignments) != 1 || inbox.Assignments[0].ID != h.d.ID || !reflect.DeepEqual(inbox.Assignments[0].Assignment, h.d.Assignment) || len(inbox.Cancels) != 0 || inbox.Paused || inbox.PollAfterMS != 500 {
		t.Fatalf("inbox: %v", inbox.Assignments)
	}
	accept := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newTestID(), Identity: h.d.Assignment.Identity, AssignmentID: h.d.Assignment.AssignmentID, RunnerBoot: h.facts.RunnerBoot, DaemonBoot: sess.DaemonBoot}
	var reply struct {
		Outcome string     `json:"outcome"`
		Message *p.Message `json:"message,omitempty"`
	}
	call(t, client, base, "POST", "/x/v1/messages", session, encode(t, execwire.MessageEnvelope{Version: execwire.Version, MessageID: accept.MessageID, DispatchID: h.d.ID, Message: accept})).decode(t, &reply)
	if reply.Outcome != "acknowledged" {
		t.Fatal(reply)
	}
	call(t, client, base, "GET", "/x/v1/inbox", session, nil).decode(t, &inbox)
	if len(inbox.Assignments) != 0 {
		t.Fatal("acknowledged assignment redelivered")
	}
	// Lease.
	sent := time.Now().UnixMilli()
	request := p.Message{Version: p.FencedVersion, Kind: "lease_request", MessageID: newTestID(), Identity: h.d.Assignment.Identity, Nonce: newTestID(), RunnerBoot: h.facts.RunnerBoot, DaemonBoot: sess.DaemonBoot, SentMS: &sent}
	leased := call(t, client, base, "POST", "/x/v1/lease", session, encode(t, execwire.LeaseEnvelope{Version: execwire.Version, MessageID: newTestID(), DispatchID: h.d.ID, Request: request}))
	lease, err := p.Decode(leased.body)
	if leased.status != 200 || err != nil {
		t.Fatalf("lease: %d %s %v", leased.status, leased.body, err)
	}
	if lease.Kind != "lease_reply" || lease.Nonce != request.Nonce || *lease.ValidityMS != 20000 || p.CheckLease(request, lease, h.d.Assignment.Identity, p.Timing{RunnerBoot: h.facts.RunnerBoot, DaemonBoot: sess.DaemonBoot, ReceivedMS: sent, DriftMS: 2000, TerminationMS: 5000, NonceActive: true}).Reason != p.OK {
		t.Fatalf("lease: %+v", lease)
	}
	// Transitions with evidence.
	call(t, client, base, "POST", "/x/v1/messages", session, encode(t, h.transition(t, p.Starting, execwire.Evidence{Kind: "launch_intent", Workspace: "/private/tmp/ws", BoundaryPort: 40001, Nonce: request.Nonce}))).decode(t, &reply)
	if reply.Outcome != "applied" || reply.Message == nil || reply.Message.To != p.Starting {
		t.Fatal(reply)
	}
	launched := execwire.Evidence{Kind: "launched", GuardianPID: 100, PID: 101, PGID: 101, StartUnixNS: 1700000000000000001}
	call(t, client, base, "POST", "/x/v1/messages", session, encode(t, h.transition(t, p.Running, launched))).decode(t, &reply)
	// Streams: contiguous batches acknowledged after durable append; gaps and
	// conflicts refused with the expected sequence; duplicates replayed.
	records := spooled(t, h.d.Assignment.Identity, "one", "two", "three")
	var ack execwire.StreamAck
	call(t, client, base, "POST", "/x/v1/streams/"+attempt, session, encode(t, execwire.StreamBatch{Version: execwire.Version, Records: records[:2]})).decode(t, &ack)
	if ack.Through != 2 || ack.Expected != 3 || ack.Bytes != 6 {
		t.Fatal(ack)
	}
	got := call(t, client, base, "POST", "/x/v1/streams/"+attempt, session, encode(t, execwire.StreamBatch{Version: execwire.Version, Records: []runstream.Record{records[2], records[2]}}))
	if got.status != 400 {
		t.Fatal("non-contiguous batch accepted", got.status, string(got.body))
	}
	skipped := spooled(t, h.d.Assignment.Identity, "one", "two", "three", "four")
	got = call(t, client, base, "POST", "/x/v1/streams/"+attempt, session, encode(t, execwire.StreamBatch{Version: execwire.Version, Records: skipped[3:]}))
	var body execwire.ErrorBody
	if err := json.Unmarshal(got.body, &body); err != nil || got.status != 409 || body.Error != "stream_sequence_gap" || body.Detail != "3" {
		t.Fatal("gap", got.status, string(got.body))
	}
	changed := records[1]
	changed.Native.Data = []byte("TWO")
	changed.Digest = ""
	changed.Digest = sha256Hex(nil) // wrong digest is refused as invalid before the sink is consulted
	if got := call(t, client, base, "POST", "/x/v1/streams/"+attempt, session, encode(t, execwire.StreamBatch{Version: execwire.Version, Records: []runstream.Record{changed}})); got.status != 409 || got.code(t) != "stream_record_conflict" {
		t.Fatal("tampered record", got.status, string(got.body))
	}
	call(t, client, base, "POST", "/x/v1/streams/"+attempt, session, encode(t, execwire.StreamBatch{Version: execwire.Version, Records: records[:2]})).decode(t, &ack)
	if ack.Through != 2 {
		t.Fatal("duplicate ack", ack)
	}
	call(t, client, base, "POST", "/x/v1/streams/"+attempt, session, encode(t, execwire.StreamBatch{Version: execwire.Version, Records: records[2:]})).decode(t, &ack)
	if ack.Through != 3 {
		t.Fatal(ack)
	}
	exit := execwire.Evidence{Kind: "exit", Code: 0, PGID: 101, PGIDEmpty: true, ObservedUnixNS: 1700000000000000002, StreamThrough: 3}
	call(t, client, base, "POST", "/x/v1/messages", session, encode(t, h.transition(t, p.ResultPending, exit))).decode(t, &reply)
	if reply.Message == nil || reply.Message.To != p.ResultPending {
		t.Fatal(reply)
	}
	// Uploads: begin, exact-length PUT, digest mismatch, duplicate, commit replay.
	begin, blobs := h.manifest(t, "succeeded", map[string]string{"greeting.txt": "hello, gaffer\n"})
	var upload execwire.UploadSession
	got = call(t, client, base, "POST", "/x/v1/attempts/"+attempt+"/uploads", session, encode(t, begin))
	if got.status != 201 {
		t.Fatal(got.status, string(got.body))
	}
	got.decode(t, &upload)
	if len(upload.Missing) != 1 || upload.BytesAllowed != 256<<20 {
		t.Fatal(upload)
	}
	digest := upload.Missing[0].SHA256
	blobPath := "/x/v1/uploads/" + upload.UploadID + "/blobs/" + digest
	if got := call(t, client, base, "PUT", blobPath, session, []byte("hello, world!\n")); got.status != 422 || got.code(t) != "digest_mismatch" {
		t.Fatal("wrong bytes accepted", got.status, string(got.body))
	}
	if got := call(t, client, base, "PUT", blobPath, session, blobs[digest]); got.status != 201 {
		t.Fatal(got.status, string(got.body))
	}
	got = call(t, client, base, "PUT", blobPath, session, blobs[digest])
	var stored store.UploadedBlob
	if got.status != 200 || json.Unmarshal(got.body, &stored) != nil || !stored.Duplicate {
		t.Fatal("duplicate PUT", got.status, string(got.body))
	}
	commitPath := "/x/v1/uploads/" + upload.UploadID + "/commit"
	first := call(t, client, base, "POST", commitPath, session, []byte(`{"version":"`+execwire.Version+`","message_id":"`+newTestID()+`"}`))
	var committed execwire.CommitReply
	first.decode(t, &committed)
	if committed.Quarantined || p.CheckAck(begin.Result, committed.Ack, begin.Result.Identity, committed.Receipt) != p.OK {
		t.Fatalf("commit: %s", first.body)
	}
	second := call(t, client, base, "POST", commitPath, session, []byte(`{"version":"`+execwire.Version+`","message_id":"`+newTestID()+`"}`))
	if second.status != 200 || !bytes.Equal(first.body, second.body) {
		t.Fatal("commit replay not byte-identical", string(second.body))
	}
	// Usage receipts, then finalize.
	usage := execwire.Usage{Version: execwire.Version, MessageID: newTestID(), Identity: h.d.Assignment.Identity, Receipts: []inference.Receipt{{RequestID: "req-1", Protocol: "openai-chat", Model: "model-a", Status: 200, PromptTokens: 3, CompletionTokens: 4, Started: time.Now().UTC(), Ended: time.Now().UTC(), Terminal: true, Source: "boundary"}}}
	call(t, client, base, "POST", "/x/v1/usage", session, encode(t, usage)).decode(t, &reply)
	if reply.Outcome != "recorded" {
		t.Fatal(reply)
	}
	streamDigest := runstream.StreamDigest(records)
	completion := execwire.Completion{Version: execwire.Version, MessageID: newTestID(), ReceiptID: committed.Receipt.ReceiptID, Stream: execwire.StreamCompletion{Through: 3, Digest: streamDigest}, Exit: execwire.Exit{Code: 0, PGID: 101, ObservedUnixNS: exit.ObservedUnixNS}}
	completion.Boundary.Quiescent = true
	incomplete := completion
	incomplete.Stream.Through = 4
	if got := call(t, client, base, "POST", "/x/v1/attempts/"+attempt+"/finalize", session, encode(t, incomplete)); got.status != 409 || got.code(t) != "reconciliation_required" {
		t.Fatal("finalized beyond the sink", got.status, string(got.body))
	}
	var finalized store.FinalizeReply
	call(t, client, base, "POST", "/x/v1/attempts/"+attempt+"/finalize", session, encode(t, completion)).decode(t, &finalized)
	if finalized.Outcome != "succeeded" || !finalized.Released {
		t.Fatal(finalized)
	}
	var state execwire.State
	call(t, client, base, "GET", "/x/v1/state?dispatch_id="+h.d.ID, session, nil).decode(t, &state)
	if state.AttemptState != p.Succeeded || !state.Released || !state.Acknowledged || state.Stream.Through != 3 || state.ReceiptID != committed.Receipt.ReceiptID || state.Head == nil || state.Head.AttemptID != attempt || state.LastLease == nil || state.LastLease.Nonce != request.Nonce || state.StopTargets == nil {
		t.Fatalf("state: %s", string(encode(t, state)))
	}
	if got := call(t, client, base, "POST", "/x/v1/streams/"+attempt, session, encode(t, execwire.StreamBatch{Version: execwire.Version, Records: records[:1]})); got.status != 409 || got.code(t) != "stale_attempt" {
		t.Fatal("terminal attempt streamed", got.status, string(got.body))
	}
	// A daemon-side session check survives across the same TLS connection: a
	// revoked runner is refused on its next request.
	principal, err := h.s.Authenticate(context.Background(), pin(t, h.runner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.UpdateIdentity(context.Background(), pin(t, h.owner), h.runnerID, principal.Revision, "revoke", ""); err != nil {
		t.Fatal(err)
	}
	if got := call(t, client, base, "GET", "/x/v1/state?dispatch_id="+h.d.ID, session, nil); got.status != 403 || got.code(t) != "identity_denied" {
		t.Fatal("revoked runner served", got.status, string(got.body))
	}
}

func (h *executionHarness) recorder(t *testing.T) http.Handler {
	t.Helper()
	handler, err := NewExecution(h.s, Deps{Store: h.s, Hub: h.hub})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func (h *executionHarness) do(t *testing.T, handler http.Handler, method, path, session, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := routeRequest(h.runner, true, method, path, body)
	if session == "" {
		req.Header.Del("X-Gaffer-Session")
	} else {
		req.Header.Set("X-Gaffer-Session", session)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestExecutionRouteRefusalsAndErrorEnvelopes(t *testing.T) {
	h := newExecutionHarness(t)
	handler := h.recorder(t)
	session := h.sessionID(t)
	envelope := string(encode(t, execwire.MessageEnvelope{Version: execwire.Version, MessageID: newTestID(), DispatchID: h.d.ID, Message: p.Message{Version: p.FencedVersion, Kind: "terminated", MessageID: newTestID(), Identity: h.d.Assignment.Identity, StopID: newTestID(), RunnerBoot: h.facts.RunnerBoot, DaemonBoot: newTestID(), ConfirmedProcess: "terminated", RemoteWork: "quiescent", EvidenceDigest: strings.Repeat("e", 64)}}))
	sent := time.Now().Add(-time.Minute).UnixMilli()
	delayed := string(encode(t, execwire.LeaseEnvelope{Version: execwire.Version, MessageID: newTestID(), DispatchID: h.d.ID, Request: p.Message{Version: p.FencedVersion, Kind: "lease_request", MessageID: newTestID(), Identity: h.d.Assignment.Identity, Nonce: newTestID(), RunnerBoot: h.facts.RunnerBoot, DaemonBoot: newTestID(), SentMS: &sent}}))
	for _, tc := range []struct {
		name, method, path, session, body string
		status                            int
		code                              string
	}{
		{"session malformed", "POST", "/x/v1/session", "", `{"version":"` + execwire.Version + `"`, 400, "malformed"},
		{"session unknown version", "POST", "/x/v1/session", "", `{"version":"other","message_id":"` + newTestID() + `","runner_boot":"` + h.facts.RunnerBoot + `","eligibility_id":"fixture-pair","eligibility_revision":1,"policy_digest":"","journals":[]}`, 400, "unknown_version"},
		{"session null journals", "POST", "/x/v1/session", "", `{"version":"` + execwire.Version + `","message_id":"` + newTestID() + `","runner_boot":"` + h.facts.RunnerBoot + `","eligibility_id":"fixture-pair","eligibility_revision":1,"policy_digest":"","journals":null}`, 400, "malformed"},
		{"state without query", "GET", "/x/v1/state", session, "", 400, "invalid_query"},
		{"state bad id", "GET", "/x/v1/state?dispatch_id=nope", session, "", 400, "invalid_id"},
		{"state unknown", "GET", "/x/v1/state?dispatch_id=" + newTestID(), session, "", 404, "not_found"},
		{"state stale session", "GET", "/x/v1/state?dispatch_id=" + h.d.ID, newTestID(), "", 409, "session_stale"},
		{"input unknown", "GET", "/x/v1/input?dispatch_id=" + newTestID(), session, "", 404, "not_found"},
		{"inbox bound", "GET", "/x/v1/inbox?wait_ms=25001", session, "", 400, "invalid_bound"},
		{"inbox query", "GET", "/x/v1/inbox?other=1", session, "", 400, "invalid_query"},
		{"messages kind", "POST", "/x/v1/messages", session, `{"version":"` + execwire.Version + `","message_id":"` + newTestID() + `","dispatch_id":"` + h.d.ID + `","message":{"version":"` + p.FencedVersion + `","message_id":"` + newTestID() + `","identity":` + string(encode(t, h.d.Assignment.Identity)) + `,"kind":"result","manifest":{"manifest_id":"` + newTestID() + `","sha256":"` + strings.Repeat("a", 64) + `","bytes":1}}}`, 400, "malformed"},
		{"terminated without measurement", "POST", "/x/v1/messages", session, envelope, 400, "malformed"},
		{"lease wrong boot", "POST", "/x/v1/lease", session, delayed, 409, "boot_mismatch"},
		{"streams bad id", "POST", "/x/v1/streams/nope", session, `{"version":"` + execwire.Version + `","records":[]}`, 400, "invalid_id"},
		{"streams empty", "POST", "/x/v1/streams/" + h.d.Assignment.Identity.AttemptID, session, `{"version":"` + execwire.Version + `","records":[]}`, 400, "malformed"},
		{"uploads bad base64", "POST", "/x/v1/attempts/" + h.d.Assignment.Identity.AttemptID + "/uploads", session, `{"version":"` + execwire.Version + `","message_id":"` + newTestID() + `","result":` + string(encode(t, p.Message{Version: p.FencedVersion, Kind: "result", MessageID: newTestID(), Identity: h.d.Assignment.Identity, Manifest: &p.Manifest{ManifestID: newTestID(), SHA256: strings.Repeat("a", 64), Bytes: 1}})) + `,"manifest_base64":"***"}`, 400, "malformed"},
		{"blob unknown upload", "PUT", "/x/v1/uploads/" + newTestID() + "/blobs/" + strings.Repeat("a", 64), session, "x", 404, "not_found"},
		{"commit unknown", "POST", "/x/v1/uploads/" + newTestID() + "/commit", session, `{"version":"` + execwire.Version + `","message_id":"` + newTestID() + `"}`, 404, "not_found"},
		{"finalize unknown attempt", "POST", "/x/v1/attempts/" + newTestID() + "/finalize", session, string(encode(t, execwire.Completion{Version: execwire.Version, MessageID: newTestID(), ReceiptID: newTestID(), Stream: execwire.StreamCompletion{Digest: strings.Repeat("a", 64)}})), 409, "stale_attempt"},
		{"usage malformed", "POST", "/x/v1/usage", session, `{"version":"` + execwire.Version + `","message_id":"` + newTestID() + `","identity":` + string(encode(t, h.d.Assignment.Identity)) + `,"receipts":null}`, 400, "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := h.do(t, handler, tc.method, tc.path, tc.session, tc.body)
			assertRouteError(t, w, tc.status, tc.code)
			var body execwire.ErrorBody
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Version != execwire.Version {
				t.Fatalf("error envelope: %s", w.Body.String())
			}
		})
	}
	// The delayed lease request was fenced before its refusal; the refusal is
	// durable even though the reply carried nothing else.
	w := h.do(t, handler, "POST", "/x/v1/lease", session, delayed)
	assertRouteError(t, w, 409, "boot_mismatch")
	// A foreign runner gets 403 for another runner's dispatch.
	other, _, _ := certificate(t, false)
	invite, err := h.s.CreateEnrollment(context.Background(), pin(t, h.owner), newTestID(), pin(t, other))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := h.s.Enroll(context.Background(), pin(t, other), invite.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.UpdateIdentity(context.Background(), pin(t, h.owner), principal.ID, principal.Revision, "enable", ""); err != nil {
		t.Fatal(err)
	}
	otherSession, err := h.s.RunnerSession(context.Background(), pin(t, other), store.HelloRecord{Version: execwire.Version, MessageID: newTestID(), RunnerBoot: newTestID(), EligibilityID: "fixture-pair", EligibilityRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	req := routeRequest(other, true, "GET", "/x/v1/input?dispatch_id="+h.d.ID, "")
	req.Header.Set("X-Gaffer-Session", otherSession.SessionID)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assertRouteError(t, w, 403, "runner_disabled")
	req = routeRequest(other, true, "GET", "/x/v1/state?dispatch_id="+h.d.ID, "")
	req.Header.Set("X-Gaffer-Session", otherSession.SessionID)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assertRouteError(t, w, 403, "runner_disabled")
	// The other runner's session is not this runner's session.
	w = h.do(t, handler, "GET", "/x/v1/state?dispatch_id="+h.d.ID, otherSession.SessionID, "")
	assertRouteError(t, w, 409, "session_stale")
}

func TestInboxLongPollWakesOnNotifyAndReportsPaused(t *testing.T) {
	h := newExecutionHarness(t)
	handler := h.recorder(t)
	session := h.sessionID(t)
	ctx := context.Background()
	accept := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newTestID(), Identity: h.d.Assignment.Identity, AssignmentID: h.d.Assignment.AssignmentID, RunnerBoot: h.facts.RunnerBoot, DaemonBoot: h.d.Assignment.DaemonBoot}
	sess, err := h.s.ExecutionSession(ctx, pin(t, h.runner), session)
	if err != nil {
		t.Fatal(err)
	}
	accept.DaemonBoot = sess.DaemonBoot
	if err := h.s.AcknowledgeAssignment(ctx, pin(t, h.runner), h.d.ID, accept); err != nil {
		t.Fatal(err)
	}
	// Nothing pending: a bounded wait returns empty at its deadline.
	started := time.Now()
	w := h.do(t, handler, "GET", "/x/v1/inbox?wait_ms=300", session, "")
	var inbox execwire.Inbox
	if w.Code != 200 || execwire.Decode(w.Body.Bytes(), &inbox) != nil || len(inbox.Assignments)+len(inbox.Cancels) != 0 || time.Since(started) < 250*time.Millisecond {
		t.Fatalf("idle long poll: %d %s after %v", w.Code, w.Body.String(), time.Since(started))
	}
	// A cancel latched while waiting wakes the poll through the hub.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- h.do(t, handler, "GET", "/x/v1/inbox?wait_ms=10000", session, "") }()
	time.Sleep(200 * time.Millisecond)
	stop := c.Request{ID: newTestID(), Kind: c.CancelAttempt, TaskID: h.d.Assignment.Identity.TaskID, AttemptID: h.d.Assignment.Identity.AttemptID, Cause: "operator"}
	started = time.Now()
	if _, err := h.s.RequestStop(ctx, pin(t, h.owner), stop); err != nil {
		t.Fatal(err)
	}
	h.hub.Notify(InboxKey)
	select {
	case w = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("long poll did not wake")
	}
	if w.Code != 200 || execwire.Decode(w.Body.Bytes(), &inbox) != nil || len(inbox.Cancels) != 1 || inbox.Cancels[0].StopID != stop.ID || time.Since(started) > 3*time.Second {
		t.Fatalf("woken poll: %d %s after %v", w.Code, w.Body.String(), time.Since(started))
	}
	// A paused daemon reports paused and delivers no new assignment, while the
	// cancel outbox still drains.
	db, err := sql.Open("sqlite", filepath.Join(h.root, "state", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE daemon_state SET paused=1"); err != nil {
		t.Fatal(err)
	}
	w = h.do(t, handler, "GET", "/x/v1/inbox", session, "")
	if w.Code != 200 || execwire.Decode(w.Body.Bytes(), &inbox) != nil || !inbox.Paused || len(inbox.Cancels) != 1 || len(inbox.Assignments) != 0 {
		t.Fatalf("paused inbox: %d %s", w.Code, w.Body.String())
	}
	sent := time.Now().UnixMilli()
	request := p.Message{Version: p.FencedVersion, Kind: "lease_request", MessageID: newTestID(), Identity: h.d.Assignment.Identity, Nonce: newTestID(), RunnerBoot: h.facts.RunnerBoot, DaemonBoot: sess.DaemonBoot, SentMS: &sent}
	w = h.do(t, handler, "POST", "/x/v1/lease", session, string(encode(t, execwire.LeaseEnvelope{Version: execwire.Version, MessageID: newTestID(), DispatchID: h.d.ID, Request: request})))
	assertRouteError(t, w, 409, "paused")
}
