//go:build aix || android || darwin || dragonfly || freebsd || illumos || netbsd || openbsd || solaris

package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func sysValidateRfork(flags rforkFlags) error {
	if flags.nameNew || flags.nameClean || flags.noMount {
		return fmt.Errorf("%w: unsupported flag n, N, or m", errRforkNotSupported)
	}
	return nil
}

func sysApplyRfork(flags rforkFlags) error {

	if flags.noteNew {
		if err := unix.Setpgid(0, 0); err != nil {
			return fmt.Errorf("setpgid: %w", err)
		}
	}

	return nil
}
