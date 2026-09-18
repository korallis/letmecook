//go:build system

package system

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func s11(t *testing.T, s *scenario) {
	fixture := obj(readJSON(filepath.Join(checkout, "tests/system/fixtures/fault-matrix-v1.json")))
	for _, v := range arr(fixture["modes"]) {
		row := obj(v)
		mode := str(row["mode"])
		t.Run(mode, func(t *testing.T) {
			r := newInstall(t, s, mode, false)
			if mode == "hang" {
				r.inferenceHang(row)
				return
			}
			w := r.readyWorker("greeting")
			settings := fakeSettings(mode)
			paths := []string{"greeting.txt"}
			edits := []object{{"path": "greeting.txt", "content": "hello, gaffer\n"}}
			switch mode {
			case "huge_output":
				obj(settings["attempts"].([]object)[0])["stream_bytes"] = 8388608
			case "create_empty":
				edits = []object{{"path": "empty.txt", "content": ""}}
				paths = []string{"empty.txt"}
			case "delete":
				edits = []object{{"path": "greeting.txt", "delete": true}}
			}
			settings["attempts"].([]object)[0]["edits"] = edits
			d := r.task(w, "Synthetic fault matrix "+mode, []object{{"id": "check", "text": "Observe declared fault outcome"}}, paths, settings)
			id := identity(d)
			if mode == "ignore_term" || mode == "detached_child" {
				r.waitAttempt(str(id["attempt_id"]), 20*time.Second, func(v object) bool { return str(v["state"]) == "running" })

				r.mustCLI("task", "stop", str(id["task_id"]))
			}
			var a object
			ok := waitFor(35*time.Second, func() bool {
				a = obj(r.get("/api/v1/attempts/" + str(id["attempt_id"])))
				return terminal(a) || (mode == "detached_child" && (str(a["state"]) == "stopping" || str(a["state"]) == "unknown"))
			})
			out := r.observe(d, w)
			expected := str(row["attempt_state"])
			state := str(a["state"])
			stateOK := state == expected || expected == "cancelled_or_expired" && (state == "cancelled" || state == "expired") || expected == "unknown_or_stopping" && (state == "unknown" || state == "stopping")
			s.check(t, mode+" fixture terminal state", ok && stateOK, object{"expected": expected, "observed": state})
			s.check(t, mode+" reservation state", boolean(a["released"]) == boolean(row["reservation_released"]), a)
			s.check(t, mode+" task phase", str(obj(out["task"])["phase"]) == str(row["task_state"]), obj(out["task"])["phase"])
			if str(row["receipt"]) == "none" || strings.Contains(str(row["receipt"]), "no custody receipt") {
				s.check(t, mode+" fixture expects no custody receipt", str(obj(out["execution"])["receipt_id"]) == "", obj(out["execution"])["receipt_id"])
			}
			for _, assertion := range arr(row["assertions"]) {
				if str(assertion) == "no_head_promoted" {
					s.check(t, "failed custody never promoted to head", str(obj(obj(out["task"])["head"])["receipt_id"]) == "", out["task"])
				}
			}
			if mode == "create_empty" || mode == "delete" {
				r.assertEdits(out, edits)
			}
			if mode == "ignore_term" {
				g := assertGuardian(w, d, 5*time.Second)
				s.check(t, "TERM ignore escalates within five seconds", boolean(g["escalated"]) && num(g["stop_to_observed_ns"]) <= int64(5*time.Second), g)
			}
			streamJSON, _ := json.Marshal(out["stream"])
			if mode == "huge_output" {
				s.check(t, "terminal spool_full retained", strings.Contains(string(streamJSON), "spool_full"), out["stream"])
			}
			if mode == "approval" {
				s.check(t, "approval_required retained and never answered", strings.Contains(string(streamJSON), "approval_required") && !strings.Contains(string(streamJSON), "approval_granted"), out["stream"])
			}
			if !boolean(a["released"]) {
				retry := r.cli("task", "retry", str(id["task_id"]))
				s.check(t, "unknown reservation blocks second attempt", retry.Exit != 0, retry)
			}
			s.add("fault-row", mode, time.Now(), object{"expected": row, "observed": out, "blocking": !boolean(a["released"])})
		})
	}
}

func s12(t *testing.T, s *scenario) {
	fixture := obj(readJSON(filepath.Join(checkout, "tests/system/fixtures/corpus-v1.json")))
	r := newInstall(t, s, "", false)
	workers := map[string]*worker{}
	for _, repo := range []string{"greeting", "calc", "notes"} {
		workers[repo] = r.readyWorker(repo)
	}
	durations := []int64{}
	for _, v := range arr(fixture["tasks"]) {
		row := obj(v)
		t.Run(str(row["id"]), func(t *testing.T) {
			old := r.t
			r.t = t
			defer func() { r.t = old }()
			started := time.Now()
			defer func() { durations = append(durations, time.Since(started).Milliseconds()) }()
			w := workers[str(row["repo"])]
			r.s.add("fixture", "corpus task", started, row)
			// Fixture operations are edit/create/delete labels, not authority operations.
			// CLI's documented read/verify/write mapping is explicit and recorded here.
			s.add("fixture-discrepancy", "operation vocabulary mapping", started, object{"fixture": row["operations"], "authority": []string{"read", "verify", "write"}})
			d := r.task(w, str(row["brief"]), asObjects(row["criteria"]), asStrings(row["paths"]), row["fake_spec"])
			id := identity(d)
			expected := str(row["expected_outcome"])
			if expected == "stopped" {
				r.waitAttempt(str(id["attempt_id"]), 20*time.Second, func(v object) bool { return str(v["state"]) == "running" })
				r.mustCLI("task", "stop", str(id["task_id"]))
			}
			a := r.settle(d)
			out := r.observe(d, w)
			if expected == "retry_then_succeeded" {
				s.check(t, "first corpus attempt failed", str(a["state"]) == "failed", a)
				d = r.mustCLI("task", "retry", str(id["task_id"]))
				a = r.settle(d)
				out = r.observe(d, w)
			}
			switch expected {
			case "succeeded", "retry_then_succeeded", "failed_verification":
				s.check(t, "execution succeeded before review", str(a["state"]) == "succeeded", a)
				script := obj(row["fake_spec"])
				attempts := arr(script["attempts"])
				if len(attempts) > 0 {
					r.assertEdits(out, asObjects(obj(attempts[len(attempts)-1])["edits"]))
				}
				final := r.verifyReview(str(id["task_id"]), expected != "failed_verification", obj(row["verification"]))
				if expected == "failed_verification" {
					s.check(t, "failed verification explicitly rejected", !boolean(obj(obj(final["verification"])["status"])["verified"]) && !boolean(obj(final["review"])["accepted"]) && str(obj(obj(final["review"])["decision"])["action"]) == "reject", final)
				} else {
					s.check(t, "corpus accepted", boolean(obj(final["review"])["accepted"]) && str(obj(obj(final["review"])["decision"])["action"]) == "accept", final)
				}
			case "approval_blocked":
				s.check(t, "approval blocked not accepted", str(a["state"]) == "failed" && !boolean(obj(obj(out["task"])["review"])["accepted"]), out)
			case "stopped":
				s.check(t, "stopped corpus task", str(a["state"]) == "cancelled" && boolean(a["released"]), a)
			case "rejected":
				review := r.cli("review", "reject", str(id["task_id"]), "--notes", "Spool limit fixture rejection")
				s.check(t, "explicit rejected outcome", review.Exit == 0, review)
			default:
				s.check(t, "known corpus outcome", false, expected)
			}
			s.add("corpus-row", str(row["id"]), started, object{"expected": expected, "observed": out, "duration_ms": time.Since(started).Milliseconds()})
		})
	}
	s.add("statistics", "corpus wall time", time.Now(), object{"samples": len(durations), "p50_ms": percentile(durations, .5), "p95_ms": percentile(durations, .95), "max_ms": percentile(durations, 1)})
}

func s13(t *testing.T, s *scenario) {
	fixture := obj(readJSON(filepath.Join(checkout, "tests/system/fixtures/recovery-100-v1.json")))
	kills := map[string]bool{}
	for _, v := range arr(obj(fixture["restart_schedule"])["kills"]) {
		kills[str(obj(v)["task_id"])] = true
	}
	samples := []object{}
	for cycle := 1; cycle <= 2; cycle++ {
		t.Run(fmt.Sprintf("cycle-%d", cycle), func(t *testing.T) {
			r := newInstall(t, s, fmt.Sprintf("cycle-%d", cycle), true)
			workers := map[string]*worker{}
			for _, repo := range []string{"greeting", "calc", "notes"} {
				w := r.addWorker(repo, "fake")
				newProxy(t, r, w)
				w.start(devProfile)
				w.importFacts()
				workers[repo] = w
			}
			for _, v := range arr(fixture["tasks"]) {
				row := obj(v)
				t.Run(str(row["id"]), func(t *testing.T) {
					old := r.t
					r.t = t
					defer func() { r.t = old }()
					w := workers[str(row["repo"])]
					var sample object
					if kills[str(row["id"])] {
						sample = object{"cycle": cycle, "task": row["id"], "kill_scheduled": true, "kill_performed": false, "blocking": true, "failure": "dispatch not admitted; no restart fabricated"}
						defer func() { samples = append(samples, sample) }()
						w.proxy.armRunning()
						defer w.proxy.releaseRunningReply()
					}
					d := r.task(w, str(row["brief"]), asObjects(row["criteria"]), asStrings(row["paths"]), row["fake_spec"])
					id := identity(d)
					if !kills[str(row["id"])] {
						a := r.settle(d)
						s.check(t, "recovery fixture task succeeds", str(a["state"]) == "succeeded", a)
						return
					}
					// Poll real persisted running state, not a sleep or a simulated crash hook.
					var running object
					seen := waitFor(5*time.Second, func() bool {
						running = obj(r.get("/api/v1/attempts/" + str(id["attempt_id"])))
						return str(running["state"]) == "running" || terminal(running)
					})
					sample["identity"] = id
					delete(sample, "failure")
					if !seen || str(running["state"]) != "running" {
						sample["blocking"] = true
						sample["failure"] = "task never reached persisted running before terminal/deadline; no crash sample fabricated"
						s.check(t, "scheduled kill hits running task", false, running)
						return
					}
					sample["pre_kill_event_sequence"] = num(obj(r.get("/api/v1/events?task_id=" + str(id["task_id"]) + "&limit=50"))["next_after"])
					sample["kill_unix_ns"] = time.Now().UnixNano()
					sample["kill_performed"] = true
					r.daemon.stop(true)
					w.proxy.releaseRunningReply()
					r.startDaemon()
					ready := r.readyAt
					sample["restart_ready_unix_ns"] = ready.UnixNano()
					var terminalTask object
					ok := waitFor(65*time.Second, func() bool {
						for _, worker := range workers {
							afterCrash(r, worker)
						}
						terminalTask = obj(r.get("/api/v1/tasks/" + str(id["task_id"])))
						as := arr(terminalTask["attempts"])
						if len(as) < 2 {
							return false
						}
						last := obj(as[len(as)-1])
						if sample["first_admitted_dispatch_unix_ns"] == nil {
							sample["first_admitted_dispatch_unix_ns"] = time.Now().UnixNano()
							sample["admission_timestamp_source"] = "first observed committed dispatch via owner API; event API has no timestamp field"
							sample["recovery_ms"] = time.Since(ready).Milliseconds()
							sample["first_admitted_event_sequence"] = num(obj(r.get("/api/v1/events?task_id=" + str(id["task_id"]) + "&limit=50"))["next_after"])
						}
						return terminal(last) && boolean(last["released"]) && str(last["state"]) == "succeeded"
					})
					sample["blocking"] = !ok
					sample["observed"] = terminalTask
					r.get("/api/v1/reconcile")
					if ok {
						sample["all_classified_unix_ns"] = time.Now().UnixNano()
						sample["classification_timestamp_source"] = "first owner read showing terminal released attempt; event API has no timestamp field"
						assertGuardian(w, d, 5*time.Second)
						sample["classify_ms"] = time.Since(ready).Milliseconds()
						sample["all_classified_event_sequence"] = num(obj(r.get("/api/v1/events?task_id=" + str(id["task_id"]) + "&limit=50"))["next_after"])
					}
					s.check(t, "auto-retry safely recovers within sixty seconds", ok && num(sample["recovery_ms"]) < 60000 && num(sample["classify_ms"]) < 60000, sample)
				})
			}
		})
	}
	report["recovery_samples"] = samples
	recovery, classify := []int64{}, []int64{}
	blocking, restarts := 0, 0
	for _, v := range samples {
		if boolean(v["kill_performed"]) {
			restarts++
		}
		if boolean(v["blocking"]) {
			blocking++
			continue
		}
		recovery = append(recovery, num(v["recovery_ms"]))
		classify = append(classify, num(v["classify_ms"]))
	}
	stats := object{"scheduled": 20, "samples": len(samples), "real_restarts": restarts, "successful_samples": len(recovery), "blocking": blocking, "recovery_ms": object{"p50": percentile(recovery, .5), "p95": percentile(recovery, .95), "max": percentile(recovery, 1)}, "classify_ms": object{"p50": percentile(classify, .5), "p95": percentile(classify, .95), "max": percentile(classify, 1)}, "method": "nearest-rank; blocking samples retained and fail the gate; no lease barrier subtracted"}
	report["recovery_statistics"] = stats
	s.add("measurement", "recovery timestamp observability", time.Now(), object{"required": "harness observation timestamps plus event sequence, per corrected fixture", "observed": "GET /api/v1/events exposes sequence, revision and message only; no timestamp", "measurement": "owner-read upper bounds, not exact event timestamps"})
	s.add("statistics", "recovery", time.Now(), stats)
	s.check(t, "at least twenty successful real restarts with no blocking cases", len(recovery) >= 20 && blocking == 0, stats)
}

func s14(t *testing.T, s *scenario) {
	if os.Getenv("GAFFER_LIVE_GATEWAY") != "1" {
		s.add("live", "not authorized in this invocation", time.Now(), object{"requests": 0})
		t.Skip("S-14 requires GAFFER_LIVE_GATEWAY=1")
	}
	for _, prior := range scenarios {
		if prior != s && prior.Status != "PASS" {
			s.require(t, "all synthetic scenarios pass before live use", false, object{"blocking_scenario": prior.ID, "status": prior.Status, "requests": 0})
		}
	}
	s.require(t, "complete synthetic gate ran before live", len(scenarios) == 14, object{"scenarios": len(scenarios), "requests": 0})
	r := newInstall(t, s, "", false)
	w := r.addWorker("greeting", "opencode")
	w.start(devProfile)
	w.importFacts()
	config := obj(readJSON(os.Getenv("GAFFER_GATEWAY_CONFIG")))
	credentialPath := str(obj(config["credential_ref"])["path"])
	config = nil
	s.require(t, "external credential reference exists without exposing contents", credentialPath != "", object{"credential_ref": "<redacted>"})
	brief := "Change greeting.txt from hello to exactly hello, gaffer followed by a newline. Also run a sandbox probe: test ! -r '" + strings.ReplaceAll(credentialPath, "'", "'\\''") + "' && printf 'credential_unreadable\\n'. Do not read, print, copy or inspect any credential. Use the fewest requests necessary; do not change any other file."
	d := r.task(w, brief, []object{{"id": "greeting", "text": "greeting.txt says hello, gaffer"}, {"id": "isolation", "text": "job reports credential_unreadable without reading credential"}}, []string{"greeting.txt"}, object{"model": "gpt-6-astra"})
	a := r.waitAttempt(str(identity(d)["attempt_id"]), 3*time.Minute, terminal)
	out := r.observe(d, w)
	receipts := arr(out["usage"])
	s.check(t, "live nonzero gateway receipts", len(receipts) > 0, receipts)
	for _, v := range receipts {
		receipt := obj(v)
		s.check(t, "pinned model and gateway usage source", str(receipt["model"]) == "gpt-6-astra" && (str(receipt["source"]) == "gateway_usage" || str(receipt["source"]) == "gateway_usage_unknown"), receipt)
	}
	s.check(t, "job-side credential read denial probe retained", credentialProbeEvidence(obj(out["stream"])), out["stream"])
	s.check(t, "live execution succeeded", str(a["state"]) == "succeeded", a)
	r.assertEdits(out, []object{{"path": "greeting.txt", "content": "hello, gaffer\n"}})
	r.verifyReview(str(identity(d)["task_id"]), true)
	s.add("live", "redacted one-task result", time.Now(), object{"requests": len(receipts), "receipts": receipts, "gateway": "<gateway>", "credential": "<redacted>", "model": "gpt-6-astra"})
}
