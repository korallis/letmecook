// Package workflow composes persisted owner intent. A proposal is not authority.
package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/execwire"
	sc "github.com/korallis/letmecook/internal/scheduler"
	p "github.com/korallis/letmecook/schemas/execution"
)

const Version = "workflow-provisional-v1"

type Criterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type TaskInput struct {
	Version    string          `json:"version"`
	MessageID  string          `json:"message_id"`
	Repository string          `json:"repository"`
	BaseCommit string          `json:"base_commit"`
	Brief      string          `json:"brief"`
	Criteria   []Criterion     `json:"criteria"`
	Paths      []string        `json:"paths"`
	Operations []string        `json:"operations"`
	Harness    string          `json:"harness"`
	Settings   json.RawMessage `json:"settings"`
}
type Proposal struct {
	Grant    g.Grant           `json:"grant"`
	Decision sc.Decision       `json:"decision"`
	Digests  map[string]string `json:"digests"`
}
type Reader interface {
	BriefInput(context.Context, string) (TaskInput, error)
	Eligibility(context.Context, string) (sc.Eligibility, error)
}

// IntentID derives a stable UUIDv4-shaped domain key, not a fresh authorization.
func IntentID(key, step string) string {
	b := sha256.Sum256([]byte(key + ":" + step))
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
func digest(value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Normalize rejects ambiguous paths and sorts the explicitly selected sets. No
// default operations, paths or acceptance criteria are invented by the service.
func Normalize(in TaskInput) (TaskInput, error) {
	if in.Version != Version || !p.ValidID(in.MessageID) || !g.ValidActor(in.Repository) || len(in.Brief) == 0 || len(in.Brief) > 65536 || strings.ContainsRune(in.Brief, 0) {
		return in, g.Deny("malformed", "task")
	}
	settings, err := normalizeSettings(in.Harness, in.Settings)
	if err != nil {
		return in, err
	}
	in.Settings = settings
	b, err := hex.DecodeString(in.BaseCommit)
	if err != nil || len(b) != 20 && len(b) != 32 || strings.ToLower(in.BaseCommit) != in.BaseCommit {
		return in, g.Deny("malformed", "base_commit")
	}
	if len(in.Criteria) == 0 || len(in.Criteria) > 128 || len(in.Paths) == 0 || len(in.Paths) > 128 || len(in.Operations) == 0 || len(in.Operations) > 3 {
		return in, g.Deny("malformed", "task_scope")
	}
	in.Criteria = append([]Criterion(nil), in.Criteria...)
	in.Paths = append([]string(nil), in.Paths...)
	in.Operations = append([]string(nil), in.Operations...)
	slices.SortFunc(in.Criteria, func(a, b Criterion) int { return strings.Compare(a.ID, b.ID) })
	for n, c := range in.Criteria {
		if !g.ValidActor(c.ID) || c.Text == "" || len(c.Text) > 8192 || n > 0 && in.Criteria[n-1].ID == c.ID {
			return in, g.Deny("malformed", "criteria")
		}
	}
	slices.Sort(in.Paths)
	slices.Sort(in.Operations)
	for n, path := range in.Paths {
		if !fs.ValidPath(path) || path == "." || len(path) > 512 || strings.ContainsAny(path, "\\*?[]:\x00\r\n\t") || n > 0 && in.Paths[n-1] == path {
			return in, g.Deny("malformed", "paths")
		}
		for _, part := range strings.Split(path, "/") {
			if strings.EqualFold(part, ".git") {
				return in, g.Deny("malformed", "paths")
			}
		}
		for _, c := range path {
			if c < 32 || c > 126 {
				return in, g.Deny("malformed", "paths")
			}
		}
	}
	for n, op := range in.Operations {
		if !slices.Contains([]string{"read", "verify", "write"}, op) || n > 0 && in.Operations[n-1] == op {
			return in, g.Deny("malformed", "operations")
		}
	}
	raw, _ := json.Marshal(in)
	if len(raw) > execwire.MaxBytes {
		return in, g.Deny("oversized", "task")
	}
	// Reserve the real runner envelope, not just the smaller owner request.
	// Every dispatch/task UUID has this fixed width and the digest is 64 hex
	// bytes; no future dispatch may make an accepted task undeliverable.
	if _, err := execwire.Encode(RunnerInput(in, "ffffffff-ffff-4fff-bfff-ffffffffffff")); err != nil {
		return in, g.Deny("oversized", "task")
	}
	return in, nil
}

// RunnerInput projects normalized owner intent into its definitive delivery
// shape. It carries no authority; the store still binds the retained brief and
// grant before delivery, and the runner validates that binding independently.
func RunnerInput(in TaskInput, dispatchID string) execwire.TaskInput {
	criteria := make([]execwire.Criterion, len(in.Criteria))
	for n, c := range in.Criteria {
		criteria[n] = execwire.Criterion{ID: c.ID, Text: c.Text}
	}
	brief, _ := TaskDigests(in)
	return execwire.TaskInput{Version: execwire.Version, DispatchID: dispatchID, TaskID: in.MessageID, Repository: in.Repository, BaseCommit: in.BaseCommit, BriefSHA256: brief, Brief: in.Brief, Criteria: criteria, Paths: in.Paths, Operations: in.Operations, Harness: in.Harness, Settings: in.Settings}
}
func TaskDigests(in TaskInput) (brief, plan string) {
	brief = digest(struct {
		Brief      string          `json:"brief"`
		Criteria   []Criterion     `json:"criteria"`
		Paths      []string        `json:"paths"`
		Operations []string        `json:"operations"`
		Harness    string          `json:"harness"`
		Settings   json.RawMessage `json:"settings"`
	}{in.Brief, in.Criteria, in.Paths, in.Operations, in.Harness, in.Settings})
	plan = digest(struct {
		Repository, Base  string
		Paths, Operations []string
	}{in.Repository, in.BaseCommit, in.Paths, in.Operations})
	return
}

func BuildGrant(ctx context.Context, source Reader, taskID, eligibilityID string) (Proposal, error) {
	in, err := source.BriefInput(ctx, taskID)
	if err != nil {
		return Proposal{}, err
	}
	in, err = Normalize(in)
	if err != nil {
		return Proposal{}, err
	}
	facts, err := source.Eligibility(ctx, eligibilityID)
	if err != nil {
		return Proposal{}, err
	}
	if err = facts.Validate(); err != nil {
		return Proposal{}, err
	}
	if facts.Repository.Repository != in.Repository || facts.Repository.BaseCommit != in.BaseCommit || facts.Route.Harness != in.Harness {
		return Proposal{}, g.Deny("no_eligible_tuple", "repository/base")
	}
	bd, pd := TaskDigests(in)
	revision := int64(1)
	if bindings, ok := source.(interface {
		TaskBindings(context.Context, string) (int64, string, string, error)
	}); ok {
		revision, bd, pd, err = bindings.TaskBindings(ctx, taskID)
		if err != nil {
			return Proposal{}, err
		}
	}
	fd, err := sc.Digest(facts)
	if err != nil {
		return Proposal{}, err
	}
	d := sc.Decision{ID: IntentID(taskID, "decision:"+fd+":"+bd+":"+pd), Revision: 1,
		Assessment:    sc.Assessment{TaskID: taskID, Brief: g.Revision{Number: revision, SHA256: bd}, Plan: g.Revision{Number: revision, SHA256: pd}, ContextDigest: bd, Role: "worker", WorkClass: facts.LocalEnvelope.TaskKinds[0], Ambiguity: "operator-reviewed", Consequence: "bounded-local", Required: []string{}, Preferences: []string{}, EvidenceRefs: []string{facts.ID}, Unknowns: []string{}, ContextTokens: 1, AssessorVersion: "operator-v1", SelectorVersion: "pinned-v1"},
		EligibilityID: facts.ID, Eligibility: g.Revision{Number: facts.Revision, SHA256: fd}, Selected: facts.Route, RankedAlternatives: []string{}, Authorization: "owner-required", Reason: "explicit-tuple", Resources: facts.Capacity}
	if err = d.Validate(); err != nil {
		return Proposal{}, err
	}
	dd, _ := sc.Digest(d)
	e := facts.LocalEnvelope
	e.Brief = d.Assessment.Brief
	e.Plan = d.Assessment.Plan
	e.RouteDecision = g.Revision{Number: d.Revision, SHA256: dd}
	e.CriterionIDs = []string{}
	for _, c := range in.Criteria {
		e.CriterionIDs = append(e.CriterionIDs, c.ID)
	}
	e.Paths = in.Paths
	e.Operations = in.Operations
	e.TaskKinds = []string{d.Assessment.WorkClass}
	e.Selection = "pinned"
	e.ExpiresMS = min(e.ExpiresMS, facts.ValidUntilMS)
	grant := g.Grant{ID: IntentID(taskID, "proposal:"+dd), TaskID: taskID, Revision: 1, Actor: "owner", Envelope: e}
	if err = grant.Validate(); err != nil {
		return Proposal{}, err
	}
	local := facts.LocalEnvelope
	local.Brief = e.Brief
	local.Plan = e.Plan
	local.RouteDecision = e.RouteDecision
	if err = g.Within(e, local); err != nil {
		return Proposal{}, err
	}
	proposal := Proposal{Grant: grant, Decision: d, Digests: map[string]string{"brief": bd, "plan": pd, "eligibility": fd, "decision": dd}}
	proposal.Digests["proposal"] = digest(struct {
		Grant    g.Grant
		Decision sc.Decision
	}{grant, d})
	return proposal, nil
}
func BuildDispatch(ctx context.Context, source Reader, proposal Proposal, messageID string, attemptMS int64) (execwire.DispatchRequest, error) {
	if err := ctx.Err(); err != nil {
		return execwire.DispatchRequest{}, err
	}
	if !p.ValidID(messageID) || attemptMS <= 0 || attemptMS > proposal.Grant.Envelope.Budgets.AttemptMS {
		return execwire.DispatchRequest{}, g.Deny("invalid_bound", "attempt_ms")
	}
	if err := proposal.Grant.Validate(); err != nil {
		return execwire.DispatchRequest{}, err
	}
	if err := proposal.Decision.Validate(); err != nil {
		return execwire.DispatchRequest{}, err
	}
	grant := proposal.Grant
	b, err := g.AttemptAllowance(grant.Envelope.Budgets, g.Budgets{}, 0, attemptMS)
	if err != nil {
		return execwire.DispatchRequest{}, err
	}
	return execwire.DispatchRequest{ID: messageID, Request: g.Request{GrantID: grant.ID, TaskID: grant.TaskID, GrantRevision: grant.Revision, Action: "execute", Envelope: grant.Envelope}, Decision: proposal.Decision, Allowance: b}, nil
}
