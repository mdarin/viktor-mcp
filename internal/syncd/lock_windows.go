//go:build windows

package syncd

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// AcquireLock takes an exclusive non-blocking lock on path — the
// Windows equivalent of flock(LOCK_EX|LOCK_NB) (card #880, card #939):
// a byte-range LockFileEx over the whole file with FAIL_IMMEDIATELY.
// The caller keeps the returned file open for the daemon's lifetime
// and closes it on shutdown to release the lock.
func AcquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("syncd lock: %w", err)
	}
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &windows.Overlapped{},
	)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("syncd: another instance holds the lock (%s)", path)
	}
	return f, nil
}
