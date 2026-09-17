package runnerjournal

import "golang.org/x/sys/unix"

func renameNew(from, to string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_EXCL)
}

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
