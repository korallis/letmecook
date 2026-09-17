package review

import (
	"strings"
	"testing"
	"time"

	v "github.com/korallis/letmecook/internal/verification"
	p "github.com/korallis/letmecook/schemas/execution"
)

func reportFixture() v.Report {
	c := v.Candidate{SelectionID: v.ID(), Identity: p.Identity{Generation: v.ID(), TaskID: v.ID(), AttemptID: v.ID(), Epoch: 1}, Manifest: p.Manifest{ManifestID: v.ID(), SHA256: strings.Repeat("a", 64), Bytes: 1}, BaseCommit: strings.Repeat("b", 40)}
	return v.Report{ID: v.ID(), Candidate: c, Checks: v.TrustedChecks{ID: "approved", ApprovedBy: "operator", ApprovalRef: "approval", Checks: []v.Check{{Name: "required", Argv: []string{"/bin/true"}, Env: map[string]string{}, CWD: ".", Timeout: time.Second, Required: true}}}, Evidence: []v.Evidence{}, Limitations: []string{v.UnqualifiedReason}}
}
func TestFreshContextPacketNotWorkerSessionOrReviewSuffix(t *testing.T) {
	report := reportFixture()
	route := PermittedRoute{ID: "named-route", PermissionRef: "separate-permission", PolicyDigest: strings.Repeat("c", 64), PermittedBy: "operator", SelectionReason: "review requirements"}
	grant := GrantReference{ID: v.ID(), Revision: 1}
	one, err := FreshInvocation(report, grant, []Criterion{{ID: "c1", Text: "required check"}}, route)
	if err != nil {
		t.Fatal(err)
	}
	two, err := FreshInvocation(report, grant, one.Criteria, route)
	if err != nil {
		t.Fatal(err)
	}
	if one.ContextID == two.ContextID || one.Status.Verified || one.Mode != "fresh-context-only" {
		t.Fatal(one, two)
	}
	one.ContextID = report.Candidate.Identity.AttemptID
	if err = one.Validate(); err == nil {
		t.Fatal("worker context reused")
	}
	one = two
	one.Route = PermittedRoute{ID: "model-review"}
	if err = one.Validate(); err == nil {
		t.Fatal("suffix proved independence")
	}
	one = two
	one.Status.Verified = true
	if err = one.Validate(); err == nil {
		t.Fatal("worker success replaced actual checks")
	}
}
func TestDecisionRefusesMissingChecksAndRetainsExactRejection(t *testing.T) {
	report := reportFixture()
	d := Decision{ID: v.ID(), Candidate: report.Candidate, VerificationID: report.ID, EvidenceIDs: []string{}, Grant: GrantReference{ID: v.ID(), Revision: 1}, Action: "accept", Actor: "operator", Coverage: []Coverage{{CriterionID: "c1", Status: "not-covered", Explanation: "required check missing"}}, Limitations: report.Limitations, At: time.Now().UTC()}
	if err := ValidateDecision(d, report); err == nil {
		t.Fatal("missing checks accepted")
	}
	d.Action = "reject"
	if err := ValidateDecision(d, report); err != nil {
		t.Fatal(err)
	}
	if current := Derive(report.Candidate, report, []Decision{d}); current.Accepted || current.Decision.ID != d.ID {
		t.Fatal(current)
	}
	changed := report.Candidate
	changed.Manifest.SHA256 = strings.Repeat("f", 64)
	if current := Derive(changed, report, []Decision{d}); current.Accepted || !strings.Contains(current.Reasons[0], "stale") {
		t.Fatal(current)
	}
}
