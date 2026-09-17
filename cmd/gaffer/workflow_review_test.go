package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/httpapi"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/jobs"
	"github.com/korallis/letmecook/internal/store"
	v "github.com/korallis/letmecook/internal/verification"
	p "github.com/korallis/letmecook/schemas/execution"
)

func TestEnrollmentTokenFileBoundsAndModes(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		mode       os.FileMode
		valid      bool
	}{
		{"private", "disposable-fixture-token\n", 0600, true}, {"world-readable", "disposable-fixture-token", 0644, false}, {"group-readable", "disposable-fixture-token", 0640, false}, {"wrong-private-mode", "disposable-fixture-token", 0400, false}, {"empty", "", 0600, false}, {"whitespace", "  \n", 0600, false}, {"two-tokens", "a b", 0600, false}, {"nul", "a\x00b", 0600, false}, {"too-large", strings.Repeat("x", 1025), 0600, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(file, []byte(tc.body), tc.mode); err != nil {
				t.Fatal(err)
			}
			value, err := enrollmentToken(file)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
			if tc.valid && value != "disposable-fixture-token" {
				t.Fatal("token not trimmed")
			}
		})
	}
	root := t.TempDir()
	file := filepath.Join(root, "token")
	if err := os.WriteFile(file, []byte("disposable-fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, link, filepath.Join(root, "missing")} {
		if _, err := enrollmentToken(path); err == nil {
			t.Fatal("nonregular or absent token accepted")
		}
	}
	for _, args := range [][]string{{"identity", "enroll"}, {"identity", "enroll", "--token", "disposable-fixture-token", "--token-file", file}} {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), args, &out, &stderr); code != 2 || out.Len() != 0 || strings.Contains(stderr.String(), "disposable-fixture-token") {
			t.Fatal("usage/token disclosure", code, stderr.String())
		}
	}
}

func TestIdentityEnrollFromPrivateTokenFileHTTPS(t *testing.T) {
	f := newOwnerFixture(t)
	endpoint, client := f.serve(t)
	defer client.CloseIdleConnections()
	cert, key, pin := generate(t, false)
	invite, err := f.s.CreateEnrollment(context.Background(), f.owner, v.ID(), pin)
	if err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(t.TempDir(), "enrollment-token")
	if err = os.WriteFile(token, []byte(invite.Token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	args := []string{"--endpoint", endpoint, "--cert", cert, "--key", key, "--daemon-fingerprint", f.serverPin, "--json", "identity", "enroll", "--token-file", token}
	if code := run(context.Background(), args, &out, &stderr); code != 0 {
		t.Fatalf("enrollment exit=%d: %s", code, stderr.String())
	}
	if strings.Contains(out.String()+stderr.String(), string(invite.Token)) {
		t.Fatal("token disclosed in output")
	}
	principal, err := f.s.Authenticate(context.Background(), pin)
	if err != nil || principal.Role != "runner" || principal.Enabled {
		t.Fatal(principal, err)
	}
}

// Scripted transport fixtures below exercise CLI interpretation, not execution.
func cliTLSServer(t *testing.T, f *ownerFixture, handler http.Handler, onState func(net.Conn, http.ConnState)) string {
	t.Helper()
	ts := httptest.NewUnstartedServer(handler)
	var err error
	ts.TLS, err = i.ServerTLS(f.serverCert, f.serverKey)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.ConnState = onState
	ts.Config.ErrorLog = log.New(io.Discard, "", 0)
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts.URL
}
func TestOwnerPollingReusesPinnedTLSClient(t *testing.T) {
	f := newOwnerFixture(t)
	for _, kind := range []string{"task", "job"} {
		t.Run(kind, func(t *testing.T) {
			var connections, calls atomic.Int32
			endpoint := cliTLSServer(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if kind == "task" {
					state := p.AttemptState("assigned")
					if n >= 2 {
						state = "succeeded"
					}
					json.NewEncoder(w).Encode(store.Task{Attempts: []store.AttemptSummary{{State: state}}})
				} else {
					state := jobs.Running
					if n >= 2 {
						state = jobs.Succeeded
					}
					json.NewEncoder(w).Encode(jobs.Job{ID: testMessageID, State: state, Result: json.RawMessage(`{"complete":true}`)})
				}
			}), func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			})
			g := globals{Endpoint: endpoint, Cert: f.cert, Key: f.key, DaemonFingerprint: f.serverPin, Timeout: 5 * time.Second}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var e *cliError
			if kind == "task" {
				_, e = watchTask(ctx, g, testMessageID, "terminal")
			} else {
				_, e = waitJob(ctx, g, json.RawMessage(`{"job_id":"`+testMessageID+`"}`))
			}
			if e != nil || calls.Load() != 2 || connections.Load() != 1 {
				t.Fatal("polls did not reuse one pinned TLS connection", e, calls.Load(), connections.Load())
			}
		})
	}
}

func TestReviewAcceptUsesServerStatusNotSelfEvaluation(t *testing.T) {
	f := newOwnerFixture(t)
	taskID := v.ID()
	report := syntheticOwnerReport(v.Candidate{SelectionID: v.ID(), Identity: p.Identity{Generation: v.ID(), TaskID: taskID, AttemptID: v.ID(), Epoch: 1}, Manifest: p.Manifest{ManifestID: v.ID(), SHA256: strings.Repeat("a", 64), Bytes: 1}, BaseCommit: f.profile.Base.Commit})
	if !v.Evaluate(report, report.Candidate).Verified {
		t.Fatal("fixture must self-evaluate verified")
	}
	var posted atomic.Bool
	endpoint := cliTLSServer(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			posted.Store(true)
			w.WriteHeader(201)
			io.WriteString(w, `{}`)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/verifications/") {
			json.NewEncoder(w).Encode(report)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/verification") {
			json.NewEncoder(w).Encode(v.Summarize(report.ID, v.Status{Verified: false, Reasons: []string{"server observed stale or unavailable candidate"}}))
			return
		}
		json.NewEncoder(w).Encode(store.Task{Brief: store.TaskBrief{TaskID: taskID, Criteria: []store.Criterion{{ID: "c1", Text: "fixture"}}}})
	}), nil)
	g := globals{Endpoint: endpoint, Cert: f.cert, Key: f.key, DaemonFingerprint: f.serverPin, Timeout: 5 * time.Second, MessageID: v.ID()}
	if _, e := reviewRequest(context.Background(), g, taskID, "accept", "", "", "", ""); e == nil || e.Code != "verification_required" || posted.Load() {
		t.Fatal("accept ignored server status", e, posted.Load())
	}
}

func TestFlowTerminalRefusalIsNotReconciliation(t *testing.T) {
	for _, state := range []p.AttemptState{"cancelled", "expired", "unknown"} {
		t.Run(string(state), func(t *testing.T) {
			f := newOwnerFixture(t)
			// Real admission APIs, with only the watch response replaced by a scripted
			// terminal state to exercise flow's classification independent of S1/S2.
			ts := httptest.NewUnstartedServer(nil)
			endpoint := "https://" + ts.Listener.Addr().String()
			handler, err := httpapi.NewTLSWithDeps(f.s, endpoint, httpapi.Deps{Store: f.s, Policy: f.policy})
			if err != nil {
				t.Fatal(err)
			}
			ts.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/tasks/") && !strings.HasSuffix(r.URL.Path, "/proposal") {
					task, err := f.s.Task(r.Context(), strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/"))
					if err != nil || len(task.Attempts) != 1 {
						w.WriteHeader(500)
						return
					}
					task.Attempts[0].State = state
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(task)
					return
				}
				handler.ServeHTTP(w, r)
			})
			ts.TLS, err = i.ServerTLS(f.serverCert, f.serverKey)
			if err != nil {
				t.Fatal(err)
			}
			ts.Config.ErrorLog = log.New(io.Discard, "", 0)
			ts.StartTLS()
			defer ts.Close()
			args := []string{"--endpoint", endpoint, "--cert", f.cert, "--key", f.key, "--daemon-fingerprint", f.serverPin, "--json", "flow", "run", "--repo", f.profile.ID, "--base", f.profile.Base.Commit, "--brief-file", f.brief, "--criterion", "c1=passes", "--path", "file", "--runner", f.runner, "--harness", "fake", "--flow-id", "terminal-" + string(state), "--allow-development-isolation"}
			var out, stderr bytes.Buffer
			code := run(context.Background(), args, &out, &stderr)
			want, refusal := 1, "candidate_unavailable"
			if state == "unknown" {
				want, refusal = 4, "reconciliation_required"
			}
			if code != want || !strings.Contains(stderr.String(), `"code":"`+refusal+`"`) {
				t.Fatal("wrong terminal classification", code, stderr.String(), out.String())
			}
		})
	}
}
