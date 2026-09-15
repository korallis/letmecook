// Package store owns local SQLite workflow metadata, never execution authority.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	p "github.com/korallis/letmecook/schemas/execution"
	a "github.com/korallis/letmecook/schemas/readapi"
	"github.com/korallis/letmecook/tests/fixtures/protocol/data"
	_ "modernc.org/sqlite"
)

type Store struct {
	mu      sync.Mutex
	db      *sql.DB
	lock    *os.File
	dir     string
	meta    a.Metadata
	fixture bool
}

func newID() string {
	b := [16]byte{}
	// crypto/rand.Read either fills b or terminates; no weak fallback.
	rand.Read(b[:])
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// New creates fresh private state under a fixed OS temp parent. It accepts no
// paths or configuration, ignores TMPDIR/HOME, and never reopens previous runs.
func New(ctx context.Context) (*Store, error) {
	dir, err := os.MkdirTemp("/tmp", "gaffer-fixture-")
	if err != nil {
		return nil, err
	}
	s, err := open(ctx, dir)
	if err == nil {
		err = s.seed(ctx)
	}
	if err == nil {
		err = s.lock.Sync()
	}
	if err == nil {
		var parent *os.File
		parent, err = os.Open("/tmp")
		if err == nil {
			err = errors.Join(parent.Sync(), parent.Close())
		}
	}
	if err != nil {
		if s != nil {
			err = errors.Join(err, s.Close())
		}
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	return s, nil
}

// open retains historical fixture stores only for tests and explicit --fixture mode.
func open(ctx context.Context, dir string) (*Store, error) {
	return openStore(ctx, dir, true)
}

// Open creates or reopens persistent, non-executing metadata. Paths are explicit;
// no fixture import, restore, runner, grant or artifact custody is provided.
func Open(ctx context.Context, dir, artifactsDir string) (*Store, error) {
	var err error
	if dir, err = prepareDirectory(dir); err != nil {
		return nil, err
	}
	if artifactsDir, err = prepareDirectory(artifactsDir); err != nil {
		return nil, err
	}
	artifacts, err := openDirectory(artifactsDir)
	if err != nil {
		return nil, err
	}
	if err = errors.Join(artifacts.Sync(), artifacts.Close()); err != nil {
		return nil, err
	}
	return openStore(ctx, dir, false)
}

func openStore(ctx context.Context, dir string, fixture bool) (_ *Store, err error) {
	lock, err := lockDirectory(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{lock: lock, dir: dir, fixture: fixture}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	for _, name := range []string{"state.db", "state.db-wal", "state.db-shm", "state.db-journal"} {
		st, e := os.Lstat(filepath.Join(dir, name))
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		if e == nil && (!st.Mode().IsRegular() || st.Mode().Perm() != 0600 || !privateFile(st)) {
			return nil, fmt.Errorf("store files must be private, owned, regular and singly linked")
		}
	}
	path := filepath.Join(dir, "state.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		err = f.Close()
	} else if os.IsExist(err) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {"rw"}, "_txlock": {"immediate"}, "_pragma": {
		"foreign_keys(1)", "journal_mode(WAL)", "synchronous(FULL)", "fullfsync(1)", "checkpoint_fullfsync(1)", "busy_timeout(1000)", "trusted_schema(0)",
	}}
	u.RawQuery = q.Encode()
	s.db, err = sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	s.db.SetMaxOpenConns(1)
	s.db.SetMaxIdleConns(1)
	var mode string
	if err = s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return nil, err
	}
	if mode != "wal" {
		return nil, fmt.Errorf("WAL unavailable")
	}
	for pragma, want := range map[string]int{"foreign_keys": 1, "synchronous": 2, "fullfsync": 1, "checkpoint_fullfsync": 1, "trusted_schema": 0} {
		var got int
		if err = s.db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
			return nil, err
		}
		if got != want {
			return nil, fmt.Errorf("unsafe SQLite setting: %s", pragma)
		}
	}
	if err = s.migrate(ctx); err != nil {
		return nil, err
	}
	if err = s.lock.Sync(); err != nil {
		return nil, err
	}
	return s, nil
}

const schema = `
CREATE TABLE metadata (singleton INTEGER PRIMARY KEY CHECK(singleton=1), generation TEXT NOT NULL UNIQUE, daemon_boot TEXT NOT NULL UNIQUE) STRICT;
CREATE TABLE tasks (id TEXT PRIMARY KEY, state TEXT NOT NULL CHECK(state IN ('ready','reconciling','verifying','awaiting_review'))) STRICT;
-- ponytail: one attempt per task; replacement/epoch admission belongs to #15 after authority/reconciliation.
CREATE TABLE attempts (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL UNIQUE REFERENCES tasks(id),
 epoch INTEGER NOT NULL CHECK(epoch BETWEEN 1 AND 9007199254740991),
 state TEXT NOT NULL CHECK(state IN ('assigned','starting','running','result_pending','stopping','unknown','succeeded','failed','cancelled','expired')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 assignment_id TEXT NOT NULL UNIQUE,
 UNIQUE(task_id,epoch), UNIQUE(id,task_id,epoch)
) STRICT;
CREATE TABLE events (
 sequence INTEGER PRIMARY KEY CHECK(sequence BETWEEN 1 AND 9007199254740991),
 message_id TEXT NOT NULL UNIQUE,
 attempt_id TEXT NOT NULL, task_id TEXT NOT NULL, epoch INTEGER NOT NULL,
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 message TEXT NOT NULL CHECK(length(message)<=8192),
 UNIQUE(attempt_id,revision),
 FOREIGN KEY(attempt_id,task_id,epoch) REFERENCES attempts(id,task_id,epoch)
) STRICT;
PRAGMA user_version=1;
`

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version, application int
	if err = tx.QueryRowContext(ctx, "PRAGMA application_id").Scan(&application); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	const applicationID = 0x47414646 // GAFF: separates persistent state from #94 fixtures.
	if (s.fixture && application != 0) || (!s.fixture && (application != applicationID && (application != 0 || version != 0))) {
		return fmt.Errorf("incompatible store identity; fixture import is not supported")
	}
	if version == 0 {
		var tables int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return err
		}
		if tables != 0 {
			return fmt.Errorf("unrecognized database")
		}
		if _, err = tx.ExecContext(ctx, schema); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO metadata VALUES (1,?,?)", newID(), newID()); err != nil {
			return err
		}
		if !s.fixture {
			if _, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id=%d", applicationID)); err != nil {
				return err
			}
		}
		version = 1
	} else if version != 1 && (s.fixture || version != 2 && version != 3) {
		return fmt.Errorf("unsupported schema version")
	}
	mode := "fixture-only"
	if !s.fixture {
		mode = "store-only"
		if version == 1 {
			if _, err = tx.ExecContext(ctx, `ALTER TABLE metadata ADD COLUMN artifacts_dir TEXT NOT NULL DEFAULT '';
CREATE TRIGGER events_no_update BEFORE UPDATE ON events BEGIN SELECT RAISE(ABORT,'events are append-only'); END;
CREATE TRIGGER events_no_delete BEFORE DELETE ON events BEGIN SELECT RAISE(ABORT,'events are append-only'); END;
PRAGMA user_version=2;`); err != nil {
				return err
			}
			version = 2
		}
		if version == 2 {
			if _, err = tx.ExecContext(ctx, identitySchema); err != nil {
				return err
			}
			version = 3
		}
	}
	boot := newID()
	if _, err = tx.ExecContext(ctx, "UPDATE metadata SET daemon_boot=? WHERE singleton=1", boot); err != nil {
		return err
	}
	meta := a.Metadata{Version: a.Version, Mode: mode, MissingCapabilities: a.MissingCapabilities(), DaemonBoot: boot, SchemaVersion: version}
	if err = tx.QueryRowContext(ctx, "SELECT generation FROM metadata WHERE singleton=1").Scan(&meta.Generation); err != nil {
		return err
	}
	if err = meta.Validate(); err != nil {
		return err
	}
	if !s.fixture {
		if err = recoverAttempts(ctx, tx, meta.Generation); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.meta = meta
	return nil
}

func (s *Store) seed(ctx context.Context) error {
	var corpus struct {
		Messages map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(data.Cases, &corpus); err != nil {
		return err
	}
	m, err := p.Decode(corpus.Messages["assign"])
	if err != nil {
		return err
	}
	m.Identity = p.Identity{Generation: s.meta.Generation, TaskID: newID(), AttemptID: newID(), Epoch: 1}
	m.MessageID, m.AssignmentID = newID(), newID()
	if err = s.assign(ctx, m); err != nil {
		return err
	}
	// Synthetic uncertainty, not an attempted launch or observed process.
	n, err := p.Decode(corpus.Messages["edge_assigned_unknown"])
	if err != nil {
		return err
	}
	n.Identity, n.MessageID = m.Identity, newID()
	return s.transition(ctx, n)
}

func record(ctx context.Context, tx *sql.Tx, m p.Message, revision int64) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO events(message_id,attempt_id,task_id,epoch,revision,message) VALUES(?,?,?,?,?,?)`, m.MessageID, m.Identity.AttemptID, m.Identity.TaskID, m.Identity.Epoch, revision, string(b))
	return err
}

func (s *Store) assign(ctx context.Context, m p.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("store closed")
	}
	if r := p.CheckCurrent(m, m.Identity); r != p.OK {
		return r
	}
	if m.Kind != "assign" {
		return p.Malformed
	}
	if !s.fixture && m.Version != p.FencedVersion {
		return p.UnknownVersion
	}
	if m.Identity.Generation != s.meta.Generation {
		return p.StaleGeneration
	}
	if m.Identity.Epoch != 1 {
		return p.StaleAttempt
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous string
	duplicate := false
	// Check both namespaces: a duplicate assignment cannot hide a conflicting
	// message ID already bound to another event.
	for _, lookup := range []struct{ query, id string }{
		{"SELECT message FROM events WHERE message_id=?", m.MessageID},
		{"SELECT e.message FROM events e JOIN attempts a ON a.id=e.attempt_id WHERE a.assignment_id=? AND e.revision=1", m.AssignmentID},
	} {
		err = tx.QueryRowContext(ctx, lookup.query, lookup.id).Scan(&previous)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		old, e := p.Decode([]byte(previous))
		if e != nil {
			return e
		}
		r := p.CheckReplay(m, old, old.Identity)
		if r != p.Duplicate {
			return r
		}
		duplicate = true
	}
	if duplicate {
		return p.Duplicate
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO tasks VALUES(?,?)", m.Identity.TaskID, p.TaskReady); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO attempts VALUES(?,?,?,?,?,?)", m.Identity.AttemptID, m.Identity.TaskID, m.Identity.Epoch, p.Assigned, 1, m.AssignmentID); err != nil {
		return err
	}
	if err = record(ctx, tx, m, 1); err != nil {
		return err
	}
	return tx.Commit()
}

// transition consumes validated synthetic messages only. No exported write API,
// process evidence, result/receipt persistence, grants or acceptance exists.
func (s *Store) transition(ctx context.Context, m p.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("store closed")
	}
	if r := p.CheckCurrent(m, m.Identity); r != p.OK {
		return r
	}
	if m.Kind != "transition" {
		return p.Malformed
	}
	if !s.fixture {
		if m.Version != p.FencedVersion {
			return p.UnknownVersion
		}
		// Without authority, process or custody owners, only fail-closed edges exist.
		if m.To != p.Unknown && m.To != p.Stopping {
			return p.ReconciliationRequired
		}
	}
	if m.Identity.Generation != s.meta.Generation {
		return p.StaleGeneration
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current := p.Identity{Generation: s.meta.Generation, TaskID: m.Identity.TaskID}
	var state p.AttemptState
	var revision int64
	err = tx.QueryRowContext(ctx, "SELECT id,epoch,state,revision FROM attempts WHERE task_id=?", m.Identity.TaskID).Scan(&current.AttemptID, &current.Epoch, &state, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return p.StaleAttempt
	}
	if err != nil {
		return err
	}
	if r := p.CheckCurrent(m, current); r != p.OK {
		return r
	}
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT message FROM events WHERE message_id=?", m.MessageID).Scan(&previous)
	if err == nil {
		old, e := p.Decode([]byte(previous))
		if e != nil {
			return e
		}
		return p.CheckReplay(m, old, current)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if r := p.CheckTransition(m, current, state, revision); r != p.OK {
		return r
	}
	result, err := tx.ExecContext(ctx, "UPDATE attempts SET state=?,revision=revision+1 WHERE id=? AND revision=?", m.To, current.AttemptID, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return p.RevisionConflict
	}
	taskState := p.TaskReconciling
	if m.To == p.ResultPending {
		taskState = p.TaskVerifying
	}
	if m.To == p.Succeeded {
		taskState = p.TaskAwaitingReview
	}
	if _, err = tx.ExecContext(ctx, "UPDATE tasks SET state=? WHERE id=?", taskState, current.TaskID); err != nil {
		return err
	}
	if err = record(ctx, tx, m, revision+1); err != nil {
		return err
	}
	return tx.Commit() // No result acknowledgement, including after a commit.
}

func (s *Store) Status(ctx context.Context) (a.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := a.Status{Metadata: s.meta}
	if s.db == nil {
		return v, fmt.Errorf("store closed")
	}
	err := s.db.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM tasks),(SELECT count(*) FROM events)").Scan(&v.TaskCount, &v.EventCount)
	if err == nil {
		err = v.Validate()
	}
	return v, err
}

func (s *Store) Snapshot(ctx context.Context, taskID string, limit int) (a.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := a.Snapshot{Metadata: s.meta, Tasks: []a.Task{}, Events: []a.Event{}}
	if s.db == nil {
		return v, fmt.Errorf("store closed")
	}
	if limit < 1 || limit > a.MaxItems || taskID != "" && !p.ValidID(taskID) {
		return v, p.Malformed
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT t.id,t.state,a.id,a.epoch,a.state,a.revision FROM tasks t JOIN attempts a ON a.task_id=t.id WHERE (?='' OR t.id=?) ORDER BY t.id LIMIT ?`, taskID, taskID, limit)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var t a.Task
		t.Attempt.Identity.Generation = s.meta.Generation
		t.Attempt.Observation = p.Observation{Desired: "stop", ConfirmedProcess: "unknown", RemoteWork: "unknown", Quarantined: true}
		if s.fixture {
			t.Attempt.Observation.ConfirmedProcess = "not_started"
		}
		if err = rows.Scan(&t.TaskID, &t.State, &t.Attempt.Identity.AttemptID, &t.Attempt.Identity.Epoch, &t.Attempt.State, &t.Attempt.Revision); err != nil {
			rows.Close()
			return v, err
		}
		t.Attempt.Identity.TaskID = t.TaskID
		v.Tasks = append(v.Tasks, t)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return v, err
	}
	if taskID != "" && len(v.Tasks) == 0 {
		return v, sql.ErrNoRows
	}
	rows, err = tx.QueryContext(ctx, `SELECT sequence,revision,message FROM events WHERE (?='' OR task_id=?) ORDER BY sequence LIMIT ?`, taskID, taskID, limit)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var e a.Event
		var raw string
		if err = rows.Scan(&e.Sequence, &e.Revision, &raw); err != nil {
			rows.Close()
			return v, err
		}
		e.Message, err = p.Decode([]byte(raw))
		if err != nil {
			rows.Close()
			return v, err
		}
		v.Events = append(v.Events, e)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return v, err
	}
	if err = v.Validate(); err != nil {
		return v, err
	}
	return v, tx.Commit()
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.db != nil {
		err = s.db.Close()
		s.db = nil
	}
	if s.lock != nil {
		err = errors.Join(err, s.lock.Close())
		s.lock = nil
	}
	return err
}

// Dispose removes only this Store's disposable directory while holding ownership.
func (s *Store) Dispose() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.fixture {
		return fmt.Errorf("persistent state cannot be disposed")
	}
	if s.lock == nil {
		var err error
		s.lock, err = lockDirectory(s.dir)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			return err
		}
		s.db = nil
	}
	err := os.RemoveAll(s.dir)
	err = errors.Join(err, s.lock.Close())
	s.lock = nil
	return err
}
