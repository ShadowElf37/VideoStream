//go:build unix && !darwin

package timeline

import "golang.org/x/sys/unix"

const fionread = unix.FIONREAD
