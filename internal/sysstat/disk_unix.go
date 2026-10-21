//go:build unix

package sysstat

import "golang.org/x/sys/unix"

func statfs(path string) (total, free uint64, ok bool) {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return 0, 0, false
	}
	bs := uint64(st.Bsize)
	return st.Blocks * bs, st.Bavail * bs, true
}
