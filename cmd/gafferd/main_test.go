package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	a "github.com/korallis/letmecook/schemas/readapi"
)

// Run the actual command entrypoint in an owned test process, not an in-memory API.
func TestDaemonProcess(t *testing.T) {
	if os.Getenv("GAFFER_ENTRYPOINT_TEST") != "1" {
		return
	}
	os.Args = []string{os.Args[0]}
	main()
}

func TestDaemonBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "executed")
	if err := os.WriteFile(filepath.Join(dir, "gaffer.json"), []byte(`{"state":"/private/state","fixture":"secret","command":"touch `+sentinel+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.sh"), []byte("#!/bin/sh\ntouch "+sentinel+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) }))
	defer trap.Close()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDaemonProcess$")
	cmd.Dir = dir
	// Valid disposable TMPDIR also permits Go's coverage runtime to flush its
	// own files; product store still ignores it.
	cmd.Env = []string{"GAFFER_ENTRYPOINT_TEST=1", "HOME=" + dir, "TMPDIR=" + dir, "PATH=" + dir, "GAFFER_CONFIG=" + filepath.Join(dir, "gaffer.json"), "GAFFER_STATE=" + dir, "GAFFER_BIND=0.0.0.0:8000", "GAFFER_FIXTURE=" + dir, "GAFFER_ROUTER=" + trap.URL, "OPENAI_BASE_URL=" + trap.URL, "HTTP_PROXY=" + trap.URL, "HTTPS_PROXY=" + trap.URL, "GAFFER_PLUGIN=" + filepath.Join(dir, "plugin.sh")}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "fixture-only http://127.0.0.1:") {
		t.Fatalf("startup: %q %s", scanner.Text(), stderr.String())
	}
	statusURL := strings.TrimPrefix(scanner.Text(), "fixture-only ")
	base := strings.TrimSuffix(statusURL, "/api/v1/status")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	res, err := client.Get(statusURL)
	if err != nil {
		t.Fatal(err)
	}
	var status a.Status
	err = json.NewDecoder(res.Body).Decode(&status)
	res.Body.Close()
	if err != nil || status.Validate() != nil || status.TaskCount != 1 || res.StatusCode != 200 {
		t.Fatalf("status %+v %v", status, err)
	}
	res, err = client.Get(base + "/api/v1/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot a.Snapshot
	err = json.NewDecoder(res.Body).Decode(&snapshot)
	res.Body.Close()
	if err != nil || snapshot.Validate() != nil || snapshot.Generation != status.Generation {
		t.Fatal("invalid store snapshot", err)
	}
	for _, path := range []string{"/api/v1/import", "/api/v1/enroll", "/api/v1/execute", "/api/v1/grant", "/api/v1/accept", "/api/v1/publish", "/api/v1/model", "/api/v1/plugin"} {
		res, err = client.Post(base+path, "application/json", strings.NewReader(`{"url":"`+trap.URL+`","command":"`+filepath.Join(dir, "plugin.sh")+`","path":"`+dir+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != 405 {
			t.Fatal("mutation accepted")
		}
	}
	if err = cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err != nil {
		t.Fatalf("shutdown: %v %s", err, stderr.String())
	}
	if hits.Load() != 0 {
		t.Fatal("malicious configuration caused egress")
	}
	if _, err = os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("repository plugin executed")
	}
	for _, args := range [][]string{{"--bind=0.0.0.0:8000"}, {"--state=" + dir}, {"--fixture=" + dir}, {"--config=" + dir}, {"import", dir}, {"--execute"}, {dir}} {
		var output bytes.Buffer
		if err = run(ctx, args, &output); err == nil || output.Len() != 0 {
			t.Fatal("startup option accepted", args)
		}
	}
}

func TestFailedStartupNoReadyClaim(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := run(ctx, nil, &out); err == nil || out.Len() != 0 {
		t.Fatal("failed startup claimed ready")
	}
}
