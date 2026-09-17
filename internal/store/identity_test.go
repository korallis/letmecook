package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	i "github.com/korallis/letmecook/internal/identity"
)

func TestIdentityPersistenceExpiryRecoveryAndRollback(t *testing.T) {
	s, artifacts := persistent(t)
	ownerPin, runnerPin, newOwnerPin := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	if err := s.BootstrapOwner(ctx, ownerPin, false); err != nil {
		t.Fatal(err)
	}
	owner, err := s.Authenticate(ctx, ownerPin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateEnrollment(ctx, runnerPin, newID(), runnerPin); !errors.Is(err, i.Denied) {
		t.Fatal("untrusted actor")
	}
	invite, err := s.CreateEnrollment(ctx, ownerPin, newID(), runnerPin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE enrollments SET expires=? WHERE id=?", time.Now().Unix(), invite.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, runnerPin, invite.Token); !errors.Is(err, i.Denied) {
		t.Fatal("expired enrollment")
	}
	if _, err := s.db.Exec("UPDATE enrollments SET expires=? WHERE id=?", time.Now().Add(time.Minute).Unix(), invite.ID); err != nil {
		t.Fatal(err)
	}
	sqlExec(t, s, "CREATE TRIGGER interrupt_identity BEFORE INSERT ON credentials BEGIN SELECT RAISE(ABORT,'secret SQL detail'); END")
	if _, err := s.Enroll(ctx, runnerPin, invite.Token); err != i.Unavailable {
		t.Fatal("failed enrollment acknowledged/leaked", err)
	}
	var consumed, principals int
	if err := s.db.QueryRow("SELECT consumed FROM enrollments WHERE id=?", invite.ID).Scan(&consumed); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM principals WHERE role='runner'").Scan(&principals); err != nil {
		t.Fatal(err)
	}
	if consumed != 0 || principals != 0 {
		t.Fatal("partial enrollment")
	}
	sqlExec(t, s, "DROP TRIGGER interrupt_identity")
	runner, err := s.Enroll(ctx, runnerPin, invite.Token)
	if err != nil {
		t.Fatal(err)
	}
	if runner.Enabled || runner.ID != invite.RunnerID {
		t.Fatal("runner default")
	}
	if _, err := s.UpdateIdentity(ctx, runnerPin, runner.ID, 1, "enable", ""); err != i.Denied {
		t.Fatal("runner owner mutation")
	}
	if _, err := s.UpdateIdentity(ctx, ownerPin, runner.ID, 1, "rotate", ownerPin); err != i.Conflict {
		t.Fatal("credential reassignment")
	}
	if _, err := s.Authenticate(ctx, runnerPin); err != nil {
		t.Fatal("failed rotation revoked old pin")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	persisted, err := r.Authenticate(ctx, runnerPin)
	if err != nil || persisted != runner {
		t.Fatal("identity changed on reopen")
	}
	if err := r.BootstrapOwner(ctx, ownerPin, false); err != i.Conflict {
		t.Fatal("bootstrap replay after restart")
	}
	if _, err := r.Enroll(ctx, runnerPin, invite.Token); err != i.Denied {
		t.Fatal("join replay after restart")
	}
	pendingPin := strings.Repeat("d", 64)
	pending, err := r.CreateEnrollment(ctx, ownerPin, newID(), pendingPin)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.BootstrapOwner(ctx, pendingPin, true); err != i.Conflict {
		t.Fatal("recovery reassigned reserved runner pin")
	}
	if err := r.BootstrapOwner(ctx, ownerPin, true); err != i.Conflict {
		t.Fatal("recovery reused old credential")
	}
	if _, err := r.Authenticate(ctx, ownerPin); err != nil {
		t.Fatal("failed recovery was not atomic")
	}
	if err := r.BootstrapOwner(ctx, newOwnerPin, true); err != nil {
		t.Fatal(err)
	}
	for _, fp := range []string{ownerPin, runnerPin} {
		if _, err := r.Authenticate(ctx, fp); err != i.Denied {
			t.Fatal("recovery retained old credential")
		}
	}
	if _, err := r.Enroll(ctx, pendingPin, pending.Token); err != i.Denied {
		t.Fatal("recovery retained invite")
	}
	recovered, err := r.Authenticate(ctx, newOwnerPin)
	if err != nil || recovered.ID != owner.ID || recovered.Revoked {
		t.Fatal("recovered owner identity")
	}
	if _, err := r.UpdateIdentity(ctx, newOwnerPin, runner.ID, 2, "enable", ""); err != i.Conflict {
		t.Fatal("revoked identity revived")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// Persisted byte contracts contain public certificate pins and token hashes,
	// not raw enrollment secrets; inspect actual DB/WAL/artifact output, not source.
	for _, root := range []string{s.dir, artifacts} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), string(invite.Token)) || strings.Contains(string(b), string(pending.Token)) {
				t.Fatal("persisted plaintext secret")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Authenticate(ctx, runnerPin); err != i.Denied {
		t.Fatal("revocation lost at restart")
	}
	if _, err := reopened.Authenticate(ctx, newOwnerPin); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityMigrationAndFixtureRefusal(t *testing.T) {
	s, artifacts := persistent(t)
	generation := s.meta.Generation
	s.Close()
	db, err := sql.Open("sqlite", filepath.Join(s.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(dropDispatchSchema + "DROP TABLE repository_profiles; DROP TABLE repositories; DROP TABLE execution_grant_heads; DROP TABLE execution_invalidations; DROP TABLE execution_grants; DROP TABLE credentials; DROP TABLE enrollments; DROP TABLE principals; PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.meta.SchemaVersion != 10 || r.meta.Generation != generation {
		t.Fatal("v2 migration")
	}
	if ok, err := r.IdentityConfigured(ctx); err != nil || ok {
		t.Fatal("migration pretrusted an owner")
	}
	f := owned(t)
	if err := f.BootstrapOwner(ctx, strings.Repeat("a", 64), false); err != i.Unavailable {
		t.Fatal("fixture identity mutation")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := r.BootstrapOwner(cancelled, strings.Repeat("a", 64), false); err == nil {
		t.Fatal("cancelled bootstrap committed")
	}
	if ok, err := r.IdentityConfigured(ctx); err != nil || ok {
		t.Fatal("partial bootstrap")
	}
}
