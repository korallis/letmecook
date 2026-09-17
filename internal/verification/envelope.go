package verification

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
)

// CheckEnvelope compares all regular candidate files to the pinned Git tree.
// It includes staged, unstaged, ignored/untracked and deleted paths, rather than
// trusting git status or a worker's declared edit list. Symlinks are refused.
func CheckEnvelope(root, base string, e g.Envelope) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := checkEnvelope(ctx, root, root, base, e)
	return err
}
func checkEnvelope(ctx context.Context, root, repo, base string, e g.Envelope) ([]string, error) {
	refuse := func() ([]string, error) { return nil, g.Deny("envelope_violation", "paths/operations") }
	if err := e.Validate(); err != nil {
		return nil, err
	}
	if base != e.BaseCommit || !filepath.IsAbs(root) || !filepath.IsAbs(repo) {
		return refuse()
	}
	tree, err := safeGit(ctx, repo, "ls-tree", "-r", "-z", base)
	if err != nil {
		return nil, err
	}
	objects := []baseObject{}
	for _, line := range bytes.Split(tree, []byte{0}) {
		if len(line) == 0 {
			continue
		}
		parts := bytes.SplitN(line, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return refuse()
		}
		fields := bytes.Fields(parts[0])
		path := string(parts[1])
		if len(fields) != 3 || string(fields[1]) != "blob" || !safePath(path) || (string(fields[0]) != "100644" && string(fields[0]) != "100755") {
			return refuse()
		}
		oid := string(fields[2])
		decoded, err := hex.DecodeString(oid)
		if err != nil || (len(decoded) != 20 && len(decoded) != 32) || strings.ToLower(oid) != oid || len(objects) >= 10000 {
			return refuse()
		}
		objects = append(objects, baseObject{path, oid})
	}
	originals, err := baseBlobs(ctx, repo, objects)
	if err != nil {
		return nil, err
	}
	changed := []string{}
	seen := map[string]bool{}
	count := 0
	var total int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !safePath(rel) {
			return fmt.Errorf("unsafe candidate path")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("nonregular candidate")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > 64<<20 {
			return fmt.Errorf("candidate file bounds")
		}
		total += info.Size()
		if total > 256<<20 {
			return fmt.Errorf("candidate byte bounds")
		}
		count++
		if count > 10000 {
			return fmt.Errorf("candidate count bounds")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		seen[rel] = true
		old, tracked := originals[rel]
		if !tracked || !bytes.Equal(old, body) {
			changed = append(changed, rel)
		}
		return nil
	})
	if err != nil {
		return refuse()
	}
	for path := range originals {
		if !seen[path] {
			changed = append(changed, path)
		}
	}
	slices.Sort(changed)
	for _, path := range changed {
		if !slices.Contains(e.Paths, path) || !slices.Contains(e.Operations, "write") {
			return refuse()
		}
	}
	return changed, nil
}

// Read every base blob through one bounded plumbing process, not N processes.
type baseObject struct{ path, oid string }

func baseBlobs(ctx context.Context, repo string, objects []baseObject) (map[string][]byte, error) {
	out := map[string][]byte{}
	if len(objects) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "diff.external=", "-c", "filter.lfs.process=", "--no-optional-locks", "cat-file", "--batch")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_ATTR_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1"}
	var input strings.Builder
	for _, object := range objects {
		input.WriteString(object.oid)
		input.WriteByte('\n')
	}
	cmd.Stdin = strings.NewReader(input.String())
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	waited := false
	defer func() {
		cancel()
		if !waited {
			_ = cmd.Wait()
		}
	}()
	reader := bufio.NewReader(pipe)
	var total int64
	for _, object := range objects {
		line, err := reader.ReadSlice('\n')
		if err != nil {
			return nil, fmt.Errorf("base blob header: %w", err)
		}
		fields := strings.Fields(string(line))
		if len(fields) != 3 || fields[0] != object.oid || fields[1] != "blob" {
			return nil, fmt.Errorf("base blob identity/type")
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 || size > 1<<20 || total+size > 256<<20 {
			return nil, fmt.Errorf("base blob bounds")
		}
		total += size
		body := make([]byte, int(size))
		if _, err = io.ReadFull(reader, body); err != nil {
			return nil, fmt.Errorf("base blob truncated: %w", err)
		}
		if delimiter, err := reader.ReadByte(); err != nil || delimiter != '\n' {
			return nil, fmt.Errorf("base blob delimiter")
		}
		out[object.path] = body
	}
	if _, err = reader.ReadByte(); err != io.EOF {
		return nil, fmt.Errorf("base blob trailing output")
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		return nil, fmt.Errorf("safe git batch: %w", err)
	}
	return out, nil
}
