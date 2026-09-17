package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/runstream"
	p "github.com/korallis/letmecook/schemas/execution"
)

func TestOpenRestoredOwnsStreamsLifecycle(t *testing.T) {
	s, artifacts := persistent(t)
	_, owner, _ := repositoryFixture(t, s)
	if _, err := s.SetPaused(ctx, owner, newID(), true, "snapshot", false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "restored")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.SnapshotDatabase(ctx, filepath.Join(dir, "state.db")); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenRestored(ctx, dir, artifacts, RestoreRecord{BackupID: newID(), BackupCreatedMS: time.Now().UnixMilli(), ManifestSHA256: strings.Repeat("a", 64)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	view := restored.Streams()
	if view == nil {
		t.Fatal("restored store has no sink view")
	}
	identity := p.Identity{Generation: restored.meta.Generation, TaskID: newID(), AttemptID: newID(), Epoch: 1}
	if _, err := restored.sinks().Receive(identity.AttemptID, identity, spooledRecords(t, identity, "restored")); err != nil {
		t.Fatal(err)
	}
	before, err := view.Watermark(identity.AttemptID)
	if err != nil || before.Through != 1 {
		t.Fatal(before, err)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := view.Watermark(identity.AttemptID); !errors.Is(err, runstream.ErrUnavailable) {
		t.Fatal("closed restored view still writable/readable", err)
	}
	reopened, err := Open(ctx, dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := reopened.Streams().Watermark(identity.AttemptID)
	if err != nil || after != before {
		t.Fatal("restored sink not retained through reopen", before, after, err)
	}
}
