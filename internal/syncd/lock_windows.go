//go:build windows

package syncd

import "os"

// AcquireLock is a no-op stub on windows (no flock syscall); the
// single-user daemon assumption of card #880 holds there without it.
func AcquireLock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}
