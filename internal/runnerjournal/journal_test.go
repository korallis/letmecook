package runnerjournal

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func pathFor(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return filepath.Join(p, "journal")
}
func TestDurabilityFailuresNoPositiveReturn(t *testing.T) {
	for _, stage := range []string{"file", "directory"} {
		t.Run(stage, func(t *testing.T) {
			path := pathFor(t)
			j, e := Create(path, []byte(`{"initial":true}`))
			if e != nil {
				t.Fatal(e)
			}
			fail := func() error { return errors.New("injected sync failure") }
			if stage == "file" {
				j.syncFile = fail
			} else {
				j.syncDir = fail
			}
			if e = j.Append([]byte(`{"accept":true}`)); e == nil {
				t.Fatal("acknowledged unsynced record")
			}
			if e = j.Append([]byte(`{"retry":true}`)); e == nil {
				t.Fatal("failed handle reused")
			}
			j.Close()
			reopened, e := Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer reopened.Close()
			// Either persisted write is recovery input, never a successful prior ack.
			if len(reopened.Records()) != 2 {
				t.Fatal("unknown outcome was silently discarded")
			}
		})
	}
}
func TestOwnershipAndUnsafePaths(t *testing.T) {
	path := pathFor(t)
	j, e := Create(path, []byte(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	if other, e := Open(path); e == nil {
		other.Close()
		t.Fatal("concurrent owner")
	}
	if e = os.Chmod(path, 0755); e != nil {
		t.Fatal(e)
	}
	if e = j.Check(); e == nil {
		t.Fatal("permissions drift accepted")
	}
	os.Chmod(path, 0700)
	if e = j.Append([]byte(`{}`)); e == nil {
		t.Fatal("poisoned owner reused")
	}
	j.Close()
	alias := path + "-alias"
	if e = os.Symlink(path, alias); e != nil {
		t.Fatal(e)
	}
	if other, e := Open(alias); e == nil {
		other.Close()
		t.Fatal("symlink directory")
	}
	os.Rename(filepath.Join(path, "journal.jsonl"), filepath.Join(path, "old"))
	os.Symlink("old", filepath.Join(path, "journal.jsonl"))
	if other, e := Open(path); e == nil {
		other.Close()
		t.Fatal("symlink file")
	}
}
func TestReplacementAndTornWrite(t *testing.T) {
	path := pathFor(t)
	j, e := Create(path, []byte(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	old := path + "-old"
	if e = os.Rename(path, old); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e = j.Append([]byte(`{}`)); e == nil {
		t.Fatal("replaced directory acknowledged")
	}
	j.Close()
	f, e := os.OpenFile(filepath.Join(old, "journal.jsonl"), os.O_WRONLY|os.O_APPEND, 0)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteString(`{"torn":`)
	f.Close()
	if other, e := Open(old); e == nil {
		other.Close()
		t.Fatal("torn record accepted")
	}
}
func TestChildCrashHelper(t *testing.T) {
	path := os.Getenv("GAFFER_RUNNER_JOURNAL_CRASH_PATH")
	if path == "" {
		return
	}
	j, e := Create(path, []byte(`{"before_ack":true}`))
	if e != nil {
		os.Exit(3)
	}
	if e = j.Append([]byte(`{"accept":true}`)); e != nil {
		os.Exit(4)
	}
	// SIGKILL without Close: OS ownership is released, retained history survives.
	self, _ := os.FindProcess(os.Getpid())
	if self.Kill() != nil {
		os.Exit(99)
	}
	for {
		time.Sleep(time.Second)
	}
}
func TestProcessLossReopenAndBound(t *testing.T) {
	path := pathFor(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildCrashHelper$")
	cmd.Env = append(os.Environ(), "GAFFER_RUNNER_JOURNAL_CRASH_PATH="+path)
	e := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != -1 {
		t.Fatal(e)
	}
	j, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	if len(j.Records()) != 2 {
		t.Fatal("acknowledged acceptance lost")
	}
	if e = j.Append(make([]byte, MaxRecord)); e == nil {
		t.Fatal("unbounded record")
	}
}
