//go:build darwin || linux

package opencode

import (
	"context"
	"os/exec"
	"syscall"
	"time"

	"github.com/korallis/letmecook/internal/harness"
)

func processGroup(pid int) int {
	group, err := syscall.Getpgid(pid)
	if err != nil {
		return 0
	}
	return group
}
func groupGone(pgid int) bool { return pgid > 1 && syscall.Kill(-pgid, 0) == syscall.ESRCH }
func terminateGroup(ctx context.Context, pgid int, grace, killWait time.Duration) harness.CancelResult {
	result := harness.CancelResult{ConfirmedProcess: "unknown", RemoteWork: "unknown"}
	if pgid <= 1 || pgid == syscall.Getpgrp() {
		return result
	}
	observed := func() bool {
		if groupGone(pgid) {
			result.ConfirmedProcess = "terminated"
			result.ObservedUnixNS = time.Now().UnixNano()
			return true
		}
		return false
	}
	if observed() {
		return result
	}
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return result
	}
	if waitGone(ctx, pgid, grace) {
		observed()
		return result
	}
	if ctx.Err() != nil {
		return result
	}
	result.Escalated = true
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return result
	}
	if waitGone(ctx, pgid, killWait) {
		observed()
	}
	return result
}
func waitGone(ctx context.Context, pgid int, wait time.Duration) bool {
	if groupGone(pgid) {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return groupGone(pgid)
		case <-tick.C:
			if groupGone(pgid) {
				return true
			}
		}
	}
}

func isGuardian(cmd *exec.Cmd) bool { return cmd.SysProcAttr != nil && cmd.SysProcAttr.Setsid }
