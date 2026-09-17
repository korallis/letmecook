package backup

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupExcludesEnrollmentReceiptSecrets(t *testing.T) {
	f := testFixture(t, true)
	owner := uuid()
	execute(t, f.db, "INSERT INTO principals VALUES(?,'owner',1,0,1)", owner)
	secret := "enrollment-secret-never-backup-" + uuid()
	response := string(rawJSON(t, map[string]any{"envelope": []any{map[string]string{"token": secret}}}))
	var originalTrigger string
	must(t, f.db.QueryRow("SELECT sql FROM sqlite_schema WHERE name='owner_commands_no_update'").Scan(&originalTrigger))
	// No gateway profile exists: receipt-only secrets must still trigger sanitation.
	for _, route := range []string{"POST /api/v1/identity/enrollments", "/api/v1/identity/enrollments", "identity.enrollments", "future.credential.route"} {
		execute(t, f.db, "INSERT INTO owner_commands VALUES(?,?,?,?,?,?,?,?)", uuid(), f.identity.Generation, owner, route, strings.Repeat("a", 64), 200, response, time.Now().UnixMilli())
	}
	ordinary := uuid()
	execute(t, f.db, "INSERT INTO owner_commands VALUES(?,?,?,?,?,?,?,?)", ordinary, f.identity.Generation, owner, "POST /api/v1/tasks", strings.Repeat("b", 64), 201, `{"task_id":"retained"}`, time.Now().UnixMilli())
	out := f.backup(t)
	_, err := Verify(out)
	must(t, err)
	must(t, filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && bytes.Contains(read(t, path), []byte(secret)) {
			t.Fatal("raw invite token in backup", path)
		}
		return nil
	}))
	var count int
	must(t, f.db.QueryRow("SELECT count(*) FROM owner_commands WHERE status=200 AND response=?", response).Scan(&count))
	if count != 4 {
		t.Fatal("source receipts changed", count)
	}
	db, err := openDatabase(filepath.Join(out, "state.db"), false)
	must(t, err)
	defer db.Close()
	must(t, db.QueryRow("SELECT count(*) FROM owner_commands WHERE status=410 AND response=?", omittedCredentialResponse).Scan(&count))
	if count != 4 {
		t.Fatal("credential tombstones missing", count)
	}
	rows, err := f.db.Query("SELECT message_id,generation,principal_id,route,request_sha256,created_ms FROM owner_commands")
	must(t, err)
	for rows.Next() {
		var id, generation, principal, route, hash string
		var created int64
		must(t, rows.Scan(&id, &generation, &principal, &route, &hash, &created))
		var same int
		must(t, db.QueryRow("SELECT count(*) FROM owner_commands WHERE message_id=? AND generation=? AND principal_id=? AND route=? AND request_sha256=? AND created_ms=?", id, generation, principal, route, hash, created).Scan(&same))
		if same != 1 {
			t.Fatal("receipt identity or request hash changed", id)
		}
	}
	must(t, rows.Err())
	must(t, rows.Close())
	var kept, trigger string
	must(t, db.QueryRow("SELECT response FROM owner_commands WHERE message_id=?", ordinary).Scan(&kept))
	if kept != `{"task_id":"retained"}` {
		t.Fatal("ordinary receipt changed", kept)
	}
	must(t, db.QueryRow("SELECT sql FROM sqlite_schema WHERE name='owner_commands_no_update'").Scan(&trigger))
	if trigger != originalTrigger {
		t.Fatal("immutability trigger changed")
	}
	if _, err = db.Exec("UPDATE owner_commands SET response='{}'"); err == nil || !strings.Contains(err.Error(), "owner command receipt is immutable") {
		t.Fatal("receipt immutability lost", err)
	}
	must(t, db.Close())
	_, err = Restore(ctx, out, filepath.Join(f.root, "redacted-state"), filepath.Join(f.root, "redacted-artifacts"))
	must(t, err)
}

func TestVerifyRejectsCredentialReceiptEvenWithMatchingDatabaseHash(t *testing.T) {
	f := testFixture(t, true)
	out := f.backup(t)
	m, err := Verify(out)
	must(t, err)
	db, err := openDatabase(filepath.Join(out, "state.db"), false)
	must(t, err)
	owner := uuid()
	execute(t, db, "INSERT INTO principals VALUES(?,'owner',1,0,1)", owner)
	execute(t, db, "INSERT INTO owner_commands VALUES(?,?,?,?,?,?,?,?)", uuid(), m.Generation, owner, "unanticipated.route", strings.Repeat("a", 64), 200, `{"nested":[{"token":"leaked-invite"}]}`, time.Now().UnixMilli())
	must(t, db.Close())
	raw := read(t, filepath.Join(out, "state.db"))
	m.Database = Blob{SHA256: sum(raw), Bytes: int64(len(raw))}
	must(t, os.WriteFile(filepath.Join(out, manifestName), rawJSON(t, m), 0600))
	if _, err = Verify(out); err == nil || err.Error() != "credential_response_in_backup" {
		t.Fatal("secret-bearing receipt verified", err)
	}
}

func TestCredentialReceiptSelectors(t *testing.T) {
	for _, tt := range []struct {
		route, response string
		want            bool
	}{
		{"identity.enrollments", `{}`, true},
		{"/api/v1/identity/enrollments", `{}`, true},
		{"POST /api/v1/identity/enrollments", `{}`, true},
		{"future.route", `{"nested":[{"token":"secret"}]}`, true},
		{"future.route", `{"to\u006ben":"secret"}`, true},
		{"future.route", `{"token":"secret","token":""}`, true},
		{"task.create", `{"token":null}`, false},
		{"task.create", `{"token":""}`, false},
		{"task.create", `{"token":42}`, false},
		{"task.create", `{"text":"token"}`, false},
		{"task.create", omittedCredentialResponse, false},
	} {
		t.Run(tt.route+tt.response, func(t *testing.T) {
			got, err := credentialReceipt(tt.route, tt.response)
			must(t, err)
			if got != tt.want {
				t.Fatal(got, tt.want)
			}
		})
	}
	if _, err := credentialReceipt("task.create", `{"token":`); err == nil {
		t.Fatal("malformed receipt accepted")
	}
}
