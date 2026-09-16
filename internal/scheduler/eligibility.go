// Package scheduler validates provisional admission facts. It launches no work.
package scheduler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"regexp"
	"slices"

	g "github.com/korallis/letmecook/internal/authority"
	r "github.com/korallis/letmecook/internal/repositories"
	p "github.com/korallis/letmecook/schemas/execution"
)

// Resources are local reservations, not subscription balances. CPU is millicores.
type Resources struct {
	CPU         int64 `json:"cpu"`
	MemoryBytes int64 `json:"memory_bytes"`
	DiskBytes   int64 `json:"disk_bytes"`
	Processes   int64 `json:"processes"`
}

func (v Resources) Values() [4]int64 { return [4]int64{v.CPU, v.MemoryBytes, v.DiskBytes, v.Processes} }
func (v Resources) Valid() bool {
	for _, n := range v.Values() {
		if !positive(n) {
			return false
		}
	}
	return true
}

// PathEvidence applies to the exact enclosing route build/graph/settings/harness,
// protocol, isolation and limits identity. Compatible includes reviewed data policy
// and billing classification. Discovery/account presence is not this evidence.
type PathEvidence struct {
	Target              g.Target `json:"target"`
	Compatible          bool     `json:"compatible"`
	Capabilities        []string `json:"capabilities"`
	ContextTokens       int64    `json:"context_tokens"`
	LocalBounds         bool     `json:"local_bounds"`
	ProviderOutputBound bool     `json:"provider_output_bound"`
	ProviderCostBound   bool     `json:"provider_cost_bound"`
}

type IsolationProfile struct {
	ID             string     `json:"id"`
	Revision       g.Revision `json:"revision"`
	RuntimeDigest  string     `json:"runtime_digest"`
	ObservedDigest string     `json:"observed_digest"`
	Kind           string     `json:"kind"` // native or dedicated-vm; never host-shell/worktree/docker.
	Supported      bool       `json:"supported"`
	DockerRequired bool       `json:"docker_required"`
	Controls       []string   `json:"controls"`
}

// Eligibility is an operator-reviewed, authenticated local-policy mirror and
// capability snapshot, not a worker claim. One exact tuple; no default placement.
// Missing/negative facts can be recorded, but cannot pass CheckDispatch.
type Eligibility struct {
	ID                  string           `json:"id"`
	Revision            int64            `json:"revision"`
	Repository          r.Selection      `json:"repository"`
	Enabled             bool             `json:"enabled"`
	RunnerBoot          string           `json:"runner_boot"`
	LocalPolicy         g.Revision       `json:"local_policy"`
	LocalEnvelope       g.Envelope       `json:"local_envelope"`
	Config              g.Revision       `json:"config"`
	Route               g.Route          `json:"route"`
	Paths               []PathEvidence   `json:"paths"`
	Capabilities        []string         `json:"capabilities"`
	Isolation           IsolationProfile `json:"isolation"`
	Capacity            Resources        `json:"capacity"`
	Concurrency         int64            `json:"concurrency"`
	RouterAuthenticated bool             `json:"router_authenticated"`
	Availability        string           `json:"availability"` // available/degraded/unknown/outage/exhausted
	ValidUntilMS        int64            `json:"valid_until_ms"`
}

// Assessment is operator-supplied in M1. No semantic assessment calls or planner
// dependency. Unknown mandatory facts park; optional unknowns remain recorded.
type Assessment struct {
	TaskID           string     `json:"task_id"`
	Brief            g.Revision `json:"brief"`
	Plan             g.Revision `json:"plan"`
	ContextDigest    string     `json:"context_digest"`
	Role             string     `json:"role"`
	WorkClass        string     `json:"work_class"`
	Ambiguity        string     `json:"ambiguity"`
	Consequence      string     `json:"consequence"`
	Required         []string   `json:"required"`
	Preferences      []string   `json:"preferences"`
	EvidenceRefs     []string   `json:"evidence_refs"`
	Unknowns         []string   `json:"unknowns"`
	MandatoryUnknown bool       `json:"mandatory_unknown"`
	ContextTokens    int64      `json:"context_tokens"`
	HardOutputBound  bool       `json:"hard_output_bound"`
	HardCostBound    bool       `json:"hard_cost_bound"`
	AssessorVersion  string     `json:"assessor_version"`
	SelectorVersion  string     `json:"selector_version"`
}

type Decision struct {
	ID                 string     `json:"id"`
	Revision           int64      `json:"revision"`
	Assessment         Assessment `json:"assessment"`
	EligibilityID      string     `json:"eligibility_id"`
	Eligibility        g.Revision `json:"eligibility"`
	Selected           g.Route    `json:"selected"`
	RankedAlternatives []string   `json:"ranked_alternatives"` // rationale only; never a fallback loop.
	Authorization      string     `json:"authorization"`
	Reason             string     `json:"reason"`
	Resources          Resources  `json:"resources"`
}

// Digest binds canonical typed records; callers still validate them at admission.
func Digest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if len(b) > g.MaxBytes {
		return "", g.Deny("oversized", "dispatch_record")
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func (v Eligibility) Validate() error {
	if !g.ValidActor(v.ID) || !positive(v.Revision) || !p.ValidID(v.RunnerBoot) || !revision(v.LocalPolicy) || !revision(v.Config) || !positive(v.ValidUntilMS) || !v.Capacity.Valid() || !positive(v.Concurrency) || !validSet(v.Capabilities, true, g.ValidActor) || !slices.Contains([]string{"available", "degraded", "unknown", "outage", "exhausted"}, v.Availability) {
		return g.Deny("malformed", "eligibility")
	}
	if err := v.LocalEnvelope.Validate(); err != nil {
		return err
	}
	if len(v.LocalEnvelope.Routes) != 1 || !reflect.DeepEqual(v.Route, v.LocalEnvelope.Routes[0]) || len(v.LocalEnvelope.Runners) != 1 || v.LocalEnvelope.Runners[0] != v.Repository.RunnerRoot.RunnerID || v.LocalEnvelope.Repository != v.Repository.Repository || v.LocalEnvelope.BaseCommit != v.Repository.BaseCommit || !positive(v.Repository.Revision) || !digest.MatchString(v.Repository.ProfileDigest) || !r.ValidRemote(v.Repository.Remote) {
		return g.Deny("malformed", "local_policy")
	}
	if !ordered(v.Paths, func(e PathEvidence) string { return targetKey(e.Target) }) {
		return g.Deny("malformed", "path_evidence")
	}
	for _, e := range v.Paths {
		if !slices.Contains(v.Route.Targets, e.Target) || !validSet(e.Capabilities, true, g.ValidActor) || !nonnegative(e.ContextTokens) {
			return g.Deny("malformed", "path_evidence")
		}
	}
	iso := v.Isolation
	if iso.ID != "" && (!g.ValidActor(iso.ID) || !revision(iso.Revision) || !digest.MatchString(iso.RuntimeDigest) || !digest.MatchString(iso.ObservedDigest) || !g.ValidActor(iso.Kind) || !validSet(iso.Controls, true, g.ValidActor)) {
		return g.Deny("malformed", "isolation")
	}
	_, err := Digest(v)
	return err
}

func (v Decision) Validate() error {
	a := v.Assessment
	if !p.ValidID(v.ID) || !positive(v.Revision) || !g.ValidActor(v.EligibilityID) || !revision(v.Eligibility) || !g.ValidActor(v.Authorization) || !g.ValidActor(v.Reason) || !v.Resources.Valid() || !p.ValidID(a.TaskID) || !revision(a.Brief) || !revision(a.Plan) || !digest.MatchString(a.ContextDigest) || !positive(a.ContextTokens) {
		return g.Deny("invalid_assessment", "decision")
	}
	for _, value := range []string{a.Role, a.WorkClass, a.Ambiguity, a.Consequence, a.AssessorVersion, a.SelectorVersion} {
		if !g.ValidActor(value) {
			return g.Deny("invalid_assessment", "classification")
		}
	}
	for _, values := range [][]string{a.Required, a.Preferences, a.EvidenceRefs, a.Unknowns} {
		if !validSet(values, true, g.ValidActor) {
			return g.Deny("invalid_assessment", "requirements")
		}
	}
	if a.AssessorVersion != "operator-v1" || len(a.EvidenceRefs) == 0 || v.RankedAlternatives == nil || len(v.RankedAlternatives) > 128 {
		return g.Deny("invalid_assessment", "provenance")
	}
	seen := map[string]bool{}
	for _, ref := range v.RankedAlternatives {
		if !g.ValidActor(ref) || seen[ref] {
			return g.Deny("invalid_assessment", "alternatives")
		}
		seen[ref] = true
	}
	_, err := Digest(v)
	return err
}

func RequiredIsolationControls() []string {
	return []string{"bounded-resources", "controlled-egress", "external-supervisor", "non-root", "secret-separation", "tree-termination", "workspace-only"}
}

// CheckDispatch consumes already authenticated facts inside the store transaction.
// It checks every reachable target, never substitutes a ranked route on outage.
func CheckDispatch(request g.Request, decision Decision, facts Eligibility, now int64) error {
	if err := decision.Validate(); err != nil {
		return err
	}
	if err := facts.Validate(); err != nil {
		return err
	}
	e := request.Envelope
	if err := e.Validate(); err != nil {
		return err
	}
	if len(e.Routes) != 1 || len(e.Runners) != 1 {
		return g.Deny("malformed", "selection")
	}
	d, err := Digest(decision)
	if err != nil {
		return err
	}
	if e.RouteDecision != (g.Revision{Number: decision.Revision, SHA256: d}) || decision.Assessment.TaskID != request.TaskID || decision.Assessment.Brief != e.Brief || decision.Assessment.Plan != e.Plan {
		return g.Deny("stale_decision", "assessment")
	}
	fd, err := Digest(facts)
	if err != nil {
		return err
	}
	if decision.EligibilityID != facts.ID || decision.Eligibility != (g.Revision{Number: facts.Revision, SHA256: fd}) {
		return g.Deny("stale_decision", "eligibility")
	}
	if now >= facts.ValidUntilMS {
		return g.Deny("stale_evidence", "eligibility")
	}
	if !facts.Enabled {
		return g.Deny("local_policy_denied", "enabled")
	}
	if !reflect.DeepEqual(e.Routes[0], decision.Selected) || !reflect.DeepEqual(decision.Selected, facts.Route) {
		return g.Deny("stale_decision", "route")
	}
	// Local policy mirrors scope, not task approval revisions. Compare scope using
	// the current approved task bindings without granting those bindings locally.
	local := facts.LocalEnvelope
	local.Brief, local.Plan, local.RouteDecision = e.Brief, e.Plan, e.RouteDecision
	if err := g.Within(e, local); err != nil {
		return g.Deny("local_policy_denied", "scope")
	}
	if now < local.NotBeforeMS || now >= local.ExpiresMS {
		return g.Deny("local_policy_denied", "validity")
	}
	iso := facts.Isolation
	if iso.ID == "" {
		return g.Deny("isolation_missing", "profile")
	}
	if !iso.Supported || iso.DockerRequired || !slices.Contains([]string{"native", "dedicated-vm"}, iso.Kind) {
		return g.Deny("isolation_unsupported", "profile")
	}
	if iso.ID != facts.Route.Isolation || iso.RuntimeDigest != iso.ObservedDigest {
		return g.Deny("isolation_drift", "profile")
	}
	for _, control := range RequiredIsolationControls() {
		if !slices.Contains(iso.Controls, control) {
			return g.Deny("isolation_unsupported", "controls")
		}
	}
	a := decision.Assessment
	if a.MandatoryUnknown {
		return g.Deny("mandatory_unknown", "assessment")
	}
	if a.HardCostBound && e.Budgets.ProviderCostMicros == nil || a.HardOutputBound && e.Budgets.ProviderOutputTokens == 0 {
		return g.Deny("hard_bound_unavailable", "task_budget")
	}
	for _, required := range a.Required {
		if !slices.Contains(facts.Capabilities, required) {
			return g.Deny("runner_capability_missing", "capabilities")
		}
	}
	if len(facts.Paths) != len(facts.Route.Targets) {
		return g.Deny("fallback_evidence_missing", "graph")
	}
	for index, path := range facts.Paths {
		if path.Target != facts.Route.Targets[index] || !path.Compatible || !path.LocalBounds || path.ContextTokens < a.ContextTokens {
			return g.Deny("fallback_ineligible", "evidence")
		}
		for _, required := range a.Required {
			if !slices.Contains(path.Capabilities, required) {
				return g.Deny("fallback_capability_missing", "capabilities")
			}
		}
		if (a.HardOutputBound || e.Budgets.ProviderOutputTokens > 0) && !path.ProviderOutputBound || (a.HardCostBound || e.Budgets.ProviderCostMicros != nil) && !path.ProviderCostBound {
			return g.Deny("hard_bound_unavailable", "provider")
		}
	}
	if facts.Route.LimitsProfile == "native-subscription-local-v1" && (a.HardOutputBound || a.HardCostBound) {
		return g.Deny("hard_bound_unavailable", "limits_profile")
	}
	if !facts.RouterAuthenticated {
		return g.Deny("router_unauthenticated", "route")
	}
	if facts.Availability == "outage" || facts.Availability == "exhausted" {
		return g.Deny("route_unavailable", "route")
	}
	return nil
}

var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func positive(n int64) bool       { return n > 0 && n <= p.MaxInteger }
func nonnegative(n int64) bool    { return n >= 0 && n <= p.MaxInteger }
func revision(v g.Revision) bool  { return positive(v.Number) && digest.MatchString(v.SHA256) }
func targetKey(v g.Target) string { return v.Provider + "\x00" + v.Model + "\x00" + v.Billing }
func ordered[T any](v []T, key func(T) string) bool {
	if v == nil || len(v) > 128 {
		return false
	}
	for j := 1; j < len(v); j++ {
		if key(v[j-1]) >= key(v[j]) {
			return false
		}
	}
	return true
}
func validSet(v []string, empty bool, valid func(string) bool) bool {
	if !empty && len(v) == 0 || !ordered(v, func(s string) string { return s }) {
		return false
	}
	for _, s := range v {
		if !valid(s) {
			return false
		}
	}
	return true
}
