//go:build !solaris && !illumos && !aix

package main

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func foregroundSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: 0}
}

func reclaimTerminal() {
	// unix.Getpgid(0) works uniformly across all POSIX platforms,
	// avoiding the signature divergence of unix.Getpgrp().
	pgid, _ := unix.Getpgid(0)
	_ = unix.IoctlSetPointerInt(0, unix.TIOCSPGRP, pgid)
}
