package repositories

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Checkout is an input-preparation result, not a job or dispatch token. The caller
// owns its dedicated clone after success; failure removes only newly owned paths.
type Checkout struct {
	Path string `json:"path"`
	Validation
}

type gitClient struct{ binary, home string }

// cappedOutput bounds untrusted Git stdout without reflecting stderr/URLs/secrets.
type cappedOutput struct{ bytes.Buffer }

func (b *cappedOutput) Write(p []byte) (int, error) {
	if len(p) > 8192-b.Len() {
		return 0, Unavailable
	}
	return b.Buffer.Write(p)
}
func (g gitClient) run(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	fixed := []string{"-c", "core.hooksPath=/dev/null", "-c", "core.attributesFile=/dev/null", "-c", "credential.helper=", "-c", "http.followRedirects=false", "-c", "http.proxy=", "-c", "http.sslVerify=true", "-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "protocol.file.allow=always", "-c", "submodule.recurse=false", "-c", "gc.auto=0", "-c", "maintenance.auto=false"}
	cmd := exec.CommandContext(ctx, g.binary, append(fixed, args...)...)
	cmd.Dir = dir
	if err := containCommand(cmd); err != nil {
		return "", err
	}
	// Do not inherit HOME, SSH_AUTH_SOCK, askpass, Git config, proxies, tokens,
	// object directories, templates, alternates or a caller-selected Git dir.
	cmd.Env = []string{"PATH=" + filepath.Dir(g.binary) + ":/usr/bin:/bin", "HOME=" + g.home, "XDG_CONFIG_HOME=" + g.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_ATTR_NOSYSTEM=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_ALLOW_PROTOCOL=https:file", "LC_ALL=C"}
	var out cappedOutput
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return "", Unavailable
	}
	return out.String(), nil
}
func newGit(home string) (gitClient, error) {
	binary, err := exec.LookPath("git")
	if err != nil || !filepath.IsAbs(binary) {
		return gitClient{}, Unavailable
	}
	return gitClient{binary: binary, home: home}, nil
}

func canonicalDirectory(path string) error {
	if !validRoot(path) {
		return Invalid
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return Denied
	}
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() {
		return Denied
	}
	return nil
}
func checkRemotePath(remote string) error {
	u, _ := url.Parse(remote) // Profile.Validate already ran.
	if u.Scheme == "file" {
		return canonicalDirectory(u.Path)
	}
	return nil
}
func (g gitClient) checkRef(ctx context.Context, dir string, v Profile) error {
	out, err := g.run(ctx, dir, "ls-remote", "--refs", "--exit-code", "--", v.Remote, v.Base.Ref)
	if err != nil {
		return err
	}
	if out != v.Base.Commit+"\t"+v.Base.Ref+"\n" {
		return RemoteChanged
	}
	return nil
}

// prepare always constructs independent Git administration. It never uses the
// operator checkout as a workspace or imports its config, hooks or credentials.
// An explicitly selected file remote is read by Git's upload-pack transport.
func prepare(ctx context.Context, v Profile, parent string) (_ Checkout, err error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	digest, err := v.Digest()
	if err != nil {
		return Checkout{}, err
	}
	if err := checkRemotePath(v.Remote); err != nil {
		return Checkout{}, err
	}
	work, err := os.MkdirTemp(parent, "gaffer-checkout-")
	if err != nil {
		return Checkout{}, Unavailable
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(work))
		}
	}()
	// Isolated empty HOME is a sibling, never copied into the job checkout.
	home, err := os.MkdirTemp(parent, "gaffer-git-home-")
	if err != nil {
		return Checkout{}, Unavailable
	}
	defer func() { err = errors.Join(err, os.RemoveAll(home)) }()
	g, err := newGit(home)
	if err != nil {
		return Checkout{}, err
	}
	if err = g.checkRef(ctx, home, v); err != nil {
		return Checkout{}, err
	}
	format := "sha1"
	if len(v.Base.Commit) == 64 {
		format = "sha256"
	}
	if _, err = g.run(ctx, home, "init", "--quiet", "--template=", "--object-format="+format, "--", work); err != nil {
		return Checkout{}, err
	}
	// Persist the non-executing hook/attribute defaults; no remote or credentials
	// are persisted. Workers eventually need OS/network containment, not Git config.
	for _, setting := range [][2]string{{"core.hooksPath", "/dev/null"}, {"core.attributesFile", "/dev/null"}, {"protocol.allow", "never"}, {"credential.helper", ""}} {
		if _, err = g.run(ctx, work, "config", "--local", setting[0], setting[1]); err != nil {
			return Checkout{}, err
		}
	}
	if _, err = g.run(ctx, work, "fetch", "--quiet", "--depth=1", "--no-tags", "--no-recurse-submodules", "--no-write-fetch-head", "--", v.Remote, v.Base.Ref+":refs/gaffer/base"); err != nil {
		return Checkout{}, err
	}
	actual, err := g.run(ctx, work, "rev-parse", "--verify", "refs/gaffer/base^{commit}")
	if err != nil {
		return Checkout{}, err
	}
	if strings.TrimSuffix(actual, "\n") != v.Base.Commit {
		return Checkout{}, RemoteChanged
	}
	if err = g.checkRef(ctx, home, v); err != nil {
		return Checkout{}, err
	}
	if _, err = g.run(ctx, work, "checkout", "--quiet", "--detach", v.Base.Commit, "--"); err != nil {
		return Checkout{}, err
	}
	return Checkout{Path: work, Validation: Validation{ProfileDigest: digest, BaseCommit: v.Base.Commit}}, nil
}

// ValidateAccess proves an anonymous/scoped read of the explicitly selected
// remote/ref and commit before registration, in a disposable trusted checkout.
// It runs no repository-supplied commands. It does not prove forge write denial.
func ValidateAccess(ctx context.Context, v Profile) (Validation, error) {
	checkout, err := prepare(ctx, v, "/tmp")
	if err != nil {
		return Validation{}, err
	}
	if err := os.RemoveAll(checkout.Path); err != nil {
		return Validation{}, Unavailable
	}
	return checkout.Validation, nil
}

// Prepare validates the exact profile selection and canonical local root, then
// creates a dedicated execution clone under that root. Root must be provisioned
// by the host operator, private (0700), and outside operator checkouts. No roots
// are discovered or created. This is not runner-local policy or execution approval.
func Prepare(ctx context.Context, v Profile, selection Selection) (Checkout, error) {
	if err := v.Select(selection); err != nil {
		return Checkout{}, err
	}
	root := selection.RunnerRoot.Root
	if err := canonicalDirectory(root); err != nil {
		return Checkout{}, err
	}
	st, err := os.Stat(root)
	if err != nil || st.Mode().Perm() != 0700 {
		return Checkout{}, Denied
	}
	// Refuse roots inside a worktree, even if the caller explicitly selected one.
	for path := root; ; path = filepath.Dir(path) {
		if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil || !os.IsNotExist(err) {
			return Checkout{}, Denied
		}
		// A bare repository has no .git entry; refuse its administration too.
		head, headErr := os.Lstat(filepath.Join(path, "HEAD"))
		_, objectsErr := os.Lstat(filepath.Join(path, "objects"))
		if headErr == nil && head.Mode().IsRegular() && objectsErr == nil {
			return Checkout{}, Denied
		}
		if path == filepath.Dir(path) {
			break
		}
	}
	return prepare(ctx, v, root)
}
