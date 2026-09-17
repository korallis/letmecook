package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	g "github.com/korallis/letmecook/internal/authority"
)

// A paused daemon admits no dispatch: the gate lives inside the admission
// transaction so callers outside HTTP cannot bypass it.
func TestDispatchRefusedWhileDaemonPaused(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	if _, err := f.s.db.Exec("UPDATE daemon_state SET paused=1, reason='test' WHERE singleton=1"); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.Dispatch(ctx, f.request)
	var refusal *g.Refusal
	if !errors.As(err, &refusal) || refusal.Code != "paused" {
		t.Fatalf("paused daemon admitted dispatch: %v", err)
	}
	if _, err := f.s.db.Exec("UPDATE daemon_state SET paused=0 WHERE singleton=1"); err != nil {
		t.Fatal(err)
	}
	admitted(t, f)
}

// A state directory marked by an interrupted restore is never served.
func TestOpenRefusesIncompleteRestoreMarker(t *testing.T) {
	s, artifacts := persistent(t)
	dir := s.dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, ".restore-incomplete")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, dir, artifacts); err == nil {
		t.Fatal("opened a state directory marked restore-incomplete")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Close()
}
