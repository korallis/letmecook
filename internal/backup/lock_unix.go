//go:build darwin || linux

package backup

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockTarget(root *os.Root) (func(), error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}
