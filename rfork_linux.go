//go:build linux

package main

import (
	"fmt"
	"syscall"
)

func sysValidateRfork(flags rforkFlags) error {
	if flags.nameClean || flags.noMount {
		return fmt.Errorf("%w: unsupported flag N or m", errRforkNotSupported)
	}
	return nil
}

func sysApplyRfork(flags rforkFlags) error {

	if flags.nameNew {
		if err := syscall.Unshare(syscall.CLONE_NEWNS); err != nil {
			return fmt.Errorf("unshare NEWNS: %w", err)
		}
	}

	if flags.noteNew {
		if err := syscall.Setpgid(0, 0); err != nil {
			return fmt.Errorf("setpgid: %w", err)
		}
	}

	return nil
}
