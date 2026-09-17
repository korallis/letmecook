package store

import (
	"bufio"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	r "github.com/korallis/letmecook/internal/review"
	v "github.com/korallis/letmecook/internal/verification"
	"modernc.org/sqlite"
)

const dropVerificationSchema = `DROP TABLE review_invocations; DROP TABLE review_decisions; DROP TABLE verification_evidence; DROP TABLE verification_reports; DROP TABLE verification_candidates;`

func verificationFixture(t *testing.T, s *Store) (v.Report, r.Decision) {
	t.Helper()
	request, _ := custodyFixture(t, s)
	var manifest CandidateManifest
	if err := json.Unmarshal(request.Manifest, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Base = ArtifactBase{Revision: strings.Repeat("b", 40), SHA256: v.Digest([]byte(strings.Repeat("b", 40)))}
	request.Manifest, _ = json.Marshal(manifest)
	request.Result.Manifest.SHA256 = v.Digest(request.Manifest)
	request.Result.Manifest.Bytes = int64(len(request.Manifest))
	if _, err := s.CustodyResult(ctx, request); err != nil {
		t.Fatal(err)
	}
	c, err := s.SelectVerificationCandidate(ctx, "", *request.Result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	grant := grantFixture()
	grant.TaskID = c.Identity.TaskID
	grant.Envelope.BaseCommit = c.BaseCommit
	grant = approve(t, s, "", grant)
	checks := v.TrustedChecks{ID: "test-approved", ApprovedBy: "operator", ApprovalRef: "test-approval", Checks: []v.Check{}}
	evidence := []v.Evidence{}
	for _, name := range []string{"first", "second"} {
		check := v.Check{Name: name, Argv: []string{"/bin/true"}, Env: map[string]string{"PATH": "/no-docker-in-test-environment"}, CWD: ".", Timeout: time.Second, Required: true}
		checks.Checks = append(checks.Checks, check)
		zero := 0
		at := time.Now().UTC()
		evidence = append(evidence, v.Evidence{ID: v.ID(), CandidateDigest: c.Manifest.SHA256, BaseCommit: c.BaseCommit, ProfileID: "test-only-unconfined", CheckName: name, Argv: check.Argv, EnvKeys: []string{"PATH"}, CWD: ".", ExitCode: &zero, Stdout: v.Stream{Prefix: []byte{}, SHA256: v.Digest(nil)}, Stderr: v.Stream{Prefix: []byte{}, SHA256: v.Digest(nil)}, Started: at, Ended: at, Verifier: "synthetic-store-fixture", Environment: v.Environment{OS: "synthetic", Arch: "synthetic", DockerBinary: "absent", DockerSearchPath: check.Env["PATH"], ExpectedConfinement: "test-only-unconfined", ObservedConfinement: "test-only-unconfined"}})
	}
	report := v.Report{ID: v.ID(), Candidate: c, Checks: checks, Evidence: evidence, Suggestions: []v.Suggestion{{Source: "model", Text: "success is not verification"}}, Limitations: []string{v.ContentLimitation, "synthetic persistence fixture; no actual checks executed"}}
	d := r.Decision{ID: v.ID(), Candidate: c, VerificationID: report.ID, EvidenceIDs: r.EvidenceIDs(report), Grant: r.GrantReference{ID: grant.ID, Revision: grant.Revision}, Action: "accept", Actor: "operator", Coverage: []r.Coverage{{CriterionID: "c1", Status: "partial", EvidenceIDs: []string{evidence[0].ID}, Explanation: "synthetic metadata check"}, {CriterionID: "c2", Status: "not-covered", EvidenceIDs: []string{}, Explanation: "runtime unqualified"}}, Limitations: append([]string{}, report.Limitations...), At: time.Now().UTC()}
	return report, d
}
func mustSaveVerification(t *testing.T, s *Store, report v.Report) {
	t.Helper()
	if err := s.SaveVerification(ctx, report); err != nil {
		t.Fatal(err)
	}
}

func TestVerificationDecisionHistoryReopenAndLostAck(t *testing.T) {
	s, artifacts := persistent(t)
	report, d := verificationFixture(t, s)
	mustSaveVerification(t, s, report)
	mustSaveVerification(t, s, report)
	if err := s.RecordLocalDecision(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLocalDecision(ctx, d); err != nil {
		t.Fatal(err)
	}
	current, err := s.CurrentLocalReview(ctx, d.Candidate.Identity.TaskID)
	if err != nil || !current.Accepted {
		t.Fatal(current, err)
	}
	_, status, err := s.CurrentVerification(ctx, d.Candidate.Identity.TaskID)
	if err != nil || !status.Verified {
		t.Fatal(status, err)
	}
	d2 := d
	d2.ID, d2.Action = v.ID(), "reject"
	if err = s.RecordLocalDecision(ctx, d2); err != nil {
		t.Fatal(err)
	}
	current, err = s.CurrentLocalReview(ctx, d.Candidate.Identity.TaskID)
	if err != nil || current.Accepted || current.Decision.ID != d2.ID {
		t.Fatal(current, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	history, err := s2.LocalDecisionHistory(ctx, d.Candidate.Identity.TaskID)
	if err != nil || !reflect.DeepEqual(history, []r.Decision{d, d2}) {
		t.Fatal(history, err)
	}
	got, err := s2.Verification(ctx, report.ID)
	if err != nil || !reflect.DeepEqual(got, report) {
		t.Fatal(got, err)
	}
	conflict := report
	conflict.Suggestions = nil
	if err = s2.SaveVerification(ctx, conflict); err == nil {
		t.Fatal("conflicting duplicate report")
	}
	conflictDecision := d
	conflictDecision.Actor = "another"
	if err = s2.RecordLocalDecision(ctx, conflictDecision); err == nil {
		t.Fatal("conflicting duplicate decision")
	}
}

func TestVerificationRefusesMissingFailedForgedAndStaleDecisions(t *testing.T) {
	for _, kind := range []string{"missing", "failed", "refused", "wrong-grant", "wrong-revision", "omitted-criterion", "omitted-limitation", "foreign-evidence", "revoked"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := persistent(t)
			report, d := verificationFixture(t, s)
			switch kind {
			case "missing":
				report.Evidence = report.Evidence[:1]
				d.EvidenceIDs = r.EvidenceIDs(report)
			case "failed":
				code := 9
				report.Evidence[0].ExitCode = &code
			case "refused":
				e := &report.Evidence[0]
				e.ProfileID = "unqualified"
				e.ExitCode = nil
				e.Refusal = &v.Refusal{Code: "unqualified_profile", Reason: v.UnqualifiedReason, Environment: e.Environment}
			case "wrong-grant":
				d.Grant.ID = v.ID()
			case "wrong-revision":
				d.Grant.Revision++
			case "omitted-criterion":
				d.Coverage = d.Coverage[:1]
			case "omitted-limitation":
				d.Limitations = []string{}
			case "foreign-evidence":
				d.EvidenceIDs[0] = v.ID()
			case "revoked":
				if err := s.InvalidateExecution(ctx, d.Candidate.Identity.TaskID, d.Grant.ID, "operator", "revoked"); err != nil {
					t.Fatal(err)
				}
			}
			mustSaveVerification(t, s, report)
			if err := s.RecordLocalDecision(ctx, d); err == nil {
				t.Fatal("invalid acceptance persisted")
			}
			history, err := s.LocalDecisionHistory(ctx, d.Candidate.Identity.TaskID)
			if err != nil || len(history) != 0 {
				t.Fatal(history, err)
			}
		})
	}
}

// #18 currently permits only one current receipt per task. Seed a second
// non-quarantined historical receipt directly to test this slice's replacement
// semantics without rewriting #18 or pretending it has a replacement ingress.
func replacementCandidate(t *testing.T, s *Store, old v.Candidate) v.Candidate {
	t.Helper()
	raw, err := s.CandidateManifest(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	var m CandidateManifest
	if err = json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Identity.AttemptID = v.ID()
	m.Identity.Epoch++
	m.Outcome = "failed"
	raw, _ = json.Marshal(m)
	ref := old.Manifest
	ref.ManifestID = v.ID()
	ref.SHA256 = v.Digest(raw)
	ref.Bytes = int64(len(raw))
	if _, err = s.db.Exec("INSERT INTO artifact_manifests VALUES(?,?,?,?,?,?)", ref.ManifestID, ref.SHA256, ref.Bytes, raw, time.Now().UnixMilli(), "committed"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("INSERT INTO artifact_results VALUES(?,?,?,?,?,?,?,?,?,?,?)", m.Identity.Generation, m.Identity.TaskID, m.Identity.AttemptID, m.Identity.Epoch, ref.ManifestID, ref.SHA256, ref.Bytes, v.ID(), 0, 0, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("INSERT INTO artifact_manifest_blobs SELECT ?,digest,role,path,bytes FROM artifact_manifest_blobs WHERE manifest_id=?", ref.ManifestID, old.Manifest.ManifestID); err != nil {
		t.Fatal(err)
	}
	c, err := s.SelectVerificationCandidate(ctx, old.SelectionID, ref)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestCandidateReplacementInvalidatesEvidenceAcceptanceAndLateWrites(t *testing.T) {
	s, _ := persistent(t)
	report, d := verificationFixture(t, s)
	mustSaveVerification(t, s, report)
	if err := s.RecordLocalDecision(ctx, d); err != nil {
		t.Fatal(err)
	}
	changed := replacementCandidate(t, s, report.Candidate)
	retry, err := s.SelectVerificationCandidate(ctx, report.Candidate.SelectionID, changed.Manifest)
	if err != nil || retry != changed {
		t.Fatal("replacement lost-ack replay", retry, err)
	}
	if v.Evaluate(report, changed).Verified {
		t.Fatal("stale evidence passed")
	}
	current, err := s.CurrentLocalReview(ctx, d.Candidate.Identity.TaskID)
	if err != nil || current.Accepted {
		t.Fatal(current, err)
	}
	_, status, err := s.CurrentVerification(ctx, d.Candidate.Identity.TaskID)
	if err != nil || status.Verified {
		t.Fatal("replacement retained verified status", status, err)
	}
	// Late evidence remains inspectable history and cannot select itself current.
	late := report
	late.ID = v.ID()
	late.Evidence = append([]v.Evidence{}, report.Evidence...)
	for n := range late.Evidence {
		late.Evidence[n].ID = v.ID()
	}
	mustSaveVerification(t, s, late)
	lateDecision := d
	lateDecision.ID = v.ID()
	if err = s.RecordLocalDecision(ctx, lateDecision); err == nil {
		t.Fatal("late decision accepted")
	}
	if err = s.RecordLocalDecision(ctx, d); err != nil {
		t.Fatal("lost-ack receipt should replay", err)
	}
	current, err = s.CurrentLocalReview(ctx, d.Candidate.Identity.TaskID)
	if err != nil || current.Accepted {
		t.Fatal("retry reactivated acceptance", current, err)
	}
	if _, err = s.SelectVerificationCandidate(ctx, report.Candidate.SelectionID, report.Candidate.Manifest); err == nil {
		t.Fatal("stale CAS selected old candidate")
	}
	back, err := s.SelectVerificationCandidate(ctx, changed.SelectionID, report.Candidate.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if back.SelectionID == report.Candidate.SelectionID || v.Evaluate(report, back).Verified {
		t.Fatal("A-B-A reused selection")
	}
}
func TestNewVerificationInvalidatesPriorAcceptance(t *testing.T) {
	s, _ := persistent(t)
	report, d := verificationFixture(t, s)
	mustSaveVerification(t, s, report)
	if err := s.RecordLocalDecision(ctx, d); err != nil {
		t.Fatal(err)
	}
	next := report
	next.ID = v.ID()
	next.Evidence = nil
	mustSaveVerification(t, s, next)
	current, err := s.CurrentLocalReview(ctx, d.Candidate.Identity.TaskID)
	if err != nil || current.Accepted {
		t.Fatal(current, err)
	}
}

func TestCandidateBlobTamperRefusesAcceptanceAndCurrentStatus(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprint(accepted), func(t *testing.T) {
			s, _ := persistent(t)
			report, d := verificationFixture(t, s)
			mustSaveVerification(t, s, report)
			if accepted {
				if err := s.RecordLocalDecision(ctx, d); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := s.CandidateManifest(ctx, report.Candidate)
			if err != nil {
				t.Fatal(err)
			}
			var m CandidateManifest
			if err = json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(blobPath(s.artifacts, m.Tracked[0].SHA256), []byte("tampered"), 0600); err != nil {
				t.Fatal(err)
			}
			if accepted {
				current, err := s.CurrentLocalReview(ctx, d.Candidate.Identity.TaskID)
				if err == nil || current.Accepted {
					t.Fatal("corrupt bytes accepted", current, err)
				}
			} else if err = s.RecordLocalDecision(ctx, d); err == nil {
				t.Fatal("tampered artifact accepted")
			}
		})
	}
}

func TestVerificationRealSQLiteFullAndCancelledWrite(t *testing.T) {
	s, _ := persistent(t)
	report, _ := verificationFixture(t, s)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.SaveVerification(cancelled, report); err == nil {
		t.Fatal("cancelled persistence passed")
	}
	var pages int
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	sqlExec(t, s, fmt.Sprintf("PRAGMA max_page_count=%d", pages))
	report.Suggestions = []v.Suggestion{{Source: "worker", Text: strings.Repeat("untrusted data ", 10000)}}
	err := s.SaveVerification(ctx, report)
	var full *sqlite.Error
	if !errors.As(err, &full) || full.Code() != 13 {
		t.Fatal("expected real SQLITE_FULL", err)
	}
	if _, err = s.Verification(ctx, report.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("partial full-disk report", err)
	}
	sqlExec(t, s, "PRAGMA max_page_count=1073741823")
	mustSaveVerification(t, s, report)
}

func TestProductVerificationPersistsDockerUnavailableRefusal(t *testing.T) {
	s, artifacts := persistent(t)
	request, _ := custodyFixture(t, s)
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=f@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=f@example.invalid"}
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(string(b), err)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "base")
	var m CandidateManifest
	if err := json.Unmarshal(request.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	m.Base.Revision = git("rev-parse", "HEAD")
	m.Base.SHA256 = v.Digest([]byte(m.Base.Revision))
	request.Manifest, _ = json.Marshal(m)
	request.Result.Manifest.SHA256 = v.Digest(request.Manifest)
	request.Result.Manifest.Bytes = int64(len(request.Manifest))
	if _, err := s.CustodyResult(ctx, request); err != nil {
		t.Fatal(err)
	}
	c, err := s.SelectVerificationCandidate(ctx, "", *request.Result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "must-not-run")
	checks := v.TrustedChecks{ID: "approved", ApprovedBy: "operator", ApprovalRef: "fixture-approval", Checks: []v.Check{{Name: "required", Argv: []string{"/usr/bin/touch", marker}, Env: map[string]string{"PATH": t.TempDir()}, CWD: ".", Timeout: time.Second, Required: true}}}
	report, err := v.Run(ctx, s, s, v.Unqualified{ExpectedConfinement: "expected", ObservedConfinement: "drifted"}, v.Request{Candidate: c, TrustedRepo: repo, PrivateParent: t.TempDir(), Verifier: "fixture-verifier", Checks: checks})
	if err != nil {
		t.Fatal(err)
	}
	if report.RecreationFailure != "" || len(report.Evidence) != 1 || v.Evaluate(report, c).Verified {
		t.Fatal(report)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("product check executed", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Verification(ctx, report.ID)
	if err != nil || !reflect.DeepEqual(got, report) || got.Evidence[0].Environment.DockerBinary != "absent" || !got.Evidence[0].Environment.ConfinementDrift || got.Evidence[0].Refusal.Reason != v.UnqualifiedReason {
		t.Fatal(got, err)
	}
}

func TestFreshReviewerPacketRetainsActualEvidenceAndPermission(t *testing.T) {
	s, artifacts := persistent(t)
	report, decision := verificationFixture(t, s)
	mustSaveVerification(t, s, report)
	route := r.PermittedRoute{ID: "operator-review-route", PermissionRef: "separate-review-permission", PolicyDigest: strings.Repeat("a", 64), PermittedBy: "operator", SelectionReason: "operator-selected independent review"}
	i, err := r.FreshInvocation(report, decision.Grant, []r.Criterion{{ID: "c1", Text: "inspect exact candidate"}, {ID: "c2", Text: "report remaining limitations"}}, route)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordReviewInvocation(ctx, i); err != nil {
		t.Fatal(err)
	}
	incomplete, err := r.FreshInvocation(report, decision.Grant, i.Criteria[:1], route)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordReviewInvocation(ctx, incomplete); err == nil {
		t.Fatal("review omitted approved criterion")
	}
	if err = s.RecordReviewInvocation(ctx, i); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.ReviewInvocation(ctx, i.ID)
	if err != nil || !reflect.DeepEqual(got, i) {
		t.Fatal(got, err)
	}
	forged := i
	forged.ID = v.ID()
	forged.ContextID = v.ID()
	forged.Verification.Suggestions = nil
	if err = reopened.RecordReviewInvocation(ctx, forged); err == nil {
		t.Fatal("review accepted edited actual evidence")
	}
	duplicateContext := i
	duplicateContext.ID = v.ID()
	if err = reopened.RecordReviewInvocation(ctx, duplicateContext); err == nil {
		t.Fatal("reused reviewer context")
	}
	if _, err = r.FreshInvocation(report, decision.Grant, i.Criteria, r.PermittedRoute{ID: "a-model-review-suffix"}); err == nil {
		t.Fatal("suffix substituted for permission")
	}
	history, err := reopened.LocalDecisionHistory(ctx, report.Candidate.Identity.TaskID)
	if err != nil || len(history) != 0 {
		t.Fatal("review granted acceptance", history, err)
	}
}

func TestVerificationAtomicFailureCorruptionAndImmutability(t *testing.T) {
	s, _ := persistent(t)
	report, d := verificationFixture(t, s)
	sqlExec(t, s, "CREATE TRIGGER fail_evidence BEFORE INSERT ON verification_evidence WHEN NEW.ordinal=1 BEGIN SELECT RAISE(ABORT,'database or disk is full'); END")
	if err := s.SaveVerification(ctx, report); err == nil {
		t.Fatal("partial evidence acknowledged")
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM verification_reports").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	sqlExec(t, s, "DROP TRIGGER fail_evidence")
	mustSaveVerification(t, s, report)
	if err := s.RecordLocalDecision(ctx, d); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"verification_candidates", "verification_reports", "verification_evidence", "review_decisions"} {
		if _, err := s.db.Exec("DELETE FROM " + table); err == nil {
			t.Fatal("mutable history", table)
		}
		if _, err := s.db.Exec("UPDATE " + table + " SET id=id"); err == nil {
			t.Fatal("mutable record", table)
		}
	}
	sqlExec(t, s, "DROP TRIGGER verification_reports_no_update")
	if _, err := s.db.Exec("UPDATE verification_reports SET body=? WHERE id=?", []byte(`{}`), report.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verification(ctx, report.ID); err == nil {
		t.Fatal("corruption trusted")
	}
	if current, err := s.CurrentLocalReview(ctx, d.Candidate.Identity.TaskID); err == nil || current.Accepted {
		t.Fatal("corrupt report accepted", current, err)
	}
}

func TestVerificationSchemaEightMigration(t *testing.T) {
	s, artifacts := persistent(t)
	report, _ := verificationFixture(t, s)
	generation := s.meta.Generation
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(s.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(dropSchema10 + dropVerificationSchema + "PRAGMA user_version=8"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.meta.SchemaVersion != 10 || reopened.meta.Generation != generation {
		t.Fatal(reopened.meta)
	}
	if _, err = reopened.SelectVerificationCandidate(ctx, "", report.Candidate.Manifest); err != nil {
		t.Fatal("custody lost by migration", err)
	}
}

// Re-exec pause control exists only in this test binary. Interrupt after a report
// and one evidence index have been written, or after COMMIT but before the ack.
func TestVerificationOwnedProcess(t *testing.T) {
	mode := os.Getenv("GAFFER_VERIFICATION_TEST_CHILD")
	if mode == "" {
		return
	}
	s, err := Open(ctx, os.Getenv("GAFFER_VERIFICATION_DIR"), os.Getenv("GAFFER_VERIFICATION_ARTIFACTS"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw, err := base64.StdEncoding.DecodeString(os.Getenv("GAFFER_VERIFICATION_REPORT"))
	if err != nil {
		t.Fatal(err)
	}
	var report v.Report
	if err = json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if mode == "interrupted" {
		err = sqlite.RegisterScalarFunction("verification_test_pause", 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
			fmt.Println("interrupted")
			_, _ = bufio.NewReader(os.Stdin).ReadByte()
			return int64(1), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		s.db.SetMaxIdleConns(0)
		if err = s.db.Ping(); err != nil {
			t.Fatal(err)
		}
		s.db.SetMaxIdleConns(1)
		sqlExec(t, s, "PRAGMA trusted_schema=ON")
		sqlExec(t, s, "CREATE TRIGGER verification_pause BEFORE INSERT ON verification_evidence WHEN NEW.ordinal=1 BEGIN SELECT verification_test_pause(); END")
	}
	if err = s.SaveVerification(ctx, report); err != nil {
		t.Fatal(err)
	}
	fmt.Println("committed")
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
}
func TestVerificationSIGKILLAtomicPersistence(t *testing.T) {
	for _, mode := range []string{"interrupted", "committed"} {
		t.Run(mode, func(t *testing.T) {
			s, artifacts := persistent(t)
			report, _ := verificationFixture(t, s)
			raw, _ := json.Marshal(report)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(deadline, os.Args[0], "-test.run=^TestVerificationOwnedProcess$")
			cmd.Env = append(os.Environ(), "GAFFER_VERIFICATION_TEST_CHILD="+mode, "GAFFER_VERIFICATION_DIR="+s.dir, "GAFFER_VERIFICATION_ARTIFACTS="+artifacts, "GAFFER_VERIFICATION_REPORT="+base64.StdEncoding.EncodeToString(raw))
			cmd.Stderr = os.Stderr
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			scanner := bufio.NewScanner(stdout)
			if !scanner.Scan() || scanner.Text() != mode {
				t.Fatal("child boundary", scanner.Text(), scanner.Err())
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal("termination not observed", err)
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatal("not SIGKILL", exit)
			}
			reopened, err := Open(ctx, s.dir, artifacts)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			got, err := reopened.Verification(ctx, report.ID)
			if mode == "interrupted" {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("torn record visible", got, err)
				}
				var n int
				if err = reopened.db.QueryRow("SELECT count(*) FROM verification_evidence").Scan(&n); err != nil || n != 0 {
					t.Fatal(n, err)
				}
				sqlExec(t, reopened, "DROP TRIGGER verification_pause")
			} else if err != nil || !reflect.DeepEqual(got, report) {
				t.Fatal("committed record incomplete", got, err)
			}
			mustSaveVerification(t, reopened, report)
			got, err = reopened.Verification(ctx, report.ID)
			if err != nil || !reflect.DeepEqual(got, report) {
				t.Fatal("retry lost evidence", got, err)
			}
		})
	}
}
