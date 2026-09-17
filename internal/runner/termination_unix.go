//go:build linux || darwin

package runner

import (
	"errors"
	"syscall"
	"time"
)

func terminateGroup(group int, grace time.Duration, deadline time.Time) (bool, error) {
	own, err := syscall.Getpgid(0)
	if err != nil || group <= 1 || group == own {
		return false, ErrTerminationUnconfirmed
	}
	actual, err := syscall.Getpgid(group)
	// Missing leader is not evidence that its descendants ever belonged to the
	// supplied group. No arbitrary negative-PID signal without this local check.
	if err != nil || actual != group {
		return false, ErrTerminationUnconfirmed
	}
	if err := syscall.Kill(-group, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return false, err
	}
	escalateAt := time.Now().Add(grace)
	escalated := false
	for {
		err := syscall.Kill(-group, 0)
		if errors.Is(err, syscall.ESRCH) {
			return escalated, nil
		}
		if err != nil {
			return escalated, err
		}
		now := time.Now()
		if !escalated && (!now.Before(escalateAt) || !now.Before(deadline)) {
			if err := syscall.Kill(-group, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				return escalated, err
			}
			escalated = true
		}
		if !now.Before(deadline) {
			return escalated, ErrTerminationUnconfirmed
		}
		time.Sleep(min(5*time.Millisecond, time.Until(deadline)))
	}
}
