//go:build !unix

package media

// freeBytes is a best-effort convenience for the library view; on platforms
// without a statfs binding it simply declines to answer rather than blocking
// the build.
func freeBytes(string) (uint64, error) { return 0, nil }
