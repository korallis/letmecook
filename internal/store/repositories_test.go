package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	i "github.com/korallis/letmecook/internal/identity"
	r "github.com/korallis/letmecook/internal/repositories"
)

func repositoryGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}
func repositoryFixture(t *testing.T, s *Store) (r.Profile, string, string) {
	t.Helper()
	owner, runner := strings.Repeat("a", 64), strings.Repeat("b", 64)
	if err := s.BootstrapOwner(ctx, owner, false); err != nil {
		t.Fatal(err)
	}
	invite, err := s.CreateEnrollment(ctx, owner, newID(), runner)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := s.Enroll(ctx, runner, invite.Token)
	if err != nil {
		t.Fatal(err)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(base, "remote.git")
	repositoryGit(t, base, "init", "--quiet", "--template=", "--initial-branch=main", remote)
	if err := os.WriteFile(filepath.Join(remote, "file"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	repositoryGit(t, remote, "add", ".")
	repositoryGit(t, remote, "commit", "--quiet", "-m", "base")
	sha := repositoryGit(t, remote, "rev-parse", "HEAD")
	u := url.URL{Scheme: "file", Path: remote}
	root := filepath.Join(base, "runner")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	profile := r.Profile{Version: r.Version, ID: "fixture", Revision: 1, Remote: u.String(), Base: r.Base{Ref: "refs/heads/main", Commit: sha, Policy: "pinned"}, ProtectedPaths: []string{"policy"}, ContextScope: []string{"."}, Verification: r.Verification{Name: "fixture", Commands: []r.Command{{Argv: []string{"go", "test", "./..."}, Directory: ".", TimeoutMS: 1000}}}, RunnerRoots: []r.RunnerRoot{{RunnerID: identity.ID, Root: root}}}
	return profile, owner, runner
}
func repositorySelection(t *testing.T, profile r.Profile) r.Selection {
	t.Helper()
	digest, err := profile.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return r.Selection{Repository: profile.ID, Revision: profile.Revision, ProfileDigest: digest, Remote: profile.Remote, BaseCommit: profile.Base.Commit, RunnerRoot: profile.RunnerRoots[0]}
}
func TestRepositoryRegistrationPolicyAndIdentity(t *testing.T) {
	s, artifacts := persistent(t)
	profile, owner, runner := repositoryFixture(t, s)
	before := snapshot(t, s)
	if _, err := s.ValidateRepository(ctx, runner, profile); !errors.Is(err, i.Denied) {
		t.Fatal("runner validation", err)
	}
	if _, err := s.RegisterRepository(ctx, runner, 0, profile); !errors.Is(err, i.Denied) {
		t.Fatal("runner registration", err)
	}
	got, err := s.RegisterRepository(ctx, owner, 0, profile)
	if err != nil || !reflect.DeepEqual(got, profile) {
		t.Fatal(got, err)
	}
	if _, err := s.RegisterRepository(ctx, owner, 0, profile); err != nil {
		t.Fatal("replay", err)
	}
	if !reflect.DeepEqual(snapshot(t, s), before) {
		t.Fatal("registration created task or event")
	}
	var grants int
	if err := s.db.QueryRow("SELECT count(*) FROM execution_grants").Scan(&grants); err != nil || grants != 0 {
		t.Fatal("implicit execution grant", grants, err)
	}
	sel := repositorySelection(t, profile)
	if err := s.CheckRepositorySelection(ctx, sel); !errors.Is(err, i.Denied) {
		t.Fatal("registration enabled runner", err)
	}
	if _, err := s.UpdateIdentity(ctx, owner, sel.RunnerRoot.RunnerID, 1, "enable", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckRepositorySelection(ctx, sel); err != nil {
		t.Fatal(err)
	}
	changed := profile
	changed.ProtectedPaths = []string{}
	if _, err := s.RegisterRepository(ctx, owner, 1, changed); !errors.Is(err, i.Conflict) {
		t.Fatal("same revision weakened protection", err)
	}
	changed.Revision++
	if _, err := s.RegisterRepository(ctx, owner, 1, changed); err != nil {
		t.Fatal("owner reapproval", err)
	}
	if err := s.CheckRepositorySelection(ctx, sel); !errors.Is(err, i.Denied) {
		t.Fatal("stale profile selected", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := reopened.RepositoryProfile(ctx, owner, profile.ID)
	if err != nil || !reflect.DeepEqual(stored, changed) {
		t.Fatal("lost profile", err)
	}
	if _, err := reopened.RepositoryProfile(ctx, runner, profile.ID); !errors.Is(err, i.Denied) {
		t.Fatal("runner profile read", err)
	}
	if err := reopened.CheckRepositorySelection(ctx, repositorySelection(t, changed)); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.UpdateIdentity(ctx, owner, sel.RunnerRoot.RunnerID, 2, "revoke", ""); err != nil {
		t.Fatal(err)
	}
	if err := reopened.CheckRepositorySelection(ctx, repositorySelection(t, changed)); !errors.Is(err, i.Denied) {
		t.Fatal("revoked runner", err)
	}
	changed.Revision++
	if _, err := reopened.RegisterRepository(ctx, owner, 2, changed); !errors.Is(err, i.Denied) {
		t.Fatal("revoked runner reapproved", err)
	}
}

func TestRepositoryInaccessibleRelabelAndTransactionFailure(t *testing.T) {
	s, _ := persistent(t)
	profile, owner, _ := repositoryFixture(t, s)
	missing := profile
	missing.Remote += "-missing"
	if _, err := s.RegisterRepository(ctx, owner, 0, missing); err == nil {
		t.Fatal("inaccessible registered")
	}
	if _, err := s.RepositoryProfile(ctx, owner, profile.ID); !errors.Is(err, i.Conflict) {
		t.Fatal("failed validation stored", err)
	}
	sqlExec(t, s, "CREATE TRIGGER fail_repository BEFORE INSERT ON repository_profiles BEGIN SELECT RAISE(ABORT,'private SQL failure'); END")
	if _, err := s.RegisterRepository(ctx, owner, 0, profile); !errors.Is(err, i.Unavailable) {
		t.Fatal("failed commit acknowledged", err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM repositories").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial identity", err)
	}
	sqlExec(t, s, "DROP TRIGGER fail_repository")
	if _, err := s.RegisterRepository(ctx, owner, 0, profile); err != nil {
		t.Fatal(err)
	}
	alias := profile
	alias.ID = "alias"
	if _, err := s.RegisterRepository(ctx, owner, 0, alias); !errors.Is(err, i.Conflict) {
		t.Fatal("same remote relabel", err)
	}
	other := filepath.Join(filepath.Dir(profile.RunnerRoots[0].Root), "different.git")
	repositoryGit(t, filepath.Dir(other), "clone", "--quiet", "--bare", "--no-local", "--template=", profile.Remote, other)
	u := url.URL{Scheme: "file", Path: other}
	alias = profile
	alias.Revision++
	alias.Remote = u.String()
	if _, err := s.RegisterRepository(ctx, owner, 1, alias); !errors.Is(err, i.Conflict) {
		t.Fatal("project relabel", err)
	}
	for _, query := range []string{"UPDATE repositories SET remote='changed'", "DELETE FROM repositories", "UPDATE repository_profiles SET revision=2", "DELETE FROM repository_profiles"} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatal("immutable contract", query)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	alias = profile
	alias.Revision++
	if _, err := s.RegisterRepository(cancelled, owner, 1, alias); err == nil {
		t.Fatal("cancelled registration")
	}
	current, err := s.RepositoryProfile(ctx, owner, profile.ID)
	if err != nil || !reflect.DeepEqual(current, profile) {
		t.Fatal("failed writes changed profile", err)
	}
}

func TestRepositoryConcurrentApprovalAndSchemaFourMigration(t *testing.T) {
	s, artifacts := persistent(t)
	profile, owner, _ := repositoryFixture(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(s.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE repository_profiles; DROP TABLE repositories; PRAGMA user_version=4"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.meta.SchemaVersion != 5 {
		t.Fatal("migration")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for n := range 2 {
		wg.Go(func() {
			v := profile
			if n == 1 {
				v.ProtectedPaths = []string{"another"}
			}
			_, err := reopened.RegisterRepository(ctx, owner, 0, v)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, i.Conflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	fixture := owned(t)
	if _, err := fixture.RegisterRepository(ctx, owner, 0, profile); !errors.Is(err, i.Unavailable) {
		t.Fatal("fixture registration", err)
	}
}
