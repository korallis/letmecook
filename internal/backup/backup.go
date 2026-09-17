// Package backup creates self-verifying paused snapshots. The manifest is the
// completion marker: a directory without it is never a backup, even if state.db
// opens. Nothing here stops a surviving runner or authorizes resume.
package backup

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/korallis/letmecook/internal/closedjson"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
	a "github.com/korallis/letmecook/schemas/readapi"
)

const Version = "gaffer-backup-v1"
const manifestName = "backup-manifest.json"
const maxManifestBytes = 16 << 20

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
type Service interface {
	Create(ctx context.Context, destination string) (Manifest, error)
	Verify(dir string) (Manifest, error)
}

func uuid() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Create includes only snapshot-referenced immutable artifacts and registered
// attempt sinks. Arbitrary files under either source root are not walked/copied.
func Create(ctx context.Context, s *store.Store, artifactsDir, streamsDir, out string) (Manifest, error) {
	return create(ctx, s, artifactsDir, streamsDir, out, fileOps{})
}
func create(ctx context.Context, s *store.Store, artifactsDir, streamsDir, out string, ops fileOps) (m Manifest, err error) {
	if s == nil {
		return m, errors.New("backup_unavailable")
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	// Pin also checks pause/active attempts under the store mutex. Hold it through
	// manifest publication, so a racing resume or GC cannot cross the snapshot.
	release, err := s.PinArtifacts()
	if err != nil {
		return m, err
	}
	defer release()
	output, closeOutput, err := emptyTarget(out)
	if err != nil {
		return m, err
	}
	defer closeOutput()
	if overlaps(output.Name(), artifactsDir) || overlaps(output.Name(), streamsDir) {
		return m, errors.New("overlapping_paths")
	}
	// Failure leaves inspectable partial data. Only withdraw a completion marker
	// this call published; a competing caller must never delete another backup.
	published := false
	defer func() {
		if err != nil && published {
			_ = output.Remove(manifestName)
			_ = syncRoot(output)
		}
	}()
	if err = s.SnapshotDatabase(ctx, filepath.Join(output.Name(), "state.db")); err != nil {
		return m, err
	}
	if err = omitGatewayProfiles(ctx, filepath.Join(output.Name(), "state.db")); err != nil {
		return m, err
	}
	if err = syncPath(output, "state.db"); err != nil {
		return m, err
	}

	m = Manifest{Version: Version, BackupID: uuid(), SchemaVersion: a.SchemaVersion, CreatedMS: time.Now().UnixMilli(), Inventory: []Entry{}, AttemptStates: map[string]p.AttemptState{}}
	refs, err := databaseInventory(ctx, filepath.Join(output.Name(), "state.db"), &m, false)
	if err != nil {
		return m, err
	}
	artifacts, err := sourceRoot(artifactsDir)
	if err != nil {
		return m, err
	}
	defer artifacts.Close()
	for _, entry := range refs {
		if err = ops.copy(ctx, artifacts, strings.TrimPrefix(entry.RelativePath, "artifacts/"), output, entry.RelativePath, Blob{entry.SHA256, entry.Bytes}); err != nil {
			return m, fmt.Errorf("%s: %w", entry.RelativePath, err)
		}
		m.Inventory = append(m.Inventory, entry)
	}
	streams, err := sourceRoot(streamsDir)
	if err != nil && !os.IsNotExist(err) {
		return m, err
	}
	if streams != nil {
		defer streams.Close()
		for attempt := range m.AttemptStates {
			name := attempt + ".sink/journal.jsonl"
			// An attempt that never emitted output legitimately has no sink. Only known
			// attempt names are considered; key files or gateway configs are ignored.
			if _, e := streams.Lstat(attempt + ".sink"); os.IsNotExist(e) {
				continue
			} else if e != nil {
				return m, e
			}
			blob, e := hashFile(ctx, streams, name)
			if e != nil {
				return m, e
			}
			entry := Entry{"stream", "streams/" + name, blob.SHA256, blob.Bytes}
			if err = ops.copy(ctx, streams, name, output, entry.RelativePath, blob); err != nil {
				return m, err
			}
			m.Inventory = append(m.Inventory, entry)
		}
	}
	sort.Slice(m.Inventory, func(i, j int) bool { return m.Inventory[i].RelativePath < m.Inventory[j].RelativePath })
	if m.Database, err = hashFile(ctx, output, "state.db"); err != nil {
		return m, err
	}
	if err = m.validate(); err != nil {
		return m, err
	}
	if err = checkStreams(ctx, output, m); err != nil {
		return m, err
	}

	raw, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	if len(raw) > maxManifestBytes {
		return m, errors.New("manifest_too_large")
	}
	// Re-hash all durable copies before publishing the only completion marker.
	for _, entry := range m.Inventory {
		if err = checkFile(ctx, output, entry.RelativePath, Blob{entry.SHA256, entry.Bytes}); err != nil {
			return m, err
		}
	}
	if err = ops.writeFile(ctx, output, ".manifest-pending", raw); err != nil {
		return m, err
	}
	allowed := map[string]bool{"state.db": true, ".manifest-pending": true}
	for _, e := range m.Inventory {
		allowed[e.RelativePath] = true
	}
	if err = exactTree(output, allowed); err != nil {
		return m, err
	}
	if err = syncTree(output); err != nil {
		return m, err
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	if err = output.Rename(".manifest-pending", manifestName); err != nil {
		return m, err
	}
	published = true
	if err = syncRoot(output); err != nil {
		return m, err
	}
	return m, nil
}

func Verify(dir string) (Manifest, error) {
	m, _, err := verify(context.Background(), dir)
	return m, err
}
func verify(ctx context.Context, dir string) (m Manifest, raw []byte, err error) {
	root, err := sourceRoot(dir)
	if err != nil {
		return m, nil, err
	}
	defer root.Close()
	raw, err = readBounded(root, manifestName, maxManifestBytes)
	if err != nil {
		return m, nil, fmt.Errorf("backup_incomplete: %w", err)
	}
	if err = closedjson.Decode(raw, &m, maxManifestBytes, nil); err != nil {
		return m, nil, fmt.Errorf("invalid_manifest: %w", err)
	}
	if err = manifestShape(raw); err != nil {
		return m, nil, err
	}
	if err = m.validate(); err != nil {
		return m, nil, err
	}
	if err = checkFile(ctx, root, "state.db", m.Database); err != nil {
		return m, nil, err
	}
	refs, err := databaseInventory(ctx, filepath.Join(root.Name(), "state.db"), &m, true)
	if err != nil {
		return m, nil, err
	}
	entries := map[string]Entry{}
	for _, e := range m.Inventory {
		entries[e.RelativePath] = e
		if err = checkFile(ctx, root, e.RelativePath, Blob{e.SHA256, e.Bytes}); err != nil {
			return m, nil, fmt.Errorf("%s: %w", e.RelativePath, err)
		}
	}
	for _, e := range refs {
		if got, ok := entries[e.RelativePath]; !ok || got != e {
			return m, nil, fmt.Errorf("incomplete_inventory: %s", e.RelativePath)
		}
		delete(entries, e.RelativePath)
	}
	for _, e := range entries {
		if e.Kind != "stream" {
			return m, nil, errors.New("unreferenced_inventory")
		}
		id := strings.TrimSuffix(strings.TrimPrefix(e.RelativePath, "streams/"), ".sink/journal.jsonl")
		if _, ok := m.AttemptStates[id]; !ok {
			return m, nil, errors.New("unreferenced_stream")
		}
	}
	if err = checkStreams(ctx, root, m); err != nil {
		return m, nil, err
	}
	// Sidecars or extra files could carry omitted content/secrets or change the
	// meaning of a main SQLite file. Never accept an inventory that ignores them.
	allowed := map[string]bool{"state.db": true, manifestName: true}
	for _, e := range m.Inventory {
		allowed[e.RelativePath] = true
	}
	if err = exactTree(root, allowed); err != nil {
		return m, nil, err
	}
	return m, raw, nil
}
func (m Manifest) validate() error {
	if m.Version != Version || m.SchemaVersion != a.SchemaVersion || !p.ValidID(m.BackupID) || !p.ValidID(m.Generation) || !p.ValidID(m.DaemonBoot) || m.CreatedMS < 1 || m.CreatedMS > 9007199254740991 || !digest(m.Database.SHA256) || m.Database.Bytes < 1 || m.Inventory == nil || m.AttemptStates == nil {
		return errors.New("invalid_manifest")
	}
	seen := map[string]bool{}
	for _, e := range m.Inventory {
		if seen[e.RelativePath] || !digest(e.SHA256) || e.Bytes < 0 || e.Bytes > 9007199254740991 {
			return errors.New("invalid_inventory")
		}
		seen[e.RelativePath] = true
		switch e.Kind {
		case "blob":
			if e.RelativePath != "artifacts/blobs/"+e.SHA256[:2]+"/"+e.SHA256 {
				return errors.New("invalid_inventory_path")
			}
		case "manifest":
			id := strings.TrimSuffix(strings.TrimPrefix(e.RelativePath, "artifacts/manifests/"), ".json")
			if !p.ValidID(id) || e.RelativePath != "artifacts/manifests/"+id+".json" || e.Bytes < 1 || e.Bytes > 1<<20 {
				return errors.New("invalid_inventory_path")
			}
		case "stream":
			id := strings.TrimSuffix(strings.TrimPrefix(e.RelativePath, "streams/"), ".sink/journal.jsonl")
			if !p.ValidID(id) || e.RelativePath != "streams/"+id+".sink/journal.jsonl" {
				return errors.New("invalid_inventory_path")
			}
		default:
			return errors.New("invalid_inventory_kind")
		}
	}
	for id, state := range m.AttemptStates {
		if !p.ValidID(id) || !validState(state) {
			return errors.New("invalid_attempt_states")
		}
	}
	return nil
}
func validState(s p.AttemptState) bool {
	switch s {
	case p.Assigned, p.Starting, p.Running, p.ResultPending, p.Stopping, p.Unknown, p.Succeeded, p.Failed, p.Cancelled, p.Expired:
		return true
	}
	return false
}

// closedjson rejects unknown/duplicate/null fields. Counts additionally require
// every field, including a zero-byte blob's bytes rather than a defaulted zero.
func manifestShape(raw []byte) error {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil || !exactKeys(top, "version", "backup_id", "generation", "daemon_boot", "schema_version", "created_ms", "database", "inventory", "attempt_states") {
		return errors.New("invalid_manifest_shape")
	}
	var database map[string]json.RawMessage
	if json.Unmarshal(top["database"], &database) != nil || !exactKeys(database, "sha256", "bytes") {
		return errors.New("invalid_manifest_shape")
	}
	var inventory []map[string]json.RawMessage
	if json.Unmarshal(top["inventory"], &inventory) != nil {
		return errors.New("invalid_manifest_shape")
	}
	for _, entry := range inventory {
		if !exactKeys(entry, "kind", "relative_path", "sha256", "bytes") {
			return errors.New("invalid_manifest_shape")
		}
	}
	return nil
}
func exactKeys(fields map[string]json.RawMessage, names ...string) bool {
	if len(fields) != len(names) {
		return false
	}
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	return true
}
