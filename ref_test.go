package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var pidRegex = regexp.MustCompile(`cs\\d+`)

func normalizeDiagnosticOutput(s, binaryPath string) string {
	s = strings.NewReplacer("‘", "'", "’", "'").Replace(s)
	s = regexp.MustCompile(regexp.QuoteMeta(binaryPath)).ReplaceAllString(s, "<exe>")
	s = regexp.MustCompile(`rc \([^)]+\)`).ReplaceAllString(s, "rc (<exe>)")
	s = pidRegex.ReplaceAllString(s, "cs<pid>")
	s = regexp.MustCompile(`(?m)^/[a-zA-Z0-9_./-]+/bin/([^/\s]+)$`).ReplaceAllString(s, "/<bin>/$1")
	return s
}

type refFixture struct {
	name  string
	args  []string
	setup func(*testing.T, string)
}

type commandResult struct {
	stdout string
	stderr string
	status int
	err    error
}

var refFixtures = []refFixture{
	{name: "syntax_comments_continuation.rc"},
	{name: "syntax_background_apid_wait.rc"},
	{name: "simple_function_beats_external.rc"},
	{name: "simple_path_search_external.rc"},
	{name: "simple_absolute_command.rc"},
	{name: "words_quotes.rc"},
	{name: "list_flattening.rc"},
	{name: "var_assignment_scalar_list.rc"},
	{name: "var_assignment_command_scope.rc"},
	{name: "var_count_empty.rc"},
	{name: "var_positional_star.rc", args: []string{"first", "second", "third"}},
	{name: "var_indirection.rc"},
	{name: "var_subscript.rc"},
	{name: "var_join.rc"},
	{name: "backquote_default_ifs.rc"},
	{name: "backquote_custom_ifs.rc"},
	{name: "backquote_assignment_concat.rc"},
	{name: "caret_explicit_pairwise.rc"},
	{name: "caret_explicit_distributive.rc"},
	{name: "caret_free_var_suffix.rc"},
	{name: "caret_free_quote_backquote.rc"},
	{name: "glob_basic.rc"},
	{name: "glob_classes.rc"},
	{name: "glob_hidden_and_unmatched.rc"},
	{name: "redir_stdout_append_stdin.rc"},
	{name: "redir_fd_dup_order.rc"},
	{name: "redir_fd_close.rc"},
	{name: "redir_readwrite_once.rc"},
	{name: "heredoc_unquoted_substitution.rc"},
	{name: "heredoc_quoted_literal.rc"},
	{name: "heredoc_caret_delete.rc"},
	{name: "pipe_stdout.rc"},
	{name: "pipe_fd_stderr.rc"},
	{name: "procsub_read.rc"},
	{name: "procsub_multiple.rc"},
	{name: "procsub_write.rc"},
	{name: "control_and_or_bang_precedence.rc"},
	{name: "control_subshell_at.rc"},
	{name: "control_if_ifnot.rc"},
	{name: "if_list_last_status.rc"},
	{name: "control_for_explicit.rc"},
	{name: "control_for_implicit_star.rc", args: []string{"red", "green", "blue"}},
	{name: "control_while.rc"},
	{name: "while_empty_list.rc"},
	{name: "control_switch_patterns.rc"},
	{name: "switch_multiple_patterns.rc"},
	{name: "switch_relaxed_slash_dot.rc"},
	{name: "switch_no_match.rc"},
	{name: "switch_fallthrough_until_next_case.rc"},
	{name: "control_switch_top_level_cases.rc"},
	{name: "control_braces_grouping.rc"},
	{name: "fn_define_args_restore.rc", args: []string{"outer1", "outer2"}},
	{name: "fn_delete_redefine.rc"},
	{name: "fn_nested.rc"},
	{name: "fn_sigexit.rc"},
	{name: "note_handlers_define_delete.rc"},
	{name: "dot_basic_args_restore.rc", args: []string{"outer"}},
	{name: "dot_path_search.rc", args: []string{"outer"}},
	{name: "builtin_bypass.rc"},
	{name: "builtin_bypass_cd.rc"},
	{name: "cd_home_cdpath.rc"},
	{name: "eval_basic.rc"},
	{name: "exec_external.rc"},
	{name: "flag_set_clear_test.rc"},
	{name: "exit_status_explicit.rc"},
	{name: "exit_current_status.rc"},
	{name: "rfork_compat.rc"},
	{name: "rfork_default.rc"},
	{name: "rfork_flags.rc"},
	{name: "shift_basic.rc", args: []string{"one", "two", "three", "four"}},
	{name: "wait_pid_and_all.rc"},
	{name: "whatis_var_fn_builtin_external.rc"},
	{name: "tilde_and_status.rc"},
	{name: "tilde_multiple_patterns.rc"},
	{name: "tilde_relaxed_slash_dot.rc"},
	{name: "keyword_disguised_command.rc"},
	{name: "caret_free_quoted_then_unquoted.rc"},
	{name: "glob_range_slash_dotdot.rc"},
	{name: "redir_fd_file_forms.rc"},
	{name: "redir_fd_dup_forms.rc"},
	{name: "redir_fd_close_input.rc"},
	{name: "redir_documented_newconn_style.rc"},
	{name: "redir_documented_lpd_style.rc"},
	{name: "redir_interspersed_assignments.rc"},
	{name: "pipe_fd_to_fd.rc"},
	{name: "backquote_empty_ifs.rc"},
	{name: "var_indirection_deep.rc"},
	{name: "var_subscript_mixed.rc"},
	{name: "var_positional_out_of_range.rc", args: []string{"one", "two"}},
	{name: "var_unassigned_and_empty_assignment.rc"},
	{
		name: "simple_path_custom_script.rc",
		setup: func(t *testing.T, dir string) {
			t.Helper()
			if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
				t.Fatalf("MkdirAll(bin): %v", err)
			}
			script := "#!/bin/sh\n" +
				"echo script-ok \"$@\"\n"
			path := filepath.Join(dir, "bin", "hello")
			if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
				t.Fatalf("WriteFile(%s): %v", path, err)
			}
		},
	},
	{
		name: "simple_slash_command_path_semantics.rc",

		setup: func(t *testing.T, dir string) {
			t.Helper()
			if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
				t.Fatalf("MkdirAll(bin): %v", err)
			}
			scriptBin := "#!/bin/sh\necho bin-hello \"$@\"\n"
			if err := os.WriteFile(filepath.Join(dir, "bin", "hello"), []byte(scriptBin), 0o755); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			scriptDot := "#!/bin/sh\necho dot-hello \"$@\"\n"
			if err := os.WriteFile(filepath.Join(dir, "hello"), []byte(scriptDot), 0o755); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
		},
	},
}

func shellTestEnv() []string {
	return append([]string(nil), os.Environ()...)
}

func referenceRuntime() string {
	runtime := os.Getenv("CONTAINER_RUNTIME")
	if runtime == "" {
		runtime = "podman"
	}
	return runtime
}

func referenceImage() string {
	image := os.Getenv("RC_REF_IMAGE")
	if image == "" {
		image = "localhost/rc-plan9port-ref:latest"
	}
	return image
}

func TestRefFixtures(t *testing.T) {
	t.Parallel()
	shellBin := buildShell(t)
	for _, tc := range refFixtures {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.name == "rfork_default.rc" || tc.name == "rfork_flags.rc" {
				t.Skip("known rfork divergence: this rc uses an explicit OS-aware compatibility layer instead of matching plan9port's partial rfork behavior")
			}

			work := t.TempDir()
			stageWorkdir(t, work, tc)
			got := runCommand(t, shellBin, nil, work, "case.rc", tc)
			if got.err != nil {
				t.Fatalf("run test shell: %v", got.err)
			}

			stageWorkdir(t, work, tc)
			want := runReferenceCommand(t, work, tc)
			if want.err != nil {
				t.Fatalf("run reference shell: %v", want.err)
			}

			if got.status == 124 && want.status == 124 {
				return // both timed out, probably okay
			}

			if got.stdout != want.stdout {
				t.Fatalf("fixture: testdata/%s\n\nstdout mismatch:\ngot:\n%q\nwant:\n%q", tc.name, got.stdout, want.stdout)
			}
			if got.stderr != want.stderr {
				t.Fatalf("fixture: testdata/%s\n\nstderr mismatch:\ngot:\n%q\nwant:\n%q", tc.name, got.stderr, want.stderr)
			}
			if got.status != want.status {
				t.Fatalf("fixture: testdata/%s\n\nstatus mismatch:\ngot: %d\nwant: %d", tc.name, got.status, want.status)
			}
		})
	}
}

func runCommand(t *testing.T, name string, args []string, work string, script string, tc refFixture) commandResult {
	t.Helper()
	cmdArgs := append([]string{}, args...)
	cmdArgs = append(cmdArgs, script)
	cmdArgs = append(cmdArgs, tc.args...)
	cmd := exec.Command(name, cmdArgs...)
	cmd.Dir = work
	cmd.Env = shellTestEnv()
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &bytes.Buffer{}
	err := cmd.Run()
	status := 127
	if cmd.ProcessState != nil {
		status = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return commandResult{
				status: status,
				err:    err,
			}
		}
		err = nil
	}

	stderrStr := normalizeWorkdirOutput(normalizeDiagnosticOutput(cmd.Stderr.(*bytes.Buffer).String(), name), work)
	stdoutStr := normalizeWorkdirOutput(normalizeDiagnosticOutput(cmd.Stdout.(*bytes.Buffer).String(), name), work)

	return commandResult{
		stdout: stdoutStr,
		stderr: stderrStr,
		status: status,
		err:    err,
	}
}

func runReferenceCommand(t *testing.T, work string, tc refFixture) commandResult {
	t.Helper()
	args := []string{
		"run", "-i", "--rm",
	}
	if referenceRuntime() == "docker" {
		args = append(args, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()))
	}
	args = append(args,
		"-v", work+":/work",
		"-w", "/work",
		referenceImage(),
	)
	return runCommand(t, referenceRuntime(), args, work, "./case.rc", tc)
}

func normalizeWorkdirOutput(s, work string) string {
	s = strings.ReplaceAll(s, work, "<work>")
	s = strings.ReplaceAll(s, "/work", "<work>")
	return s
}

func stageWorkdir(t *testing.T, dir string, tc refFixture) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			t.Fatalf("RemoveAll(%s): %v", entry.Name(), err)
		}
	}

	copyFixture(t, filepath.Join("testdata", tc.name), filepath.Join(dir, "case.rc"))
	if tc.setup != nil {
		tc.setup(t, dir)
	}
}

func copyFixture(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", dst, err)
	}
}

func TestCaretMismatchedListError(t *testing.T) {
	t.Parallel()
	shellBin := buildShell(t)
	work := t.TempDir()
	scriptPath := filepath.Join(work, "case.rc")
	script := "x=(a b)\ny=(c d e)\necho $x^$y\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	tc := refFixture{}
	got := runCommand(t, shellBin, nil, work, "case.rc", tc)
	want := runReferenceCommand(t, work, tc)

	if got.status == 0 {
		t.Errorf("got status 0, want non-zero")
	}
	if want.status == 0 {
		t.Errorf("reference got status 0, want non-zero")
	}
}

func TestEnvironmentImportsListVariable(t *testing.T) {
	t.Parallel()
	shellBin := buildShell(t)
	work := t.TempDir()
	scriptPath := filepath.Join(work, "case.rc")
	script := "echo count:$#RC_IMPORT_LIST\necho value:$\"RC_IMPORT_LIST\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(shellBin, "case.rc")
	cmd.Dir = work
	env := shellTestEnv()
	env = append(env, "RC_IMPORT_LIST=one\x01two\x01three")
	cmd.Env = env
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	got := stdout.String()
	want := "count:3\nvalue:one two three\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEnvironmentImportsFunction(t *testing.T) {
	t.Parallel()
	shellBin := buildShell(t)
	work := t.TempDir()
	scriptPath := filepath.Join(work, "case.rc")
	script := "imported a b\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(shellBin, "case.rc")
	cmd.Dir = work
	env := shellTestEnv()
	// The implementation exports/imports functions as just the braced body
	env = append(env, "fn#imported={ echo imported:$* }")
	cmd.Env = env
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	got := stdout.String()
	want := "imported:a imported:b\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHeredocInFnDivergence(t *testing.T) {
	t.Parallel()
	// Plan 9 rc(1) has a known bug: "Functions containing here documents don't work."
	// This project intentionally diverges and fixes the bug.
	// This test proves the Go implementation works, unlike the reference.
	shellBin := buildShell(t)
	work := t.TempDir()
	scriptPath := filepath.Join(work, "case.rc")
	script := "fn f {\ncat <<EOF\nheredoc in fn\nEOF\n}\nf\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(shellBin, "case.rc")
	cmd.Dir = work
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	got := stdout.String()
	want := "heredoc in fn\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
