//go:build linux || darwin

package store

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func privateFile(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Geteuid()) && st.Nlink == 1
}

func openDirectory(dir string) (*os.File, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), dir)
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err == nil && (stat.Uid != uint32(os.Geteuid()) || stat.Mode&0777 != 0700) {
		err = fmt.Errorf("store directory must be owned and mode 0700")
	}
	if err == nil {
		err = localFilesystem(fd)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("store ownership: %w", err)
	}
	return f, nil
}

func lockDirectory(dir string) (*os.File, error) {
	f, err := openDirectory(dir)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("store ownership: %w", err)
	}
	// Lock the directory inode, not a replaceable/deletable lock-file pathname.
	return f, nil
}
