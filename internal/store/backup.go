package store

// Backup and restore seam (docs/decisions/0002 §8). S6 (#23) replaces the bodies
// in this file only; until then every function returns ErrNotImplemented.
// Backups never contain keys, credentials or tokens. A restored store starts a
// new generation and paused; every old-generation runner message is fenced
// stale_generation and its custody quarantined, never current.

import "context"

// RestoreRecord identifies the backup a store was restored from.
type RestoreRecord struct {
	BackupID        string `json:"backup_id"`
	BackupCreatedMS int64  `json:"backup_created_ms"`
	ManifestSHA256  string `json:"manifest_sha256"`
}

// RestoreEntry mirrors one restore_history row.
type RestoreEntry struct {
	ID              string `json:"id"`
	OldGeneration   string `json:"old_generation"`
	NewGeneration   string `json:"new_generation"`
	BackupID        string `json:"backup_id"`
	BackupCreatedMS int64  `json:"backup_created_ms"`
	RestoredMS      int64  `json:"restored_ms"`
	ManifestSHA256  string `json:"manifest_sha256"`
}

// SnapshotDatabase writes a consistent copy of the database to path (VACUUM INTO
// under s.mu, or a checkpoint plus copy under the lock where unsupported).
// Durable point: the snapshot file fsynced before return. backup.Create calls it
// only while the daemon is paused with no non-terminal attempt.
func (s *Store) SnapshotDatabase(ctx context.Context, path string) error {
	return ErrNotImplemented
}

// PinArtifacts suspends CollectArtifacts until release is called, so a backup can
// copy every referenced blob. It returns an error instead of a no-op release, so
// a caller can never mistake the unimplemented seam for a held pin.
func (s *Store) PinArtifacts() (release func(), err error) {
	return nil, ErrNotImplemented
}

// OpenRestored opens a store copied from a verified backup. Durable point: in the
// startup transaction it records restore_history, allocates a new generation,
// sets daemon_state paused=1 reason=restored and lets recoverAttempts mark active
// attempts unknown. o carries the same Options as OpenWithOptions.
func OpenRestored(ctx context.Context, dir, artifactsDir string, restore RestoreRecord, o Options) (*Store, error) {
	return nil, ErrNotImplemented
}

// RestoreHistory lists restore_history oldest first. Durable point: none (read).
func (s *Store) RestoreHistory(ctx context.Context) ([]RestoreEntry, error) {
	return nil, ErrNotImplemented
}
