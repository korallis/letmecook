//go:build linux || darwin

package runnerjournal

import (
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func ownedDirectory(s os.FileInfo) bool {
	v, ok := s.Sys().(*syscall.Stat_t)
	return ok && s.IsDir() && s.Mode().Perm() == 0700 && v.Uid == uint32(os.Geteuid())
}
func ownedFile(s os.FileInfo) bool {
	v, ok := s.Sys().(*syscall.Stat_t)
	return ok && s.Mode().IsRegular() && s.Mode().Perm() == 0600 && v.Uid == uint32(os.Geteuid()) && v.Nlink == 1
}
func openOwnedDirectory(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	s, err := f.Stat()
	if err == nil && !ownedDirectory(s) {
		err = ErrUnavailable
	}
	if err == nil {
		err = localFilesystem(fd)
	}
	if err == nil {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
