//go:build linux || darwin

package store

import (
	"golang.org/x/sys/unix"
	"testing"
)

func TestUnsupportedFilesystem(t *testing.T) {
	// Real devfs/tmpfs, not a mocked filesystem type; no device reads/writes.
	fd, err := unix.Open("/dev", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err = localFilesystem(fd); err == nil {
		t.Fatal("non-durable filesystem accepted")
	}
}
