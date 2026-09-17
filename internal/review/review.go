// Package review defines provisional local decision records, not authority.
package review

import (
	"fmt"
	"reflect"
	"slices"
	"time"

	v "github.com/korallis/letmecook/internal/verification"
	p "github.com/korallis/letmecook/schemas/execution"
)

type GrantReference struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}
type Coverage struct {
	CriterionID string   `json:"criterion_id"`
	Status      string   `json:"status"` // covered, partial, not-covered
	EvidenceIDs []string `json:"evidence_ids"`
	Explanation string   `json:"explanation"`
}
type Decision struct {
	ID             string         `json:"id"`
	Candidate      v.Candidate    `json:"candidate"`
	VerificationID string         `json:"verification_id"`
	EvidenceIDs    []string       `json:"evidence_ids"`
	Grant          GrantReference `json:"grant"`
	Action         string         `json:"action"`
	Actor          string         `json:"actor"`
	Coverage       []Coverage     `json:"coverage"`
	Limitations    []string       `json:"limitations"`
	At             time.Time      `json:"at"`
}

func EvidenceIDs(report v.Report) []string {
	ids := make([]string, 0, len(report.Evidence))
	for _, e := range report.Evidence {
		ids = append(ids, e.ID)
	}
	return ids
}

// ValidateDecision checks exact evidence binding. The caller separately checks
// retained grant identity/criteria and current candidate in the same transaction.
func ValidateDecision(d Decision, report v.Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	if !p.ValidID(d.ID) || d.Candidate != report.Candidate || d.VerificationID != report.ID || !slices.Equal(d.EvidenceIDs, EvidenceIDs(report)) || !p.ValidID(d.Grant.ID) || d.Grant.Revision < 1 || d.Actor == "" || d.At.IsZero() || (d.Action != "accept" && d.Action != "reject") || len(d.Coverage) == 0 || len(d.Coverage) > 128 || d.Limitations == nil {
		return fmt.Errorf("invalid local decision binding")
	}
	seen := map[string]bool{}
	for _, c := range d.Coverage {
		if c.CriterionID == "" || seen[c.CriterionID] || c.Explanation == "" || (c.Status != "covered" && c.Status != "partial" && c.Status != "not-covered") {
			return fmt.Errorf("invalid criterion coverage")
		}
		seen[c.CriterionID] = true
		for _, id := range c.EvidenceIDs {
			if !slices.Contains(d.EvidenceIDs, id) {
				return fmt.Errorf("coverage references unrelated evidence")
			}
		}
	}
	for _, limit := range report.Limitations {
		if !slices.Contains(d.Limitations, limit) {
			return fmt.Errorf("verification limitation omitted")
		}
	}
	if d.Action == "accept" && !v.Evaluate(report, d.Candidate).Verified {
		return fmt.Errorf("unverified candidate cannot be accepted")
	}
	return nil
}

type Current struct {
	Decision *Decision `json:"decision"`
	Accepted bool      `json:"accepted"`
	Reasons  []string  `json:"reasons"`
}

// Derive never overwrites history. A newer verification, even for the same
// candidate, needs a new decision; replacement selections cannot resurrect one.
func Derive(current v.Candidate, report v.Report, history []Decision) Current {
	out := Current{Reasons: []string{}}
	if len(history) == 0 {
		out.Reasons = append(out.Reasons, "no local decision")
		return out
	}
	d := history[len(history)-1]
	out.Decision = &d
	if d.Candidate != current || report.Candidate != current {
		out.Reasons = append(out.Reasons, "candidate replaced; acceptance stale")
		return out
	}
	if err := ValidateDecision(d, report); err != nil {
		out.Reasons = append(out.Reasons, err.Error())
		return out
	}
	if d.Action == "reject" {
		out.Reasons = append(out.Reasons, "locally rejected")
		return out
	}
	s := v.Evaluate(report, current)
	out.Accepted, out.Reasons = s.Verified, s.Reasons
	return out
}

type Criterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// PermittedRoute is a separately operator-permitted named reviewer route. It
// contains no account choice, provider credentials, fallback loop or worker session.
type PermittedRoute struct {
	ID              string `json:"id"`
	PermissionRef   string `json:"permission_ref"`
	PolicyDigest    string `json:"policy_digest"`
	PermittedBy     string `json:"permitted_by"`
	SelectionReason string `json:"selection_reason"`
}
type Invocation struct {
	ID           string         `json:"id"`
	ContextID    string         `json:"context_id"`
	Mode         string         `json:"mode"`
	Candidate    v.Candidate    `json:"candidate"`
	Grant        GrantReference `json:"grant"`
	Criteria     []Criterion    `json:"criteria"`
	Verification v.Report       `json:"verification"`
	Status       v.Status       `json:"status"`
	Limitations  []string       `json:"limitations"`
	Route        PermittedRoute `json:"route"`
	Created      time.Time      `json:"created"`
}

// FreshInvocation is a data packet only. No inference is dispatched. A suffix on
// a model/route cannot supply fresh context or the separate route permission.
func FreshInvocation(report v.Report, grant GrantReference, criteria []Criterion, route PermittedRoute) (Invocation, error) {
	i := Invocation{ID: v.ID(), ContextID: v.ID(), Mode: "fresh-context-only", Candidate: report.Candidate, Grant: grant, Criteria: criteria, Verification: report, Status: v.Evaluate(report, report.Candidate), Limitations: slices.Clone(report.Limitations), Route: route, Created: time.Now().UTC()}
	return i, i.Validate()
}
func (i Invocation) Validate() error {
	if err := i.Verification.Validate(); err != nil {
		return err
	}
	if !p.ValidID(i.ID) || !p.ValidID(i.ContextID) || i.ID == i.ContextID || i.ContextID == i.Candidate.Identity.AttemptID || i.Mode != "fresh-context-only" || i.Candidate != i.Verification.Candidate || !p.ValidID(i.Grant.ID) || i.Grant.Revision < 1 || !reflect.DeepEqual(i.Status, v.Evaluate(i.Verification, i.Candidate)) || !slices.Equal(i.Limitations, i.Verification.Limitations) || i.Created.IsZero() || i.Route.ID == "" || i.Route.PermissionRef == "" || i.Route.PermittedBy == "" || i.Route.SelectionReason == "" || !v.IsDigest(i.Route.PolicyDigest) || len(i.Criteria) < 1 || len(i.Criteria) > 128 {
		return fmt.Errorf("invalid fresh reviewer packet/permission")
	}
	seen := map[string]bool{}
	for _, c := range i.Criteria {
		if c.ID == "" || c.Text == "" || seen[c.ID] {
			return fmt.Errorf("invalid review criteria")
		}
		seen[c.ID] = true
	}
	return nil
}
