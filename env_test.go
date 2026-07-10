package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEnvironment(t *testing.T) {
	exe := buildShell(t)

	tests := []struct {
		name   string
		env    []string
		args   []string
		stdout string
	}{
		{
			name:   "scalar import",
			env:    []string{"foo=bar"},
			args:   []string{"-c", "echo $foo"},
			stdout: "bar\n",
		},
		{
			name:   "list import",
			env:    []string{"foo=a\x01b\x01c"},
			args:   []string{"-c", "echo $foo(2)"},
			stdout: "b\n",
		},
		{
			name:   "fn import",
			env:    []string{"fn#myfunc={echo myfunc called}"},
			args:   []string{"-c", "myfunc"},
			stdout: "myfunc called\n",
		},
		{
			name:   "env export list",
			env:    []string{},
			args:   []string{"-c", "foo=(a b c); " + exe + " -c 'whatis foo'"},
			stdout: "foo=(a b c)\n",
		},
		{
			name:   "env export fn",
			env:    []string{},
			args:   []string{"-c", "fn myfunc { echo exported }; " + exe + " -c 'whatis myfunc'"},
			stdout: "fn myfunc {echo exported}\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(exe, tc.args...)
			cmd.Env = append(tc.env, "PATH="+os.Getenv("PATH")) // Need real PATH for finding external exe inside test? The exe is absolute path!
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err != nil {
				t.Logf("err: %v", err)
			}

			if got := stdout.String(); got != tc.stdout {
				t.Errorf("stdout got %q, want %q\nstderr: %s", got, tc.stdout, stderr.String())
			}
		})
	}
}

func TestExportEnv(t *testing.T) {
	e, err := newShellEnv(nil, "")
	if err != nil {
		t.Fatalf("newShellEnv: %v", err)
	}
	e.set("myvar", []string{"a", "b"})
	fnProg, err := ParseSource("{echo}\n")
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	e.fns["myfn"] = funcBody{prog: fnProg, root: fnProg.Root}

	envStrings := e.exportEnv()
	cases := []struct {
		name string
		want func(string) bool
	}{
		{
			name: "exports variable",
			want: func(entry string) bool {
				return entry == "myvar=a\x01b"
			},
		},
		{
			name: "exports function",
			want: func(entry string) bool {
				return strings.HasPrefix(entry, "fn#myfn=")
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			for _, entry := range envStrings {
				if tc.want(entry) {
					return
				}
			}
			t.Fatalf("exported environment missing expected entry: %v", envStrings)
		})
	}
}

func TestExportEnvPrefersShellPath(t *testing.T) {
	e := &shellEnv{
		vars: map[string][]string{
			"PATH": {"/stale/bin:/usr/bin"},
			"path": {"/fresh/bin", "/bin"},
		},
		fns:   map[string]funcBody{},
		jobs:  newJobState(),
		flags: map[string]bool{},
	}

	envStrings := e.exportEnv()
	gotPath := envEntryValue(t, envStrings, "PATH")
	wantPath := strings.Join([]string{"/fresh/bin", "/bin"}, string(os.PathListSeparator))
	if gotPath != wantPath {
		t.Fatalf("exported PATH = %q, want %q", gotPath, wantPath)
	}

	for _, entry := range envStrings {
		if strings.HasPrefix(entry, "path=") {
			t.Fatalf("exported environment should not include raw path entry: %v", envStrings)
		}
	}
}

func TestExportEnvFallsBackToImportedPATH(t *testing.T) {
	e := &shellEnv{
		vars: map[string][]string{
			"PATH": {"/usr/local/bin:/usr/bin"},
		},
		fns:   map[string]funcBody{},
		jobs:  newJobState(),
		flags: map[string]bool{},
	}

	envStrings := e.exportEnv()
	gotPath := envEntryValue(t, envStrings, "PATH")
	wantPath := "/usr/local/bin:/usr/bin"
	if gotPath != wantPath {
		t.Fatalf("exported PATH = %q, want %q", gotPath, wantPath)
	}
}

func TestShellEnvMutationHelpers(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, env *shellEnv)
	}{
		{
			name: "lookup returns copy",
			run: func(t *testing.T, env *shellEnv) {
				t.Helper()
				got := env.lookup("x")
				got[0] = "changed"
				if current := env.lookup("x"); current[0] != "a" {
					t.Fatalf("lookup returned aliased slice: got %v", current)
				}
			},
		},
		{
			name: "clone mutations do not leak",
			run: func(t *testing.T, env *shellEnv) {
				t.Helper()
				clone := env.clone()
				clone.set("x", []string{"clone"})
				clone.flags["x"] = true
				if current := env.lookup("x"); current[0] != "a" {
					t.Fatalf("clone mutation leaked into original vars: %v", current)
				}
				if env.flags["x"] {
					t.Fatal("clone mutation leaked into original flags")
				}
			},
		},
		{
			name: "unset removes variable",
			run: func(t *testing.T, env *shellEnv) {
				t.Helper()
				env.unset("x")
				if current := env.lookup("x"); current != nil {
					t.Fatalf("unset did not remove variable: %v", current)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			env := &shellEnv{
				vars:  map[string][]string{},
				fns:   map[string]funcBody{},
				jobs:  newJobState(),
				flags: map[string]bool{},
			}
			env.set("x", []string{"a", "b"})
			tc.run(t, env)
		})
	}
}

func TestImportEnv(t *testing.T) {
	cases := []struct {
		name    string
		environ []string
		check   func(t *testing.T, env *shellEnv)
	}{
		{
			name:    "key value",
			environ: []string{"KEY=value"},
			check: func(t *testing.T, env *shellEnv) {
				t.Helper()
				if got := env.lookup("KEY"); !reflect.DeepEqual(got, []string{"value"}) {
					t.Fatalf("KEY = %v, want %v", got, []string{"value"})
				}
			},
		},
		{
			name:    "empty value",
			environ: []string{"KEY="},
			check: func(t *testing.T, env *shellEnv) {
				t.Helper()
				if got := env.lookup("KEY"); !reflect.DeepEqual(got, []string{""}) {
					t.Fatalf("KEY = %v, want %v", got, []string{""})
				}
			},
		},
		{
			name:    "value contains equals",
			environ: []string{"KEY=a=b"},
			check: func(t *testing.T, env *shellEnv) {
				t.Helper()
				if got := env.lookup("KEY"); !reflect.DeepEqual(got, []string{"a=b"}) {
					t.Fatalf("KEY = %v, want %v", got, []string{"a=b"})
				}
			},
		},
		{
			name:    "missing equals ignored",
			environ: []string{"NOEQUALS"},
			check: func(t *testing.T, env *shellEnv) {
				t.Helper()
				if got := env.lookup("NOEQUALS"); got != nil {
					t.Fatalf("NOEQUALS = %v, want nil", got)
				}
			},
		},
		{
			name:    "empty key allowed",
			environ: []string{"=value"},
			check: func(t *testing.T, env *shellEnv) {
				t.Helper()
				if got := env.lookup(""); !reflect.DeepEqual(got, []string{"value"}) {
					t.Fatalf("empty key = %v, want %v", got, []string{"value"})
				}
			},
		},
		{
			name:    "function import",
			environ: []string{"fn#myfunc={echo imported}"},
			check: func(t *testing.T, env *shellEnv) {
				t.Helper()
				body, ok := env.lookupFunc("myfunc")
				if !ok || body.prog == nil || body.root < 0 {
					t.Fatal("expected imported function")
				}
				if got := FormatTree(body.prog, body.root); got != "{echo imported}" {
					t.Fatalf("FormatTree(body) = %q, want %q", got, "{echo imported}")
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			env := &shellEnv{
				vars:  map[string][]string{},
				fns:   map[string]funcBody{},
				jobs:  newJobState(),
				flags: map[string]bool{},
			}
			env.importEnv(tc.environ)
			tc.check(t, env)
		})
	}
}

func TestFilepathList(t *testing.T) {
	sep := string(os.PathListSeparator)
	cases := []struct {
		name string
		path string
		want []string
	}{
		{name: "empty", path: "", want: nil},
		{name: "single", path: "/bin", want: []string{"/bin"}},
		{name: "multiple", path: strings.Join([]string{"/bin", "/usr/bin", "."}, sep), want: filepath.SplitList(strings.Join([]string{"/bin", "/usr/bin", "."}, sep))},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := filepathList(tc.path); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("filepathList(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestVariableHelpers(t *testing.T) {
	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "keyword lookup",
			check: func(t *testing.T) {
				t.Helper()
				if got := Klook("if"); got.Type != tokenIf {
					t.Fatalf("Klook(%q) = type %s, want IF", "if", tokenName(got.Type))
				}
				if got := Klook("iffy"); got.Type != tokenWord {
					t.Fatalf("Klook(%q) = type %s, want WORD", "iffy", tokenName(got.Type))
				}
			},
		},
		{
			name: "scope lookup and set var",
			check: func(t *testing.T) {
				t.Helper()
				scope := NewScope()
				scope.SetVar("status", NewWord("", nil))
				if got := List2Str(scope.GVlook("status").Val); got != "" {
					t.Fatalf("global status = %q, want empty string", got)
				}
				scope.Local = &Var{Name: "status", Val: NewWord("local", nil)}
				if got := List2Str(scope.Vlook("status").Val); got != "local" {
					t.Fatalf("local status = %q, want %q", got, "local")
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t)
		})
	}
}

func TestCountAndList2Str(t *testing.T) {
	cases := []struct {
		name string
		word *Word
		want int
		text string
	}{
		{name: "nil", want: 0, text: ""},
		{name: "single", word: makeWordChain("one"), want: 1, text: "one"},
		{name: "multiple", word: makeWordChain("one", "two", "three"), want: 3, text: "one two three"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := Count(tc.word); got != tc.want {
				t.Fatalf("Count() = %d, want %d", got, tc.want)
			}
			if got := List2Str(tc.word); got != tc.text {
				t.Fatalf("List2Str() = %q, want %q", got, tc.text)
			}
		})
	}
}

func TestCopyWords(t *testing.T) {
	tail := makeWordChain("tail")
	cases := []struct {
		name string
		src  *Word
		tail *Word
		want []string
	}{
		{name: "nil source returns tail", tail: tail, want: []string{"tail"}},
		{name: "single word", src: makeWordChain("one"), want: []string{"one"}},
		{name: "multiple with tail", src: makeWordChain("one", "two"), tail: tail, want: []string{"one", "two", "tail"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := CopyWords(tc.src, tc.tail)
			if words := wordSlice(got); !reflect.DeepEqual(words, tc.want) {
				t.Fatalf("CopyWords() = %v, want %v", words, tc.want)
			}
		})
	}
}

func TestCopyWordsDoesNotAliasSource(t *testing.T) {
	src := makeWordChain("one", "two")
	got := CopyWords(src, nil)
	src.Word = "changed"
	src.Next.Word = "changed-too"

	if words := wordSlice(got); !reflect.DeepEqual(words, []string{"one", "two"}) {
		t.Fatalf("copied words changed with source mutation: got %v", words)
	}
}

func makeWordChain(words ...string) *Word {
	var head *Word
	for i := len(words) - 1; i >= 0; i-- {
		head = NewWord(words[i], head)
	}
	return head
}

func wordSlice(word *Word) []string {
	var out []string
	for ; word != nil; word = word.Next {
		out = append(out, word.Word)
	}
	return out
}

func envEntryValue(t *testing.T, env []string, name string) string {
	t.Helper()

	prefix := name + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}

	t.Fatalf("environment missing %s entry: %v", name, env)
	return ""
}

func BenchmarkEnvGetSet(b *testing.B) {
	env, _ := newShellEnv(nil, "")
	env.set("test_var", []string{"value1", "value2"})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		env.lookup("test_var")
		env.set("test_var2", []string{"value"})
	}
}
