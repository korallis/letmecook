//go:build !linux && !darwin

package repositories

import "os/exec"

// ponytail: trusted checkout supports only current store host platforms; add
// another OS only with owned-process-tree cancellation and checkout evidence.
func containCommand(*exec.Cmd) error { return Unavailable }
