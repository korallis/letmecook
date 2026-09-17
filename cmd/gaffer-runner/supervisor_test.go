package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execclient"
	w "github.com/korallis/letmecook/internal/execwire"
	h "github.com/korallis/letmecook/internal/harness"
	"github.com/korallis/letmecook/internal/harness/fake"
	"github.com/korallis/letmecook/internal/httpapi"
	"github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/isolation"
	repo "github.com/korallis/letmecook/internal/repositories"
	"github.com/korallis/letmecook/internal/runner"
	"github.com/korallis/letmecook/internal/runstream"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

var testBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "runner-tests-bin-")
	if err != nil {
		panic(err)
	}
	testBinary = filepath.Join(dir, "gaffer-runner")
	cmd := exec.Command("go", "build", "-o", testBinary, ".")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
func certFiles(t *testing.T, dir, name string) (string, string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := x509.MarshalPKCS8PrivateKey(priv)
	cp := filepath.Join(dir, name+".crt")
	kp := filepath.Join(dir, name+".key")
	os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600)
	sum := sha256.Sum256(der)
	return cp, kp, hex.EncodeToString(sum[:])
}

type fakeDaemon struct {
	t                               *testing.T
	mu                              sync.Mutex
	session                         w.Session
	dispatch                        store.Dispatch
	input                           execclient.TaskInput
	peer                            string
	reply                           map[string][]byte
	request                         map[string][]byte
	calls                           []string
	state                           p.AttemptState
	revision                        int64
	sink                            *runstream.Sink
	upload                          string
	begin                           w.UploadBegin
	blobs                           map[string][]byte
	receipt                         w.CommitReply
	commits                         int
	finalized                       bool
	released                        bool
	dropCommit                      bool
	refuseFinalize                  bool
	blockLease                      bool
	stale                           bool
	cancel                          *p.Message
	termination                     *w.MessageEnvelope
	manifest                        store.CandidateManifest
	leaseCount                      int
	inputTamper                     bool
	inputRaw                        []byte
	inputRedirect                   string
	blockRunning                    bool
	runningWaiting                  chan struct{}
	eligibleBoot, sessionRunnerBoot string
	paused, delivered               bool
	inboxCalls, cancelCopies        int
	blockFinalize                   bool
	finalizeWaiting                 chan struct{}
	finalizeBodies                  [][]byte
	lastNonce                       string
}

func (d *fakeDaemon) handler(resp http.ResponseWriter, req *http.Request) {
	state, ok := httpapi.ExecutionState(req.Context())
	if !ok || state.NegotiatedProtocol != p.FencedVersion || len(state.PeerCertificates) != 1 {
		http.Error(resp, "tls", 403)
		return
	}
	fp, _ := identity.Fingerprint(state.PeerCertificates[0])
	if fp != d.peer {
		http.Error(resp, "peer", 403)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	errorReply := func(status int, code string) {
		resp.WriteHeader(status)
		json.NewEncoder(resp).Encode(w.ErrorBody{Version: w.Version, Error: code, Detail: "test refusal"})
	}
	if req.URL.Path != "/x/v1/session" && req.Header.Get("X-Gaffer-Session") != d.session.SessionID {
		errorReply(409, "session_stale")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(req.Body, 64<<20))
	send := func(v any) { resp.Header().Set("Content-Type", "application/json"); json.NewEncoder(resp).Encode(v) }
	switch req.URL.Path {
	case "/x/v1/session":
		var h w.Hello
		if w.Decode(body, &h) != nil {
			errorReply(400, "malformed")
			return
		}
		d.calls = append(d.calls, "hello")
		d.sessionRunnerBoot = h.RunnerBoot
		d.session.Mode = "recovery_only"
		if h.RunnerBoot == d.eligibleBoot {
			d.session.Mode = "normal"
		}
		if d.paused {
			d.session.Mode = "paused"
		}
		send(d.session)
	case "/x/v1/input":
		if d.inputRaw != nil {
			resp.Write(d.inputRaw)
			return
		}
		if d.inputRedirect != "" {
			http.Redirect(resp, req, d.inputRedirect, 307)
			return
		}
		if d.inputTamper {
			v := d.input
			v.Brief = "tampered"
			send(v)
		} else {
			send(d.input)
		}
	case "/x/v1/inbox":
		d.inboxCalls++
		in := w.Inbox{Assignments: []w.Dispatch{}, Cancels: []p.Message{}, PollAfterMS: 500, Paused: d.paused}
		if !d.dispatch.Acknowledged && !d.delivered && d.session.Mode == "normal" {
			d.delivered = true
			b, _ := json.Marshal(d.dispatch)
			var v w.Dispatch
			json.Unmarshal(b, &v)
			in.Assignments = append(in.Assignments, v)
		}
		if d.cancel != nil {
			for i := 0; i < max(1, d.cancelCopies); i++ {
				in.Cancels = append(in.Cancels, *d.cancel)
			}
		}
		send(in)
	case "/x/v1/state":
		send(w.State{AttemptState: d.state, Revision: d.revision, ReceiptID: d.receipt.Receipt.ReceiptID, StopTargets: []cTarget{}, Stream: w.StreamAck{}})
	case "/x/v1/messages":
		var m w.MessageEnvelope
		if w.Decode(body, &m) != nil {
			errorReply(400, "malformed")
			return
		}
		mb, _ := json.Marshal(m.Message)
		if _, e := p.Decode(mb); e != nil {
			errorReply(400, "malformed")
			return
		}
		if d.stale {
			errorReply(409, "stale_generation")
			return
		}
		if old, ok := d.reply[m.MessageID]; ok {
			var prior w.MessageEnvelope
			_ = json.Unmarshal(d.request[m.MessageID], &prior)
			if p.CheckReplay(m.Message, prior.Message, d.dispatch.Assignment.Identity) != p.OK || !bytes.Equal(d.request[m.MessageID], body) {
				errorReply(409, "identity_conflict")
				return
			}
			resp.Write(old)
			return
		}
		reply := execclient.MessageReply{Outcome: "recorded"}
		switch m.Message.Kind {
		case "accept":
			d.dispatch.Acknowledged = true
			d.calls = append(d.calls, "accept")
			reply.Outcome = "acknowledged"
		case "refuse":
			if d.dispatch.Acknowledged {
				errorReply(409, "reconciliation_required")
				return
			}
			d.calls = append(d.calls, "refuse")
		case "transition":
			reply.Outcome = "applied"
			if m.Message.ExpectedRevision == nil || *m.Message.ExpectedRevision != d.revision || m.Message.From != d.state {
				errorReply(409, "revision_conflict")
				return
			}
			if m.Message.To == p.Starting && (m.Evidence == nil || m.Evidence.Kind != "launch_intent" || m.Evidence.GuardianPID != 0 || m.Evidence.Nonce == "") {
				errorReply(409, "reconciliation_required")
				return
			}
			if m.Message.To == p.Running && (m.Evidence == nil || m.Evidence.PID <= 1 || m.Evidence.GuardianPID <= 1) {
				errorReply(409, "reconciliation_required")
				return
			}
			if m.Message.To == p.ResultPending && (m.Evidence == nil || !m.Evidence.PGIDEmpty || m.Evidence.StreamThrough != d.sink.Acknowledged()) {
				errorReply(409, "reconciliation_required")
				return
			}
			if m.Message.To == p.Running && d.blockRunning {
				select {
				case d.runningWaiting <- struct{}{}:
				default:
				}
				d.mu.Unlock()
				<-req.Context().Done()
				d.mu.Lock()
				return
			}
			d.state = m.Message.To
			d.revision++
			d.calls = append(d.calls, string(d.state))
			reply.Message = &m.Message
		case "terminated":
			if m.Measurement == nil || m.Boundary == nil || m.Measurement.Validate(m.Message.ConfirmedProcess != "unknown") != nil {
				errorReply(400, "malformed")
				return
			}
			d.termination = &m
			if d.sessionRunnerBoot != m.Message.RunnerBoot {
				reply.Outcome = "retained"
				break
			}
			latched := d.cancel != nil && d.cancel.StopID == m.Message.StopID
			if d.lastNonce != "" && m.Message.StopID == w.ExpiryStopID(d.lastNonce) {
				latched = true
			}
			for _, cause := range w.LocalStopCauses {
				if m.Message.StopID == w.LocalStopID(m.Message.Identity.AttemptID, cause) {
					latched = true
				}
			}
			if !latched {
				errorReply(409, "reconciliation_required")
				return
			}
			d.released = m.Message.ConfirmedProcess != "unknown" && m.Boundary.Quiescent && (d.state == p.Stopping || d.state == p.Unknown)
			reply.Outcome = "observed"
			reply.Released = d.released
			d.calls = append(d.calls, "terminated")
		}
		b, _ := json.Marshal(reply)
		d.reply[m.MessageID] = b
		d.request[m.MessageID] = append([]byte(nil), body...)
		resp.Write(b)
	case "/x/v1/lease":
		var m w.LeaseEnvelope
		if w.Decode(body, &m) != nil {
			errorReply(400, "malformed")
			return
		}
		if d.blockLease && d.leaseCount > 0 {
			errorReply(409, "delayed_reply")
			return
		}
		d.leaseCount++
		d.lastNonce = m.Request.Nonce
		if old, ok := d.reply[m.MessageID]; ok {
			resp.Write(old)
			return
		}
		valid := d.session.LeaseValidityMS
		reply := p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "lease_reply", Identity: m.Request.Identity, Nonce: m.Request.Nonce, RunnerBoot: m.Request.RunnerBoot, DaemonBoot: m.Request.DaemonBoot, ValidityMS: &valid}
		b, _ := json.Marshal(reply)
		d.reply[m.MessageID] = b
		d.calls = append(d.calls, "lease")
		resp.Write(b)
	case "/x/v1/usage":
		var m w.Usage
		if w.Decode(body, &m) != nil {
			errorReply(400, "malformed")
			return
		}
		send(map[string]string{"outcome": "recorded"})
	default:
		switch {
		case strings.HasPrefix(req.URL.Path, "/x/v1/streams/"):
			var b w.StreamBatch
			if w.Decode(body, &b) != nil {
				errorReply(400, "malformed")
				return
			}
			var a runstream.Ack
			for _, r := range b.Records {
				var e error
				a, e = d.sink.Receive(r)
				if e != nil {
					if errors.Is(e, runstream.ErrGap) {
						resp.WriteHeader(409)
						json.NewEncoder(resp).Encode(w.ErrorBody{Version: w.Version, Error: "stream_sequence_gap", Detail: strconv.FormatInt(a.Expected, 10)})
						return
					}
					errorReply(409, "stream_record_conflict")
					return
				}
			}
			d.calls = append(d.calls, "stream")
			send(w.StreamAck{Through: a.Through, Expected: a.Expected, Bytes: a.Bytes})
		case strings.HasSuffix(req.URL.Path, "/uploads"):
			if d.state != p.ResultPending {
				errorReply(409, "invalid_transition")
				return
			}
			if w.Decode(body, &d.begin) != nil {
				errorReply(400, "malformed")
				return
			}
			rb, _ := json.Marshal(d.begin.Result)
			if _, e := p.Decode(rb); e != nil {
				errorReply(400, "malformed")
				return
			}
			b, _ := base64.StdEncoding.DecodeString(d.begin.ManifestBase64)
			if json.Unmarshal(b, &d.manifest) != nil {
				errorReply(400, "malformed")
				return
			}
			missing := []w.MissingBlob{}
			for _, v := range append(append(append(d.manifest.Tracked, d.manifest.Untracked...), d.manifest.Binary...), d.manifest.Recovery...) {
				if _, ok := d.blobs[v.SHA256]; !ok {
					missing = append(missing, w.MissingBlob{SHA256: v.SHA256, Bytes: v.Bytes})
				}
			}
			d.calls = append(d.calls, "begin")
			resp.WriteHeader(201)
			send(w.UploadSession{UploadID: d.upload, Missing: missing, BytesAllowed: 256 << 20})
		case strings.Contains(req.URL.Path, "/blobs/"):
			digest := filepath.Base(req.URL.Path)
			sum := sha256.Sum256(body)
			if digest != hex.EncodeToString(sum[:]) {
				errorReply(422, "digest_mismatch")
				return
			}
			d.blobs[digest] = append([]byte(nil), body...)
			d.calls = append(d.calls, "blob")
			send(execclient.BlobReply{SHA256: digest, Bytes: int64(len(body))})
		case strings.HasSuffix(req.URL.Path, "/commit"):
			d.commits++
			if d.receipt.Receipt.ReceiptID == "" {
				receiptID := uuid()
				d.receipt = w.CommitReply{Ack: p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "result_ack", Identity: d.dispatch.Assignment.Identity, Manifest: d.begin.Result.Manifest, ReceiptID: receiptID}, Receipt: p.Receipt{Identity: d.dispatch.Assignment.Identity, Manifest: *d.begin.Result.Manifest, ReceiptID: receiptID, Artifacts: "durable", Metadata: "durable"}}
			}
			if d.dropCommit && d.commits == 1 {
				hij := resp.(http.Hijacker)
				conn, _, _ := hij.Hijack()
				conn.Close()
				return
			}
			d.calls = append(d.calls, "commit")
			send(d.receipt)
		case strings.HasSuffix(req.URL.Path, "/finalize"):
			if d.refuseFinalize {
				errorReply(409, "reconciliation_required")
				return
			}
			d.finalizeBodies = append(d.finalizeBodies, append([]byte(nil), body...))
			if d.stale {
				errorReply(409, "stale_generation")
				return
			}
			var completion w.Completion
			if w.Decode(body, &completion) != nil {
				errorReply(400, "malformed")
				return
			}
			if old, ok := d.request[completion.MessageID]; ok && !bytes.Equal(old, body) {
				errorReply(409, "identity_conflict")
				return
			}
			d.request[completion.MessageID] = append([]byte(nil), body...)
			hash := sha256.New()
			for _, r := range d.sink.Records() {
				hash.Write([]byte(r.Digest))
			}
			if completion.Stream.Through != d.sink.Acknowledged() || completion.Stream.Digest != hex.EncodeToString(hash.Sum(nil)) || completion.ReceiptID != d.receipt.Receipt.ReceiptID || !completion.Boundary.Quiescent {
				errorReply(409, "reconciliation_required")
				return
			}
			d.finalized = true
			d.released = true
			d.calls = append(d.calls, "finalize")
			if d.blockFinalize {
				select {
				case d.finalizeWaiting <- struct{}{}:
				default:
				}
				d.mu.Unlock()
				<-req.Context().Done()
				d.mu.Lock()
				return
			}
			send(execclient.FinalizeReply{Outcome: d.manifest.Outcome, Released: true})
		default:
			errorReply(404, "not_found")
		}
	}
}

// type alias avoids any fake store implementation; TLS and durable stream are real.
type cTarget = control.Target

type fixture struct {
	s        *supervisor
	d        *fakeDaemon
	dispatch w.Dispatch
	local    sc.Eligibility
	root     string
	profile  repo.Profile
	ctx      context.Context
	cancel   context.CancelFunc
	out      bytes.Buffer
}

func newFixture(t *testing.T, mode string) *fixture {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	roots := filepath.Join(root, "roots")
	stateDir := filepath.Join(root, "state")
	os.Mkdir(roots, 0700)
	os.Mkdir(stateDir, 0700)
	source := filepath.Join(root, "source")
	os.Mkdir(source, 0700)
	git := func(args ...string) string {
		cmd := exec.Command("/usr/bin/git", append([]string{"-C", source}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %s %v", args, b, e)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "--quiet", "-b", "main")
	os.WriteFile(filepath.Join(source, "greeting.txt"), []byte("hello\n"), 0600)
	git("add", ".")
	git("-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "base")
	base := git("rev-parse", "HEAD")
	bare := filepath.Join(root, "remote.git")
	git("clone", "--quiet", "--bare", source, bare)
	daemonCert, daemonKey, daemonFP := certFiles(t, root, "daemon")
	runnerCert, runnerKey, runnerFP := certFiles(t, root, "runner")
	certificate, _ := tls.LoadX509KeyPair(runnerCert, runnerKey)
	tlsCfg, err := identity.ExecutionTLS(daemonCert, daemonKey)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := httpapi.ExecutionListener(inner, tlsCfg, time.Second)
	boot := uuid()
	rid := uuid()
	session := w.Session{SessionID: uuid(), Generation: uuid(), DaemonBoot: uuid(), DaemonFingerprint: daemonFP, RunnerID: rid, Mode: "normal", DriftMS: 2000, TerminationMS: 5000, LeaseValidityMS: 20000, RenewEveryMS: 5000}
	hash := strings.Repeat("a", 64)
	rev := g.Revision{Number: 1, SHA256: hash}
	route := g.Route{RouteRef: "worker-dev", ProfileRef: "mock", RouteRevision: 1, Policy: rev, RouterBuild: hash, GraphDigest: hash, Evidence: rev, Harness: "fake", Protocol: "responses", SettingsDigest: hash, Isolation: isolation.DevelopmentProfileID, LimitsProfile: "gateway-local-bounds-v1", LimitsAuthority: "operator", Targets: []g.Target{{Provider: "mock", Model: "gpt-6-astra", Billing: "gateway-managed"}}}
	profile := repo.Profile{Version: repo.Version, ID: "fixture", Revision: 1, Remote: "file://" + bare, Base: repo.Base{Ref: "refs/heads/main", Commit: base, Policy: "pinned"}, ProtectedPaths: []string{}, Verification: repo.Verification{Name: "fixture", Commands: []repo.Command{{Argv: []string{"/usr/bin/true"}, Directory: ".", TimeoutMS: 1000}}}, ContextScope: []string{"greeting.txt"}, RunnerRoots: []repo.RunnerRoot{{RunnerID: rid, Root: roots}}}
	ph, err := profile.Digest()
	if err != nil {
		t.Fatal("profile", err)
	}
	settings, _ := json.Marshal(fake.Script{Attempts: []fake.Settings{{Mode: mode, Edits: []fake.Edit{{Path: "greeting.txt", Content: "hello, gaffer\n"}}}}})
	input := execclient.TaskInput{Version: w.Version, DispatchID: uuid(), TaskID: uuid(), Repository: "fixture", BaseCommit: base, Brief: "edit the greeting", Criteria: []execclient.Criterion{{ID: "greeting", Text: "greeting says hello, gaffer"}}, Paths: []string{"greeting.txt"}, Operations: []string{"read", "verify", "write"}, Harness: "fake", Settings: settings}
	input.BriefSHA256, _ = input.Digest()
	briefRev := g.Revision{Number: 1, SHA256: input.BriefSHA256}
	now := time.Now().UnixMilli()
	env := g.Envelope{Repository: "fixture", BaseCommit: base, Brief: briefRev, Plan: rev, RouteDecision: rev, CriterionIDs: []string{"greeting"}, TaskKinds: []string{"code"}, Paths: input.Paths, Operations: input.Operations, Systems: []string{}, Runners: []string{rid}, Routes: []g.Route{route}, Selection: "pinned", Budgets: g.Budgets{Requests: 5, Attempts: 3, Subattempts: 10, Retries: 2, Concurrency: 1, RequestBytes: 1024, ResponseBytes: 4096, TotalMS: 90000, AttemptMS: 30000, FirstOutputMS: 5000, IdleMS: 5000}, NotBeforeMS: now - 10000, ExpiresMS: now + 600000}
	local := sc.Eligibility{ID: "fixture-pair", Revision: 1, Repository: repo.Selection{Repository: "fixture", Revision: 1, ProfileDigest: ph, Remote: profile.Remote, BaseCommit: base, RunnerRoot: profile.RunnerRoots[0]}, Enabled: true, RunnerBoot: boot, LocalPolicy: rev, LocalEnvelope: env, Config: rev, Route: route, Paths: []sc.PathEvidence{{Target: route.Targets[0], Compatible: true, Capabilities: []string{"tools"}, ContextTokens: 8192, LocalBounds: true}}, Capabilities: []string{"tools"}, Isolation: sc.IsolationProfile{ID: isolation.DevelopmentProfileID, Revision: rev, RuntimeDigest: hash, ObservedDigest: hash, Kind: "native", Supported: false, Qualification: "development", Controls: sc.DevelopmentIsolationControls()}, Capacity: sc.Resources{CPU: 1000, MemoryBytes: 1 << 30, DiskBytes: 1 << 30, Processes: 32}, Concurrency: 1, RouterAuthenticated: true, Availability: "available", ValidUntilMS: now + 600000}
	// A real authenticated local HTTPS models probe is required even for fake jobs.
	local.RouterAuthenticated = authenticatedMock(t, root)
	decision := sc.Decision{ID: uuid(), Revision: 1, Assessment: sc.Assessment{TaskID: input.TaskID, Brief: briefRev, Plan: rev, ContextDigest: hash, Role: "worker", WorkClass: "code", Ambiguity: "low", Consequence: "high", Required: []string{"tools"}, Preferences: []string{}, EvidenceRefs: []string{"operator"}, Unknowns: []string{}, ContextTokens: 4096, AssessorVersion: "operator-v1", SelectorVersion: "deterministic-v1"}, EligibilityID: local.ID, Selected: route, RankedAlternatives: []string{}, Authorization: "operator", Reason: "pinned", Resources: sc.Resources{CPU: 500, MemoryBytes: 1 << 28, DiskBytes: 1 << 28, Processes: 8}}
	fh, _ := sc.Digest(local)
	decision.Eligibility = g.Revision{Number: 1, SHA256: fh}
	dh, _ := sc.Digest(decision)
	env.RouteDecision = g.Revision{Number: 1, SHA256: dh}
	allowance := env.Budgets
	allowance.Attempts = 1
	allowance.Retries = 0
	allowance.TotalMS = allowance.AttemptMS
	dispatch := store.Dispatch{DispatchRequest: store.DispatchRequest{ID: input.DispatchID, Request: g.Request{GrantID: uuid(), TaskID: input.TaskID, GrantRevision: 1, Action: "execute", Envelope: env}, Decision: decision, Allowance: allowance}, Facts: local}
	ih, _ := sc.Digest(struct {
		store.DispatchRequest
		Facts sc.Eligibility `json:"facts"`
	}{dispatch.DispatchRequest, dispatch.Facts})
	dispatch.Assignment = p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "assign", Identity: p.Identity{Generation: session.Generation, TaskID: input.TaskID, AttemptID: uuid(), Epoch: 1}, AssignmentID: uuid(), InputDigest: ih, Route: &p.Route{RouteRef: route.RouteRef, DecisionDigest: dh, PolicyDigest: hash, LimitsProfile: route.LimitsProfile}}
	if e := sc.CheckDispatchWithPolicy(dispatch.Request, decision, local, now, sc.AdmissionPolicy{DevelopmentProfiles: []string{isolation.DevelopmentProfileID}}); e != nil {
		t.Fatal("fixture admission", e)
	}
	sink, err := runstream.CreateSink(filepath.Join(root, "sink"), dispatch.Assignment.Identity, runstream.MaxLimit)
	if err != nil {
		t.Fatal(err)
	}
	daemon := &fakeDaemon{t: t, session: session, eligibleBoot: boot, dispatch: dispatch, input: input, peer: runnerFP, reply: map[string][]byte{}, request: map[string][]byte{}, state: p.Assigned, revision: 1, sink: sink, upload: uuid(), blobs: map[string][]byte{}}
	server := &http.Server{Handler: http.HandlerFunc(daemon.handler), ConnContext: httpapi.ExecutionConnContext}
	go server.Serve(listener)
	client, err := execclient.New(execclient.Options{Endpoint: "https://" + inner.Addr().String(), Fingerprint: daemonFP, Certificate: certificate, RetryDelay: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{StateDir: stateDir, RepositoryRoot: roots, Isolation: isolation.DevelopmentProfileID, Harness: "fake", Policy: filepath.Join(stateDir, "policy.json"), RepositoryProfile: filepath.Join(stateDir, "repository.json"), SpoolLimit: 4 << 20, Daemon: "https://" + inner.Addr().String(), Fingerprint: daemonFP, Cert: runnerCert, Key: runnerKey}
	runner.DurableFile(cfg.Policy, local)
	runner.DurableFile(cfg.RepositoryProfile, profile)
	prof, err := profileFor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{d: daemon, local: local, root: root, profile: profile}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	f.s = &supervisor{cfg: cfg, client: client, session: session, boot: boot, clock: time.Now(), profile: prof, harness: fake.New(testBinary), cancel: make(chan p.Message, 16), out: &f.out, executable: testBinary}
	body, _ := json.Marshal(dispatch)
	json.Unmarshal(body, &f.dispatch)
	_, err = client.Hello(f.ctx, w.Hello{Version: w.Version, MessageID: uuid(), RunnerBoot: boot, EligibilityID: local.ID, EligibilityRevision: local.Revision, PolicyDigest: fh, Journals: []w.Journal{}})
	if err != nil {
		t.Fatal("hello", err)
	}
	t.Cleanup(func() { f.cancel(); client.Close(); server.Close(); sink.Close() })
	return f
}
func authenticatedMock(t *testing.T, root string) bool {
	t.Helper()
	keyPath := filepath.Join(root, "mock-token")
	os.WriteFile(keyPath, []byte("synthetic-test-only"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var out lockedBuffer
	done := make(chan error, 1)
	go func() { done <- mockGateway(ctx, []string{"--key-file", keyPath}, &out) }()
	var info struct {
		URL           string `json:"url"`
		CAFile        string `json:"ca_file"`
		Qualification string `json:"qualification"`
		Supported     bool   `json:"supported"`
	}
	deadline := time.Now().Add(3 * time.Second)
	for json.Unmarshal(out.Bytes(), &info) != nil {
		if time.Now().After(deadline) {
			t.Fatal("mock did not start", out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	ok, err := probeGateway(context.Background(), inference.Gateway{BaseURL: info.URL, CredentialRef: inference.CredentialRef{Kind: "file", Path: keyPath}}, info.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.b.Bytes()...)
}
func (b *lockedBuffer) String() string { return string(b.Bytes()) }
func TestFakeEditEndToEndAndLostCommitReply(t *testing.T) {
	f := newFixture(t, "edit")
	f.d.dropCommit = true
	if err := f.s.attempt(f.ctx, f.dispatch); err != nil {
		t.Fatal(err, f.out.String(), f.d.calls)
	}
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	if !f.d.finalized || !f.d.released || f.d.commits != 2 || f.d.manifest.Outcome != "succeeded" {
		t.Fatalf("finalization %+v", f.d.calls)
	}
	found := false
	for _, b := range f.d.blobs {
		found = found || string(b) == "hello, gaffer\n"
	}
	if !found {
		t.Fatal("edited bytes absent")
	}
	for _, step := range []string{"hello", "accept", "lease", "starting", "running", "stream", "begin", "blob", "commit", "finalize"} {
		if !contains(f.d.calls, step) {
			t.Fatal("missing step", step, f.d.calls)
		}
	}
	t.Logf("real TLS/subprocess/sandbox flow: %v; commit requests=%d; receipt=%s", f.d.calls, f.d.commits, f.d.receipt.Receipt.ReceiptID)
}
func TestFailuresProduceFailedManifest(t *testing.T) {
	for _, mode := range []string{"huge_output", "approval", "crash", "exit_nonzero", "crash_after_edit", "envelope"} {
		t.Run(mode, func(t *testing.T) {
			actual := mode
			if mode == "envelope" {
				actual = "edit"
			}
			f := newFixture(t, actual)
			if mode == "huge_output" {
				f.s.cfg.SpoolLimit = 64 << 10
			}
			if mode == "envelope" {
				settings, _ := json.Marshal(fake.Script{Attempts: []fake.Settings{{Mode: "edit", Edits: []fake.Edit{{Path: "outside.txt", Content: "unauthorized"}}}}})
				f.rebindInput(t, settings)
			}
			if err := f.s.attempt(f.ctx, f.dispatch); err != nil {
				t.Fatal(err)
			}
			if f.d.manifest.Outcome != "failed" || !f.d.finalized {
				t.Fatal("missing failed manifest", f.d.calls)
			}
			if mode == "huge_output" || mode == "envelope" {
				want := map[string]string{"huge_output": "spool_full", "envelope": "envelope_violation"}[mode]
				found := false
				for _, r := range f.d.sink.Records() {
					found = found || r.Normalized.Stream == "status" && r.Normalized.Text == want
				}
				if !found {
					t.Fatal("terminal status missing", want)
				}
			}
		})
	}
}
func (f *fixture) rebindInput(t *testing.T, settings []byte) {
	t.Helper()
	f.d.input.Settings = settings
	hash, _ := f.d.input.Digest()
	f.d.input.BriefSHA256 = hash
	d := f.d.dispatch
	d.Request.Envelope.Brief.SHA256 = hash
	d.Decision.Assessment.Brief.SHA256 = hash
	dh, _ := sc.Digest(d.Decision)
	d.Request.Envelope.RouteDecision.SHA256 = dh
	d.Assignment.Route.DecisionDigest = dh
	ih, _ := sc.Digest(struct {
		store.DispatchRequest
		Facts sc.Eligibility `json:"facts"`
	}{d.DispatchRequest, d.Facts})
	d.Assignment.InputDigest = ih
	f.d.dispatch = d
	b, _ := json.Marshal(d)
	json.Unmarshal(b, &f.dispatch)
}
func TestLeaseLapseAndIgnoreTermCancel(t *testing.T) {
	for _, mode := range []string{"hang", "ignore_term"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, mode)
			f.d.blockLease = mode == "hang"
			if mode == "hang" {
				f.d.session.LeaseValidityMS = 8000
				f.s.session.LeaseValidityMS = 8000
			}
			done := make(chan error, 1)
			go func() { done <- f.s.attempt(f.ctx, f.dispatch) }()
			waitState(t, f, p.Running)
			start := time.Now()
			if mode == "ignore_term" {
				cancel := p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "cancel", Identity: f.dispatch.Assignment.Identity, StopID: uuid(), RunnerBoot: f.s.boot, DaemonBoot: f.s.session.DaemonBoot}
				f.sendCancel(cancel)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(12 * time.Second):
				t.Fatal("termination timed out")
			}
			f.d.mu.Lock()
			defer f.d.mu.Unlock()
			if f.d.termination == nil || !f.d.released || f.d.termination.Message.ConfirmedProcess != "terminated" {
				t.Fatal("no release evidence", f.d.calls)
			}
			if mode == "ignore_term" && !f.d.termination.Measurement.Escalated {
				t.Fatal("KILL escalation not measured")
			}
			if mode == "hang" && f.d.termination.Message.StopID != w.ExpiryStopID(lastLeaseNonce(f.d)) {
				t.Fatal("wrong expiry stop id")
			}
			t.Logf("%s termination from running in %s, escalated=%v", mode, time.Since(start), f.d.termination.Measurement.Escalated)
		})
	}
}
func lastLeaseNonce(d *fakeDaemon) string {
	for _, b := range d.reply {
		var m p.Message
		if json.Unmarshal(b, &m) == nil && m.Kind == "lease_reply" {
			return m.Nonce
		}
	}
	return ""
}
func waitState(t *testing.T, f *fixture, want p.AttemptState) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		f.d.mu.Lock()
		state := f.d.state
		f.d.mu.Unlock()
		if state == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("state not reached", want)
}
func TestDetachedChildNeverClaimsTermination(t *testing.T) {
	f := newFixture(t, "detached_child")
	done := make(chan error, 1)
	go func() { done <- f.s.attempt(f.ctx, f.dispatch) }()
	waitState(t, f, p.Running)
	time.Sleep(350 * time.Millisecond)
	f.sendCancel(p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "cancel", Identity: f.dispatch.Assignment.Identity, StopID: uuid(), RunnerBoot: f.s.boot, DaemonBoot: f.s.session.DaemonBoot})
	select {
	case err := <-done:
		if !errors.Is(err, runner.ErrTerminationUnconfirmed) {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("detached timeout")
	}
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	if f.d.termination == nil || f.d.termination.Message.ConfirmedProcess != "unknown" || f.d.termination.Measurement.Validate(false) != nil || f.d.released || f.d.finalized {
		t.Fatal("escaped group falsely released")
	}
}
func TestFencedAndUnqualifiedNeverLaunch(t *testing.T) {
	for _, mode := range []string{"stale_generation", "unqualified", "tampered_input"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, "edit")
			if mode == "stale_generation" {
				f.d.stale = true
			}
			if mode == "unqualified" {
				f.s.profile = isolation.Unqualified{}
			}
			if mode == "tampered_input" {
				f.d.inputTamper = true
			}
			if err := f.s.attempt(f.ctx, f.dispatch); err == nil {
				t.Fatal("refusal absent")
			}
			if contains(f.d.calls, "running") || f.d.finalized {
				t.Fatal("refused attempt launched")
			}
			if mode == "stale_generation" {
				b, err := os.ReadFile(filepath.Join(f.s.cfg.StateDir, "attempts", f.dispatch.ID, "journal", "journal.jsonl"))
				if err != nil || !bytes.Contains(b, []byte(`"kind":"fenced"`)) {
					t.Fatal("fence not durable", err)
				}
			}
		})
	}
}

var _ = fmt.Sprintf
var _ = syscall.SIGKILL

func TestSupervisorSIGKILLGuardianEOFAndOpen(t *testing.T) {
	for _, gap := range []bool{false, true} {
		t.Run(fmt.Sprintf("running_ack_gap=%v", gap), func(t *testing.T) {
			f := newFixture(t, "hang")
			f.d.blockRunning = gap
			f.d.runningWaiting = make(chan struct{}, 1)
			cfg := f.s.cfg
			var stdout, stderr lockedBuffer
			cmd := exec.Command(testBinary, "serve", "--state-dir", cfg.StateDir, "--repository-root", cfg.RepositoryRoot, "--daemon", cfg.Daemon, "--daemon-fingerprint", cfg.Fingerprint, "--cert", cfg.Cert, "--key", cfg.Key, "--isolation-profile", cfg.Isolation, "--harness", "fake")
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			reaped := false
			t.Cleanup(func() {
				if !reaped {
					cmd.Process.Kill()
					cmd.Wait()
				}
			})
			var boot bootRecord
			deadline := time.Now().Add(4 * time.Second)
			for {
				b, e := os.ReadFile(filepath.Join(cfg.StateDir, "boot.json"))
				if e == nil && json.Unmarshal(b, &boot) == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("no boot", stderr.String())
				}
				time.Sleep(5 * time.Millisecond)
			}
			// Simulate the explicit owner facts import after observing the process boot.
			local := f.local
			local.RunnerBoot = boot.RunnerBoot
			f.d.mu.Lock()
			d := f.d.dispatch
			d.Facts = local
			fh, _ := sc.Digest(local)
			d.Decision.Eligibility.SHA256 = fh
			dh, _ := sc.Digest(d.Decision)
			d.Request.Envelope.RouteDecision.SHA256 = dh
			d.Assignment.Route.DecisionDigest = dh
			ih, _ := sc.Digest(struct {
				store.DispatchRequest
				Facts sc.Eligibility `json:"facts"`
			}{d.DispatchRequest, d.Facts})
			d.Assignment.InputDigest = ih
			f.d.dispatch = d
			f.d.eligibleBoot = boot.RunnerBoot
			f.d.mu.Unlock()
			if err := runner.DurableFile(cfg.Policy, local); err != nil {
				t.Fatal(err)
			}
			if gap {
				select {
				case <-f.d.runningWaiting:
				case <-time.After(8 * time.Second):
					t.Fatal("running request not reached")
				}
				f.d.mu.Lock()
				if f.d.state != p.Starting {
					t.Error("running acknowledged before gap")
				}
				f.d.mu.Unlock()
			} else {
				waitState(t, f, p.Running)
			}
			journal := filepath.Join(cfg.StateDir, "attempts", d.ID, "journal")
			var runtime runner.LaunchRecord
			b, _ := os.ReadFile(filepath.Join(journal, "journal.jsonl"))
			for _, line := range bytes.Split(b, []byte{'\n'}) {
				var row struct {
					Data struct {
						Kind    string               `json:"kind"`
						Runtime *runner.LaunchRecord `json:"runtime"`
					} `json:"data"`
				}
				if json.Unmarshal(line, &row) == nil && row.Data.Kind == "launched" {
					runtime = *row.Data.Runtime
				}
			}
			if runtime.PID <= 1 || runtime.GuardianPID <= 1 {
				t.Fatal("missing durable launched identity")
			}
			start := time.Now()
			if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			reaped = true
			f.s.boot = uuid()
			r, err := runner.Open(journal, f.s.options())
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			defer r.Close()
			status := r.Status()
			if status.Guardian == nil || !status.Guardian.PGIDEmpty || status.Guardian.Escaped || status.Guardian.Cause != "supervisor_eof" || !status.Quarantined || status.ExecutionEnabled {
				t.Fatalf("recovery did not retain termination: %+v", status)
			}
			if err := syscall.Kill(-runtime.PGID, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatal("job group remains", err)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("guardian EOF deadline exceeded", time.Since(start))
			}
			t.Logf("SIGKILL supervisor -> guardian EOF -> durable empty pgid observed -> Open quarantined in %s; job=%d guardian=%d", time.Since(start), runtime.PID, runtime.GuardianPID)

		})
	}
}
func TestClientTLSFingerprintALPNAndClosedJSON(t *testing.T) {
	f := newFixture(t, "edit")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	cert, _ := tls.LoadX509KeyPair(f.s.cfg.Cert, f.s.cfg.Key)
	bad, err := execclient.New(execclient.Options{Endpoint: f.s.cfg.Daemon, Fingerprint: strings.Repeat("b", 64), Certificate: cert, Attempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if _, err = bad.Hello(f.ctx, w.Hello{Version: w.Version, MessageID: uuid(), RunnerBoot: f.s.boot, EligibilityID: f.local.ID, EligibilityRevision: 1, PolicyDigest: strings.Repeat("a", 64), Journals: []w.Journal{}}); err == nil {
		t.Fatal("fingerprint pin ignored")
	}
	conn, err := tls.Dial("tcp", strings.TrimPrefix(f.s.cfg.Daemon, "https://"), &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2", "http/1.1"}, InsecureSkipVerify: true})
	if err == nil {
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		_, err = conn.Write([]byte("GET /x/v1/inbox HTTP/1.1\r\nHost: localhost\r\n\r\n"))
		if err == nil {
			buf := make([]byte, 256)
			n, e := conn.Read(buf)
			if e == nil && n > 0 {
				t.Fatal("wrong ALPN served", string(buf[:n]))
			}
		}
	}
	if _, err = f.s.client.Input(f.ctx, uuid()); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"unknown":true}`, `{"settings":null}`, `{"version":"a","version":"b"}`, strings.Repeat("x", w.MaxBytes+1)} {
		f.d.mu.Lock()
		f.d.inputRaw = []byte(raw)
		f.d.mu.Unlock()
		if _, err = f.s.client.Input(f.ctx, uuid()); err == nil {
			t.Fatal("malformed response accepted", raw[:min(len(raw), 80)])
		}
	}
	f.d.mu.Lock()
	f.d.inputRaw = nil
	f.d.inputRedirect = "/x/v1/state"
	f.d.mu.Unlock()
	if _, err = f.s.client.Input(f.ctx, uuid()); err == nil {
		t.Fatal("redirect followed")
	}
}
func TestFactsFakeWithoutGatewayNeverClaimsAuthentication(t *testing.T) {
	f := newFixture(t, "edit")
	boot := bootRecord{uuid(), os.Getpid(), time.Now().UTC()}
	if err := runner.DurableFile(filepath.Join(f.s.cfg.StateDir, "boot.json"), boot); err != nil {
		t.Fatal(err)
	}
	if err := runner.DurableFile(filepath.Join(f.s.cfg.StateDir, "serve.json"), f.s.cfg); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := facts(context.Background(), []string{"--state-dir", f.s.cfg.StateDir}, &out); err != nil {
		t.Fatal(err)
	}
	var v sc.Eligibility
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.RouterAuthenticated || v.RunnerBoot != boot.RunnerBoot || v.Isolation.Supported || v.Isolation.Qualification != "development" || !contains(v.Capabilities, "fake-no-inference") {
		t.Fatal("false fake facts", out.String())
	}
	local := f.local
	local.RouterAuthenticated = v.RouterAuthenticated
	decision := f.d.dispatch.Decision
	fh, _ := sc.Digest(local)
	decision.Eligibility.SHA256 = fh
	request := f.d.dispatch.Request
	dh, _ := sc.Digest(decision)
	request.Envelope.RouteDecision.SHA256 = dh
	err := sc.CheckDispatchWithPolicy(request, decision, local, time.Now().UnixMilli(), sc.AdmissionPolicy{DevelopmentProfiles: []string{isolation.DevelopmentProfileID}})
	if err == nil || !strings.Contains(err.Error(), "router_unauthenticated") {
		t.Fatalf("no-gateway scheduler result: %v", err)
	}
}

func TestSessionAllowsPendingFingerprintButKeepsTLSPin(t *testing.T) {
	f := newFixture(t, "noop")
	f.d.mu.Lock()
	f.d.session.DaemonFingerprint = ""
	f.d.session.DriftMS = 2000
	f.d.session.TerminationMS = 5000
	f.d.session.LeaseValidityMS = 20000
	f.d.session.RenewEveryMS = 5000
	f.d.mu.Unlock()
	hello := w.Hello{Version: w.Version, MessageID: uuid(), RunnerBoot: f.s.boot, EligibilityID: f.local.ID, EligibilityRevision: 1, PolicyDigest: strings.Repeat("a", 64), Journals: []w.Journal{}}
	got, err := f.s.client.Hello(f.ctx, hello)
	if err != nil || got.LeaseValidityMS != 20000 {
		t.Fatal(got, err)
	}
	f.d.mu.Lock()
	f.d.session.DaemonFingerprint = strings.Repeat("b", 64)
	f.d.mu.Unlock()
	hello.MessageID = uuid()
	if _, err = f.s.client.Hello(f.ctx, hello); err == nil {
		t.Fatal("conflicting declared pin accepted")
	}
}

func TestNativeEventChunkingRetainsBytesAndUTF8(t *testing.T) {
	f := newFixture(t, "noop")
	spool, err := runstream.CreateSpool(filepath.Join(f.root, "event-spool"), f.dispatch.Assignment.Identity, runstream.MaxLimit)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	original := strings.Repeat("stderr line\r\n", 6000)
	raw, _ := json.Marshal(original)
	full, err := appendHarnessEvent(spool, h.Event{Kind: h.Activity, Native: true, Raw: raw, Summary: strings.Repeat("界", 4000)})
	if err != nil || full {
		t.Fatal(full, err)
	}
	var retained []byte
	for _, record := range spool.Records() {
		retained = append(retained, record.Native.Data...)
		if !utf8.ValidString(record.Normalized.Text) || len(record.Normalized.Text) > runstream.MaxText || record.Normalized.Stream != "stderr" {
			t.Fatal("invalid normalized projection")
		}
		b, _ := json.Marshal(w.StreamBatch{Version: w.Version, Records: []runstream.Record{record}})
		if len(b) > w.MaxBytes {
			t.Fatal("oversized execution envelope", len(b))
		}
	}
	if string(retained) != original {
		t.Fatal("native line endings or bytes lost")
	}
}
func TestEnvelopeIncludesIgnoredUntrackedPaths(t *testing.T) {
	f := newFixture(t, "noop")
	checkout, err := repo.Prepare(f.ctx, f.profile, f.local.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(checkout.Path, ".git", "info"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(checkout.Path, ".git", "info", "exclude"), []byte("forbidden.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(checkout.Path, "forbidden.txt"), []byte("ignored but unauthorized"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = checkEnvelope(f.ctx, checkout.Path, checkout.BaseCommit, f.d.dispatch.Request.Envelope, f.profile); err == nil {
		t.Fatal("ignored unauthorized file escaped envelope")
	}
}

type releaseCheckingHarness struct {
	h.Harness
	mu    sync.Mutex
	last  int64
	ended bool
	calls int
	check func(int64)
}

func (a *releaseCheckingHarness) Events(ctx context.Context, handle h.RunHandle, after int64) (h.EventStream, error) {
	base, err := a.Harness.Events(ctx, handle, after)
	if err != nil {
		return nil, err
	}
	return &releaseCheckingStream{base: base, owner: a}, nil
}
func (a *releaseCheckingHarness) Release(ctx context.Context, handle h.RunHandle, through int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if !a.ended || through != a.last || through <= 0 {
		return errors.New("release before final event")
	}
	a.check(through)
	return nil
}

type releaseCheckingStream struct {
	base  h.EventStream
	owner *releaseCheckingHarness
}

func (s *releaseCheckingStream) Next(ctx context.Context) (h.Event, error) {
	ev, err := s.base.Next(ctx)
	s.owner.mu.Lock()
	if err == nil {
		s.owner.last = ev.Sequence
	} else if err == io.EOF {
		s.owner.ended = true
	}
	s.owner.mu.Unlock()
	return ev, err
}
func TestHarnessReleaseAfterTerminalAndCompleteSpooling(t *testing.T) {
	for _, scenario := range []string{"finalized", "terminated", "unfinalized", "spool_full"} {
		t.Run(scenario, func(t *testing.T) {
			mode := "edit"
			if scenario == "terminated" {
				mode = "ignore_term"
			}
			if scenario == "spool_full" {
				mode = "huge_output"
			}
			f := newFixture(t, mode)
			if scenario == "unfinalized" {
				f.d.refuseFinalize = true
			}
			if scenario == "spool_full" {
				f.s.cfg.SpoolLimit = runstream.MinLimit
			}
			a := &releaseCheckingHarness{Harness: f.s.harness, check: func(through int64) {
				f.d.mu.Lock()
				defer f.d.mu.Unlock()
				if !f.d.finalized && f.d.termination == nil {
					t.Error("release before terminal acknowledgement")
				}
				if f.d.sink.Acknowledged() == 0 {
					t.Error("release before durable stream acknowledgement")
				}
			}}
			f.s.harness = a
			var err error
			if scenario == "terminated" {
				done := make(chan error, 1)
				go func() { done <- f.s.attempt(f.ctx, f.dispatch) }()
				waitState(t, f, p.Running)
				f.sendCancel(p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "cancel", Identity: f.dispatch.Assignment.Identity, StopID: uuid(), RunnerBoot: f.s.boot, DaemonBoot: f.s.session.DaemonBoot})
				err = <-done
			} else {
				err = f.s.attempt(f.ctx, f.dispatch)
			}
			if scenario == "unfinalized" {
				if err == nil {
					t.Fatal("missing finalize reply accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			a.mu.Lock()
			calls := a.calls
			a.mu.Unlock()
			want := 1
			if scenario == "unfinalized" || scenario == "spool_full" {
				want = 0
			}
			if calls != want {
				t.Fatalf("Release calls=%d want=%d", calls, want)
			}
		})
	}
}

func TestGuardianOwnDeadlineWithOpenSupervisorPipe(t *testing.T) {
	for _, hung := range []bool{false, true} {
		t.Run(fmt.Sprintf("hung_supervisor=%v", hung), func(t *testing.T) {
			dir := t.TempDir()
			spec := runner.LaunchSpec{Argv: []string{"/bin/sleep", "60"}, Env: []string{"PATH=/usr/bin:/bin"}, Cwd: dir, DeadlineMS: 500, GraceMS: 200, KillMS: 1000, Rlimits: runner.ResourceLimits{OpenFiles: 1024, FileBytes: 64 << 20, CPUSeconds: 10}, ReceiptPath: filepath.Join(dir, "guardian.json")}
			in, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			defer write.Close()
			control, childControl, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer control.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, testBinary, "guardian")
			cmd.Stdin = in
			cmd.ExtraFiles = []*os.File{childControl}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			childControl.Close()
			t.Cleanup(func() { cmd.Process.Kill() })
			if hung {
				body, _ := json.Marshal(spec)
				parent := exec.Command("/bin/sh", "-c", `printf '%s\n' "$SPEC"; kill -STOP $$`)
				parent.Env = []string{"PATH=/usr/bin:/bin", "SPEC=" + string(body)}
				parent.Stdout = write
				if err := parent.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { parent.Process.Signal(syscall.SIGCONT); parent.Process.Kill(); parent.Wait() }()
				write.Close() // only the deliberately hung supervisor retains the pipe
			} else if err := json.NewEncoder(write).Encode(spec); err != nil {
				t.Fatal(err)
			}
			dec := json.NewDecoder(control)
			var started runner.GuardianStarted
			var report runner.GuardianReport
			if err := dec.Decode(&started); err != nil {
				t.Fatal(err, stderr.String())
			}
			if err := dec.Decode(&report); err != nil {
				t.Fatal(err, stderr.String())
			}
			if err := cmd.Wait(); err != nil {
				t.Fatal(err, stderr.String())
			}
			if report.Cause != "lease_expired" || !report.PGIDEmpty || report.Escaped || report.StopToObservedNS < 0 || report.StopToObservedNS > int64(time.Duration(spec.GraceMS+spec.KillMS)*time.Millisecond) {
				t.Fatalf("deadline report %+v", report)
			}
			if report.StopUnixNS-started.StartUnixNS < int64(450*time.Millisecond) {
				t.Fatal("deadline stopped early")
			}
			if err := syscall.Kill(-started.PGID, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatal("group remains", err)
			}
			var retained runner.GuardianReport
			b, err := os.ReadFile(spec.ReceiptPath)
			if err != nil || json.Unmarshal(b, &retained) != nil || retained != report {
				t.Fatal("receipt mismatch", err)
			}
			t.Logf("independent cutoff %s; stop-to-observed %s", time.Duration(report.StopUnixNS-started.StartUnixNS), time.Duration(report.StopToObservedNS))
		})
	}
}

func TestRepositoryMismatchRefusedBeforeAcceptance(t *testing.T) {
	for _, mode := range []string{"root", "digest", "base", "remote"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, "noop")
			local := f.profile
			switch mode {
			case "root":
				f.s.cfg.RepositoryRoot = filepath.Join(f.root, "other")
			case "digest":
				local.ProtectedPaths = []string{"new-protected"}
			case "base":
				local.Base.Commit = strings.Repeat("b", 40)
			case "remote":
				local.Remote = "file:///nonexistent.git"
			}
			if err := runner.DurableFile(f.s.cfg.RepositoryProfile, local); err != nil {
				t.Fatal(err)
			}
			if err := f.s.attempt(f.ctx, f.dispatch); !errors.Is(err, runner.ErrPolicy) {
				t.Fatal(err)
			}
			f.d.mu.Lock()
			defer f.d.mu.Unlock()
			if f.d.dispatch.Acknowledged || f.d.leaseCount != 0 || contains(f.d.calls, "running") {
				t.Fatal("mismatch accepted", f.d.calls)
			}
			found := false
			for _, body := range f.d.request {
				var env w.MessageEnvelope
				if json.Unmarshal(body, &env) == nil && env.Message.Kind == "refuse" {
					found = env.Message.Reason == p.LocalPolicyDenied && env.Message.InReplyTo == f.dispatch.Assignment.MessageID
				}
			}
			if !found {
				t.Fatal("missing local_policy_denied refusal")
			}
		})
	}
}
func TestMockGatewayRefusesNonLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", "[::]:0", "192.0.2.1:0", "localhost:0"} {
		err := mockGateway(context.Background(), []string{"--listen", addr, "--key-file", "/not-read"}, io.Discard)
		if err == nil || err.Error() != "mock gateway requires loopback IP" {
			t.Fatalf("%s: %v", addr, err)
		}
	}
}

func (f *fixture) sendCancel(m p.Message) {
	f.d.mu.Lock()
	f.d.cancel = &m
	f.d.mu.Unlock()
	f.s.cancel <- m
}
