package inference

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/korallis/letmecook/internal/inference/protocoljson"
	p "github.com/korallis/letmecook/schemas/execution"
)

var (
	ErrInvalidScope = errors.New("invalid_inference_scope")
	ErrCredential   = errors.New("inference_credential_refused")
	ErrBoundary     = errors.New("inference_boundary_unavailable")
)

var protocolPaths = map[string]string{
	"chat_completions": "/v1/chat/completions",
	"responses":        "/v1/responses",
	"messages":         "/v1/messages",
}

// Start creates a supervisor-owned boundary. All errors are deliberately opaque:
// neither the configured endpoint, credential reference nor tokens are diagnostics.
func Start(ctx context.Context, s Scope, sink ReceiptSink) (Boundary, error) {
	u, err := validateScope(s, sink)
	if err != nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrInvalidScope
	}
	var tlsConfig *tls.Config
	if s.Gateway.CABundle != "" {
		if !filepath.IsAbs(s.Gateway.CABundle) || filepath.Clean(s.Gateway.CABundle) != s.Gateway.CABundle {
			return nil, ErrInvalidScope
		}
		f, err := os.Open(s.Gateway.CABundle)
		if err != nil {
			return nil, ErrGatewayConfig
		}
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			f.Close()
			return nil, ErrGatewayConfig
		}
		pem, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		f.Close()
		if err != nil || len(pem) > 1<<20 {
			return nil, ErrGatewayConfig
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, ErrGatewayConfig
		}
		tlsConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	credential, err := loadCredential(s.Gateway.CredentialRef)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(credential), []byte(s.Token)) == 1 {
		return nil, ErrInvalidScope
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, ErrBoundary
	}
	transport := &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: s.Limits.RequestTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig: tlsConfig, TLSHandshakeTimeout: min(s.Limits.RequestTimeout, 10*time.Second),
		ResponseHeaderTimeout: s.Limits.RequestTimeout, DisableCompression: true,
		ForceAttemptHTTP2: false, MaxIdleConns: 8, MaxIdleConnsPerHost: 8,
	}
	// Do not retain the reference or the entire Scope, and copy all mutable lists.
	b := &boundary{addr: ln.Addr().String(), token: sha256.Sum256([]byte(s.Token)), credential: credential,
		base: u, models: slices.Clone(s.Models), protocols: slices.Clone(s.Protocols), limits: s.Limits,
		sink: sink, transport: transport, drained: make(chan struct{}), stopped: make(chan struct{})}
	b.client = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	b.server = &http.Server{Handler: b, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: min(s.Limits.RequestTimeout, 30*time.Second),
		IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	go func() { _ = b.server.Serve(ln); close(b.stopped) }()
	timeout := s.Limits.RequestTimeout
	go func() {
		select {
		case <-ctx.Done():
			drain, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			b.Close(drain)
		case <-b.stopped:
		}
	}()
	return b, nil
}

type boundary struct {
	addr              string
	token             [32]byte
	credential        string
	base              *url.URL
	models, protocols []string
	limits            Limits
	sink              ReceiptSink
	client            *http.Client
	transport         *http.Transport
	server            *http.Server
	mu                sync.Mutex
	state             State
	closing           bool
	drained, stopped  chan struct{}
	drainOnce         sync.Once
}

func (b *boundary) Addr() string { return b.addr }
func (b *boundary) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.state
	s.Quiescent = s.InFlight == 0 && s.Reservations == s.TerminalReceipts
	return s
}

// Close stops admission immediately and drains already reserved requests. A
// deadline returns the then-current state, never an invented terminal receipt.
// Outstanding upstream drains retain their original RequestTimeout.
func (b *boundary) Close(ctx context.Context) State {
	b.mu.Lock()
	b.closing = true
	b.signalDrainedLocked()
	b.mu.Unlock()
	if err := b.server.Shutdown(ctx); err != nil {
		_ = b.server.Close()
	}
	select {
	case <-b.drained:
	case <-ctx.Done():
	}
	b.transport.CloseIdleConnections()
	return b.State()
}
func (b *boundary) signalDrainedLocked() {
	if b.closing && b.state.InFlight == 0 {
		b.credential = ""
		b.drainOnce.Do(func() { close(b.drained) })
	}
}
func (b *boundary) finish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state.InFlight--
	b.signalDrainedLocked()
}

func (b *boundary) authenticated(r *http.Request) bool {
	auth, key := r.Header.Values("Authorization"), r.Header.Values("X-Api-Key")
	candidate := ""
	wellFormed := false
	if len(auth) == 1 && len(key) == 0 && strings.HasPrefix(auth[0], "Bearer ") {
		candidate, wellFormed = strings.TrimPrefix(auth[0], "Bearer "), true
	} else if len(key) == 1 && len(auth) == 0 {
		candidate, wellFormed = key[0], true
	}
	got := sha256.Sum256([]byte(candidate))
	equal := subtle.ConstantTimeCompare(got[:], b.token[:])
	return equal == 1 && wellFormed
}

func refuse(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":"`+code+`"}`+"\n")
}

func (b *boundary) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !b.authenticated(r) || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || r.URL.IsAbs() {
		refuse(w, http.StatusForbidden, "boundary_refused")
		return
	}
	b.mu.Lock()
	closed := b.closing
	b.mu.Unlock()
	if closed {
		refuse(w, http.StatusForbidden, "boundary_refused")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			refuse(w, 403, "boundary_refused")
			return
		}
		data := make([]map[string]string, 0, len(b.models))
		for _, m := range b.models {
			data = append(data, map[string]string{"id": m, "object": "model", "owned_by": "gaffer"})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
		return
	}
	protocol := ""
	for _, enabled := range b.protocols {
		if r.URL.Path == protocolPaths[enabled] {
			protocol = enabled
			break
		}
	}
	if r.Method != http.MethodPost || protocol == "" || r.Header.Get("Content-Encoding") != "" {
		refuse(w, 403, "boundary_refused")
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		refuse(w, 403, "boundary_refused")
		return
	}
	if r.ContentLength > b.limits.RequestBytes {
		refuse(w, 413, "request_too_large")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, b.limits.RequestBytes+1))
	if int64(len(body)) > b.limits.RequestBytes {
		refuse(w, 413, "request_too_large")
		return
	}
	if err != nil {
		refuse(w, 403, "boundary_refused")
		return
	}
	model, err := requestModel(body, protocol)
	if err != nil || !slices.Contains(b.models, model) {
		refuse(w, 403, "boundary_refused")
		return
	}

	// Admission and count are serialized, including concurrent clients. A failed
	// journal write is ambiguous durability: do not send and do not call it quiet.
	b.mu.Lock()
	if b.closing {
		b.mu.Unlock()
		refuse(w, 403, "boundary_refused")
		return
	}
	if b.state.Reservations >= b.limits.Requests {
		b.mu.Unlock()
		refuse(w, 429, "budget_exhausted")
		return
	}
	b.state.Reservations++
	b.state.InFlight++
	credential := b.credential
	b.mu.Unlock()
	defer b.finish()
	ctx, cancel := context.WithTimeout(context.Background(), b.limits.RequestTimeout)
	defer cancel()
	receipt := Receipt{RequestID: requestID(), Protocol: protocol, Model: model, BytesIn: int64(len(body)), Started: time.Now().UTC(), Source: "gateway_usage_unknown"}
	if err := b.sink.Reserve(ctx, receipt); err != nil {
		refuse(w, 503, "journal_unavailable")
		return
	}

	endpoint := *b.base
	endpoint.Path = strings.TrimSuffix(strings.TrimSuffix(endpoint.Path, "/"), "/v1") + protocolPaths[protocol]
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		refuse(w, 502, "gateway_unavailable")
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "application/json, text/event-stream")
	upstream.Header.Set("Accept-Encoding", "identity")
	upstream.Header.Set("User-Agent", "gaffer-inference-boundary/1")
	// Construct headers from scratch: no worker authentication, forwarding, host,
	// cookies, redirect or provider-routing headers cross this boundary.
	if protocol == "messages" {
		upstream.Header.Set("x-api-key", credential)
		upstream.Header.Set("anthropic-version", "2023-06-01")
	} else {
		upstream.Header.Set("Authorization", "Bearer "+credential)
	}
	resp, err := b.client.Do(upstream)
	if err != nil {
		refuse(w, 502, "gateway_unavailable")
		return
	}
	defer resp.Body.Close()
	receipt.Status = resp.StatusCode
	if resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
		// No invisible decompression or unaccounted response bodies.
		refuse(w, 502, "gateway_unavailable")
		return
	}
	parser := newUsageParser(protocol, resp.Header.Get("Content-Type"))
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(min(5*time.Second, b.limits.RequestTimeout)))
	w.Header().Set("Content-Type", safeContentType(resp.Header.Get("Content-Type")))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	dropped := controller.Flush() != nil
	buf := make([]byte, 32*1024)
	terminal, aborted := false, false
	for {
		n, readErr := resp.Body.Read(buf[:min(int64(len(buf)), b.limits.ResponseBytes-receipt.BytesOut+1)])
		if n > 0 {
			receipt.BytesOut += int64(n)
			if receipt.BytesOut > b.limits.ResponseBytes {
				aborted = true
				break
			}
			parser.feed(buf[:n])
			if !dropped && r.Context().Err() == nil {
				_ = controller.SetWriteDeadline(time.Now().Add(min(5*time.Second, b.limits.RequestTimeout)))
				_, writeErr := w.Write(buf[:n])
				dropped = writeErr != nil || controller.Flush() != nil
			} else {
				dropped = true
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				terminal = parser.finish(resp.StatusCode)
			} else {
				aborted = true
			}
			break
		}
	}
	parser.apply(&receipt)
	if aborted {
		// Close/reset the worker response rather than cleanly ending a truncated
		// body. Deferred upstream close and accounting still run; no completion.
		panic(http.ErrAbortHandler)
	}
	if !terminal {
		return
	}
	receipt.Terminal, receipt.Ended = true, time.Now().UTC()
	// Completion is independent of the worker connection. Use a bounded fresh
	// journal context even when the upstream consumed almost its whole deadline.
	journalCtx, journalCancel := context.WithTimeout(context.Background(), b.limits.RequestTimeout)
	defer journalCancel()
	if b.sink.Complete(journalCtx, receipt) == nil {
		b.mu.Lock()
		b.state.TerminalReceipts++
		b.mu.Unlock()
	}
}

func safeContentType(v string) string {
	media, _, err := mime.ParseMediaType(v)
	if err == nil && media == "text/event-stream" {
		return "text/event-stream"
	}
	return "application/json"
}

func validateScope(s Scope, sink ReceiptSink) (*url.URL, error) {
	if sink == nil || !p.ValidID(s.Identity.Generation) || !p.ValidID(s.Identity.TaskID) || !p.ValidID(s.Identity.AttemptID) || s.Identity.Epoch < 1 || s.Identity.Epoch > p.MaxInteger ||
		!label(s.Token, 1024) || len(s.Token) < 32 || !list(s.Models, 256) || !list(s.Protocols, 3) || !list(s.Gateway.Models, 256) || !list(s.Gateway.Protocols, 3) ||
		s.Limits.Requests <= 0 || s.Limits.RequestBytes <= 0 || s.Limits.RequestBytes > 64<<20 || s.Limits.ResponseBytes <= 0 || s.Limits.ResponseBytes > 1<<30 ||
		s.Limits.RequestTimeout <= 0 || s.Limits.RequestTimeout > 24*time.Hour {
		return nil, ErrInvalidScope
	}
	for _, protocol := range s.Gateway.Protocols {
		if protocolPaths[protocol] == "" {
			return nil, ErrInvalidScope
		}
	}
	for _, protocol := range s.Protocols {
		if protocolPaths[protocol] == "" || !slices.Contains(s.Gateway.Protocols, protocol) {
			return nil, ErrInvalidScope
		}
	}
	for _, model := range s.Models {
		if !slices.Contains(s.Gateway.Models, model) {
			return nil, ErrInvalidScope
		}
	}
	u, err := url.Parse(s.Gateway.BaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.String() != s.Gateway.BaseURL || strings.Contains(u.Path, "..") {
		return nil, ErrInvalidScope
	}
	return u, nil
}

func loadCredential(ref CredentialRef) (string, error) {
	if ref.Kind != "file" || !filepath.IsAbs(ref.Path) || filepath.Clean(ref.Path) != ref.Path {
		return "", ErrCredential
	}
	before, err := os.Lstat(ref.Path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 {
		return "", ErrCredential
	}
	f, err := os.Open(ref.Path)
	if err != nil {
		return "", ErrCredential
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Mode().Perm() != 0600 {
		return "", ErrCredential
	}
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(raw) > 16384 {
		return "", ErrCredential
	}
	value := strings.TrimSpace(string(raw))
	for i := range raw {
		raw[i] = 0
	}
	if value == "" {
		return "", ErrCredential
	}
	for _, ch := range value {
		if ch < 33 || ch > 126 {
			return "", ErrCredential
		}
	}
	return value, nil
}

func requestID() string {
	var id [16]byte
	// crypto/rand.Read in the selected Go runtime cannot return an error.
	_, _ = rand.Read(id[:])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	s := hex.EncodeToString(id[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// The protocol envelopes are closed; tool arguments and JSON schemas remain
// data, including nulls. Structural validation checks duplicate keys, UTF-8,
// nesting and trailing data; authority-bearing fields are checked below.
func requestModel(body []byte, protocol string) (string, error) {
	var envelope map[string]json.RawMessage
	if protocoljson.Decode(body, &envelope, len(body)) != nil || envelope == nil {
		return "", ErrInvalidScope
	}
	fields := "model stream metadata temperature top_p tools tool_choice"
	switch protocol {
	case "chat_completions":
		fields += " messages max_tokens max_completion_tokens stream_options stop frequency_penalty presence_penalty seed response_format user n logprobs top_logprobs logit_bias parallel_tool_calls reasoning_effort verbosity service_tier store prediction modalities audio functions function_call"
	case "responses":
		fields += " input instructions max_output_tokens stream_options previous_response_id parallel_tool_calls reasoning text truncation user service_tier store include background max_tool_calls safety_identifier prompt_cache_key prompt_cache_retention"
	case "messages":
		fields += " messages system max_tokens stop_sequences top_k thinking service_tier container context_management output_config output_format"
	default:
		return "", ErrInvalidScope
	}
	allowed := strings.Fields(fields)
	for k := range envelope {
		if !slices.Contains(allowed, k) {
			return "", ErrInvalidScope
		}
	}
	var model string
	if json.Unmarshal(envelope["model"], &model) != nil || !label(model, 256) {
		return "", ErrInvalidScope
	}
	if raw, ok := envelope["background"]; ok && string(raw) != "false" {
		return "", ErrInvalidScope
	}
	if raw, ok := envelope["stream"]; ok && string(raw) != "true" && string(raw) != "false" {
		return "", ErrInvalidScope
	}
	return model, nil
}
