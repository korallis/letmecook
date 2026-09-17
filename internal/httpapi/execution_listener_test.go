package httpapi

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	i "github.com/korallis/letmecook/internal/identity"
	p "github.com/korallis/letmecook/schemas/execution"
)

func TestExecutionListenerHTTPAndALPN(t *testing.T) {
	serverCert, certFile, keyFile := certificate(t, true)
	clientCert, _, _ := certificate(t, false)
	config, err := i.ExecutionTLS(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := ExecutionListener(raw, config, 100*time.Millisecond)
	var calls atomic.Int64
	server := &http.Server{ConnContext: ExecutionConnContext, ErrorLog: log.New(io.Discard, "", 0), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		state, ok := ExecutionState(r.Context())
		if !ok || state.NegotiatedProtocol != p.FencedVersion || r.TLS != nil {
			t.Error("not an opaque execution connection")
			w.WriteHeader(500)
			return
		}
		fp, err := i.Fingerprint(state.PeerCertificates[0])
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, fp)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() { server.Close(); <-done }()
	roots := x509.NewCertPool()
	roots.AddCert(serverCert.Leaf)
	dial := func(protocols []string) (*tls.Conn, error) {
		return tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", listener.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{clientCert}, NextProtos: protocols})
	}
	for _, protocol := range []string{"http/1.1", "h2"} {
		conn, err := dial([]string{protocol})
		if err == nil {
			conn.Close()
			t.Fatalf("%s negotiated", protocol)
		}
		if !strings.Contains(err.Error(), "application protocol") {
			t.Fatalf("not an ALPN handshake refusal: %v", err)
		}
	}
	// A client without ALPN can complete TLS, but never reaches an HTTP parser.
	conn, err := dial(nil)
	if err == nil {
		conn.SetDeadline(time.Now().Add(time.Second))
		fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: "+listener.Addr().String()+"\r\n\r\n")
		if _, err := http.ReadResponse(bufio.NewReader(conn), nil); err == nil {
			t.Fatal("ALPN-less request parsed")
		}
		conn.Close()
	}
	if calls.Load() != 0 {
		t.Fatal("refused client reached handler")
	}
	// Stall before the ClientHello; the timeout must close it and keep accepting.
	stalled, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	stalled.SetReadDeadline(time.Now().Add(2 * time.Second))
	var b [1]byte
	if _, err := stalled.Read(b[:]); err == nil {
		t.Fatal("stalled handshake not closed")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("server did not close at handshake deadline")
	}
	stalled.Close()
	conn, err = dial([]string{p.FencedVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: "+listener.Addr().String()+"\r\nConnection: close\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || string(body) != pin(t, clientCert) || calls.Load() != 1 {
		t.Fatalf("execution HTTP response: %d %q %v", response.StatusCode, body, err)
	}
	if _, ok := ExecutionState(context.Background()); ok {
		t.Fatal("invented execution state")
	}
}

func TestExecutionListenerCloseInterruptsHandshake(t *testing.T) {
	_, certFile, keyFile := certificate(t, true)
	cfg, err := i.ExecutionTLS(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := ExecutionListener(raw, cfg, time.Minute)
	done := make(chan error, 1)
	go func() { _, err := l.Accept(); done <- err }()
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	l.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed listener accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("Close left a stalled handshake")
	}
}

func TestExecutionListenerConcurrentHandshakeAndCloseWrite(t *testing.T) {
	serverCert, certFile, keyFile := certificate(t, true)
	clientCert, _, _ := certificate(t, false)
	cfg, err := i.ExecutionTLS(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := ExecutionListener(raw, cfg, 10*time.Second).(*executionListener)
	defer listener.Close()
	stalled, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	// Wait until the idle peer actually owns a handshake slot.
	deadline := time.Now().Add(time.Second)
	for {
		listener.mu.Lock()
		pending := len(listener.pending)
		listener.mu.Unlock()
		if pending == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stalled peer was not accepted")
		}
		time.Sleep(time.Millisecond)
	}
	accepted := make(chan net.Conn, 1)
	go func() { c, _ := listener.Accept(); accepted <- c }()
	roots := x509.NewCertPool()
	roots.AddCert(serverCert.Leaf)
	client, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", listener.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{clientCert}, NextProtos: []string{p.FencedVersion}})
	if err != nil {
		t.Fatal("healthy peer blocked behind stalled handshake:", err)
	}
	defer client.Close()
	var conn net.Conn
	select {
	case conn = <-accepted:
		if conn == nil {
			t.Fatal("valid connection refused")
		}
	case <-time.After(time.Second):
		t.Fatal("healthy handshake not delivered")
	}
	defer conn.Close()
	// The opaque connection must retain TLS half-close, not close the TCP reader.
	if err := conn.(*execConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	client.SetDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := client.Read(b[:]); err != io.EOF {
		t.Fatalf("missing TLS close_notify: %v", err)
	}
	if _, err := client.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.ReadFull(conn, b[:]); err != nil || b[0] != 'x' {
		t.Fatalf("half-close lost read side: %q %v", b, err)
	}
	listener.Close()
	stalled.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := stalled.Read(b[:]); err == nil {
		t.Fatal("Close left pending peer open")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("Close did not interrupt pending peer")
	}
}
