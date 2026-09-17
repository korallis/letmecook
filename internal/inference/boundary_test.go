package inference

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	p "github.com/korallis/letmecook/schemas/execution"
)

const testToken = "synthetic-attempt-token-0000000000000000"
const testCredential = "synthetic-supervisor-only-credential"

type receiptJournal struct {
	mu                        sync.Mutex
	reserved, completed       []Receipt
	failReserve, failComplete bool
	file                      string
}

func (s *receiptJournal) Reserve(_ context.Context, r Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failReserve {
		return errors.New("synthetic failure")
	}
	s.reserved = append(s.reserved, r)
	return s.write(r)
}
func (s *receiptJournal) Complete(_ context.Context, r Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failComplete {
		return errors.New("synthetic failure")
	}
	s.completed = append(s.completed, r)
	return s.write(r)
}
func (s *receiptJournal) write(r Receipt) error {
	if s.file == "" {
		return nil
	}
	f, err := os.OpenFile(s.file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = json.NewEncoder(f).Encode(r); err != nil {
		return err
	}
	return f.Sync()
}
func (s *receiptJournal) snapshot() ([]Receipt, []Receipt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Receipt(nil), s.reserved...), append([]Receipt(nil), s.completed...)
}
func scopeFor(t *testing.T, gateway string) Scope {
	t.Helper()
	cred := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(cred, []byte(testCredential+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return Scope{Identity: p.Identity{Generation: requestID(), TaskID: requestID(), AttemptID: requestID(), Epoch: 1}, Token: testToken,
		Models: []string{"model-a"}, Protocols: []string{"chat_completions", "responses", "messages"},
		Gateway: Gateway{ID: "test", BaseURL: gateway, CredentialRef: CredentialRef{Kind: "file", Path: cred}, Models: []string{"model-a"}, Protocols: []string{"chat_completions", "responses", "messages"}},
		Limits:  Limits{Requests: 20, RequestBytes: 65536, ResponseBytes: 1 << 20, RequestTimeout: 3 * time.Second}}
}
func boundaryFor(t *testing.T, s Scope, gateway *httptest.Server, sink ReceiptSink) Boundary {
	t.Helper()
	caFile := filepath.Join(t.TempDir(), "gateway-ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: gateway.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	s.Gateway.CABundle = caFile
	b, err := Start(context.Background(), s, sink)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		b.Close(ctx)
	})
	return b
}
func call(t *testing.T, b Boundary, method, path, token, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+b.Addr()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

func TestProtocolsByteExactUsageAndSecretSeparation(t *testing.T) {
	tests := []struct {
		name, protocol, content, body string
		prompt, completion            int64
		status                        int
	}{
		{"chat-json", "chat_completions", "application/json", `{"choices":[],"usage":{"prompt_tokens":13,"completion_tokens":5}}`, 13, 5, 200},
		{"chat-sse", "chat_completions", "text/event-stream", "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\r\n\r\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":7}}\n\ndata: [DONE]\n\n", 17, 7, 200},
		{"responses-json", "responses", "application/json", `{"status":"completed","usage":{"input_tokens":23,"output_tokens":11}}`, 23, 11, 200},
		{"responses-sse", "responses", "text/event-stream", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":29,\"output_tokens\":13}}}\n\n", 29, 13, 200},
		{"messages-json", "messages", "application/json", `{"type":"message","usage":{"input_tokens":11,"cache_read_input_tokens":7,"cache_creation_input_tokens":3,"output_tokens":9}}`, 21, 9, 200},
		{"messages-sse", "messages", "text/event-stream", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":31,\"output_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":17}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", 31, 17, 200},
		{"upstream-5xx", "chat_completions", "application/json", `{"error":"unavailable"}`, 0, 0, 503},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requestBody := `{"model":"model-a","messages":[]}`
			if tc.protocol == "responses" {
				requestBody = `{"model":"model-a","input":[]}`
			}
			journalDir := t.TempDir()
			sink := &receiptJournal{file: filepath.Join(journalDir, "receipts.jsonl")}
			var calls atomic.Int64
			gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				reserved, _ := sink.snapshot()
				if len(reserved) != 1 {
					t.Error("upstream send preceded durable reserve")
				}
				if tc.protocol == "messages" {
					if r.Header.Get("x-api-key") != testCredential || r.Header.Get("Authorization") != "" {
						t.Error("wrong gateway auth")
					}
				} else if r.Header.Get("Authorization") != "Bearer "+testCredential || r.Header.Get("x-api-key") != "" {
					t.Error("wrong gateway auth")
				}
				if r.URL.Path != protocolPaths[tc.protocol] {
					t.Errorf("wrong path %q", r.URL.Path)
				}
				for _, k := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "Cookie", "X-Api-Key-Override", "Proxy-Authorization"} {
					if r.Header.Get(k) != "" {
						t.Error("forwarded worker header", k)
					}
				}
				raw, _ := io.ReadAll(r.Body)
				if !bytes.Equal(raw, []byte(requestBody)) {
					t.Error("changed request")
				}
				w.Header().Set("Content-Type", tc.content)
				w.Header().Set("Location", "https://untrusted.example/redirect")
				w.WriteHeader(tc.status)
				// Split every byte, including CRLF and JSON tokens, across transport writes.
				for i := range len(tc.body) {
					_, _ = w.Write([]byte{tc.body[i]})
					w.(http.Flusher).Flush()
				}
			}))
			defer gateway.Close()
			s := scopeFor(t, gateway.URL+"/v1")
			b := boundaryFor(t, s, gateway, sink)
			// Deleting the reference after Start proves requests use the read-once value.
			if err := os.Remove(s.Gateway.CredentialRef.Path); err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequest("POST", "http://"+b.Addr()+protocolPaths[tc.protocol], strings.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+testToken)
			for _, k := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "Cookie", "X-Api-Key-Override", "Proxy-Authorization"} {
				req.Header.Set(k, "worker-value")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.status || string(data) != tc.body || resp.Header.Get("Location") != "" {
				t.Fatal("response not byte exact or unsafe header forwarded")
			}
			state := b.Close(context.Background())
			if !state.Quiescent || state.Reservations != 1 || state.TerminalReceipts != 1 || calls.Load() != 1 {
				t.Fatalf("state: %+v", state)
			}
			reserved, complete := sink.snapshot()
			if len(complete) != 1 {
				t.Fatal("missing terminal receipt")
			}
			r := complete[0]
			if r.RequestID != reserved[0].RequestID || !r.Terminal || r.Status != tc.status || r.PromptTokens != tc.prompt || r.CompletionTokens != tc.completion || r.BytesOut != int64(len(tc.body)) || r.Ended.Before(r.Started) {
				t.Fatalf("receipt: %+v", r)
			}
			if tc.status == 503 && r.Source != "gateway_usage_unknown" {
				t.Fatal("missing usage claimed as zero")
			}
			raw, err := os.ReadFile(sink.file)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{testToken, testCredential, gateway.URL, s.Gateway.CredentialRef.Path} {
				if bytes.Contains(raw, []byte(secret)) {
					t.Fatal("secret or endpoint in receipt journal")
				}
			}
		})
	}
}

func TestBoundaryRefusals(t *testing.T) {
	var calls atomic.Int64
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer gateway.Close()
	s := scopeFor(t, gateway.URL)
	s.Protocols = []string{"chat_completions"}
	s.Limits.RequestBytes = 256
	b := boundaryFor(t, s, gateway, &receiptJournal{})
	for _, tc := range []struct {
		name, method, path, token, body string
		status                          int
	}{
		{"missing-token", "POST", "/v1/chat/completions", "", `{"model":"model-a"}`, 403},
		{"wrong-token", "POST", "/v1/chat/completions", "wrong", `{"model":"model-a"}`, 403},
		{"wrong-same-length-token", "POST", "/v1/chat/completions", strings.Repeat("x", len(testToken)), `{"model":"model-a"}`, 403},
		{"wrong-model", "POST", "/v1/chat/completions", testToken, `{"model":"model-b"}`, 403},
		{"disabled-protocol", "POST", "/v1/messages", testToken, `{"model":"model-a"}`, 403},
		{"discovery-subpath", "GET", "/v1/models/model-a", testToken, "", 403},
		{"unknown-path", "POST", "/admin", testToken, `{"model":"model-a"}`, 403},
		{"query", "POST", "/v1/chat/completions?host=evil", testToken, `{"model":"model-a"}`, 403},
		{"escaped-path", "POST", "/v1/chat%2fcompletions", testToken, `{"model":"model-a"}`, 403},
		{"oversize", "POST", "/v1/chat/completions", testToken, strings.Repeat(" ", 257), 413},
		{"unknown-field", "POST", "/v1/chat/completions", testToken, `{"model":"model-a","base_url":"https://evil.example"}`, 403},
		{"duplicate-field", "POST", "/v1/chat/completions", testToken, `{"model":"model-a","model":"model-b"}`, 403},
		{"nested-duplicate", "POST", "/v1/chat/completions", testToken, `{"model":"model-a","messages":[{"role":"user","role":"assistant"}]}`, 403},
		{"null-model", "POST", "/v1/chat/completions", testToken, `{"model":null}`, 403},
		{"trailing-json", "POST", "/v1/chat/completions", testToken, `{"model":"model-a"}{}`, 403},
		{"wrong-stream-type", "POST", "/v1/chat/completions", testToken, `{"model":"model-a","stream":"true"}`, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := call(t, b, tc.method, tc.path, tc.token, tc.body)
			if status != tc.status {
				t.Fatalf("%d %s", status, body)
			}
			if bytes.Contains(body, []byte(testToken)) || bytes.Contains(body, []byte(testCredential)) {
				t.Fatal("secret in refusal")
			}
		})
	}
	if calls.Load() != 0 || !b.State().Quiescent || b.State().Reservations != 0 {
		t.Fatal("refused request reached gateway")
	}
	status, body := call(t, b, "GET", "/v1/models", testToken, "")
	if status != 200 || !bytes.Contains(body, []byte(`"id":"model-a"`)) || bytes.Contains(body, []byte("model-b")) {
		t.Fatal("unscoped discovery")
	}
	req, _ := http.NewRequest("POST", "http://"+b.Addr()+"/v1/chat/completions", strings.NewReader(`{"model":"model-a"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("x-api-key refused")
	}
	req, _ = http.NewRequest("POST", "http://"+b.Addr()+"/v1/chat/completions", strings.NewReader(`{"model":"model-a"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Add("Authorization", "Bearer "+testToken)
	req.Header.Add("Authorization", "Bearer "+testToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("duplicate auth admitted")
	}
}

func TestRequestCapAndCloseDrain(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		io.WriteString(w, `{"usage":{"prompt_tokens":1,"completion_tokens":2}}`)
	}))
	defer gateway.Close()
	s := scopeFor(t, gateway.URL)
	s.Limits.Requests = 1
	b := boundaryFor(t, s, gateway, &receiptJournal{})
	first := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest("POST", "http://"+b.Addr()+"/v1/chat/completions", strings.NewReader(`{"model":"model-a"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Error(err)
			first <- 0
			return
		}
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Error(err)
		}
		first <- resp.StatusCode
	}()
	<-entered
	status, _ := call(t, b, "POST", "/v1/chat/completions", testToken, `{"model":"model-a"}`)
	if status != 429 {
		t.Fatal("request cap not enforced")
	}
	done := make(chan State, 1)
	go func() { done <- b.Close(context.Background()) }()
	select {
	case <-done:
		t.Fatal("Close acknowledged undrained work")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if <-first != 200 {
		t.Fatal("first request failed")
	}
	state := <-done
	if !state.Quiescent || state.Reservations != 1 || state.TerminalReceipts != 1 {
		t.Fatalf("%+v", state)
	}
}

func TestDroppedClientDrainsOrRemainsUnresolved(t *testing.T) {
	for _, hang := range []bool{false, true} {
		t.Run(fmt.Sprint("hang=", hang), func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"choices\":[]}\n\n")
				w.(http.Flusher).Flush()
				close(started)
				if hang {
					<-r.Context().Done()
					return
				}
				<-release
				io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n")
			}))
			defer gateway.Close()
			s := scopeFor(t, gateway.URL)
			s.Limits.RequestTimeout = 250 * time.Millisecond
			sink := &receiptJournal{}
			b := boundaryFor(t, s, gateway, sink)
			conn, err := net.Dial("tcp", b.Addr())
			if err != nil {
				t.Fatal(err)
			}
			body := `{"model":"model-a","stream":true}`
			fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: local\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", testToken, len(body), body)
			<-started
			conn.Close()
			close(release)
			state := b.Close(context.Background())
			_, complete := sink.snapshot()
			if hang {
				if state.Quiescent || state.InFlight != 0 || state.Reservations != 1 || state.TerminalReceipts != 0 || len(complete) != 0 {
					t.Fatalf("uncertain work acknowledged %+v", state)
				}
			} else if !state.Quiescent || len(complete) != 1 {
				t.Fatalf("dropped client was not drained %+v", state)
			}
		})
	}
}

func TestIncompleteResponseAndJournalFailuresHoldReservation(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind          string
		failReserve, failComplete bool
		maxBytes                  int64
	}{
		{name: "unterminated-sse", kind: "text/event-stream", body: "data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n"},
		{name: "async-response", kind: "application/json", body: `{"status":"in_progress"}`},
		{name: "response-cap", kind: "application/json", body: strings.Repeat("x", 100), maxBytes: 10},
		{name: "upstream-read-error", kind: "application/json", body: `{"usage":{"prompt_tokens":1,"completion_tokens":2}}`},
		{name: "reserve-failure", kind: "application/json", body: `{}`, failReserve: true},
		{name: "complete-failure", kind: "application/json", body: `{}`, failComplete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", tc.kind)
				if tc.name == "upstream-read-error" {
					w.Header().Set("Content-Length", "1000")
				}
				io.WriteString(w, tc.body)
			}))
			defer gateway.Close()
			sink := &receiptJournal{failReserve: tc.failReserve, failComplete: tc.failComplete}
			s := scopeFor(t, gateway.URL)
			if tc.maxBytes > 0 {
				s.Limits.ResponseBytes = tc.maxBytes
			}
			b := boundaryFor(t, s, gateway, sink)
			path := "/v1/chat/completions"
			if tc.name == "async-response" {
				path = "/v1/responses"
			}
			req, _ := http.NewRequest("POST", "http://"+b.Addr()+path, strings.NewReader(`{"model":"model-a"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+testToken)
			resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			mustAbort := tc.name == "response-cap" || tc.name == "upstream-read-error"
			if (readErr != nil) != mustAbort {
				t.Fatalf("worker body completion: got error %v, want abort %t", readErr, mustAbort)
			}
			state := b.Close(context.Background())
			if state.Quiescent || state.Reservations != 1 || state.TerminalReceipts != 0 || state.InFlight != 0 {
				t.Fatalf("uncertain work marked terminal: %+v", state)
			}
			if tc.failReserve && calls.Load() != 0 {
				t.Fatal("send despite failed reserve")
			}
		})
	}
}

func TestCredentialValidationAndScope(t *testing.T) {
	s := scopeFor(t, "https://gateway.example")
	sink := &receiptJournal{}
	for _, mode := range []os.FileMode{0644, 0400, 0660} {
		if err := os.Chmod(s.Gateway.CredentialRef.Path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := Start(context.Background(), s, sink); !errors.Is(err, ErrCredential) {
			t.Fatal("unsafe mode admitted", mode, err)
		}
	}
	os.Chmod(s.Gateway.CredentialRef.Path, 0600)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(s.Gateway.CredentialRef.Path, link); err != nil {
		t.Fatal(err)
	}
	bad := s
	bad.Gateway.CredentialRef.Path = link
	if _, err := Start(context.Background(), bad, sink); !errors.Is(err, ErrCredential) {
		t.Fatal("symlink credential admitted")
	}
	for _, change := range []func(*Scope){func(s *Scope) { s.Token = "short" }, func(s *Scope) { s.Identity.Epoch = 0 }, func(s *Scope) { s.Models = []string{"other"} }, func(s *Scope) { s.Protocols = []string{"unknown"} }, func(s *Scope) { s.Gateway.BaseURL = "http://gateway.example" }, func(s *Scope) { s.Limits.Requests = 0 }} {
		bad = s
		change(&bad)
		if _, err := Start(context.Background(), bad, sink); !errors.Is(err, ErrInvalidScope) {
			t.Fatal("invalid scope admitted", err)
		}
	}
	if _, err := Start(context.Background(), s, nil); !errors.Is(err, ErrInvalidScope) {
		t.Fatal("nil sink admitted")
	}
}

func TestNoRedirectOrEnvironmentProxy(t *testing.T) {
	var alternate atomic.Int64
	dest := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { alternate.Add(1) }))
	defer dest.Close()
	var primary atomic.Int64
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primary.Add(1)
		http.Redirect(w, r, dest.URL, http.StatusTemporaryRedirect)
	}))
	defer gateway.Close()
	t.Setenv("HTTPS_PROXY", dest.URL)
	t.Setenv("HTTP_PROXY", dest.URL)
	t.Setenv("ALL_PROXY", dest.URL)
	b := boundaryFor(t, scopeFor(t, gateway.URL), gateway, &receiptJournal{})
	status, _ := call(t, b, "POST", "/v1/chat/completions", testToken, `{"model":"model-a"}`)
	if status != 307 || primary.Load() != 1 || alternate.Load() != 0 || !b.Close(context.Background()).Quiescent {
		t.Fatal("proxy or redirect followed")
	}
	if b.(*boundary).transport.Proxy != nil {
		t.Fatal("ambient proxy enabled")
	}
}

func TestConcurrentBudget(t *testing.T) {
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) }))
	defer gateway.Close()
	s := scopeFor(t, gateway.URL)
	s.Limits.Requests = 3
	b := boundaryFor(t, s, gateway, &receiptJournal{})
	var accepted, refused atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			status, _ := call(t, b, "POST", "/v1/chat/completions", testToken, `{"model":"model-a"}`)
			if status == 200 {
				accepted.Add(1)
			} else if status == 429 {
				refused.Add(1)
			} else {
				t.Errorf("unexpected status %d", status)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 3 || refused.Load() != 17 || !b.Close(context.Background()).Quiescent {
		t.Fatal("concurrent budget overspend")
	}
}

func TestUsageFramesAndUnknowns(t *testing.T) {
	for _, protocol := range []string{"chat_completions", "responses", "messages"} {
		u := newUsageParser(protocol, "application/json")
		u.feed([]byte(`{"usage":{"input_tokens":-1,"output_tokens":null}}`))
		u.finish(503)
		r := Receipt{}
		u.apply(&r)
		if r.Source != "gateway_usage_unknown" {
			t.Fatal("invalid usage observed")
		}
	}
	u := newUsageParser("chat_completions", "text/event-stream")
	u.feed([]byte("data: " + strings.Repeat("x", maxUsageFrame+1) + "\n\ndata: [DONE]\n\n"))
	if !u.finish(200) {
		t.Fatal("oversized observation prevented draining terminal frame")
	}
}

func TestGatewayCABundleNeverDisablesVerification(t *testing.T) {
	var calls atomic.Int64
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, `{}`) }))
	defer gateway.Close()
	for _, variant := range []string{"system-roots-only", "unrelated-ca", "wrong-hostname"} {
		t.Run(variant, func(t *testing.T) {
			scope := scopeFor(t, gateway.URL)
			if variant != "system-roots-only" {
				certificate := gateway.Certificate()
				if variant == "unrelated-ca" {
					// A distinct generated root rather than httptest's shared default cert.
					cert, err := testCertificate()
					if err != nil {
						t.Fatal(err)
					}
					pemPath := filepath.Join(t.TempDir(), "unrelated.pem")
					os.WriteFile(pemPath, cert, 0600)
					scope.Gateway.CABundle = pemPath
				} else {
					pemPath := filepath.Join(t.TempDir(), "ca.pem")
					os.WriteFile(pemPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0600)
					scope.Gateway.CABundle = pemPath
					scope.Gateway.BaseURL = strings.Replace(scope.Gateway.BaseURL, "127.0.0.1", "localhost", 1)
				}
			}
			boundary, err := Start(context.Background(), scope, &receiptJournal{})
			if err != nil {
				t.Fatal(err)
			}
			status, body := call(t, boundary, "POST", "/v1/chat/completions", testToken, `{"model":"model-a"}`)
			state := boundary.Close(context.Background())
			if status != 502 || state.Quiescent || state.Reservations != 1 || state.TerminalReceipts != 0 || bytes.Contains(body, []byte(gateway.URL)) {
				t.Fatal("TLS verification bypassed or endpoint disclosed")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("credential-bearing request reached unverified TLS gateway")
	}
	scope := scopeFor(t, gateway.URL)
	scope.Gateway.CABundle = filepath.Join(t.TempDir(), "invalid.pem")
	os.WriteFile(scope.Gateway.CABundle, []byte("not a CA"), 0600)
	if _, err := Start(context.Background(), scope, &receiptJournal{}); !errors.Is(err, ErrGatewayConfig) {
		t.Fatal("invalid CA accepted")
	}
}

func testCertificate() ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func TestChunkedOversizeAndCloseDeadline(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; io.WriteString(w, `{}`) }))
	defer gateway.Close()
	scope := scopeFor(t, gateway.URL)
	scope.Limits.RequestBytes = 128
	boundary := boundaryFor(t, scope, gateway, &receiptJournal{})
	req, _ := http.NewRequest("POST", "http://"+boundary.Addr()+"/v1/chat/completions", io.NopCloser(strings.NewReader(strings.Repeat("x", 129))))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatal("chunked body evaded cap")
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		request, _ := http.NewRequest("POST", "http://"+boundary.Addr()+"/v1/chat/completions", strings.NewReader(`{"model":"model-a"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+testToken)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	state := boundary.Close(ctx)
	cancel()
	if state.Quiescent || state.Reservations != 1 || state.InFlight != 1 {
		t.Fatalf("premature drain %+v", state)
	}
	close(release)
	<-finished
	if !boundary.Close(context.Background()).Quiescent {
		t.Fatal("bounded upstream did not complete after Close deadline")
	}
}

func TestNullableProtocolDataAndTerminalAccounting(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, content, response string
		unknown, unresolved               bool
	}{
		{"chat-json", "chat_completions", "application/json", `{"choices":[{"message":{"content":null,"reasoning_content":null,"tool_calls":null},"native_finish_reason":null}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`, false, false},
		{"chat-sse", "chat_completions", "text/event-stream", "data: {\"choices\":[{\"delta\":{\"content\":null,\"reasoning_content\":null,\"tool_calls\":null},\"native_finish_reason\":null}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n", false, false},
		{"responses-json", "responses", "application/json", `{"status":"completed","max_tool_calls":null,"usage":{"input_tokens":7,"output_tokens":3}}`, false, false},
		{"responses-sse", "responses", "text/event-stream", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"max_tool_calls\":null,\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\n", false, false},
		{"messages-json", "messages", "application/json", `{"type":"message","container":null,"usage":{"input_tokens":7,"output_tokens":3}}`, false, false},
		{"messages-sse", "messages", "text/event-stream", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"container\":null,\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", false, false},
		{"failed-json-null-usage", "responses", "application/json", `{"status":"failed","usage":null,"max_tool_calls":null}`, true, false},
		{"failed-sse-null-usage", "responses", "text/event-stream", "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"usage\":null,\"max_tool_calls\":null}}\n\n", true, false},
		{"incomplete-sse-null-usage", "responses", "text/event-stream", "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"usage\":null}}\n\n", true, false},
		{"completed-sse-null-usage", "responses", "text/event-stream", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":null}}\n\n", true, false},
		{"failed-sse-bad-usage", "responses", "text/event-stream", "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"usage\":{\"input_tokens\":\"invalid\"}}}\n\n", true, false},
		{"duplicate-status-json", "responses", "application/json", `{"status":"in_progress","status":"completed","usage":null}`, true, true},
		{"duplicate-status-sse", "responses", "text/event-stream", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"in_progress\",\"status\":\"completed\",\"usage\":null}}\n\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Null schema/default/enum members and arbitrary metadata remain data.
			request := `{"model":"model-a","stream":true,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"x":{"default":null,"enum":[null,"ok"]}}}}}],"metadata":{"nested":[null,{"arbitrary":null}]}}`
			gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, _ := io.ReadAll(r.Body)
				if string(got) != request {
					t.Error("nullable data was rewritten")
				}
				w.Header().Set("Content-Type", tc.content)
				io.WriteString(w, tc.response)
			}))
			defer gateway.Close()
			sink := &receiptJournal{}
			b := boundaryFor(t, scopeFor(t, gateway.URL), gateway, sink)
			status, body := call(t, b, "POST", protocolPaths[tc.protocol], testToken, request)
			if status != 200 || string(body) != tc.response {
				t.Fatal("legal nullable data refused or response changed")
			}
			state := b.Close(context.Background())
			_, receipts := sink.snapshot()
			if tc.unresolved {
				if state.Quiescent || state.TerminalReceipts != 0 || len(receipts) != 0 {
					t.Fatal("ambiguous JSON forged terminal result")
				}
				return
			}
			if !state.Quiescent || state.Reservations != 1 || state.TerminalReceipts != 1 || len(receipts) != 1 {
				t.Fatal("terminal nullable result not durably completed", state)
			}
			r := receipts[0]
			if tc.unknown {
				if r.Source != "gateway_usage_unknown" || r.PromptTokens != 0 || r.CompletionTokens != 0 {
					t.Fatal("unobserved totals represented as known usage")
				}
			} else if r.Source != "gateway_usage" || r.PromptTokens != 7 || r.CompletionTokens != 3 {
				t.Fatal("nullable fields erased observed usage", r)
			}
		})
	}
}

func TestNullableDataDoesNotRelaxRequestAuthority(t *testing.T) {
	for _, body := range []string{`{"model":null}`, `{"model":"model-a","stream":null}`, `{"model":"model-a","background":null}`, `{"model":"model-a","background":true}`, `{"model":"model-a","unknown":null}`, `{"model":"model-a","tools":[{"parameters":{"default":null,"default":0}}]}`} {
		if _, err := requestModel([]byte(body), "responses"); err == nil {
			t.Fatalf("invalid authority or ambiguous data accepted: %s", body)
		}
	}
}
