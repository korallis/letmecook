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
type executionListener struct {
	net.Listener
	cfg     *tls.Config
	timeout time.Duration
	mu      sync.Mutex
	pending map[net.Conn]struct{}
	closed  bool
}

// ExecutionListener authenticates and pins ALPN before net/http sees a byte.
// Invalid or timed-out peers are closed, not returned as fatal Accept errors.
func ExecutionListener(inner net.Listener, cfg *tls.Config, handshake time.Duration) net.Listener {
	if handshake <= 0 {
		handshake = 5 * time.Second
	}
	return &executionListener{Listener: inner, cfg: cfg.Clone(), timeout: handshake, pending: make(map[net.Conn]struct{})}
}

func (l *executionListener) Accept() (net.Conn, error) {
	for {
		raw, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			raw.Close()
			return nil, net.ErrClosed
		}
		l.pending[raw] = struct{}{}
		l.mu.Unlock()
		conn := tls.Server(raw, l.cfg)
		ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
		err = conn.HandshakeContext(ctx)
		cancel()
		state := conn.ConnectionState()
		if err == nil {
			if !state.HandshakeComplete || state.NegotiatedProtocol != p.FencedVersion || len(state.PeerCertificates) != 1 {
				err = identity.Denied
			} else {
				_, err = identity.Fingerprint(state.PeerCertificates[0])
			}
		}
		l.mu.Lock()
		delete(l.pending, raw)
		closed := l.closed
		l.mu.Unlock()
		if err != nil || closed {
			raw.Close()
			if closed {
				return nil, net.ErrClosed
			}
			continue
		}
		return &execConn{Conn: conn, state: state}, nil
	}
}

func (l *executionListener) Close() error {
	l.mu.Lock()
	l.closed = true
	for c := range l.pending {
		c.Close()
	}
	l.mu.Unlock()
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
