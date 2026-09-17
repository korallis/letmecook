package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	a "github.com/korallis/letmecook/schemas/readapi"
)

// dropSchema10 returns a schema-10 store to its exact schema-9 shape, including
// the narrower artifact_blobs check, so migration fixtures replay the real step.
const dropSchema10 = `DROP INDEX attempts_active; DROP TABLE restore_history; DROP TABLE reconcile_reports; DROP TABLE attempt_usage; DROP TABLE artifact_result_heads; DROP TABLE upload_blobs; DROP TABLE upload_sessions; DROP TABLE runtime_observations; DROP TABLE runner_sessions; DROP TABLE gateway_profiles; DROP TABLE daemon_state; DROP TABLE jobs; DROP TABLE owner_commands; DROP TABLE execution_decisions; DROP TABLE task_briefs;
CREATE TABLE artifact_blobs_prev (
 digest TEXT PRIMARY KEY CHECK(length(digest)=64), bytes INTEGER NOT NULL CHECK(bytes BETWEEN 1 AND 1073741824), created_ms INTEGER NOT NULL,
 retained_until_ms INTEGER NOT NULL, state TEXT NOT NULL CHECK(state IN ('committed','quarantined'))
) STRICT;
INSERT INTO artifact_blobs_prev SELECT * FROM artifact_blobs;
DROP TABLE artifact_blobs;
ALTER TABLE artifact_blobs_prev RENAME TO artifact_blobs;
CREATE TRIGGER artifact_blobs_immutable BEFORE UPDATE ON artifact_blobs BEGIN SELECT RAISE(ABORT,'artifact blob immutable'); END;`

func TestUpgradeChainIsOrderedAndComplete(t *testing.T) {
	if len(upgrades) == 0 || upgrades[0].from != 1 || upgrades[len(upgrades)-1].from+1 != a.SchemaVersion {
		t.Fatalf("chain must run 1..%d: %+v", a.SchemaVersion, upgrades)
	}
	for j, step := range upgrades {
		if step.from != j+1 || !strings.Contains(step.sql, "PRAGMA user_version="+strconv.Itoa(step.from+1)+";") {
			t.Fatalf("step %d: from=%d must set user_version=%d", j, step.from, step.from+1)
		}
	}
}

func TestSchemaTenMigration(t *testing.T) {
	s, artifacts := persistent(t)
	request, _ := custodyFixture(t, s)
	received, err := s.CustodyResult(context.Background(), request)
	if err != nil || received.Quarantined {
		t.Fatal(received, err)
	}
	generation := s.meta.Generation
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(s.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(dropSchema10 + "PRAGMA user_version=9"); err != nil {
		t.Fatal(err)
	}
	// The schema-9 shape really refuses zero-byte blobs; the migration must widen it.
	if _, err = db.Exec("INSERT INTO artifact_blobs VALUES(?,0,1,1,'committed')", strings.Repeat("0", 64)); err == nil {
		t.Fatal("schema-9 fixture accepted a zero-byte blob")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.meta.SchemaVersion != 10 || reopened.meta.Generation != generation {
		t.Fatal(reopened.meta)
	}
	consistent(t, reopened)
	for table, want := range map[string]int{"artifact_results": 1, "artifact_blobs": 4, "artifact_manifest_blobs": 4, "artifact_manifests": 1, "attempts": 1, "daemon_state WHERE paused=0 AND singleton=1": 1, "sqlite_schema WHERE type='index' AND name='attempts_active'": 1} {
		rowCount(t, reopened, table, want)
	}
	// Custody rows and the receipt survive: a lost-ack replay returns the same receipt.
	retry := request
	retry.Sources = nil
	replayed, err := reopened.CustodyResult(context.Background(), retry)
	if err != nil || replayed.Receipt.ReceiptID != received.Receipt.ReceiptID || controlJSON(replayed.Ack) != controlJSON(received.Ack) {
		t.Fatal(replayed, err)
	}
	var ddl string
	if err = reopened.db.QueryRow("SELECT sql FROM sqlite_schema WHERE name='artifact_manifest_blobs'").Scan(&ddl); err != nil || !strings.Contains(ddl, "REFERENCES artifact_blobs(") {
		t.Fatal("child foreign key lost its parent", ddl, err)
	}
	empty := strings.Repeat("0", 64)
	if _, err = reopened.db.Exec("INSERT INTO artifact_blobs VALUES(?,0,1,1,'committed')", empty); err != nil {
		t.Fatal("zero-byte blob refused after migration", err)
	}
	if _, err = reopened.db.Exec("UPDATE artifact_blobs SET bytes=1 WHERE digest=?", empty); err == nil {
		t.Fatal("blob immutability trigger not recreated")
	}
	for _, query := range []string{
		"DELETE FROM daemon_state",
		"UPDATE daemon_state SET paused=2",
		"INSERT INTO runner_sessions VALUES('s','missing-principal','b','d','g','normal','{}',1)",
		"INSERT INTO runner_sessions VALUES('s','missing-principal','b','d','g','elevated','{}',1)",
		"INSERT INTO upload_blobs VALUES('missing-session','" + empty + "',1,'missing')",
		"INSERT INTO jobs VALUES('j','kind','s','done','b',1,0,0,'','')",
		"INSERT INTO artifact_result_heads VALUES('missing-task','g','missing-attempt',1,'missing-manifest','missing-receipt',1)",
	} {
		if _, err := reopened.db.Exec(query); err == nil {
			t.Fatal("constraint missing", query)
		}
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	// Migrating twice is not possible: a store already at 10 reopens unchanged, and
	// a future schema is refused rather than partially interpreted.
	again, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if again.meta.SchemaVersion != 10 {
		t.Fatal(again.meta)
	}
	rowCount(t, again, "artifact_blobs", 5)
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", filepath.Join(s.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=11"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if future, err := Open(ctx, s.dir, artifacts); err == nil {
		future.Close()
		t.Fatal("schema 11 accepted")
	}
}
