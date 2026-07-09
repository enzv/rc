package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestApplyRforkOS(t *testing.T) {
	// envNew and fdNew are shell-level flags; the OS layer ignores them.
	if err := applyRforkOS(rforkFlags{envNew: true, fdNew: true}); err != nil {
		t.Errorf("applyRforkOS(e/f) failed: %v", err)
	}

	// m (noMount) must fail at validation since no Unix equivalent exists.
	if err := validateRforkOS(rforkFlags{noMount: true}); err == nil {
		t.Errorf("validateRforkOS(m) unexpectedly succeeded, want error")
	}

	if err := validateRforkOS(rforkFlags{nameNew: true}); err == nil {
		runRforkOSHelper(t, "n")
	}

	if err := validateRforkOS(rforkFlags{noteNew: true}); err == nil {
		runRforkOSHelper(t, "s")
	}
}

func TestParseRforkArgsDefault(t *testing.T) {
	flags, err := parseRforkArgs([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !flags.envNew || !flags.nameNew || !flags.noteNew {
		t.Errorf("expected empty args to default to ens, got %+v", flags)
	}
}

func TestParseRforkArgsInvalid(t *testing.T) {
	_, err := parseRforkArgs([]string{"x"})
	if err != errRforkUsage {
		t.Errorf("expected errRforkUsage for unknown flag 'x', got %v", err)
	}
}

// TestParseRforkArgsUsage confirms that multiple separate arguments are rejected.
func TestParseRforkArgsUsage(t *testing.T) {
	_, err := parseRforkArgs([]string{"n", "e"})
	if err != errRforkUsage {
		t.Errorf("expected errRforkUsage for multiple args, got %v", err)
	}
}

func TestParseRforkArgsFlags(t *testing.T) {
	// Combined flags in a single argument are accepted.
	flags, err := parseRforkArgs([]string{"enFm"})
	if err != nil {
		t.Fatalf("unexpected error parsing 'enFm': %v", err)
	}
	if !flags.envNew || !flags.nameNew || !flags.fdClean || !flags.noMount {
		t.Errorf("failed to parse enFm correctly: %+v", flags)
	}
}

// TestRforkMAccepted confirms that 'm' is accepted by the parser even though
// OS support will fail at validation time. rc(1) documents 'm' as RFNOMNT;
// we accept it at parse time to give a clear "unsupported on this platform"
// error rather than a usage error.
func TestRforkMAccepted(t *testing.T) {
	flags, err := parseRforkArgs([]string{"m"})
	if err != nil {
		t.Fatalf("unexpected error parsing 'm': %v", err)
	}
	if !flags.noMount {
		t.Errorf("expected noMount to be true for 'm', got false")
	}
}

func newTestRunner(t *testing.T) (*runner, *shellEnv) {
	t.Helper()
	env, err := newShellEnv(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	r := &runner{
		env:       env,
		diag:      io.Discard,
		fdReaders: make(map[int]io.Reader),
		fdWriters: make(map[int]io.Writer),
	}
	return r, env
}

func runRforkOSHelper(t *testing.T, flag string) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=TestApplyRforkOSHelper")
	cmd.Env = append(os.Environ(), "RFORK_OS_HELPER="+flag)

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Run(); err != nil {
		t.Fatalf("rfork helper %q failed: %v\n%s", flag, err, output.String())
	}
}

func TestApplyRforkOSHelper(t *testing.T) {
	switch os.Getenv("RFORK_OS_HELPER") {
	case "":
		t.Skip("helper subprocess only")
	case "n":
		if err := applyRforkOS(rforkFlags{nameNew: true}); err != nil {
			if errors.Is(err, unix.EPERM) {
				t.Skipf("applyRforkOS(n) requires mount-namespace permission: %v", err)
			}
			t.Fatalf("applyRforkOS(n) failed: %v", err)
		}
	case "s":
		if err := applyRforkOS(rforkFlags{noteNew: true}); err != nil {
			t.Fatalf("applyRforkOS(s) failed: %v", err)
		}
	default:
		t.Fatalf("unknown rfork helper flag %q", os.Getenv("RFORK_OS_HELPER"))
	}
}

func TestRforkECompatibility(t *testing.T) {
	r, _ := newTestRunner(t)
	// 'e' (RFENVG) is a no-op at the OS level; the interpreter already
	// operates with an independent env group by default.
	if err := r.execRfork([]string{"e"}); err != nil {
		t.Fatalf("expected e to succeed, got %v", err)
	}
}

// TestRforkFUnsupported confirms that 'f' (RFFDG / fd table cloning) is
// explicitly rejected and sets $status, without returning an error from
// execRfork itself (which always returns nil, setting status on failure).
func TestRforkFUnsupported(t *testing.T) {
	r, env := newTestRunner(t)

	if err := r.execRfork([]string{"f"}); err != nil {
		t.Fatalf("execRfork returned non-nil error, want nil (errors set $status): %v", err)
	}

	status := env.lookup("status")
	if len(status) == 0 || !strings.HasPrefix(status[0], "rfork failed") {
		t.Errorf("expected $status to start with 'rfork failed', got %v", status)
	}
}

// TestRforkCleanEnvPreservesEssentials confirms that 'E' (RFCENVG) clears
// user-defined shell variables and functions while preserving the essential
// built-in variables that the shell needs to continue running.
func TestRforkCleanEnvPreservesEssentials(t *testing.T) {
	r, env := newTestRunner(t)

	env.set("custom", []string{"foo"})
	env.set("status", []string{"0"})
	env.defineFunc("myfunc", &Tree{})

	if err := r.execRfork([]string{"E"}); err != nil {
		t.Fatal(err)
	}

	if env.lookup("custom") != nil {
		t.Errorf("expected E to clear 'custom', but it was preserved")
	}
	if env.lookup("status") == nil {
		t.Errorf("expected E to preserve 'status', but it was cleared")
	}
	if _, ok := env.lookupFunc("myfunc"); ok {
		t.Errorf("expected E to clear functions, but 'myfunc' survived")
	}
}
