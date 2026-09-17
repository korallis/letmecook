package store

// Schema 10 is the single M1 end-to-end migration (docs/decisions/0002 §9). It
// adds the workflow, execution-channel, reconcile and backup tables that later
// slices fill, seeds the paused flag, indexes active attempts and rebuilds
// artifact_blobs so zero-byte candidate files are storable.
//
// The artifact_blobs rebuild keeps artifact_manifest_blobs' foreign key intact
// without dropping that child table. SQLite counts deferred foreign-key
// violations rather than re-checking them at COMMIT, so dropping the old parent
// table increments the counter once per child row and renaming the rebuilt table
// into place never decrements it. Re-inserting the retained rows into the renamed
// parent balances the counter; the migration test asserts PRAGMA foreign_key_check
// is empty afterwards. defer_foreign_keys resets itself at COMMIT.
const schema10 = `
PRAGMA defer_foreign_keys=ON;
CREATE TABLE task_briefs (
 task_id TEXT PRIMARY KEY REFERENCES tasks(id),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 repository_id TEXT NOT NULL REFERENCES repositories(id),
 base_commit TEXT NOT NULL CHECK(length(base_commit) IN (40,64)),
 brief TEXT NOT NULL CHECK(length(CAST(brief AS BLOB))<=65536),
 criteria TEXT NOT NULL CHECK(length(CAST(criteria AS BLOB))<=65536),
 paths TEXT NOT NULL CHECK(length(CAST(paths AS BLOB))<=65536),
 operations TEXT NOT NULL CHECK(length(operations)<=256),
 brief_sha256 TEXT NOT NULL CHECK(length(brief_sha256)=64),
 plan_sha256 TEXT NOT NULL CHECK(length(plan_sha256)=64),
 actor TEXT NOT NULL REFERENCES principals(id),
 created_ms INTEGER NOT NULL CHECK(created_ms BETWEEN 1 AND 9007199254740991)
) STRICT;
CREATE TRIGGER task_briefs_identity BEFORE UPDATE ON task_briefs
 WHEN NEW.task_id<>OLD.task_id OR NEW.repository_id<>OLD.repository_id OR NEW.base_commit<>OLD.base_commit OR NEW.actor<>OLD.actor OR NEW.created_ms<>OLD.created_ms OR NEW.revision<>OLD.revision+1
 BEGIN SELECT RAISE(ABORT,'task brief identity is immutable; revisions increase by one'); END;
CREATE TRIGGER task_briefs_no_delete BEFORE DELETE ON task_briefs BEGIN SELECT RAISE(ABORT,'task brief is retained'); END;
CREATE TABLE execution_decisions (
 grant_id TEXT PRIMARY KEY REFERENCES execution_grants(id),
 body TEXT NOT NULL CHECK(length(CAST(body AS BLOB))<=65536),
 sha256 TEXT NOT NULL CHECK(length(sha256)=64)
) STRICT;
CREATE TRIGGER execution_decisions_no_update BEFORE UPDATE ON execution_decisions BEGIN SELECT RAISE(ABORT,'execution decision is immutable'); END;
CREATE TRIGGER execution_decisions_no_delete BEFORE DELETE ON execution_decisions BEGIN SELECT RAISE(ABORT,'execution decision is retained'); END;
CREATE TABLE owner_commands (
 message_id TEXT PRIMARY KEY, generation TEXT NOT NULL,
 principal_id TEXT NOT NULL REFERENCES principals(id),
 route TEXT NOT NULL CHECK(length(route) BETWEEN 1 AND 256),
 request_sha256 TEXT NOT NULL CHECK(length(request_sha256)=64),
 status INTEGER NOT NULL CHECK(status BETWEEN 100 AND 599),
 response TEXT NOT NULL CHECK(length(CAST(response AS BLOB))<=1048576),
 created_ms INTEGER NOT NULL CHECK(created_ms BETWEEN 1 AND 9007199254740991)
) STRICT;
CREATE TRIGGER owner_commands_no_update BEFORE UPDATE ON owner_commands BEGIN SELECT RAISE(ABORT,'owner command receipt is immutable'); END;
CREATE TRIGGER owner_commands_no_delete BEFORE DELETE ON owner_commands BEGIN SELECT RAISE(ABORT,'owner command receipt is retained'); END;
CREATE TABLE jobs (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK(length(kind) BETWEEN 1 AND 64),
 subject_id TEXT NOT NULL CHECK(length(subject_id)<=256),
 state TEXT NOT NULL CHECK(state IN ('queued','running','succeeded','failed')),
 daemon_boot TEXT NOT NULL,
 created_ms INTEGER NOT NULL CHECK(created_ms BETWEEN 1 AND 9007199254740991),
 started_ms INTEGER NOT NULL DEFAULT 0 CHECK(started_ms BETWEEN 0 AND 9007199254740991),
 finished_ms INTEGER NOT NULL DEFAULT 0 CHECK(finished_ms BETWEEN 0 AND 9007199254740991),
 result TEXT NOT NULL DEFAULT '' CHECK(length(CAST(result AS BLOB))<=65536),
 error TEXT NOT NULL DEFAULT '' CHECK(length(error)<=4096)
) STRICT;
CREATE TRIGGER jobs_identity BEFORE UPDATE OF id,kind,subject_id,created_ms ON jobs BEGIN SELECT RAISE(ABORT,'job identity is immutable'); END;
CREATE TRIGGER jobs_terminal BEFORE UPDATE ON jobs WHEN OLD.state IN ('succeeded','failed') BEGIN SELECT RAISE(ABORT,'terminal job is immutable'); END;
CREATE TRIGGER jobs_no_delete BEFORE DELETE ON jobs BEGIN SELECT RAISE(ABORT,'jobs are retained'); END;
CREATE TABLE daemon_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 paused INTEGER NOT NULL CHECK(paused IN (0,1)),
 reason TEXT NOT NULL CHECK(length(reason)<=256),
 updated_ms INTEGER NOT NULL CHECK(updated_ms BETWEEN 0 AND 9007199254740991)
) STRICT;
INSERT INTO daemon_state VALUES(1,0,'',CAST(strftime('%s','now') AS INTEGER)*1000);
CREATE TRIGGER daemon_state_no_delete BEFORE DELETE ON daemon_state BEGIN SELECT RAISE(ABORT,'daemon state is a seeded singleton'); END;
CREATE TABLE gateway_profiles (
 digest TEXT PRIMARY KEY CHECK(length(digest)=64),
 body TEXT NOT NULL CHECK(length(CAST(body AS BLOB))<=16384),
 created_ms INTEGER NOT NULL CHECK(created_ms BETWEEN 1 AND 9007199254740991)
) STRICT;
CREATE TRIGGER gateway_profiles_no_update BEFORE UPDATE ON gateway_profiles BEGIN SELECT RAISE(ABORT,'gateway profile is immutable'); END;
CREATE TRIGGER gateway_profiles_no_delete BEFORE DELETE ON gateway_profiles BEGIN SELECT RAISE(ABORT,'gateway profile is retained'); END;
CREATE TABLE runner_sessions (
 id TEXT PRIMARY KEY, runner_id TEXT NOT NULL REFERENCES principals(id),
 runner_boot TEXT NOT NULL, daemon_boot TEXT NOT NULL, generation TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('normal','recovery_only')),
 hello TEXT NOT NULL CHECK(length(CAST(hello AS BLOB))<=65536),
 created_ms INTEGER NOT NULL CHECK(created_ms BETWEEN 1 AND 9007199254740991)
) STRICT;
CREATE INDEX runner_sessions_runner ON runner_sessions(runner_id,created_ms);
CREATE TRIGGER runner_sessions_no_update BEFORE UPDATE ON runner_sessions BEGIN SELECT RAISE(ABORT,'runner session is immutable'); END;
CREATE TRIGGER runner_sessions_no_delete BEFORE DELETE ON runner_sessions BEGIN SELECT RAISE(ABORT,'runner session is retained'); END;
CREATE TABLE runtime_observations (
 attempt_id TEXT NOT NULL REFERENCES attempts(id),
 evidence_sha256 TEXT NOT NULL CHECK(length(evidence_sha256)=64),
 kind TEXT NOT NULL CHECK(length(kind) BETWEEN 1 AND 64),
 runner_boot TEXT NOT NULL, daemon_boot TEXT NOT NULL,
 body TEXT NOT NULL CHECK(length(CAST(body AS BLOB))<=16384),
 recorded_ms INTEGER NOT NULL CHECK(recorded_ms BETWEEN 1 AND 9007199254740991),
 PRIMARY KEY(attempt_id,evidence_sha256)
) STRICT;
CREATE TRIGGER runtime_observations_no_update BEFORE UPDATE ON runtime_observations BEGIN SELECT RAISE(ABORT,'runtime observation is immutable'); END;
CREATE TRIGGER runtime_observations_no_delete BEFORE DELETE ON runtime_observations BEGIN SELECT RAISE(ABORT,'runtime observation is retained'); END;
CREATE TABLE upload_sessions (
 id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL REFERENCES attempts(id),
 runner_id TEXT NOT NULL REFERENCES principals(id),
 manifest_id TEXT NOT NULL, manifest_sha256 TEXT NOT NULL CHECK(length(manifest_sha256)=64),
 manifest_bytes INTEGER NOT NULL CHECK(manifest_bytes BETWEEN 1 AND 1048576),
 result TEXT NOT NULL CHECK(length(result)<=8192),
 state TEXT NOT NULL CHECK(state IN ('open','committed','abandoned')),
 bytes_total INTEGER NOT NULL CHECK(bytes_total BETWEEN 0 AND 268435456),
 created_ms INTEGER NOT NULL CHECK(created_ms BETWEEN 1 AND 9007199254740991)
) STRICT;
CREATE TRIGGER upload_sessions_identity BEFORE UPDATE OF id,attempt_id,runner_id,manifest_id,manifest_sha256,manifest_bytes,result,created_ms ON upload_sessions BEGIN SELECT RAISE(ABORT,'upload session identity is immutable'); END;
CREATE TRIGGER upload_sessions_terminal BEFORE UPDATE ON upload_sessions WHEN OLD.state<>'open' BEGIN SELECT RAISE(ABORT,'closed upload session is immutable'); END;
CREATE TRIGGER upload_sessions_no_delete BEFORE DELETE ON upload_sessions BEGIN SELECT RAISE(ABORT,'upload session is retained'); END;
CREATE TABLE upload_blobs (
 upload_id TEXT NOT NULL REFERENCES upload_sessions(id),
 digest TEXT NOT NULL CHECK(length(digest)=64),
 bytes INTEGER NOT NULL CHECK(bytes BETWEEN 0 AND 1073741824),
 state TEXT NOT NULL CHECK(state IN ('missing','staged','committed')),
 PRIMARY KEY(upload_id,digest)
) STRICT;
CREATE TRIGGER upload_blobs_identity BEFORE UPDATE OF upload_id,digest,bytes ON upload_blobs BEGIN SELECT RAISE(ABORT,'upload blob identity is immutable'); END;
CREATE TRIGGER upload_blobs_no_delete BEFORE DELETE ON upload_blobs BEGIN SELECT RAISE(ABORT,'upload blob is retained'); END;
CREATE TABLE artifact_result_heads (
 task_id TEXT PRIMARY KEY REFERENCES tasks(id), generation TEXT NOT NULL,
 attempt_id TEXT NOT NULL REFERENCES attempts(id),
 epoch INTEGER NOT NULL CHECK(epoch BETWEEN 1 AND 9007199254740991),
 manifest_id TEXT NOT NULL REFERENCES artifact_manifests(manifest_id),
 receipt_id TEXT NOT NULL REFERENCES artifact_results(receipt_id),
 finalized_ms INTEGER NOT NULL CHECK(finalized_ms BETWEEN 1 AND 9007199254740991),
 FOREIGN KEY(attempt_id,task_id,epoch) REFERENCES attempts(id,task_id,epoch)
) STRICT;
CREATE TRIGGER artifact_result_heads_identity BEFORE UPDATE OF task_id ON artifact_result_heads BEGIN SELECT RAISE(ABORT,'result head task is immutable'); END;
CREATE TRIGGER artifact_result_heads_no_delete BEFORE DELETE ON artifact_result_heads BEGIN SELECT RAISE(ABORT,'result head is retained'); END;
CREATE TABLE attempt_usage (
 attempt_id TEXT NOT NULL REFERENCES attempts(id),
 request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
 body TEXT NOT NULL CHECK(length(CAST(body AS BLOB))<=8192),
 PRIMARY KEY(attempt_id,request_id)
) STRICT;
CREATE TRIGGER attempt_usage_identity BEFORE UPDATE OF attempt_id,request_id ON attempt_usage BEGIN SELECT RAISE(ABORT,'usage identity is immutable'); END;
CREATE TRIGGER attempt_usage_no_delete BEFORE DELETE ON attempt_usage BEGIN SELECT RAISE(ABORT,'usage is retained'); END;
CREATE TABLE reconcile_reports (
 id TEXT PRIMARY KEY, daemon_boot TEXT NOT NULL,
 created_ms INTEGER NOT NULL CHECK(created_ms BETWEEN 1 AND 9007199254740991),
 body TEXT NOT NULL CHECK(length(CAST(body AS BLOB))<=1048576)
) STRICT;
CREATE TRIGGER reconcile_reports_no_update BEFORE UPDATE ON reconcile_reports BEGIN SELECT RAISE(ABORT,'reconcile report is immutable'); END;
CREATE TRIGGER reconcile_reports_no_delete BEFORE DELETE ON reconcile_reports BEGIN SELECT RAISE(ABORT,'reconcile report is retained'); END;
CREATE TABLE restore_history (
 id TEXT PRIMARY KEY, old_generation TEXT NOT NULL, new_generation TEXT NOT NULL,
 backup_id TEXT NOT NULL CHECK(length(backup_id) BETWEEN 1 AND 128),
 backup_created_ms INTEGER NOT NULL CHECK(backup_created_ms BETWEEN 1 AND 9007199254740991),
 restored_ms INTEGER NOT NULL CHECK(restored_ms BETWEEN 1 AND 9007199254740991),
 manifest_sha256 TEXT NOT NULL CHECK(length(manifest_sha256)=64)
) STRICT;
CREATE TRIGGER restore_history_no_update BEFORE UPDATE ON restore_history BEGIN SELECT RAISE(ABORT,'restore history is immutable'); END;
CREATE TRIGGER restore_history_no_delete BEFORE DELETE ON restore_history BEGIN SELECT RAISE(ABORT,'restore history is retained'); END;
CREATE INDEX attempts_active ON attempts(state) WHERE state NOT IN ('succeeded','failed','cancelled','expired');
CREATE TABLE artifact_blobs_next (
 digest TEXT PRIMARY KEY CHECK(length(digest)=64), bytes INTEGER NOT NULL CHECK(bytes BETWEEN 0 AND 1073741824), created_ms INTEGER NOT NULL,
 retained_until_ms INTEGER NOT NULL, state TEXT NOT NULL CHECK(state IN ('committed','quarantined'))
) STRICT;
CREATE TEMP TABLE artifact_blobs_migrate AS SELECT * FROM artifact_blobs;
DROP TABLE artifact_blobs;
ALTER TABLE artifact_blobs_next RENAME TO artifact_blobs;
INSERT INTO artifact_blobs SELECT * FROM artifact_blobs_migrate;
DROP TABLE artifact_blobs_migrate;
CREATE TRIGGER artifact_blobs_immutable BEFORE UPDATE ON artifact_blobs BEGIN SELECT RAISE(ABORT,'artifact blob immutable'); END;
PRAGMA user_version=10;
`
