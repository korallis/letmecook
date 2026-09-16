package repositories

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSuffix(string(out), "\n")
}
func put(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}
func profileFixture(t *testing.T) (Profile, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operator := filepath.Join(base, "operator")
	git(t, base, "init", "--quiet", "--template=", "--initial-branch=main", operator)
	put(t, filepath.Join(operator, "main.txt"), "base\n", 0600)
	put(t, filepath.Join(operator, "policy", "checks"), "approved\n", 0600)
	git(t, operator, "add", ".")
	git(t, operator, "commit", "--quiet", "-m", "base")
	sha := git(t, operator, "rev-parse", "HEAD")
	remote := filepath.Join(base, "remote.git")
	git(t, base, "clone", "--quiet", "--bare", "--no-local", "--template=", operator, remote)
	root := filepath.Join(base, "runner")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "file", Path: remote}
	v := Profile{Version: Version, ID: "fixture", Revision: 1, Remote: u.String(), Base: Base{Ref: "refs/heads/main", Commit: sha, Policy: "pinned"}, ProtectedPaths: []string{"policy"}, Verification: Verification{Name: "approved", Commands: []Command{{Argv: []string{"go", "test", "./..."}, Directory: ".", TimeoutMS: 1000}}}, ContextScope: []string{"."}, RunnerRoots: []RunnerRoot{{RunnerID: "00000000-0000-4000-8000-000000000001", Root: root}}}
	return v, operator
}
func selection(t *testing.T, v Profile) Selection {
	t.Helper()
	digest, err := v.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return Selection{Repository: v.ID, Revision: v.Revision, ProfileDigest: digest, Remote: v.Remote, BaseCommit: v.Base.Commit, RunnerRoot: v.RunnerRoots[0]}
}
func cloneProfile(v Profile) Profile {
	v.ProtectedPaths = append([]string{}, v.ProtectedPaths...)
	v.ContextScope = append([]string{}, v.ContextScope...)
	v.RunnerRoots = append([]RunnerRoot{}, v.RunnerRoots...)
	v.Verification.Commands = append([]Command{}, v.Verification.Commands...)
	return v
}
func TestAccessCheckoutAndDirtyPreservation(t *testing.T) {
	v, operator := profileFixture(t)
	git(t, operator, "switch", "-c", "user-branch")
	put(t, filepath.Join(operator, "main.txt"), "staged change\n", 0600)
	git(t, operator, "add", "main.txt")
	put(t, filepath.Join(operator, "main.txt"), "unstaged change\n", 0600)
	put(t, filepath.Join(operator, "untracked"), "keep me\n", 0600)
	before := map[string]string{}
	for _, args := range [][]string{{"status", "--porcelain=v1", "--untracked-files=all"}, {"show-ref"}, {"symbolic-ref", "HEAD"}, {"diff", "--binary"}, {"diff", "--cached", "--binary"}} {
		before[strings.Join(args, " ")] = git(t, operator, args...)
	}
	validation, err := ValidateAccess(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := Prepare(context.Background(), v, selection(t, v))
	if err != nil {
		t.Fatal(err)
	}
	if validation != checkout.Validation || checkout.BaseCommit != v.Base.Commit || checkout.Path == operator || filepath.Dir(checkout.Path) != v.RunnerRoots[0].Root {
		t.Fatal(checkout)
	}
	if git(t, checkout.Path, "rev-parse", "HEAD") != v.Base.Commit || git(t, checkout.Path, "remote") != "" || git(t, checkout.Path, "status", "--porcelain") != "" {
		t.Fatal("checkout mismatch")
	}
	if _, err := os.Stat(filepath.Join(checkout.Path, ".git", "objects", "info", "alternates")); !os.IsNotExist(err) {
		t.Fatal("shared objects", err)
	}
	if body, err := os.ReadFile(filepath.Join(checkout.Path, "main.txt")); err != nil || string(body) != "base\n" {
		t.Fatal("copied dirty content")
	}
	for key, want := range before {
		if got := git(t, operator, strings.Split(key, " ")...); got != want {
			t.Fatalf("operator mutated: %s", key)
		}
	}
	if body, err := os.ReadFile(filepath.Join(operator, "untracked")); err != nil || string(body) != "keep me\n" {
		t.Fatal("untracked file lost")
	}
}

func TestInaccessibleRemoteRefDriftAndRootRefusal(t *testing.T) {
	v, operator := profileFixture(t)
	ctx := context.Background()
	missing := cloneProfile(v)
	missing.Remote += "-missing"
	if _, err := ValidateAccess(ctx, missing); err == nil {
		t.Fatal("inaccessible remote accepted")
	}
	missing = cloneProfile(v)
	missing.Base.Ref = "refs/heads/missing"
	if _, err := ValidateAccess(ctx, missing); err == nil {
		t.Fatal("missing ref accepted")
	}
	wrong := selection(t, v)
	wrong.RunnerRoot.Root = filepath.Dir(wrong.RunnerRoot.Root)
	if _, err := Prepare(ctx, v, wrong); !errors.Is(err, Denied) {
		t.Fatal(err)
	}
	wrong = selection(t, v)
	wrong.Remote += "-other"
	if _, err := Prepare(ctx, v, wrong); !errors.Is(err, Denied) {
		t.Fatal("relabel", err)
	}
	wrong = selection(t, v)
	wrong.Repository = "other"
	if _, err := Prepare(ctx, v, wrong); !errors.Is(err, Denied) {
		t.Fatal("label", err)
	}
	inCheckout := cloneProfile(v)
	inCheckout.RunnerRoots[0].Root = operator
	if _, err := Prepare(ctx, inCheckout, selection(t, inCheckout)); !errors.Is(err, Denied) {
		t.Fatal("operator root", err)
	}
	link := filepath.Join(filepath.Dir(operator), "alias")
	if err := os.Symlink(v.RunnerRoots[0].Root, link); err != nil {
		t.Fatal(err)
	}
	inCheckout.RunnerRoots[0].Root = link
	if _, err := Prepare(ctx, inCheckout, selection(t, inCheckout)); !errors.Is(err, Denied) {
		t.Fatal("symlink root", err)
	}
	inCheckout.RunnerRoots[0].Root = filepath.Join(filepath.Dir(operator), "remote.git")
	if err := os.Chmod(inCheckout.RunnerRoots[0].Root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(ctx, inCheckout, selection(t, inCheckout)); !errors.Is(err, Denied) {
		t.Fatal("bare administration root", err)
	}
	if err := os.Chmod(v.RunnerRoots[0].Root, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(ctx, v, selection(t, v)); !errors.Is(err, Denied) {
		t.Fatal("nonprivate root", err)
	}
	if err := os.Chmod(v.RunnerRoots[0].Root, 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(operator, "main.txt"), "advanced\n", 0600)
	git(t, operator, "add", ".")
	git(t, operator, "commit", "--quiet", "-m", "advance")
	git(t, operator, "push", "--quiet", v.Remote, "HEAD:refs/heads/main")
	if _, err := ValidateAccess(ctx, v); !errors.Is(err, RemoteChanged) {
		t.Fatal("ref drift", err)
	}
	if _, err := Prepare(ctx, v, selection(t, v)); !errors.Is(err, RemoteChanged) {
		t.Fatal("checkout ref drift", err)
	}
	entries, err := os.ReadDir(v.RunnerRoots[0].Root)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed checkout leaked", err, entries)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ValidateAccess(cancelled, v); err == nil {
		t.Fatal("cancelled access accepted")
	}
}

func TestMaliciousHooksFiltersAndInheritedCredentialsDoNotRun(t *testing.T) {
	v, operator := profileFixture(t)
	base := filepath.Dir(operator)
	marker := filepath.Join(base, "executed")
	hooks := filepath.Join(base, "hooks")
	payload := "#!/bin/sh\nprintf executed > '" + marker + "'\n"
	for _, name := range []string{"post-checkout", "reference-transaction", "post-merge"} {
		put(t, filepath.Join(hooks, name), payload, 0700)
	}
	put(t, filepath.Join(operator, ".git", "hooks", "post-checkout"), payload, 0700)
	put(t, filepath.Join(base, "remote.git", "hooks", "post-checkout"), payload, 0700)
	put(t, filepath.Join(operator, ".gitattributes"), "main.txt filter=hostile\n", 0600)
	put(t, filepath.Join(operator, ".gitmodules"), "[submodule \"hostile\"]\n path = nested\n url = ext::sh -c touch-malicious\n", 0600)
	git(t, operator, "add", ".gitattributes", ".gitmodules")
	git(t, operator, "commit", "--quiet", "-m", "attributes")
	git(t, operator, "push", "--quiet", v.Remote, "HEAD:refs/heads/main")
	v.Base.Commit = git(t, operator, "rev-parse", "HEAD")
	git(t, filepath.Join(base, "remote.git"), "config", "uploadpack.packObjectsHook", "echo pack > '"+marker+"'")
	home := filepath.Join(base, "host-home")
	put(t, filepath.Join(home, ".gitconfig"), "[core]\n hooksPath = "+hooks+"\n[filter \"hostile\"]\n smudge = \"echo filter > '"+marker+"'\"\n required = true\n[credential]\n helper = \"!echo credential > '"+marker+"'\"\n[url \"file:///inaccessible/\"]\n insteadOf = file:///\n", 0600)
	askpass := filepath.Join(base, "askpass")
	put(t, askpass, payload, 0700)
	for key, val := range map[string]string{"HOME": home, "GIT_CONFIG_GLOBAL": filepath.Join(home, ".gitconfig"), "GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.hooksPath", "GIT_CONFIG_VALUE_0": hooks, "GIT_TEMPLATE_DIR": filepath.Dir(hooks), "GIT_ASKPASS": askpass, "SSH_ASKPASS": askpass, "SSH_AUTH_SOCK": filepath.Join(base, "secret-agent"), "GIT_DIR": filepath.Join(operator, ".git"), "GIT_WORK_TREE": operator} {
		t.Setenv(key, val)
	}
	if _, err := ValidateAccess(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	checkout, err := Prepare(context.Background(), v, selection(t, v))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("executed unmanaged code", err)
	}
	// Read generated checkout config as a persisted credential/remote contract.
	config, err := os.ReadFile(filepath.Join(checkout.Path, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{home, marker, "secret-agent", v.Remote, "hostile"} {
		if strings.Contains(string(config), forbidden) {
			t.Fatal("job retained trusted configuration")
		}
	}
}

func TestProfileValidationProtectedChangesAndDigest(t *testing.T) {
	v, operator := profileFixture(t)
	before, err := v.Digest()
	if err != nil {
		t.Fatal(err)
	}
	public := cloneProfile(v)
	public.Remote = "https://example.com/team/repository.git"
	if err := public.Validate(); err != nil {
		t.Fatal("canonical HTTPS profile", err)
	}
	put(t, filepath.Join(operator, "policy", "checks"), "silently weakened\n", 0600)
	paths := strings.Split(git(t, operator, "diff", "--name-only"), "\n")
	if err := v.CheckChanges(paths); !errors.Is(err, ProtectedPath) {
		t.Fatal("protected edit", err)
	}
	git(t, operator, "mv", "policy/checks", "moved")
	paths = strings.Split(git(t, operator, "diff", "HEAD", "--no-renames", "--name-only"), "\n")
	if err := v.CheckChanges(paths); !errors.Is(err, ProtectedPath) {
		t.Fatal("protected rename", err)
	}
	if err := v.CheckChanges([]string{"policy-copy", "main.txt"}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Profile){
		func(v *Profile) { v.Remote = "https://user:secret@example.com/repo.git" },
		func(v *Profile) { v.Remote = "https://example.com/org/../repo.git" },
		func(v *Profile) { v.Remote = "https://example.com/org/repo.git?secret=x" },
		func(v *Profile) { v.Remote = "ext::sh -c evil" },
		func(v *Profile) { v.Remote = "git@example.com:repo.git" },
		func(v *Profile) { v.Base.Ref = "refs/heads/-bad.lock" },
		func(v *Profile) { v.Base.Ref = "refs/heads/main:refs/heads/other" },
		func(v *Profile) { v.Base.Policy = "follow" },
		func(v *Profile) { v.ContextScope = []string{"../secret"} },
		func(v *Profile) { v.ProtectedPaths = nil },
		func(v *Profile) { v.ProtectedPaths = []string{".git/config"} },
		func(v *Profile) { v.RunnerRoots[0].Root = "/" },
		func(v *Profile) { v.Verification.Commands[0].TimeoutMS = 0 },
	} {
		n := cloneProfile(v)
		mutate(&n)
		if err := n.Validate(); !errors.Is(err, Invalid) {
			t.Fatal("invalid accepted", n, err)
		}
	}
	changed := cloneProfile(v)
	changed.ProtectedPaths = []string{}
	after, err := changed.Digest()
	if err != nil || before == after {
		t.Fatal("protection not identity-bound")
	}
	if err := changed.Select(selection(t, v)); !errors.Is(err, Denied) {
		t.Fatal("changed policy reused", err)
	}
	if !reflect.DeepEqual(v.ProtectedPaths, []string{"policy"}) {
		t.Fatal("fixture mutated")
	}
}
