package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/closedjson"
	c "github.com/korallis/letmecook/internal/control"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/jobs"
	"github.com/korallis/letmecook/internal/reconcile"
	repositories "github.com/korallis/letmecook/internal/repositories"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	"github.com/korallis/letmecook/internal/workflow"
	p "github.com/korallis/letmecook/schemas/execution"
)

const WorkflowVersion = workflow.Version

// Same durable-inbox wake key as S1 httpapi.InboxKey; reconcile at integration.
const workflowInboxKey = "execution.inbox"

type commandHeader struct {
	Version   string `json:"version"`
	MessageID string `json:"message_id"`
}
type repositoryCommand struct {
	commandHeader
	ExpectedRevision int64                `json:"expected_revision"`
	Profile          repositories.Profile `json:"profile"`
}
type eligibilityCommand struct {
	commandHeader
	ExpectedRevision int64          `json:"expected_revision"`
	Facts            sc.Eligibility `json:"facts"`
}
type approveCommand struct {
	commandHeader
	ExpectedGrantID           string     `json:"expected_grant_id"`
	EligibilityID             string     `json:"eligibility_id"`
	Budgets                   *g.Budgets `json:"budgets,omitempty"`
	ExpiresMS                 int64      `json:"expires_ms,omitempty"`
	AllowDevelopmentIsolation bool       `json:"allow_development_isolation"`
	ProposalDigest            string     `json:"proposal_digest"`
}
type restrictCommand struct {
	commandHeader
	ExpectedGrantID string     `json:"expected_grant_id"`
	Envelope        g.Envelope `json:"envelope"`
}
type invalidateCommand struct {
	commandHeader
	ExpectedGrantID string `json:"expected_grant_id"`
	Reason          string `json:"reason"`
}
type dispatchCommand struct {
	commandHeader
	GrantID       string `json:"grant_id"`
	GrantRevision int64  `json:"grant_revision"`
	AttemptMS     int64  `json:"attempt_ms"`
}
type pauseCommand struct {
	commandHeader
	Reason              string `json:"reason"`
	ConfirmSourceFenced bool   `json:"confirm_source_fenced,omitempty"`
}

// One receipt writer per store prevents simultaneous HTTP retries from racing
// their receipt commits. Domain keys remain the recovery source after a crash.
var ownerSerial sync.Map

func ownerMutex(s *store.Store) chan struct{} {
	v, _ := ownerSerial.LoadOrStore(s, make(chan struct{}, 1))
	return v.(chan struct{})
}
func authOwner(ctx context.Context, d Deps, a Actor) error {
	who, err := d.Store.Authenticate(ctx, a.Fingerprint)
	if err != nil {
		return err
	}
	if who.ID != a.ID || who.Role != "owner" || !who.Enabled || who.Revoked {
		return i.Denied
	}
	return nil
}
func workflowError(err error) *Error {
	if err == nil {
		return nil
	}
	var refusal *g.Refusal
	if errors.As(err, &refusal) {
		status := 422
		switch refusal.Code {
		case "malformed", "invalid_query", "invalid_id", "invalid_bound", "invalid_assessment":
			status = 400
		case "identity_denied", "boundary_refused":
			status = 403
		case "not_found":
			status = 404
		case "revision_conflict", "identity_conflict", "current_assignment", "reconciliation_required", "stopped", "stop_latched", "candidate_conflict", "paused", "eligibility_stale", "superseded", "stale_generation", "stale_decision", "stale_evidence":
			status = 409
		case "cursor_expired":
			status = 410
		case "store_unavailable", "store_closed", "busy", "fixture_only":
			status = 503
		}
		return &Error{status, refusal.Code, refusal.Field}
	}
	switch {
	case errors.Is(err, p.IdentityConflict):
		return &Error{409, "identity_conflict", "message_id"}
	case errors.Is(err, p.ReconciliationRequired):
		return &Error{409, "reconciliation_required", "attempt"}
	case errors.Is(err, p.StaleAttempt):
		return &Error{409, "stale_attempt", "attempt"}
	case errors.Is(err, sql.ErrNoRows):
		return &Error{404, "not_found", ""}
	case errors.Is(err, i.Denied), errors.Is(err, repositories.Denied):
		return &Error{403, "identity_denied", ""}
	case errors.Is(err, i.Invalid), errors.Is(err, repositories.Invalid), errors.Is(err, p.Malformed):
		return &Error{400, "malformed", ""}
	case errors.Is(err, i.Conflict):
		return &Error{409, "revision_conflict", ""}
	case errors.Is(err, repositories.ProtectedPath):
		return &Error{422, "envelope_violation", "paths"}
	case errors.Is(err, repositories.RemoteChanged):
		return &Error{409, "revision_conflict", "repository"}
	case errors.Is(err, c.ErrFenced):
		return &Error{409, "stop_latched", ""}
	}
	// Older verification APIs use fixed diagnostic errors; never reflect supplied
	// filesystem paths, credentials or driver errors into a network response.
	text := err.Error()
	if strings.Contains(text, "identity conflict") {
		return &Error{409, "identity_conflict", ""}
	}
	if strings.Contains(text, "candidate") && (strings.Contains(text, "conflict") || strings.Contains(text, "replaced") || strings.Contains(text, "stale")) {
		return &Error{409, "candidate_conflict", ""}
	}
	if strings.Contains(text, "superseded") {
		return &Error{409, "superseded", "verification"}
	}
	if strings.Contains(text, "unverified candidate") {
		return &Error{422, "verification_required", "verification"}
	}
	return &Error{503, "store_unavailable", ""}
}
func decodeWorkflow(body []byte, dst any) error {
	return closedjson.Decode(body, dst, 65536, map[string]bool{"provider_cost_micros": true})
}

// OwnerMutation is shared with the backup lane: decode first, commit the domain
// intent, then retain its exact successful response. It never stores a failure
// as success and never promotes a background job's queued state to completed.
func OwnerMutation(d Deps, route string, decode func([]byte) (string, any, error), commit func(context.Context, Actor, Request, any) (any, int, error)) func(context.Context, Actor, Request) (any, *Error) {
	return func(ctx context.Context, a Actor, req Request) (any, *Error) {
		if len(req.Query) != 0 {
			return nil, &Error{400, "invalid_query", ""}
		}
		for _, value := range req.Path {
			if !p.ValidID(value) {
				return nil, &Error{400, "invalid_id", ""}
			}
		}
		id, input, err := decode(req.Body)
		if err != nil {
			return nil, &Error{400, "malformed", ""}
		}
		if !p.ValidID(id) {
			return nil, &Error{400, "invalid_id", "message_id"}
		}
		raw, _ := json.Marshal(input)
		// Sort nested object keys as well as typed outer fields, preserving numbers.
		var canonical any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if decoder.Decode(&canonical) != nil {
			return nil, &Error{400, "malformed", ""}
		}
		raw, _ = json.Marshal(canonical)
		hash, _ := sc.Digest(struct {
			Route string
			Path  map[string]string
			Body  json.RawMessage
		}{route, req.Path, raw})
		lock := ownerMutex(d.Store)
		select {
		case lock <- struct{}{}:
		case <-ctx.Done():
			return nil, &Error{503, "store_unavailable", "request_timeout"}
		}
		defer func() { <-lock }()
		if err = authOwner(ctx, d, a); err != nil {
			return nil, workflowError(err)
		}
		ctx = store.OwnerContext(ctx, a.Fingerprint)
		status, err := d.Store.Status(ctx)
		if err != nil {
			return nil, workflowError(err)
		}
		old, err := d.Store.OwnerCommand(ctx, id)
		if err == nil {
			if old.RequestSHA256 != hash || old.Route != route || old.PrincipalID != a.ID {
				return nil, &Error{409, "identity_conflict", "message_id"}
			}
			if old.Generation != status.Generation {
				return nil, &Error{409, "stale_generation", "message_id"}
			}
			return Response{Status: old.Status, Body: old.Response, Header: http.Header{"Idempotent-Replay": []string{"true"}}}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, workflowError(err)
		}
		result, code, err := commit(ctx, a, req, input)
		if err != nil {
			return nil, workflowError(err)
		}
		// Wake only after the domain commit, even if retaining the response fails.
		if d.Hub != nil {
			d.Hub.Notify("owner")
			d.Hub.Notify(workflowInboxKey)
		}
		body, err := json.Marshal(result)
		if err != nil {
			return nil, &Error{503, "store_unavailable", "response"}
		}
		if err = d.Store.RecordOwnerCommand(ctx, store.OwnerCommand{MessageID: id, Generation: status.Generation, PrincipalID: a.ID, Route: route, RequestSHA256: hash, Status: code, Response: body}); err != nil {
			return nil, workflowError(err)
		}
		return Response{Status: code, Body: json.RawMessage(body)}, nil
	}
}
func ownerMutation[T any](d Deps, route string, fn func(context.Context, Actor, Request, T) (any, int, error)) func(context.Context, Actor, Request) (any, *Error) {
	return OwnerMutation(d, route, func(raw []byte) (string, any, error) {
		var input T
		if err := decodeWorkflow(raw, &input); err != nil {
			return "", nil, err
		}
		var head commandHeader
		if err := json.Unmarshal(raw, &head); err != nil {
			return "", nil, err
		}
		if head.Version != WorkflowVersion {
			return "", nil, g.Deny("malformed", "version")
		}
		return head.MessageID, input, nil
	}, func(ctx context.Context, a Actor, r Request, v any) (any, int, error) { return fn(ctx, a, r, v.(T)) })
}
func ownerRoute(method, path string, fn func(context.Context, Actor, Request) (any, *Error)) Route {
	return Route{Method: method, Pattern: path, Role: "owner", Class: Control, Handle: fn}
}
func actorReference(a Actor) string { return "owner-" + strings.ReplaceAll(a.ID, "-", "") }
func init()                         { Register("workflow", workflowRoutes) }
func workflowRoutes(d Deps) []Route {
	routes := []Route{
		ownerRoute("POST", "/api/v1/tasks", ownerMutation(d, "task.create", func(ctx context.Context, a Actor, r Request, in workflow.TaskInput) (any, int, error) {
			normalized, err := workflow.Normalize(in)
			if err != nil {
				return nil, 0, err
			}
			b := store.TaskBrief{TaskID: normalized.MessageID, Repository: normalized.Repository, BaseCommit: normalized.BaseCommit, Brief: normalized.Brief, Paths: normalized.Paths, Operations: normalized.Operations, Harness: normalized.Harness, Settings: normalized.Settings}
			for _, c := range normalized.Criteria {
				b.Criteria = append(b.Criteria, store.Criterion{ID: c.ID, Text: c.Text})
			}
			out, err := d.Store.CreateTask(ctx, a.Fingerprint, b)
			return out, 201, err
		})),
		ownerRoute("POST", "/api/v1/eligibility", ownerMutation(d, "eligibility.publish", func(ctx context.Context, a Actor, r Request, in eligibilityCommand) (any, int, error) {
			if in.Facts.Isolation.Qualification == "development" && !d.Policy.Development(in.Facts.Isolation.ID) {
				return nil, 0, g.Deny("development_isolation_refused", "profile")
			}
			err := d.Store.PublishEligibility(ctx, a.Fingerprint, in.ExpectedRevision, in.Facts)
			return in.Facts, 201, err
		})),
		ownerRoute("POST", "/api/v1/tasks/{id}/approve", ownerMutation(d, "task.approve", func(ctx context.Context, a Actor, r Request, in approveCommand) (any, int, error) {
			proposal, err := workflow.BuildGrant(ctx, d.Store, r.Path["id"], in.EligibilityID)
			if err != nil {
				return nil, 0, err
			}
			if proposal.Digests["proposal"] != in.ProposalDigest {
				return nil, 0, g.Deny("revision_conflict", "proposal_digest")
			}
			facts, err := d.Store.Eligibility(ctx, in.EligibilityID)
			if err != nil {
				return nil, 0, err
			}
			if facts.Isolation.Qualification == "development" && (!in.AllowDevelopmentIsolation || !d.Policy.Development(facts.Isolation.ID)) {
				return nil, 0, g.Deny("development_isolation_refused", "profile")
			}
			grant := proposal.Grant
			grant.ID = in.MessageID
			grant.Actor = actorReference(a)
			if in.ExpectedGrantID != "" {
				old, err := d.Store.ExecutionGrant(ctx, in.ExpectedGrantID)
				if err != nil {
					return nil, 0, err
				}
				if old.TaskID != r.Path["id"] {
					return nil, 0, g.Deny("revision_conflict", "expected_grant_id")
				}
				grant.Revision = old.Revision + 1
			}
			if in.Budgets != nil {
				grant.Envelope.Budgets = *in.Budgets
			}
			if in.ExpiresMS != 0 {
				grant.Envelope.ExpiresMS = in.ExpiresMS
			}
			if err = grant.Validate(); err != nil {
				return nil, 0, err
			}
			request := g.Request{GrantID: grant.ID, TaskID: grant.TaskID, GrantRevision: grant.Revision, Action: "execute", Envelope: grant.Envelope}
			if err = sc.CheckDispatchWithPolicy(request, proposal.Decision, facts, time.Now().UnixMilli(), d.Policy); err != nil {
				return nil, 0, err
			}
			if err = authOwner(ctx, d, a); err != nil {
				return nil, 0, err
			}
			grant, err = d.Store.ApproveExecution(store.DecisionContext(ctx, proposal.Decision), in.ExpectedGrantID, grant)
			if err != nil {
				return nil, 0, err
			}
			if err = d.Store.RetainExecutionDecision(ctx, a.Fingerprint, grant.ID, proposal.Decision); err != nil {
				return nil, 0, err
			}
			return struct {
				Grant    g.Grant     `json:"grant"`
				Decision sc.Decision `json:"decision"`
			}{grant, proposal.Decision}, 201, nil
		})),
		ownerRoute("POST", "/api/v1/tasks/{id}/restrict", ownerMutation(d, "task.restrict", func(ctx context.Context, a Actor, r Request, in restrictCommand) (any, int, error) {
			old, err := d.Store.ExecutionGrant(ctx, in.ExpectedGrantID)
			if err != nil {
				return nil, 0, err
			}
			if old.TaskID != r.Path["id"] {
				return nil, 0, g.Deny("revision_conflict", "expected_grant_id")
			}
			decision, err := d.Store.ExecutionDecision(ctx, old.ID)
			if err != nil {
				return nil, 0, err
			}
			grant := g.Grant{ID: in.MessageID, TaskID: old.TaskID, Revision: old.Revision + 1, Actor: old.Actor, Envelope: in.Envelope}
			if err = authOwner(ctx, d, a); err != nil {
				return nil, 0, err
			}
			grant, err = d.Store.RestrictExecution(store.DecisionContext(ctx, decision), in.ExpectedGrantID, grant)
			if err == nil {
				err = d.Store.RetainExecutionDecision(ctx, a.Fingerprint, grant.ID, decision)
			}
			return grant, 201, err
		})),
		ownerRoute("POST", "/api/v1/tasks/{id}/invalidate", ownerMutation(d, "task.invalidate", func(ctx context.Context, a Actor, r Request, in invalidateCommand) (any, int, error) {
			if err := authOwner(ctx, d, a); err != nil {
				return nil, 0, err
			}
			err := d.Store.InvalidateExecution(ctx, r.Path["id"], in.ExpectedGrantID, actorReference(a), in.Reason)
			return map[string]string{"grant_id": in.ExpectedGrantID, "reason": in.Reason}, 200, err
		})),
		ownerRoute("POST", "/api/v1/tasks/{id}/dispatch", ownerMutation(d, "task.dispatch", func(ctx context.Context, a Actor, r Request, in dispatchCommand) (any, int, error) {
			grant, err := d.Store.ExecutionGrant(ctx, in.GrantID)
			if err != nil {
				return nil, 0, err
			}
			if grant.TaskID != r.Path["id"] || grant.Revision != in.GrantRevision {
				return nil, 0, g.Deny("revision_conflict", "grant_id/grant_revision")
			}
			decision, err := d.Store.ExecutionDecision(ctx, grant.ID)
			if err != nil {
				return nil, 0, err
			}
			request, err := workflow.BuildDispatch(ctx, d.Store, workflow.Proposal{Grant: grant, Decision: decision}, in.MessageID, in.AttemptMS)
			if err != nil {
				return nil, 0, err
			}
			if err = authOwner(ctx, d, a); err != nil {
				return nil, 0, err
			}
			if state, err := d.Store.Paused(ctx); err != nil {
				return nil, 0, err
			} else if state.Paused {
				return nil, 0, g.Deny("paused", "daemon")
			}
			out, err := d.Store.Dispatch(ctx, store.DispatchRequest{ID: request.ID, Request: request.Request, Decision: request.Decision, Allowance: request.Allowance})
			return out, 201, err
		})),
		ownerRoute("POST", "/api/v1/tasks/{id}/retry", ownerMutation(d, "task.retry", func(ctx context.Context, a Actor, r Request, in commandHeader) (any, int, error) {
			request, err := reconcile.PlanRetry(ctx, reconcile.Deps{Store: d.Store}, r.Path["id"])
			if errors.Is(err, reconcile.ErrNotImplemented) {
				return nil, 0, g.Deny("retry_unavailable", "reconcile")
			}
			if err != nil {
				return nil, 0, err
			}
			request.ID = in.MessageID
			if err = authOwner(ctx, d, a); err != nil {
				return nil, 0, err
			}
			if state, err := d.Store.Paused(ctx); err != nil {
				return nil, 0, err
			} else if state.Paused {
				return nil, 0, g.Deny("paused", "daemon")
			}
			out, err := d.Store.Dispatch(ctx, request)
			return out, 201, err
		})),
	}
	for _, path := range []string{"/api/v1/repositories", "/api/v1/repositories/validate"} {
		kind := "repo-register"
		if strings.HasSuffix(path, "/validate") {
			kind = "repo-validate"
		}
		routes = append(routes, ownerRoute("POST", path, ownerMutation(d, kind, func(ctx context.Context, a Actor, r Request, in repositoryCommand) (any, int, error) {
			if d.Jobs == nil {
				return nil, 0, g.Deny("store_unavailable", "jobs")
			}
			if err := in.Profile.Validate(); err != nil {
				return nil, 0, err
			}
			raw, _ := json.Marshal(repositoryJob{Actor: a.Fingerprint, Command: in})
			job, err := d.Jobs.Submit(ctx, jobs.Job{ID: in.MessageID, Kind: kind, SubjectID: in.Profile.ID, Result: raw})
			return map[string]string{"job_id": job.ID}, 202, err
		})))
	}
	for _, entry := range []struct {
		path, kind string
		control    c.Kind
	}{{"/api/v1/tasks/{id}/stop", "task.stop", c.PauseTask}, {"/api/v1/attempts/{id}/cancel", "attempt.cancel", c.CancelAttempt}, {"/api/v1/daemon/stop", "daemon.stop", c.GlobalStop}} {
		routes = append(routes, ownerRoute("POST", entry.path, ownerMutation(d, entry.kind, func(ctx context.Context, a Actor, r Request, in commandHeader) (any, int, error) {
			request := c.Request{ID: in.MessageID, Kind: entry.control, Cause: "operator"}
			switch entry.control {
			case c.PauseTask:
				if _, err := d.Store.Task(ctx, r.Path["id"]); err != nil {
					return nil, 0, err
				}
				request.TaskID = r.Path["id"]
			case c.CancelAttempt:
				attempt, err := d.Store.AttemptView(ctx, r.Path["id"])
				if err != nil {
					return nil, 0, err
				}
				request.TaskID = attempt.Identity.TaskID
				request.AttemptID = attempt.Identity.AttemptID
			}
			receipt, err := d.Store.RequestStop(ctx, a.Fingerprint, request)
			// The stop intent is already durable even if sticky admission fencing
			// fails afterward. Do not hide that committed stop from the runner.
			if err == nil && d.Hub != nil {
				d.Hub.Notify(workflowInboxKey)
			}
			if err == nil && request.Kind == c.PauseTask {
				err = d.Store.StopDispatch(ctx, a.Fingerprint, request.TaskID)
			}
			return receipt, 202, err
		})))
	}
	for _, paused := range []bool{true, false} {
		path := "pause"
		if !paused {
			path = "resume"
		}
		routes = append(routes, ownerRoute("POST", "/api/v1/daemon/"+path, ownerMutation(d, "daemon."+path, func(ctx context.Context, a Actor, r Request, in pauseCommand) (any, int, error) {
			out, err := d.Store.SetPaused(ctx, a.Fingerprint, in.MessageID, paused, in.Reason, in.ConfirmSourceFenced)
			return out, 200, err
		})))
	}
	return routes
}

// strictQuery rejects repeated and unknown query arguments, including duplicate
// empty values, before any domain operation is attempted.
func strictQuery(r Request, allowed ...string) error {
	for key, values := range r.Query {
		found := false
		for _, a := range allowed {
			if key == a {
				found = true
			}
		}
		if !found || len(values) != 1 {
			return g.Deny("invalid_query", "query")
		}
	}
	return nil
}
func queryBound(r Request, key string, fallback, max int64) (int64, error) {
	raw := r.Query.Get(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 || n > max {
		return 0, g.Deny("invalid_query", key)
	}
	return n, nil
}
