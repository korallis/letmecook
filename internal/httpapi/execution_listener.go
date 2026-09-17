package httpapi

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/korallis/letmecook/internal/identity"
	p "github.com/korallis/letmecook/schemas/execution"
)

// execConn deliberately is not a *tls.Conn. net/http must serve HTTP/1.1 rather
// than reject this connection as an unregistered TLSNextProto protocol.
type execConn struct {
	net.Conn
	state tls.ConnectionState
}

// CloseWrite preserves net/http's half-close path, including TLS close_notify.
func (c *execConn) CloseWrite() error { return c.Conn.(*tls.Conn).CloseWrite() }

type executionListener struct {
	net.Listener
	cfg     *tls.Config
	timeout time.Duration
	ctx     context.Context
	cancel  context.CancelFunc
	ready   chan acceptedExecution
	errors  chan error
	slots   chan struct{}
	mu      sync.Mutex
	pending map[net.Conn]struct{}
	closed  bool
}

type acceptedExecution struct {
	conn *execConn
	raw  net.Conn
}

// ExecutionListener authenticates and pins ALPN before net/http sees a byte.
// Invalid or timed-out peers are closed, not returned as fatal Accept errors.
// At most 64 connections are handshaking or awaiting delivery to Accept.
func ExecutionListener(inner net.Listener, cfg *tls.Config, handshake time.Duration) net.Listener {
	if handshake <= 0 {
		handshake = 5 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &executionListener{Listener: inner, cfg: cfg.Clone(), timeout: handshake,
		ctx: ctx, cancel: cancel, ready: make(chan acceptedExecution), errors: make(chan error),
		slots: make(chan struct{}, 64), pending: make(map[net.Conn]struct{})}
	go l.acceptRaw()
	return l
}

func (l *executionListener) acceptRaw() {
	for {
		select {
		case l.slots <- struct{}{}:
		case <-l.ctx.Done():
			return
		}
		raw, err := l.Listener.Accept()
		if err != nil {
			<-l.slots
			select {
			case l.errors <- err:
			case <-l.ctx.Done():
				return
			}
			continue
		}
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			raw.Close()
			<-l.slots
			return
		}
		l.pending[raw] = struct{}{}
		l.mu.Unlock()
		go l.handshake(raw)
	}
}

func (l *executionListener) handshake(raw net.Conn) {
	defer func() { <-l.slots }()
	// Ownership remains pending through delivery. Close also interrupts workers
	// blocked on ready, not only peers still in their TLS handshake.
	delivered := false
	defer func() {
		if !delivered {
			l.mu.Lock()
			delete(l.pending, raw)
			l.mu.Unlock()
			raw.Close()
		}
	}()
	conn := tls.Server(raw, l.cfg)
	ctx, cancel := context.WithTimeout(l.ctx, l.timeout)
	err := conn.HandshakeContext(ctx)
	cancel()
	state := conn.ConnectionState()
	if err != nil || !state.HandshakeComplete || state.NegotiatedProtocol != p.FencedVersion || len(state.PeerCertificates) != 1 {
		return
	}
	if _, err := identity.Fingerprint(state.PeerCertificates[0]); err != nil {
		return
	}
	select {
	case l.ready <- acceptedExecution{conn: &execConn{Conn: conn, state: state}, raw: raw}:
		delivered = true
	case <-l.ctx.Done():
	}
}

func (l *executionListener) Accept() (net.Conn, error) {
	select {
	case accepted := <-l.ready:
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.closed {
			return nil, net.ErrClosed
		}
		delete(l.pending, accepted.raw)
		return accepted.conn, nil
	case err := <-l.errors:
		return nil, err
	case <-l.ctx.Done():
		return nil, net.ErrClosed
	}
}

func (l *executionListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return net.ErrClosed
	}
	l.closed = true
	l.cancel()
	for c := range l.pending {
		c.Close()
	}
	return l.Listener.Close()
}

type executionStateKey struct{}

// ExecutionConnContext is the execution server's http.Server.ConnContext hook.
// It records state only from the opaque connection produced by ExecutionListener.
func ExecutionConnContext(ctx context.Context, conn net.Conn) context.Context {
	if c, ok := conn.(*execConn); ok {
		return context.WithValue(ctx, executionStateKey{}, c.state)
	}
	return ctx
}

// ExecutionState returns handshake state, never state asserted in an HTTP body.
func ExecutionState(ctx context.Context) (tls.ConnectionState, bool) {
	s, ok := ctx.Value(executionStateKey{}).(tls.ConnectionState)
	return s, ok
}
