//go:build darwin

package verification

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/isolation"
)

func TestDevelopmentGoCheckUsesPrivateRuntimeEnvironment(t *testing.T) {
	parent, err := os.MkdirTemp("/var/tmp", "gaffer-go-verification-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(parent)
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	state, root := filepath.Join(parent, "state"), filepath.Join(parent, "candidate")
	for _, path := range []string{state, root} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(parent, "synthetic-secret")
	if err := os.WriteFile(secret, []byte("not a real credential"), 0600); err != nil {
		t.Fatal(err)
	}
	profile, err := isolation.DevelopmentProfile(isolation.SandboxConfig{SandboxExec: "/usr/bin/sandbox-exec", RunnerStateDir: state, SecretPaths: []string{secret}})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewDevelopmentProfile(profile, time.Minute, state)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"go.mod": "module example.test/privateverify\n\ngo 1.23\n",
		"private_test.go": `package privateverify
import ("os"; "testing"; "strings")
func TestPrivateRuntime(t *testing.T) {
 for _, key := range []string{"HOME", "TMPDIR", "GOCACHE", "GOMODCACHE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
  path := os.Getenv(key)
  if path == "" || strings.HasPrefix(path, "/private/tmp/") || strings.HasPrefix(path, "/tmp/") || strings.HasPrefix(path, "/private/var/folders/") { t.Fatalf("unsafe %s=%q", key, path) }
  file, err := os.CreateTemp(path, "write-proof-"); if err != nil { t.Fatal(key, err) }; file.Close(); os.Remove(file.Name())
 }
 if _, err := os.ReadFile(os.Getenv("SECRET_CANARY")); err == nil { t.Fatal("secret path was readable") }
 if err := os.WriteFile(os.Getenv("OUTSIDE_CANARY"), []byte("escape"), 0600); err == nil { t.Fatal("write escaped workspace") }
}
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(state, "must-not-exist")
	check := Check{Name: "go-test", Argv: []string{goBin, "test", "./..."}, CWD: ".", Timeout: time.Minute, Env: map[string]string{"PATH": filepath.Dir(goBin) + ":/usr/bin:/bin", "SECRET_CANARY": secret, "OUTSIDE_CANARY": outside, "GOCACHE": "/tmp/untrusted-cache"}}
	out := verifier.run(context.Background(), root, check)
	if out.failure != "" || out.exit == nil || *out.exit != 0 || out.environment.ConfinementDrift || out.refusal != nil {
		t.Fatalf("Go verification refused: failure=%s exit=%v stderr=%s", out.failure, out.exit, out.stderr.Prefix)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatal("canary escaped", err)
	}
	if check.Env["GOCACHE"] != "/tmp/untrusted-cache" || check.Env["HOME"] != "" {
		t.Fatal("trusted check environment was mutated")
	}
}
