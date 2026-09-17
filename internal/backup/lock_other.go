//go:build !darwin && !linux

package backup

import (
	"errors"
	"os"
)

func lockTarget(*os.Root) (func(), error) {
	return nil, errors.New("backup filesystem ownership unsupported on this platform")
}
