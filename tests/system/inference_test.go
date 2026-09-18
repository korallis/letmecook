//go:build system

package system

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Read only documented runner-journal evidence. No row or receipt is injected.
func boundaryEvidence(w *worker, d object) object {
	b, err := os.ReadFile(filepath.Join(w.state, "attempts", str(d["id"]), "journal", "journal.jsonl"))
	if err != nil {
		return object{"read_error": err.Error()}
	}
	reserved, completed := map[string]object{}, map[string]object{}
	for _, line := range strings.Split(string(b), "\n") {
		outbox := obj(obj(obj(parse([]byte(line)))["data"])["outbox"])
		receipt := obj(outbox["Body"])
		id := str(receipt["request_id"])
		switch str(outbox["Kind"]) {
		case "usage-reserve":
			reserved[id] = receipt
		case "usage-complete":
			if boolean(receipt["terminal"]) {
				completed[id] = receipt
			}
		}
	}
	return object{"reservations": len(reserved), "terminal_receipts": len(completed), "in_flight": len(reserved) - len(completed), "reserved": reserved, "completed": completed}
}

func (r *installation) inferenceHang(row object) {
	w := r.addWorker("greeting", "opencode")
	w.start(devProfile)
	w.importFacts()
	r.attemptMS = 15000
	d := r.task(w, "Reply with exactly ok; do not edit files or use tools.", []object{{"id": "check", "text": "Observe the hanging synthetic inference request without live model access"}}, []string{"greeting.txt"}, object{"model": "gpt-6-astra"})
	id := identity(d)
	defer func() {
		out := r.observe(d, w)
		r.s.add("fault-row", "hang", time.Now(), object{"expected": row, "observed": out, "boundary": boundaryEvidence(w, d), "blocking": !boolean(out["released"])})
	}()
	r.waitAttempt(str(id["attempt_id"]), 30*time.Second, func(v object) bool { return str(v["state"]) == "running" })
	var boundary, attempt object
	var began time.Time
	held := waitFor(15*time.Second, func() bool {
		boundary = boundaryEvidence(w, d)
		attempt = obj(r.get("/api/v1/attempts/" + str(id["attempt_id"])))
		if num(boundary["in_flight"]) > 0 {
			if began.IsZero() {
				began = time.Now()
			}
			r.s.check(r.t, "in-flight model request retains reservation", !boolean(attempt["released"]), attempt)
			return time.Since(began) >= time.Second
		}
		began = time.Time{}
		return terminal(attempt)
	})
	r.s.check(r.t, "mock-gateway --hang-after 0 holds a real model request", held && num(boundary["in_flight"]) > 0 && !began.IsZero() && time.Since(began) >= time.Second, object{"boundary": boundary, "attempt": attempt, "required_flag": "mock-gateway --hang-after"})
	waitFor(40*time.Second, func() bool {
		attempt = obj(r.get("/api/v1/attempts/" + str(id["attempt_id"])))
		return terminal(attempt)
	})
	boundary = boundaryEvidence(w, d)
	r.observe(d, w)
	r.s.check(r.t, "no release with unfinished inference receipt", !boolean(attempt["released"]) || num(boundary["in_flight"]) == 0, object{"attempt": attempt, "boundary": boundary})
	r.s.check(r.t, "inference-hang fixture terminal state", str(attempt["state"]) == str(row["attempt_state"]), attempt)
}

// Only a completed shell-tool result is evidence. A prompt echo or model claim
// containing the marker cannot prove that the sandbox denied the credential.
func credentialProbeEvidence(stream object) bool {
	for _, raw := range arr(stream["records"]) {
		rec := obj(raw)
		native := decodeBase64(str(obj(rec["native"])["data"]))
		for _, line := range strings.Split(string(native), "\n") {
			event := obj(parse([]byte(line)))
			part := obj(event["part"])
			state := obj(part["state"])
			if str(event["type"]) == "tool_use" && str(part["tool"]) == "bash" && str(state["status"]) == "completed" && strings.TrimSpace(str(state["output"])) == "credential_unreadable" {
				return true
			}
		}
	}
	return false
}
