//go:build system

package system

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func decodeBase64(s string) []byte { b, _ := base64.StdEncoding.DecodeString(s); return b }
func afterCrash(r *installation, w *worker) {
	if !w.p.alive() {
		r.s.add("recovery", "runner exited after daemon failure", time.Now(), w.p.logs())
		w.start(devProfile)
		w.importFacts()
	}
}
func s06(t *testing.T, s *scenario) {
	r := newInstall(t, s, "", false)
	w := r.readyWorker("greeting")
	d := r.simple(w, "hang")
	id := identity(d)
	r.waitAttempt(str(id["attempt_id"]), 20*time.Second, func(v object) bool { return str(v["state"]) == "running" })
	before := obj(r.get("/api/v1/daemon"))
	r.daemon.stop(true)
	s.add("fault", "SIGKILL daemon", time.Now(), r.daemon.logs())
	r.startDaemon()
	after := obj(r.get("/api/v1/daemon"))
	s.check(t, "ordinary restart retains generation changes boot", before["generation"] == after["generation"] && before["daemon_boot"] != after["daemon_boot"], object{"before": before, "after": after})
	view := obj(r.get("/api/v1/attempts/" + str(id["attempt_id"])))
	s.check(t, "running becomes unknown on restart", str(view["state"]) == "unknown", view)
	blocked := r.cli("task", "retry", str(id["task_id"]))
	s.check(t, "no second attempt while unknown", blocked.Exit != 0, blocked)
	time.Sleep(2 * time.Second)
	afterCrash(r, w)
	var settled object
	ok := waitFor(65*time.Second, func() bool {
		afterCrash(r, w)
		settled = obj(r.get("/api/v1/attempts/" + str(id["attempt_id"])))
		return terminal(settled)
	})
	r.get("/api/v1/reconcile")
	r.observe(d, w)
	s.check(t, "daemon restart evidence releases expired or completes original result", ok && boolean(settled["released"]) && (str(settled["state"]) == "expired" || str(settled["state"]) == "succeeded"), settled)
	assertGuardian(w, d, 5*time.Second)
}
func s07(t *testing.T, s *scenario) {
	r := newInstall(t, s, "", false)
	w := r.readyWorker("greeting")
	d := r.simple(w, "hang")
	id := identity(d)
	r.waitAttempt(str(id["attempt_id"]), 20*time.Second, func(v object) bool { return str(v["state"]) == "running" })
	oldBoot := str(w.session()["runner_boot"])
	killed := time.Now()
	w.p.stop(true)
	s.add("fault", "SIGKILL runner", killed, w.p.logs())
	g := assertGuardian(w, d, 8*time.Second)
	s.check(t, "guardian kills on supervisor EOF", str(g["cause"]) == "supervisor_eof" || str(g["cause"]) == "eof", g)
	w.start(devProfile)
	w.waitMode("recovery_only")
	s.check(t, "new runner boot after SIGKILL", str(w.session()["runner_boot"]) != oldBoot, w.session())
	w.importFacts()
	var a object
	ok := waitFor(65*time.Second, func() bool { a = obj(r.get("/api/v1/attempts/" + str(id["attempt_id"]))); return terminal(a) })
	r.get("/api/v1/reconcile")
	r.observe(d, w)
	s.check(t, "old-boot termination reconciles cancelled and released", ok && str(a["state"]) == "cancelled" && boolean(a["released"]), a)
	if ok && boolean(a["released"]) {
		d2 := r.mustCLI("task", "retry", str(id["task_id"]))
		s.check(t, "new epoch only after release and facts import", num(identity(d2)["epoch"]) == 2, d2)
		r.mustCLI("task", "stop", str(id["task_id"]))
		r.settle(d2)
	}
}
func s08(t *testing.T, s *scenario) {
	r := newInstall(t, s, "", false)
	w := r.addWorker("greeting", "fake")
	p := newProxy(t, r, w)
	p.flags(true, false, false)
	w.start(devProfile)
	w.importFacts()
	d := r.simple(w, "edit")
	a := r.settle(d)
	snapshot := p.snapshot()
	s.add("proxy", "commit replay transcript", time.Now(), snapshot)
	commits := snapshot["commits"].([]object)
	s.check(t, "commit reply dropped and retried", len(commits) >= 2, snapshot)
	if len(commits) >= 2 {
		s.check(t, "byte-identical custody ack and receipt", commits[0]["response_bytes"] == commits[1]["response_bytes"] && jsonEqual(commits[0]["request"], commits[1]["request"]) && str(obj(obj(commits[0]["response"])["ack"])["receipt_id"]) != "", commits)
	}
	s.check(t, "one finalized original attempt", str(a["state"]) == "succeeded" && boolean(a["released"]), a)
	out := r.observe(d, w)
	s.check(t, "one custody manifest", len(arr(out["artifacts"])) == 1, out["artifacts"])
}
func s09(t *testing.T, s *scenario) {
	r := newInstall(t, s, "", false)
	w := r.addWorker("greeting", "fake")
	p := newProxy(t, r, w)
	w.start(devProfile)
	w.importFacts()
	d := r.simple(w, "hang")
	id := identity(d)
	r.waitAttempt(str(id["attempt_id"]), 20*time.Second, func(v object) bool { return str(v["state"]) == "running" })
	p.flags(false, true, false)
	blocked := time.Now()
	s.add("fault", "block /x/v1/lease", blocked, nil)
	g := assertGuardian(w, d, 23*time.Second)
	s.check(t, "cutoff plus drift termination bound", time.Since(blocked) <= 20*time.Second && num(g["stop_to_observed_ns"]) <= int64(7*time.Second), object{"elapsed_ms": time.Since(blocked).Milliseconds(), "guardian": g})
	p.flags(false, false, false)
	a := r.waitAttempt(str(id["attempt_id"]), 45*time.Second, terminal)
	s.check(t, "lease expiry terminal and release", str(a["state"]) == "expired" && boolean(a["released"]), a)
	r.observe(d, w)
	s.add("proxy", "lease partition transcript", time.Now(), p.snapshot())
}
func s10(t *testing.T, s *scenario) {
	r := newInstall(t, s, "", false)
	w := r.addWorker("greeting", "fake")
	p := newProxy(t, r, w)
	p.flags(true, false, true)
	w.start(devProfile)
	w.importFacts()
	d := r.simple(w, "edit")
	select {
	case <-p.commitDropped:
	case <-time.After(30 * time.Second):
		s.require(t, "commit durable before reply fault", false, p.snapshot())
	}
	w.p.stop(true)
	s.add("fault", "kill runner with unacknowledged commit", time.Now(), w.p.logs())
	p.flags(false, false, false)
	var a object
	ok := waitFor(65*time.Second, func() bool { a = obj(r.get("/api/v1/attempts/" + str(identity(d)["attempt_id"]))); return terminal(a) })
	s.require(t, "source custody reconciled before backup", ok && boolean(a["released"]), a)
	r.mustCLI("daemon", "pause", "--reason", "restore drill")
	backup := r.mustCLI("backup", "create", "--destination", "snapshot")
	j := r.job(str(backup["job_id"]))
	s.add("backup", "source snapshot", time.Now(), j)
	sourceGeneration := str(obj(r.get("/api/v1/daemon"))["generation"])
	r.daemon.stop(false)
	p.close()
	r.proxy = nil
	w.proxy = nil
	_ = os.Mkdir(filepath.Join(r.root, "restored"), 0700)
	r.state = filepath.Join(r.root, "restored", "state")
	r.artifacts = filepath.Join(r.root, "restored", "artifacts")
	c := s.command(90*time.Second, filepath.Join(binDir, "gaffer"), "--json", "restore", "--backup", filepath.Join(r.root, "backups", "snapshot"), "--state-dir", r.state, "--artifacts-dir", r.artifacts)
	s.require(t, "offline restore completed", c.Exit == 0, c)
	r.startDaemon()
	status := obj(r.get("/api/v1/daemon"))
	s.check(t, "restore new generation paused", boolean(status["paused"]) && str(status["generation"]) != sourceGeneration, status)
	refused := r.cli("daemon", "resume")
	s.check(t, "source fencing confirmation mandatory", refused.Exit != 0, refused)
	w.start(devProfile)
	w.waitMode("recovery_only")
	w.importFacts()
	r.get("/api/v1/reconcile")
	s.add("journal", "stale generation replay", time.Now(), journalSummary(w))
	s.check(t, "old runner outbox gets stale-generation fencing", strings.Contains(compact(journalSummary(w)), "stale_generation"), journalSummary(w))
	sess := w.session()
	snap := p.snapshot()
	commits := snap["commits"].([]object)
	s.require(t, "original commit request retained", len(commits) > 0, snap)
	requests := snap["requests"].([]object)
	path := ""
	for _, v := range requests {
		if strings.HasSuffix(str(v["path"]), "/commit") {
			path = str(v["path"])
			break
		}
	}
	code, b, e := request(w.client, "POST", r.execURL+path, commits[0]["request"], map[string]string{"X-Gaffer-Session": str(sess["session_id"])})
	reply := obj(parse(b))
	s.add("replay", "restored old custody reply", time.Now(), object{"status": code, "body": reply, "error": fmt.Sprint(e)})
	s.check(t, "old receipt replay quarantined", code == 200 && boolean(reply["quarantined"]), reply)
	r.mustCLI("daemon", "resume", "--confirm-source-fenced")
	next := r.simple(w, "edit")
	s.check(t, "new dispatch uses restored generation", str(identity(next)["generation"]) == str(status["generation"]), next)
	r.settle(next)
}
func journalSummary(w *worker) object {
	out := object{}
	paths, _ := filepath.Glob(filepath.Join(w.state, "attempts", "*", "journal", "journal.jsonl"))
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			continue
		}
		rows := []any{}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "fenced") || strings.Contains(line, "guardian_observed") || strings.Contains(line, "terminated") || strings.Contains(line, "launched") || strings.Contains(line, "local_failure") || strings.Contains(line, `"kind":"request"`) {
				rows = append(rows, parse([]byte(line)))
			}
		}
		out[filepath.Base(filepath.Dir(filepath.Dir(p)))] = rows
	}
	return out
}
