//go:build !windows

package syncd

import (
	"fmt"
	"os"
	"syscall"
)

// AcquireLock takes an exclusive non-blocking flock on path — the
// skip-on-stale-lock guard of card #880: two parallel syncd processes
// must not interleave their ticks (Ollama/Qdrant embedding is the
// expensive part). The caller keeps the returned file open for the
// daemon's lifetime and closes it on shutdown to release the lock.
func AcquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("syncd lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("syncd: another instance holds the lock (%s)", path)
	}
	return f, nil
}
