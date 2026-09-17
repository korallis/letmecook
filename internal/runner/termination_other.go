//go:build !linux && !darwin

package runner

import "time"

func terminateGroup(int, time.Duration, time.Time) (bool, error) {
	return false, ErrTerminationUnconfirmed
}
