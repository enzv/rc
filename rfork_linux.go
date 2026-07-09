//go:build linux && !android

package main

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

func sysValidateRfork(flags rforkFlags) error {
	if flags.nameClean || flags.noMount {
		return fmt.Errorf("%w: unsupported flag N or m", errRforkNotSupported)
	}
	return nil
}

func sysApplyRfork(flags rforkFlags) error {

	if flags.nameNew {
		// Mount namespaces are thread-affine. Keep the calling goroutine on this
		// OS thread so later shell work stays in the same namespace. On success,
		// the runner stays pinned to this thread for the rest of its lifetime.
		runtime.LockOSThread()
		if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
			runtime.UnlockOSThread()
			return fmt.Errorf("unshare NEWNS: %w", err)
		}
	}

	if flags.noteNew {
		if err := unix.Setpgid(0, 0); err != nil {
			return fmt.Errorf("setpgid: %w", err)
		}
	}

	return nil
}
