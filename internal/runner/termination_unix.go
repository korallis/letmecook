//go:build linux || darwin

package runner

import (
	"errors"
	"syscall"
	"time"
)

func terminateGroup(group int, grace time.Duration, deadline time.Time) (bool, error) {
	return terminateGroupWithSignals(group, grace, deadline, syscall.Getpgid, syscall.Kill)
}

// Instance-local signal seam lets tests exercise EPERM without requiring another UID.
func terminateGroupWithSignals(group int, grace time.Duration, deadline time.Time, getpgid func(int) (int, error), kill func(int, syscall.Signal) error) (bool, error) {
	own, err := getpgid(0)
	if err != nil || group <= 1 || group == own {
		return false, ErrTerminationUnconfirmed
	}
	actual, err := getpgid(group)
	// Missing leader is not evidence that its descendants ever belonged to the
	// supplied group. No arbitrary negative-PID signal without this local check.
	if err != nil || actual != group {
		return false, ErrTerminationUnconfirmed
	}
	if err := kill(-group, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
		return false, err
	}
	escalateAt := time.Now().Add(grace)
	escalated := false
	for {
		err := kill(-group, 0)
		if errors.Is(err, syscall.ESRCH) {
			return escalated, nil
		}
		// XNU reports EPERM, not ESRCH, while a process group still exists but
		// has no signalable member (for example only unreaped zombies remain).
		// EPERM is also what an unsignalable foreign member produces, so it is
		// never positive evidence: keep observing until ESRCH or the deadline.
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return escalated, err
		}
		now := time.Now()
		if !escalated && (!now.Before(escalateAt) || !now.Before(deadline)) {
			if err := kill(-group, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
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
