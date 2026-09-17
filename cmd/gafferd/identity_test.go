package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	i "github.com/korallis/letmecook/internal/identity"
	p "github.com/korallis/letmecook/schemas/execution"
)

func localCertificate(t *testing.T, server bool) (tls.Certificate, string, string) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}
	if server {
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), 0600); err != nil {
		t.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	return cert, certFile, keyFile
}

func TestIdentityCLI(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	base := []string{"--state-dir", filepath.Join(dir, "state"), "--artifacts-dir", filepath.Join(dir, "artifacts")}
	owner, ownerFile, _ := localCertificate(t, false)
	server, serverFile, serverKey := localCertificate(t, true)
	var out bytes.Buffer
	bootstrap := append(append([]string{}, base...), "--bootstrap-owner-cert", ownerFile)
	if err := run(ctx, bootstrap, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "owner identity committed; HTTPS required\n" {
		t.Fatal("bootstrap output")
	}
	out.Reset()
	if err := run(ctx, bootstrap, &out); !errors.Is(err, i.Conflict) || out.Len() != 0 {
		t.Fatal("bootstrap replay")
	}
	if err := run(ctx, append(append([]string{}, base...), "--listen", "127.0.0.1:0"), &out); err == nil {
		t.Fatal("plaintext downgrade")
	}
	for _, bad := range [][]string{
		append(append([]string{}, bootstrap...), "--listen", "127.0.0.1:0"),
		append(append([]string{}, base...), "--listen", "127.0.0.1:0", "--tls-cert", serverFile, "--tls-key", serverKey, "--endpoint", "https://wrong.invalid"),
	} {
		if err := run(ctx, bad, &out); err == nil || out.Len() != 0 {
			t.Fatal("invalid identity config admitted")
		}
	}
	// Real run entrypoint, TLS listener and shutdown. Explicit ephemeral listener
	// is supplied by this test; no service discovery or unrelated process control.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	args := append(append([]string{}, base...), "--listen", addr, "--execution-listen", "127.0.0.1:0", "--endpoint", "https://"+addr, "--tls-cert", serverFile, "--tls-key", serverKey)
	live, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan error, 1)
	go func() { err := run(live, args, writer); writer.Close(); done <- err }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	scanner := bufio.NewScanner(reader)
	if !scanner.Scan() || scanner.Text() != "store-only https://"+addr+"/api/v1/status" {
		t.Fatal("TLS startup")
	}
	if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "execution https://127.0.0.1:") || !strings.HasSuffix(scanner.Text(), "/x/v1") {
		t.Fatal("execution startup", scanner.Text())
	}
	executionAddr := strings.TrimSuffix(strings.TrimPrefix(scanner.Text(), "execution https://"), "/x/v1")
	roots := x509.NewCertPool()
	roots.AddCert(server.Leaf)
	client := &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{owner}}}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	res, err := client.Get("https://" + addr + "/api/v1/identity/self")
	if err != nil {
		t.Fatal(err)
	}
	var identity i.Principal
	err = json.NewDecoder(res.Body).Decode(&identity)
	res.Body.Close()
	if err != nil || res.StatusCode != 200 || identity.Role != "owner" {
		t.Fatal("CLI owner authentication")
	}
	// Exercise the actual execution socket and ConnContext hook, not just its
	// startup line. Enroll and enable the runner through the live owner API.
	runner, _, _ := localCertificate(t, false)
	runnerPin, err := i.Fingerprint(runner.Leaf)
	if err != nil {
		t.Fatal(err)
	}
	runnerClient := &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{runner}}}, Timeout: time.Second}
	defer runnerClient.CloseIdleConnections()
	post := func(c *http.Client, path string, body any, result any) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		res, err := c.Post("https://"+addr+"/api/v1/identity/"+path, "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("identity %s status %d", path, res.StatusCode)
		}
		if err := json.NewDecoder(res.Body).Decode(result); err != nil {
			t.Fatal(err)
		}
	}
	var invitation struct {
		Token i.Token `json:"token"`
	}
	const messageID = "00000000-0000-4000-8000-000000000001"
	post(client, "enrollments", map[string]any{"version": i.Version, "message_id": messageID, "fingerprint": runnerPin}, &invitation)
	var principal i.Principal
	post(runnerClient, "enroll", map[string]any{"version": i.Version, "message_id": messageID, "token": invitation.Token}, &principal)
	requestExecution := func(cert tls.Certificate, wantStatus int, wantError string) {
		t.Helper()
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", executionAddr, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}, NextProtos: []string{p.FencedVersion}})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if conn.ConnectionState().NegotiatedProtocol != p.FencedVersion {
			t.Fatal("execution ALPN not selected")
		}
		conn.SetDeadline(time.Now().Add(time.Second))
		if _, err := fmt.Fprintf(conn, "GET /x/v1 HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", executionAddr); err != nil {
			t.Fatal(err)
		}
		res, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body struct{ Version, Error string }
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil || res.StatusCode != wantStatus || body.Version != "execution-channel-provisional-v1" || body.Error != wantError {
			t.Fatalf("execution response: %d %+v %v", res.StatusCode, body, err)
		}
	}
	requestExecution(owner, 403, "identity_denied")
	requestExecution(runner, 403, "identity_denied") // Enrollment alone grants no runner authority.
	post(client, "update", map[string]any{"version": i.Version, "message_id": messageID, "id": principal.ID, "revision": principal.Revision, "action": "enable", "fingerprint": ""}, &principal)
	requestExecution(runner, 404, "not_found") // Empty route set reached after the runner gate.
	// Offline recovery must fail while the daemon holds the state lock.
	_, nextOwner, _ := localCertificate(t, false)
	out.Reset()
	if err := run(ctx, append(append([]string{}, base...), "--recover-owner-cert", nextOwner), &out); err == nil || out.Len() != 0 {
		t.Fatal("online recovery admitted")
	}
	for _, arg := range []string{"--unknown=secret", "--fixture=secret"} {
		if err := run(ctx, []string{arg}, &out); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("flag value leaked")
		}
	}
}
