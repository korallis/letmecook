package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/korallis/letmecook/internal/store"
)

// Restore is offline, verifies before writing, and never overwrites or removes
// the backup. Targets must be distinct empty private directories. state.db is
// published LAST, only after the new-generation paused transaction is durable.
// Failed restores leave .restore-incomplete and require fresh empty targets.
func Restore(ctx context.Context, dir, stateDir, artifactsDir string) (store.RestoreEntry, error) {
	return restore(ctx, dir, stateDir, artifactsDir, fileOps{})
}
func restore(ctx context.Context, dir, stateDir, artifactsDir string, ops fileOps) (entry store.RestoreEntry, err error) {
	m, raw, err := verify(ctx, dir)
	if err != nil {
		return entry, err
	}
	paths := []string{dir, stateDir, artifactsDir}
	for i := range paths {
		paths[i], err = cleanPath(paths[i])
		if err != nil {
			return entry, err
		}
	}
	for i := range paths {
		for j := i + 1; j < len(paths); j++ {
			if overlaps(paths[i], paths[j]) {
				return entry, errors.New("overlapping_paths")
			}
		}
	}
	// Preflight BOTH before creating a marker or copying any bytes.
	state, closeState, err := emptyTarget(paths[1])
	if err != nil {
		return entry, err
	}
	defer closeState()
	artifacts, closeArtifacts, err := emptyTarget(paths[2])
	if err != nil {
		return entry, err
	}
	defer closeArtifacts()
	source, err := sourceRoot(paths[0])
	if err != nil {
		return entry, err
	}
	defer source.Close()
	if err = ops.writeFile(ctx, state, ".restore-incomplete", []byte(m.BackupID+"\n")); err != nil {
		return entry, err
	}
	if err = syncRoot(state); err != nil {
		return entry, err
	}
	if err = state.Mkdir(".restore-pending", 0700); err != nil {
		return entry, err
	}
	pending, err := state.OpenRoot(".restore-pending")
	if err != nil {
		return entry, err
	}
	defer pending.Close()
	if err = ops.copy(ctx, source, "state.db", pending, "state.db", m.Database); err != nil {
		return entry, err
	}
	for _, e := range m.Inventory {
		target, name := artifacts, strings.TrimPrefix(e.RelativePath, "artifacts/")
		if e.Kind == "stream" {
			target, name = pending, e.RelativePath
		}
		if err = ops.copy(ctx, source, e.RelativePath, target, name, Blob{e.SHA256, e.Bytes}); err != nil {
			return entry, err
		}
	}
	if err = syncTree(artifacts); err != nil {
		return entry, err
	}
	if err = syncTree(pending); err != nil {
		return entry, err
	}
	hash := sha256.Sum256(raw)
	restored, err := store.OpenRestored(ctx, pending.Name(), artifacts.Name(), store.RestoreRecord{BackupID: m.BackupID, BackupCreatedMS: m.CreatedMS, ManifestSHA256: hex.EncodeToString(hash[:])}, store.Options{})
	if err != nil {
		return entry, err
	}
	history, e := restored.RestoreHistory(ctx)
	closeErr := restored.Close()
	if e != nil || closeErr != nil {
		return entry, errors.Join(e, closeErr)
	}
	if len(history) == 0 {
		return entry, errors.New("restore_history_missing")
	}
	// Match our manifest, not a timestamp ordering assumption under clock rollback.
	found := false
	for _, r := range history {
		if r.OldGeneration == m.Generation && r.BackupID == m.BackupID && r.ManifestSHA256 == hex.EncodeToString(hash[:]) {
			entry = r
			found = true
		}
	}
	if !found {
		return entry, errors.New("restore_history_missing")
	}
	// The final SQLite close checkpoints/removes its WAL. Never publish just its
	// main file while a nonempty WAL could contain the fencing transaction.
	if st, e := pending.Stat("state.db-wal"); e == nil && st.Size() != 0 {
		return entry, errors.New("restore_wal_not_checkpointed")
	} else if e != nil && !os.IsNotExist(e) {
		return entry, e
	}
	if err = syncPath(pending, "state.db"); err != nil {
		return entry, err
	}
	if _, e = pending.Stat("streams"); e == nil {
		if err = state.Rename(".restore-pending/streams", "streams"); err != nil {
			return entry, err
		}
	} else if !os.IsNotExist(e) {
		return entry, e
	}
	if err = ctx.Err(); err != nil {
		return entry, err
	}
	if err = state.Rename(".restore-pending/state.db", "state.db"); err != nil {
		return entry, err
	}
	if err = syncRoot(state); err != nil {
		return entry, err
	}
	// Only these known SQLite scratch files can remain. Do not recursively remove
	// an unexpected directory or any user-supplied data on the success path.
	for _, name := range []string{"state.db-wal", "state.db-shm"} {
		if e = pending.Remove(name); e != nil && !os.IsNotExist(e) {
			return entry, e
		}
	}
	if err = state.Remove(".restore-pending"); err != nil {
		return entry, err
	}
	if err = state.Remove(".restore-incomplete"); err != nil {
		return entry, err
	}
	if err = syncRoot(state); err != nil {
		return entry, err
	}
	return entry, nil
}

type service struct {
	s                        *store.Store
	artifacts, streams, root string
}

// NewService confines owner-selected destinations to a single new child of root
// (--backup-dir). The caller wires it into httpapi.Deps.Backup; nil disables the
// API. Source roots are explicit operator configuration, never request values.
func NewService(s *store.Store, artifactsDir, streamsDir, root string) (Service, error) {
	if s == nil {
		return nil, errors.New("backup_unavailable")
	}
	clean, err := cleanPath(root)
	if err != nil {
		return nil, err
	}
	if err = os.Mkdir(clean, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	r, err := sourceRoot(clean)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	st, err := r.Stat(".")
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm() != 0700 {
		return nil, errors.New("backup_root_not_private")
	}
	artifactsDir, err = cleanPath(artifactsDir)
	if err != nil {
		return nil, err
	}
	streamsDir, err = cleanPath(streamsDir)
	if err != nil {
		return nil, err
	}
	if overlaps(clean, artifactsDir) || overlaps(clean, streamsDir) {
		return nil, errors.New("overlapping_paths")
	}
	return &service{s, artifactsDir, streamsDir, r.Name()}, nil
}
func (s *service) destination(name string) (string, error) {
	if filepath.IsAbs(name) {
		if filepath.Dir(name) != s.root {
			return "", errors.New("invalid_destination")
		}
		name = filepath.Base(name)
	}
	if name == "" || name == "." || name == ".." || len(name) > 128 || strings.ContainsAny(name, "/\\\x00\r\n") || filepath.Clean(name) != name {
		return "", errors.New("invalid_destination")
	}
	r, err := sourceRoot(s.root)
	if err != nil {
		return "", err
	}
	defer r.Close()
	if r.Name() != s.root {
		return "", fmt.Errorf("backup_root_changed")
	}
	return filepath.Join(s.root, name), nil
}
func (s *service) Create(ctx context.Context, destination string) (Manifest, error) {
	out, err := s.destination(destination)
	if err != nil {
		return Manifest{}, err
	}
	return Create(ctx, s.s, s.artifacts, s.streams, out)
}
func (s *service) Verify(dir string) (Manifest, error) {
	out, err := s.destination(dir)
	if err != nil {
		return Manifest{}, err
	}
	return Verify(out)
}
