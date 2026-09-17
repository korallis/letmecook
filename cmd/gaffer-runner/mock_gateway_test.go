package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMockGatewayHangAfter(t *testing.T) {
	key := filepath.Join(t.TempDir(), "synthetic-mock-token")
	if err := os.WriteFile(key, []byte("synthetic-test-only"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out lockedBuffer
	done := make(chan error, 1)
	go func() { done <- mockGateway(ctx, []string{"--key-file", key, "--hang-after", "1"}, &out) }()
	var ready struct {
		URL    string `json:"url"`
		CAFile string `json:"ca_file"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for json.Unmarshal(out.Bytes(), &ready) != nil {
		if time.Now().After(deadline) {
			t.Fatal("mock did not start")
		}
		select {
		case err := <-done:
			t.Fatal("mock exited", err)
		case <-time.After(5 * time.Millisecond):
		}
	}
	pem, err := os.ReadFile(ready.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("invalid mock CA")
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}}, Timeout: 15 * time.Second}
	defer client.CloseIdleConnections()
	request := func(path, token string, trace *httptrace.ClientTrace) error {
		r, err := http.NewRequest("POST", ready.URL+path, strings.NewReader(`{"model":"fixture","messages":[{"role":"user","content":"test"}]}`))
		if err != nil {
			return err
		}
		r.Header.Set("Authorization", "Bearer "+token)
		if trace != nil {
			r = r.WithContext(httptrace.WithClientTrace(r.Context(), trace))
		}
		res, err := client.Do(r)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if token != "synthetic-test-only" {
			if res.StatusCode != 403 {
				t.Errorf("unauthenticated request status %d", res.StatusCode)
			}
			return nil
		}
		if res.StatusCode != 200 {
			t.Errorf("model request status %d", res.StatusCode)
		}
		_, err = io.Copy(io.Discard, res.Body)
		return err
	}
	if err := request("/v1/chat/completions", "wrong", nil); err != nil {
		t.Fatal(err)
	}
	if err := request("/v1/chat/completions", "synthetic-test-only", nil); err != nil {
		t.Fatal("first model request", err)
	}
	written := make(chan struct{}, 1)
	blocked := make(chan error, 1)
	go func() {
		blocked <- request("/v1/chat/completions", "synthetic-test-only", &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { written <- struct{}{} }})
	}()
	select {
	case <-written:
	case err := <-blocked:
		t.Fatal("second request returned", err)
	case <-time.After(5 * time.Second):
		t.Fatal("second request not written")
	}
	// Exceed the normal server write deadline: hang mode must hold the
	// response until shutdown, not merely delay it by five seconds.
	select {
	case err := <-blocked:
		t.Fatal("second request did not hang", err)
	case <-time.After(5200 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mock did not close")
	}
	select {
	case err := <-blocked:
		if err == nil {
			t.Fatal("hung request received a successful response on close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hung request not released on close")
	}
}
