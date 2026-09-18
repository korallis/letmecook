//go:build system

package system

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// faultProxy is a test-owned TCP/TLS terminator. Both legs use the real generated
// credentials, exact certificate pins, TLS 1.3 and the execution ALPN. The only
// mutation is Host to the daemon's accepted socket; bodies are forwarded intact.
// No product route, stored row or runner journal is injected or rewritten.
type faultProxy struct {
	r                                  *installation
	listener                           net.Listener
	server                             *http.Server
	client                             *http.Client
	mu                                 sync.Mutex
	dropCommit, blockLease, holdCommit bool
	commitDropped                      chan struct{}
	commits                            []object
	requests                           []object
	release                            chan struct{}
	holdRunning                        bool
	runningHeld, runningRelease        chan struct{}
	once                               sync.Once
}
type opaqueConn struct{ net.Conn }
type alpnListener struct {
	net.Listener
	cfg *tls.Config
}

func (l alpnListener) Accept() (net.Conn, error) {
	for {
		raw, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		c := tls.Server(raw, l.cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e = c.HandshakeContext(ctx)
		cancel()
		if e != nil || c.ConnectionState().NegotiatedProtocol != "execution-provisional-v2" {
			_ = c.Close()
			continue
		}
		return opaqueConn{c}, nil
	}
}
func newProxy(t *testing.T, r *installation, w *worker) *faultProxy {
	t.Helper()
	cfg := tlsConfig(t, r.root, "daemon", "execution-provisional-v2")
	cfg.RootCAs = nil
	cfg.VerifyConnection = nil
	cfg.ClientAuth = tls.RequireAnyClientCert
	runnerPin := pinFile(r.root + "/runner-" + w.repo + ".crt")
	cfg.VerifyConnection = func(c tls.ConnectionState) error {
		if len(c.PeerCertificates) != 1 || digest(c.PeerCertificates[0].Raw) != runnerPin {
			return errors.New("unexpected proxy client pin")
		}
		return nil
	}
	raw, e := net.Listen("tcp", "127.0.0.1:0")
	r.s.require(t, "proxy TCP listener", e == nil, nil)
	p := &faultProxy{r: r, listener: raw, client: w.client, commitDropped: make(chan struct{}), release: make(chan struct{})}
	p.server = &http.Server{Handler: http.HandlerFunc(p.serve), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = p.server.Serve(alpnListener{raw, cfg}) }()
	r.proxy = p
	w.proxy = p
	r.proxies = append(r.proxies, p)
	return p
}
func (p *faultProxy) endpoint() string { return "https://" + p.listener.Addr().String() }
func (p *faultProxy) close()           { p.once.Do(func() { close(p.release); _ = p.server.Close() }) }
func (p *faultProxy) flags(drop, block, hold bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dropCommit = drop
	p.blockLease = block
	p.holdCommit = hold
}

// armRunning holds one already-committed running transition reply. It changes
// no task/fixture bytes and prevents a trivial task finalizing before SIGKILL.
func (p *faultProxy) armRunning() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.holdRunning = true
	p.runningHeld = make(chan struct{})
	p.runningRelease = make(chan struct{})
}
func (p *faultProxy) releaseRunningReply() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runningRelease != nil {
		close(p.runningRelease)
		p.runningRelease = nil
	}
}
func (p *faultProxy) snapshot() object {
	p.mu.Lock()
	defer p.mu.Unlock()
	return object{"commits": append([]object{}, p.commits...), "requests": append([]object{}, p.requests...)}
}
func (p *faultProxy) serve(w http.ResponseWriter, req *http.Request) {
	body, e := io.ReadAll(io.LimitReader(req.Body, 65<<20))
	if e != nil {
		http.Error(w, "read", 400)
		return
	}
	p.mu.Lock()
	block := p.blockLease && req.URL.Path == "/x/v1/lease"
	drop := p.dropCommit && strings.HasSuffix(req.URL.Path, "/commit")
	hold := p.holdCommit && drop
	if drop {
		p.dropCommit = false
	}
	if req.Method == "POST" {
		p.requests = append(p.requests, object{"path": req.URL.Path, "at_unix_ns": time.Now().UnixNano(), "body": parse(body), "blocked": block})
	}
	p.mu.Unlock()
	if block {
		select {
		case <-req.Context().Done():
		case <-p.release:
		}
		return
	}
	up, e := http.NewRequestWithContext(req.Context(), req.Method, p.r.execURL+req.URL.RequestURI(), bytes.NewReader(body))
	if e != nil {
		http.Error(w, "request", 502)
		return
	}
	up.Header = req.Header.Clone()
	up.ContentLength = req.ContentLength
	resp, e := p.client.Do(up)
	if e != nil {
		http.Error(w, "upstream", 502)
		return
	}
	defer resp.Body.Close()
	reply, e := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if e != nil {
		http.Error(w, "response", 502)
		return
	}
	p.mu.Lock()
	holdRunning := p.holdRunning && resp.StatusCode >= 200 && resp.StatusCode < 300 && str(obj(obj(parse(body))["message"])["to"]) == "running"
	held, releaseRunning := p.runningHeld, p.runningRelease
	if holdRunning {
		p.holdRunning = false
	}
	p.mu.Unlock()
	if holdRunning {
		close(held)
		select {
		case <-releaseRunning:
		case <-p.release:
		case <-req.Context().Done():
		}
	}
	if strings.HasSuffix(req.URL.Path, "/commit") {
		p.mu.Lock()
		p.commits = append(p.commits, object{"status": resp.StatusCode, "request": parse(body), "response": parse(reply), "response_bytes": string(reply), "dropped": drop})
		p.mu.Unlock()
	}
	if drop {
		p.r.s.add("fault", "commit reply dropped after upstream response", time.Now(), object{"status": resp.StatusCode, "body": parse(reply)})
		close(p.commitDropped)
		if hold {
			select {
			case <-p.release:
			case <-req.Context().Done():
			}
		}
		if h, ok := w.(http.Hijacker); ok {
			c, _, err := h.Hijack()
			if err == nil {
				_ = c.Close()
			}
		}
		return
	}
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(reply)
}
