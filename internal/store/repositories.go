package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	a "github.com/korallis/letmecook/internal/authority"
	i "github.com/korallis/letmecook/internal/identity"
	r "github.com/korallis/letmecook/internal/repositories"
)

const repositorySchema = `
CREATE TABLE repositories (
 id TEXT PRIMARY KEY, remote TEXT NOT NULL UNIQUE
) STRICT;
CREATE TABLE repository_profiles (
 repository_id TEXT NOT NULL REFERENCES repositories(id),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 body TEXT NOT NULL CHECK(length(CAST(body AS BLOB))<=65536),
 digest TEXT NOT NULL CHECK(length(digest)=64),
 actor TEXT NOT NULL REFERENCES principals(id),
 validated_ms INTEGER NOT NULL CHECK(validated_ms BETWEEN 1 AND 9007199254740991),
 PRIMARY KEY(repository_id,revision)
) STRICT;
CREATE TRIGGER repositories_no_update BEFORE UPDATE ON repositories BEGIN SELECT RAISE(ABORT,'repository identities are immutable'); END;
CREATE TRIGGER repositories_no_delete BEFORE DELETE ON repositories BEGIN SELECT RAISE(ABORT,'repository identities are immutable'); END;
CREATE TRIGGER repository_profiles_no_update BEFORE UPDATE ON repository_profiles BEGIN SELECT RAISE(ABORT,'repository profiles are immutable'); END;
CREATE TRIGGER repository_profiles_no_delete BEFORE DELETE ON repository_profiles BEGIN SELECT RAISE(ABORT,'repository profiles are immutable'); END;
PRAGMA user_version=5;
`

func (s *Store) repositoryOwner(ctx context.Context, actor string) error {
	return s.identityWrite(ctx, func(tx *sql.Tx) error { return owner(ctx, tx, actor) })
}

// ValidateRepository is a trusted in-process entry point for a future authenticated
// CLI/API. actor must come from identity.Fingerprint after proof of possession,
// never from a request field. It is checked before any network/filesystem work.
// Validation alone is not enrollment; RegisterRepository repeats the access check.
func (s *Store) ValidateRepository(ctx context.Context, actor string, profile r.Profile) (r.Validation, error) {
	if err := s.repositoryOwner(ctx, actor); err != nil {
		return r.Validation{}, err
	}
	return r.ValidateAccess(ctx, profile)
}

// RegisterRepository stores only after reading the exact selected remote/base.
// Owner identity and runner enrollment are rechecked at commit. expectedRevision
// is CAS (zero for a new identity). No grant, task, runner enablement or external
// effect is created. Revocation cannot race a pre-validation authentication check.
func (s *Store) RegisterRepository(ctx context.Context, actor string, expectedRevision int64, profile r.Profile) (r.Profile, error) {
	if expectedRevision < 0 {
		return r.Profile{}, r.Invalid
	}
	validation, err := s.ValidateRepository(ctx, actor, profile)
	if err != nil {
		return r.Profile{}, err
	}
	err = s.identityWrite(ctx, func(tx *sql.Tx) error {
		if err := owner(ctx, tx, actor); err != nil {
			return err
		}
		who, err := principal(ctx, tx, actor)
		if err != nil {
			return err
		}
		old, err := repositoryProfile(ctx, tx, profile.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if old.ID != "" && old.Remote != profile.Remote {
			return i.Conflict
		}
		for _, root := range profile.RunnerRoots {
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM principals WHERE id=? AND role='runner' AND revoked=0", root.RunnerID).Scan(&n); err != nil {
				return err
			}
			if n != 1 {
				return i.Denied
			}
		}
		if old.Revision == profile.Revision && reflect.DeepEqual(old, profile) {
			if expectedRevision != old.Revision && expectedRevision != old.Revision-1 {
				return i.Conflict
			}
			return nil
		}
		if old.Revision != expectedRevision || profile.Revision != expectedRevision+1 {
			return i.Conflict
		}
		if old.ID == "" {
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM repositories WHERE remote=?", profile.Remote).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				return i.Conflict
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO repositories VALUES(?,?)", profile.ID, profile.Remote); err != nil {
				return err
			}
		}
		body, err := json.Marshal(profile)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO repository_profiles VALUES(?,?,?,?,?,?)", profile.ID, profile.Revision, string(body), validation.ProfileDigest, who.ID, time.Now().UnixMilli())
		return err
	})
	if err != nil {
		return r.Profile{}, err
	}
	return profile, nil
}

func repositoryProfile(ctx context.Context, tx *sql.Tx, id string) (r.Profile, error) {
	if !a.ValidActor(id) {
		return r.Profile{}, i.Invalid
	}
	var profile r.Profile
	var body, digest, remote string
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT p.body,p.digest,r.remote,p.revision FROM repository_profiles p JOIN repositories r ON r.id=p.repository_id WHERE r.id=? ORDER BY p.revision DESC LIMIT 1`, id).Scan(&body, &digest, &remote, &revision)
	if err != nil {
		return profile, err
	}
	if len(body) > r.MaxBytes || json.Unmarshal([]byte(body), &profile) != nil {
		return r.Profile{}, i.Unavailable
	}
	actual, err := profile.Digest()
	if err != nil || actual != digest || profile.ID != id || profile.Remote != remote || profile.Revision != revision {
		return r.Profile{}, i.Unavailable
	}
	return profile, nil
}

// RepositoryProfile returns current approved inputs to a proven owner. It does
// not renew read access or establish current remote/ref freshness.
func (s *Store) RepositoryProfile(ctx context.Context, actor, id string) (r.Profile, error) {
	var profile r.Profile
	err := s.identityWrite(ctx, func(tx *sql.Tx) error {
		if err := owner(ctx, tx, actor); err != nil {
			return err
		}
		var err error
		profile, err = repositoryProfile(ctx, tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return i.Conflict
		}
		return err
	})
	if err != nil {
		return r.Profile{}, err
	}
	return profile, nil
}

// CheckRepositorySelection checks the current profile and enabled runner identity,
// not runner-local permission, OS confinement, grant authority or dispatch.
// Dispatch rechecks placement in its admission transaction; see ../scheduler/README.md.
func (s *Store) CheckRepositorySelection(ctx context.Context, selection r.Selection) error {
	return s.identityWrite(ctx, func(tx *sql.Tx) error {
		profile, err := repositoryProfile(ctx, tx, selection.Repository)
		if errors.Is(err, sql.ErrNoRows) {
			return i.Denied
		}
		if err != nil {
			return err
		}
		if profile.Select(selection) != nil {
			return i.Denied
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM principals WHERE id=? AND role='runner' AND enabled=1 AND revoked=0", selection.RunnerRoot.RunnerID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return i.Denied
		}
		return nil
	})
}
