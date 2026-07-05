// Command rc is a reverse-engineered, statically compiled port of the Plan 9 shell.
//
// If you want the syntax, go read the original Plan 9 rc(1) man page:
// https://github.com/plan9foundation/plan9/blob/main/sys/man/1/rc.
// We reconstructed the exact AST semantics. If it works in the original, it works here.
//
// This document exists to explain the underlying architecture, specifically how
// this Go implementation deviates from, extends, and fixes the original C codebase.
// This is written for engineers who care about how things actually work.
//
// # Concurrency via Goroutines
//
// In the original C implementation, the '&' operator triggers a fork(2) syscall.
// Go's runtime scheduler fundamentally conflicts with raw fork(2) without an immediate exec().
// Instead of fighting the Go runtime, this port maps the '&' operator directly to
// native Go goroutines.
//
//	# The AST evaluator spawns a goroutine for the subtree.
//	# The environment block is deeply cloned for memory safety.
//	# The virtual PID is immediately bound to $apid.
//	sleep 10 &
//	echo Async job started with ID: $apid
//
// Background execution is significantly lighter and completely contained within a
// single OS process.
//
// # Raw TTY and Line Editing
//
// POSIX shells typically rely on heavy external libraries like GNU Readline or
// demand you wrap the binary in rlwrap. This port ships with its own native
// TTY reader embedded via the golang.org/x/term package.
//
// When stdin is a terminal, the shell drops the TTY into raw mode and manages
// the buffer, cursor positions, and ANSI escape sequences manually. It supports
// standard Emacs bindings. Read the README for the full shortcut table.
//
// Crucially, it implements a native $VISUAL hook:
//
//	# Type a long command, then press Ctrl-V:
//	for (i in 1 2 3) {
//
// Pressing Ctrl-V triggers an immediate suspension of raw mode. The current input buffer
// is flushed to a temporary file, and $VISUAL (or vi) is invoked synchronously.
// When the editor exits, the file is read back into the buffer, deleted, and raw mode
// is re-engaged.
//
// # Process Substitution over Native os.Pipe
//
// The original rc relied on brittle /dev/fd tricks or named pipes (FIFOs) to handle
// process substitution like '<{command}'. These hacks are notoriously unreliable
// across different UNIX-like operating systems.
//
// This port implements process substitution deterministically via Go's native os.Pipe().
//
//	# 1. An in-memory os.Pipe() is created.
//	# 2. 'ls' executes asynchronously, binding its stdout to the write end.
//	# 3. 'cmp' receives the read end formatted transparently as /dev/fd/N.
//	cmp <{ls -l /bin} <{ls -l /usr/bin}
//
// This avoids the filesystem completely. It relies on the Go runtime to manage
// descriptor inheritance and is vastly more resilient across platforms.
//
// # Startup Configuration
//
// The original Plan 9 rc relied heavily on the OS-level '/rc/lib/rcmain' file to
// initialize its environment. This approach is dead on arrival for a portable binary
// meant to run on modern UNIX-like systems.
//
// This port introduces '~/.rcrc'. When started interactively, the shell
// resolves the user's home directory and automatically sources the .rcrc file.
//
//	# ~/.rcrc
//	# Aliases are just functions. No 'export' keywords.
//	fn ll {
//		ls -alF $*
//	}
//	path = ($home/bin $path)
//
// This allows you to cleanly define functions and environment variables locally
// without polluting system directories or requiring root privileges.
//
// # Fixed: Function Heredoc Parsing
//
// The original Plan 9 rc(1) parser contains a notorious historical bug: functions
// containing here-documents fail to parse or execute correctly. The original YACC
// grammar lazily evaluated heredoc payloads, attempting to stream them from the
// lexer during execution rather than binding them at parse-time.
//
// We fixed the grammar and the lexer state machine. When the lexer encounters '<<',
// it now tracks the EOF marker, consumes the entire heredoc payload eagerly, and
// emits a single token containing the complete text.
//
//	# Heredocs inside functions are now bound securely to the AST leaf node.
//	fn motd {
//		cat << EOF
//		Welcome to the system.
//		EOF
//	}
//
// This ensures that when the AST is serialized into the environment (as functions
// are just text in rc) or executed later, the here-document is completely intact.
//
// # Pure Static Binary
//
// No CGO. No shared libraries. No glibc compatibility nightmares.
// The shell is compiled as a pure Go binary with CGO_ENABLED=0. It cross-compiles
// natively to over 30 UNIX architectures right out of the box. You drop the 2MB
// binary on a server, and it works.
//
// # Direct AST Tree-Walking Evaluator
//
// The original C Plan 9 rc shell is a bytecode interpreter. It compiles the
// parsed syntax tree into a flat array of function pointers and opcodes
// (the 'code' array) and executes it inside a virtual machine loop.
//
// This port abandons the bytecode VM entirely.
//
// Instead, it implements a direct, recursive AST tree-walking evaluator.
// When a command is parsed, the execution engine walks the '*Tree' nodes
// recursively. Because Go's function call overhead is negligible and stack
// management is dynamic, the tree-walker approach is extremely fast, avoids
// the immense complexity of compiling shell opcodes, and makes the execution
// flow trivial to debug.
//
// # Memory Management & Garbage Collection
//
// The original Plan 9 rc shell did not use standard malloc(3) because it was
// too slow for constant string manipulation. It implemented a complex, custom
// memory "Arena" system that was historically prone to leaking memory during
// long-running interactive sessions.
//
// This port completely annihilates the Arena system.
//
//	# Strings and AST nodes are just native Go structs.
//	# When the node falls out of scope, the Go runtime collects it.
//	x = (a b c d e f g)
//
// By delegating 100% of memory management to the Go Garbage Collector, we threw
// away thousands of lines of unsafe C code, eliminated the leaks, and drastically
// simplified the AST lifecycle.
//
// # Signal Handling
//
// In Plan 9, if you define a function named 'sigint', the shell intercepts
// the OS interrupt signal and executes your function instead of dying.
//
//	# Catch Ctrl-C gracefully without crashing the shell.
//	fn sigint {
//		echo 'Caught SIGINT. Type exit to leave.'
//	}
//
// In C, signal handlers are a minefield of race conditions and async-signal-safe
// restrictions. This port delegates signal trapping to a dedicated background
// goroutine using 'os/signal'. When a signal like SIGINT arrives, it safely
// queues the event and injects it into the AST evaluation loop without causing
// deadlocks or memory corruption.
//
// # Native Globbing Engine
//
// The original C implementation relied heavily on the underlying Plan 9
// filesystem and libc for pattern matching.
//
//	# Pattern matching works identically, but safely.
//	echo *.go
//
// This port implements a custom, deterministic globbing engine in pure Go,
// completely bypassing the host OS libc and Go's own 'path/filepath'.
// It perfectly mirrors Plan 9's specific matching rules (including dotfile
// handling and character classes), guaranteeing safe, predictable pattern
// matching everywhere.
package main
