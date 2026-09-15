//go:build !linux && !darwin

package store

import (
	"fmt"
	"os"
)

func lockDirectory(string) (*os.File, error) {
	return nil, fmt.Errorf("unsupported OS: fixture store requires Linux local disk or macOS APFS")
}
