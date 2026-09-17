package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/backup"
	"github.com/korallis/letmecook/internal/httpapi"
	"github.com/korallis/letmecook/internal/jobs"
	"github.com/korallis/letmecook/internal/store"
)

const messageID = "7c2c10cc-f7e6-4e64-a13b-57b889b21f61"

func certificate(t *testing.T, server bool) tls.Certificate {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	usage := x509.ExtKeyUsageClientAuth
	if server {
		usage = x509.ExtKeyUsageServerAuth
	}
	template := x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "backup-test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}
func fingerprint(cert tls.Certificate) string {
	h := sha256.Sum256(cert.Certificate[0])
	return hex.EncodeToString(h[:])
}

type jobRecorder struct {
	submitted []jobs.Job
	err       error
}

func (j *jobRecorder) Submit(_ context.Context, job jobs.Job) (jobs.Job, error) {
	j.submitted = append(j.submitted, job)
	return job, j.err
}
func (j *jobRecorder) Get(context.Context, string) (jobs.Job, error) {
	return jobs.Job{}, sql.ErrNoRows
}

func TestOwnerBackupRouteOverMutualTLS(t *testing.T) {
	for _, mode := range []string{"jobs-unavailable", "jobs", "jobs-conflict", "unpaused", "active", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			state, artifacts := filepath.Join(root, "state"), filepath.Join(root, "artifacts")
			s, err := store.Open(context.Background(), state, artifacts)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			owner, runner, serverCert := certificate(t, false), certificate(t, false), certificate(t, true)
			if err = s.BootstrapOwner(context.Background(), fingerprint(owner), false); err != nil {
				t.Fatal(err)
			}
			invite, err := s.CreateEnrollment(context.Background(), fingerprint(owner), messageID, fingerprint(runner))
			if err != nil {
				t.Fatal(err)
			}
			principal, err := s.Enroll(context.Background(), fingerprint(runner), invite.Token)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.UpdateIdentity(context.Background(), fingerprint(owner), principal.ID, 1, "enable", ""); err != nil {
				t.Fatal(err)
			}
			// Seed only the pause singleton; S4 owns the authenticated pause command.
			db, err := sql.Open("sqlite", filepath.Join(state, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec("UPDATE daemon_state SET paused=1"); err != nil {
				t.Fatal(err)
			}
			svc, err := backup.NewService(s, artifacts, filepath.Join(state, "streams"), filepath.Join(root, "backups"))
			if err != nil {
				t.Fatal(err)
			}
			deps := httpapi.Deps{Backup: svc}
			recorder := &jobRecorder{}
			if mode == "jobs" || mode == "jobs-conflict" || mode == "unpaused" || mode == "active" {
				deps.Jobs = recorder
			}
			if mode == "jobs-conflict" {
				recorder.err = fmt.Errorf("worker submission: %w", authority.Deny("identity_conflict", "job"))
			}
			if mode == "disabled" {
				deps.Backup = nil
			}
			server := httptest.NewUnstartedServer(nil)
			handler, err := httpapi.NewTLSWithDeps(s, "https://"+server.Listener.Addr().String(), deps)
			if err != nil {
				t.Fatal(err)
			}
			server.Config.Handler = handler
			server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAnyClientCert, NextProtos: []string{"http/1.1"}}
			server.StartTLS()
			defer server.Close()
			var responseHeader http.Header
			call := func(cert tls.Certificate, body string) (int, []byte) {
				t.Helper()
				roots := x509.NewCertPool()
				roots.AddCert(serverCert.Leaf)
				transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}}}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
				response, e := client.Post(server.URL+"/api/v1/backup", "application/json", strings.NewReader(body))
				if e != nil {
					t.Fatal(e)
				}
				defer response.Body.Close()
				raw, e := io.ReadAll(response.Body)
				if e != nil {
					t.Fatal(e)
				}
				responseHeader = response.Header.Clone()
				return response.StatusCode, raw
			}
			body := `{"version":"workflow-provisional-v1","message_id":"` + messageID + `","destination":"nightly"}`
			status, raw := call(runner, body)
			if status != 403 || !bytes.Contains(raw, []byte("identity_denied")) {
				t.Fatal(status, string(raw))
			}
			status, raw = call(owner, strings.Replace(body, `"destination":"nightly"`, `"destination":"nightly","destination":"other"`, 1))
			if status != 400 {
				t.Fatal("duplicate JSON accepted", status, string(raw))
			}
			for _, name := range []string{"", ".", "..", "../escape", "nested/name", `nested\name`, strings.Repeat("x", 129), "bad\x00name", "bad\nname"} {
				encoded, e := json.Marshal(map[string]string{"version": "workflow-provisional-v1", "message_id": messageID, "destination": name})
				if e != nil {
					t.Fatal(e)
				}
				status, raw = call(owner, string(encoded))
				if status != 400 || !bytes.Contains(raw, []byte("invalid_destination")) || len(recorder.submitted) != 0 {
					t.Fatal("invalid destination queued", name, status, string(raw), recorder.submitted)
				}
			}
			if mode == "unpaused" {
				if _, err = db.Exec("UPDATE daemon_state SET paused=0"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "active" {
				if _, err = db.Exec("INSERT INTO tasks VALUES(?,'reconciling')", messageID); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec("INSERT INTO attempts VALUES(?,?,1,'unknown',1,?)", messageID, messageID, messageID); err != nil {
					t.Fatal(err)
				}
			}
			status, raw = call(owner, body)
			switch mode {
			case "disabled":
				if status != 503 || !bytes.Contains(raw, []byte("backup_unavailable")) {
					t.Fatal(status, string(raw))
				}
			case "jobs":
				if status != 202 || len(recorder.submitted) != 1 || recorder.submitted[0].ID != messageID || recorder.submitted[0].Kind != "backup" || recorder.submitted[0].SubjectID != "nightly" {
					t.Fatal(status, string(raw), recorder.submitted)
				}
				t.Run("replay-after-resume", func(t *testing.T) {
					original := append([]byte(nil), raw...)
					if _, err := s.SetPaused(context.Background(), fingerprint(owner), "7c2c10cc-f7e6-4e64-a13b-57b889b21f62", false, "resume after submission", false); err != nil {
						t.Fatal(err)
					}
					status, raw := call(owner, body)
					if status != 202 || responseHeader.Get("Idempotent-Replay") != "true" || !bytes.Equal(raw, original) || len(recorder.submitted) != 1 {
						t.Fatal("backup reply not replayed after resume", status, string(raw), responseHeader, recorder.submitted)
					}
					status, raw = call(owner, strings.Replace(body, `"nightly"`, `"different"`, 1))
					if status != 409 || !bytes.Contains(raw, []byte("identity_conflict")) || len(recorder.submitted) != 1 {
						t.Fatal("changed backup intent did not conflict", status, string(raw), recorder.submitted)
					}
				})
			case "jobs-conflict":
				if status != 409 || !bytes.Contains(raw, []byte("identity_conflict")) {
					t.Fatal(status, string(raw))
				}
			case "unpaused":
				if status != 409 || !bytes.Contains(raw, []byte("not_paused")) || len(recorder.submitted) != 0 {
					t.Fatal("unpaused job admitted", status, string(raw), recorder.submitted)
				}
			case "active":
				if status != 409 || !bytes.Contains(raw, []byte("active_execution")) || len(recorder.submitted) != 0 {
					t.Fatal("active job admitted", status, string(raw), recorder.submitted)
				}
			case "jobs-unavailable":
				if status != 503 || !bytes.Contains(raw, []byte("store_unavailable")) || !bytes.Contains(raw, []byte("jobs_unavailable")) {
					t.Fatal(status, string(raw))
				}

			}
		})
	}
}
