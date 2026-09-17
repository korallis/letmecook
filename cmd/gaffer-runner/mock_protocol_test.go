package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	h "github.com/korallis/letmecook/internal/harness"
	"github.com/korallis/letmecook/internal/harness/opencode"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/isolation"
	p "github.com/korallis/letmecook/schemas/execution"
)

func TestMockProtocolEnvelopesAndDeadlineAbort(t *testing.T) {
	for _, protocol := range []string{"chat/completions", "responses", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			one := uint64(1)
			handler := mockGatewayHandler(context.Background(), "synthetic", []string{"model-a"}, &one)
			call := func(body, token string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/v1/"+protocol, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			for _, body := range []string{`{"model":"other"}`, `{"model":"model-a","unknown":true}`, `{"model":"model-a","stream":null}`, `{"model":"model-a","model":"model-a"}`, `{"model":"model-a","metadata":"` + strings.Repeat("x", 65536) + `"}`} {
				if got := call(body, "synthetic"); got.Code < 400 {
					t.Fatal("invalid request accepted")
				}
			}
			if got := call(`{"model":"model-a"}`, "wrong"); got.Code != 403 {
				t.Fatal(got.Code)
			}
			body := `{"model":"model-a","stream":true,"temperature":1,"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object","default":null}}}]}`
			got := call(body, "synthetic")
			if got.Code != 200 || got.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(got.Body.String(), "data:") {
				t.Fatal(got.Code, got.Body.String())
			}
			// A ResponseRecorder cannot set deadlines. Failure must abort, not return 200.
			defer func() {
				if got := recover(); got != http.ErrAbortHandler {
					t.Fatalf("deadline failure did not abort: %v", got)
				}
			}()
			call(body, "synthetic")
		})
	}
}

type mockAdapterLauncher struct{}

func (*mockAdapterLauncher) Wrap(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}
func (*mockAdapterLauncher) Observation() isolation.Observation {
	return isolation.Observation{Limitations: []string{"test-only-unconfined"}}
}
func (*mockAdapterLauncher) Cleanup() error { return nil }
func mockAdapterWorkspace(t *testing.T) h.Workspace {
	t.Helper()
	root := t.TempDir()
	w := h.Workspace{Root: filepath.Join(root, "repo"), PrivateHome: filepath.Join(root, "home"), TempDir: filepath.Join(root, "tmp"), RuntimeDir: filepath.Join(root, "runtime")}
	for _, dir := range []string{w.Root, w.PrivateHome, w.TempDir, w.RuntimeDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

type mockAdapterReceipts struct {
	mu   sync.Mutex
	path string
}

func (s *mockAdapterReceipts) Reserve(_ context.Context, r inference.Receipt) error {
	return s.append(r)
}
func (s *mockAdapterReceipts) Complete(_ context.Context, r inference.Receipt) error {
	return s.append(r)
}
func (s *mockAdapterReceipts) append(r inference.Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = json.NewEncoder(f).Encode(r); err != nil {
		return err
	}
	return f.Sync()
}

// This gate uses the same optional pinned binary as the adapter package, never
// the operator gateway. Real OpenCode request shapes cross the real boundary to
// the authenticated HTTPS mock; receipts are durably appended before forwarding.
func TestRealOpenCodeAgainstMockHangAfter(t *testing.T) {
	bin := os.Getenv("GAFFER_OPENCODE_BIN")
	if bin == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home for optional pinned binary")
		}
		bin = filepath.Join(home, ".opencode", "bin", "opencode")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("pinned OpenCode unavailable")
	}
	for _, protocol := range []string{"chat_completions", "responses", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			root := t.TempDir()
			key := filepath.Join(root, "synthetic-key")
			if err := os.WriteFile(key, []byte("synthetic-test-only"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var output lockedBuffer
			done := make(chan error, 1)
			go func() {
				done <- mockGateway(ctx, []string{"--key-file", key, "--models", "model-a", "--hang-after", "1"}, &output)
			}()
			var ready struct {
				URL    string `json:"url"`
				CAFile string `json:"ca_file"`
			}
			deadline := time.Now().Add(5 * time.Second)
			for json.Unmarshal(output.Bytes(), &ready) != nil {
				if time.Now().After(deadline) {
					t.Fatal("mock readiness timeout")
				}
				select {
				case err := <-done:
					t.Fatal(err)
				case <-time.After(5 * time.Millisecond):
				}
			}
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Error("mock shutdown timeout")
				}
			})
			adapter, err := opencode.New(bin)
			if err != nil {
				t.Fatal(err)
			}
			launcher := &mockAdapterLauncher{}
			result, err := adapter.Probe(context.Background(), h.ProbeRequest{Workspace: mockAdapterWorkspace(t), Boundary: h.BoundaryHandle{Protocol: protocol, Models: []string{"model-a"}}, Launcher: launcher})
			if err != nil || !result.ConfigIsolated {
				t.Fatalf("probe failed: %v", err)
			}
			token := "synthetic-scoped-token-000000000000000"
			scope := inference.Scope{Identity: p.Identity{Generation: uuid(), TaskID: uuid(), AttemptID: uuid(), Epoch: 1}, Token: token, Models: []string{"model-a"}, Protocols: []string{protocol}, Gateway: inference.Gateway{BaseURL: ready.URL, CABundle: ready.CAFile, CredentialRef: inference.CredentialRef{Kind: "file", Path: key}, Models: []string{"model-a"}, Protocols: []string{protocol}}, Limits: inference.Limits{Requests: 2, RequestBytes: 65536, ResponseBytes: 1 << 20, RequestTimeout: 60 * time.Second}}
			boundary, err := inference.Start(context.Background(), scope, &mockAdapterReceipts{path: filepath.Join(root, "receipts.jsonl")})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cancel()
				closeCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
				defer stop()
				boundary.Close(closeCtx)
			}()
			start := func() h.RunHandle {
				handle, err := adapter.Start(context.Background(), h.RunRequest{Workspace: mockAdapterWorkspace(t), Brief: "Reply ok without using tools.", Settings: json.RawMessage(`{"model":"model-a"}`), Boundary: h.BoundaryHandle{URL: "http://" + boundary.Addr(), Token: token, Models: []string{"model-a"}, Protocol: protocol}, Launcher: launcher, Limits: h.Limits{MaxStdoutBytes: 1 << 20}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					stopCtx, stop := context.WithTimeout(context.Background(), 8*time.Second)
					defer stop()
					adapter.Cancel(stopCtx, handle)
				})
				return handle
			}
			first := start()
			events, err := adapter.Events(context.Background(), first, 0)
			if err != nil {
				t.Fatal(err)
			}
			readCtx, stop := context.WithCancel(context.Background())
			defer stop()
			finished := make(chan error, 1)
			go func() {
				for {
					event, err := events.Next(readCtx)
					if err != nil {
						finished <- err
						return
					}
					if event.Kind == h.Completed || event.Kind == h.Failed {
						finished <- errors.New("adapter ended before mock shutdown: " + event.Kind)
						return
					}
				}
			}()
			// Real OpenCode first requests a title, then its build response. The
			// first model request completes; the second is deliberately held.
			deadline = time.Now().Add(20 * time.Second)
			for {
				state := boundary.State()
				if state.Reservations == 2 && state.TerminalReceipts == 1 && state.InFlight == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("model requests did not reach hang boundary: %+v", state)
				}
				select {
				case err := <-finished:
					t.Fatal(err, state)
				case <-time.After(10 * time.Millisecond):
				}
			}
			select {
			case err := <-finished:
				t.Fatal(err)
			case <-time.After(150 * time.Millisecond):
			}
			if state := boundary.State(); state.Reservations != 2 || state.TerminalReceipts != 1 || state.InFlight != 1 || state.Quiescent {
				t.Fatalf("hang did not retain nonterminal work: %+v", state)
			}
			cancel()
		})
	}
}

func TestMockNonStreamingProtocolsAndMessagesAuthentication(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		handler := mockGatewayHandler(context.Background(), "synthetic", []string{"model-a"}, nil)
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"model-a","stream":false}`))
		if path == "/v1/messages" {
			r.Header.Set("x-api-key", "synthetic")
		} else {
			r.Header.Set("Authorization", "Bearer synthetic")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var response struct {
			Model string         `json:"model"`
			Usage map[string]int `json:"usage"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Model != "model-a" || len(response.Usage) == 0 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
}
