package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/isolation"
)

func TestDarwinSandboxCanaries(t *testing.T) {
	home, _ := os.UserHomeDir()
	root, err := os.MkdirTemp(home, ".gaffer-sandbox-canary-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	config := filepath.Join(home, ".config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	secret, err := os.CreateTemp(config, "gaffer-canary-")
	if err != nil {
		t.Fatal(err)
	}
	secret.WriteString("synthetic-home-canary")
	secret.Close()
	t.Cleanup(func() { os.Remove(secret.Name()) })
	forbidden, err := os.CreateTemp(home, "gaffer-outside-canary-")
	if err != nil {
		t.Fatal(err)
	}
	forbidden.Close()
	t.Cleanup(func() { os.Remove(forbidden.Name()) })
	credential := filepath.Join(root, "credential")
	os.WriteFile(credential, []byte("synthetic-secret"), 0600)
	identityKey := filepath.Join(root, "runner-identity-key")
	os.WriteFile(identityKey, []byte("synthetic-runner-identity"), 0600)
	state := filepath.Join(root, "state")
	os.Mkdir(state, 0700)
	os.WriteFile(filepath.Join(state, "secret"), []byte("runner-secret"), 0600)
	w := isolation.Workspace{Root: filepath.Join(root, "workspace"), PrivateHome: filepath.Join(root, "home"), TempDir: filepath.Join(root, "tmp"), RuntimeDir: filepath.Join(root, "runtime")}
	for _, p := range []string{w.Root, w.PrivateHome, w.TempDir, w.RuntimeDir} {
		os.Mkdir(p, 0700)
	}
	serve := func() (string, func()) {
		ln, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("canary-ok")) }), ReadHeaderTimeout: time.Second}
		go s.Serve(ln)
		return ln.Addr().String(), func() { s.Close() }
	}
	allowed, closeAllowed := serve()
	defer closeAllowed()
	denied, closeDenied := serve()
	defer closeDenied()
	_, port, _ := net.SplitHostPort(allowed)
	n, _ := strconv.Atoi(port)
	profile := isolation.MacOSSandboxExecDev{BoundaryPort: n, CredentialPath: credential, RunnerStateDir: state, SecretPaths: []string{identityKey}, RepositoryRoot: root}
	launcher, err := profile.Prepare(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	defer launcher.Cleanup()
	run := func(binary string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = w.Root
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + w.PrivateHome, "TMPDIR=" + w.TempDir}
		if e := launcher.Wrap(cmd); e != nil {
			return nil, e
		}
		return cmd.CombinedOutput()
	}
	for name, path := range map[string]string{"home-secret": secret.Name(), "credential": credential, "runner-identity": identityKey, "runner-state": filepath.Join(state, "secret")} {
		t.Run(name, func(t *testing.T) {
			if b, e := run("/bin/cat", path); e == nil || strings.Contains(string(b), "synthetic-") {
				t.Fatalf("canary readable: %q, %v", b, e)
			}
		})
	}
	if b, e := run("/usr/bin/touch", forbidden.Name()); e == nil {
		t.Fatalf("outside write permitted %q", b)
	}
	for _, parent := range []string{"/private/tmp", os.TempDir(), root} {
		file, e := os.CreateTemp(parent, "gaffer-outside-write-")
		if e != nil {
			t.Fatal(e)
		}
		file.Close()
		t.Cleanup(func() { os.Remove(file.Name()) })
		if b, e := run("/usr/bin/touch", file.Name()); e == nil {
			t.Fatalf("outside temporary/repository write allowed %s: %s", file.Name(), b)
		}
	}
	sleeper := exec.Command("/bin/sleep", "60")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sleeper.Process.Kill(); sleeper.Wait() })
	if b, e := run("/bin/kill", "-0", strconv.Itoa(sleeper.Process.Pid)); e == nil {
		t.Fatalf("outside process signal allowed: %s", b)
	}
	if b, e := run("/bin/sh", "-c", `sleep 60 & child=$!; kill -0 "$child" && kill -TERM "$child" || exit 1; wait "$child"; exit 0`); e != nil {
		t.Fatalf("same-sandbox signal denied %q: %v", b, e)
	}
	if b, e := run("/usr/bin/touch", filepath.Join(w.Root, "allowed")); e != nil {
		t.Fatalf("workspace write denied %q %v", b, e)
	}
	if b, e := run("/usr/bin/curl", "--silent", "--show-error", "--max-time", "1", "http://"+allowed); e != nil || string(b) != "canary-ok" {
		t.Fatalf("boundary denied %q %v", b, e)
	}
	if b, e := run("/usr/bin/curl", "--silent", "--show-error", "--max-time", "1", "http://"+denied); e == nil {
		t.Fatalf("second loopback permitted %q", b)
	}
	if profile.Qualification() != "development" {
		t.Fatal("incorrect qualification")
	}
	t.Log("real sandbox-exec denied HOME canary, credentials, runner state, outside signal, temporary/repository writes and second loopback; permitted workspace write and boundary port")
}
func TestSandboxEscapesQuotedPaths(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	w := isolation.Workspace{Root: filepath.Join(root, `work"space`), PrivateHome: filepath.Join(root, `home\private`), TempDir: filepath.Join(root, "tmp"), RuntimeDir: filepath.Join(root, "runtime")}
	for _, path := range []string{w.Root, w.PrivateHome, w.TempDir, w.RuntimeDir} {
		os.Mkdir(path, 0700)
	}
	launcher, err := (isolation.MacOSSandboxExecDev{}).Prepare(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	defer launcher.Cleanup()
	cmd := exec.Command("/usr/bin/true")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + w.PrivateHome}
	cmd.Dir = w.Root
	if err = launcher.Wrap(cmd); err != nil {
		t.Fatal(err)
	}
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("escaped profile invalid: %s %v", b, e)
	}
}
