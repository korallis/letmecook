package runnerjournal

import "golang.org/x/sys/unix"

func renameNew(from, to string) error {
	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
}

func localFilesystem(fd int) error {
	var s unix.Statfs_t
	if err := unix.Fstatfs(fd, &s); err != nil {
		return err
	}
	switch s.Type {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC:
		return nil
	}
	return ErrUnavailable
}
