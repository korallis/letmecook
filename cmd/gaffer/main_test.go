package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/httpapi"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/store"
)

const testMessageID = "00000000-0000-4000-8000-000000000001"

func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ENDPOINT", "CERT", "KEY", "DAEMON_FINGERPRINT", "JSON", "MESSAGE_ID", "TIMEOUT"} {
		t.Setenv("GAFFER_"+name, "")
	}
}
func generate(t *testing.T, server bool) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "identity.crt"), filepath.Join(dir, "identity.key")
	args := []string{"identity", "keygen", "--cert", cert, "--key", key}
	if server {
		args = append(args, "--server", "--host", "127.0.0.1")
	}
	var out, stderr bytes.Buffer
	if code := run(context.Background(), args, &out, &stderr); code != 0 {
		t.Fatalf("keygen %d: %s", code, stderr.String())
	}
	return cert, key, strings.TrimSpace(out.String())
}
func TestIdentityKeygenAndJSON(t *testing.T) {
	clearEnv(t)
	cert, key, pin := generate(t, false)
	got, err := i.ReadCertificate(cert)
	if err != nil || got != pin {
		t.Fatal("certificate rejected", got, err)
	}
	for path, mode := range map[string]os.FileMode{cert: 0644, key: 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatal("file permissions", path, err)
		}
	}
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil || pair.Leaf.IsCA || pair.Leaf.KeyUsage != x509.KeyUsageDigitalSignature || len(pair.Leaf.ExtKeyUsage) != 1 || pair.Leaf.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatal("wrong identity extensions", err)
	}
	b, _ := os.ReadFile(key)
	block, _ := pem.Decode(b)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil || fmt.Sprintf("%T", parsed) != "ed25519.PrivateKey" {
		t.Fatal("not Ed25519", err)
	}
	original, _ := os.ReadFile(key)
	var out, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--json", "identity", "keygen", "--cert", cert, "--key", key}, &out, &stderr); code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), `"code":"identity_write_refused"`) {
		t.Fatal("overwrite admitted", code, stderr.String())
	}
	retained, _ := os.ReadFile(key)
	if !bytes.Equal(original, retained) {
		t.Fatal("existing key changed")
	}
	// If only the certificate already exists, the new key must be rolled back.
	nextKey := filepath.Join(filepath.Dir(key), "new.key")
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"identity", "keygen", "--cert", cert, "--key", nextKey}, &out, &stderr); code != 1 {
		t.Fatal("certificate overwrite admitted")
	}
	if _, err := os.Stat(nextKey); !os.IsNotExist(err) {
		t.Fatal("orphan private key")
	}
	dir := t.TempDir()
	out.Reset()
	stderr.Reset()
	args := []string{"identity", "keygen", "--cert", filepath.Join(dir, "cert"), "--key", filepath.Join(dir, "key"), "--json", "--message-id", testMessageID}
	if code := run(context.Background(), args, &out, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	var envelope struct {
		Version, Command string
		MessageID        string `json:"message_id"`
		Result           struct {
			Fingerprint string `json:"fingerprint"`
		}
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || envelope.Version != cliVersion || envelope.Command != "identity.keygen" || envelope.MessageID != testMessageID {
		t.Fatal("bad envelope", out.String())
	}
	if fp, err := i.ReadCertificate(filepath.Join(dir, "cert")); err != nil || fp != envelope.Result.Fingerprint {
		t.Fatal("JSON fingerprint mismatch")
	}
}
func TestIdentitySelfPinnedDaemon(t *testing.T) {
	clearEnv(t)
	cert, key, _ := generate(t, false)
	serverCert, serverKey, serverPin := generate(t, true)
	root := t.TempDir()
	s, err := store.Open(context.Background(), filepath.Join(root, "state"), filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ownerPin, err := i.ReadCertificate(cert)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BootstrapOwner(context.Background(), ownerPin, false); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(nil)
	ts.TLS, err = i.ServerTLS(serverCert, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.ErrorLog = log.New(io.Discard, "", 0)
	ts.Config.Handler, err = httpapi.NewTLS(s, "https://"+ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ts.StartTLS()
	defer ts.Close()
	args := []string{"identity", "self", "--endpoint", ts.URL, "--cert", cert, "--key", key, "--daemon-fingerprint", serverPin, "--json", "--message-id", testMessageID}
	var out, stderr bytes.Buffer
	if code := run(context.Background(), args, &out, &stderr); code != 0 {
		t.Fatalf("self %d: %s", code, stderr.String())
	}
	var envelope struct {
		Version, Command string
		MessageID        string `json:"message_id"`
		Result           i.Principal
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || envelope.Version != cliVersion || envelope.Command != "identity.self" || envelope.MessageID != testMessageID || envelope.Result.Role != "owner" || !envelope.Result.Enabled {
		t.Fatal("self envelope", out.String())
	}
	wrong := append([]string{}, args...)
	for n := range wrong {
		if wrong[n] == serverPin {
			wrong[n] = strings.Repeat("0", 64)
		}
	}
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), wrong, &out, &stderr); code != 3 || out.Len() != 0 {
		t.Fatal("wrong pin did not fail", code, stderr.String())
	}
	// Same DER pin but wrong hostname must still fail PKI hostname verification.
	wrong = append([]string{}, args...)
	for n := range wrong {
		if wrong[n] == ts.URL {
			wrong[n] = strings.Replace(ts.URL, "127.0.0.1", "localhost", 1)
		}
	}
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), wrong, &out, &stderr); code != 3 || out.Len() != 0 {
		t.Fatal("wrong hostname did not fail", code, stderr.String())
	}
	principal, err := s.Authenticate(context.Background(), ownerPin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateIdentity(context.Background(), ownerPin, principal.ID, principal.Revision, "revoke", ""); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), args, &out, &stderr); code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), `"code":"identity_denied"`) {
		t.Fatal("revocation", code, stderr.String())
	}
}
func TestGlobalsHelpAndExitCodes(t *testing.T) {
	clearEnv(t)
	t.Setenv("GAFFER_JSON", "true")
	t.Setenv("GAFFER_MESSAGE_ID", testMessageID)
	t.Setenv("GAFFER_TIMEOUT", "2s")
	var out, stderr bytes.Buffer
	if code := run(context.Background(), []string{"version"}, &out, &stderr); code != 0 {
		t.Fatal(code)
	}
	if out.String() != `{"version":"gaffer-cli-v1","command":"version","message_id":"`+testMessageID+`","result":"gaffer-cli-v1"}`+"\n" {
		t.Fatal("version golden", out.String())
	}
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"help", "--json=false"}, &out, &stderr); code != 0 || !strings.HasPrefix(out.String(), "gaffer (provisional)") {
		t.Fatal("global override", code, out.String())
	}
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"unknown"}, &out, &stderr); code != 2 || out.Len() != 0 || !strings.Contains(stderr.String(), `"status":400`) {
		t.Fatal("usage envelope", code, stderr.String())
	}
	for _, test := range []struct {
		status int
		code   string
		want   int
	}{{403, "identity_denied", 1}, {409, "identity_conflict", 1}, {400, "malformed", 2}, {503, "busy", 3}, {0, "network", 3}, {409, "reconciliation_required", 4}} {
		if got := exitCode(test.status, test.code); got != test.want {
			t.Fatal("exit code", got, test)
		}
	}
}
func TestNoRedirectsOrProxy(t *testing.T) {
	clearEnv(t)
	cert, key, _ := generate(t, false)
	serverCert, serverKey, pin := generate(t, true)
	cfg, err := i.ServerTLS(serverCert, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer trap.Close()
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, trap.URL, http.StatusFound) }))
	ts.TLS = cfg
	ts.StartTLS()
	defer ts.Close()
	t.Setenv("HTTPS_PROXY", trap.URL)
	g := globals{Endpoint: ts.URL, Cert: cert, Key: key, DaemonFingerprint: pin, Timeout: time.Second}
	client, err := ownerClient(g)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	response, err := client.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 302 || calls != 0 {
		t.Fatal("proxy or redirect used")
	}
	// Pin is the leaf DER digest, not a PEM digest or a subject-based identity.
	pair, err := tls.LoadX509KeyPair(serverCert, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(pair.Leaf.Raw)
	if pin != hex.EncodeToString(sum[:]) {
		t.Fatal("pin algorithm")
	}
}
