package store

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func localFilesystem(fd int) error {
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		return err
	}
	// ponytail: conservative local-disk allowlist; qualify more filesystems with
	// real sync/lock/crash evidence before adding them. No NFS/SMB/FUSE/overlay.
	switch st.Type {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC:
		return nil
	}
	return fmt.Errorf("unsupported filesystem: %x", st.Type)
}
