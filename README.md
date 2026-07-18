![Logo](./logo.svg)

`rc` is a ~99% identical port of the Plan 9 `rc(1)` shell. Reverse-engineered directly from the [original C source](https://github.com/plan9foundation/plan9/tree/main/sys/src/cmd/rc) and [man page](https://github.com/plan9foundation/plan9/blob/main/sys/man/1/rc), it reconstructs the exact AST semantics. We even resurrected undocumented quirks like `-V` and obscure list-flattening.

A memory-safe, cross-platform 2MB static binary.

## Features

- Native interactive line editing and command history without `rlwrap`
- Unix native support for Linux, macOS, and BSDs spanning 30+ architectures
- Single statically compiled binary with a low memory footprint
- A best-effort Unix implementation of rfork behavior
- AST-based interpreter providing fast and predictable execution
- Native concurrency mapping `&` background tasks directly to goroutines
- Core Plan 9 mechanics like closures, list flattening, and caret concatenation
- Interactive startup parses `~/.rcrc` for convenient local configurations
- AST parser natively evaluates heredocs inside functions
- Process substitution uses `os.Pipe` circumventing `/dev/fd` restrictions
- The `~` operator mutates `$status`
- Globbing matches single strings instead of lists

## Non-Features

- CGO. Pure static binaries only. iOS/legacy Android dropped due to C linkage.
- Plan 9 support. You already have the real `rc`, use it.
- Windows support. If you want a Plan 9 shell on Windows, you are in the wrong place.
- POSIX compliance overhead. Standard shells already do this.
- The `export` keyword. Variables are just variables.
- Implicit string splitting. A list is always treated as a list.
- Heavy plugin ecosystems.
- Job control. Commands like `fg` or `bg` belong in a terminal multiplexer.
- Programmable auto-completion. We parse commands, not your mind.
- Shell arithmetic. Math belongs in dedicated tools like `expr` or `bc`.
- Cryptic parameter expansion modifiers. Write explicit code instead of relying on arcane syntax.
- Command aliasing. Standard functions handle this better.

## Line Editing

You don't need `rlwrap`. The shell implements its own raw TTY reader with standard bindings.

| Key | Action |
| --- | --- |
| `Up` / `Down` | Navigate history |
| `Left` / `Right` | Move cursor |
| `Ctrl-A` / `Ctrl-E` | Move to start / end of line |
| `Ctrl-U` / `Ctrl-K` | Delete to start / end of line |
| `Ctrl-L` | Clear screen |
| `Ctrl-C` / `Ctrl-D`| Cancel input / Exit shell |
| `Ctrl-V` | Open current line in `$VISUAL` or `vi` |

## Installation

Requires Go 1.26+.

```bash
env CGO_ENABLED=0 go install -trimpath -ldflags="-s -w" github.com/enzv/rc@latest
```

## Usage

Read the original Plan 9 rc manual. It works exactly like that.

## Dogfooding & Testing

This shell builds itself. We don't use `make`. We use `make.rc`. 

It is an `rc` script that lints, cross-compiles, and orchestrates the test suite.
When the plan9port reference image is available, the tests compare this port
against plan9port's `rc` fixture-by-fixture, except for OS-specific `rfork`
cases covered by local contract tests.

Bootstrap the build system using the Go toolchain:

```bash
go run . make.rc build
go run . make.rc test
go run . make.rc all
```
