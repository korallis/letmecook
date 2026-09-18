//go:build system

package system

import (
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"time"
)

// These are the complete SQL allowlist, also documented in evidence section 7.
// The daemon has stopped and checkpointed before these immutable reads. If a WAL
// remains nonempty, the read is refused rather than treating stale pages as truth.
var invariantQueries = []struct {
	name, sql string
	zero      bool
}{
	{"multiple_active_attempts", `SELECT task_id,count(*) AS n FROM attempts WHERE state NOT IN ('succeeded','failed','cancelled','expired') GROUP BY task_id HAVING count(*)>1`, true},
	{"duplicate_custody", `SELECT generation,task_id,attempt_id,epoch,count(*) AS n FROM artifact_results GROUP BY generation,task_id,attempt_id,epoch HAVING count(*)>1`, true},
	{"terminal_without_release", `SELECT a.id,a.state FROM attempts a JOIN dispatches d ON d.attempt_id=a.id LEFT JOIN dispatch_releases r ON r.dispatch_id=d.id WHERE a.state IN ('succeeded','failed','cancelled','expired') AND r.dispatch_id IS NULL`, true},
	{"head_without_custody", `SELECT h.task_id FROM artifact_result_heads h LEFT JOIN artifact_results r ON r.receipt_id=h.receipt_id WHERE r.receipt_id IS NULL OR r.quarantined<>0 OR r.attempt_id<>h.attempt_id OR r.generation<>h.generation`, true},
	{"revision_event_mismatch", `SELECT a.id,a.revision,max(e.revision) AS event_revision FROM attempts a LEFT JOIN events e ON e.attempt_id=a.id GROUP BY a.id HAVING a.revision<>max(e.revision)`, true},
	{"foreign_keys", `PRAGMA foreign_key_check`, true},
	{"custody_inventory", `SELECT generation,task_id,attempt_id,epoch,manifest_id,receipt_id,quarantined FROM artifact_results ORDER BY created_ms`, false},
	{"reservation_inventory", `SELECT d.id,d.attempt_id,d.runner_id,d.grant_id,a.state,a.revision,r.body AS release FROM dispatches d JOIN attempts a ON a.id=d.attempt_id LEFT JOIN dispatch_releases r ON r.dispatch_id=d.id ORDER BY d.created_ms`, false},
	{"session_inventory", `SELECT id,runner_id,runner_boot,daemon_boot,generation,mode FROM runner_sessions ORDER BY created_ms`, false},
	{"termination_inventory", `SELECT attempt_id,kind,runner_boot,daemon_boot,body FROM runtime_observations WHERE kind IN ('launched','exit','terminated','completion','stop') ORDER BY recorded_ms`, false},
}

func (r *installation) invariants() {
	r.t.Helper()
	path := filepath.Join(r.state, "state.db")
	if _, e := os.Stat(path); e != nil {
		return
	}
	if r.daemon != nil && r.daemon.alive() {
		r.s.check(r.t, "invariants require stopped daemon", false, r.daemon.logs())
		return
	}
	if i, e := os.Stat(path + "-wal"); e == nil && i.Size() > 0 {
		r.s.check(r.t, "checkpoint before immutable invariant queries", false, object{"wal_bytes": i.Size()})
		return
	}
	db, e := sql.Open("sqlite", "file:"+quotePath(path)+"?mode=ro&immutable=1")
	if !r.s.check(r.t, "open immutable read-only invariant connection", e == nil, fmt.Sprint(e)) {
		return
	}
	defer db.Close()
	for _, q := range invariantQueries {
		now := time.Now()
		rows, e := db.Query(q.sql)
		if !r.s.check(r.t, "query "+q.name, e == nil, fmt.Sprint(e)) {
			continue
		}
		cols, _ := rows.Columns()
		out := []object{}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if e = rows.Scan(ptrs...); e != nil {
				break
			}
			v := object{}
			for i, k := range cols {
				if b, ok := values[i].([]byte); ok {
					values[i] = string(b)
				}
				if s, ok := values[i].(string); ok && (k == "body" || k == "release") {
					values[i] = parse([]byte(s))
				}
				v[k] = values[i]
			}
			out = append(out, v)
		}
		err := rows.Err()
		_ = rows.Close()
		r.s.add("invariant", q.name, now, object{"sql": q.sql, "rows": out, "error": fmt.Sprint(err)})
		r.s.check(r.t, "invariant "+q.name, e == nil && err == nil && (!q.zero || len(out) == 0), object{"rows": len(out), "error": fmt.Sprint(e, err)})
	}
}
