package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	authority "github.com/korallis/letmecook/internal/authority"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	"github.com/korallis/letmecook/internal/workflow"
)

func init() { commands["flow"] = flowCommand }

type transcript struct {
	Step      string `json:"step"`
	Request   any    `json:"request"`
	Response  any    `json:"response"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

func flowCommand(ctx context.Context, g globals, args []string) int {
	if len(args) == 0 || args[0] != "run" {
		return usage(g, "flow run --flow-id ID --repo ID --base SHA --brief-file FILE --criterion id=text --path PATH --runner UUID --harness fake|opencode")
	}
	f := flags("flow run")
	repo, base, brief, harness, settings, criterion, paths, operations := taskInputFlags(f)
	runner := f.String("runner", "", "enrolled runner UUID")
	flowID := f.String("flow-id", "", "stable flow intent id")
	fakeSpec := f.String("fake-spec", "", "fake attempt script JSON")
	model := f.String("model", "", "gateway model for opencode")
	allow := f.Bool("allow-development-isolation", false, "explicit development-only consent")
	output := f.String("transcript", "", "append NDJSON to private file instead of stdout")
	until := f.String("until", "accepted", "assigned or accepted")
	attemptMS := f.Int64("attempt-ms", 60000, "attempt wall time")
	if f.Parse(args[1:]) != nil || f.NArg() != 0 || *flowID == "" || len(*flowID) > 128 || strings.ContainsAny(*flowID, "\x00\r\n") || !validID(*runner) || (*until != "assigned" && *until != "accepted") || *attemptMS <= 0 {
		return usage(g, "flow requires --flow-id, --runner and a valid task scope")
	}
	if *fakeSpec != "" {
		if *harness != "fake" || *settings != "" {
			return usage(g, "--fake-spec requires fake harness and no --settings-file")
		}
		*settings = *fakeSpec
	}
	createGlobals := g
	createGlobals.MessageID = workflow.IntentID(*flowID, "create")
	if *harness == "opencode" && *settings == "" { // model is typed configuration, never shell syntax
		if *model == "" {
			return usage(g, "opencode flow requires --model or --settings-file")
		}
		// Build below without a temporary model-settings file.
		*settings = ""
	}
	var input workflow.TaskInput
	var err error
	if *harness == "opencode" && *settings == "" {
		briefBody, readErr := os.ReadFile(*brief)
		cs, criteriaErr := criteria(*criterion)
		if readErr != nil || criteriaErr != nil {
			return usage(g, "invalid brief or criteria")
		}
		ops := []string(*operations)
		if len(ops) == 0 {
			ops = []string{"read", "verify", "write"}
		}
		modelSettings, _ := json.Marshal(map[string]string{"model": *model})
		input, err = workflow.Normalize(workflow.TaskInput{Version: workflow.Version, MessageID: createGlobals.MessageID, Repository: *repo, BaseCommit: *base, Brief: string(briefBody), Criteria: cs, Paths: *paths, Operations: ops, Harness: *harness, Settings: modelSettings})
	} else {
		input, err = makeTaskInput(createGlobals, *repo, *base, *brief, *harness, *settings, *criterion, *paths, *operations)
	}
	if err != nil {
		return usage(g, "invalid task brief, scope or harness settings")
	}
	var writer io.Writer = g.Out
	if *output != "" {
		file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return failLocal(g, 3, "output_unavailable", "cannot open transcript")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return failLocal(g, 1, "transcript_refused", "transcript must be a private regular file")
		}
		writer = file
	}
	record := func(step string, request, response any, start time.Time) int {
		if err := json.NewEncoder(writer).Encode(transcript{step, request, response, time.Since(start).Milliseconds()}); err != nil {
			return failLocal(g, 3, "output_unavailable", "cannot write flow transcript")
		}
		if file, ok := writer.(*os.File); ok && *output != "" {
			if err := file.Sync(); err != nil {
				return failLocal(g, 3, "output_unavailable", "cannot sync flow transcript")
			}
		}
		return 0
	}
	step := func(name, method, path string, in any) (json.RawMessage, *cliError) {
		start := time.Now()
		raw, e := call(ctx, g, method, path, in)
		var response any = raw
		if e != nil {
			response = e
		}
		if code := record(name, in, response, start); code != 0 {
			return nil, &cliError{0, "output_unavailable", "cannot record flow step"}
		}
		return raw, e
	}
	if _, e := step("create", "POST", "/api/v1/tasks", input); e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	raw, e := call(ctx, g, "GET", "/api/v1/runners", nil)
	if e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	var runners []store.RunnerView
	if decodeReply(raw, &runners) != nil {
		return failLocal(g, 3, "invalid_response", "invalid runner facts")
	}
	eligibility := ""
	for _, r := range runners {
		if r.Principal.ID != *runner {
			continue
		}
		for _, facts := range r.Eligibility {
			if facts.Repository.Repository == *repo && facts.Repository.BaseCommit == *base && facts.Route.Harness == *harness {
				if *model != "" {
					found := false
					for _, target := range facts.Route.Targets {
						if target.Model == *model {
							found = true
						}
					}
					if !found {
						continue
					}
				}
				if eligibility != "" {
					return fail(g, 422, "no_eligible_tuple", "more than one matching tuple; use task approve --eligibility")
				}
				eligibility = facts.ID
			}
		}
	}
	if eligibility == "" {
		return fail(g, 422, "no_eligible_tuple", "import matching runner facts first")
	}
	raw, e = call(ctx, g, "GET", "/api/v1/tasks/"+input.MessageID+"/proposal?eligibility="+eligibility, nil)
	if e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	var proposal workflow.Proposal
	if decodeReply(raw, &proposal) != nil {
		return failLocal(g, 3, "invalid_response", "invalid proposal")
	}
	approveID := workflow.IntentID(*flowID, "approve")
	approval := struct {
		header
		ExpectedGrantID string `json:"expected_grant_id"`
		EligibilityID   string `json:"eligibility_id"`
		Allow           bool   `json:"allow_development_isolation"`
		Digest          string `json:"proposal_digest"`
	}{header{workflow.Version, approveID}, "", eligibility, *allow, proposal.Digests["proposal"]}
	raw, e = step("approve", "POST", "/api/v1/tasks/"+input.MessageID+"/approve", approval)
	if e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	var approved struct {
		Grant    authority.Grant `json:"grant"`
		Decision sc.Decision     `json:"decision"`
	}
	if decodeReply(raw, &approved) != nil {
		return failLocal(g, 3, "invalid_response", "invalid approval")
	}
	dispatch := dispatchInput{header{workflow.Version, workflow.IntentID(*flowID, "dispatch")}, approved.Grant.ID, approved.Grant.Revision, *attemptMS}
	if _, e = step("dispatch", "POST", "/api/v1/tasks/"+input.MessageID+"/dispatch", dispatch); e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	start := time.Now()
	watchUntil := "terminal"
	if *until == "assigned" {
		watchUntil = "assigned"
	}
	raw, e = watchTask(ctx, g, input.MessageID, watchUntil)
	var observed any = raw
	if e != nil {
		observed = e
	}
	if code := record("watch", map[string]string{"task_id": input.MessageID, "until": watchUntil}, observed, start); code != 0 {
		return code
	}
	if e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	if *until == "assigned" {
		return 0
	}
	var completed store.Task
	if decodeReply(raw, &completed) != nil || len(completed.Attempts) == 0 {
		return failLocal(g, 3, "invalid_response", "missing attempt")
	}
	state := string(completed.Attempts[len(completed.Attempts)-1].State)
	if !slices.Contains([]string{"succeeded", "failed"}, state) {
		return fail(g, 409, "reconciliation_required", "attempt did not produce a candidate")
	}
	verifyID := workflow.IntentID(*flowID, "verify")
	verification := struct {
		header
		Selection string `json:"expected_selection"`
	}{header{workflow.Version, verifyID}, ""}
	raw, e = step("verify", "POST", "/api/v1/tasks/"+input.MessageID+"/verify", verification)
	if e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	start = time.Now()
	raw, e = waitJob(ctx, g, raw)
	observed = raw
	if e != nil {
		observed = e
	}
	if code := record("verification", map[string]string{"job_id": verifyID}, observed, start); code != 0 {
		return code
	}
	if e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	g.MessageID = workflow.IntentID(*flowID, "accept")
	start = time.Now()
	raw, e = reviewRequest(ctx, g, input.MessageID, "accept", "", "", "flow owner acceptance", "")
	observed = raw
	if e != nil {
		observed = e
	}
	if code := record("accept", map[string]string{"message_id": g.MessageID, "task_id": input.MessageID}, observed, start); code != 0 {
		return code
	}
	if e != nil {
		return fail(g, e.Status, e.Code, e.Detail)
	}
	return 0
}
