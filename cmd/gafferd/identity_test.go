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
	args := append(append([]string{}, base...), "--listen", addr, "--endpoint", "https://"+addr, "--tls-cert", serverFile, "--tls-key", serverKey)
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
