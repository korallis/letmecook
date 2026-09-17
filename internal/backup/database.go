package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strings"

	"github.com/korallis/letmecook/internal/closedjson"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
	a "github.com/korallis/letmecook/schemas/readapi"
)

func openDatabase(path string, readonly bool) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {"rw"}, "_pragma": {"foreign_keys(1)", "trusted_schema(0)", "synchronous(FULL)", "fullfsync(1)"}}
	if readonly {
		q.Set("mode", "ro")
		q.Set("immutable", "1")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
func integrity(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return err
	}
	var results int
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		if s != "ok" {
			rows.Close()
			return errors.New("database_integrity")
		}
		results++
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if results != 1 {
		return errors.New("database_integrity")
	}
	rows, err = db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("database_foreign_keys")
	}
	return rows.Err()
}

// Secret-bearing columns are an explicit allowlist, not a scan of arbitrary
// candidate/output text. Only the COPY is changed; trigger definitions are
// restored exactly and VACUUM removes the deleted values from free pages.
const omittedCredentialResponse = `{"error":"credential_response_omitted","detail":"create a new enrollment after restoring"}`

// Match both current path spellings and the planned semantic identity route.
// The content rule also covers future names and nested response envelopes.
func credentialReceipt(route, response string) (bool, error) {
	switch route {
	case "/api/v1/identity/enrollments", "POST /api/v1/identity/enrollments", "identity.enrollments":
		return true, nil
	}
	if response == "" {
		return false, nil
	}
	d := json.NewDecoder(strings.NewReader(response))
	d.UseNumber()
	// Token traversal observes duplicate keys too; ordinary Unmarshal would
	// discard an earlier non-empty token if a later duplicate were empty.
	var visit func(int) (json.Token, bool, error)
	visit = func(depth int) (json.Token, bool, error) {
		if depth > 64 {
			return nil, false, errors.New("invalid_owner_response")
		}
		token, err := d.Token()
		if err != nil {
			return nil, false, err
		}
		found := false
		switch token {
		case json.Delim('{'):
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, false, err
				}
				value, nested, err := visit(depth + 1)
				if err != nil {
					return nil, false, err
				}
				secret, ok := value.(string)
				found = found || nested || (key == "token" && ok && secret != "")
			}
			if end, err := d.Token(); err != nil || end != json.Delim('}') {
				return nil, false, errors.New("invalid_owner_response")
			}
		case json.Delim('['):
			for d.More() {
				_, nested, err := visit(depth + 1)
				if err != nil {
					return nil, false, err
				}
				found = found || nested
			}
			if end, err := d.Token(); err != nil || end != json.Delim(']') {
				return nil, false, errors.New("invalid_owner_response")
			}
		}
		return token, found, nil
	}
	_, found, err := visit(0)
	if err != nil {
		return false, errors.New("invalid_owner_response")
	}
	if _, err = d.Token(); err != io.EOF {
		return false, errors.New("invalid_owner_response")
	}
	return found, nil
}

// Collect before writing so one SQLite connection never has an active rows
// cursor while mutating receipts. The same predicate guards Verify.
func secretReceiptIDs(ctx context.Context, reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]string, error) {
	rows, err := reader.QueryContext(ctx, "SELECT message_id,route,status,response FROM owner_commands")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id, route, response string
		var status int
		if err = rows.Scan(&id, &route, &status, &response); err != nil {
			return nil, err
		}
		match, e := credentialReceipt(route, response)
		if e != nil {
			return nil, e
		}
		if match && (status != 410 || response != omittedCredentialResponse) {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

func sanitizeSnapshot(ctx context.Context, path string) (err error) {
	db, err := openDatabase(path, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var profiles int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM gateway_profiles").Scan(&profiles); err != nil {
		return err
	}
	ids, err := secretReceiptIDs(ctx, tx)
	if err != nil {
		return err
	}
	var profileArgs, receiptArgs [][]any
	if profiles > 0 {
		profileArgs = [][]any{nil}
	}
	for _, id := range ids {
		receiptArgs = append(receiptArgs, []any{omittedCredentialResponse, id})
	}
	// Explicit table/column allowlist. Preserve receipt identity/request hashes;
	// a 410 tombstone cannot masquerade as a successful credential replay.
	rules := []struct {
		trigger, mutation string
		args              [][]any
	}{
		{"gateway_profiles_no_delete", "DELETE FROM gateway_profiles", profileArgs},
		{"owner_commands_no_update", "UPDATE owner_commands SET status=410,response=? WHERE message_id=?", receiptArgs},
	}
	for _, rule := range rules {
		if len(rule.args) == 0 {
			continue
		}
		var trigger string
		if err = tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE type='trigger' AND name=?", rule.trigger).Scan(&trigger); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DROP TRIGGER "+rule.trigger); err != nil {
			return err
		}
		for _, args := range rule.args {
			if _, err = tx.ExecContext(ctx, rule.mutation, args...); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, trigger); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if profiles > 0 || len(ids) > 0 {
		_, err = db.ExecContext(ctx, "VACUUM")
	}
	return err
}

// Read expected inventory from the snapshot, not from a live filesystem walk.
// verify=true compares identity, attempt states and full referential integrity.
func databaseInventory(ctx context.Context, path string, m *Manifest, verify bool) ([]Entry, error) {
	db, err := openDatabase(path, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var schema, application int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema); err != nil {
		return nil, err
	}
	if err = db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&application); err != nil {
		return nil, err
	}
	if schema != a.SchemaVersion || application != 0x47414646 {
		return nil, errors.New("incompatible_database")
	}
	if err = integrity(ctx, db); err != nil {
		return nil, err
	}
	var generation, boot string
	var paused bool
	if err = db.QueryRowContext(ctx, "SELECT generation,daemon_boot,paused FROM metadata,daemon_state WHERE metadata.singleton=1 AND daemon_state.singleton=1").Scan(&generation, &boot, &paused); err != nil {
		return nil, err
	}
	if !p.ValidID(generation) || !p.ValidID(boot) || !paused {
		return nil, errors.New("invalid_snapshot_state")
	}
	if verify && (m.Generation != generation || m.DaemonBoot != boot) {
		return nil, errors.New("snapshot_identity_mismatch")
	}
	m.Generation, m.DaemonBoot = generation, boot
	states := map[string]p.AttemptState{}
	rows, err := db.QueryContext(ctx, "SELECT id,state FROM attempts ORDER BY id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var state p.AttemptState
		if err = rows.Scan(&id, &state); err != nil {
			rows.Close()
			return nil, err
		}
		if !p.ValidID(id) || !validState(state) {
			rows.Close()
			return nil, errors.New("invalid_attempt")
		}
		states[id] = state
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if verify && !reflect.DeepEqual(m.AttemptStates, states) {
		return nil, errors.New("snapshot_attempt_mismatch")
	}
	m.AttemptStates = states
	// Manifests themselves are immutable custody evidence, including quarantine;
	// every one and every blob it references must be present, even after release.
	refs := []Entry{}
	type link struct {
		digest, role string
		bytes        int64
	}
	links := map[string]link{}
	rows, err = db.QueryContext(ctx, "SELECT manifest_id,sha256,bytes,body FROM artifact_manifests ORDER BY manifest_id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, sha string
		var n int64
		var body []byte
		if err = rows.Scan(&id, &sha, &n, &body); err != nil {
			rows.Close()
			return nil, err
		}
		hash := sha256.Sum256(body)
		if !p.ValidID(id) || int64(len(body)) != n || hex.EncodeToString(hash[:]) != sha {
			rows.Close()
			return nil, errors.New("invalid_artifact_manifest")
		}
		var candidate store.CandidateManifest
		if closedjson.Decode(body, &candidate, 1<<20, nil) != nil || candidate.Version != "gaffer-artifact-manifest-v1" {
			rows.Close()
			return nil, errors.New("invalid_artifact_manifest")
		}
		canonical, e := json.Marshal(candidate)
		if e != nil || string(canonical) != string(body) {
			rows.Close()
			return nil, errors.New("invalid_artifact_manifest")
		}
		for role, group := range map[string][]store.ArtifactBlob{"tracked": candidate.Tracked, "untracked": candidate.Untracked, "binary": candidate.Binary, "recovery": candidate.Recovery} {
			for _, blob := range group {
				key := id + "\x00" + blob.Path
				if _, exists := links[key]; exists || !digest(blob.SHA256) || blob.Bytes < 0 {
					rows.Close()
					return nil, errors.New("invalid_artifact_manifest")
				}
				links[key] = link{blob.SHA256, role, blob.Bytes}
			}
		}
		refs = append(refs, Entry{"manifest", "artifacts/manifests/" + id + ".json", sha, n})
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	rows, err = db.QueryContext(ctx, `SELECT r.manifest_id,r.path,r.digest,r.role,r.bytes,b.bytes FROM artifact_manifest_blobs r JOIN artifact_blobs b ON b.digest=r.digest`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, path, sha, role string
		var n, storedBytes int64
		if err = rows.Scan(&id, &path, &sha, &role, &n, &storedBytes); err != nil {
			rows.Close()
			return nil, err
		}
		key := id + "\x00" + path
		if want, ok := links[key]; !ok || want != (link{sha, role, n}) || n != storedBytes {
			rows.Close()
			return nil, errors.New("artifact_reference_mismatch")
		}
		delete(links, key)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if len(links) != 0 {
		return nil, errors.New("artifact_reference_missing")
	}
	rows, err = db.QueryContext(ctx, `SELECT DISTINCT b.digest,b.bytes FROM artifact_blobs b JOIN artifact_manifest_blobs r ON r.digest=b.digest ORDER BY b.digest`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sha string
		var n int64
		if err = rows.Scan(&sha, &n); err != nil {
			rows.Close()
			return nil, err
		}
		if !digest(sha) || n < 0 {
			rows.Close()
			return nil, errors.New("invalid_blob")
		}
		refs = append(refs, Entry{"blob", "artifacts/blobs/" + sha[:2] + "/" + sha, sha, n})
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	var profiles int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM gateway_profiles").Scan(&profiles); err != nil {
		return nil, err
	}
	if profiles != 0 {
		return nil, fmt.Errorf("gateway_configuration_in_backup")
	}
	credentialReceipts, err := secretReceiptIDs(ctx, db)
	if err != nil {
		return nil, err
	}
	if len(credentialReceipts) > 0 {
		return nil, errors.New("credential_response_in_backup")
	}
	return refs, nil
}
