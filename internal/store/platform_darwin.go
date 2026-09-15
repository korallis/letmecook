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
	if unix.ByteSliceToString(st.Fstypename[:]) != "apfs" || st.Flags&unix.MNT_LOCAL == 0 {
		return fmt.Errorf("unsupported filesystem")
	}
	return nil
}
