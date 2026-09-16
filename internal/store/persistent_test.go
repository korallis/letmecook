package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	p "github.com/korallis/letmecook/schemas/execution"
	"modernc.org/sqlite"
)

func persistent(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	artifacts := filepath.Join(dir, "artifacts")
	s, err := Open(ctx, filepath.Join(dir, "state"), artifacts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, artifacts
}

func assignment(s *Store) p.Message {
	return p.Message{Version: p.FencedVersion, Kind: "assign", MessageID: newID(), Identity: p.Identity{Generation: s.meta.Generation, TaskID: newID(), AttemptID: newID(), Epoch: 1}, AssignmentID: newID(), InputDigest: strings.Repeat("a", 64), Route: &p.Route{RouteRef: "public-fixture", DecisionDigest: strings.Repeat("b", 64), PolicyDigest: strings.Repeat("c", 64), LimitsProfile: "strict-provider-output-v1"}}
}

// No public write endpoint exists: trusted store transactions are exercised directly
// with synthetic v2 inputs, without pretending they carry execution authority.
func TestPersistentTransactionsAndRecovery(t *testing.T) {
	s, artifacts := persistent(t)
	fresh := snapshot(t, s)
	if fresh.Mode != "store-only" || fresh.SchemaVersion != 3 || len(fresh.Tasks) != 0 || len(fresh.Events) != 0 {
		t.Fatal(fresh)
	}
	m := assignment(s)
	legacy := m
	legacy.Version = p.Version
	if err := s.assign(ctx, legacy); !errors.Is(err, p.UnknownVersion) {
		t.Fatal(err)
	}
	sqlExec(t, s, "CREATE TRIGGER interrupt BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'interrupted'); END")
	if err := s.assign(ctx, m); err == nil {
		t.Fatal("interrupted assign acknowledged")
	}
	if !reflect.DeepEqual(fresh, snapshot(t, s)) {
		t.Fatal("partial assignment")
	}
	sqlExec(t, s, "DROP TRIGGER interrupt")
	if err := s.assign(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := s.assign(ctx, m); !errors.Is(err, p.Duplicate) {
		t.Fatal(err)
	}
	for _, query := range []string{"UPDATE events SET revision=2", "DELETE FROM events", "UPDATE attempts SET task_id='missing'", "UPDATE attempts SET epoch=0"} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatal("constraint missing", query)
		}
	}
	transition := next(t, s, p.Starting)
	transition.Version = p.FencedVersion
	if err := s.transition(ctx, transition); !errors.Is(err, p.ReconciliationRequired) {
		t.Fatal("unproven execution accepted", err)
	}
	before := snapshot(t, s)
	if err := s.Dispose(); err == nil {
		t.Fatal("persistent state deleted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	after := snapshot(t, r)
	if after.Generation != before.Generation || after.DaemonBoot == before.DaemonBoot || after.Tasks[0].Attempt.State != p.Unknown || after.Tasks[0].Attempt.Revision != 2 || len(after.Events) != 2 || after.Tasks[0].Attempt.Observation.ConfirmedProcess != "unknown" {
		t.Fatal("restart did not quarantine atomically", after)
	}
	if !reflect.DeepEqual(before.Events[0], after.Events[0]) {
		t.Fatal("rewrote assignment")
	}
	if err := r.assign(ctx, m); !errors.Is(err, p.Duplicate) {
		t.Fatal("lost durable replay identity", err)
	}
	consistent(t, r)
}

func TestPersistentMigrationAndFixtureRefusal(t *testing.T) {
	s, artifacts := persistent(t)
	generation := s.meta.Generation
	// Build a real prior persistent schema, including its distinguishing application ID.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(s.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("DROP TABLE credentials; DROP TABLE enrollments; DROP TABLE principals; DROP TRIGGER events_no_update; DROP TRIGGER events_no_delete; ALTER TABLE metadata DROP COLUMN artifacts_dir; PRAGMA user_version=1")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	r, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if r.meta.SchemaVersion != 3 || r.meta.Generation != generation {
		t.Fatal("migration identity")
	}
	r.Close()
	fixture := owned(t)
	fixture.Close()
	if _, err := Open(ctx, fixture.dir, filepath.Join(t.TempDir(), "artifacts")); err == nil {
		t.Fatal("fixture imported")
	}
	if _, err := open(ctx, s.dir); err == nil {
		t.Fatal("persistent store reopened as fixture")
	}
}

func TestPersistentArtifactConfiguration(t *testing.T) {
	s, artifacts := persistent(t)
	if err := os.Chmod(filepath.Dir(s.dir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.assign(ctx, assignment(s)); err != nil {
		t.Fatal(err)
	}
	if err := s.transition(ctx, next(t, s, p.Unknown)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE metadata SET artifacts_dir=? WHERE singleton=1", artifacts); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"relocated":          filepath.Join(t.TempDir(), "different"),
		"artifacts-in-state": filepath.Join(s.dir, "nested"),
		"state-in-artifacts": filepath.Dir(s.dir),
		"same-directory":     s.dir,
	} {
		t.Run(name, func(t *testing.T) {
			r, err := Open(ctx, s.dir, dir)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			after := snapshot(t, r)
			if after.Generation != before.Generation || after.DaemonBoot == before.DaemonBoot || !reflect.DeepEqual(after.Tasks, before.Tasks) || !reflect.DeepEqual(after.Events, before.Events) {
				t.Fatal("artifact configuration changed durable state", after)
			}
		})
	}
}

func TestPersistentFailedRecoveryRollsBackBootAndState(t *testing.T) {
	s, artifacts := persistent(t)
	if err := s.assign(ctx, assignment(s)); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, s)
	sqlExec(t, s, "CREATE TRIGGER refuse_recovery BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'recovery interrupted'); END")
	s.Close()
	if r, err := Open(ctx, s.dir, artifacts); err == nil {
		r.Close()
		t.Fatal("partial recovery accepted")
	}
	db, err := sql.Open("sqlite", filepath.Join(s.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var boot, state string
	var revision, events int
	if err = db.QueryRow("SELECT daemon_boot FROM metadata").Scan(&boot); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT state,revision FROM attempts").Scan(&state, &revision); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if boot != before.DaemonBoot || state != "assigned" || revision != 1 || events != 1 {
		t.Fatal("failed recovery changed authoritative state")
	}
	if _, err = db.Exec("DROP TRIGGER refuse_recovery"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal("failed open retained ownership", err)
	}
	defer r.Close()
	if v := snapshot(t, r); len(v.Events) != 2 || v.Tasks[0].Attempt.State != p.Unknown {
		t.Fatal("recovery missing")
	}
}

func TestPersistentFailuresAndPaths(t *testing.T) {
	for _, kind := range []string{"relative", "symlink", "public", "hardlink", "future", "artifact-missing", "artifact-relative", "artifact-symlink", "artifact-public", "artifact-file"} {
		t.Run(kind, func(t *testing.T) {
			s, artifacts := persistent(t)
			s.Close()
			state := s.dir
			switch kind {
			case "relative":
				state = "relative"
			case "symlink":
				state = filepath.Join(filepath.Dir(state), "alias")
				if err := os.Symlink(s.dir, state); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Chmod(state, 0755); err != nil {
					t.Fatal(err)
				}
			case "artifact-missing":
				artifacts = ""
			case "artifact-relative":
				artifacts = "relative"
			case "artifact-symlink":
				alias := filepath.Join(filepath.Dir(artifacts), "alias")
				if err := os.Symlink(artifacts, alias); err != nil {
					t.Fatal(err)
				}
				artifacts = alias
			case "artifact-public":
				if err := os.Chmod(artifacts, 0755); err != nil {
					t.Fatal(err)
				}
			case "artifact-file":
				artifacts = filepath.Join(filepath.Dir(artifacts), "file")
				if err := os.WriteFile(artifacts, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(filepath.Join(state, "state.db"), filepath.Join(filepath.Dir(state), "alias.db")); err != nil {
					t.Fatal(err)
				}
			case "future":
				db, err := sql.Open("sqlite", filepath.Join(state, "state.db"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec("PRAGMA user_version=999"); err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			if r, err := Open(ctx, state, artifacts); err == nil {
				r.Close()
				t.Fatal("unsafe placement accepted")
			}
		})
	}
	s, _ := persistent(t)
	m := assignment(s)
	sqlExec(t, s, "CREATE TABLE pressure(payload BLOB) STRICT")
	var pages int
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA max_page_count=" + strconv.Itoa(pages)); err != nil {
		t.Fatal(err)
	}
	sqlExec(t, s, "CREATE TRIGGER full_write BEFORE INSERT ON events BEGIN INSERT INTO pressure VALUES(zeroblob(1048576)); END")
	err := s.assign(ctx, m)
	var full *sqlite.Error
	if !errors.As(err, &full) || full.Code() != 13 {
		t.Fatal("expected SQLITE_FULL", err)
	}
	if v := snapshot(t, s); len(v.Tasks) != 0 || len(v.Events) != 0 {
		t.Fatal("partial full write")
	}
	consistent(t, s)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.assign(cancelled, m); err == nil {
		t.Fatal("cancelled transaction accepted")
	}
}

func TestPersistentProcessCrashAndOwnership(t *testing.T) {
	for _, mode := range []string{"crash", "commit"} {
		t.Run(mode, func(t *testing.T) {
			s, artifacts := persistent(t)
			if err := s.assign(ctx, assignment(s)); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GAFFER_OWNED_TEST_ARTIFACTS", filepath.Join(t.TempDir(), "different"))
			child(t, s.dir, "compete", "locked", false)
			t.Setenv("GAFFER_OWNED_TEST_ARTIFACTS", artifacts)
			shared, err := Open(ctx, filepath.Join(t.TempDir(), "state"), artifacts)
			if err != nil {
				t.Fatal("shared artifact configuration rejected", err)
			}
			if err := shared.Close(); err != nil {
				t.Fatal(err)
			}
			s.Close()
			// Reopen before crash to commit startup uncertainty separately.
			r, err := Open(ctx, s.dir, artifacts)
			if err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, r)
			r.Close()
			want := "interrupted"
			if mode == "commit" {
				want = "committed"
			}
			child(t, s.dir, mode, want, true)
			r, err = Open(ctx, s.dir, artifacts)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			after := snapshot(t, r)
			if after.Generation != before.Generation || after.DaemonBoot == before.DaemonBoot {
				t.Fatal("restart identity")
			}
			if mode == "crash" {
				if !reflect.DeepEqual(before.Tasks, after.Tasks) || !reflect.DeepEqual(before.Events, after.Events) {
					t.Fatal("partial crashed transaction")
				}
			} else if len(after.Events) != len(before.Events)+2 || after.Tasks[0].Attempt.State != p.Unknown {
				t.Fatal("committed write lost or recovery missing")
			}
			consistent(t, r)
		})
	}
}
