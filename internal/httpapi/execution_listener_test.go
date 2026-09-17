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
