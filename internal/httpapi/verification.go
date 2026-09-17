package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/jobs"
	"github.com/korallis/letmecook/internal/repositories"
	review "github.com/korallis/letmecook/internal/review"
	"github.com/korallis/letmecook/internal/store"
	v "github.com/korallis/letmecook/internal/verification"
	"github.com/korallis/letmecook/internal/workflow"
	p "github.com/korallis/letmecook/schemas/execution"
)

type repositoryJob struct {
	Actor   string            `json:"actor"`
	Command repositoryCommand `json:"command"`
}
type verifyCommand struct {
	commandHeader
	Manifest          *p.Manifest `json:"manifest,omitempty"`
	ExpectedSelection string      `json:"expected_selection"`
}
type verificationJob struct {
	Actor   string        `json:"actor"`
	TaskID  string        `json:"task_id"`
	Command verifyCommand `json:"command"`
}
type reviewCommand struct {
	commandHeader
	VerificationID string            `json:"verification_id"`
	SelectionID    string            `json:"selection_id"`
	Action         string            `json:"action"`
	Coverage       []review.Coverage `json:"coverage"`
	Limitations    []string          `json:"limitations"`
	Notes          string            `json:"notes"`
}

// VerificationOptions is process configuration, never request or model input.
type VerificationOptions struct {
	StateDir string
	Profile  v.Profile
}

func buildProposal(ctx context.Context, d Deps, task, eligibility string) (workflow.Proposal, error) {
	return workflow.BuildGrant(ctx, d.Store, task, eligibility)
}

// WorkflowJobHandlers composes owner-only long work. The daemon may add the
// backup handler to this map before constructing jobs.NewWorker.
func WorkflowJobHandlers(d Deps, options VerificationOptions) map[string]jobs.Handler {
	handlers := map[string]jobs.Handler{}
	for _, kind := range []string{"repo-register", "repo-validate"} {
		handlers[kind] = func(ctx context.Context, j jobs.Job) (json.RawMessage, error) {
			var in repositoryJob
			if err := decodeWorkflow(j.Result, &in); err != nil {
				return nil, err
			}
			ctx = store.OwnerContext(ctx, in.Actor)
			if kind == "repo-validate" {
				result, err := d.Store.ValidateRepository(ctx, in.Actor, in.Command.Profile)
				if err != nil {
					return nil, err
				}
				return json.Marshal(result)
			}
			result, err := d.Store.RegisterRepository(ctx, in.Actor, in.Command.ExpectedRevision, in.Command.Profile)
			if err != nil {
				return nil, err
			}
			return json.Marshal(result)
		}
	}
	handlers["verify"] = func(ctx context.Context, j jobs.Job) (json.RawMessage, error) {
		var in verificationJob
		if err := decodeWorkflow(j.Result, &in); err != nil {
			return nil, err
		}
		who, err := d.Store.Authenticate(ctx, in.Actor)
		if err != nil {
			return nil, err
		}
		if who.Role != "owner" || !who.Enabled || who.Revoked {
			return nil, i.Denied
		}
		ctx = store.OwnerContext(ctx, in.Actor)
		task, err := d.Store.Task(ctx, in.TaskID)
		if err != nil {
			return nil, err
		}
		manifest, err := d.Store.HeadManifest(ctx, in.TaskID)
		if err != nil {
			return nil, err
		}
		if in.Command.Manifest != nil && *in.Command.Manifest != manifest {
			return nil, g.Deny("candidate_conflict", "manifest")
		}
		candidate, err := d.Store.SelectVerificationCandidate(ctx, in.Command.ExpectedSelection, manifest)
		if err != nil {
			return nil, err
		}
		profile, err := d.Store.RepositoryProfile(ctx, in.Actor, task.Brief.Repository)
		if err != nil {
			return nil, err
		}
		profileDigest, err := profile.Digest()
		if err != nil {
			return nil, err
		}
		grant, err := d.Store.ExecutionGrant(ctx, task.GrantHead)
		if err != nil {
			return nil, err
		}
		checks := v.TrustedChecks{ID: profileDigest, ApprovedBy: who.ID, ApprovalRef: profile.ID + ":" + strconv.FormatInt(profile.Revision, 10), Checks: []v.Check{}}
		for index, command := range profile.Verification.Commands {
			argv := append([]string(nil), command.Argv...)
			if !filepath.IsAbs(argv[0]) {
				var found string
				for _, dir := range []string{"/usr/bin", "/bin", "/usr/local/bin", "/opt/homebrew/bin"} {
					path := filepath.Join(dir, argv[0])
					info, err := os.Stat(path)
					if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
						found = path
						break
					}
				}
				if found == "" {
					return nil, g.Deny("store_unavailable", "trusted_check_binary")
				}
				argv[0] = found
			}
			checks.Checks = append(checks.Checks, v.Check{Name: profile.Verification.Name + "-" + strconv.Itoa(index+1), Argv: argv, CWD: command.Directory, Timeout: time.Duration(min(command.TimeoutMS, int64(time.Hour/time.Millisecond))) * time.Millisecond, Required: true, Env: map[string]string{"PATH": "/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin"}})
		}
		if options.StateDir == "" || !filepath.IsAbs(options.StateDir) {
			return nil, g.Deny("store_unavailable", "verification_state")
		}
		parent := filepath.Join(options.StateDir, "verify")
		if err = os.MkdirAll(parent, 0700); err != nil {
			return nil, err
		}
		parent, err = filepath.EvalSymlinks(parent)
		if err != nil {
			return nil, err
		}
		// Keep remote, pinned base and trusted commands unchanged; only the daemon's
		// private checkout destination differs from the runner's execution root.
		checkoutProfile := profile
		checkoutProfile.RunnerRoots = []repositories.RunnerRoot{{RunnerID: grant.Envelope.Runners[0], Root: parent}}
		checkoutDigest, err := checkoutProfile.Digest()
		if err != nil {
			return nil, err
		}
		selection := repositories.Selection{Repository: profile.ID, Revision: profile.Revision, ProfileDigest: checkoutDigest, Remote: profile.Remote, BaseCommit: profile.Base.Commit, RunnerRoot: checkoutProfile.RunnerRoots[0]}
		checkout, err := repositories.Prepare(ctx, checkoutProfile, selection)
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(checkout.Path)
		verifier := options.Profile
		if verifier == nil {
			verifier = v.Unqualified{}
		}
		metadata, err := d.Store.Status(ctx)
		if err != nil {
			return nil, err
		}
		report, err := v.Run(ctx, d.Store, d.Store, verifier, v.Request{ReportID: workflow.IntentID(j.ID, "verification"), Candidate: candidate, TrustedRepo: checkout.Path, PrivateParent: parent, Verifier: "gafferd:" + metadata.DaemonBoot, Checks: checks, Envelope: &grant.Envelope, RepositoryProfile: &profile})
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"verification_id": report.ID, "selection_id": report.Candidate.SelectionID})
	}
	return handlers
}
func init() {
	Register("verification", func(d Deps) []Route {
		return []Route{
			ownerRoute("POST", "/api/v1/tasks/{id}/verify", ownerMutation(d, "task.verify", func(ctx context.Context, a Actor, r Request, in verifyCommand) (any, int, error) {
				if in.ExpectedSelection != "" && !p.ValidID(in.ExpectedSelection) {
					return nil, 0, g.Deny("invalid_id", "expected_selection")
				}
				if d.Jobs == nil {
					return nil, 0, g.Deny("store_unavailable", "jobs")
				}
				if _, err := d.Store.Task(ctx, r.Path["id"]); err != nil {
					return nil, 0, err
				}
				raw, _ := json.Marshal(verificationJob{Actor: a.Fingerprint, TaskID: r.Path["id"], Command: in})
				job, err := d.Jobs.Submit(ctx, jobs.Job{ID: in.MessageID, Kind: "verify", SubjectID: r.Path["id"], Result: raw})
				return map[string]string{"job_id": job.ID}, 202, err
			})),
			readRoute("/api/v1/verifications/{id}", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
				if err := pathID(r); err != nil {
					return nil, err
				}
				return d.Store.Verification(ctx, r.Path["id"])
			}),
			readRoute("/api/v1/tasks/{id}/verification", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
				if err := pathID(r); err != nil {
					return nil, err
				}
				report, status, err := d.Store.CurrentVerification(ctx, r.Path["id"])
				return map[string]any{"report": report, "status": status}, err
			}),
			ownerRoute("POST", "/api/v1/tasks/{id}/review", ownerMutation(d, "task.review", func(ctx context.Context, a Actor, r Request, in reviewCommand) (any, int, error) {
				if !p.ValidID(in.VerificationID) || !p.ValidID(in.SelectionID) || len(in.Notes) > 8192 || !slices.Contains([]string{"accept", "reject"}, in.Action) {
					return nil, 0, g.Deny("malformed", "review")
				}
				report, err := d.Store.Verification(ctx, in.VerificationID)
				if err != nil {
					return nil, 0, err
				}
				if report.Candidate.Identity.TaskID != r.Path["id"] || report.Candidate.SelectionID != in.SelectionID {
					return nil, 0, g.Deny("candidate_conflict", "selection_id")
				}
				task, err := d.Store.Task(ctx, r.Path["id"])
				if err != nil {
					return nil, 0, err
				}
				grant, err := d.Store.ExecutionGrant(ctx, task.GrantHead)
				if err != nil {
					return nil, 0, err
				}
				limitations := append([]string{}, in.Limitations...)
				if in.Notes != "" {
					limitations = append(limitations, "owner note: "+in.Notes)
				}
				decision := review.Decision{ID: in.MessageID, Candidate: report.Candidate, VerificationID: report.ID, EvidenceIDs: review.EvidenceIDs(report), Grant: review.GrantReference{ID: grant.ID, Revision: grant.Revision}, Action: in.Action, Actor: a.ID, Coverage: in.Coverage, Limitations: limitations, At: time.Now().UTC()}
				history, err := d.Store.LocalDecisionHistory(ctx, r.Path["id"])
				if err != nil {
					return nil, 0, err
				}
				for _, old := range history {
					if old.ID == decision.ID {
						decision.At = old.At
						break
					}
				}
				if err = review.ValidateDecision(decision, report); err != nil {
					if strings.Contains(err.Error(), "unverified") {
						return nil, 0, g.Deny("verification_required", "verification_id")
					}
					return nil, 0, g.Deny("malformed", "review")
				}
				if err = authOwner(ctx, d, a); err != nil {
					return nil, 0, err
				}
				if err = d.Store.RecordLocalDecision(ctx, decision); err != nil {
					return nil, 0, err
				}
				current, err := d.Store.CurrentLocalReview(ctx, r.Path["id"])
				return current, 201, err
			})),
			readRoute("/api/v1/tasks/{id}/review", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
				if err := pathID(r); err != nil {
					return nil, err
				}
				current, err := d.Store.CurrentLocalReview(ctx, r.Path["id"])
				if err != nil {
					return nil, err
				}
				history, err := d.Store.LocalDecisionHistory(ctx, r.Path["id"])
				return map[string]any{"current": current, "history": history}, err
			}),
		}
	})
}
