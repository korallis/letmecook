package verification

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	originals := map[string][]byte{}
	var total int64
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
		body, err := safeGit(ctx, repo, "cat-file", "blob", string(fields[2]))
		if err != nil {
			return nil, err
		}
		total += int64(len(body))
		if total > 256<<20 || len(originals) >= 10000 {
			return refuse()
		}
		originals[path] = body
	}
	changed := []string{}
	seen := map[string]bool{}
	count := 0
	total = 0
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
