//go:build windows

package sync

import (
	"fmt"
	"os"
)

// acquireFlock on Windows is a best-effort no-op. The implementation
// doc explicitly accepts this for v1 — switching to LockFileEx via
// golang.org/x/sys/windows is a follow-up. Until then, concurrent
// `bacio sync` runs on Windows are not protected at the file-lock
// level; sqlite still serialises actual DB writes.
func acquireFlock(_ *os.File) error {
	fmt.Fprintln(os.Stderr, "bacio: warning: process lock not implemented on Windows; concurrent bacio sync runs are not prevented")
	return nil
}

// releaseFlock is the Windows no-op counterpart to acquireFlock.
func releaseFlock(_ *os.File) error { return nil }
