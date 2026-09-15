//go:build !linux && !darwin

package store

import (
	"fmt"
	"os"
)

func privateFile(os.FileInfo) bool { return false }

func lockDirectory(string) (*os.File, error) {
	return nil, fmt.Errorf("unsupported OS: store requires Linux local disk or macOS APFS")
}
