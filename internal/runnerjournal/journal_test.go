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

func TestCreateFailureCleansUpAndRetries(t *testing.T) {
	for _, stage := range []string{"post-mkdir", "ownership", "lock", "partial-open", "invalid-initial", "file", "directory", "parent"} {
		t.Run(stage, func(t *testing.T) {
			path := pathFor(t)
			injected := errors.New("injected creation failure")
			fail := func() error { return injected }
			initial := []byte(`{"initial":true}`)
			if stage == "invalid-initial" {
				initial = []byte(`{"torn":`)
			}
			var retained *Journal
			prepare := func(j *Journal) error {
				retained = j
				entries, err := os.ReadDir(j.path)
				if err != nil || len(entries) != 0 {
					t.Fatalf("expected new empty directory: %v, %v", entries, err)
				}
				if stage == "post-mkdir" {
					return injected
				}
				if stage == "ownership" {
					if err := os.Chmod(j.path, 0755); err != nil {
						t.Fatal(err)
					}
				}
				if stage == "lock" {
					owner, err := openOwnedDirectory(j.path)
					if err != nil {
						t.Fatal(err)
					}
					defer owner.Close()
				}
				if err := j.open(true); err != nil {
					return err
				}
				switch stage {
				case "partial-open":
					return injected
				case "file":
					j.syncFile = fail
				case "directory":
					j.syncDir = fail
				}
				return nil
			}
			syncParent := func(parent *os.File) error {
				if stage == "parent" {
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("journal not published before parent sync: %v", err)
					}
					if other, err := Open(path); err == nil {
						other.Close()
						t.Fatal("publication released ownership lock")
					}
					return injected
				}
				return parent.Sync()
			}
			if j, err := create(path, initial, prepare, syncParent); err == nil || j != nil {
				t.Fatalf("creation acknowledged failure: %v, %v", j, err)
			} else if stage != "ownership" && stage != "lock" && stage != "invalid-initial" && !errors.Is(err, injected) {
				t.Fatalf("lost injected error: %v", err)
			}
			if retained == nil || retained.dir != nil || retained.root != nil || retained.file != nil {
				t.Fatal("failed creation retained open handles")
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(entries) != 0 {
				t.Fatalf("creation left directory or partial file behind: %v, %v", entries, err)
			}
			j, err := Create(path, []byte(`{"retry":true}`))
			if err != nil {
				t.Fatalf("retry failed: %v", err)
			}
			if err := j.Check(); err != nil {
				t.Fatal(err)
			}
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			j, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			if rows := j.Records(); len(rows) != 1 || string(rows[0]) != `{"retry":true}` {
				t.Fatalf("unexpected recovered retry: %s", rows)
			}
		})
	}
}

func TestCreatePreservesExistingDestination(t *testing.T) {
	for _, kind := range []string{"empty-directory", "nonempty-directory", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := pathFor(t)
			foreign := path
			switch kind {
			case "empty-directory", "nonempty-directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				foreign = filepath.Join(path, "journal.jsonl")
			case "symlink":
				foreign = filepath.Join(filepath.Dir(path), "foreign")
				if err := os.Symlink(foreign, path); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "empty-directory" {
				if err := os.WriteFile(foreign, []byte("foreign content"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if j, err := Create(path, []byte(`{}`)); err == nil || j != nil {
				t.Fatalf("creation replaced existing destination: %v, %v", j, err)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("existing destination changed: %v", err)
			}
			if kind != "empty-directory" {
				data, err := os.ReadFile(foreign)
				if err != nil || string(data) != "foreign content" {
					t.Fatalf("foreign content changed: %q, %v", data, err)
				}
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			want := 1
			if kind == "symlink" {
				want = 2
			}
			if err != nil || len(entries) != want {
				t.Fatalf("temporary state leaked: %v, %v", entries, err)
			}
		})
	}
}

func TestCreateRollbackPreservesForeignContent(t *testing.T) {
	for _, stage := range []string{"before-open", "added-file", "replaced-file", "replaced-directory"} {
		t.Run(stage, func(t *testing.T) {
			path := pathFor(t)
			injected := errors.New("injected failure with foreign content")
			var directory, foreign string
			prepare := func(j *Journal) error {
				directory = j.path
				foreign = filepath.Join(directory, "journal.jsonl")
				if stage == "before-open" {
					if err := os.WriteFile(foreign, []byte("foreign content"), 0600); err != nil {
						t.Fatal(err)
					}
					return j.open(true)
				}
				if err := j.open(true); err != nil {
					return err
				}
				switch stage {
				case "added-file":
					foreign = filepath.Join(directory, "foreign")
				case "replaced-file":
					if err := os.Remove(foreign); err != nil {
						t.Fatal(err)
					}
				case "replaced-directory":
					if err := os.Rename(directory, directory+"-moved"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(directory, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(foreign, []byte("foreign content"), 0600); err != nil {
					t.Fatal(err)
				}
				return injected
			}
			if j, err := create(path, []byte(`{}`), prepare, (*os.File).Sync); err == nil || j != nil {
				t.Fatalf("creation acknowledged failure: %v, %v", j, err)
			}
			data, err := os.ReadFile(foreign)
			if err != nil || string(data) != "foreign content" {
				t.Fatalf("rollback removed foreign content: %q, %v", data, err)
			}
			if stage == "added-file" {
				if _, err := os.Lstat(filepath.Join(directory, "journal.jsonl")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("rollback left its own partial file: %v", err)
				}
			}
			j, err := Create(path, []byte(`{}`))
			if err != nil {
				t.Fatalf("foreign staging content blocked target retry: %v", err)
			}
			j.Close()
		})
	}
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
