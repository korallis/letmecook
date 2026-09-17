package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/korallis/letmecook/internal/backup"
	"github.com/korallis/letmecook/internal/execwire"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/jobs"
	"github.com/korallis/letmecook/internal/notify"
	"github.com/korallis/letmecook/internal/reconcile"
	"github.com/korallis/letmecook/internal/runstream"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
	a "github.com/korallis/letmecook/schemas/readapi"
)

// Class selects independent bounded concurrency and request context budgets.
type Class int

const (
	Control Class = iota
	Long
	Bulk
)

type Route struct {
	Method, Pattern, Role string
	Class                 Class
	MaxBody               int64
	Raw                   bool // Stream the bounded request through Request.BodyReader.
	Handle                func(context.Context, Actor, Request) (any, *Error)
}
type Actor struct{ Fingerprint, Role, ID string }
type Request struct {
	Query      url.Values
	Body       []byte
	BodyReader io.Reader         // Raw routes only; Body stays nil. Consume within Handle and check read errors.
	Path       map[string]string // Go 1.22 path wildcards, not a reparsed URL.
	State      tls.ConnectionState
	Selected   string // negotiated ALPN, never a body field.
	Session    string // X-Gaffer-Session; S1 checks it against durable session state.
	// ContentLength is the request's declared body length (-1 when unknown), for
	// raw routes that must bind a promised byte count before reading.
	ContentLength int64
}
type Error struct {
	Status       int
	Code, Detail string
}

// Response lets route bodies select a successful status and response headers.
// Plain Handle results remain HTTP 200. Invalid statuses are programming errors.
// A []byte or io.Reader Body streams verbatim, without the JSON response bound.
// Supply Content-Type/Content-Length as appropriate. An io.ReadCloser transfers
// ownership to the handler and is closed after streaming (including errors).
type Response struct {
	Status int
	Body   any
	Header http.Header
}

// SinkReader exposes only owner-readable windows; Store.Streams' read-only
// SinkView satisfies it without leaking the serialized writer.
type SinkReader = runstream.SinkReader
type Deps struct {
	Store *store.Store
	// DaemonFingerprint is the SHA-256 DER pin of the execution listener's
	// certificate, reported to runners in the session reply.
	DaemonFingerprint string
	Sinks             SinkReader
	Jobs              jobs.Runner
	Reconcile         reconcile.Reader
	Hub               *notify.Hub
	Gateway           inference.Gateway
	Policy            sc.AdmissionPolicy
	Backup            backup.Service
}

type registration struct {
	set string
	fn  func(Deps) []Route
}

var registry struct {
	sync.RWMutex
	entries  []registration
	composed bool
}

// Register contributes a route set from a package init. Duplicate (method,
// pattern) pairs panic when composed, including duplicates across owner sets.
// Factories run only with real dependencies, never speculatively during init.
// The first composition freezes registration; later Register calls panic.
func Register(set string, fn func(Deps) []Route) {
	if set == "" || fn == nil {
		panic("invalid route registration")
	}
	registry.Lock()
	defer registry.Unlock()
	if registry.composed {
		panic("route registration after composition")
	}
	registry.entries = append(registry.entries, registration{set, fn})
}
func registered() []registration {
	registry.Lock()
	defer registry.Unlock()
	registry.composed = true
	return append([]registration(nil), registry.entries...)
}

type routePools struct{ slots [3]chan struct{} }

func newPools() *routePools {
	return &routePools{slots: [3]chan struct{}{make(chan struct{}, 16), make(chan struct{}, 8), make(chan struct{}, 2)}}
}

var budgets = [3]time.Duration{2 * time.Second, 30 * time.Second, 120 * time.Second}

func (pools *routePools) serve(class Class, h http.Handler, busy func(http.ResponseWriter, *http.Request)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case pools.slots[class] <- struct{}{}:
			defer func() { <-pools.slots[class] }()
		default:
			busy(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), budgets[class])
		defer cancel()
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newAPI(s *store.Store, host string, secure bool, dependencies ...Deps) (http.Handler, error) {
	metadata, err := s.Status(context.Background())
	if err != nil {
		return nil, err
	}
	d := Deps{Store: s}
	if len(dependencies) > 0 {
		d = dependencies[0]
		d.Store = s
	}
	pools := newPools()
	legacy := pools.serve(Control, legacyAPI(s, metadata, secure), func(w http.ResponseWriter, r *http.Request) { legacyRefuse(metadata, secure, w, r)(503, "busy") })
	mux := http.NewServeMux()
	seen := map[string]bool{
		"GET /api/v1/status": true, "GET /api/v1/snapshot": true,
		"GET /api/v1/identity/self": true, "POST /api/v1/identity/enrollments": true,
		"POST /api/v1/identity/enroll": true, "POST /api/v1/identity/update": true,
	}
	for pattern := range seen {
		mux.Handle(pattern, legacy)
	}
	if secure {
		mount(mux, seen, d, false, pools)
	} else {
		_ = registered() // Plaintext composition also freezes the init-only registry.
	}
	mux.Handle("/", legacy)
	// ServeMux redirects noncanonical paths. The old API did not: pass them to the
	// unchanged legacy handler so hostile path and method behaviour stays exact.
	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !canonicalPath(r.URL.Path) {
			legacy.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return guard(host, secure, func(w http.ResponseWriter, r *http.Request, status int, code string) {
		if secure && strings.HasPrefix(r.URL.Path, "/api/v1/") && !legacyRoutePath(r.URL.Path) {
			writeRouteError(w, false, &Error{status, code, ""})
			return
		}
		legacyRefuse(metadata, secure, w, r)(status, code)
	}, dispatch), nil
}

// NewExecution mounts only the execution set, with a runner-only per-request
// authorization gate even when no route bodies have been installed yet.
func NewExecution(s *store.Store, d Deps) (http.Handler, error) {
	configured, err := s.IdentityConfigured(context.Background())
	if err != nil {
		return nil, err
	}
	if !configured {
		return nil, i.Denied
	}
	d.Store = s
	pools := newPools()
	mux := http.NewServeMux()
	mount(mux, map[string]bool{}, d, true, pools)
	fallback := pools.serve(Control, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, failure := authenticate(r, s, true); failure != nil {
			writeRouteError(w, true, failure)
			return
		}
		writeRouteError(w, true, &Error{404, "not_found", ""})
	}), func(w http.ResponseWriter, r *http.Request) { writeRouteError(w, true, &Error{503, "busy", ""}) })
	mux.Handle("/", fallback)
	gate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !canonicalPath(r.URL.Path) {
			fallback.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return guard("", true, func(w http.ResponseWriter, r *http.Request, status int, code string) {
		writeRouteError(w, true, &Error{status, code, ""})
	}, gate), nil
}

func mount(mux *http.ServeMux, seen map[string]bool, d Deps, execution bool, pools *routePools) {
	for _, entry := range registered() {
		if (entry.set == "execution") != execution {
			continue
		}
		for _, route := range entry.fn(d) {
			role, prefix := "owner", "/api/v1/"
			if execution {
				role, prefix = "runner", "/x/v1/"
			}
			if route.Method == "" || strings.ContainsAny(route.Method, " \t\r\n") || !strings.HasPrefix(route.Pattern, prefix) || route.Role != role || route.Handle == nil || route.Class < Control || route.Class > Bulk || route.MaxBody < 0 {
				panic("invalid route")
			}
			key := route.Method + " " + route.Pattern
			if seen[key] {
				panic("duplicate route: " + key)
			}
			seen[key] = true
			h := routeHandler(d, route, execution)
			mux.Handle(key, pools.serve(route.Class, h, func(w http.ResponseWriter, r *http.Request) { writeRouteError(w, execution, &Error{503, "busy", ""}) }))
		}
	}
}

func authenticate(r *http.Request, s *store.Store, execution bool) (Actor, tls.ConnectionState, *Error) {
	var state tls.ConnectionState
	if execution {
		state, _ = ExecutionState(r.Context())
	} else if r.TLS != nil {
		state = *r.TLS
	}
	deny := &Error{403, "identity_denied", ""}
	if !state.HandshakeComplete || state.Version < tls.VersionTLS13 || len(state.PeerCertificates) != 1 || (execution && state.NegotiatedProtocol != p.FencedVersion) {
		return Actor{}, state, deny
	}
	fp, err := i.Fingerprint(state.PeerCertificates[0])
	if err != nil {
		return Actor{}, state, deny
	}
	principal, err := s.Authenticate(r.Context(), fp)
	if err != nil {
		if errors.Is(err, i.Denied) {
			return Actor{}, state, deny
		}
		return Actor{}, state, &Error{503, "store_unavailable", ""}
	}
	role := "owner"
	if execution {
		role = "runner"
	}
	if principal.Role != role || !principal.Enabled || principal.Revoked {
		return Actor{}, state, deny
	}
	return Actor{Fingerprint: fp, Role: principal.Role, ID: principal.ID}, state, nil
}
func routeHandler(d Deps, route Route, execution bool) http.Handler {
	names := []string{}
	for _, segment := range strings.Split(route.Pattern, "/") {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") && segment != "{$}" {
			names = append(names, strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}"), "..."))
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail := func(status int, code string) { writeRouteError(w, execution, &Error{status, code, ""}) }
		actor, state, failure := authenticate(r, d.Store, execution)
		if failure != nil {
			writeRouteError(w, execution, failure)
			return
		}
		// ServeMux lets a GET pattern match HEAD. No implicit method widening.
		if r.Method != route.Method {
			w.Header().Set("Allow", route.Method)
			fail(405, "method_refused")
			return
		}
		session := ""
		if execution && !(r.Method == "POST" && r.URL.Path == "/x/v1/session") {
			values := r.Header.Values("X-Gaffer-Session")
			if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
				fail(409, "session_stale")
				return
			}
			session = values[0]
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || r.URL.ForceQuery {
			fail(400, "invalid_query")
			return
		}
		maxBody := route.MaxBody
		if maxBody == 0 {
			maxBody = execwire.MaxBytes
		}
		if r.ContentLength > maxBody {
			fail(413, "oversized")
			return
		}
		if (r.Method == "GET" || r.Method == "HEAD") && (r.ContentLength != 0 || len(r.TransferEncoding) != 0) {
			fail(400, "invalid_request")
			return
		}
		bounded := http.MaxBytesReader(w, r.Body, maxBody)
		var body []byte
		var bodyReader io.Reader
		if route.Raw {
			bodyReader = bounded
		} else {
			body, err = io.ReadAll(bounded)
			if err != nil {
				fail(413, "oversized")
				return
			}
		}
		params := make(map[string]string, len(names))
		for _, name := range names {
			params[name] = r.PathValue(name)
		}
		result, failure := route.Handle(r.Context(), actor, Request{Query: q, Body: body, BodyReader: bodyReader, ContentLength: r.ContentLength, Path: params, State: state, Selected: state.NegotiatedProtocol, Session: session})
		if failure != nil {
			writeRouteError(w, execution, failure)
			return
		}
		status := http.StatusOK
		var header http.Header
		var stream io.Reader
		if response, ok := result.(Response); ok {
			if response.Status < 200 || response.Status > 299 || (response.Status == 204 && response.Body != nil) {
				fail(500, "internal")
				return
			}
			status, header, result = response.Status, response.Header, response.Body
			switch raw := response.Body.(type) {
			case []byte:
				stream = bytes.NewReader(raw)
			case io.Reader:
				stream = raw
				if closer, ok := raw.(io.Closer); ok {
					defer closer.Close()
				}
			}
		}
		var b []byte
		if status != 204 && stream == nil {
			b, err = json.Marshal(result)
			if err != nil || len(b) > a.MaxBytes {
				fail(503, "store_unavailable")
				return
			}
		}
		if stream != nil {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		for name, values := range header {
			w.Header()[name] = append([]string(nil), values...)
		}
		w.WriteHeader(status)
		if stream != nil {
			if _, err := io.Copy(w, stream); err != nil {
				// A committed response cannot become a JSON error or a clean EOF.
				panic(http.ErrAbortHandler)
			}
			return
		}
		if len(b) > 0 {
			w.Write(b)
		}
	})
}
func writeRouteError(w http.ResponseWriter, execution bool, e *Error) {
	if e.Status < 400 || e.Status > 599 {
		e = &Error{500, "internal", ""}
	}
	version := "workflow-provisional-v1"
	if execution {
		version = execwire.Version
	}
	w.WriteHeader(e.Status)
	json.NewEncoder(w).Encode(execwire.ErrorBody{Version: version, Error: e.Code, Detail: e.Detail})
}
func canonicalPath(p string) bool {
	np := path.Clean(p)
	if strings.HasSuffix(p, "/") && np != "/" {
		np += "/"
	}
	return p != "" && np == p
}

func legacyRoutePath(path string) bool {
	switch path {
	case "/api/v1/status", "/api/v1/snapshot", "/api/v1/identity/self",
		"/api/v1/identity/enrollments", "/api/v1/identity/enroll", "/api/v1/identity/update":
		return true
	}
	return false
}

func legacyRefuse(metadata a.Status, secure bool, w http.ResponseWriter, r *http.Request) func(int, string) {
	return func(status int, code string) {
		version := a.Version
		if secure && strings.HasPrefix(r.URL.Path, "/api/v1/identity/") {
			version = i.Version
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(struct {
			Version             string   `json:"version"`
			Error               string   `json:"error"`
			Mode                string   `json:"mode"`
			MissingCapabilities []string `json:"missing_capabilities"`
		}{version, code, metadata.Mode, a.MissingCapabilities()})
	}
}

// guard retains the existing Host, Origin, forwarding, RawPath and URI checks.
// An empty host is reserved for the execution listener: net/http supplies the
// accepted socket's real local address, never a client-controlled header.
func guard(host string, secure bool, refuse func(http.ResponseWriter, *http.Request, int, string), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		expected := host
		if expected == "" {
			if addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
				expected = addr.String()
			}
		}
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || (!secure && !net.ParseIP(peer).IsLoopback()) || expected == "" || r.Host != expected || r.URL.IsAbs() || r.URL.Host != "" {
			refuse(w, r, 403, "boundary_refused")
			return
		}
		if origins := r.Header.Values("Origin"); len(origins) > 1 || len(origins) == 1 && (secure || origins[0] != "http://"+expected) {
			refuse(w, r, 403, "origin_refused")
			return
		}
		for name := range r.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") || strings.EqualFold(name, "Forwarded") {
				refuse(w, r, 403, "proxy_refused")
				return
			}
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			refuse(w, r, 403, "origin_refused")
			return
		}
		if len(r.RequestURI) > 512 || r.Header.Get("Content-Encoding") != "" || (secure && (r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "")) {
			refuse(w, r, 400, "invalid_request")
			return
		}
		if r.URL.RawPath != "" {
			refuse(w, r, 404, "not_found")
			return
		}
		next.ServeHTTP(w, r)
	})
}
