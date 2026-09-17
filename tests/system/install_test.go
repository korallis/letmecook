//go:build system

package system

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type installation struct {
	t                                                           *testing.T
	s                                                           *scenario
	root, state, artifacts, ownerURL, execURL, pin, gateway, ca string
	daemon, mock                                                *process
	runners                                                     []*worker
	client                                                      *http.Client
	auto                                                        bool
	reads                                                       map[string]string
	proxy                                                       *faultProxy
	dispatches                                                  []object
	proxies                                                     []*faultProxy
	readyAt                                                     time.Time
}
type worker struct {
	r                                                                  *installation
	repo, id, root, state, profile, policy, base, eligibility, harness string
	revision                                                           int64
	p                                                                  *process
	facts                                                              object
	client                                                             *http.Client
	proxy                                                              *faultProxy
}

func newInstall(t *testing.T, s *scenario, suffix string, auto bool) *installation {
	t.Helper()
	root := s.Root
	if suffix != "" {
		root = filepath.Join(root, suffix)
	}
	s.require(t, "fresh scenario installation", os.MkdirAll(root, 0700) == nil, root)
	r := &installation{t: t, s: s, root: root, state: filepath.Join(root, "state"), artifacts: filepath.Join(root, "artifacts"), ownerURL: "https://" + freeAddress(t), execURL: "https://" + freeAddress(t), auto: auto, reads: map[string]string{}}
	t.Cleanup(func() {
		// Capture public persisted state even when an earlier prerequisite failed.
		for _, d := range r.dispatches {
			for _, w := range r.runners {
				if str(obj(d["decision"])["eligibility_id"]) == w.eligibility || len(r.runners) == 1 {
					r.observe(d, w)
					break
				}
			}
		}
		for _, w := range r.runners {
			s.add("journal", "retained runner journal evidence", time.Now(), journalSummary(w))
			w.p.stop(false)
			s.add("process", "runner shutdown", time.Now(), w.p.logs())
		}
		for _, p := range r.proxies {
			p.close()
		}
		r.daemon.stop(false)
		s.add("process", "daemon shutdown", time.Now(), r.daemon.logs())
		r.mock.stop(false)
		r.invariants()
	})
	for _, name := range []string{"owner", "daemon"} {
		args := []string{"identity", "keygen", "--cert", filepath.Join(root, name+".crt"), "--key", filepath.Join(root, name+".key"), "--json"}
		if name == "daemon" {
			args = append(args, "--server", "--host", "127.0.0.1")
		}
		c := s.command(time.Minute, filepath.Join(binDir, "gaffer"), args...)
		s.require(t, "keygen "+name, c.Exit == 0, c)
	}
	r.pin = pinFile(filepath.Join(root, "daemon.crt"))
	c := s.command(time.Minute, filepath.Join(binDir, "gafferd"), "--state-dir", r.state, "--artifacts-dir", r.artifacts, "--bootstrap-owner-cert", filepath.Join(root, "owner.crt"))
	s.require(t, "offline owner bootstrap", c.Exit == 0, c)
	_ = os.Mkdir(filepath.Join(root, "backups"), 0700)
	if os.Getenv("GAFFER_LIVE_GATEWAY") == "1" && s.ID == "S-14" {
		r.gateway = os.Getenv("GAFFER_GATEWAY_CONFIG")
		s.require(t, "explicit external live gateway config", r.gateway != "", object{"configured": r.gateway != ""})
	} else {
		key := filepath.Join(root, "mock-token")
		s.require(t, "synthetic mock key", os.WriteFile(key, []byte("synthetic-"+uuid()), 0600) == nil, "private synthetic token")
		var e error
		r.mock, e = startProcess(filepath.Join(binDir, "gaffer-runner"), "mock-gateway", "--listen", "127.0.0.1:0", "--key-file", key, "--models", "gpt-6-astra")
		s.require(t, "start mock gateway", e == nil, fmt.Sprint(e))
		var info object
		ok := waitFor(10*time.Second, func() bool {
			info = obj(parse([]byte(strings.TrimSpace(r.mock.out.String()))))
			return str(info["url"]) != "" || !r.mock.alive()
		})
		s.require(t, "mock HTTPS ready", ok && str(info["url"]) != "", r.mock.logs())
		r.ca = str(info["ca_file"])
		r.gateway = filepath.Join(root, "gateway.json")
		s.require(t, "write synthetic gateway config", writeJSON(r.gateway, object{"version": "gateway-config-v1", "gateway_id": "system-mock", "base_url": info["url"], "credential_ref": object{"kind": "file", "path": key}, "protocols": []string{"chat_completions"}, "models": []string{"gpt-6-astra"}, "ca_bundle": r.ca}) == nil, "mock gateway only")
	}
	r.client = httpClient(t, root, "owner", "http/1.1")
	r.startDaemon()
	c = s.command(time.Minute, "bash", filepath.Join(checkout, "tests/system/fixtures/repos/make-repos.sh"), filepath.Join(root, "repos"))
	s.require(t, "pinned repository generation", c.Exit == 0, c)
	return r
}
func (r *installation) daemonArgs() []string {
	a := []string{"--state-dir", r.state, "--artifacts-dir", r.artifacts, "--listen", strings.TrimPrefix(r.ownerURL, "https://"), "--execution-listen", strings.TrimPrefix(r.execURL, "https://"), "--endpoint", r.ownerURL, "--tls-cert", filepath.Join(r.root, "daemon.crt"), "--tls-key", filepath.Join(r.root, "daemon.key"), "--allow-development-profile", devProfile, "--verification-isolation-profile", devProfile, "--gateway-config", r.gateway, "--backup-dir", filepath.Join(r.root, "backups")}
	if r.auto {
		a = append(a, "--auto-retry")
	}
	return a
}
func (r *installation) startDaemon() {
	r.t.Helper()
	now := time.Now()
	p, e := startProcess(filepath.Join(binDir, "gafferd"), r.daemonArgs()...)
	r.s.require(r.t, "spawn daemon", e == nil, fmt.Sprint(e))
	r.daemon = p
	ok := waitFor(15*time.Second, func() bool {
		status, _, _ := request(r.client, "GET", r.ownerURL+"/api/v1/daemon", nil, nil)
		if status == 200 {
			r.readyAt = time.Now()
		}
		return status == 200 || !p.alive()
	})
	r.s.require(r.t, "both listeners ready", ok && p.alive() && strings.Contains(p.out.String(), "execution https://"), p.logs())
	r.s.add("process", "daemon ready", now, p.logs())
	r.get("/api/v1/daemon")
}
func (r *installation) cliAs(role string, args ...string) result {
	all := []string{"--endpoint", r.ownerURL, "--cert", filepath.Join(r.root, role+".crt"), "--key", filepath.Join(r.root, role+".key"), "--daemon-fingerprint", r.pin, "--json", "--timeout", "90s"}
	return r.s.command(100*time.Second, filepath.Join(binDir, "gaffer"), append(all, args...)...)
}
func (r *installation) cli(args ...string) result { return r.cliAs("owner", args...) }
func resultObject(c result) object                { return obj(obj(c.Value)["result"]) }
func (r *installation) mustCLI(args ...string) object {
	r.t.Helper()
	c := r.cli(args...)
	r.s.require(r.t, strings.Join(args[:min(2, len(args))], " "), c.Exit == 0, c)
	return resultObject(c)
}
func (r *installation) get(path string) any {
	now := time.Now()
	code, b, e := request(r.client, "GET", r.ownerURL+path, nil, nil)
	v := parse(b)
	key := fmt.Sprintf("%d:%s:%v", code, b, e)
	if r.reads[path] != key {
		r.s.add("read", path, now, object{"status": code, "body": v, "error": fmt.Sprint(e)})
		r.reads[path] = key
	}
	return v
}
func (r *installation) job(id string) object {
	r.t.Helper()
	var out object
	ok := waitFor(65*time.Second, func() bool {
		out = obj(r.get("/api/v1/jobs/" + id))
		return str(out["state"]) == "succeeded" || str(out["state"]) == "failed"
	})
	r.s.require(r.t, "durable job completes", ok && str(out["state"]) == "succeeded", out)
	return out
}
func (r *installation) addWorker(repo, harness string) *worker {
	r.t.Helper()
	role := "runner-" + repo
	w := &worker{r: r, repo: repo, root: filepath.Join(r.root, "roots", repo), state: filepath.Join(r.root, "runner-"+repo), eligibility: repo + "-pair", harness: harness}
	w.policy = filepath.Join(w.state, "policy.json")
	w.profile = filepath.Join(w.state, "repository.json")
	for _, p := range []string{w.root, w.state} {
		r.s.require(r.t, "private runner directory", os.MkdirAll(p, 0700) == nil, p)
	}
	c := r.s.command(time.Minute, filepath.Join(binDir, "gaffer"), "identity", "keygen", "--cert", filepath.Join(r.root, role+".crt"), "--key", filepath.Join(r.root, role+".key"), "--json")
	r.s.require(r.t, "runner keygen", c.Exit == 0, c)
	invite := r.mustCLI("identity", "invite", "--fingerprint", pinFile(filepath.Join(r.root, role+".crt")))
	w.id = str(invite["runner_id"])
	tokenFile := filepath.Join(w.state, "enrollment-token")
	r.s.require(r.t, "private enrollment token", os.WriteFile(tokenFile, []byte(str(invite["token"])), 0600) == nil, "0600 token file, no argv token")
	c = r.cliAs(role, "identity", "enroll", "--token-file", tokenFile)
	_ = os.Remove(tokenFile)
	r.s.require(r.t, "runner enroll", c.Exit == 0, c)
	principal := resultObject(c)
	r.s.check(r.t, "enrolled runner starts disabled", !boolean(principal["enabled"]), principal)
	r.mustCLI("identity", "update", "--id", w.id, "--revision", fmt.Sprint(num(principal["revision"])), "--action", "enable")
	profilePath := filepath.Join(r.root, "repos", repo+".repo-profile.json")
	if _, e := os.Stat(profilePath); e != nil {
		profilePath = filepath.Join(r.root, "repos", repo+"-profile.json")
	}
	profile := obj(readJSON(profilePath))
	if profile == nil || str(profile["id"]) == "" { // Generator puts skeletons alongside bare repositories.
		matches, _ := filepath.Glob(filepath.Join(r.root, "repos", "*", "repo-profile.json"))
		r.s.require(r.t, "generated profile location", false, object{"path": profilePath, "found": matches})
	}
	profile["runner_roots"] = []object{{"runner_id": w.id, "root": w.root}}
	w.base = str(obj(profile["base"])["commit"])
	r.s.require(r.t, "write local repository profile", writeJSON(w.profile, profile) == nil, w.profile)
	reg := r.mustCLI("repo", "register", "--profile", w.profile, "--expected-revision", "0")
	r.job(str(reg["job_id"]))
	stored := obj(r.get("/api/v1/repositories/" + repo))
	r.s.check(r.t, "repository revision one", num(stored["revision"]) == 1, stored)
	validation := r.mustCLI("repo", "validate", "--profile", w.profile, "--expected-revision", "1")
	j := r.job(str(validation["job_id"]))
	ph := str(obj(j["result"])["profile_digest"])
	r.s.require(r.t, "public validation profile digest", len(ph) == 64, j)
	policy := policyFor(w, stored, ph)
	r.s.require(r.t, "write independent local policy", writeJSON(w.policy, policy) == nil, w.policy)
	// facts increments an already valid local revision. Install the explicitly
	// unauthenticated policy mirror at revision 1 first; it cannot admit execution.
	r.mustCLI("runner", "facts", "import", "--file", w.policy, "--expected-revision", "0")
	w.revision = 1
	r.runners = append(r.runners, w)
	w.client = httpClient(r.t, r.root, role, "execution-provisional-v2")
	return w
}
func policyFor(w *worker, profile object, profileDigest string) object {
	hash := strings.Repeat("a", 64)
	rev := object{"number": 1, "sha256": hash}
	now := time.Now().UnixMilli()
	target := object{"provider": "system-mock", "model": "gpt-6-astra", "billing": "gateway-managed"}
	route := object{"route_ref": "worker-dev", "profile_ref": "system-mock", "route_revision": 1, "policy": rev, "router_build": hash, "graph_digest": hash, "evidence": rev, "harness": w.harness, "protocol": "chat_completions", "settings_digest": hash, "isolation": devProfile, "limits_profile": "gateway-local-bounds-v1", "limits_authority": "operator", "targets": []object{target}}
	paths := []string{"greeting.txt", "empty.txt", "README.md"}
	criteria := []string{"greeting", "check", "isolation"}
	for _, file := range []string{"corpus-v1.json", "recovery-100-v1.json"} {
		f := obj(readJSON(filepath.Join(checkout, "tests/system/fixtures", file)))
		for _, v := range arr(f["tasks"]) {
			t := obj(v)
			if str(t["repo"]) == w.repo {
				for _, p := range arr(t["paths"]) {
					paths = append(paths, str(p))
				}
				for _, c := range arr(t["criteria"]) {
					criteria = append(criteria, str(obj(c)["id"]))
				}
			}
		}
	}
	envelope := object{"repository": w.repo, "base_commit": w.base, "brief": rev, "plan": rev, "route_decision": rev, "criterion_ids": sortedSet(criteria), "task_kinds": []string{"code"}, "paths": sortedSet(paths), "operations": []string{"read", "verify", "write"}, "systems": []string{}, "runners": []string{w.id}, "routes": []object{route}, "selection": "pinned", "budgets": object{"requests": 12, "attempts": 3, "subattempts": 12, "retries": 2, "concurrency": 1, "request_bytes": 1048576, "response_bytes": 8388608, "total_ms": 360000, "attempt_ms": 120000, "first_output_ms": 60000, "idle_ms": 60000, "provider_output_tokens": 0, "provider_cost_micros": nil}, "not_before_ms": now - 10000, "expires_ms": now + 6*60*60*1000}
	return object{"id": w.eligibility, "revision": 1, "repository": object{"repository": w.repo, "revision": 1, "profile_digest": profileDigest, "remote": profile["remote"], "base_commit": w.base, "runner_root": object{"runner_id": w.id, "root": w.root}}, "enabled": true, "runner_boot": uuid(), "local_policy": rev, "local_envelope": envelope, "config": rev, "route": route, "paths": []object{{"target": target, "compatible": true, "capabilities": []string{"tools"}, "context_tokens": 32768, "local_bounds": true, "provider_output_bound": false, "provider_cost_bound": false}}, "capabilities": []string{"tools"}, "isolation": object{"id": devProfile, "revision": rev, "runtime_digest": hash, "observed_digest": hash, "kind": "native", "supported": false, "docker_required": false, "qualification": "development", "controls": []string{"controlled-egress", "external-supervisor", "secret-separation", "tree-termination", "workspace-only"}}, "capacity": object{"cpu": 1000, "memory_bytes": 1073741824, "disk_bytes": 1073741824, "processes": 32}, "concurrency": 1, "router_authenticated": false, "availability": "available", "valid_until_ms": now + 6*60*60*1000}
}
func (w *worker) start(profile string) {
	r := w.r
	r.t.Helper()
	role := "runner-" + w.repo
	endpoint := r.execURL
	if w.proxy != nil {
		endpoint = w.proxy.endpoint()
	}
	args := []string{"serve", "--state-dir", w.state, "--daemon", endpoint, "--daemon-fingerprint", r.pin, "--cert", filepath.Join(r.root, role+".crt"), "--key", filepath.Join(r.root, role+".key"), "--repository-root", w.root, "--isolation-profile", profile, "--harness", w.harness, "--repository-profile", w.profile, "--policy", w.policy, "--gateway-config", r.gateway}
	if w.harness == "opencode" {
		args = append(args, "--opencode-bin", "/Users/leebarry/.opencode/bin/opencode")
	}
	old := str(obj(readJSON(filepath.Join(w.state, "boot.json")))["runner_boot"])
	var e error
	w.p, e = startProcess(filepath.Join(binDir, "gaffer-runner"), args...)
	r.s.require(r.t, "start real runner", e == nil, fmt.Sprint(e))
	ok := waitFor(10*time.Second, func() bool {
		return str(obj(readJSON(filepath.Join(w.state, "boot.json")))["runner_boot"]) != old || !w.p.alive()
	})
	r.s.require(r.t, "new runner boot persisted", ok && w.p.alive(), w.p.logs())
}
func (w *worker) session() object {
	for _, v := range arr(w.r.get("/api/v1/runners")) {
		r := obj(v)
		if str(obj(r["principal"])["id"]) == w.id {
			return obj(r["session"])
		}
	}
	return nil
}
func (w *worker) waitMode(mode string) {
	w.r.t.Helper()
	var view object
	ok := waitFor(15*time.Second, func() bool { view = w.session(); return str(view["mode"]) == mode || !w.p.alive() })
	w.r.s.require(w.r.t, "runner session "+mode, ok && str(view["mode"]) == mode, object{"session": view, "process": w.p.logs()})
}
func (w *worker) importFacts() {
	r := w.r
	r.t.Helper()
	args := []string{"facts", "--state-dir", w.state, "--gateway-config", r.gateway}
	if r.ca != "" {
		args = append(args, "--gateway-ca", r.ca)
	}
	c := r.s.command(30*time.Second, filepath.Join(binDir, "gaffer-runner"), args...)
	r.s.require(r.t, "measure real authenticated facts", c.Exit == 0, c)
	w.facts = obj(c.Value)
	path := filepath.Join(w.state, "measured-facts.json")
	r.s.require(r.t, "persist measured facts", writeJSON(path, w.facts) == nil, path)
	r.s.check(r.t, "real gateway probe and development label", boolean(w.facts["router_authenticated"]) && str(obj(w.facts["isolation"])["qualification"]) == "development" && !boolean(obj(w.facts["isolation"])["supported"]), w.facts)
	r.mustCLI("runner", "facts", "import", "--file", path, "--expected-revision", fmt.Sprint(w.revision))
	w.revision = num(w.facts["revision"])
	w.waitMode("normal")
}
func (r *installation) readyWorker(repo string) *worker {
	w := r.addWorker(repo, "fake")
	w.start(devProfile)
	w.waitMode("recovery_only")
	w.importFacts()
	return w
}
func (w *worker) executionState(dispatch string) object {
	sess := w.session()
	code, b, e := request(w.client, "GET", w.r.execURL+"/x/v1/state?dispatch_id="+dispatch, nil, map[string]string{"X-Gaffer-Session": str(sess["session_id"])})
	v := obj(parse(b))
	w.r.s.add("read", "execution state", time.Now(), object{"status": code, "body": v, "error": fmt.Sprint(e)})
	return v
}

func (r *installation) task(w *worker, brief string, criteria []object, paths []string, settings any) object {
	r.t.Helper()
	id := uuid()
	bf := filepath.Join(r.root, id+"-brief.txt")
	sf := filepath.Join(r.root, id+"-settings.json")
	_ = os.WriteFile(bf, []byte(brief), 0600)
	_ = writeJSON(sf, settings)
	args := []string{"task", "create", "--repo", w.repo, "--base", w.base, "--brief-file", bf, "--harness", w.harness, "--settings-file", sf, "--message-id", id}
	for _, c := range criteria {
		args = append(args, "--criterion", str(c["id"])+"="+str(c["text"]))
	}
	for _, p := range paths {
		args = append(args, "--path", p)
	}
	r.mustCLI(args...)
	a := r.mustCLI("task", "approve", id, "--eligibility", w.eligibility, "--allow-development-isolation")
	g := obj(a["grant"])
	d := r.mustCLI("task", "dispatch", id, "--grant-id", str(g["id"]), "--grant-revision", fmt.Sprint(num(g["revision"])), "--attempt-ms", "120000")
	r.dispatches = append(r.dispatches, d)
	return d
}
func fakeSettings(mode string) object {
	return object{"attempts": []object{{"mode": mode, "edits": []object{{"path": "greeting.txt", "content": "hello, gaffer\n"}}}}}
}
func (r *installation) simple(w *worker, mode string) object {
	return r.task(w, "Set greeting.txt to hello, gaffer", []object{{"id": "greeting", "text": "greeting is hello, gaffer"}}, []string{"greeting.txt"}, fakeSettings(mode))
}
func identity(d object) object { return obj(obj(d["assignment"])["identity"]) }
func (r *installation) waitAttempt(id string, timeout time.Duration, predicate func(object) bool) object {
	r.t.Helper()
	var v object
	ok := waitFor(timeout, func() bool { v = obj(r.get("/api/v1/attempts/" + id)); return predicate(v) || terminal(v) })
	r.s.require(r.t, "attempt reaches expected state", ok && predicate(v), v)
	return v
}
func terminal(v object) bool {
	return strings.Contains("|succeeded|failed|cancelled|expired|", "|"+str(v["state"])+"|") && str(v["state"]) != ""
}
func (r *installation) settle(d object) object {
	return r.waitAttempt(str(identity(d)["attempt_id"]), 45*time.Second, terminal)
}
func (r *installation) observe(d object, w *worker) object {
	ident := identity(d)
	id := str(ident["attempt_id"])
	a := obj(r.get("/api/v1/attempts/" + id))
	task := obj(r.get("/api/v1/tasks/" + str(ident["task_id"])))
	stream := r.streamEvidence(id, ident, terminal(a))
	artifacts := r.get("/api/v1/attempts/" + id + "/artifacts")
	usage := r.get("/api/v1/attempts/" + id + "/usage")
	ex := w.executionState(str(d["id"]))
	out := object{"identity": a["identity"], "state": a["state"], "revision": a["revision"], "released": a["released"], "acknowledged": a["acknowledged"], "stream": stream, "artifacts": artifacts, "usage": usage, "execution": ex, "task": task}
	stops := []object{}
	for _, target := range arr(ex["stop_targets"]) {
		stopID := str(obj(obj(target)["cancel"])["stop_id"])
		if stopID == "" {
			continue
		}
		view := obj(r.get("/api/v1/stops/" + stopID + "?attempt_id=" + id))
		stops = append(stops, view)
		if remote := str(view["RemoteWork"]); remote != "" {
			out["remote_work"] = remote
		}
	}
	out["stops"] = stops
	r.s.add("observation", "persisted attempt evidence", time.Now(), out)
	r.s.check(r.t, "retained approved identity", compact(a["identity"]) == compact(ident), a["identity"])
	page := obj(r.get("/api/v1/events?task_id=" + str(ident["task_id"]) + "&limit=50"))
	var revision int64
	monotonic := true
	for _, ev := range arr(page["events"]) {
		e := obj(ev)
		m := obj(e["message"])
		if str(obj(m["identity"])["attempt_id"]) != id {
			continue
		}
		monotonic = monotonic && num(e["revision"]) == revision+1
		revision = num(e["revision"])
	}
	r.s.check(r.t, "contiguous durable event revisions", monotonic && revision >= num(a["revision"]) && revision > 0, object{"attempt_revision": a["revision"], "last_event_revision": revision})
	return out
}
func (r *installation) verifyReview(task string, accept bool) object {
	r.t.Helper()
	job := r.mustCLI("verify", "run", task)
	j := r.job(str(job["job_id"]))
	r.s.add("verification", "job evidence", time.Now(), j)
	current := obj(r.get("/api/v1/tasks/" + task))
	summary := obj(current["verification"])
	reportID := str(summary["id"])
	if reportID != "" {
		r.get("/api/v1/verifications/" + reportID)
	}
	if accept {
		r.s.check(r.t, "verification is verified", boolean(obj(summary["status"])["verified"]), summary)
		r.mustCLI("review", "accept", task, "--verification-id", reportID)
	} else {
		r.mustCLI("review", "reject", task, "--verification-id", reportID, "--notes", "Expected negative fixture outcome")
	}
	return obj(r.get("/api/v1/tasks/" + task))
}
func asObjects(v any) []object {
	out := []object{}
	for _, x := range arr(v) {
		out = append(out, obj(x))
	}
	return out
}
func asStrings(v any) []string {
	out := []string{}
	for _, x := range arr(v) {
		out = append(out, str(x))
	}
	return out
}
func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
