package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// prepareDirectory creates only the final component under an existing parent.
// Resolve parent aliases (including macOS /tmp); refuse symlink store roots.
func prepareDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(os.PathSeparator) {
		return "", fmt.Errorf("store path must be absolute, clean and non-root")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	path = filepath.Join(parent, filepath.Base(path))
	if err = os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	f, err := os.Open(parent)
	if err != nil {
		return "", err
	}
	return path, errors.Join(f.Sync(), f.Close())
}
