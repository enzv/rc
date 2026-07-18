package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newInteractiveEnv(t *testing.T) *shellEnv {
	t.Helper()
	env, err := newShellEnv(nil, "")
	if err != nil {
		t.Fatalf("newShellEnv: %v", err)
	}
	env.set("rcname", []string{defaultCommandName})
	env.set("argv0", []string{defaultCommandName})
	return env
}

func buildShell(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), defaultCommandName)
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", exe, ".")

	gopath := t.TempDir()
	t.Cleanup(func() {
		filepath.Walk(gopath, func(path string, info os.FileInfo, err error) error {
			if err == nil {
				os.Chmod(path, 0777)
			}
			return nil
		})
	})

	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(t.TempDir(), "gocache"), "GOPATH="+gopath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build shell: %v\nOutput: %s", err, string(out))
	}
	return exe
}

func TestCLIStartup(t *testing.T) {
	t.Parallel()
	exe := buildShell(t)

	tmpDir := t.TempDir()
	scriptFile := filepath.Join(tmpDir, "file_args.rc")
	os.WriteFile(scriptFile, []byte("echo $rcname $*\n"), 0644)

	tests := []struct {
		name   string
		args   []string
		stdin  string
		stdout string
		stderr string
	}{
		{
			name:   "command flag",
			args:   []string{"-c", "echo hello"},
			stdout: "hello\n",
		},
		{
			name:   "command flag with args",
			args:   []string{"-c", "echo $rcname $1 $2", "mycmd", "a", "b"},
			stdout: "mycmd a b\n",
		},
		{
			name:   "file execution with args",
			args:   []string{scriptFile, "a", "b"},
			stdout: scriptFile + " a b\n",
		},
		{
			name:   "pid exists",
			args:   []string{"-c", "if (! ~ $pid '') echo pid_ok"},
			stdout: "pid_ok\n",
		},
		{
			name:   "default path",
			args:   []string{"-c", "echo $path"},
			stdout: ". /bin /usr/bin\n",
		},
		{
			name:   "default ifs",
			args:   []string{"-c", "whatis ifs"},
			stdout: "ifs=(' ' '\t' '\n')\n",
		},
		{
			name:   "default prompt",
			args:   []string{"-c", "whatis prompt"},
			stdout: "prompt=('% ' '')\n",
		},
		{
			name:   "batch stdin no prompt",
			stdin:  "echo hello\n",
			stdout: "hello\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(exe, tc.args...)
			if tc.stdin != "" {
				cmd.Stdin = strings.NewReader(tc.stdin)
			}
			// Set clean env to avoid PATH pollution
			cmd.Env = []string{}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			_ = cmd.Run()

			if got := stdout.String(); got != tc.stdout {
				t.Errorf("stdout got %q, want %q", got, tc.stdout)
			}
			if got := stderr.String(); got != tc.stderr {
				t.Errorf("stderr got %q, want %q", got, tc.stderr)
			}
		})
	}
}

func TestInteractiveCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		useFileInput bool
		setup        func(t *testing.T) string
		stdout       string
		checkStdout  bool
		stderr       string
		checkStderr  bool
		exitCode     int
		check        func(t *testing.T, gotStdout, gotStderr string)
	}{
		{
			name:        "preserves state",
			setup:       func(t *testing.T) string { return "x=hello\necho $x\nfn f { echo ok }\nf\n" },
			stdout:      "hello\nok\n",
			checkStdout: true,
			stderr:      "% % % % % ",
			checkStderr: true,
			exitCode:    0,
		},
		{
			name:        "status and parse error",
			setup:       func(t *testing.T) string { return ")\ndefinitely_missing_command\necho $\"status\necho ok\n" },
			stdout:      "1\nok\n",
			checkStdout: true,
			exitCode:    0,
			check: func(t *testing.T, gotStdout, gotStderr string) {
				t.Helper()
				if !strings.Contains(gotStderr, "rc:") {
					t.Fatalf("stderr = %q, want rc-prefixed parse/runtime error", gotStderr)
				}
				if !strings.HasSuffix(gotStderr, "% ") {
					t.Fatalf("stderr = %q, want trailing prompt", gotStderr)
				}
			},
		},
		{
			name:        "block continuation",
			setup:       func(t *testing.T) string { return "if(~ x x) {\necho ok\n}\n" },
			stdout:      "ok\n",
			checkStdout: true,
			stderr:      "% % ",
			checkStderr: true,
			exitCode:    0,
		},
		{
			name:        "eof returns last status",
			setup:       func(t *testing.T) string { return "definitely_missing_command\n" },
			stdout:      "",
			checkStdout: true,
			exitCode:    1,
		},
		{
			name:         "external command preserves next prompt",
			useFileInput: true,
			setup: func(t *testing.T) string {
				inputFile := filepath.Join(t.TempDir(), "interactive-input.txt")
				if err := os.WriteFile(inputFile, []byte("ls\necho after\n"), 0o644); err != nil {
					t.Fatalf("WriteFile(%s): %v", inputFile, err)
				}
				return inputFile
			},
			stderr:      "% % % ",
			checkStderr: true,
			exitCode:    0,
			check: func(t *testing.T, gotStdout, gotStderr string) {
				t.Helper()
				if !strings.Contains(gotStdout, "after\n") {
					t.Fatalf("stdout = %q, want output from command after external", gotStdout)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			env := newInteractiveEnv(t)
			var inputReader *strings.Reader
			var stdin *os.File
			if tc.useFileInput {
				inputFile := tc.setup(t)
				var err error
				stdin, err = os.Open(inputFile)
				if err != nil {
					t.Fatalf("Open(%s): %v", inputFile, err)
				}
				defer stdin.Close()
			} else {
				inputReader = strings.NewReader(tc.setup(t))
			}

			var stdout, stderr bytes.Buffer
			var (
				result RunResult
				err    error
			)
			if stdin != nil {
				result, err = runInteractive(stdin, &stdout, &stderr, env, false)
			} else {
				result, err = runInteractive(inputReader, &stdout, &stderr, env, false)
			}
			if err != nil {
				t.Fatalf("runInteractive returned error: %v", err)
			}
			if tc.checkStdout {
				if got := stdout.String(); got != tc.stdout {
					t.Fatalf("stdout got %q, want %q", got, tc.stdout)
				}
			}
			if tc.checkStderr {
				if got := stderr.String(); got != tc.stderr {
					t.Fatalf("stderr got %q, want %q", got, tc.stderr)
				}
			}
			if result.ExitCode != tc.exitCode {
				t.Fatalf("exit code = %d, want %d", result.ExitCode, tc.exitCode)
			}
			if tc.check != nil {
				tc.check(t, stdout.String(), stderr.String())
			}
		})
	}
}

func TestCLIStreamsLoopOutput(t *testing.T) {
	t.Parallel()
	exe := buildShell(t)
	script := filepath.Join(t.TempDir(), "while.rc")
	if err := os.WriteFile(script, []byte("while(~ $#x 0){echo impossible}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", script, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, script)
	cmd.Env = []string{"PATH=/bin:/usr/bin"}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()

	if !strings.HasPrefix(stdout.String(), "impossible\n") {
		t.Fatalf("stdout got %q, want prefix %q", stdout.String(), "impossible\\n")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr got %q, want empty", stderr.String())
	}
}

func TestLexicalTraceFlag(t *testing.T) {
	t.Parallel()
	input := "echo hola\n"
	env := newInteractiveEnv(t)
	env.flags["V"] = true

	var stdout, stderr bytes.Buffer
	result, err := runInteractive(strings.NewReader(input), &stdout, &stderr, env, false)
	if err != nil {
		t.Fatalf("runInteractive failed: %v", err)
	}

	got := stderr.String()
	// LexicalTrace using io.TeeReader will dump exactly what is read,
	// which means it will write the input to stderr.
	// Since runInteractive also prints prompts to stderr, we check for Contains.
	if !strings.Contains(got, input) {
		t.Errorf("LexicalTrace stderr = %q, want it to contain %q", got, input)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
}

func TestParseFlags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args []string
		want rcFlags
	}{
		{
			args: []string{"-sxi", "-c", "echo ok", "arg1", "arg2"},
			want: rcFlags{
				HasCommand:  true,
				Command:     "echo ok",
				PrintStatus: true,
				VerboseExec: true,
				Interactive: true,
				Args:        []string{"arg1", "arg2"},
			},
		},
		{
			args: []string{"-c", "hello", "-x", "arg"}, // after -c, non-flag is command, but wait! In rc, arguments after -c string are just `$0`, `$1`, etc.
			want: rcFlags{
				HasCommand: true,
				Command:    "hello",
				Args:       []string{"-x", "arg"},
			},
		},
		{
			args: []string{"script.rc", "a", "b"},
			want: rcFlags{
				Args: []string{"script.rc", "a", "b"},
			},
		},
	}

	for _, tc := range cases {
		got := ParseFlags(tc.args)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseFlags(%v) = %+v, want %+v", tc.args, got, tc.want)
		}
	}
}

type cliResult struct {
	stdout   string
	stderr   string
	exitCode int
}

type cliOptions struct {
	dir   string
	stdin string
	env   []string
	argv0 string
}

func runCLI(t *testing.T, exe string, opts cliOptions, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command(exe, args...)
	if opts.dir != "" {
		cmd.Dir = opts.dir
	}
	cmd.Env = append([]string{"PATH=/bin:/usr/bin"}, opts.env...)
	if opts.argv0 != "" {
		cmd.Args[0] = opts.argv0
	}
	if opts.stdin != "" {
		cmd.Stdin = strings.NewReader(opts.stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return cliResult{
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		exitCode: exitCode,
	}
}

func TestInvocationConformance(t *testing.T) {
	t.Parallel()
	exe := buildShell(t)

	t.Run("invocation_c_flag", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "echo c-ok")
		if got.stdout != "c-ok\n" || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d", got.stdout, got.stderr, got.exitCode)
		}
	})

	t.Run("invocation_file_args", func(t *testing.T) {
		dir := t.TempDir()
		script := filepath.Join(dir, "args.rc")
		if err := os.WriteFile(script, []byte("echo args:$\"*\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", script, err)
		}
		got := runCLI(t, exe, cliOptions{dir: dir}, script, "a", "b")
		if got.stdout != "args:a b\n" || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d", got.stdout, got.stderr, got.exitCode)
		}
	})

	t.Run("invocation_s_flag", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-s", "-c", "~ a b")
		if got.exitCode == 0 {
			t.Fatalf("expected non-zero exit, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
		if !strings.Contains(got.stdout+got.stderr, "1") {
			t.Fatalf("expected printed status, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})

	t.Run("invocation_e_flag", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-e", "-c", "~ a b; echo after")
		if got.exitCode == 0 {
			t.Fatalf("expected non-zero exit, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
		if strings.Contains(got.stdout, "after") {
			t.Fatalf("unexpected continued execution: stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})

	t.Run("invocation_v_flag", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{stdin: "echo v-ok\n"}, "-v")
		if got.stdout != "v-ok\n" || !strings.Contains(got.stderr, "echo v-ok") || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d", got.stdout, got.stderr, got.exitCode)
		}
	})

	t.Run("invocation_x_flag", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-x", "-c", "echo x-ok")
		if got.stdout != "x-ok\n" || !strings.Contains(got.stderr, "echo x-ok") || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d", got.stdout, got.stderr, got.exitCode)
		}
	})

	t.Run("invocation_p_d_noop", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-p", "-d", "-c", "echo noop")
		if got.stdout != "noop\n" || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d", got.stdout, got.stderr, got.exitCode)
		}
	})

	t.Run("invocation_i_flag", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{stdin: "echo i-ok\nexit\n"}, "-i")
		if got.stdout != "i-ok\n" || !strings.Contains(got.stderr, "% ") || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d", got.stdout, got.stderr, got.exitCode)
		}
	})

	t.Run("invocation_I_flag", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-I", "-c", "echo noninteractive")
		if got.stdout != "noninteractive\n" || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d", got.stdout, got.stderr, got.exitCode)
		}
	})

	t.Run("invocation_m_initial", func(t *testing.T) {
		dir := t.TempDir()
		initial := filepath.Join(dir, "initial.rc")
		if err := os.WriteFile(initial, []byte("x=initial\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", initial, err)
		}
		got := runCLI(t, exe, cliOptions{dir: dir}, "-m", initial, "-c", "echo $x")
		if got.stdout != "initial\n" || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d", got.stdout, got.stderr, got.exitCode)
		}
	})

	t.Run("invocation_l_flag_profile", func(t *testing.T) {
		dir := t.TempDir()
		home := filepath.Join(dir, "home")
		if err := os.MkdirAll(filepath.Join(home, "lib"), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", home, err)
		}
		profile := filepath.Join(home, "lib", "profile")
		if err := os.WriteFile(profile, []byte("x=profile\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", profile, err)
		}
		got := runCLI(t, exe, cliOptions{dir: dir, env: []string{"HOME=" + home}}, "-l", "-c", "echo $x")
		if got.stdout != "profile\n" || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d", got.stdout, got.stderr, got.exitCode)
		}
	})

	t.Run("invocation_r_flag", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-r", "-c", "echo debug")
		if got.stdout != "debug\n" || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d", got.stdout, got.stderr, got.exitCode)
		}
	})

	t.Run("invocation_c_flag_extra_args", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "echo star:$\"*; echo one:$1", "arg0", "a", "b")
		if got.stdout != "star:a b\none:a\n" || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d", got.stdout, got.stderr, got.exitCode)
		}
	})

	t.Run("cd_file_rejected", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "file"), []byte("not a dir"), 0o644); err != nil {
			t.Fatalf("WriteFile(file): %v", err)
		}
		got := runCLI(t, exe, cliOptions{dir: dir}, "-c", "cd file; echo status:$status pwd:$pwd")
		wantStdout := "status:1 pwd:" + dir + "\n"
		if got.stdout != wantStdout || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d, want stdout=%q", got.stdout, got.stderr, got.exitCode, wantStdout)
		}
		if !strings.Contains(got.stderr, "Can't cd file: Not a directory") {
			t.Fatalf("stderr=%q, want not-a-directory diagnostic", got.stderr)
		}
	})

	t.Run("invocation_login_argv0_dash", func(t *testing.T) {
		dir := t.TempDir()
		home := filepath.Join(dir, "home")
		if err := os.MkdirAll(filepath.Join(home, "lib"), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", home, err)
		}
		profile := filepath.Join(home, "lib", "profile")
		if err := os.WriteFile(profile, []byte("x=login\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", profile, err)
		}
		got := runCLI(t, exe, cliOptions{dir: dir, env: []string{"HOME=" + home}, argv0: "-rc"}, "-c", "echo $x")
		if got.stdout != "login\n" || got.exitCode != 0 {
			t.Fatalf("stdout=%q stderr=%q exit=%d", got.stdout, got.stderr, got.exitCode)
		}
	})

	t.Run("parse_error_unmatched_quote", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "echo 'unterminated")
		if got.exitCode == 0 {
			t.Fatalf("expected parse failure, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})

	t.Run("parse_error_unmatched_brace", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "{ echo hi")
		if got.exitCode == 0 {
			t.Fatalf("expected parse failure, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})

	t.Run("parse_error_unmatched_parenthesis", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "if(~ a a")
		if got.exitCode == 0 {
			t.Fatalf("expected parse failure, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})

	t.Run("parse_error_bad_redir", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "echo hi >[")
		if got.exitCode == 0 {
			t.Fatalf("expected parse failure, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})

	t.Run("parse_error_invalid_fd_redirection", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "echo hi >[x]out")
		if got.exitCode == 0 {
			t.Fatalf("expected parse failure, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})

	t.Run("parse_error_bad_subscript", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "x=(a b)\necho $x(a-b-c)")
		if got.exitCode == 0 {
			t.Fatalf("expected subscript failure, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})

	t.Run("parse_error_malformed_switch", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "switch(a) case a")
		if got.exitCode == 0 {
			t.Fatalf("expected switch parse failure, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})

	t.Run("parse_error_malformed_fn", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "fn { echo no }")
		if got.exitCode == 0 {
			t.Fatalf("expected fn parse failure, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})

	t.Run("parse_error_malformed_for", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "for(x in a b")
		if got.exitCode == 0 {
			t.Fatalf("expected for parse failure, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})

	t.Run("keyword_first_word_rejected", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "if")
		if got.exitCode == 0 {
			t.Fatalf("expected keyword parse failure, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})

	t.Run("parse_error_if_not_without_if", func(t *testing.T) {
		got := runCLI(t, exe, cliOptions{}, "-c", "if not echo no")
		if got.exitCode == 0 {
			t.Fatalf("expected if-not failure, got stdout=%q stderr=%q", got.stdout, got.stderr)
		}
	})
}
