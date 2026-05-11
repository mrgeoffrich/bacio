//go:build !windows

package sync

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// acquireFlock takes an exclusive flock with a poll-based timeout.
// `unix.Flock` itself doesn't accept a deadline (it's either blocking
// LOCK_EX or non-blocking LOCK_EX|LOCK_NB), so the timeout is
// implemented as repeated non-blocking attempts spaced ~50 ms apart.
// Net effect at the limit: ~600 attempts over the full 30 s, each a
// cheap syscall.
func acquireFlock(f *os.File) error {
	deadline := time.Now().Add(time.Duration(LockTimeoutSeconds) * time.Second)
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return fmt.Errorf("flock: %w", err)
		}
		if time.Now().After(deadline) {
			return ErrLockTimeout
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// releaseFlock drops the held flock. Errors are non-fatal — fd close
// will release the lock as a backstop. Returning the error anyway so
// callers can log it if they want.
func releaseFlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
