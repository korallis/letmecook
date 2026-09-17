package verification

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

// Profile is sealed: product callers cannot inject an executor. Only Unqualified
// is implemented in product builds; the unconfined executor lives in _test.go.
type Profile interface {
	id() string
	run(context.Context, string, Check) outcome
}
type outcome struct {
	exit           *int
	stdout, stderr Stream
	environment    Environment
	refusal        *Refusal
	failure        string
}
type Unqualified struct {
	ExpectedConfinement string
	ObservedConfinement string
}

func (Unqualified) id() string { return "unqualified" }
func observe(expected, observed, searchPath string) Environment {
	docker := "absent"
	for _, dir := range filepath.SplitList(searchPath) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, "docker")); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			docker = "present"
			break
		}
	}
	return Environment{OS: runtime.GOOS, Arch: runtime.GOARCH, DockerBinary: docker, DockerSearchPath: searchPath, ExpectedConfinement: expected, ObservedConfinement: observed, ConfinementDrift: expected != observed}
}
func (p Unqualified) run(_ context.Context, _ string, c Check) outcome {
	e := observe(p.ExpectedConfinement, p.ObservedConfinement, c.Env["PATH"])
	r := &Refusal{Code: "unqualified_profile", Reason: UnqualifiedReason, Environment: e}
	return outcome{environment: e, refusal: r, stdout: emptyStream(), stderr: emptyStream()}
}
func emptyStream() Stream { return Stream{Prefix: []byte{}, SHA256: Digest(nil)} }
func envKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type Journal interface {
	SaveVerification(context.Context, Report) error
}
type Request struct {
	Candidate                            Candidate
	TrustedRepo, PrivateParent, Verifier string
	Checks                               TrustedChecks
	Suggestions                          []Suggestion
}

// Run persists the complete report before returning it. Suggestions (including
// worker success claims) never contribute commands, policy, or passing evidence.
func Run(ctx context.Context, source Source, journal Journal, profile Profile, request Request) (Report, error) {
	if profile == nil || journal == nil || source == nil || request.Verifier == "" {
		return Report{}, fmt.Errorf("verifier dependencies required")
	}
	if err := request.Candidate.Validate(); err != nil {
		return Report{}, err
	}
	if err := request.Checks.Validate(); err != nil {
		return Report{}, err
	}
	// Freeze slices/maps before passing them to the check runner or persistence.
	r := Report{ID: ID(), Candidate: request.Candidate, Checks: request.Checks, Suggestions: request.Suggestions, Evidence: []Evidence{}, Limitations: []string{ContentLimitation, "provisional library: no execution, acceptance, publication or merge authority"}}
	raw, err := json.Marshal(r)
	if err != nil {
		return Report{}, err
	}
	if err = json.Unmarshal(raw, &r); err != nil {
		return Report{}, err
	}
	for _, check := range r.Checks.Checks {
		root, recreateErr := Recreate(ctx, source, r.Candidate, request.TrustedRepo, request.PrivateParent)
		if recreateErr != nil {
			r.RecreationFailure = recreateErr.Error()
			break
		}
		start := time.Now().UTC()
		o := profile.run(ctx, root, check)
		end := time.Now().UTC()
		cleanupErr := os.RemoveAll(root)
		if cleanupErr != nil {
			o.failure = "private directory cleanup: " + cleanupErr.Error()
		}
		r.Evidence = append(r.Evidence, Evidence{ID: ID(), CandidateDigest: r.Candidate.Manifest.SHA256, BaseCommit: r.Candidate.BaseCommit, ProfileID: profile.id(), CheckName: check.Name, Argv: check.Argv, EnvKeys: envKeys(check.Env), CWD: check.CWD, ExitCode: o.exit, Stdout: o.stdout, Stderr: o.stderr, Started: start, Ended: end, Duration: end.Sub(start), Verifier: request.Verifier, Environment: o.environment, Refusal: o.refusal, Failure: o.failure})
	}
	if err = r.Validate(); err != nil {
		return Report{}, err
	}
	if err = journal.SaveVerification(ctx, r); err != nil {
		return Report{}, err
	}
	return r, nil
}
