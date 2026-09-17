// Package verification records exact-content checks, never execution authority.
package verification

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	p "github.com/korallis/letmecook/schemas/execution"
)

const CaptureLimit = 16 << 10

// MaxReportBytes is the dedicated report endpoint/storage bound, not a task-view bound.
const MaxReportBytes = 8 << 20
const UnqualifiedReason = "no proven Docker-free execution profile (#89)"
const DevelopmentLimitation = "isolation profile macos-sandbox-exec-dev is a development profile: unqualified for unattended execution"
const ContentLimitation = "manifest v1 binds content, not executable modes; recreated files use mode 0600"

func ID() string {
	b := [16]byte{}
	rand.Read(b[:])
	b[6], b[8] = b[6]&15|64, b[8]&63|128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func IsDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && strings.ToLower(s) == s
}

// SelectionID fences A -> B -> A as well as a changed manifest digest.
type Candidate struct {
	SelectionID string     `json:"selection_id"`
	Identity    p.Identity `json:"identity"`
	Manifest    p.Manifest `json:"manifest"`
	BaseCommit  string     `json:"base_commit"`
}

func (c Candidate) Validate() error {
	_, err := hex.DecodeString(c.BaseCommit)
	if !p.ValidID(c.SelectionID) || !p.ValidID(c.Identity.Generation) || !p.ValidID(c.Identity.TaskID) || !p.ValidID(c.Identity.AttemptID) || c.Identity.Epoch < 1 || c.Identity.Epoch > p.MaxInteger || !p.ValidID(c.Manifest.ManifestID) || !IsDigest(c.Manifest.SHA256) || c.Manifest.Bytes < 1 || c.Manifest.Bytes > 1<<20 || err != nil || (len(c.BaseCommit) != 40 && len(c.BaseCommit) != 64) || strings.ToLower(c.BaseCommit) != c.BaseCommit {
		return fmt.Errorf("invalid candidate identity")
	}
	return nil
}

type Check struct {
	Name string   `json:"name"`
	Argv []string `json:"argv"`
	// Only these explicit values enter the environment; nothing is inherited.
	Env      map[string]string `json:"env"`
	CWD      string            `json:"cwd"`
	Timeout  time.Duration     `json:"timeout_ns"`
	Required bool              `json:"required"`
}

// TrustedChecks is an operator-approved input, not a model proposal or an auth API.
// Its entire value is retained with each report, including approval provenance.
type TrustedChecks struct {
	ID          string  `json:"id"`
	ApprovedBy  string  `json:"approved_by"`
	ApprovalRef string  `json:"approval_ref"`
	Checks      []Check `json:"checks"`
}

func (p TrustedChecks) Validate() error {
	if p.ID == "" || p.ApprovedBy == "" || p.ApprovalRef == "" || len(p.Checks) > 128 {
		return fmt.Errorf("trusted check approval missing")
	}
	seen := map[string]bool{}
	for _, c := range p.Checks {
		if c.Name == "" || seen[c.Name] || len(c.Argv) == 0 || len(c.Argv) > 128 || !filepath.IsAbs(c.Argv[0]) || (c.CWD != "." && !safePath(c.CWD)) || c.Timeout <= 0 || c.Timeout > time.Hour || len(c.Env) > 128 {
			return fmt.Errorf("invalid trusted check")
		}
		seen[c.Name] = true
		for _, a := range c.Argv {
			if strings.ContainsRune(a, 0) {
				return fmt.Errorf("invalid argv")
			}
		}
		for k, v := range c.Env {
			if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
				return fmt.Errorf("invalid environment")
			}
			if k == "PATH" {
				for _, dir := range filepath.SplitList(v) {
					if !filepath.IsAbs(dir) {
						return fmt.Errorf("environment PATH must contain absolute directories")
					}
				}
			}
		}
	}
	b, err := json.Marshal(p)
	if err != nil || len(b) > 65536 {
		return fmt.Errorf("check profile bounds")
	}
	return nil
}

type Environment struct {
	OS                  string `json:"os"`
	Arch                string `json:"arch"`
	DockerBinary        string `json:"docker_binary"` // absent or present, never a daemon/readiness claim
	DockerSearchPath    string `json:"docker_search_path"`
	ExpectedConfinement string `json:"expected_confinement"`
	ObservedConfinement string `json:"observed_confinement"`
	ConfinementDrift    bool   `json:"confinement_drift"`
}
type Refusal struct {
	Code        string      `json:"code"`
	Reason      string      `json:"reason"`
	Environment Environment `json:"environment"`
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Reason }

type Stream struct {
	Prefix []byte `json:"prefix"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func (s Stream) valid() bool {
	return len(s.Prefix) <= CaptureLimit && s.Bytes >= int64(len(s.Prefix)) && IsDigest(s.SHA256) && (s.Bytes > int64(len(s.Prefix)) || Digest(s.Prefix) == s.SHA256)
}

type Evidence struct {
	ID              string        `json:"id"`
	CandidateDigest string        `json:"candidate_digest"`
	BaseCommit      string        `json:"base_commit"`
	ProfileID       string        `json:"profile_id"`
	Qualification   string        `json:"qualification,omitempty"`
	ProfileDigest   string        `json:"profile_digest,omitempty"`
	RuntimeDigest   string        `json:"runtime_digest,omitempty"`
	CheckName       string        `json:"check_name"`
	Argv            []string      `json:"argv"`
	EnvKeys         []string      `json:"env_keys"`
	CWD             string        `json:"cwd"`
	ExitCode        *int          `json:"exit_code"`
	Stdout          Stream        `json:"stdout"`
	Stderr          Stream        `json:"stderr"`
	Started         time.Time     `json:"started"`
	Ended           time.Time     `json:"ended"`
	Duration        time.Duration `json:"duration_ns"`
	Verifier        string        `json:"verifier"`
	Environment     Environment   `json:"environment"`
	Refusal         *Refusal      `json:"refusal"`
	Failure         string        `json:"failure"`
}
type Suggestion struct {
	Source string `json:"source"`
	Text   string `json:"text"`
}
type Report struct {
	ID                string        `json:"id"`
	Candidate         Candidate     `json:"candidate"`
	Checks            TrustedChecks `json:"checks"`
	Evidence          []Evidence    `json:"evidence"`
	Suggestions       []Suggestion  `json:"suggestions"`
	Limitations       []string      `json:"limitations"`
	RecreationFailure string        `json:"recreation_failure"`
}
type Status struct {
	Verified bool     `json:"verified"`
	Reasons  []string `json:"reasons"`
}

func (r Report) Validate() error {
	if !p.ValidID(r.ID) {
		return fmt.Errorf("invalid verification id")
	}
	if err := r.Candidate.Validate(); err != nil {
		return err
	}
	if err := r.Checks.Validate(); err != nil {
		return err
	}
	checks := map[string]Check{}
	for _, c := range r.Checks.Checks {
		checks[c.Name] = c
	}
	seen, ids := map[string]bool{}, map[string]bool{}
	for _, e := range r.Evidence {
		c, ok := checks[e.CheckName]
		if !ok || seen[e.CheckName] || ids[e.ID] || !p.ValidID(e.ID) || e.CandidateDigest != r.Candidate.Manifest.SHA256 || e.BaseCommit != r.Candidate.BaseCommit || !reflect.DeepEqual(c.Argv, e.Argv) || !reflect.DeepEqual(envKeys(c.Env), e.EnvKeys) || e.CWD != c.CWD || e.Verifier == "" || e.Started.IsZero() || e.Ended.Before(e.Started) || e.Duration < 0 || e.Duration != e.Ended.Sub(e.Started) || !e.Stdout.valid() || !e.Stderr.valid() {
			return fmt.Errorf("invalid verification evidence")
		}
		if e.Environment.OS == "" || e.Environment.Arch == "" || (e.Environment.DockerBinary != "absent" && e.Environment.DockerBinary != "present") {
			return fmt.Errorf("missing environment observation")
		}
		if e.Environment.DockerSearchPath != c.Env["PATH"] || e.Environment.ConfinementDrift != (e.Environment.ExpectedConfinement != e.Environment.ObservedConfinement) {
			return fmt.Errorf("check environment mismatch")
		}
		// No product-qualified execution profile exists. Reject invented labels.
		if e.ProfileID != "unqualified" && e.ProfileID != "test-only-unconfined" && e.ProfileID != "macos-sandbox-exec-dev" {
			return fmt.Errorf("unknown isolation profile")
		}
		if e.ProfileID == "macos-sandbox-exec-dev" && (e.Qualification != "development" || !IsDigest(e.ProfileDigest) || !IsDigest(e.RuntimeDigest) || e.Environment.ExpectedConfinement != e.ProfileID || e.Environment.ObservedConfinement != e.ProfileID || !slices.Contains(r.Limitations, DevelopmentLimitation)) {
			return fmt.Errorf("development profile must be explicitly labelled")
		}
		if e.ProfileID == "unqualified" && (e.Refusal == nil || e.Refusal.Code != "unqualified_profile" || e.Refusal.Reason != UnqualifiedReason || e.ExitCode != nil) {
			return fmt.Errorf("unqualified profile cannot execute")
		}
		if e.ProfileID == "test-only-unconfined" && (e.Environment.ExpectedConfinement != e.ProfileID || e.Environment.ObservedConfinement != e.ProfileID) {
			return fmt.Errorf("test-only environment must be explicitly labelled")
		}
		if e.Refusal != nil && (e.ExitCode != nil || e.Refusal.Environment != e.Environment) {
			return fmt.Errorf("invalid refusal evidence")
		}
		if e.ExitCode == nil && e.Refusal == nil && e.Failure == "" {
			return fmt.Errorf("missing check outcome")
		}
		seen[e.CheckName], ids[e.ID] = true, true
	}
	b, err := json.Marshal(r)
	if err != nil || len(b) > MaxReportBytes {
		return fmt.Errorf("verification bounds")
	}
	return nil
}

// Evaluate is a pure comparison, not an authority token. Durable callers must
// load a committed report and the current selection, rather than worker claims.
func Evaluate(r Report, current Candidate) Status {
	s := Status{Reasons: []string{}}
	if err := r.Validate(); err != nil {
		s.Reasons = append(s.Reasons, err.Error())
		return s
	}
	if r.Candidate != current {
		s.Reasons = append(s.Reasons, "candidate replaced; evidence stale")
	}
	if r.RecreationFailure != "" {
		s.Reasons = append(s.Reasons, "candidate recreation failed: "+r.RecreationFailure)
	}
	byName := map[string]Evidence{}
	for _, e := range r.Evidence {
		byName[e.CheckName] = e
	}
	required := 0
	for _, c := range r.Checks.Checks {
		if !c.Required {
			continue
		}
		required++
		e, ok := byName[c.Name]
		if !ok {
			s.Reasons = append(s.Reasons, "missing required check: "+c.Name)
		} else if e.Refusal != nil || e.Failure != "" || e.ExitCode == nil || *e.ExitCode != 0 {
			s.Reasons = append(s.Reasons, "required check did not pass: "+c.Name)
		}
	}
	if required == 0 {
		s.Reasons = append(s.Reasons, "no required checks approved")
	}
	s.Verified = len(s.Reasons) == 0
	return s
}

// Summary keeps task and list views independent of potentially large evidence.
type Summary struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
}

// Summarize keeps diagnostics bounded; complete evidence is read by report ID.
func Summarize(id string, status Status) Summary {
	reasons := make([]string, 0, min(len(status.Reasons), 32))
	for _, reason := range status.Reasons[:min(len(status.Reasons), 32)] {
		runes := []rune(reason)
		if len(runes) > 256 {
			reason = string(runes[:256]) + "…"
		}
		reasons = append(reasons, reason)
	}
	status.Reasons = reasons
	return Summary{ID: id, Status: status}
}
