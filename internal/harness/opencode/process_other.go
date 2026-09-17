//go:build !darwin && !linux

package opencode

import (
	"context"
	"github.com/korallis/letmecook/internal/harness"
	"os/exec"
	"time"
)

func processGroup(int) int { return 0 }
func terminateGroup(context.Context, int, time.Duration, time.Duration) harness.CancelResult {
	return harness.CancelResult{ConfirmedProcess: "unknown", RemoteWork: "unknown"}
}

func isGuardian(cmd *exec.Cmd) bool { return true }

func groupGone(int) bool { return false }
