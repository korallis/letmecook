package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	authority "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/backup"
	"github.com/korallis/letmecook/internal/closedjson"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/jobs"
	"github.com/korallis/letmecook/internal/repositories"
	review "github.com/korallis/letmecook/internal/review"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	v "github.com/korallis/letmecook/internal/verification"
	"github.com/korallis/letmecook/internal/workflow"
)

func init() {
	commands["repo"] = repoCommand
	commands["runner"] = runnerCommand
	commands["task"] = taskCommand
	commands["attempt"] = attemptCommand
	commands["verify"] = verifyCommand
	commands["review"] = reviewCommand
	commands["daemon"] = daemonCommand
	commands["reconcile"] = reconcileCommand
	commands["backup"] = backupCommand
}

type header struct {
	Version   string `json:"version"`
	MessageID string `json:"message_id"`
}

func intent(g globals) header { return header{workflow.Version, g.MessageID} }
func flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}
func parseID(f *flag.FlagSet, args []string) (string, bool) {
	id := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id = args[0]
		args = args[1:]
	}
	if f.Parse(args) != nil {
		return "", false
	}
	if id == "" && f.NArg() == 1 {
		id = f.Arg(0)
	} else if f.NArg() != 0 {
		return "", false
	}
	return id, validID(id)
}
func usage(g globals, detail string) int { return failLocal(g, 2, "invalid_arguments", detail) }
func readJSON(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return closedjson.Decode(b, out, 65536, map[string]bool{"provider_cost_micros": true})
}

// Replies are bounded and checked for duplicate keys/invalid encoding before
// they are interpreted. Only documented optional legacy fields may be null.
func decodeReply(raw []byte, value any) error {
	return closedjson.Decode(raw, value, maxReply, map[string]bool{
		"provider_cost_micros": true, "exit_code": true, "refusal": true, "decision": true,
		"criteria": true, "paths": true, "operations": true, "attempts": true, "events": true,
		"evidence": true, "suggestions": true, "limitations": true, "reasons": true, "checks": true,
		"env_keys": true, "prefix": true, "records": true, "coverage": true, "evidence_ids": true,
		"ranked_alternatives": true, "capabilities": true, "required": true, "preferences": true,
		"evidence_refs": true, "unknowns": true, "systems": true, "targets": true,
	})
}
func call(ctx context.Context, g globals, method, path string, input any) (json.RawMessage, *cliError) {
	client := g.client
	var err error
	if client == nil {
		client, err = ownerClient(g)
		if err != nil {
			return nil, &cliError{0, "invalid_configuration", "explicit HTTPS endpoint, client certificate/key and daemon fingerprint are required"}
		}
		defer client.CloseIdleConnections()
	}
	var body []byte
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return nil, &cliError{0, "invalid_arguments", "request encoding failed"}
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, g.Endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, &cliError{0, "invalid_arguments", "invalid request"}
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, &cliError{0, "daemon_unavailable", "pinned mTLS request failed"}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxReply+1))
	if err != nil || len(raw) > maxReply || !json.Valid(raw) {
		return nil, &cliError{0, "invalid_response", "daemon response was not bounded JSON"}
	}
	var bounded any
	if decodeReply(raw, &bounded) != nil {
		return nil, &cliError{0, "invalid_response", "daemon response was not closed JSON"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var e struct {
			Version string `json:"version"`
			Error   string `json:"error"`
			Detail  string `json:"detail"`
		}
		if decodeReply(raw, &e) != nil {
			return nil, &cliError{0, "invalid_response", "daemon error did not match the error schema"}
		}
		if e.Error == "" {
			e.Error = "request_refused"
		}
		return nil, &cliError{response.StatusCode, e.Error, e.Detail}
	}
	return json.RawMessage(raw), nil
}
func request(ctx context.Context, g globals, command, method, path string, input any) int {
	body, e := call(ctx, g, method, path, input)
	if e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	return emit(g, command, body)
}
func getTask(ctx context.Context, g globals, id string) (store.Task, *cliError) {
	raw, e := call(ctx, g, "GET", "/api/v1/tasks/"+id, nil)
	var t store.Task
	if e == nil && decodeReply(raw, &t) != nil {
		e = &cliError{0, "invalid_response", "invalid task"}
	}
	return t, e
}
func readFail(g globals) int {
	return usage(g, "input file must contain bounded closed JSON of the documented type")
}

func repoCommand(ctx context.Context, g globals, args []string) int {
	if len(args) == 0 {
		return usage(g, "repo register|validate|show")
	}
	f := flags("repo " + args[0])
	if args[0] == "show" {
		id := f.String("id", "", "repository id")
		if f.Parse(args[1:]) != nil {
			return usage(g, "repo show ID")
		}
		if *id == "" && f.NArg() == 1 {
			*id = f.Arg(0)
		}
		if *id == "" || f.NArg() > 1 {
			return usage(g, "repo show ID")
		}
		return request(ctx, g, "repo.show", "GET", "/api/v1/repositories/"+url.PathEscape(*id), nil)
	}
	if args[0] != "register" && args[0] != "validate" {
		return usage(g, "repo register|validate|show")
	}
	path := f.String("profile", "", "approved repository profile JSON")
	revision := f.Int64("expected-revision", 0, "current repository revision")
	wait := f.Bool("wait", false, "wait for the durable job")
	if f.Parse(args[1:]) != nil || f.NArg() != 0 || *path == "" || *revision < 0 {
		return usage(g, "repo register|validate --profile FILE --expected-revision N")
	}
	var profile repositories.Profile
	if readJSON(*path, &profile) != nil {
		return readFail(g)
	}
	input := struct {
		header
		ExpectedRevision int64                `json:"expected_revision"`
		Profile          repositories.Profile `json:"profile"`
	}{intent(g), *revision, profile}
	route := "/api/v1/repositories"
	if args[0] == "validate" {
		route += "/validate"
	}
	raw, e := call(ctx, g, "POST", route, input)
	if e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	if *wait {
		raw, e = waitJob(ctx, g, raw)
		if e != nil {
			return fail(g, e.Status, e.Code, e.Detail)
		}
	}
	return emit(g, "repo."+args[0], raw)
}
func runnerCommand(ctx context.Context, g globals, args []string) int {
	if len(args) < 2 || args[0] != "facts" {
		return usage(g, "runner facts import|show")
	}
	f := flags("runner facts")
	switch args[1] {
	case "show":
		if len(args) == 2 {
			return request(ctx, g, "runner.facts.show", "GET", "/api/v1/runners", nil)
		}
		if len(args) != 3 {
			return usage(g, "runner facts show [ELIGIBILITY]")
		}
		return request(ctx, g, "runner.facts.show", "GET", "/api/v1/eligibility/"+url.PathEscape(args[2]), nil)
	case "import":
		file := f.String("file", "", "eligibility facts JSON")
		expected := f.Int64("expected-revision", 0, "current facts revision")
		if f.Parse(args[2:]) != nil || f.NArg() != 0 || *file == "" || *expected < 0 {
			return usage(g, "runner facts import --file FILE --expected-revision N")
		}
		var facts sc.Eligibility
		if readJSON(*file, &facts) != nil {
			return readFail(g)
		}
		return request(ctx, g, "runner.facts.import", "POST", "/api/v1/eligibility", struct {
			header
			ExpectedRevision int64          `json:"expected_revision"`
			Facts            sc.Eligibility `json:"facts"`
		}{intent(g), *expected, facts})
	}
	return usage(g, "runner facts import|show")
}

type values []string

func (v *values) String() string     { return strings.Join(*v, ",") }
func (v *values) Set(s string) error { *v = append(*v, s); return nil }
func criteria(items []string) ([]workflow.Criterion, error) {
	out := []workflow.Criterion{}
	for _, item := range items {
		id, text, ok := strings.Cut(item, "=")
		if !ok || id == "" || text == "" {
			return nil, fmt.Errorf("criterion must be id=text")
		}
		out = append(out, workflow.Criterion{ID: id, Text: text})
	}
	return out, nil
}
func taskInputFlags(f *flag.FlagSet) (repo, base, brief, harness, settings *string, criterion, paths, operations *values) {
	repo = f.String("repo", "", "registered repository")
	base = f.String("base", "", "pinned commit")
	brief = f.String("brief-file", "", "task brief text")
	harness = f.String("harness", "fake", "fake or opencode")
	settings = f.String("settings-file", "", "harness settings JSON")
	criterion, paths, operations = new(values), new(values), new(values)
	f.Var(criterion, "criterion", "id=text; repeatable")
	f.Var(paths, "path", "exact allowed path; repeatable")
	f.Var(operations, "operation", "read, write or verify; repeatable")
	return
}
func makeTaskInput(g globals, repo, base, briefFile, harness, settingsFile string, criterion, paths, operations []string) (workflow.TaskInput, error) {
	brief, err := os.ReadFile(briefFile)
	if err != nil {
		return workflow.TaskInput{}, err
	}
	cs, err := criteria(criterion)
	if err != nil {
		return workflow.TaskInput{}, err
	}
	settings := json.RawMessage(`{"attempts":[{"mode":"noop","edits":[]}]}`)
	if settingsFile != "" {
		if err = readJSON(settingsFile, &settings); err != nil {
			return workflow.TaskInput{}, err
		}
	} else if harness == "opencode" {
		return workflow.TaskInput{}, fmt.Errorf("opencode requires settings")
	}
	if len(operations) == 0 {
		operations = []string{"read", "verify", "write"}
	}
	return workflow.Normalize(workflow.TaskInput{Version: workflow.Version, MessageID: g.MessageID, Repository: repo, BaseCommit: base, Brief: string(brief), Criteria: cs, Paths: paths, Operations: operations, Harness: harness, Settings: settings})
}
func taskCommand(ctx context.Context, g globals, args []string) int {
	if len(args) == 0 {
		return usage(g, "task create|approve|dispatch|inspect|watch|stop|retry|list")
	}
	f := flags("task " + args[0])
	switch args[0] {
	case "create":
		repo, base, brief, harness, settings, criterion, paths, operations := taskInputFlags(f)
		if f.Parse(args[1:]) != nil || f.NArg() != 0 {
			return usage(g, "task create --repo ID --base SHA --brief-file FILE --criterion id=text --path PATH --harness fake|opencode --settings-file FILE")
		}
		input, err := makeTaskInput(g, *repo, *base, *brief, *harness, *settings, *criterion, *paths, *operations)
		if err != nil {
			return usage(g, "invalid task brief, scope or harness settings")
		}
		return request(ctx, g, "task.create", "POST", "/api/v1/tasks", input)
	case "list":
		after := f.String("after", "", "task cursor")
		limit := f.Int("limit", 32, "page size")
		if f.Parse(args[1:]) != nil || f.NArg() != 0 {
			return usage(g, "task list [--after UUID] [--limit N]")
		}
		return request(ctx, g, "task.list", "GET", "/api/v1/tasks?after="+url.QueryEscape(*after)+"&limit="+strconv.Itoa(*limit), nil)
	case "inspect", "stop", "retry":
		id, ok := parseID(f, args[1:])
		if !ok {
			return usage(g, "task "+args[0]+" UUID")
		}
		method, path, input := "GET", "/api/v1/tasks/"+id, any(nil)
		if args[0] != "inspect" {
			method = "POST"
			path += "/" + args[0]
			input = intent(g)
		}
		return request(ctx, g, "task."+args[0], method, path, input)
	case "watch":
		until := f.String("until", "terminal", "terminal or assigned")
		id, ok := parseID(f, args[1:])
		if !ok || (*until != "terminal" && *until != "assigned") {
			return usage(g, "task watch UUID [--until terminal|assigned]")
		}
		raw, e := watchTask(ctx, g, id, *until)
		if e != nil {
			return fail(g, e.Status, e.Code, e.Detail)
		}
		return emit(g, "task.watch", raw)
	case "approve":
		eligibility := f.String("eligibility", "", "published eligibility id")
		expected := f.String("expected-grant-id", "", "current grant head")
		allow := f.Bool("allow-development-isolation", false, "explicit development-only consent")
		budgetsFile := f.String("budgets-file", "", "explicit budgets JSON")
		expires := f.Int64("expires-ms", 0, "absolute grant expiry")
		id, ok := parseID(f, args[1:])
		if !ok || *eligibility == "" {
			return usage(g, "task approve UUID --eligibility ID [--expected-grant-id UUID] [--allow-development-isolation]")
		}
		var budgets *authority.Budgets
		if *budgetsFile != "" {
			budgets = &authority.Budgets{}
			if readJSON(*budgetsFile, budgets) != nil {
				return readFail(g)
			}
		}
		raw, e := approve(ctx, g, id, *eligibility, *expected, *allow, budgets, *expires)
		if e != nil {
			return fail(g, e.Status, e.Code, e.Detail)
		}
		return emit(g, "task.approve", raw)
	case "dispatch":
		grantID := f.String("grant-id", "", "approved grant UUID")
		revision := f.Int64("grant-revision", 0, "approved grant revision")
		attempt := f.Int64("attempt-ms", 60000, "single-attempt wall time")
		id, ok := parseID(f, args[1:])
		if !ok || !validID(*grantID) || *revision < 1 {
			return usage(g, "task dispatch UUID --grant-id UUID --grant-revision N [--attempt-ms N]")
		}
		return request(ctx, g, "task.dispatch", "POST", "/api/v1/tasks/"+id+"/dispatch", dispatchInput{intent(g), *grantID, *revision, *attempt})
	}
	return usage(g, "unknown task subcommand")
}

type dispatchInput struct {
	header
	GrantID       string `json:"grant_id"`
	GrantRevision int64  `json:"grant_revision"`
	AttemptMS     int64  `json:"attempt_ms"`
}

func approve(ctx context.Context, global globals, id, eligibility, expected string, allow bool, budgets *authority.Budgets, expires int64) (json.RawMessage, *cliError) {
	raw, e := call(ctx, global, "GET", "/api/v1/tasks/"+id+"/proposal?eligibility="+url.QueryEscape(eligibility), nil)
	if e != nil {
		return nil, e
	}
	var proposal workflow.Proposal
	if json.Unmarshal(raw, &proposal) != nil || proposal.Digests["proposal"] == "" {
		return nil, &cliError{0, "invalid_response", "invalid proposal"}
	}
	if !global.JSON {
		fmt.Fprintf(global.Err, "Proposal (not authority): %s\n", raw)
	}
	return call(ctx, global, "POST", "/api/v1/tasks/"+id+"/approve", struct {
		header
		ExpectedGrantID string             `json:"expected_grant_id"`
		EligibilityID   string             `json:"eligibility_id"`
		Budgets         *authority.Budgets `json:"budgets,omitempty"`
		ExpiresMS       int64              `json:"expires_ms,omitempty"`
		Allow           bool               `json:"allow_development_isolation"`
		Digest          string             `json:"proposal_digest"`
	}{intent(global), expected, eligibility, budgets, expires, allow, proposal.Digests["proposal"]})
}
func pausePoll(ctx context.Context) bool {
	t := time.NewTimer(500 * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func watchTask(ctx context.Context, g globals, id, until string) (json.RawMessage, *cliError) {
	if g.client == nil {
		client, err := ownerClient(g)
		if err != nil {
			return nil, &cliError{0, "invalid_configuration", "explicit pinned owner TLS configuration is required"}
		}
		g.client = client
		defer client.CloseIdleConnections()
	}

	for {
		raw, e := call(ctx, g, "GET", "/api/v1/tasks/"+id, nil)
		if e != nil {
			return nil, e
		}
		var task store.Task
		if decodeReply(raw, &task) != nil {
			return nil, &cliError{0, "invalid_response", "invalid task"}
		}
		if len(task.Attempts) > 0 {
			state := task.Attempts[len(task.Attempts)-1].State
			if until == "assigned" && state == "assigned" || slices.Contains([]string{"succeeded", "failed", "cancelled", "expired"}, string(state)) {
				return raw, nil
			}
			if state == "unknown" {
				return nil, &cliError{409, "reconciliation_required", "attempt outcome unknown"}
			}
		}
		if !pausePoll(ctx) {
			return nil, &cliError{0, "timeout", "task remains pending; inspect or watch again"}
		}
	}
}
func waitJob(ctx context.Context, g globals, submitted json.RawMessage) (json.RawMessage, *cliError) {
	if g.client == nil {
		client, err := ownerClient(g)
		if err != nil {
			return nil, &cliError{0, "invalid_configuration", "explicit pinned owner TLS configuration is required"}
		}
		g.client = client
		defer client.CloseIdleConnections()
	}

	var ref struct {
		ID string `json:"job_id"`
	}
	if decodeReply(submitted, &ref) != nil || !validID(ref.ID) {
		return nil, &cliError{0, "invalid_response", "invalid job receipt"}
	}
	for {
		raw, e := call(ctx, g, "GET", "/api/v1/jobs/"+ref.ID, nil)
		if e != nil {
			return nil, e
		}
		var job jobs.Job
		if decodeReply(raw, &job) != nil {
			return nil, &cliError{0, "invalid_response", "invalid job"}
		}
		switch job.State {
		case "succeeded":
			return job.Result, nil
		case "failed":
			code, detail, _ := strings.Cut(job.Error, ":")
			status := 422
			if code == "daemon_restart" || code == "store_unavailable" {
				status = 503
			}
			return nil, &cliError{status, code, detail}
		}
		if !pausePoll(ctx) {
			return nil, &cliError{0, "timeout", "job remains pending; inspect again"}
		}
	}
}
func attemptCommand(ctx context.Context, g globals, args []string) int {
	if len(args) == 0 {
		return usage(g, "attempt cancel|inspect|stream|artifacts")
	}
	f := flags("attempt")
	after := f.Int64("after", 0, "stream sequence")
	limit := f.Int("limit", 32, "stream window")
	id, ok := parseID(f, args[1:])
	if !ok {
		return usage(g, "attempt "+args[0]+" UUID")
	}
	path := "/api/v1/attempts/" + id
	switch args[0] {
	case "inspect":
		return request(ctx, g, "attempt.inspect", "GET", path, nil)
	case "cancel":
		return request(ctx, g, "attempt.cancel", "POST", path+"/cancel", intent(g))
	case "stream":
		return request(ctx, g, "attempt.stream", "GET", path+"/stream?after="+strconv.FormatInt(*after, 10)+"&limit="+strconv.Itoa(*limit), nil)
	case "artifacts":
		return request(ctx, g, "attempt.artifacts", "GET", path+"/artifacts", nil)
	}
	return usage(g, "unknown attempt subcommand")
}
func verifyCommand(ctx context.Context, g globals, args []string) int {
	if len(args) == 0 {
		return usage(g, "verify run|show")
	}
	f := flags("verify")
	selection := f.String("expected-selection", "", "current candidate selection")
	wait := f.Bool("wait", false, "wait for committed verification")
	id, ok := parseID(f, args[1:])
	if !ok {
		return usage(g, "verify run TASK_UUID | verify show REPORT_UUID")
	}
	if args[0] == "show" {
		return request(ctx, g, "verify.show", "GET", "/api/v1/verifications/"+id, nil)
	}
	if args[0] != "run" {
		return usage(g, "verify run|show")
	}
	raw, e := call(ctx, g, "POST", "/api/v1/tasks/"+id+"/verify", struct {
		header
		Selection string `json:"expected_selection"`
	}{intent(g), *selection})
	if e == nil && *wait {
		raw, e = waitJob(ctx, g, raw)
	}
	if e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	return emit(g, "verify.run", raw)
}
func reviewCommand(ctx context.Context, g globals, args []string) int {
	if len(args) == 0 {
		return usage(g, "review accept|reject|show")
	}
	f := flags("review")
	verification := f.String("verification-id", "", "report UUID")
	override := f.String("override-with-reason", "", "record explicit limitation; never bypasses store acceptance checks")
	notes := f.String("notes", "", "owner review notes")
	coverageFile := f.String("coverage-file", "", "criterion coverage JSON")
	id, ok := parseID(f, args[1:])
	if !ok {
		return usage(g, "review accept|reject|show TASK_UUID")
	}
	if args[0] == "show" {
		return request(ctx, g, "review.show", "GET", "/api/v1/tasks/"+id+"/review", nil)
	}
	if args[0] != "accept" && args[0] != "reject" {
		return usage(g, "review accept|reject|show")
	}
	raw, e := reviewRequest(ctx, g, id, args[0], *verification, *override, *notes, *coverageFile)
	if e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	return emit(g, "review."+args[0], raw)
}
func reviewRequest(ctx context.Context, g globals, id, action, reportID, override, notes, coverageFile string) (json.RawMessage, *cliError) {
	task, e := getTask(ctx, g, id)
	if e != nil {
		return nil, e
	}
	raw, e := call(ctx, g, "GET", "/api/v1/tasks/"+id+"/verification", nil)
	if e != nil {
		return nil, e
	}
	var current struct {
		Report v.Report `json:"report"`
		Status v.Status `json:"status"`
	}
	if decodeReply(raw, &current) != nil {
		return nil, &cliError{0, "invalid_response", "invalid current verification"}
	}
	report := current.Report
	if reportID != "" && reportID != report.ID {
		raw, e = call(ctx, g, "GET", "/api/v1/verifications/"+reportID, nil)
		if e != nil {
			return nil, e
		}
		if decodeReply(raw, &report) != nil {
			return nil, &cliError{0, "invalid_response", "invalid verification"}
		}
	}
	// Only the server knows the live generation, selection and artifact custody.
	status := current.Status
	status.Verified = status.Verified && report.ID == current.Report.ID && report.Candidate == current.Report.Candidate
	if action == "accept" && !status.Verified && override == "" {
		return nil, &cliError{422, "verification_required", "accept requires verified evidence; an override request cannot bypass the service gate"}
	}
	limitations := append([]string{}, report.Limitations...)
	if override != "" {
		limitations = append(limitations, "owner override requested: "+override)
	}
	coverage := []review.Coverage{}
	if coverageFile != "" {
		if readJSON(coverageFile, &coverage) != nil {
			return nil, &cliError{0, "invalid_arguments", "invalid coverage JSON"}
		}
	} else {
		for _, c := range task.Brief.Criteria {
			coverage = append(coverage, review.Coverage{CriterionID: c.ID, Status: "not-covered", EvidenceIDs: review.EvidenceIDs(report), Explanation: "owner reviewed exact candidate and report"})
		}
		if action == "accept" {
			for n := range coverage {
				coverage[n].Status = "covered"
			}
		}
	}
	return call(ctx, g, "POST", "/api/v1/tasks/"+id+"/review", struct {
		header
		VerificationID string            `json:"verification_id"`
		SelectionID    string            `json:"selection_id"`
		Action         string            `json:"action"`
		Coverage       []review.Coverage `json:"coverage"`
		Limitations    []string          `json:"limitations"`
		Notes          string            `json:"notes"`
	}{intent(g), report.ID, report.Candidate.SelectionID, action, coverage, limitations, notes})
}
func daemonCommand(ctx context.Context, g globals, args []string) int {
	if len(args) == 0 {
		return usage(g, "daemon status|pause|resume|stop-all")
	}
	f := flags("daemon")
	reason := f.String("reason", "operator", "reason")
	confirm := f.Bool("confirm-source-fenced", false, "confirm restored source is fenced")
	if f.Parse(args[1:]) != nil || f.NArg() != 0 {
		return usage(g, "daemon status|pause|resume|stop-all")
	}
	switch args[0] {
	case "status":
		return request(ctx, g, "daemon.status", "GET", "/api/v1/daemon", nil)
	case "stop-all":
		return request(ctx, g, "daemon.stop-all", "POST", "/api/v1/daemon/stop", intent(g))
	case "pause", "resume":
		return request(ctx, g, "daemon."+args[0], "POST", "/api/v1/daemon/"+args[0], struct {
			header
			Reason  string `json:"reason"`
			Confirm bool   `json:"confirm_source_fenced,omitempty"`
		}{intent(g), *reason, *confirm})
	}
	return usage(g, "unknown daemon subcommand")
}
func reconcileCommand(ctx context.Context, g globals, args []string) int {
	if len(args) == 1 && args[0] == "status" {
		return request(ctx, g, "reconcile.status", "GET", "/api/v1/reconcile", nil)
	}
	if len(args) == 0 || args[0] != "release" {
		return usage(g, "reconcile status | reconcile release UUID --proof FILE")
	}
	f := flags("reconcile release")
	file := f.String("proof", "", "owner-reviewed reconciliation proof")
	id, ok := parseID(f, args[1:])
	if !ok || *file == "" {
		return usage(g, "reconcile release UUID --proof FILE")
	}
	var proof store.Reconciliation
	if readJSON(*file, &proof) != nil {
		return readFail(g)
	}
	return request(ctx, g, "reconcile.release", "POST", "/api/v1/reconcile/"+id+"/release", struct {
		header
		Proof store.Reconciliation `json:"proof"`
	}{intent(g), proof})
}
func backupCommand(ctx context.Context, g globals, args []string) int {
	if len(args) == 0 {
		return usage(g, "backup create|verify")
	}
	f := flags("backup")
	destination := f.String("destination", "", "backup destination")
	dir := f.String("backup", "", "existing backup directory")
	if f.Parse(args[1:]) != nil || f.NArg() != 0 {
		return usage(g, "backup create --destination DIR | backup verify --backup DIR")
	}
	switch args[0] {
	case "create":
		if *destination == "" {
			return usage(g, "backup create --destination DIR")
		}
		return request(ctx, g, "backup.create", "POST", "/api/v1/backup", struct {
			header
			Destination string `json:"destination"`
		}{intent(g), *destination})
	case "verify":
		if *dir == "" {
			return usage(g, "backup verify --backup DIR")
		}
		manifest, err := backup.Verify(*dir)
		if err != nil {
			return failLocal(g, 1, "backup_refused", err.Error())
		}
		return emit(g, "backup.verify", manifest)
	}
	return usage(g, "unknown backup subcommand")
}
func identityWorkflow(ctx context.Context, g globals, args []string) int {
	f := flags("identity " + args[0])
	switch args[0] {
	case "invite":
		fingerprint := f.String("fingerprint", "", "runner leaf fingerprint")
		if f.Parse(args[1:]) != nil || f.NArg() != 0 || !validPin(*fingerprint) {
			return usage(g, "identity invite --fingerprint SHA256")
		}
		return request(ctx, g, "identity.invite", "POST", "/api/v1/identity/enrollments", struct {
			header
			Fingerprint string `json:"fingerprint"`
		}{header{i.Version, g.MessageID}, *fingerprint})
	case "enroll":
		token := f.String("token", "", "one-use invitation token (visible in process arguments; prefer --token-file)")
		tokenFile := f.String("token-file", "", "private 0600 file containing a one-use invitation token")
		if f.Parse(args[1:]) != nil || f.NArg() != 0 || (*token == "") == (*tokenFile == "") {
			return usage(g, "identity enroll --token-file FILE (0600), or --token TOKEN")
		}
		if *tokenFile != "" {
			value, err := enrollmentToken(*tokenFile)
			if err != nil {
				return usage(g, "token file must be a bounded regular file with mode 0600")
			}
			*token = value
		}
		return request(ctx, g, "identity.enroll", "POST", "/api/v1/identity/enroll", struct {
			header
			Token string `json:"token"`
		}{header{i.Version, g.MessageID}, *token})
	case "update":
		id := f.String("id", "", "principal UUID")
		revision := f.Int64("revision", 0, "expected principal revision")
		action := f.String("action", "", "enable, disable, revoke or rotate")
		fingerprint := f.String("fingerprint", "", "new fingerprint for rotation")
		if f.Parse(args[1:]) != nil || f.NArg() != 0 || !validID(*id) || *revision < 1 {
			return usage(g, "identity update --id UUID --revision N --action ACTION [--fingerprint SHA256]")
		}
		return request(ctx, g, "identity.update", "POST", "/api/v1/identity/update", struct {
			header
			ID          string `json:"id"`
			Revision    int64  `json:"revision"`
			Action      string `json:"action"`
			Fingerprint string `json:"fingerprint"`
		}{header{i.Version, g.MessageID}, *id, *revision, *action, *fingerprint})
	}
	return usage(g, "unknown identity subcommand")
}

// enrollmentToken never prints the token or returns it in an error. Validate the
// opened inode too, so replacing a checked path cannot bypass the private mode.
func enrollmentToken(path string) (string, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 {
		return "", fmt.Errorf("invalid token file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Mode().Perm() != 0600 {
		return "", fmt.Errorf("invalid token file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 1025))
	value := strings.TrimSpace(string(b))
	if err != nil || len(b) > 1024 || value == "" || strings.ContainsAny(value, " \t\r\n\x00") {
		return "", fmt.Errorf("invalid token file")
	}
	return value, nil
}
