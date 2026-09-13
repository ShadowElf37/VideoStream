//go:build darwin

package timeline

// FIONREAD is not exported by x/sys/unix on darwin; value from <sys/filio.h>.
const fionread = 0x4004667f
