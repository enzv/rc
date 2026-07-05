package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const minExtraFd = 3

// RunOptions configures the execution environment for a program.
type RunOptions struct {
	Args            []string
	LocalArgs       []string
	LocalizeStar    bool
	Stdin           string
	StdinReader     io.Reader
	Cwd             string
	StdoutWriter    io.Writer
	StderrWriter    io.Writer
	DiagWriter      io.Writer
	PrintStatus     bool
	ExitOnError     bool
	VerboseExec     bool
	VerboseIn       bool
	Debug           bool
	Env             *shellEnv
	SetupSignals    bool
	SuppressSigexit bool
	Context         context.Context
}

// RunResult captures the output and status of an rc execution.
type RunResult struct {
	Stdout        string
	Stderr        string
	Status        string
	ExitCode      int
	ExitRequested bool
}

// runner is the rc interpreter state for a single execution context.
type runner struct {
	env        *shellEnv
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	diag       io.Writer
	extraFiles []*os.File
	fdReaders  map[int]io.Reader
	fdWriters  map[int]io.Writer
	ctx        context.Context
}

type exitSignal struct {
	status string
	code   int
}

func (e exitSignal) Error() string {
	return "exit"
}

func statusCode(status string) int {
	if isTrueStatus(status) {
		return 0
	}
	if n, err := strconv.Atoi(status); err == nil {
		return n
	}
	return 1
}

func isTrueStatus(status string) bool {
	for i := 0; i < len(status); i++ {
		if status[i] != '0' && status[i] != '|' {
			return false
		}
	}
	return true
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// RunSource parses and executes rc source code from a string.
func RunSource(src string, opts RunOptions) (RunResult, error) {
	prog, err := ParseSource(src)
	if err != nil {
		return RunResult{}, err
	}
	return RunProgram(prog, opts)
}

// RunProgram executes an already parsed AST program.
func RunProgram(prog *Program, opts RunOptions) (RunResult, error) {
	var err error
	env := opts.Env
	if env == nil {
		env, err = newShellEnv(opts.Args, opts.Cwd)
		if err != nil {
			return RunResult{}, err
		}
	}
	outBuf := &safeBuffer{}
	errBuf := &safeBuffer{}
	stdout := io.Writer(outBuf)
	stderr := io.Writer(errBuf)
	if opts.StdoutWriter != nil {
		stdout = opts.StdoutWriter
	}
	if opts.StderrWriter != nil {
		stderr = opts.StderrWriter
	}
	diag := stderr
	if opts.DiagWriter != nil {
		diag = opts.DiagWriter
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	r := &runner{
		env:    env,
		stdout: stdout,
		stderr: stderr,
		diag:   diag,
		ctx:    ctx,
	}
	if opts.StdinReader != nil {
		r.stdin = opts.StdinReader
	} else {
		r.stdin = strings.NewReader(opts.Stdin)
	}
	r.bindReader(0, r.stdin)
	r.bindWriter(1, r.stdout)
	r.bindWriter(2, r.stderr)
	if opts.SetupSignals {
		setupSignals(r)
	}
	if opts.LocalizeStar {
		err = r.execWithLocalStar(prog.Root, opts.LocalArgs)
	} else {
		err = r.exec(prog.Root)
	}
	if !opts.SuppressSigexit {
		r.runSigexit()
	}
	result := RunResult{
		Stdout: outBuf.String(),
		Stderr: errBuf.String(),
		Status: r.env.status(),
	}
	if sig, ok := err.(exitSignal); ok {
		result.Status = sig.status
		result.ExitCode = sig.code
		result.ExitRequested = true
		return result, nil
	}
	if err != nil {
		if result.Status == "" {
			result.Status = "1"
		}
		result.ExitCode = statusCode(result.Status)
		return result, err
	}
	result.ExitCode = statusCode(result.Status)
	return result, nil
}

// exec dispatches AST node execution.
func (r *runner) exec(node *Tree) error {
	if node == nil {
		r.env.setStatus("")
		return nil
	}
	switch node.Type {
	case ';':
		if err := r.exec(node.Child[0]); err != nil {
			return err
		}
		if r.env.flags["e"] && !isTrueStatus(r.env.status()) {
			return exitSignal{
				status: r.env.status(),
				code:   statusCode(r.env.status()),
			}
		}
		return r.exec(node.Child[1])
	case '&':
		return r.execAsync(node)
	case '=':
		return r.execAssignment(node)
	case tokenSimple:
		return r.execSimple(node)
	case tokenTwiddle:
		return r.execTwiddle(node)
	case tokenRedir, tokenDup:
		return r.execRedir(node)
	case tokenPipe:
		return r.execPipe(node)
	case tokenBrace, tokenPCmd:
		return r.exec(node.Child[0])
	case tokenIf:
		return r.execIf(node)
	case tokenNot:
		return r.execIfNot(node)
	case tokenFor:
		return r.execFor(node)
	case tokenWhile:
		return r.execWhile(node)
	case tokenSwitch:
		return r.execSwitch(node)
	case tokenBang:
		return r.execBang(node)
	case tokenSubshell:
		return r.execSubshell(node)
	case tokenAndAnd:
		return r.execAndAnd(node)
	case tokenOrOr:
		return r.execOrOr(node)
	case tokenFn:
		return r.execFnDef(node)
	default:
		return fmt.Errorf("unsupported runtime node %s", tokenName(node.Type))
	}
}

func (r *runner) execTwiddle(node *Tree) error {
	subjects, err := r.expandWord(node.Child[0])
	if err != nil {
		return err
	}
	patterns, err := r.expandWords(node.Child[1])
	if err != nil {
		return err
	}
	args := []string{"~"}
	args = append(args, subjects...)
	args = append(args, patterns...)
	return r.execMatch(args[1:])
}

func (r *runner) execAssignment(node *Tree) error {
	r.env.ifstate = 0
	name, err := r.expandName(node.Child[0])
	if err != nil {
		return err
	}
	values, err := r.expandWord(node.Child[1])
	if err != nil {
		return err
	}
	if node.Child[2] == nil {
		r.env.set(name, values)
		r.env.setStatus("")
		return nil
	}
	saved, had := r.env.vars[name]
	r.env.set(name, values)
	err = r.exec(node.Child[2])
	if had {
		r.env.vars[name] = saved
	} else {
		r.env.unset(name)
	}
	return err
}

func (r *runner) execSimple(node *Tree) error {
	r.env.ifstate = 0
	parts := flattenArgList(node.Child[0])
	var args []string
	for _, part := range parts {
		values, err := r.expandWord(part)
		if err != nil {
			return err
		}
		args = append(args, values...)
	}
	if len(args) == 0 {
		r.env.setStatus("")
		return nil
	}
	args, err := expandGlobWords(args, r.env.cwd)
	if err != nil {
		return err
	}
	err = r.dispatch(args)
	if errors.Is(err, syscall.EBADF) {
		_, _ = fmt.Fprintf(r.stderr, "%s: write error: Bad file descriptor\n", args[0])
		r.env.setStatus("1")
		err = nil
	}

	for _, f := range r.extraFiles {
		if f != nil {
			f.Close()
		}
	}
	r.extraFiles = nil
	return err
}

func flattenArgList(node *Tree) []*Tree {
	if node == nil {
		return nil
	}
	if node.Type != tokenArgList {
		return []*Tree{node}
	}
	var out []*Tree
	out = append(out, flattenArgList(node.Child[0])...)
	if node.Child[1] != nil {
		out = append(out, node.Child[1])
	}
	return out
}

func (r *runner) dispatch(args []string) error {
	// Check functions first, matching rc semantics.
	if body, ok := r.env.lookupFunc(args[0]); ok {
		return r.execFunc(args[0], body, args[1:])
	}
	if r.env.flags["x"] {
		if _, err := fmt.Fprintln(r.diag, strings.Join(args, " ")); err != nil {
			return err
		}
	}
	if args[0] != "echo" {
		if fn, ok := builtins[args[0]]; ok {
			return fn(r, args[1:])
		}
	}
	if fn, ok := builtins[args[0]]; ok && args[0] == "echo" {
		return fn(r, args[1:])
	}
	return r.execExternal(args)
}

func (r *runner) execExternal(args []string) error {
	bin := r.searchPath(args[0])
	if bin == "" {
		_, _ = fmt.Fprintf(r.stderr, "%s: No such file or directory\n", args[0])
		r.env.setStatus("1")
		return nil
	}
	cmd := exec.CommandContext(r.ctx, bin, args[1:]...)
	cmd.Args = append([]string{args[0]}, args[1:]...)
	cmd.Dir = r.env.cwd
	cmd.Env = r.exportEnv()
	cmd.Stdin = r.stdin
	cmd.Stdout = r.stdout
	cmd.Stderr = r.stderr
	cmd.ExtraFiles = r.extraFiles
	err := cmd.Run()
	if err == nil {
		r.env.setStatus("")
		return nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		code := exitErr.ExitCode()
		r.env.setStatus(strconv.Itoa(code))
		return nil
	}
	// Report command not found or other exec errors like rc does.
	_, _ = fmt.Fprintf(r.stderr, "%s: %v\n", args[0], err)
	r.env.setStatus(args[0] + ": not found")
	return nil
}

func (r *runner) exportEnv() []string {
	return r.env.exportEnv()
}

func (r *runner) child(env *shellEnv) *runner {
	readers := make(map[int]io.Reader, len(r.fdReaders))
	for fd, reader := range r.fdReaders {
		readers[fd] = reader
	}
	writers := make(map[int]io.Writer, len(r.fdWriters))
	for fd, writer := range r.fdWriters {
		writers[fd] = writer
	}
	return &runner{
		env:       env,
		stdin:     r.stdin,
		stdout:    r.stdout,
		stderr:    r.stderr,
		diag:      r.diag,
		fdReaders: readers,
		fdWriters: writers,
		ctx:       r.ctx,
	}
}

// execAsync implements: command &
func (r *runner) execAsync(node *Tree) error {
	sub := r.child(r.env.clone())
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	sub.stdin = devNull

	r.env.jobs.mu.Lock()
	pid := strconv.Itoa(r.env.jobs.nextPID)
	r.env.jobs.nextPID++
	done := make(chan string, 1)
	r.env.jobs.jobs[pid] = done
	r.env.jobs.mu.Unlock()

	r.env.set("apid", []string{pid})

	go func() {
		sub.exec(node.Child[0])
		devNull.Close()
		st := sub.env.status()

		r.env.jobs.mu.Lock()
		done <- st
		close(done)
		r.env.jobs.mu.Unlock()
	}()

	r.env.setStatus("")
	// Execute following commands if they exist (in the AST, '&' wraps the left command and is part of a list)
	return r.exec(node.Child[1])
}

// execIf implements: if (list) command
// Sets env.ifstate to track whether the condition was false.
func (r *runner) execIf(node *Tree) error {
	// Execute condition (Child[0] is a tokenPCmd wrapping the condition list).
	if err := r.exec(node.Child[0]); err != nil {
		return err
	}
	if isTrueStatus(r.env.status()) {
		r.env.ifstate = 0
		err := r.exec(node.Child[1])
		// After the if-body runs, mark that the condition was true.
		// This makes subsequent 'if not' skip.
		r.env.ifstate = 2
		return err
	}
	// Mark for 'if not' because the condition was false.
	r.env.ifstate = 1
	return nil
}

// execIfNot implements: if not command
// Only executes body if the preceding if-condition was false (env.ifstate == 1).
func (r *runner) execIfNot(node *Tree) error {
	state := r.env.ifstate
	r.env.ifstate = 0
	if state == 1 {
		return r.exec(node.Child[0])
	}
	if state == 2 {
		return nil
	}
	r.shellErrorf("if not without matching if\n")
	r.env.setStatus("1")
	return exitSignal{status: "1", code: 1}
}

// execFor implements: for (name in args) command and for (name) command
func (r *runner) execFor(node *Tree) error {
	nameStr, err := r.expandName(node.Child[0])
	if err != nil {
		return err
	}
	var items []string
	if node.Child[1] != nil {
		// Explicit word list: for (x in a b c)
		items, err = r.expandWords(node.Child[1])
		if err != nil {
			return err
		}
		items, err = expandGlobWords(items, r.env.cwd)
		if err != nil {
			return err
		}
	} else {
		// No 'in' clause: iterate over $*
		items = r.env.lookup("*")
	}
	for _, item := range items {
		r.env.set(nameStr, []string{item})
		if err := r.exec(node.Child[2]); err != nil {
			return err
		}
	}
	return nil
}

// execWhile implements: while (list) command
func (r *runner) execWhile(node *Tree) error {
	for {
		if err := r.exec(node.Child[0]); err != nil {
			return err
		}
		if !isTrueStatus(r.env.status()) {
			return nil
		}
		if err := r.exec(node.Child[1]); err != nil {
			return err
		}
	}
}

// execSwitch implements: switch (arg) { list }
// Searches for top-level 'case' simple commands and matches patterns.
func (r *runner) execSwitch(node *Tree) error {
	argVals, err := r.expandWord(node.Child[0])
	if err != nil {
		return err
	}
	if len(argVals) == 0 {
		return nil
	}
	subject := argVals[0]

	// The body is a tokenBrace wrapping a semicolon-list.
	body := node.Child[1]
	if body == nil {
		return nil
	}
	if body.Type == tokenBrace {
		body = body.Child[0]
	}

	stmts := flattenSemicolons(body)

	// Find case blocks: a 'case' command starts a block that runs
	// until the next case command.
	type caseBlock struct {
		patterns []string
		stmts    []*Tree
	}
	var cases []caseBlock
	var current *caseBlock
	for _, stmt := range stmts {
		if isCase(stmt) {
			pats, perr := r.extractCasePatterns(stmt)
			if perr != nil {
				return perr
			}
			cases = append(cases, caseBlock{patterns: pats})
			current = &cases[len(cases)-1]
		} else if current != nil {
			current.stmts = append(current.stmts, stmt)
			cases[len(cases)-1] = *current
		}
	}

	// Match subject against each case.
	for _, c := range cases {
		for _, pat := range c.patterns {
			// In switch, use relaxed matching (no special path restrictions).
			if matchPattern(subject, pat, true) {
				for _, stmt := range c.stmts {
					if err := r.exec(stmt); err != nil {
						return err
					}
				}
				return nil
			}
		}
	}
	return nil
}

// flattenSemicolons collects all statements from a semicolon-tree into a slice.
func flattenSemicolons(node *Tree) []*Tree {
	if node == nil {
		return nil
	}
	if node.Type == ';' {
		left := flattenSemicolons(node.Child[0])
		right := flattenSemicolons(node.Child[1])
		return append(left, right...)
	}
	return []*Tree{node}
}

// isCase checks if a statement is a simple command starting with "case".
func isCase(node *Tree) bool {
	if node == nil {
		return false
	}
	if node.Type != tokenSimple {
		return false
	}
	parts := flattenArgList(node.Child[0])
	if len(parts) == 0 {
		return false
	}
	first := parts[0]
	return first.Type == tokenWord && first.Str == "case"
}

// extractCasePatterns expands the words after "case" in a case command.
func (r *runner) extractCasePatterns(node *Tree) ([]string, error) {
	parts := flattenArgList(node.Child[0])
	if len(parts) <= 1 {
		return nil, nil
	}
	var pats []string
	for _, p := range parts[1:] {
		vals, err := r.expandWord(p)
		if err != nil {
			return nil, err
		}
		pats = append(pats, vals...)
	}
	return pats, nil
}

// execBang implements: ! command
// Inverts the status: true becomes "false", false becomes "".
func (r *runner) execBang(node *Tree) error {
	err := r.exec(node.Child[0])
	if err != nil {
		return err
	}
	if isTrueStatus(r.env.status()) {
		r.env.setStatus("false")
	} else {
		r.env.setStatus("")
	}
	return nil
}

// execSubshell implements: @ command
// Runs the child in a cloned environment (simulating a subshell).
func (r *runner) execSubshell(node *Tree) error {
	sub := r.child(r.env.clone())
	err := sub.exec(node.Child[0])
	// Propagate status back to parent.
	r.env.setStatus(sub.env.status())
	return err
}

// execAndAnd implements: left && right
// Execute right only if left status is true.
func (r *runner) execAndAnd(node *Tree) error {
	if err := r.exec(node.Child[0]); err != nil {
		return err
	}
	if isTrueStatus(r.env.status()) {
		return r.exec(node.Child[1])
	}
	return nil
}

// execOrOr implements: left || right
// Execute right only if left status is false.
func (r *runner) execOrOr(node *Tree) error {
	if err := r.exec(node.Child[0]); err != nil {
		return err
	}
	if !isTrueStatus(r.env.status()) {
		return r.exec(node.Child[1])
	}
	return nil
}

// execFnDef implements: fn name { list } and fn name (deletion)
func (r *runner) execFnDef(node *Tree) error {
	names, err := r.expandWords(node.Child[0])
	if err != nil {
		return err
	}
	body := node.Child[1]
	for _, name := range names {
		if body != nil {
			r.env.defineFunc(name, body)
		} else {
			r.env.deleteFunc(name)
		}
	}
	r.env.setStatus("")
	return nil
}

// execFunc runs a previously defined function body with the given arguments.
// Temporarily binds $* to the function arguments, restoring the caller's $*
// when the function returns.
func (r *runner) execFunc(name string, body *Tree, args []string) error {
	savedStar, hadStar := r.env.vars["*"]
	r.env.set("*", args)
	err := r.exec(body)
	if hadStar {
		r.env.vars["*"] = savedStar
	} else {
		r.env.unset("*")
	}
	return err
}

func (r *runner) execWithLocalStar(node *Tree, args []string) error {
	savedStar, hadStar := r.env.vars["*"]
	r.env.set("*", args)
	err := r.exec(node)
	if hadStar {
		r.env.vars["*"] = savedStar
	} else {
		r.env.unset("*")
	}
	return err
}

func normalizeName(name string) string {
	return strings.ReplaceAll(name, string(globMark), "")
}

func (r *runner) shellPrefix() string {
	argv0 := r.env.scalar("argv0")
	if argv0 == "" {
		return ""
	}
	return fmt.Sprintf("rc (%s): ", argv0)
}

func (r *runner) shellErrorf(format string, args ...any) {
	prefix := r.shellPrefix()
	if prefix == "" {
		_, _ = fmt.Fprintf(r.diag, format, args...)
		return
	}
	_, _ = fmt.Fprintf(r.diag, prefix+format, args...)
}

func (r *runner) searchPath(name string) string {
	if filepath.IsAbs(name) || strings.HasPrefix(name, "./") || strings.HasPrefix(name, "../") {
		if stat, err := os.Stat(name); err == nil && !stat.IsDir() {
			return name
		}
		return ""
	}
	for _, dir := range r.env.lookup("path") {
		try := filepath.Join(dir, name)
		if stat, err := os.Stat(try); err == nil && !stat.IsDir() {
			return try
		}
	}
	return ""
}

func (r *runner) expandWord(node *Tree) ([]string, error) {
	if node == nil {
		return nil, nil
	}
	switch node.Type {
	case tokenWord:
		return []string{node.Str}, nil
	case tokenParen:
		return r.expandWords(node.Child[0])
	case '$':
		name, err := r.expandName(node.Child[0])
		if err != nil {
			return nil, err
		}
		return r.lookupVar(name), nil
	case tokenCount:
		name, err := r.expandName(node.Child[0])
		if err != nil {
			return nil, err
		}
		return []string{strconv.Itoa(len(r.lookupVar(name)))}, nil
	case '"':
		name, err := r.expandName(node.Child[0])
		if err != nil {
			return nil, err
		}
		return []string{strings.Join(r.lookupVar(name), " ")}, nil
	case tokenSub:
		name, err := r.expandName(node.Child[0])
		if err != nil {
			return nil, err
		}
		values := r.lookupVar(name)
		parts, err := r.expandWords(node.Child[1])
		if err != nil {
			return nil, err
		}
		var out []string
		for _, part := range parts {
			indexes, err := parseSubscript(part, len(values))
			if err != nil {
				return nil, err
			}
			for _, index := range indexes {
				out = append(out, values[index])
			}
		}
		return out, nil
	case '^':
		left, err := r.expandWord(node.Child[0])
		if err != nil {
			return nil, err
		}
		right, err := r.expandWord(node.Child[1])
		if err != nil {
			return nil, err
		}
		res, err := concatWords(left, right)
		if err != nil {
			prefix := r.shellPrefix()
			target := r.diag
			if target == nil {
				target = r.stderr
			}
			if target != nil {
				if prefix == "" {
					_, _ = fmt.Fprintf(target, "%s\n", err)
				} else {
					_, _ = fmt.Fprintf(target, "%s%s\n", prefix, err)
				}
			}
			r.env.setStatus("1")
			return nil, exitSignal{status: "1", code: 1}
		}
		return res, nil
	case '`':
		return r.execBackquote(node)
	case tokenPipeFD:
		return r.execProcSub(node)
	default:
		return nil, fmt.Errorf("unsupported word node %s", tokenName(node.Type))
	}
}

// execProcSub implements process substitution: <{ command } and >{ command }
func (r *runner) execProcSub(node *Tree) ([]string, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	isRead := node.FD0 == 0
	var parentFile, childFile *os.File
	if isRead {
		parentFile = pr
		childFile = pw
	} else {
		parentFile = pw
		childFile = pr
	}

	// Calculate the index for extraFiles mapping
	osFd := int(parentFile.Fd())
	idx := osFd - minExtraFd
	for len(r.extraFiles) <= idx {
		r.extraFiles = append(r.extraFiles, nil)
	}
	r.extraFiles[idx] = parentFile

	r.env.jobs.mu.Lock()
	pid := strconv.Itoa(r.env.jobs.nextPID)
	r.env.jobs.nextPID++
	done := make(chan string, 1)
	r.env.jobs.jobs[pid] = done
	r.env.jobs.mu.Unlock()

	r.env.set("apid", []string{pid})

	sub := r.child(r.env.clone())
	if isRead {
		sub.stdout = childFile
	} else {
		sub.stdin = childFile
	}

	go func() {
		sub.exec(node.Child[0])
		childFile.Close()

		st := sub.env.status()
		r.env.jobs.mu.Lock()
		done <- st
		close(done)
		r.env.jobs.mu.Unlock()
	}()

	return []string{procSubFDPath(osFd)}, nil
}

func procSubFDPath(fd int) string {
	return fmt.Sprintf("/dev/fd/%d", fd)
}

// execBackquote runs the command inside `{...}, captures stdout,
// and splits the output into a word list using $ifs characters.
var builtins map[string]func(*runner, []string) error

func init() {
	builtins = map[string]func(*runner, []string) error{
		"echo":    (*runner).execEcho,
		"exit":    (*runner).execExit,
		"~":       (*runner).execMatch,
		"cd":      (*runner).execCD,
		"shift":   (*runner).execShift,
		"eval":    (*runner).execEval,
		"wait":    (*runner).execWait,
		".":       (*runner).execDot,
		"builtin": (*runner).execBuiltin,
		"exec":    (*runner).execExec,
		"flag":    (*runner).execFlag,
		"whatis":  (*runner).execWhatis,
		"rfork":   (*runner).execRfork,
	}
}
func (r *runner) execBackquote(node *Tree) ([]string, error) {
	var buf bytes.Buffer
	sub := &runner{
		env:    r.env,
		stdin:  r.stdin,
		stdout: &buf,
		stderr: r.stderr,
		diag:   r.diag,
		ctx:    r.ctx,
	}
	// Backquotes isolate evaluation errors from the parent sequence.
	_ = sub.exec(node.Child[0])
	output := buf.String()
	return splitByIFS(output, r.env), nil
}

// splitByIFS splits output into words using the characters in $ifs.
// If $ifs is not set, defaults to space, tab, newline.
// Multiple consecutive IFS characters do not produce empty words.
func splitByIFS(output string, env *shellEnv) []string {
	ifs := " \t\n"
	if v, ok := env.vars["ifs"]; ok {
		if len(v) == 0 {
			ifs = "" // Empty list means no splitting
		} else {
			ifs = strings.Join(v, "")
		}
	}
	if ifs == "" {
		// If ifs is empty string, entire output is one word (no splitting).
		if output == "" {
			return nil
		}
		return []string{output}
	}
	var words []string
	var cur strings.Builder
	for _, ch := range output {
		if strings.ContainsRune(ifs, ch) {
			if cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
			}
		} else {
			cur.WriteRune(ch)
		}
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words
}

func (r *runner) expandWords(node *Tree) ([]string, error) {
	if node == nil {
		return nil, nil
	}
	if node.Type != tokenWords {
		return r.expandWord(node)
	}
	left, err := r.expandWords(node.Child[0])
	if err != nil {
		return nil, err
	}
	right, err := r.expandWord(node.Child[1])
	if err != nil {
		return nil, err
	}
	return append(left, right...), nil
}

func (r *runner) expandName(node *Tree) (string, error) {
	values, err := r.expandWord(node)
	if err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "", nil
	}
	return normalizeName(values[0]), nil
}

func (r *runner) lookupVar(name string) []string {
	if name == "" {
		return nil
	}
	if index, err := strconv.Atoi(name); err == nil {
		args := r.env.lookup("*")
		if index < 1 || index > len(args) {
			return nil
		}
		return []string{args[index-1]}
	}
	return r.env.lookup(name)
}

func concatWords(left, right []string) ([]string, error) {
	if len(left) == 0 || len(right) == 0 {
		return nil, fmt.Errorf("null list in concatenation")
	}
	switch {
	case len(left) == len(right):
		out := make([]string, len(left))
		for i := range left {
			out[i] = left[i] + right[i]
		}
		return out, nil
	case len(left) == 1:
		out := make([]string, len(right))
		for i := range right {
			out[i] = left[0] + right[i]
		}
		return out, nil
	case len(right) == 1:
		out := make([]string, len(left))
		for i := range left {
			out[i] = left[i] + right[0]
		}
		return out, nil
	default:
		return nil, fmt.Errorf("mismatched list lengths in concatenation")
	}
}

func (r *runner) execEcho(args []string) error {
	if _, err := fmt.Fprintln(r.stdout, strings.Join(args, " ")); err != nil {
		return err
	}
	r.env.setStatus("")
	return nil
}

func (r *runner) execExit(args []string) error {
	status := r.env.status()
	code := statusCode(status)
	if len(args) != 0 {
		status = args[0]
		code = statusCode(status)
	}
	r.env.setStatus(status)
	return exitSignal{status: status, code: code}
}

func (r *runner) execMatch(args []string) error {
	if len(args) < 2 {
		r.env.setStatus("1")
		return nil
	}
	subject := args[0]
	for _, pattern := range args[1:] {
		if matchPattern(subject, pattern, true) {
			r.env.setStatus("")
			return nil
		}
	}
	r.env.setStatus("1")
	return nil
}

func (r *runner) execCD(args []string) error {
	switch len(args) {
	case 0:
		home := r.env.lookup("home")
		if len(home) == 0 {
			_, _ = fmt.Fprintln(r.diag, "Can't cd -- $home empty")
			r.env.setStatus("1")
			return nil
		}
		path := home[0]
		if !filepath.IsAbs(path) {
			path = filepath.Join(r.env.cwd, path)
		}
		if _, err := os.Stat(path); err != nil {
			_, _ = fmt.Fprintf(r.diag, "Can't cd %s: %s\n", home[0], titleError(err))
			r.env.setStatus("1")
			return nil
		}
		r.env.cwd = path
		r.env.set("PWD", []string{path})
		r.env.set("pwd", []string{path})
		r.env.setStatus("")
		return nil
	case 1:
		target := args[0]
		if filepath.IsAbs(target) || len(r.env.lookup("cdpath")) == 0 {
			path := target
			if !filepath.IsAbs(path) {
				path = filepath.Join(r.env.cwd, path)
			}
			if _, err := os.Stat(path); err != nil {
				_, _ = fmt.Fprintf(r.diag, "Can't cd %s: %s\n", target, titleError(err))
				r.env.setStatus("1")
				return nil
			}
			os.Chdir(path)
			r.env.cwd = path
			r.env.set("PWD", []string{path})
			r.env.set("pwd", []string{path})
			r.env.setStatus("")
			return nil
		}

		for _, dir := range r.env.lookup("cdpath") {
			candidate := target
			if dir != "" {
				candidate = dir + "/" + target
			}
			path := candidate
			if !filepath.IsAbs(path) {
				path = filepath.Join(r.env.cwd, path)
			}
			if _, err := os.Stat(path); err == nil {
				os.Chdir(path)
				r.env.cwd = path
				r.env.set("PWD", []string{path})
				r.env.set("pwd", []string{path})
				if dir != "" && dir != "." {
					_, _ = fmt.Fprintf(r.diag, "%s\n", candidate)
				}
				r.env.setStatus("")
				return nil
			}
		}
		_, _ = fmt.Fprintf(r.diag, "Can't cd %s: No such file or directory\n", target)
		r.env.setStatus("1")
		return nil
	default:
		_, _ = fmt.Fprintln(r.diag, "Usage: cd [directory]")
		r.env.setStatus("1")
		return nil
	}
}

func (r *runner) execShift(args []string) error {
	n := 1
	if len(args) != 0 {
		value, err := strconv.Atoi(args[0])
		if err != nil || value < 0 {
			r.env.setStatus("1")
			return nil
		}
		n = value
	}
	values := r.env.lookup("*")
	if n >= len(values) {
		r.env.set("*", nil)
	} else {
		r.env.set("*", values[n:])
	}
	r.env.setStatus("")
	return nil
}

func (r *runner) execEval(args []string) error {
	src := strings.Join(args, " ")
	prog, err := ParseSource(src)
	if err != nil {
		r.env.setStatus("1")
		return err
	}
	return r.exec(prog.Root)
}

func (r *runner) execWait(args []string) error {
	switch len(args) {
	case 0:
		r.env.jobs.mu.Lock()
		var chans []chan string
		var pids []string
		for pid, ch := range r.env.jobs.jobs {
			chans = append(chans, ch)
			pids = append(pids, pid)
		}
		r.env.jobs.mu.Unlock()

		for i, ch := range chans {
			<-ch
			r.env.jobs.mu.Lock()
			delete(r.env.jobs.jobs, pids[i])
			r.env.jobs.mu.Unlock()
		}
		r.env.setStatus("")
		return nil
	case 1:
		pid := args[0]
		r.env.jobs.mu.Lock()
		ch, ok := r.env.jobs.jobs[pid]
		r.env.jobs.mu.Unlock()

		if !ok {
			return nil
		}

		status := <-ch
		r.env.jobs.mu.Lock()
		delete(r.env.jobs.jobs, pid)
		r.env.jobs.mu.Unlock()

		r.env.setStatus(status)
		return nil
	default:
		r.shellErrorf("Usage: wait [pid]\n")
		r.env.setStatus("1")
		return exitSignal{status: "1", code: 1}
	}
}

func (r *runner) execDot(args []string) error {
	if len(args) == 0 {
		r.shellErrorf("Usage: . [-i] file [arg ...]\n")
		r.env.setStatus("1")
		return nil
	}
	file := args[0]
	var target string
	if filepath.IsAbs(file) || strings.HasPrefix(file, "./") || strings.HasPrefix(file, "../") {
		target = file
	} else {
		for _, dir := range r.env.lookup("path") {
			try := filepath.Join(dir, file)
			if _, err := os.Stat(try); err == nil {
				target = try
				break
			}
		}
	}
	if target == "" {
		prefix := r.shellPrefix()
		if prefix == "" {
			_, _ = fmt.Fprintf(r.diag, "%s: .: can't open: No such file or directory\n", file)
		} else {
			_, _ = fmt.Fprintf(r.diag, "%s: %s.: can't open: No such file or directory\n", file, prefix)
		}
		r.env.setStatus("1")
		return exitSignal{status: "1", code: 1}
	}
	data, err := os.ReadFile(target)
	if err != nil {
		prefix := r.shellPrefix()
		if prefix == "" {
			_, _ = fmt.Fprintf(r.diag, "%s: .: can't open: %s\n", file, titleError(err))
		} else {
			_, _ = fmt.Fprintf(r.diag, "%s: %s.: can't open: %s\n", file, prefix, titleError(err))
		}
		r.env.setStatus("1")
		return exitSignal{status: "1", code: 1}
	}

	savedArgs := r.env.lookup("*")
	r.env.set("*", args[1:])
	defer r.env.set("*", savedArgs)

	prog, err := ParseSource(string(data))
	if err != nil {
		r.env.setStatus("1")
		return err
	}
	return r.exec(prog.Root)
}

func (r *runner) execBuiltin(args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(r.stderr, "builtin: usage: builtin command [arg ...]")
		r.env.setStatus("1")
		return nil
	}
	name := args[0]
	if fn, ok := builtins[name]; ok {
		return fn(r, args[1:])
	}
	_, _ = fmt.Fprintf(r.stderr, "builtin: %s: not a builtin\n", name)
	r.env.setStatus("1")
	return nil
}

func (r *runner) execExec(args []string) error {
	if len(args) == 0 {
		return nil
	}
	err := r.execExternal(args)
	if err != nil {
		return err
	}
	return exitSignal{status: r.env.status(), code: statusCode(r.env.status())}
}

func (r *runner) execFlag(args []string) error {
	if len(args) == 0 || len(args) > 2 {
		r.shellErrorf("Usage: flag [letter] [+-]\n")
		r.env.setStatus("1")
		return nil
	}
	f := args[0]
	if len(f) != 1 {
		r.shellErrorf("Usage: flag [letter] [+-]\n")
		r.env.setStatus("1")
		return nil
	}
	if len(args) == 1 {
		if r.env.flags[f] {
			r.env.setStatus("")
		} else {
			r.env.setStatus("1")
		}
		return nil
	}
	op := args[1]
	if op == "+" {
		r.env.flags[f] = true
	} else if op == "-" {
		r.env.flags[f] = false
	} else {
		r.shellErrorf("Usage: flag [letter] [+-]\n")
		r.env.setStatus("1")
		return nil
	}
	r.env.setStatus("")
	return nil
}

func (r *runner) execWhatis(args []string) error {
	if len(args) == 0 {
		r.shellErrorf("Usage: whatis name ...\n")
		r.env.setStatus("1")
		return nil
	}
	r.env.setStatus("")
	for _, name := range args {
		if vals := r.env.lookup(name); vals != nil {
			_, _ = fmt.Fprintf(r.stdout, "%s=%s\n", name, formatWhatisValues(vals))
			continue
		}
		if body, ok := r.env.lookupFunc(name); ok {
			fmt.Fprintf(r.stdout, "fn %s %s\n", name, FormatTree(body))
			continue
		}
		path := r.searchPath(name)
		if path != "" {
			fmt.Fprintf(r.stdout, "%s\n", path)
			continue
		}
		if _, ok := builtins[name]; ok {
			fmt.Fprintf(r.stdout, "builtin %s\n", name)
			continue
		}
		_, _ = fmt.Fprintf(r.stderr, "%s: not found\n", name)
		r.env.setStatus("1")
	}
	return nil
}

func (r *runner) execRfork(args []string) error {
	switch len(args) {
	case 0:
		_, _ = fmt.Fprintln(r.diag, "rc: rfork failed")
		r.env.setStatus("rfork failed")
	case 1:
		switch args[0] {
		case "n", "e", "s":
			r.env.setStatus("")
		case "N", "E", "f", "F":
			_, _ = fmt.Fprintln(r.diag, "rc: rfork failed")
			r.env.setStatus("rfork failed")
		case "m":
			_, _ = fmt.Fprintln(r.diag, "Usage: rfork [nNeEsfF]")
			r.env.setStatus("rfork usage")
		default:
			_, _ = fmt.Fprintln(r.diag, "Usage: rfork [nNeEsfF]")
			r.env.setStatus("rfork usage")
		}
	default:
		_, _ = fmt.Fprintln(r.diag, "Usage: rfork [nNeEsfF]")
		r.env.setStatus("rfork usage")
	}
	return nil
}

func formatWhatisValues(values []string) string {
	if len(values) == 0 {
		return "()"
	}
	if len(values) == 1 {
		return rcQuoteWord(values[0])
	}
	var b strings.Builder
	b.WriteByte('(')
	for i, value := range values {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(rcQuoteWord(value))
	}
	b.WriteByte(')')
	return b.String()
}

func rcQuoteWord(s string) string {
	for _, r := range s {
		if !wordchr(int(r)) {
			return rcQuote(s)
		}
	}
	if s == "" {
		return "''"
	}
	return s
}

func titleError(err error) string {
	msg := err.Error()
	if idx := strings.LastIndex(msg, ": "); idx >= 0 {
		msg = msg[idx+2:]
	}
	if msg == "" {
		return msg
	}
	return strings.ToUpper(msg[:1]) + msg[1:]
}
