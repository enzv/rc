package main

import (
	"errors"
)

type rforkFlags struct {
	nameNew   bool // n: RFNAMEG
	nameClean bool // N: RFCNAMEG
	envNew    bool // e: RFENVG
	envClean  bool // E: RFCENVG
	noteNew   bool // s: RFNOTEG
	fdNew     bool // f: RFFDG
	fdClean   bool // F: RFCFDG
	noMount   bool // m: RFNOMNT
}

var (
	errRforkUsage        = errors.New("rfork usage")
	errRforkNotSupported = errors.New("rfork flag not supported on this platform")
)

// validateRforkOS checks if the requested OS-level flags are supported on the
// current platform. It only inspects flags that require OS support: nameNew,
// nameClean, noteNew, and noMount. Flags handled entirely by the shell layer
// (envNew, envClean, fdNew, fdClean) are not inspected here.
func validateRforkOS(flags rforkFlags) error {
	return sysValidateRfork(flags)
}

// applyRforkOS applies the OS-specific portions of rfork to the current
// process. It only handles nameNew, nameClean, noteNew, and noMount; shell
// state mutations (env clearing, fd table) are left to the caller.
func applyRforkOS(flags rforkFlags) error {
	return sysApplyRfork(flags)
}
