// Package authority checks immutable execution envelopes. It performs no inference,
// authentication, dispatch, filesystem access or external effects.
package authority

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"regexp"
	"slices"
	"strings"

	p "github.com/korallis/letmecook/schemas/execution"
)

const MaxBytes = 65536

// Refusal exposes stable, non-reflecting reasons to callers. Fields name the
// contract member, never include untrusted values or model rationale.
type Refusal struct {
	Code  string `json:"code"`
	Field string `json:"field"`
}

func (r *Refusal) Error() string    { return r.Code + ": " + r.Field }
func Deny(code, field string) error { return &Refusal{Code: code, Field: field} }

type Revision struct {
	Number int64  `json:"number"`
	SHA256 string `json:"sha256"`
}

type Target struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Billing  string `json:"billing"` // subscription or metered; unknown is ineligible.
}

// Route binds the complete approved 9Router graph, not just its first model.
// Digests refer to operator-reviewed records, not worker assertions of readiness.
// 9Router alone owns connection choice, credentials and request fallback.
type Route struct {
	RouteRef        string   `json:"route_ref"`
	ProfileRef      string   `json:"profile_ref"`
	RouteRevision   int64    `json:"route_revision"`
	Policy          Revision `json:"policy"`
	RouterBuild     string   `json:"router_build"`
	GraphDigest     string   `json:"graph_digest"`
	Evidence        Revision `json:"evidence"`
	Harness         string   `json:"harness"`
	Protocol        string   `json:"protocol"`
	SettingsDigest  string   `json:"settings_digest"`
	Isolation       string   `json:"isolation"`
	LimitsProfile   string   `json:"limits_profile"`
	LimitsAuthority string   `json:"limits_authority"`
	Targets         []Target `json:"targets"`
}

// Budgets are ceilings, not remaining balances or reservations. Zero output
// tokens and nil cost explicitly mean unavailable provider bounds (native only).
// A zero cost pointer is a required zero-spend cap, not unlimited spend.
type Budgets struct {
	Requests             int64  `json:"requests"`
	Attempts             int64  `json:"attempts"`
	Subattempts          int64  `json:"subattempts"`
	Retries              int64  `json:"retries"`
	Concurrency          int64  `json:"concurrency"`
	RequestBytes         int64  `json:"request_bytes"`
	ResponseBytes        int64  `json:"response_bytes"`
	TotalMS              int64  `json:"total_ms"`
	AttemptMS            int64  `json:"attempt_ms"`
	FirstOutputMS        int64  `json:"first_output_ms"`
	IdleMS               int64  `json:"idle_ms"`
	ProviderOutputTokens int64  `json:"provider_output_tokens"`
	ProviderCostMicros   *int64 `json:"provider_cost_micros"`
}

type Envelope struct {
	Repository    string   `json:"repository"`
	BaseCommit    string   `json:"base_commit"`
	Brief         Revision `json:"brief"`
	Plan          Revision `json:"plan"`
	RouteDecision Revision `json:"route_decision"`
	CriterionIDs  []string `json:"criterion_ids"`
	TaskKinds     []string `json:"task_kinds"`
	Paths         []string `json:"paths"`
	Operations    []string `json:"operations"`
	Systems       []string `json:"systems"`
	Runners       []string `json:"runners"`
	Routes        []Route  `json:"routes"`
	Selection     string   `json:"selection"` // pinned or within-envelope
	Budgets       Budgets  `json:"budgets"`
	NotBeforeMS   int64    `json:"not_before_ms"`
	ExpiresMS     int64    `json:"expires_ms"`
}

type Grant struct {
	ID       string   `json:"id"`
	TaskID   string   `json:"task_id"`
	Revision int64    `json:"revision"`
	Actor    string   `json:"actor"`
	Envelope Envelope `json:"envelope"`
}

// Request is untrusted intent, never an approval. One exact runner, route and
// task kind are selected from the envelope. No model text is interpreted here.
type Request struct {
	GrantID       string   `json:"grant_id"`
	TaskID        string   `json:"task_id"`
	GrantRevision int64    `json:"grant_revision"`
	Action        string   `json:"action"`
	Envelope      Envelope `json:"envelope"`
}

var reference = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var model = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var commit = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

func ValidActor(v string) bool { return reference.MatchString(v) }
func positive(n int64) bool    { return n > 0 && n <= p.MaxInteger }
func nonnegative(n int64) bool { return n >= 0 && n <= p.MaxInteger }
func revision(r Revision) bool { return positive(r.Number) && digest.MatchString(r.SHA256) }
func ordered[T any](v []T, key func(T) string) bool {
	if v == nil || len(v) > 128 {
		return false
	}
	for i := 1; i < len(v); i++ {
		if key(v[i-1]) >= key(v[i]) {
			return false
		}
	}
	return true
}
func routeKey(r Route) string   { return r.RouteRef + "\x00" + r.ProfileRef }
func targetKey(t Target) string { return t.Provider + "\x00" + t.Model + "\x00" + t.Billing }
func validSet(v []string, empty bool, valid func(string) bool) bool {
	if (!empty && len(v) == 0) || !ordered(v, func(s string) string { return s }) {
		return false
	}
	for _, s := range v {
		if !valid(s) {
			return false
		}
	}
	return true
}
func validPath(s string) bool {
	// ponytail: exact portable file paths only, no globs/directories/symlinks;
	// runner filesystem mediation must prove resolution before any access (#14/#16).
	if len(s) > 512 || !fs.ValidPath(s) || s == "." || strings.ContainsAny(s, "\\*?[]:\x00\r\n\t") {
		return false
	}
	for _, c := range s {
		if c < 32 || c > 126 {
			return false
		}
	}
	for _, part := range strings.Split(s, "/") {
		if strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}

func (e Envelope) Validate() error {
	if !reference.MatchString(e.Repository) || !commit.MatchString(e.BaseCommit) {
		return Deny("malformed", "repository/base")
	}
	if !revision(e.Brief) || !revision(e.Plan) || !revision(e.RouteDecision) {
		return Deny("malformed", "revisions")
	}
	for _, s := range []struct {
		name   string
		values []string
		empty  bool
		valid  func(string) bool
	}{
		{"criterion_ids", e.CriterionIDs, false, reference.MatchString},
		{"task_kinds", e.TaskKinds, false, reference.MatchString},
		{"paths", e.Paths, false, validPath},
		{"operations", e.Operations, false, func(s string) bool { return slices.Contains([]string{"read", "verify", "write"}, s) }},
		{"systems", e.Systems, true, reference.MatchString},
		{"runners", e.Runners, false, p.ValidID},
	} {
		if !validSet(s.values, s.empty, s.valid) {
			return Deny("malformed", s.name)
		}
	}
	if !positive(e.NotBeforeMS) || !positive(e.ExpiresMS) || e.NotBeforeMS >= e.ExpiresMS {
		return Deny("malformed", "validity")
	}
	if !slices.Contains([]string{"pinned", "within-envelope"}, e.Selection) || len(e.Routes) == 0 || !ordered(e.Routes, routeKey) || e.Selection == "pinned" && len(e.Routes) != 1 {
		return Deny("malformed", "routes")
	}
	b := e.Budgets
	for _, n := range []int64{b.Requests, b.Attempts, b.Subattempts, b.Concurrency, b.RequestBytes, b.ResponseBytes, b.TotalMS, b.AttemptMS, b.FirstOutputMS, b.IdleMS} {
		if !positive(n) {
			return Deny("malformed", "budgets")
		}
	}
	if !nonnegative(b.Retries) || !nonnegative(b.ProviderOutputTokens) || b.ProviderCostMicros != nil && !nonnegative(*b.ProviderCostMicros) || b.AttemptMS > b.TotalMS || b.FirstOutputMS > b.AttemptMS || b.IdleMS > b.AttemptMS {
		return Deny("malformed", "budgets")
	}
	for _, r := range e.Routes {
		if !reference.MatchString(r.RouteRef) || !reference.MatchString(r.ProfileRef) || !positive(r.RouteRevision) || !revision(r.Policy) || !revision(r.Evidence) || !digest.MatchString(r.RouterBuild) || !digest.MatchString(r.GraphDigest) || !digest.MatchString(r.SettingsDigest) || !reference.MatchString(r.Harness) || !reference.MatchString(r.Protocol) || !reference.MatchString(r.Isolation) || !reference.MatchString(r.LimitsAuthority) || len(r.Targets) == 0 || !ordered(r.Targets, targetKey) {
			return Deny("malformed", "route_profile")
		}
		switch r.LimitsProfile {
		case "strict-provider-output-v1":
			if b.ProviderOutputTokens == 0 {
				return Deny("incompatible_route_policy", "provider_output_tokens")
			}
		case "native-subscription-local-v1":
			if b.ProviderOutputTokens != 0 || b.ProviderCostMicros != nil {
				return Deny("incompatible_route_policy", "provider_bounds")
			}
		default:
			return Deny("incompatible_route_policy", "limits_profile")
		}
		for _, t := range r.Targets {
			if !reference.MatchString(t.Provider) || !model.MatchString(t.Model) || !slices.Contains([]string{"subscription", "metered"}, t.Billing) {
				return Deny("malformed", "provider_model_billing")
			}
			if r.LimitsProfile == "native-subscription-local-v1" && t.Billing != "subscription" {
				return Deny("incompatible_route_policy", "billing")
			}
		}
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(raw) > MaxBytes {
		return Deny("oversized", "envelope")
	}
	return nil
}

func (g Grant) Validate() error {
	if !p.ValidID(g.ID) || !p.ValidID(g.TaskID) || !positive(g.Revision) || !ValidActor(g.Actor) {
		return Deny("malformed", "grant")
	}
	if err := g.Envelope.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(g)
	if err != nil {
		return err
	}
	if len(b) > MaxBytes {
		return Deny("oversized", "grant")
	}
	return nil
}

// CheckRevision rejects rollback and changed content masquerading as the same
// brief/plan/route decision. Operator approval cannot rewrite retained identities.
func CheckRevision(next, old Revision) error {
	if !revision(next) || !revision(old) {
		return Deny("malformed", "revision")
	}
	if next.Number < old.Number || next.Number == old.Number && next.SHA256 != old.SHA256 {
		return Deny("stale_revision", "revision")
	}
	return nil
}

// Replacement checks revision continuity even for an explicit new approval.
func Replacement(next, old Envelope) error {
	for _, pair := range [][2]Revision{{next.Brief, old.Brief}, {next.Plan, old.Plan}, {next.RouteDecision, old.RouteDecision}} {
		if err := CheckRevision(pair[0], pair[1]); err != nil {
			return err
		}
	}
	for _, n := range next.Routes {
		if !slices.ContainsFunc(old.Routes, func(o Route) bool { return reflect.DeepEqual(n, o) }) &&
			(next.RouteDecision.Number <= old.RouteDecision.Number || next.RouteDecision.SHA256 == old.RouteDecision.SHA256) {
			return Deny("stale_revision", "route_decision")
		}
		for _, o := range old.Routes {
			if routeKey(n) != routeKey(o) {
				continue
			}
			if n.RouteRevision < o.RouteRevision || n.RouteRevision == o.RouteRevision && !reflect.DeepEqual(n, o) {
				return Deny("stale_revision", "route_profile")
			}
			if err := CheckRevision(n.Policy, o.Policy); err != nil {
				return err
			}
			if err := CheckRevision(n.Evidence, o.Evidence); err != nil {
				return err
			}
			if n.LimitsProfile != o.LimitsProfile && (n.Policy.Number <= o.Policy.Number || n.Policy.SHA256 == o.Policy.SHA256) {
				return Deny("incompatible_route_policy", "limits_policy_revision")
			}
		}
	}
	return nil
}

// Within rejects material changes; reductions never restore already used budget.
// All brief/plan/base/decision changes require explicit operator approval.
func Within(next, approved Envelope) error {
	if err := next.Validate(); err != nil {
		return err
	}
	if err := approved.Validate(); err != nil {
		return err
	}
	if next.Repository != approved.Repository || next.BaseCommit != approved.BaseCommit {
		return Deny("material_change", "repository/base")
	}
	if next.Brief != approved.Brief || next.Plan != approved.Plan || next.RouteDecision != approved.RouteDecision {
		return Deny("stale_revision", "brief/plan/route_decision")
	}
	if next.Selection != approved.Selection {
		return Deny("material_change", "selection")
	}
	for _, s := range []struct {
		name           string
		next, approved []string
	}{
		{"criterion_ids", next.CriterionIDs, approved.CriterionIDs}, {"task_kinds", next.TaskKinds, approved.TaskKinds},
		{"paths", next.Paths, approved.Paths}, {"operations", next.Operations, approved.Operations},
		{"systems", next.Systems, approved.Systems}, {"runners", next.Runners, approved.Runners},
	} {
		for _, v := range s.next {
			if !slices.Contains(s.approved, v) {
				return Deny("widened_scope", s.name)
			}
		}
	}
	for _, r := range next.Routes {
		if !slices.ContainsFunc(approved.Routes, func(a Route) bool { return reflect.DeepEqual(a, r) }) {
			return Deny("incompatible_route_policy", "route_profile")
		}
	}
	if next.NotBeforeMS < approved.NotBeforeMS || next.ExpiresMS > approved.ExpiresMS {
		return Deny("widened_scope", "validity")
	}
	n, a := next.Budgets, approved.Budgets
	for _, pair := range [][2]int64{{n.Requests, a.Requests}, {n.Attempts, a.Attempts}, {n.Subattempts, a.Subattempts}, {n.Retries, a.Retries}, {n.Concurrency, a.Concurrency}, {n.RequestBytes, a.RequestBytes}, {n.ResponseBytes, a.ResponseBytes}, {n.TotalMS, a.TotalMS}, {n.AttemptMS, a.AttemptMS}, {n.FirstOutputMS, a.FirstOutputMS}, {n.IdleMS, a.IdleMS}, {n.ProviderOutputTokens, a.ProviderOutputTokens}} {
		if pair[0] > pair[1] {
			return Deny("widened_scope", "budgets")
		}
	}
	if a.ProviderCostMicros != nil && (n.ProviderCostMicros == nil || *n.ProviderCostMicros > *a.ProviderCostMicros) {
		return Deny("widened_scope", "provider_cost_micros")
	}
	return nil
}

func Check(g Grant, r Request, nowMS int64) error {
	if err := g.Validate(); err != nil {
		return err
	}
	if !p.ValidID(r.GrantID) || !p.ValidID(r.TaskID) || !positive(r.GrantRevision) || !positive(nowMS) {
		return Deny("malformed", "request")
	}
	if r.Action != "execute" {
		return Deny("distinct_authority_required", "action")
	}
	if r.GrantID != g.ID || r.TaskID != g.TaskID || r.GrantRevision != g.Revision {
		return Deny("stale_revision", "grant")
	}
	if nowMS >= g.Envelope.ExpiresMS {
		return Deny("expired", "grant")
	}
	if nowMS < g.Envelope.NotBeforeMS {
		return Deny("not_yet_valid", "grant")
	}
	if err := Within(r.Envelope, g.Envelope); err != nil {
		return err
	}
	if nowMS >= r.Envelope.ExpiresMS || nowMS < r.Envelope.NotBeforeMS {
		return Deny("outside_validity", "request")
	}
	if len(r.Envelope.Routes) != 1 || len(r.Envelope.Runners) != 1 || len(r.Envelope.TaskKinds) != 1 {
		return Deny("malformed", "selection")
	}
	return nil
}

// DecodeGrant is only for stored canonical records, not an approval endpoint.
func DecodeGrant(raw string) (Grant, error) {
	var g Grant
	if len(raw) > MaxBytes {
		return g, Deny("oversized", "grant")
	}
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return Grant{}, Deny("malformed", "stored_grant")
	}
	if err := g.Validate(); err != nil {
		return Grant{}, err
	}
	canonical, err := json.Marshal(g)
	if err != nil || string(canonical) != raw {
		return Grant{}, fmt.Errorf("noncanonical stored grant")
	}
	return g, nil
}
