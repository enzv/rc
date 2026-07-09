//go:build solaris || illumos || aix

package main

import "syscall"

func foregroundSysProcAttr() *syscall.SysProcAttr {
	return nil
}

func reclaimTerminal() {
	// Job control is safely disabled on these platforms to ensure compilation succeeds.
	// The x/sys/unix package exposes incompatible TTY APIs on these systems where
	// unix.Getpgrp returns multiple values on Solaris/Illumos and unix.TIOCSPGRP
	// causes an untyped constant overflow on AIX.
}
