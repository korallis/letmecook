// Package backup defines verified offline snapshots. These stubs never copy files,
// restore state, or claim that a live execution has stopped.
package backup

import (
	"context"
	"errors"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

const Version = "gaffer-backup-v1"

var ErrNotImplemented = errors.New("backup_not_implemented")

type Blob struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type Entry struct {
	Kind         string `json:"kind"`
	RelativePath string `json:"relative_path"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
}
type Manifest struct {
	Version       string                    `json:"version"`
	BackupID      string                    `json:"backup_id"`
	Generation    string                    `json:"generation"`
	DaemonBoot    string                    `json:"daemon_boot"`
	SchemaVersion int                       `json:"schema_version"`
	CreatedMS     int64                     `json:"created_ms"`
	Database      Blob                      `json:"database"`
	Inventory     []Entry                   `json:"inventory"`
	AttemptStates map[string]p.AttemptState `json:"attempt_states"`
}

// Service is the owner route's configured snapshot boundary, not a filesystem API.
type Service interface {
	Create(ctx context.Context, destination string) (Manifest, error)
	Verify(dir string) (Manifest, error)
}

func Create(ctx context.Context, s *store.Store, artifactsDir, streamsDir, out string) (Manifest, error) {
	return Manifest{}, ErrNotImplemented
}
func Verify(dir string) (Manifest, error) { return Manifest{}, ErrNotImplemented }
