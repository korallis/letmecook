package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

func isolatedRegistry(t *testing.T) {
	t.Helper()
	registry.Lock()
	previous, composed := registry.entries, registry.composed
	registry.entries, registry.composed = nil, false
	registry.Unlock()
	t.Cleanup(func() { registry.Lock(); registry.entries, registry.composed = previous, composed; registry.Unlock() })
}
func routeStore(t *testing.T) (*store.Store, tls.Certificate, tls.Certificate, string) {
	t.Helper()
	root := t.TempDir()
	s, err := store.Open(context.Background(), filepath.Join(root, "state"), filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	owner, _, _ := certificate(t, false)
	if err := s.BootstrapOwner(context.Background(), pin(t, owner), false); err != nil {
		t.Fatal(err)
	}
	runner, _, _ := certificate(t, false)
	invite, err := s.CreateEnrollment(context.Background(), pin(t, owner), messageID, pin(t, runner))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := s.Enroll(context.Background(), pin(t, runner), invite.Token)
	if err != nil {
		t.Fatal(err)
	}
	return s, owner, runner, principal.ID
}
func routeRequest(cert tls.Certificate, execution bool, method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:7444"+path, strings.NewReader(body))
	r.RequestURI = path
	r.URL.Scheme = ""
	r.URL.Host = ""
	r.RemoteAddr = "127.0.0.1:12345"
	state := tls.ConnectionState{HandshakeComplete: true, Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{cert.Leaf}, NegotiatedProtocol: "http/1.1"}
	if execution {
		state.NegotiatedProtocol = p.FencedVersion
		ctx := context.WithValue(r.Context(), executionStateKey{}, state)
		ctx = context.WithValue(ctx, http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7444})
		r = r.WithContext(ctx)
		r.Header.Set("X-Gaffer-Session", messageID)
	} else {
		r.TLS = &state
	}
	return r
}
func assertRouteError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != status || body.Error != code {
		t.Fatalf("response: %d %s (wanted %d %s)", w.Code, w.Body.String(), status, code)
	}
}
func TestRegistryDuplicatePanics(t *testing.T) {
	isolatedRegistry(t)
	handler := func(context.Context, Actor, Request) (any, *Error) { return nil, nil }
	Register("first", func(Deps) []Route {
		return []Route{{Method: "GET", Pattern: "/api/v1/test", Role: "owner", Handle: handler}}
	})
	Register("second", func(Deps) []Route {
		return []Route{{Method: "GET", Pattern: "/api/v1/test", Role: "owner", Handle: handler}}
	})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate method/path did not panic")
		}
	}()
	mount(http.NewServeMux(), map[string]bool{}, Deps{}, false, newPools())
}
func TestClassPoolExhaustion(t *testing.T) {
	for class, capacity := range []int{16, 8, 2} {
		t.Run(fmt.Sprint(class), func(t *testing.T) {
			isolatedRegistry(t)
			s, owner, _, _ := routeStore(t)
			entered := make(chan struct{}, capacity)
			release := make(chan struct{})
			Register("test", func(Deps) []Route {
				return []Route{{Method: "GET", Pattern: "/api/v1/pool", Role: "owner", Class: Class(class), Handle: func(ctx context.Context, _ Actor, _ Request) (any, *Error) {
					deadline, ok := ctx.Deadline()
					remaining := time.Until(deadline)
					if !ok || remaining > budgets[class] || remaining < budgets[class]-time.Second {
						t.Errorf("class %d budget: %v", class, remaining)
					}
					entered <- struct{}{}
					<-release
					return map[string]bool{"ok": true}, nil
				}}}
			})
			h, err := NewTLS(s, "https://127.0.0.1:7444")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan int, capacity)
			for range capacity {
				go func() {
					w := httptest.NewRecorder()
					h.ServeHTTP(w, routeRequest(owner, false, "GET", "/api/v1/pool", ""))
					done <- w.Code
				}()
			}
			// Always release blocking handlers, including a failing assertion.
			defer func() {
				close(release)
				for range capacity {
					if code := <-done; code != 200 {
						t.Errorf("blocking handler status %d", code)
					}
				}
			}()
			for range capacity {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("pool handlers did not enter")
				}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, routeRequest(owner, false, "GET", "/api/v1/pool", ""))
			assertRouteError(t, w, 503, "busy")
		})
	}
}
func TestRouteRoleSessionAndPathGates(t *testing.T) {
	isolatedRegistry(t)
	s, owner, runner, runnerID := routeStore(t)
	Register("workflow", func(d Deps) []Route {
		if d.Store != s {
			t.Fatal("lost composition dependency")
		}
		return []Route{{Method: "POST", Pattern: "/api/v1/items/{id}", Role: "owner", MaxBody: 8, Handle: func(ctx context.Context, a Actor, r Request) (any, *Error) {
			if a.Role != "owner" || a.Fingerprint != pin(t, owner) || r.Path["id"] != "hello" || string(r.Body) != "payload" || r.Query.Get("x") != "1" || r.Selected != "http/1.1" {
				t.Error("route projection")
			}
			return map[string]bool{"ok": true}, nil
		}}}
	})
	Register("execution", func(Deps) []Route {
		return []Route{{Method: "GET", Pattern: "/x/v1/state", Role: "runner", Handle: func(ctx context.Context, a Actor, r Request) (any, *Error) {
			if a.ID != runnerID || r.Selected != p.FencedVersion || r.Session != messageID {
				t.Error("execution projection")
			}
			return map[string]bool{"ok": true}, nil
		}}}
	})
	ownerAPI, err := NewTLS(s, "https://127.0.0.1:7444")
	if err != nil {
		t.Fatal(err)
	}
	executionAPI, err := NewExecution(s, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	ownerAPI.ServeHTTP(w, routeRequest(owner, false, "POST", "/api/v1/items/hello?x=1", "payload"))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, tc := range []struct {
		h      http.Handler
		r      *http.Request
		status int
		code   string
	}{
		{ownerAPI, routeRequest(runner, false, "POST", "/api/v1/items/hello?x=1", "payload"), 403, "identity_denied"},
		{ownerAPI, routeRequest(owner, false, "POST", "/api/v1/items/hello?x=1", "oversized"), 413, "oversized"},
		{executionAPI, routeRequest(owner, true, "GET", "/x/v1/state", ""), 403, "identity_denied"},
		{executionAPI, routeRequest(runner, true, "GET", "/x/v1/state", ""), 403, "identity_denied"}, // disabled
	} {
		w := httptest.NewRecorder()
		tc.h.ServeHTTP(w, tc.r)
		assertRouteError(t, w, tc.status, tc.code)
	}
	principal, err := s.Authenticate(context.Background(), pin(t, runner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateIdentity(context.Background(), pin(t, owner), runnerID, principal.Revision, "enable", ""); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	executionAPI.ServeHTTP(w, routeRequest(runner, true, "GET", "/x/v1/state", ""))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("X-Gaffer-Session") },
		func(r *http.Request) { r.Header.Add("X-Gaffer-Session", messageID) },
	} {
		r := routeRequest(runner, true, "GET", "/x/v1/state", "")
		mutate(r)
		w := httptest.NewRecorder()
		executionAPI.ServeHTTP(w, r)
		assertRouteError(t, w, 409, "session_stale")
	}
	// Supplying execution ALPN in a request header cannot replace handshake state.
	r := routeRequest(runner, false, "GET", "/x/v1/state", "")
	r.Header.Set("Selected", p.FencedVersion)
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7444}))
	w = httptest.NewRecorder()
	executionAPI.ServeHTTP(w, r)
	assertRouteError(t, w, 403, "identity_denied")
	// The same established state is reauthenticated after revocation.
	principal, err = s.Authenticate(context.Background(), pin(t, runner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateIdentity(context.Background(), pin(t, owner), runnerID, principal.Revision, "revoke", ""); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	executionAPI.ServeHTTP(w, routeRequest(runner, true, "GET", "/x/v1/state", ""))
	assertRouteError(t, w, 403, "identity_denied")
}

func TestRouteResponseStatusAndHeaders(t *testing.T) {
	isolatedRegistry(t)
	s, owner, _, _ := routeStore(t)
	Register("workflow", func(Deps) []Route {
		return []Route{{Method: "POST", Pattern: "/api/v1/response/{kind}", Role: "owner", Handle: func(ctx context.Context, a Actor, r Request) (any, *Error) {
			switch r.Path["kind"] {
			case "created":
				return Response{Status: 201, Body: map[string]bool{"ok": true}, Header: http.Header{"Idempotent-Replay": []string{"true"}}}, nil
			case "empty":
				return Response{Status: 204}, nil
			case "invalid":
				return Response{Status: 700, Body: "must not be leaked", Header: http.Header{"Secret": []string{"must not be leaked"}}}, nil
			case "redirect":
				return Response{Status: 302}, nil
			case "zero":
				return Response{}, nil
			default:
				return nil, &Error{Status: 0, Code: "leak", Detail: "must not be leaked"}
			}
		}}}
	})
	h, err := NewTLS(s, "https://127.0.0.1:7444")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, routeRequest(owner, false, "POST", "/api/v1/response/created", ""))
	if w.Code != 201 || w.Header().Get("Idempotent-Replay") != "true" || w.Body.String() != `{"ok":true}` {
		t.Fatal("response projection", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, routeRequest(owner, false, "POST", "/api/v1/response/empty", ""))
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatal("204 body")
	}
	for _, kind := range []string{"invalid", "redirect", "zero", "error"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, routeRequest(owner, false, "POST", "/api/v1/response/"+kind, ""))
		assertRouteError(t, w, 500, "internal")
		if strings.Contains(w.Body.String(), "leaked") || w.Header().Get("Secret") != "" {
			t.Fatal("programming error leaked")
		}
	}
}

func TestNoncanonicalPathsNeverRedirect(t *testing.T) {
	isolatedRegistry(t)
	s, owner, _, _ := routeStore(t)
	ownerAPI, err := NewTLS(s, "https://127.0.0.1:7444")
	if err != nil {
		t.Fatal(err)
	}
	executionAPI, err := NewExecution(s, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"//", "///", "/api//v1/status", "/x/v1/../"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			ownerAPI.ServeHTTP(w, routeRequest(owner, false, "GET", path, ""))
			assertRouteError(t, w, 404, "not_found")
			if w.Header().Get("Location") != "" {
				t.Fatal("owner redirected")
			}
			r := routeRequest(owner, false, "GET", path, "")
			r.TLS = nil
			r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7444}))
			w = httptest.NewRecorder()
			executionAPI.ServeHTTP(w, r)
			assertRouteError(t, w, 403, "identity_denied")
			if w.Header().Get("Location") != "" {
				t.Fatal("execution redirected before authorization")
			}
		})
	}
	for path, want := range map[string]bool{"": false, "/": true, "//": false, "///": false, "/api/v1/": true, "/api/v1": true, "/api/v1//": false, "/api/./v1": false} {
		if canonicalPath(path) != want {
			t.Errorf("canonicalPath(%q) != %t", path, want)
		}
	}
}

func TestGuardRefusalEnvelopeByRouteFamily(t *testing.T) {
	isolatedRegistry(t)
	s, owner, _, _ := routeStore(t)
	Register("workflow", func(Deps) []Route {
		return []Route{{Method: "GET", Pattern: "/api/v1/tasks", Role: "owner", Handle: func(context.Context, Actor, Request) (any, *Error) { t.Fatal("guard bypassed"); return nil, nil }}}
	})
	h, err := NewTLS(s, "https://127.0.0.1:7444")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/tasks", "/api/v1/future", "/api/v1/status", "/api/v1/snapshot", "/api/v1/identity/self", "/api/v1/identity/enrollments", "/api/v1/identity/enroll", "/api/v1/identity/update"} {
		r := routeRequest(owner, false, "GET", path, "")
		r.Header.Set("Origin", "https://hostile.invalid")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assertRouteError(t, w, 403, "origin_refused")
		var body map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if !legacyRoutePath(path) {
			if string(body["version"]) != `"workflow-provisional-v1"` || string(body["detail"]) != `""` || len(body) != 3 {
				t.Fatalf("workflow guard envelope: %s", w.Body.String())
			}
		} else {
			version := `"read-provisional-v1"`
			if strings.HasPrefix(path, "/api/v1/identity/") {
				version = `"identity-provisional-v1"`
			}
			if string(body["version"]) != version || body["mode"] == nil || body["missing_capabilities"] == nil || len(body) != 4 {
				t.Fatalf("legacy guard envelope: %s", w.Body.String())
			}
		}
	}
}

func TestRegistryRejectsLateRegistration(t *testing.T) {
	for _, execution := range []bool{false, true} {
		t.Run(fmt.Sprint(execution), func(t *testing.T) {
			isolatedRegistry(t)
			s, _, _, _ := routeStore(t)
			Register("empty", func(Deps) []Route { return nil })
			var err error
			if execution {
				_, err = NewExecution(s, Deps{})
			} else {
				_, err = NewTLS(s, "https://127.0.0.1:7444")
			}
			if err != nil {
				t.Fatal(err)
			}
			// Both listeners may compose sequentially from the same frozen snapshot.
			if _, err := NewExecution(s, Deps{}); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if recover() == nil {
					t.Fatal("late registration did not panic")
				}
			}()
			Register("late", func(Deps) []Route { return nil })
		})
	}
}

type observedBody struct {
	io.Reader
	reads  int
	closed bool
}

func (b *observedBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (b *observedBody) Close() error               { b.closed = true; return nil }

func TestRawRouteStreamingAndBounds(t *testing.T) {
	isolatedRegistry(t)
	s, owner, _, _ := routeStore(t)
	payload := bytes.Repeat([]byte{0, 0xff, 'x'}, 1<<20) // Binary, 3 MiB, not JSON.
	var input *observedBody
	var output *observedBody
	calls := 0
	Register("workflow", func(Deps) []Route {
		return []Route{{Method: "POST", Pattern: "/api/v1/blob/{kind}", Role: "owner", Class: Bulk, MaxBody: int64(len(payload)), Raw: true, Handle: func(_ context.Context, _ Actor, r Request) (any, *Error) {
			calls++
			if r.Body != nil || r.BodyReader == nil || input.reads != 0 {
				t.Fatal("raw request buffered before Handle")
			}
			got, err := io.ReadAll(r.BodyReader)
			if r.Path["kind"] == "oversized" {
				var limit *http.MaxBytesError
				if !errors.As(err, &limit) || len(got) != len(payload) {
					t.Fatalf("unbounded raw body: %d %v", len(got), err)
				}
				return nil, &Error{413, "oversized", ""}
			}
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("streamed request mismatch: %d %v", len(got), err)
			}
			var body any = payload
			if r.Path["kind"] == "reader" {
				output = &observedBody{Reader: bytes.NewReader(payload)}
				body = output
			}
			return Response{Status: 200, Body: body, Header: http.Header{"Content-Type": []string{"application/x-artifact"}, "Content-Length": []string{strconv.Itoa(len(payload))}}}, nil
		}}}
	})
	h, err := NewTLS(s, "https://127.0.0.1:7444")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"bytes", "reader", "oversized", "declared-oversized"} {
		t.Run(kind, func(t *testing.T) {
			r := routeRequest(owner, false, "POST", "/api/v1/blob/"+kind, "")
			data := payload
			if strings.Contains(kind, "oversized") {
				data = append(append([]byte(nil), payload...), 'x')
			}
			input = &observedBody{Reader: bytes.NewReader(data)}
			r.Body, r.ContentLength = input, -1 // Unknown length must still be bounded.
			if kind == "declared-oversized" {
				r.ContentLength = int64(len(data))
			}
			before := calls
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if strings.Contains(kind, "oversized") {
				assertRouteError(t, w, 413, "oversized")
				if kind == "declared-oversized" && (calls != before || input.reads != 0) {
					t.Fatal("declared oversize entered handler")
				}
				return
			}
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), payload) || w.Header().Get("Content-Type") != "application/x-artifact" || w.Header().Get("Content-Length") != strconv.Itoa(len(payload)) {
				t.Fatalf("raw response mismatch: %d %d", w.Code, w.Body.Len())
			}
			if kind == "reader" && (!output.closed || output.reads == 0) {
				t.Fatal("response reader not consumed and closed")
			}
		})
	}
}
