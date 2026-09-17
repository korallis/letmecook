package store

// Backup/restore owns its startup transaction and the collection pin here, so it
// does not weaken normal startup, receipt immutability, or the workflow API.
import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	p "github.com/korallis/letmecook/schemas/execution"
	a "github.com/korallis/letmecook/schemas/readapi"
)

type RestoreRecord struct {
	BackupID        string `json:"backup_id"`
	BackupCreatedMS int64  `json:"backup_created_ms"`
	ManifestSHA256  string `json:"manifest_sha256"`
}
type RestoreEntry struct {
	ID              string `json:"id"`
	OldGeneration   string `json:"old_generation"`
	NewGeneration   string `json:"new_generation"`
	BackupID        string `json:"backup_id"`
	BackupCreatedMS int64  `json:"backup_created_ms"`
	RestoredMS      int64  `json:"restored_ms"`
	ManifestSHA256  string `json:"manifest_sha256"`
}

// A pin holds the existing store mutex, also used by collection, for the entire
// copy. This deliberately freezes ALL metadata writes (including resume) rather
// than introducing a weaker GC-only boundary. No other store method may be called
// while holding a pin, except SnapshotDatabase. The small registry coordinates
// that method with release; it never acquires a store mutex while locked.
var backupPins = struct {
	sync.Mutex
	held map[*Store]bool
}{held: make(map[*Store]bool)}

func (s *Store) backupReady(ctx context.Context) error {
	if s.db == nil || s.fixture || s.artifacts == "" {
		return errors.New("backup_unavailable")
	}
	var paused, active bool
	if err := s.db.QueryRowContext(ctx, `SELECT paused, EXISTS(SELECT 1 FROM attempts WHERE state NOT IN ('succeeded','failed','cancelled','expired')) FROM daemon_state WHERE singleton=1`).Scan(&paused, &active); err != nil {
		return err
	}
	if active {
		return errors.New("active_execution")
	}
	if !paused {
		return errors.New("not_paused")
	}
	return nil
}

// PinArtifacts blocks collection AND resume until its idempotent release runs.
// It refuses unpaused stores and every unresolved attempt, including unknown.
func (s *Store) PinArtifacts() (release func(), err error) {
	s.mu.Lock()
	if err = s.backupReady(context.Background()); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	backupPins.Lock()
	backupPins.held[s] = true
	backupPins.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			backupPins.Lock()
			delete(backupPins.held, s)
			s.mu.Unlock()
			backupPins.Unlock()
		})
	}, nil
}

// SnapshotDatabase uses modernc SQLite's VACUUM INTO (including committed WAL
// pages), not a main-file copy. Both independent snapshots and pinned snapshots
// execute under s.mu; the latter borrow the pin's already-held mutex. The output
// must not exist. VACUUM INTO does not fsync its output; we do that explicitly.
func (s *Store) SnapshotDatabase(ctx context.Context, path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("invalid_destination")
	}
	backupPins.Lock()
	if backupPins.held[s] {
		defer backupPins.Unlock()
		return s.snapshotDatabaseLocked(ctx, path)
	}
	backupPins.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotDatabaseLocked(ctx, path)
}
func (s *Store) snapshotDatabaseLocked(ctx context.Context, path string) (err error) {
	if err = s.backupReady(ctx); err != nil {
		return err
	}
	// Reserve a private, empty, non-aliased output. SQLite permits an empty file.
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, out.Close())
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	if _, err = s.db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// OpenRestored opens ONLY a copied schema-10 persistent database. A single FULL
// synchronous transaction records history, changes generation/boot, pauses and
// recovers attempts. It never calls Open, whose independent startup transaction
// would otherwise expose a partially restored generation after a crash.
func OpenRestored(ctx context.Context, dir, artifactsDir string, restore RestoreRecord, o Options) (_ *Store, err error) {
	if !p.ValidID(restore.BackupID) || restore.BackupCreatedMS <= 0 || restore.BackupCreatedMS > 9007199254740991 || !validHex(restore.ManifestSHA256) {
		return nil, p.Malformed
	}
	if dir, err = prepareDirectory(dir); err != nil {
		return nil, err
	}
	if artifactsDir, err = prepareDirectory(artifactsDir); err != nil {
		return nil, err
	}
	af, err := openDirectory(artifactsDir)
	if err != nil {
		return nil, err
	}
	if err = errors.Join(af.Sync(), af.Close()); err != nil {
		return nil, err
	}
	lock, err := lockDirectory(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{lock: lock, dir: dir, artifacts: artifactsDir, admission: o.Admission, controlStart: time.Now(), controlNow: time.Now}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	path := filepath.Join(dir, "state.db")
	for _, name := range []string{"state.db", "state.db-wal", "state.db-shm", "state.db-journal"} {
		st, e := os.Lstat(filepath.Join(dir, name))
		if os.IsNotExist(e) && name != "state.db" {
			continue
		}
		if e != nil {
			return nil, e
		}
		if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || !privateFile(st) {
			return nil, errors.New("restore files must be private, owned, regular and singly linked")
		}
	}
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = url.Values{"mode": {"rw"}, "_txlock": {"immediate"}, "_pragma": {"foreign_keys(1)", "journal_mode(WAL)", "synchronous(FULL)", "fullfsync(1)", "checkpoint_fullfsync(1)", "busy_timeout(1000)", "trusted_schema(0)"}}.Encode()
	if s.db, err = sql.Open("sqlite", u.String()); err != nil {
		return nil, err
	}
	s.db.SetMaxOpenConns(1)
	s.db.SetMaxIdleConns(1)
	var mode string
	if err = s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		return nil, errors.Join(err, errors.New("restore WAL unavailable"))
	}
	for pragma, want := range map[string]int{"user_version": a.SchemaVersion, "application_id": 0x47414646, "foreign_keys": 1, "synchronous": 2, "fullfsync": 1, "checkpoint_fullfsync": 1, "trusted_schema": 0} {
		var got int
		if err = s.db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
			return nil, err
		}
		if got != want {
			return nil, fmt.Errorf("restore incompatible %s", pragma)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var old string
	if err = tx.QueryRowContext(ctx, "SELECT generation FROM metadata WHERE singleton=1").Scan(&old); err != nil {
		return nil, err
	}
	if !p.ValidID(old) {
		return nil, p.Malformed
	}
	meta := a.Metadata{Version: a.Version, Mode: "store-only", MissingCapabilities: a.MissingCapabilities(), Generation: newID(), DaemonBoot: newID(), SchemaVersion: a.SchemaVersion}
	if err = meta.Validate(); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, "UPDATE metadata SET generation=?,daemon_boot=?,artifacts_dir=? WHERE singleton=1", meta.Generation, meta.DaemonBoot, artifactsDir); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE daemon_state SET paused=1,reason='restored',updated_ms=? WHERE singleton=1", now); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO restore_history VALUES(?,?,?,?,?,?,?)", newID(), old, meta.Generation, restore.BackupID, restore.BackupCreatedMS, now, restore.ManifestSHA256); err != nil {
		return nil, err
	}
	if err = expireGrants(ctx, tx, now); err != nil {
		return nil, err
	}
	if err = recoverAttempts(ctx, tx, meta.Generation); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.meta = meta
	if err = s.lock.Sync(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) RestoreHistory(ctx context.Context) ([]RestoreEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil || s.fixture {
		return nil, errors.New("restore_history_unavailable")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,old_generation,new_generation,backup_id,backup_created_ms,restored_ms,manifest_sha256 FROM restore_history ORDER BY restored_ms,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []RestoreEntry{}
	for rows.Next() {
		var r RestoreEntry
		if err = rows.Scan(&r.ID, &r.OldGeneration, &r.NewGeneration, &r.BackupID, &r.BackupCreatedMS, &r.RestoredMS, &r.ManifestSHA256); err != nil {
			return nil, err
		}
		entries = append(entries, r)
	}
	return entries, rows.Err()
}
