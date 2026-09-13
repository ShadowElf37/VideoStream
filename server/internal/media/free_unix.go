//go:build unix

package media

import "golang.org/x/sys/unix"

func freeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	// Bavail, not Bfree: the space a non-root process can actually use.
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
