package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	i "github.com/korallis/letmecook/internal/identity"
	p "github.com/korallis/letmecook/schemas/execution"
)

const identitySchema = `
CREATE TABLE principals (
 id TEXT PRIMARY KEY, role TEXT NOT NULL CHECK(role IN ('owner','runner')),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), revoked INTEGER NOT NULL CHECK(revoked IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991)
) STRICT;
CREATE UNIQUE INDEX one_owner ON principals(role) WHERE role='owner';
CREATE TABLE credentials (
 fingerprint TEXT PRIMARY KEY CHECK(length(fingerprint)=64),
 principal_id TEXT NOT NULL REFERENCES principals(id), revoked INTEGER NOT NULL CHECK(revoked IN (0,1))
) STRICT;
CREATE TABLE enrollments (
 id TEXT PRIMARY KEY, runner_id TEXT NOT NULL UNIQUE, fingerprint TEXT NOT NULL UNIQUE CHECK(length(fingerprint)=64),
 token_hash TEXT NOT NULL UNIQUE CHECK(length(token_hash)=64), expires INTEGER NOT NULL,
 consumed INTEGER NOT NULL CHECK(consumed IN (0,1))
) STRICT;
PRAGMA user_version=3;
`

// identityWrite serializes authentication with mutation/revocation, in one durable
// transaction. No raw driver error or supplied value escapes this boundary.
func (s *Store) identityWrite(ctx context.Context, fn func(*sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil || s.fixture {
		return i.Unavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return i.Unavailable
	}
	defer tx.Rollback()
	if err = fn(tx); err == nil {
		err = tx.Commit()
	}
	if err == nil || errors.Is(err, i.Denied) || errors.Is(err, i.Invalid) || errors.Is(err, i.Conflict) {
		return err
	}
	return i.Unavailable
}

func principal(ctx context.Context, tx *sql.Tx, fingerprint string) (i.Principal, error) {
	v := i.Principal{Version: i.Version}
	if !i.ValidFingerprint(fingerprint) {
		return v, i.Denied
	}
	err := tx.QueryRowContext(ctx, `SELECT p.id,p.role,p.enabled,p.revoked,p.revision FROM principals p JOIN credentials c ON c.principal_id=p.id WHERE c.fingerprint=? AND c.revoked=0 AND p.revoked=0`, fingerprint).Scan(&v.ID, &v.Role, &v.Enabled, &v.Revoked, &v.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = i.Denied
	}
	return v, err
}
func owner(ctx context.Context, tx *sql.Tx, fingerprint string) error {
	v, err := principal(ctx, tx, fingerprint)
	if err != nil {
		return err
	}
	if v.Role != "owner" {
		return i.Denied
	}
	return nil
}

// BootstrapOwner is local-only, under the store's exclusive directory lock.
// Recovery is explicit and fences every existing credential and invite; it is
// never exposed by HTTP and does not imply execution recovery or process death.
func (s *Store) BootstrapOwner(ctx context.Context, fingerprint string, recover bool) error {
	if !i.ValidFingerprint(fingerprint) {
		return i.Invalid
	}
	return s.identityWrite(ctx, func(tx *sql.Tx) error {
		var id string
		err := tx.QueryRowContext(ctx, "SELECT id FROM principals WHERE role='owner'").Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			if recover {
				return i.Conflict
			}
			id = newID()
			if _, err = tx.ExecContext(ctx, "INSERT INTO principals VALUES(?,'owner',1,0,1)", id); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if !recover {
				return i.Conflict
			}
			if _, err = tx.ExecContext(ctx, "UPDATE credentials SET revoked=1; UPDATE enrollments SET consumed=1; UPDATE principals SET revoked=1,enabled=0,revision=revision+1"); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE principals SET revoked=0,enabled=1 WHERE id=?", id); err != nil {
				return err
			}
		}
		var reserved int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM enrollments WHERE fingerprint=?", fingerprint).Scan(&reserved); err != nil {
			return err
		}
		if reserved != 0 {
			return i.Conflict
		}
		return addCredential(ctx, tx, id, fingerprint)
	})
}

func addCredential(ctx context.Context, tx *sql.Tx, id, fingerprint string) error {
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM credentials WHERE fingerprint=?", fingerprint).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return i.Conflict
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO credentials VALUES(?,?,0)", fingerprint, id)
	return err
}

func (s *Store) IdentityConfigured(ctx context.Context) (bool, error) {
	configured := false
	err := s.identityWrite(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM principals WHERE role='owner')").Scan(&configured)
	})
	return configured, err
}
func (s *Store) Authenticate(ctx context.Context, fingerprint string) (i.Principal, error) {
	var v i.Principal
	err := s.identityWrite(ctx, func(tx *sql.Tx) error {
		var err error
		v, err = principal(ctx, tx, fingerprint)
		return err
	})
	if err != nil {
		return i.Principal{}, err
	}
	return v, nil
}

type Enrollment struct {
	Version  string  `json:"version"`
	ID       string  `json:"id"`
	RunnerID string  `json:"runner_id"`
	Token    i.Token `json:"token"`
	Expires  int64   `json:"expires"`
}

// ponytail: retained enrollment tombstones stay unbounded; add explicit retention
// only with a durable replay/identity retirement contract, never a TTL deletion.
// CreateEnrollment pins a selected machine's public credential before issuing a
// ten-minute, one-use secret. Lost responses require a fresh certificate/invite;
// no secret is persisted or replayed. IDs and pins remain tombstones.
func (s *Store) CreateEnrollment(ctx context.Context, actor, id, fingerprint string) (Enrollment, error) {
	if !p.ValidID(id) || !i.ValidFingerprint(fingerprint) {
		return Enrollment{}, i.Invalid
	}
	v := Enrollment{Version: i.Version, ID: id, RunnerID: newID(), Token: i.NewToken(), Expires: time.Now().Add(10 * time.Minute).Unix()}
	hash, _ := i.TokenHash(v.Token)
	err := s.identityWrite(ctx, func(tx *sql.Tx) error {
		if err := owner(ctx, tx, actor); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM enrollments WHERE id=? OR fingerprint=?) + (SELECT count(*) FROM credentials WHERE fingerprint=?)`, id, fingerprint, fingerprint).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return i.Conflict
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO enrollments VALUES(?,?,?,?,?,0)", id, v.RunnerID, fingerprint, hash, v.Expires)
		return err
	})
	if err != nil {
		return Enrollment{}, err
	}
	return v, nil
}

func (s *Store) Enroll(ctx context.Context, fingerprint string, token i.Token) (i.Principal, error) {
	hash, err := i.TokenHash(token)
	if err != nil || !i.ValidFingerprint(fingerprint) {
		return i.Principal{}, i.Denied
	}
	v := i.Principal{Version: i.Version, Role: "runner", Revision: 1}
	err = s.identityWrite(ctx, func(tx *sql.Tx) error {
		var id string
		err := tx.QueryRowContext(ctx, "SELECT id,runner_id FROM enrollments WHERE fingerprint=? AND token_hash=? AND consumed=0 AND expires>?", fingerprint, hash, time.Now().Unix()).Scan(&id, &v.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return i.Denied
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE enrollments SET consumed=1 WHERE id=?", id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO principals VALUES(?,'runner',0,0,1)", v.ID); err != nil {
			return err
		}
		return addCredential(ctx, tx, v.ID, fingerprint)
	})
	if err != nil {
		return i.Principal{}, err
	}
	return v, nil
}

// UpdateIdentity requires an owner and a revision CAS; runner enablement is not
// eligibility, grants, lease admission or permission to execute anything.
func (s *Store) UpdateIdentity(ctx context.Context, actor, id string, revision int64, action, fingerprint string) (i.Principal, error) {
	if !p.ValidID(id) || revision < 1 || revision >= p.MaxInteger {
		return i.Principal{}, i.Invalid
	}
	if action != "enable" && action != "disable" && action != "revoke" && action != "rotate" {
		return i.Principal{}, i.Invalid
	}
	if action == "rotate" && !i.ValidFingerprint(fingerprint) || action != "rotate" && fingerprint != "" {
		return i.Principal{}, i.Invalid
	}
	v := i.Principal{Version: i.Version, ID: id}
	err := s.identityWrite(ctx, func(tx *sql.Tx) error {
		if err := owner(ctx, tx, actor); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, "SELECT role,enabled,revoked,revision FROM principals WHERE id=?", id).Scan(&v.Role, &v.Enabled, &v.Revoked, &v.Revision)
		if errors.Is(err, sql.ErrNoRows) {
			return i.Conflict
		}
		if err != nil {
			return err
		}
		if v.Revision != revision || v.Revoked {
			return i.Conflict
		}
		if action == "enable" || action == "disable" {
			if v.Role != "runner" {
				return i.Invalid
			}
			v.Enabled = action == "enable"
		} else {
			if _, err = tx.ExecContext(ctx, "UPDATE credentials SET revoked=1 WHERE principal_id=?", id); err != nil {
				return err
			}
			if action == "rotate" {
				// A reserved enrollment pin cannot be moved onto another identity.
				var n int
				if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM enrollments WHERE fingerprint=?", fingerprint).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					return i.Conflict
				}
				if err = addCredential(ctx, tx, id, fingerprint); err != nil {
					return err
				}
			} else {
				v.Revoked, v.Enabled = true, false
			}
		}
		v.Revision++
		_, err = tx.ExecContext(ctx, "UPDATE principals SET enabled=?,revoked=?,revision=? WHERE id=?", v.Enabled, v.Revoked, v.Revision, id)
		return err
	})
	if err != nil {
		return i.Principal{}, err
	}
	return v, nil
}
