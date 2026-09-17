package runnerjournal

import "golang.org/x/sys/unix"

func localFilesystem(fd int) error {
	var s unix.Statfs_t
	if err := unix.Fstatfs(fd, &s); err != nil {
		return err
	}
	if unix.ByteSliceToString(s.Fstypename[:]) != "apfs" || s.Flags&unix.MNT_LOCAL == 0 {
		return ErrUnavailable
	}
	return nil
}
