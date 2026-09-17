package runnerjournal

import "golang.org/x/sys/unix"

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
