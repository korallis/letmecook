//go:build system

package system

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestM1(t *testing.T) {
	cases := []struct {
		id, name string
		fn       func(*testing.T, *scenario)
	}{
		{"S-01", "bootstrap listeners ALPN and fail-closed profiles", s01}, {"S-02", "enrollment and repository registration", s02}, {"S-03", "facts boot and recovery-only sessions", s03}, {"S-04", "accepted fake flow and second dispatch", s04}, {"S-05", "stop running job and explicit resume retry", s05}, {"S-06", "daemon SIGKILL recovery", s06}, {"S-07", "runner SIGKILL guardian recovery", s07}, {"S-08", "lost custody reply replay", s08}, {"S-09", "lease partition cutoff", s09}, {"S-10", "backup restore stale generation", s10}, {"S-11", "fault matrix", s11}, {"S-12", "twenty representative tasks", s12}, {"S-13", "hundred task recovery cycles", s13}, {"S-14", "one live OpenCode accepted task", s14},
	}
	for _, c := range cases {
		runScenario(t, c.id, c.name, c.fn)
	}
}
func s01(t *testing.T, s *scenario) {
	r := newInstall(t, s, "", false)
	w := r.addWorker("greeting", "fake")
	r.mustCLI("identity", "self")
	conn, e := net.DialTimeout("tcp", strings.TrimPrefix(r.execURL, "https://"), time.Second)
	s.require(t, "raw execution TCP connect", e == nil, fmt.Sprint(e))
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = conn.Write([]byte("GET /x/v1/inbox HTTP/1.1\r\nHost: invalid\r\n\r\n"))
	b := make([]byte, 2048)
	n, err := conn.Read(b)
	_ = conn.Close()
	s.check(t, "plaintext gets no execution payload", err != nil || !bytes.Contains(b[:n], []byte("assignments")), string(b[:n]))
	cfg := tlsConfig(t, r.root, "runner-greeting", "execution-provisional-v2")
	c, e := tls.Dial("tcp", strings.TrimPrefix(r.execURL, "https://"), cfg)
	s.require(t, "execution singleton ALPN", e == nil, fmt.Sprint(e))
	s.check(t, "selected ALPN", c.ConnectionState().NegotiatedProtocol == "execution-provisional-v2", c.ConnectionState().NegotiatedProtocol)
	_ = c.Close()
	bad := tlsConfig(t, r.root, "runner-greeting", "http/1.1")
	c, e = tls.Dial("tcp", strings.TrimPrefix(r.execURL, "https://"), bad)
	if c != nil {
		_ = c.Close()
	}
	s.check(t, "HTTP ALPN refused on execution listener", e != nil, fmt.Sprint(e))
	temp, e := os.MkdirTemp("/private/tmp", "gaffer-system-profile-refusal-")
	s.require(t, "temp refusal root", e == nil, fmt.Sprint(e))
	defer os.RemoveAll(temp)
	args := r.daemonArgs()
	for i := range args {
		if args[i] == r.state {
			args[i] = filepath.Join(temp, "state")
		}
		if args[i] == r.artifacts {
			args[i] = filepath.Join(temp, "artifacts")
		}
	}
	bootstrap := s.command(time.Minute, filepath.Join(binDir, "gafferd"), "--state-dir", filepath.Join(temp, "state"), "--artifacts-dir", filepath.Join(temp, "artifacts"), "--bootstrap-owner-cert", filepath.Join(r.root, "owner.crt"))
	s.require(t, "bootstrap temp refusal case", bootstrap.Exit == 0, bootstrap)
	refused := s.command(15*time.Second, filepath.Join(binDir, "gafferd"), args...)
	s.check(t, "development verifier rejects system-temp root before readiness", refused.Exit != 0 && !strings.Contains(refused.Stdout, "store-only"), refused)
	s.check(t, "temp-root refusal names rejected path and profile", strings.Contains(refused.Stderr, temp) && strings.Contains(refused.Stderr, devProfile), refused)
	w.start("unqualified")
	facts := s.command(20*time.Second, filepath.Join(binDir, "gaffer-runner"), "facts", "--state-dir", w.state, "--gateway-config", r.gateway, "--gateway-ca", r.ca)
	s.check(t, "unqualified runner cannot acquire launch facts", facts.Exit != 0 || str(obj(obj(facts.Value)["isolation"])["qualification"]) == "unqualified", facts)
	v := r.get("/api/v1/tasks")
	s.check(t, "no launch or task from enrollment", len(arr(obj(v)["tasks"])) == 0, v)
}
func s02(t *testing.T, s *scenario) {
	r := newInstall(t, s, "", false)
	w := r.addWorker("greeting", "fake")
	p := r.cliAs("runner-greeting", "identity", "self")
	s.require(t, "enabled enrolled runner identity", p.Exit == 0 && boolean(resultObject(p)["enabled"]) && str(resultObject(p)["id"]) == w.id, p)
	denied := r.cliAs("runner-greeting", "daemon", "status")
	s.check(t, "runner cannot read owner workflow", denied.Exit != 0, denied)
	profile := obj(r.get("/api/v1/repositories/greeting"))
	s.check(t, "pinned file remote and revision", num(profile["revision"]) == 1 && strings.HasPrefix(str(profile["remote"]), "file://") && str(obj(profile["base"])["commit"]) == "479eec44aa5cda0a5f91a48b2e5b6119c18570ba", profile)
}
func s03(t *testing.T, s *scenario) {
	r := newInstall(t, s, "", false)
	w := r.readyWorker("greeting")
	before := w.session()
	w.p.stop(false)
	w.start(devProfile)
	w.waitMode("recovery_only")
	after := w.session()
	s.check(t, "restart boot changes before new facts", str(before["runner_boot"]) != str(after["runner_boot"]), object{"before": before, "after": after})
	w.importFacts()
	normal := w.session()
	s.check(t, "same restarted boot normal after import", str(normal["runner_boot"]) == str(after["runner_boot"]) && w.revision == 3, normal)
}
func s04(t *testing.T, s *scenario) {
	r := newInstall(t, s, "", false)
	w := r.readyWorker("greeting")
	bf := filepath.Join(r.root, "brief.txt")
	sf := filepath.Join(r.root, "fake.json")
	_ = os.WriteFile(bf, []byte("Change greeting.txt to exactly hello, gaffer followed by a newline."), 0600)
	_ = writeJSON(sf, fakeSettings("edit"))
	flow := "S04-" + uuid()
	c := r.cli("flow", "run", "--flow-id", flow, "--repo", "greeting", "--base", w.base, "--brief-file", bf, "--criterion", "greeting=greeting.txt says hello, gaffer", "--path", "greeting.txt", "--runner", w.id, "--harness", "fake", "--fake-spec", sf, "--allow-development-isolation")
	s.check(t, "flow reaches accepted", c.Exit == 0, c)
	taskID := intent(flow, "create")
	task := obj(r.get("/api/v1/tasks/" + taskID))
	attempts := arr(task["attempts"])
	s.require(t, "flow persisted an attempt", len(attempts) == 1, task)
	a := obj(attempts[0])
	v := obj(r.get("/api/v1/attempts/" + str(obj(a["identity"])["attempt_id"])))
	d := obj(v["dispatch"])
	r.dispatches = append(r.dispatches, d)
	out := r.observe(d, w)
	s.check(t, "terminal succeeded and released", str(out["state"]) == "succeeded" && boolean(out["released"]), out)
	s.check(t, "head verification and acceptance set", str(obj(task["head"])["receipt_id"]) != "" && boolean(obj(obj(task["verification"])["status"])["verified"]) && str(task["phase"]) == "accepted", task)
	assertSequence(r, taskID, []string{"assigned", "starting", "running", "result_pending", "succeeded"})
	r.assertEdits(out, []object{{"path": "greeting.txt", "content": "hello, gaffer\n"}})
	d2 := r.simple(w, "edit")
	a2 := r.settle(d2)
	s.check(t, "second task dispatch succeeds", str(a2["state"]) == "succeeded", a2)
}
func assertSequence(r *installation, task string, want []string) {
	page := obj(r.get("/api/v1/events?task_id=" + task + "&limit=50"))
	got := []string{}
	for _, x := range arr(page["events"]) {
		m := obj(obj(x)["message"])
		if str(m["kind"]) == "assign" {
			got = append(got, "assigned")
		} else if str(m["kind"]) == "transition" {
			got = append(got, str(m["to"]))
		}
	}
	r.s.check(r.t, "durable transition sequence", jsonEqual(got, want), got)
}
func (r *installation) assertEdits(out object, edits []object) {
	artifacts := arr(out["artifacts"])
	r.s.require(r.t, "custody manifest available", len(artifacts) == 1, artifacts)
	manifest := obj(artifacts[0])
	r.s.add("artifact", "exact candidate manifest", time.Now(), manifest)
	for _, ed := range edits {
		path := str(ed["path"])
		if boolean(ed["delete"]) {
			found := false
			for _, p := range arr(manifest["deleted"]) {
				found = found || str(p) == path
			}
			r.s.check(r.t, "manifest deletion "+path, found, manifest["deleted"])
			continue
		}
		found := false
		entries := []any{}
		for _, category := range []string{"tracked", "untracked", "binary", "recovery"} {
			entries = append(entries, arr(manifest[category])...)
		}
		for _, entry := range entries {
			e := obj(entry)
			if str(e["path"]) != path {
				continue
			}
			found = true
			status, b, err := request(r.client, "GET", r.ownerURL+"/api/v1/artifacts/blobs/"+str(e["sha256"]), nil, nil)
			want := []byte(str(ed["content"]))
			if encoded := str(ed["content_base64"]); encoded != "" {
				want = decodeBase64(encoded)
			}
			r.s.check(r.t, "custody bytes "+path, status == 200 && err == nil && bytes.Equal(b, want) && digest(b) == str(e["sha256"]), object{"status": status, "bytes": len(b), "sha256": digest(b), "expected_sha256": digest(want)})
		}
		r.s.check(r.t, "manifest path "+path, found, manifest)
	}
}
func s05(t *testing.T, s *scenario) {
	r := newInstall(t, s, "", false)
	w := r.readyWorker("greeting")
	settings := object{"attempts": []object{{"mode": "hang", "edits": []object{}}, {"mode": "edit", "edits": []object{{"path": "greeting.txt", "content": "hello, gaffer\n"}}}}}
	d := r.task(w, "Set greeting after cancellation", []object{{"id": "greeting", "text": "greeting changes"}}, []string{"greeting.txt"}, settings)
	id := identity(d)
	r.waitAttempt(str(id["attempt_id"]), 15*time.Second, func(v object) bool { return str(v["state"]) == "running" })
	now := time.Now()
	stop := r.mustCLI("task", "stop", str(id["task_id"]))
	stopID := str(stop["stop_id"])
	if stopID == "" {
		stopID = str(stop["id"])
	}
	stopping := r.waitAttempt(str(id["attempt_id"]), 8*time.Second, func(v object) bool { return str(v["state"]) == "stopping" || terminal(v) })
	s.check(t, "cancel reflected within one second", time.Since(now) <= time.Second, object{"elapsed_ms": time.Since(now).Milliseconds(), "attempt": stopping})
	a := r.settle(d)
	s.check(t, "cancelled and released", str(a["state"]) == "cancelled" && boolean(a["released"]), a)
	r.get("/api/v1/stops/" + stopID + "?attempt_id=" + str(id["attempt_id"]))
	assertGuardian(w, d, 5*time.Second)
	blocked := r.cli("task", "retry", str(id["task_id"]))
	s.check(t, "sticky task pause refuses retry", blocked.Exit != 0, blocked)
	r.mustCLI("task", "resume", str(id["task_id"]))
	next := r.mustCLI("task", "retry", str(id["task_id"]))
	s.check(t, "retry has new identity and epoch", str(identity(next)["attempt_id"]) != str(id["attempt_id"]) && num(identity(next)["epoch"]) == 2, next)
	r.settle(next)
	r.observe(d, w)
}
func guardian(w *worker, d object) object {
	path := filepath.Join(w.state, "attempts", str(d["id"]), "guardian.json")
	return obj(readJSON(path))
}
func assertGuardian(w *worker, d object, timeout time.Duration) object {
	r := w.r
	var g object
	ok := waitFor(timeout, func() bool { g = guardian(w, d); return num(g["pgid"]) > 0 })
	r.s.require(r.t, "guardian durable receipt exists", ok, g)
	err := syscall.Kill(-int(num(g["pgid"])), 0)
	r.s.check(r.t, "guardian confirms empty process group", boolean(g["pgid_empty"]) && err == syscall.ESRCH, object{"guardian": g, "probe": fmt.Sprint(err)})
	r.s.add("guardian", "durable supervisor evidence", time.Now(), g)
	return g
}
