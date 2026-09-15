package store

import (
	"bufio"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	p "github.com/korallis/letmecook/schemas/execution"
	a "github.com/korallis/letmecook/schemas/readapi"
	"github.com/korallis/letmecook/tests/fixtures/protocol/data"
	"modernc.org/sqlite"
)

var ctx = context.Background()

func owned(t *testing.T) *Store {
	t.Helper()
	s, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Dispose(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func snapshot(t *testing.T, s *Store) a.Snapshot {
	t.Helper()
	v, err := s.Snapshot(ctx, "", a.MaxItems)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func next(t *testing.T, s *Store, to p.AttemptState) p.Message {
	t.Helper()
	task := snapshot(t, s).Tasks[0]
	revision := task.Attempt.Revision
	return p.Message{Version: p.Version, MessageID: newID(), Kind: "transition", Identity: task.Attempt.Identity, ExpectedRevision: &revision, From: task.Attempt.State, To: to}
}
func sqlExec(t *testing.T, s *Store, query string) {
	t.Helper()
	if _, err := s.db.Exec(query); err != nil {
		t.Fatal(err)
	}
}
func consistent(t *testing.T, s *Store) {
	t.Helper()
	var result string
	if err := s.db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		t.Fatalf("integrity: %s %v", result, err)
	}
	rows, err := s.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("foreign key violation")
	}
}
func TestFreshReopenAndSettings(t *testing.T) {
	s := owned(t)
	before := snapshot(t, s)
	if len(before.Tasks) != 1 || len(before.Events) != 2 || before.Tasks[0].Attempt.State != p.Unknown {
		t.Fatalf("bad seed: %+v", before)
	}
	if before.Tasks[0].State == p.TaskAccepted {
		t.Fatal("fixture granted acceptance")
	}
	var sqliteVersion string
	if err := s.db.QueryRow("SELECT sqlite_version()").Scan(&sqliteVersion); err != nil {
		t.Fatal(err)
	}
	t.Log("SQLite", sqliteVersion)
	// Force database/sql to recreate its connection: each new connection needs all pragmas.
	s.db.SetMaxIdleConns(0)
	for name, want := range map[string]int{"foreign_keys": 1, "synchronous": 2, "fullfsync": 1, "checkpoint_fullfsync": 1, "trusted_schema": 0, "user_version": 1} {
		var got int
		if err := s.db.QueryRow("PRAGMA " + name).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: %d %v", name, got, err)
		}
	}
	s.db.SetMaxIdleConns(1)
	consistent(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := open(ctx, s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after := snapshot(t, reopened)
	if after.Generation != before.Generation || after.DaemonBoot == before.DaemonBoot || !reflect.DeepEqual(after.Tasks, before.Tasks) || !reflect.DeepEqual(after.Events, before.Events) {
		t.Fatal("reopen changed committed identity/state")
	}
	other := owned(t)
	if other.meta.Generation == before.Generation || snapshot(t, other).Tasks[0].TaskID == before.Tasks[0].TaskID {
		t.Fatal("fresh store reused identity")
	}
}

func TestTransitionsAndAtomicFailure(t *testing.T) {
	s := owned(t)
	before := snapshot(t, s)
	valid := next(t, s, p.Stopping)
	cases := map[string]struct {
		change func(*p.Message)
		want   error
	}{
		"invalid edge":     {func(m *p.Message) { m.To = p.Succeeded }, p.InvalidTransition},
		"stale generation": {func(m *p.Message) { m.Identity.Generation = newID() }, p.StaleGeneration},
		"stale task":       {func(m *p.Message) { m.Identity.TaskID = newID() }, p.StaleAttempt},
		"stale attempt":    {func(m *p.Message) { m.Identity.AttemptID = newID() }, p.StaleAttempt},
		"stale epoch":      {func(m *p.Message) { m.Identity.Epoch++ }, p.StaleAttempt},
		"stale revision":   {func(m *p.Message) { r := int64(1); m.ExpectedRevision = &r }, p.RevisionConflict},
		"malformed":        {func(m *p.Message) { m.Identity.TaskID = "../../state.db" }, p.Malformed},
		"unknown version":  {func(m *p.Message) { m.Version = "next" }, p.UnknownVersion},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := valid
			c.change(&m)
			if err := s.transition(ctx, m); !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
			if !reflect.DeepEqual(before, snapshot(t, s)) {
				t.Fatal("refusal wrote state")
			}
		})
	}
	// Trigger runs after attempt/task writes but before the event insert.
	sqlExec(t, s, "CREATE TRIGGER refuse_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'test interrupted write'); END")
	if err := s.transition(ctx, valid); err == nil {
		t.Fatal("acknowledged interrupted write")
	}
	if !reflect.DeepEqual(before, snapshot(t, s)) {
		t.Fatal("partial transaction")
	}
	sqlExec(t, s, "DROP TRIGGER refuse_event")
	if err := s.transition(ctx, valid); err != nil {
		t.Fatal(err)
	}
	committed := snapshot(t, s)
	if committed.Tasks[0].Attempt.Revision != 3 || len(committed.Events) != 3 {
		t.Fatal("missing atomic revision/event")
	}
	if err := s.transition(ctx, valid); !errors.Is(err, p.Duplicate) {
		t.Fatal(err)
	}
	conflict := valid
	conflict.To = p.Expired
	if err := s.transition(ctx, conflict); !errors.Is(err, p.IdentityConflict) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(committed, snapshot(t, s)) {
		t.Fatal("replay changed state")
	}
	cancel := next(t, s, p.Cancelled)
	if err := s.transition(ctx, cancel); err != nil {
		t.Fatal(err)
	}
	if err := s.transition(ctx, next(t, s, p.Running)); !errors.Is(err, p.InvalidTransition) {
		t.Fatal(err)
	}
	if snapshot(t, s).Tasks[0].State == p.TaskAccepted {
		t.Fatal("terminal fixture accepted task")
	}
	consistent(t, s)
}

func TestConcurrentCASAndNoAcceptance(t *testing.T) {
	s := owned(t)
	first := next(t, s, p.ResultPending)
	second := first
	second.MessageID = newID()
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, m := range []p.Message{first, second} {
		wg.Go(func() { outcomes <- s.transition(ctx, m) })
	}
	wg.Wait()
	close(outcomes)
	committed, refused := 0, 0
	for err := range outcomes {
		if err == nil {
			committed++
		} else if errors.Is(err, p.RevisionConflict) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	if committed != 1 || refused != 1 {
		t.Fatal("CAS admitted competing revisions")
	}
	if err := s.transition(ctx, next(t, s, p.Succeeded)); err != nil {
		t.Fatal(err)
	}
	v := snapshot(t, s)
	if v.Tasks[0].State != p.TaskAwaitingReview || len(v.Events) != 4 {
		t.Fatal("success bypassed review")
	}
	for _, e := range v.Events {
		if e.Message.Kind == "result_ack" {
			t.Fatal("invented custody acknowledgement")
		}
	}
	// Safe-integer exhaustion refuses rather than wrapping authoritative revision.
	sqlExec(t, s, "UPDATE attempts SET revision=9007199254740991")
	if err := s.transition(ctx, next(t, s, p.Unknown)); !errors.Is(err, p.RevisionConflict) {
		t.Fatal(err)
	}
}

func TestAssignmentUniquenessAndRollback(t *testing.T) {
	s := owned(t)
	before := snapshot(t, s)
	m := before.Events[0].Message
	if err := s.assign(ctx, m); !errors.Is(err, p.Duplicate) {
		t.Fatal(err)
	}
	m.MessageID = before.Events[1].Message.MessageID
	if err := s.assign(ctx, m); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("assignment replay hid message conflict", err)
	}
	m.MessageID = newID()
	if err := s.assign(ctx, m); !errors.Is(err, p.Duplicate) {
		t.Fatal(err)
	}
	m.InputDigest = strings.Repeat("b", 64)
	if err := s.assign(ctx, m); !errors.Is(err, p.IdentityConflict) {
		t.Fatal(err)
	}
	m = before.Events[0].Message
	m.Identity.TaskID = newID()
	m.MessageID = newID()
	m.AssignmentID = newID()
	if err := s.assign(ctx, m); err == nil {
		t.Fatal("reused attempt")
	}
	if !reflect.DeepEqual(before, snapshot(t, s)) {
		t.Fatal("orphan task")
	}
	// Direct real SQLite constraints remain active independent of Go checks.
	for _, query := range []string{
		"INSERT INTO tasks SELECT * FROM tasks",
		"UPDATE attempts SET epoch=0",
		"UPDATE attempts SET revision=0",
		"UPDATE attempts SET task_id='missing'",
		"UPDATE events SET attempt_id='missing'",
	} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatalf("constraint missing: %s", query)
		}
	}
	consistent(t, s)
}

func TestDiskFullRollsBackAuthoritativeWrites(t *testing.T) {
	s := owned(t)
	sqlExec(t, s, "CREATE TABLE pressure(payload BLOB) STRICT")
	var pages int
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	sqlExec(t, s, fmt.Sprintf("PRAGMA max_page_count=%d", pages))
	sqlExec(t, s, "CREATE TRIGGER fill_disk BEFORE INSERT ON events BEGIN INSERT INTO pressure VALUES(zeroblob(1048576)); END")
	before := snapshot(t, s)
	err := s.transition(ctx, next(t, s, p.Stopping))
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != 13 {
		t.Fatalf("expected real SQLITE_FULL (13): %v", err)
	}
	if !reflect.DeepEqual(before, snapshot(t, s)) {
		t.Fatal("disk-full left partial authoritative records")
	}
	consistent(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := open(ctx, s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after := snapshot(t, reopened)
	if !reflect.DeepEqual(before.Tasks, after.Tasks) || !reflect.DeepEqual(before.Events, after.Events) {
		t.Fatal("disk-full lost committed state")
	}
	// Startup boot update must also fail closed on a real SQLite capacity error.
	sqlExec(t, reopened, fmt.Sprintf("PRAGMA max_page_count=%d", pages))
	sqlExec(t, reopened, "CREATE TRIGGER full_startup BEFORE UPDATE ON metadata BEGIN INSERT INTO pressure VALUES(zeroblob(1048576)); END")
	if err := reopened.migrate(ctx); !errors.As(err, &sqliteErr) || sqliteErr.Code() != 13 {
		t.Fatalf("startup capacity error: %v", err)
	}
	var boot string
	if err := reopened.db.QueryRow("SELECT daemon_boot FROM metadata").Scan(&boot); err != nil || boot != after.DaemonBoot {
		t.Fatal("failed startup committed boot", err)
	}
}

func TestFailedStartupAndMigration(t *testing.T) {
	// Version zero means genuinely empty, not an import of unrecognized tables.
	for _, query := range []string{"PRAGMA user_version=99", "DROP TABLE metadata", "PRAGMA user_version=0"} {
		t.Run(query, func(t *testing.T) {
			s := owned(t)
			sqlExec(t, s, query)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if other, err := open(ctx, s.dir); err == nil {
				other.Close()
				t.Fatal("unsafe startup accepted")
			}
			lock, err := lockDirectory(s.dir)
			if err != nil {
				t.Fatal("failed startup leaked lock", err)
			}
			lock.Close()
		})
	}
	s := owned(t)
	dir := s.dir
	if err := s.Dispose(); err != nil {
		t.Fatal(err)
	}
	if _, err := open(ctx, dir); err == nil {
		t.Fatal("missing store opened")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "absent"), filepath.Join(dir, "state.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := open(ctx, dir); err == nil {
		t.Fatal("symlink followed")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := open(ctx, dir); err == nil {
		t.Fatal("public directory accepted")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
}

// Child control exists only in this test binary, never in cmd/gafferd.
func TestOwnedStoreProcess(t *testing.T) {
	mode := os.Getenv("GAFFER_OWNED_TEST_CHILD")
	if mode == "" {
		return
	}
	s, err := open(ctx, os.Getenv("GAFFER_OWNED_TEST_DIR"))
	if mode == "compete" {
		if err == nil {
			s.Close()
			os.Exit(2)
		}
		fmt.Println("locked")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if mode == "crash" {
		err = sqlite.RegisterScalarFunction("owned_test_pause", 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
			fmt.Println("interrupted")
			bufio.NewReader(os.Stdin).ReadByte()
			return int64(1), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		// Registration applies to new connections. Test-only trigger deliberately
		// needs trusted_schema; production connections keep it disabled.
		s.db.SetMaxIdleConns(0)
		if err := s.db.Ping(); err != nil {
			t.Fatal(err)
		}
		s.db.SetMaxIdleConns(1)
		sqlExec(t, s, "PRAGMA trusted_schema=ON")
		sqlExec(t, s, "CREATE TRIGGER pause_write BEFORE INSERT ON events BEGIN SELECT owned_test_pause(); END")
	}
	if err := s.transition(ctx, next(t, s, p.Stopping)); err != nil {
		t.Fatal(err)
	}
	fmt.Println("committed")
	bufio.NewReader(os.Stdin).ReadByte()
}

func child(t *testing.T, dir, mode, want string, kill bool) {
	t.Helper()
	deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(deadline, os.Args[0], "-test.run=^TestOwnedStoreProcess$")
	cmd.Env = append(os.Environ(), "GAFFER_OWNED_TEST_CHILD="+mode, "GAFFER_OWNED_TEST_DIR="+dir)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
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
	if !scanner.Scan() || scanner.Text() != want {
		lines := []string{scanner.Text()}
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		t.Fatalf("child %s: got %q (%v)", mode, lines, scanner.Err())
	}
	if kill {
		if err = cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err = cmd.Wait(); err == nil {
			t.Fatal("crash child exited successfully")
		}
	} else if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestProcessCrashAndOwnership(t *testing.T) {
	for _, mode := range []string{"crash", "commit"} {
		t.Run(mode, func(t *testing.T) {
			s := owned(t)
			before := snapshot(t, s)
			child(t, s.dir, "compete", "locked", false)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			want := "interrupted"
			if mode == "commit" {
				want = "committed"
			}
			child(t, s.dir, mode, want, true)
			reopened, err := open(ctx, s.dir)
			if err != nil {
				t.Fatal("crash retained OS ownership", err)
			}
			defer reopened.Close()
			after := snapshot(t, reopened)
			if after.Generation != before.Generation || after.DaemonBoot == before.DaemonBoot {
				t.Fatal("recovery identity")
			}
			if mode == "crash" {
				if !reflect.DeepEqual(before.Tasks, after.Tasks) || !reflect.DeepEqual(before.Events, after.Events) {
					t.Fatal("partial interrupted write")
				}
			} else if after.Tasks[0].Attempt.State != p.Stopping || len(after.Events) != 3 {
				t.Fatal("lost committed write")
			}
			consistent(t, reopened)
		})
	}
}

func TestProtocolFixtureReuse(t *testing.T) {
	var corpus struct {
		Messages map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(data.Cases, &corpus); err != nil {
		t.Fatal(err)
	}
	s := owned(t)
	for _, name := range []string{"result", "ack", "request", "reply"} {
		m, err := p.Decode(corpus.Messages[name])
		if err != nil {
			t.Fatal(err)
		}
		m.Identity = snapshot(t, s).Tasks[0].Attempt.Identity
		if err = s.transition(ctx, m); !errors.Is(err, p.Malformed) {
			t.Fatal("unsupported effect stored", name, err)
		}
	}
}
