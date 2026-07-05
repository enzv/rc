//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly || illumos || solaris || android || aix

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const defaultCommandName = "rc"

// version is set at build time.
var version = "devel"

func printMainError(err error) {
	fmt.Fprintln(os.Stderr, defaultCommandName+":", err)
}

func stdinIsTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func shouldRunInteractive(flags rcFlags, stdin *os.File) bool {
	if flags.HasCommand || len(flags.Args) > 0 || flags.NoInteract {
		return false
	}
	if flags.Interactive {
		return true
	}
	return stdinIsTerminal(stdin)
}

func promptStrings(env *shellEnv) (string, string) {
	prompt := env.lookup("prompt")
	primary := "% "
	continuation := "\t"
	if len(prompt) > 0 && prompt[0] != "" {
		primary = prompt[0]
	}
	if len(prompt) > 1 && prompt[1] != "" {
		continuation = prompt[1]
	}

	if _, ok := env.lookupFunc("prompt"); ok {
		if prog, err := ParseSource("prompt"); err == nil {
			savedStatus := env.lookup("status")
			var buf strings.Builder
			RunProgram(prog, RunOptions{
				Env:             env,
				StdoutWriter:    &buf,
				StderrWriter:    io.Discard,
				DiagWriter:      io.Discard,
				SuppressSigexit: true,
			})
			env.set("status", savedStatus)
			primary = buf.String()
			if strings.HasSuffix(primary, "\n") {
				primary = primary[:len(primary)-1]
			}
		}
	}

	return primary, continuation
}

func isIncompleteInput(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unterminated quoted word") ||
		strings.Contains(msg, `near "EOF"`) ||
		strings.Contains(msg, " near EOF")
}

func runInteractive(input io.Reader, stdout, stderr io.Writer, env *shellEnv, setup bool) (RunResult, error) {
	if env.flags["V"] {
		input = io.TeeReader(input, stderr)
	}
	reader := bufio.NewReader(input)
	var editor *Editor
	if file, ok := input.(*os.File); ok && stdinIsTerminal(file) {
		editor = NewEditor()
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	env.flags["i"] = true
	if setup {
		setupSignals(&runner{
			env:    env,
			stdin:  input,
			stdout: stdout,
			stderr: stderr,
			diag:   stderr,
		})
	}

	var pending strings.Builder
	primary, continuation := promptStrings(env)
	useContinuation := false
	commandInput := io.Reader(reader)
	if _, ok := input.(*os.File); ok {
		// External commands should inherit the underlying interactive file
		// descriptor, not the shell's buffered line reader. Passing the
		// bufio.Reader to os/exec lets non-reading commands like "ls" consume
		// future shell input in the stdin copy goroutine before the prompt is
		// reprinted.
		commandInput = input
	}

	for {
		var line string
		var readErr error

		if editor != nil {
			prompt := primary
			if useContinuation {
				prompt = continuation
			}
			line, readErr = editor.ReadLine(prompt, env.cwd)
			if readErr == nil {
				editor.AddHistory(line)
				line += "\n"
			} else if readErr == ErrInterrupted {
				fmt.Fprintln(stderr)
				pending.Reset()
				useContinuation = false
				continue
			}
		} else {
			if useContinuation {
				if _, err := io.WriteString(stderr, continuation); err != nil {
					return RunResult{}, err
				}
			} else {
				if _, err := io.WriteString(stderr, primary); err != nil {
					return RunResult{}, err
				}
			}
			line, readErr = reader.ReadString('\n')
		}

		if readErr != nil && readErr != io.EOF {
			return RunResult{}, readErr
		}
		if line == "" && readErr == io.EOF {
			break
		}
		pending.WriteString(line)

		prog, err := ParseSource(pending.String())
		if err != nil {
			if readErr != io.EOF && isIncompleteInput(err) {
				useContinuation = true
				continue
			}
			fmt.Fprintln(stderr, defaultCommandName+":", err)
			env.setStatus("1")
			pending.Reset()
			useContinuation = false
			if readErr == io.EOF {
				break
			}
			continue
		}

		result, err := RunProgram(prog, RunOptions{
			Env:             env,
			StdinReader:     commandInput,
			StdoutWriter:    stdout,
			StderrWriter:    stderr,
			DiagWriter:      stderr,
			SuppressSigexit: true,
		})
		if err != nil {
			fmt.Fprintln(stderr, defaultCommandName+":", err)
			env.setStatus("1")
		}
		pending.Reset()
		useContinuation = false
		primary, continuation = promptStrings(env)
		if result.ExitRequested {
			break
		}
	}

	finalRunner := &runner{
		env:    env,
		stdout: stdout,
		stderr: stderr,
		diag:   stderr,
		stdin:  input,
	}
	finalRunner.runSigexit()

	return RunResult{
		Status:   env.status(),
		ExitCode: statusCode(env.status()),
	}, nil
}

func runInitFile(path string, env *shellEnv) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = RunSource(string(data), RunOptions{
		Env:             env,
		StdinReader:     os.Stdin,
		StdoutWriter:    os.Stdout,
		StderrWriter:    os.Stderr,
		DiagWriter:      os.Stderr,
		SuppressSigexit: true,
	})
	return err
}

func shouldLoadProfile(flags rcFlags, argv0 string) bool {
	return flags.Login || strings.HasPrefix(filepath.Base(argv0), "-")
}

func runProfile(env *shellEnv) error {
	home := env.scalar("home")
	if home == "" {
		return nil
	}
	profile := filepath.Join(home, "lib", "profile")
	if _, err := os.Stat(profile); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return runInitFile(profile, env)
}

func runRcrc(env *shellEnv) error {
	home := env.scalar("home")
	if home == "" {
		return nil
	}
	rcrc := filepath.Join(home, ".rcrc")
	if _, err := os.Stat(rcrc); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return runInitFile(rcrc, env)
}

func main() {
	flags := ParseFlags(os.Args[1:])

	if flags.Version {
		fmt.Printf("rc (enzv) %s\n", version)
		os.Exit(0)
	}

	if shouldRunInteractive(flags, os.Stdin) {
		env, err := newShellEnv(nil, "")
		if err != nil {
			printMainError(err)
			os.Exit(1)
		}
		env.vars["rcname"] = []string{defaultCommandName}
		env.vars["argv0"] = []string{os.Args[0]}
		if flags.ExitOnError {
			env.flags["e"] = true
		}
		if flags.VerboseExec {
			env.flags["x"] = true
		}
		if flags.VerboseIn {
			env.flags["v"] = true
		}
		if flags.PrintStatus {
			env.flags["s"] = true
		}
		if flags.LexicalTrace {
			env.flags["V"] = true
		}
		if shouldLoadProfile(flags, os.Args[0]) {
			if err := runProfile(env); err != nil {
				printMainError(err)
				os.Exit(1)
			}
		}
		if err := runRcrc(env); err != nil {
			printMainError(err)
			os.Exit(1)
		}
		if flags.InitFile != "" {
			if err := runInitFile(flags.InitFile, env); err != nil {
				printMainError(err)
				os.Exit(1)
			}
		}
		result, err := runInteractive(os.Stdin, os.Stdout, os.Stderr, env, true)
		if err != nil {
			printMainError(err)
			os.Exit(1)
		}
		os.Exit(result.ExitCode)
	}

	var src string
	var rcname string
	var scriptArgs []string
	var stdinReader io.Reader

	if flags.HasCommand {
		src = flags.Command
		stdinReader = os.Stdin
		if len(flags.Args) > 0 {
			rcname = flags.Args[0]
			scriptArgs = flags.Args[1:]
		} else {
			rcname = defaultCommandName
			scriptArgs = []string{}
		}
	} else if len(flags.Args) > 0 {
		rcname = flags.Args[0]
		data, err := os.ReadFile(rcname)
		if err != nil {
			printMainError(err)
			os.Exit(1)
		}
		src = string(data)
		stdinReader = os.Stdin
		scriptArgs = flags.Args[1:]
	} else {
		rcname = defaultCommandName
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			printMainError(err)
			os.Exit(1)
		}
		src = string(data)
		scriptArgs = []string{}
	}

	if flags.VerboseIn || flags.LexicalTrace {
		fmt.Fprint(os.Stderr, src)
	}

	env, err := newShellEnv(scriptArgs, "")
	if err != nil {
		printMainError(err)
		os.Exit(1)
	}
	env.vars["rcname"] = []string{rcname}
	env.vars["argv0"] = []string{os.Args[0]}
	localizeStar := false
	localArgs := []string(nil)
	if !flags.HasCommand && len(flags.Args) > 0 {
		env.vars["*"] = append([]string{rcname}, scriptArgs...)
		localizeStar = true
		localArgs = append([]string(nil), scriptArgs...)
	}
	if flags.ExitOnError {
		env.flags["e"] = true
	}
	if flags.VerboseExec {
		env.flags["x"] = true
	}
	if flags.VerboseIn {
		env.flags["v"] = true
	}
	if flags.PrintStatus {
		env.flags["s"] = true
	}
	if flags.LexicalTrace {
		env.flags["V"] = true
	}
	if flags.Interactive {
		env.flags["i"] = true
	}
	if shouldLoadProfile(flags, os.Args[0]) {
		if err := runProfile(env); err != nil {
			printMainError(err)
			os.Exit(1)
		}
	}
	if flags.InitFile != "" {
		if err := runInitFile(flags.InitFile, env); err != nil {
			printMainError(err)
			os.Exit(1)
		}
	}

	opts := RunOptions{
		Args:         scriptArgs,
		LocalArgs:    localArgs,
		LocalizeStar: localizeStar,
		StdinReader:  stdinReader,
		StdoutWriter: os.Stdout,
		StderrWriter: os.Stderr,
		DiagWriter:   os.Stderr,
		PrintStatus:  flags.PrintStatus,
		ExitOnError:  flags.ExitOnError,
		VerboseExec:  flags.VerboseExec,
		Debug:        flags.Debug,
		Env:          env,
		SetupSignals: true,
	}

	result, err := RunSource(src, opts)
	if err != nil {
		printMainError(err)
		os.Exit(1)
	}

	if flags.PrintStatus && result.ExitCode != 0 {
		fmt.Fprintln(os.Stderr, env.status())
	}
	os.Exit(result.ExitCode)
}

type rcFlags struct {
	Command      string
	HasCommand   bool
	InitFile     string
	ExitOnError  bool
	Interactive  bool
	NoInteract   bool
	Login        bool
	VerboseIn    bool
	VerboseExec  bool
	Debug        bool
	PrintStatus  bool
	LexicalTrace bool
	Version      bool

	Args []string
}

// ParseFlags parses standard rc command line arguments and flags.
func ParseFlags(args []string) rcFlags {
	flags := rcFlags{}
	var i int
	for i = 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--version" {
			flags.Version = true
			continue
		}
		if len(arg) < 2 || arg[0] != '-' {
			break
		}
		if arg == "--" {
			i++
			break
		}
		for j := 1; j < len(arg); j++ {
			switch arg[j] {
			case 'c':
				flags.HasCommand = true
				if j+1 < len(arg) {
					flags.Command = arg[j+1:]
				} else {
					i++
					if i < len(args) {
						flags.Command = args[i]
					}
				}
				i++ // move past the command string
				goto doneFlags
			case 'm':
				if j+1 < len(arg) {
					flags.InitFile = arg[j+1:]
					j = len(arg)
				} else {
					i++
					if i < len(args) {
						flags.InitFile = args[i]
					}
				}
			case 'e':
				flags.ExitOnError = true
			case 'i':
				flags.Interactive = true
			case 'I':
				flags.NoInteract = true
			case 'l':
				flags.Login = true
			case 's':
				flags.PrintStatus = true
			case 'v':
				flags.VerboseIn = true
			case 'x':
				flags.VerboseExec = true
			case 'r':
				flags.Debug = true
			case 'V':
				flags.LexicalTrace = true
			case 'p', 'd':
				// no-op
			}
		}
	}
doneFlags:

	if i < len(args) {
		flags.Args = args[i:]
	}
	return flags
}

func setupSignals(r *runner) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGALRM)
	go func() {
		for sig := range c {
			switch sig {
			case syscall.SIGINT:
				r.runSignal("sigint")
			case syscall.SIGQUIT:
				r.runSignal("sigquit")
			case syscall.SIGHUP:
				r.runSignal("sighup")
			case syscall.SIGALRM:
				r.runSignal("sigalrm")
			}
		}
	}()
}

// runSigexit runs the sigexit function if defined, typically on shell exit.
func (r *runner) runSigexit() {
	if body, ok := r.env.lookupFunc("sigexit"); ok {
		args := r.env.lookup("*")
		_ = r.execFunc("sigexit", body, args)
	}
}

// runSignal runs the rc function for a signal (e.g. sigint, sighup) if defined.
// It falls back to default OS behavior if not defined.
func (r *runner) runSignal(sigName string) {
	if body, ok := r.env.lookupFunc(sigName); ok {
		args := r.env.lookup("*")
		_ = r.execFunc(sigName, body, args)
	} else if sigName == "sigint" || sigName == "sigquit" {
		if !r.env.flags["i"] {
			os.Exit(r.exitCode())
		}
	} else {
		os.Exit(r.exitCode())
	}
}

func (r *runner) exitCode() int {
	st := r.env.status()
	if st == "" {
		return 0
	}
	if first, _, ok := strings.Cut(st, "|"); ok {
		st = first
	}
	if c, err := strconv.Atoi(st); err == nil {
		return c
	}
	return 1
}
