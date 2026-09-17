package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/isolation"
)

// TestSandboxSymlinkCannotWriteOutside probes escapes the canary test does
// not cover: writing through a symlink that lives inside the workspace but
// points outside it, and reading host files the profile never denies.
func TestSandboxSymlinkCannotWriteOutside(t *testing.T) {
	home, _ := os.UserHomeDir()
	root, err := os.MkdirTemp(home, ".gaffer-s2review2-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	w := isolation.Workspace{Root: filepath.Join(root, "workspace"), PrivateHome: filepath.Join(root, "home"), TempDir: filepath.Join(root, "tmp"), RuntimeDir: filepath.Join(root, "runtime")}
	for _, p := range []string{w.Root, w.PrivateHome, w.TempDir, w.RuntimeDir} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	// A symlink created inside the workspace pointing out of it.
	link := filepath.Join(w.Root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	profile := isolation.MacOSSandboxExecDev{CredentialPath: filepath.Join(root, "credential"), RunnerStateDir: filepath.Join(root, "state"), RepositoryRoot: root}
	if err := os.WriteFile(profile.CredentialPath, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(profile.RunnerStateDir, 0700); err != nil {
		t.Fatal(err)
	}
	launcher, err := profile.Prepare(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	defer launcher.Cleanup()
	run := func(binary string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = w.Root
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + w.PrivateHome, "TMPDIR=" + w.TempDir}
		if e := launcher.Wrap(cmd); e != nil {
			return nil, e
		}
		return cmd.CombinedOutput()
	}
	// 1. Write through the in-workspace symlink to a path outside the workspace.
	b, e := run("/bin/sh", "-c", `printf tampered > escape/victim.txt && echo wrote`)
	after, _ := os.ReadFile(target)
	t.Logf("symlink write: out=%q err=%v victim=%q", strings.TrimSpace(string(b)), e, after)
	if string(after) != "original" {
		t.Errorf("SANDBOX ESCAPE: wrote outside the workspace through an in-workspace symlink; victim now %q", after)
	}
	// 2. Create a symlink then write to a brand new file outside.
	b, e = run("/bin/sh", "-c", `ln -sfn `+outside+` link2 && printf x > link2/created.txt && echo wrote`)
	_, statErr := os.Stat(filepath.Join(outside, "created.txt"))
	t.Logf("fresh symlink write: out=%q err=%v created=%v", strings.TrimSpace(string(b)), e, statErr == nil)
	if statErr == nil {
		t.Error("SANDBOX ESCAPE: created a file outside the workspace through a job-created symlink")
	}
	// Readable nonsecret host files are a documented compatibility limitation.
	b, e = run("/bin/ls", "-d", "/etc/passwd")
	if e != nil {
		t.Fatalf("compatibility read unexpectedly denied: %q %v", b, e)
	}
}
