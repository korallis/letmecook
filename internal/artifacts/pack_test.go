package artifacts

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.test")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}
func TestPackDerivesCompleteCandidateAndRefusesLFS(t *testing.T) {
	root := t.TempDir()
	run(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(t, root, "add", "tracked.txt")
	run(t, root, "commit", "-qm", "base")
	base := run(t, root, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.bin"), []byte{0, 1}, 0600); err != nil {
		t.Fatal(err)
	}
	pkg, err := Pack(context.Background(), Request{Root: root, Base: string(base[:40]), RecoveryDir: filepath.Join(t.TempDir(), "recovery"), Identity: p.Identity{Generation: "00000000-0000-4000-8000-000000000001", TaskID: "00000000-0000-4000-8000-000000000002", AttemptID: "00000000-0000-4000-8000-000000000003", Epoch: 1}, Outcome: "succeeded"})
	if err != nil {
		t.Fatal(err)
	}
	var m store.CandidateManifest
	if err := json.Unmarshal(pkg.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Tracked) != 1 || len(m.Binary) != 1 || len(pkg.Sources) != 2 {
		t.Fatal(m, pkg)
	}
	if err := os.WriteFile(filepath.Join(root, "lfs.txt"), []byte("version https://git-lfs.github.com/spec/v1\noid sha256:x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Pack(context.Background(), Request{Root: root, Base: string(base[:40]), RecoveryDir: filepath.Join(t.TempDir(), "r"), Identity: m.Identity, Outcome: "succeeded"}); err == nil {
		t.Fatal("LFS pointer accepted")
	}
}
