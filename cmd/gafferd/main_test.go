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
	"runtime"
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
	os.Args = []string{os.Args[0], "--fixture"}
	if state := os.Getenv("GAFFER_TEST_STATE"); state != "" {
		os.Args = []string{os.Args[0], "--state-dir", state, "--artifacts-dir", os.Getenv("GAFFER_TEST_ARTIFACTS"), "--listen", "127.0.0.1:0"}
	}
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

func TestPersistentDaemonReopenAndOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	state, artifacts := filepath.Join(dir, "state"), filepath.Join(dir, "artifacts")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	start := func() (*exec.Cmd, a.Status) {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDaemonProcess$")
		cmd.Env = append(os.Environ(), "GAFFER_ENTRYPOINT_TEST=1", "GAFFER_TEST_STATE="+state, "GAFFER_TEST_ARTIFACTS="+artifacts)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = os.Stderr
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				cmd.Process.Kill()
				cmd.Wait()
			}
		})
		scanner := bufio.NewScanner(stdout)
		if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "store-only http://127.0.0.1:") {
			t.Fatal("persistent startup", scanner.Text())
		}
		url := strings.TrimPrefix(scanner.Text(), "store-only ")
		res, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		var status a.Status
		err = json.NewDecoder(res.Body).Decode(&status)
		res.Body.Close()
		if err != nil || status.Validate() != nil || status.Mode != "store-only" || status.TaskCount != 0 || status.EventCount != 0 {
			t.Fatal("fresh install is not empty", status, err)
		}
		res, err = client.Get(strings.TrimSuffix(url, "/api/v1/status") + "/")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatal("persistent store exposed fixture shell")
		}
		return cmd, status
	}
	first, before := start()
	var output bytes.Buffer
	args := []string{"--state-dir", state, "--artifacts-dir", artifacts, "--listen", "127.0.0.1:0"}
	if err := run(ctx, args, &output); err == nil || output.Len() != 0 {
		t.Fatal("second daemon claimed ownership")
	}
	if err := first.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(); err == nil {
		t.Fatal("SIGKILL succeeded")
	}
	second, after := start()
	if before.Generation != after.Generation || before.DaemonBoot == after.DaemonBoot {
		t.Fatal("crash recovery identity")
	}
	if err := second.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := second.Wait(); err != nil {
		t.Fatal(err)
	}
	third, graceful := start()
	if after.Generation != graceful.Generation || after.DaemonBoot == graceful.DaemonBoot {
		t.Fatal("graceful reopen identity")
	}
	third.Process.Signal(syscall.SIGTERM)
	if err := third.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "state.db")); err != nil {
		t.Fatal("shutdown removed persistent state", err)
	}
	for _, bad := range [][]string{
		nil, {"--fixture", "--state-dir", state}, {"--state-dir", state},
		{"--state-dir", state, "--artifacts-dir", artifacts, "--listen", "0.0.0.0:8000"},
		{"--state-dir", state, "--artifacts-dir", artifacts, "--listen", "localhost:8000"},
		{"--state-dir", state, "--artifacts-dir", artifacts, "--listen", "127.0.0.1:08000"},
	} {
		output.Reset()
		if err := run(ctx, bad, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid install accepted", bad)
		}
	}
}

func TestFailedStartupNoReadyClaim(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	dir := t.TempDir()
	args := []string{"--state-dir", filepath.Join(dir, "state"), "--artifacts-dir", filepath.Join(dir, "artifacts"), "--listen", "127.0.0.1:0"}
	if err := run(ctx, args, &out); err == nil || out.Len() != 0 {
		t.Fatal("failed startup claimed ready")
	}
}

func TestExecutionFlagValidation(t *testing.T) {
	dir := t.TempDir()
	base := []string{"--state-dir", filepath.Join(dir, "state"), "--artifacts-dir", filepath.Join(dir, "artifacts"), "--listen", "127.0.0.1:0"}
	for name, extra := range map[string][]string{
		"no TLS":                   {"--execution-listen", "127.0.0.1:0"},
		"TLS requires execution":   {"--tls-cert", "missing", "--tls-key", "missing", "--endpoint", "https://127.0.0.1:7443"},
		"execution hostname":       {"--execution-listen", "localhost:7444", "--tls-cert", "missing"},
		"execution port":           {"--execution-listen", "127.0.0.1:07444", "--tls-cert", "missing"},
		"profile empty":            {"--allow-development-profile="},
		"profile invalid":          {"--allow-development-profile=not/a/profile"},
		"profile duplicate":        {"--allow-development-profile=macos-sandbox-exec-dev", "--allow-development-profile=macos-sandbox-exec-dev"},
		"verification unknown":     {"--verification-isolation-profile=unknown"},
		"verification not allowed": {"--verification-isolation-profile=macos-sandbox-exec-dev"},
		"relative backup":          {"--backup-dir=relative"},
		"unclean backup":           {"--backup-dir=/private/tmp/../backups"},
		"gateway missing":          {"--gateway-config=missing"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := run(context.Background(), append(append([]string{}, base...), extra...), &out); err == nil || out.Len() != 0 {
				t.Fatal("invalid flags admitted")
			}
		})
	}
	for _, extra := range [][]string{{"--execution-listen=127.0.0.1:0"}, {"--auto-retry"}, {"--allow-development-profile=macos-sandbox-exec-dev"}, {"--verification-isolation-profile=unqualified"}, {"--backup-dir=" + filepath.Join(dir, "backup")}} {
		var out bytes.Buffer
		if err := run(context.Background(), append([]string{"--fixture"}, extra...), &out); err == nil || out.Len() != 0 {
			t.Fatal("fixture accepted install seams")
		}
	}
}

func TestPrepareVerificationState(t *testing.T) {
	t.Run("creates-private-root", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state")
		for range 2 {
			if err := prepareVerificationState(path); err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(path)
			if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
				t.Fatal("verification root is not private", info, err)
			}
		}
	})
	t.Run("does-not-create-missing-parent", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "missing")
		if err := prepareVerificationState(filepath.Join(parent, "state")); err == nil {
			t.Fatal("missing installation parent accepted")
		}
		if _, err := os.Lstat(parent); !os.IsNotExist(err) {
			t.Fatal("preflight created installation parent", err)
		}
	})
	t.Run("refuses-symlink-root", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(t.TempDir(), "state")
		if err := os.Symlink(root, path); err != nil {
			t.Fatal(err)
		}
		if err := prepareVerificationState(path); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "0700") {
			t.Fatal("symlink verification root accepted", err)
		}
	})
}

func TestDevelopmentVerificationRefusesWideStateBeforeStartup(t *testing.T) {
	_, cert, key := localCertificate(t, true)
	for name, mode := range map[string]os.FileMode{"group-read": 0740, "group-execute": 0710, "other-execute": 0701, "public": 0755} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			state, artifacts := filepath.Join(root, "state"), filepath.Join(root, "artifacts")
			if err := os.Mkdir(state, 0700); err != nil {
				t.Fatal(err)
			}
			// Set the precise existing mode independent of the test process umask.
			if err := os.Chmod(state, mode); err != nil {
				t.Fatal(err)
			}
			args := []string{"--state-dir", state, "--artifacts-dir", artifacts, "--listen", "127.0.0.1:0", "--execution-listen", "127.0.0.1:0", "--endpoint", "https://127.0.0.1", "--tls-cert", cert, "--tls-key", key, "--allow-development-profile=macos-sandbox-exec-dev", "--verification-isolation-profile=macos-sandbox-exec-dev"}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var out bytes.Buffer
			err := run(ctx, args, &out)
			if err == nil || !strings.Contains(err.Error(), state) || !strings.Contains(err.Error(), "0700") || out.Len() != 0 {
				t.Fatal("startup did not clearly refuse the non-private verification root", err, out.String())
			}
			info, err := os.Lstat(state)
			if err != nil || info.Mode().Perm() != mode {
				t.Fatal("operator permissions changed", info, err)
			}
			for _, path := range []string{filepath.Join(state, "state.db"), artifacts} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("refusal happened after store initialization", path, err)
				}
			}
		})
	}
}

// Invoke the real main in an owned child so its stderr and os.Exit are observable.
func TestDaemonDiagnosticProcess(t *testing.T) {
	if os.Getenv("GAFFER_DIAGNOSTIC_PROCESS") != "1" {
		return
	}
	for n, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[n+1:]...)
			main()
			return
		}
	}
	t.Fatal("diagnostic child requires an argument separator")
}

func TestMainStartupDiagnostics(t *testing.T) {
	_, cert, key := localCertificate(t, true)
	cases := []struct {
		name string
		mode os.FileMode
		want string
	}{
		{"missing-install-flags", 0, "configure --state-dir, --artifacts-dir and --listen"},
		{"state-permissions", 0755, "mode 0700"},
	}
	if runtime.GOOS == "darwin" {
		// The native development verifier rejects macOS system-temp exceptions.
		cases = append(cases, struct {
			name string
			mode os.FileMode
			want string
		}{"system-temp-state", 0700, "outside system-temp exception roots"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			args := []string{"-test.run=^TestDaemonDiagnosticProcess$", "--"}
			state := ""
			if tc.mode != 0 {
				parent := t.TempDir()
				if tc.name == "system-temp-state" {
					var err error
					parent, err = os.MkdirTemp("/private/tmp", "gafferd-diagnostic-")
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { os.RemoveAll(parent) })
				}
				root, err := filepath.EvalSymlinks(parent)
				if err != nil {
					t.Fatal(err)
				}
				state = filepath.Join(root, "state")
				if err := os.Mkdir(state, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(state, tc.mode); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--state-dir", state, "--artifacts-dir", filepath.Join(root, "artifacts"), "--listen", "127.0.0.1:0", "--execution-listen", "127.0.0.1:0", "--endpoint", "https://127.0.0.1", "--tls-cert", cert, "--tls-key", key, "--allow-development-profile=macos-sandbox-exec-dev", "--verification-isolation-profile=macos-sandbox-exec-dev")
			}
			cmd := exec.CommandContext(ctx, os.Args[0], args...)
			cmd.Env = append(os.Environ(), "GAFFER_DIAGNOSTIC_PROCESS=1")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 || ctx.Err() != nil {
				t.Fatalf("startup must exit 1 without hanging: %v; stderr=%s", err, stderr.String())
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "gafferd startup or shutdown failed: ") || !strings.Contains(stderr.String(), tc.want) || state != "" && !strings.Contains(stderr.String(), state) {
				t.Fatalf("missing actionable diagnostic or readiness claimed: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			if state != "" {
				info, err := os.Lstat(state)
				if err != nil || info.Mode().Perm() != tc.mode {
					t.Fatal("diagnostic refusal changed operator permissions", info, err)
				}
			}
		})
	}
}
