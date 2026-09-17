//go:build !linux && !darwin

package runnerjournal

import "os"

func ownedDirectory(os.FileInfo) bool             { return false }
func ownedFile(os.FileInfo) bool                  { return false }
func openOwnedDirectory(string) (*os.File, error) { return nil, ErrUnavailable }
