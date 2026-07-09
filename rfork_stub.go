//go:build !linux && !aix && !darwin && !dragonfly && !freebsd && !illumos && !netbsd && !openbsd && !solaris

package main

import (
	"fmt"
)

func sysValidateRfork(flags rforkFlags) error {
	if flags.nameNew || flags.nameClean || flags.noteNew || flags.noMount {
		return fmt.Errorf("%w: unsupported flag", errRforkNotSupported)
	}
	return nil
}

func sysApplyRfork(flags rforkFlags) error {

	return nil
}
