package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/store"
)

// Real disposable key pairs; no checked-in credential and no insecure client mode.
func certificate(t *testing.T, server bool) (tls.Certificate, string, string) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
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
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), 0600); err != nil {
		t.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	return cert, certPath, keyPath
}
func pin(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	fp, err := i.Fingerprint(cert.Leaf)
	if err != nil {
		t.Fatal(err)
	}
	return fp
}
func clientFor(t *testing.T, server, client tls.Certificate) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(server.Leaf)
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}
	if len(client.Certificate) != 0 {
		cfg.Certificates = []tls.Certificate{client}
	}
	c := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg, Proxy: nil}, Timeout: 3 * time.Second}
	t.Cleanup(c.CloseIdleConnections)
	return c
}

const messageID = "00000000-0000-4000-8000-000000000001"

func requestBody(extra string) string {
	return `{"version":"` + i.Version + `","message_id":"` + messageID + `",` + extra + `}`
}
func exchange(t *testing.T, client *http.Client, base, path, body string, want int) []byte {
	t.Helper()
	method := "GET"
	if body != "" {
		method = "POST"
	}
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s: status %d want %d", path, res.StatusCode, want)
	}
	if res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unsafe response headers")
	}
	return b
}
func decodePrincipal(t *testing.T, b []byte) i.Principal {
	t.Helper()
	var v i.Principal
	if json.Unmarshal(b, &v) != nil {
		t.Fatal("invalid principal")
	}
	return v
}

func TestTLSIdentityLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(dir, "state"), filepath.Join(dir, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ownerCert, ownerPath, _ := certificate(t, false)
	ownerPin, err := i.ReadCertificate(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BootstrapOwner(ctx, ownerPin, false); err != nil {
		t.Fatal(err)
	}
	if err = s.BootstrapOwner(ctx, ownerPin, false); err != i.Conflict {
		t.Fatal("bootstrap replay", err)
	}
	if _, err := New(s, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8000}); err == nil {
		t.Fatal("plaintext downgrade")
	}
	serverCert, certFile, keyFile := certificate(t, true)
	tlsConfig, err := i.ServerTLS(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(nil)
	ts.TLS = tlsConfig
	var logs bytes.Buffer
	ts.Config.ErrorLog = log.New(&logs, "", 0)
	h, err := NewTLS(s, "https://"+ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = h
	ts.StartTLS()
	defer ts.Close()
	ownerClient := clientFor(t, serverCert, ownerCert)
	owner := decodePrincipal(t, exchange(t, ownerClient, ts.URL, "/api/v1/identity/self", "", 200))
	if owner.Role != "owner" || !owner.Enabled {
		t.Fatal("owner scope")
	}
	runnerCert, _, _ := certificate(t, false)
	runnerClient := clientFor(t, serverCert, runnerCert)
	wrongCert, _, _ := certificate(t, false)
	wrongClient := clientFor(t, serverCert, wrongCert)
	exchange(t, wrongClient, ts.URL, "/api/v1/status", "", 403)
	inviteBody := requestBody(`"fingerprint":"` + pin(t, runnerCert) + `"`)
	var invite store.Enrollment
	if json.Unmarshal(exchange(t, ownerClient, ts.URL, "/api/v1/identity/enrollments", inviteBody, 200), &invite) != nil {
		t.Fatal("invalid invite")
	}
	if invite.Expires <= time.Now().Unix() || invite.Expires > time.Now().Add(10*time.Minute).Unix() {
		t.Fatal("unbounded invite")
	}
	exchange(t, ownerClient, ts.URL, "/api/v1/identity/enrollments", inviteBody, 409)
	joinBody := requestBody(`"token":"` + string(invite.Token) + `"`)
	bad := exchange(t, wrongClient, ts.URL, "/api/v1/identity/enroll", joinBody, 403)
	if bytes.Contains(bad, []byte(invite.Token)) {
		t.Fatal("token reflected")
	}
	exchange(t, runnerClient, ts.URL, "/api/v1/identity/enroll", requestBody(`"token":"`+strings.Repeat("0", 64)+`"`), 403)
	// Race the real network interface. Exactly one response may enroll this pin.
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Go(func() {
			res, err := runnerClient.Post(ts.URL+"/api/v1/identity/enroll", "application/json", strings.NewReader(joinBody))
			if err != nil {
				statuses <- 0
				return
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			statuses <- res.StatusCode
		})
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[403] != 1 {
		t.Fatal("double enrollment", counts)
	}
	runner := decodePrincipal(t, exchange(t, runnerClient, ts.URL, "/api/v1/identity/self", "", 200))
	if runner.ID != invite.RunnerID || runner.Role != "runner" || runner.Enabled || runner.Revoked {
		t.Fatal("runner identity/disabled default")
	}
	exchange(t, runnerClient, ts.URL, "/api/v1/status", "", 403)
	exchange(t, runnerClient, ts.URL, "/api/v1/identity/enrollments", inviteBody, 403)
	update := func(client *http.Client, v i.Principal, action, fingerprint string, want int) i.Principal {
		return decodePrincipal(t, exchange(t, client, ts.URL, "/api/v1/identity/update", requestBody(fmt.Sprintf(`"id":%q,"revision":%d,"action":%q,"fingerprint":%q`, v.ID, v.Revision, action, fingerprint)), want))
	}
	update(runnerClient, runner, "enable", "", 403)
	enabled := update(ownerClient, runner, "enable", "", 200)
	if !enabled.Enabled || enabled.Revision != 2 {
		t.Fatal("enable CAS")
	}
	update(ownerClient, runner, "enable", "", 409)
	fresh := decodePrincipal(t, exchange(t, runnerClient, ts.URL, "/api/v1/identity/self", "", 200))
	if !fresh.Enabled {
		t.Fatal("enable not durable")
	}
	rotatedCert, _, _ := certificate(t, false)
	rotated := update(ownerClient, enabled, "rotate", pin(t, rotatedCert), 200)
	if rotated.ID != runner.ID {
		t.Fatal("rotation changed stable identity")
	}
	// Reuse the same keep-alive connection: authorization must not be TLS-cached.
	exchange(t, runnerClient, ts.URL, "/api/v1/identity/self", "", 403)
	rotatedClient := clientFor(t, serverCert, rotatedCert)
	exchange(t, rotatedClient, ts.URL, "/api/v1/identity/self", "", 200)
	revoked := update(ownerClient, rotated, "revoke", "", 200)
	if !revoked.Revoked || revoked.Enabled {
		t.Fatal("revocation state")
	}
	exchange(t, rotatedClient, ts.URL, "/api/v1/identity/self", "", 403)
	update(ownerClient, revoked, "enable", "", 409)
	newOwnerCert, _, _ := certificate(t, false)
	newOwner := update(ownerClient, owner, "rotate", pin(t, newOwnerCert), 200)
	if newOwner.ID != owner.ID {
		t.Fatal("owner rotation changed identity")
	}
	exchange(t, ownerClient, ts.URL, "/api/v1/identity/self", "", 403)
	newOwnerClient := clientFor(t, serverCert, newOwnerCert)
	exchange(t, newOwnerClient, ts.URL, "/api/v1/status", "", 200)
	update(newOwnerClient, newOwner, "revoke", "", 200)
	exchange(t, newOwnerClient, ts.URL, "/api/v1/status", "", 403)
	if strings.Contains(fmt.Sprintf("%v %#v", invite, invite), string(invite.Token)) {
		t.Fatal("formatted secret")
	}
	if strings.Contains(logs.String(), string(invite.Token)) {
		t.Fatal("logged secret")
	}
}

func TestTLSAndUntrustedRequests(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(dir, "state"), filepath.Join(dir, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, _, _ := certificate(t, false)
	if err = s.BootstrapOwner(ctx, pin(t, owner), false); err != nil {
		t.Fatal(err)
	}
	server, certFile, keyFile := certificate(t, true)
	cfg, err := i.ServerTLS(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(nil)
	ts.TLS = cfg
	ts.Config.ErrorLog = log.New(io.Discard, "", 0)
	ts.Config.Handler, err = NewTLS(s, "https://"+ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ts.StartTLS()
	defer ts.Close()
	client := clientFor(t, server, owner)
	for _, kind := range []string{"no-client", "wrong-server-root", "wrong-server-name", "tls12", "plaintext"} {
		t.Run(kind, func(t *testing.T) {
			c := clientFor(t, server, owner)
			url := ts.URL
			conf := c.Transport.(*http.Transport).TLSClientConfig
			switch kind {
			case "no-client":
				conf.Certificates = nil
			case "wrong-server-root":
				conf.RootCAs = x509.NewCertPool()
			case "wrong-server-name":
				conf.ServerName = "wrong.invalid"
			case "tls12":
				conf.MinVersion = tls.VersionTLS12
				conf.MaxVersion = tls.VersionTLS12
			case "plaintext":
				url = strings.Replace(url, "https:", "http:", 1)
			}
			res, err := c.Get(url + "/api/v1/identity/self")
			if err == nil {
				defer res.Body.Close()
				if kind != "plaintext" || res.StatusCode != 400 {
					t.Fatal("TLS mismatch accepted")
				}
			}
		})
	}
	for _, extra := range []string{
		`"fingerprint":null`, `"fingerprint":"x"`, `"fingerprint":"` + strings.Repeat("a", 64) + `","fingerprint":"` + strings.Repeat("b", 64) + `"`,
		`"fingerprint":"` + strings.Repeat("a", 64) + `","unknown":true`, `"fingerprint":{"nested":true}`, `"fingerprint":"` + strings.Repeat("a", 2050) + `"`,
	} {
		exchange(t, client, ts.URL, "/api/v1/identity/enrollments", requestBody(extra), 400)
	}
	for _, body := range []string{`null`, requestBody(`"fingerprint":"`+strings.Repeat("a", 64)+`"`) + `{}`, `{"version":"future","message_id":"` + messageID + `","fingerprint":"` + strings.Repeat("a", 64) + `"}`, string([]byte{0xff})} {
		exchange(t, client, ts.URL, "/api/v1/identity/enrollments", body, 400)
	}
	for _, header := range []string{"Origin", "Cookie", "Authorization", "Forwarded", "X-Forwarded-For", "Sec-Fetch-Site"} {
		req, _ := http.NewRequest("GET", ts.URL+"/api/v1/identity/self", nil)
		req.Header.Set(header, "untrusted-secret")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode < 400 || bytes.Contains(b, []byte("untrusted-secret")) {
			t.Fatal("header admitted/reflected", header)
		}
	}
	exchange(t, client, ts.URL, "/api/v1/identity/self?token=untrusted-secret", "", 400)
	for _, endpoint := range []string{"http://127.0.0.1:8000", "https://user:password@host", "https://host/", "https://host?x=1", "https://host#fragment"} {
		if _, err := NewTLS(s, endpoint); err == nil {
			t.Fatal("unsafe endpoint")
		}
	}
}
