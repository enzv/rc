package main

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestIsTrueStatus(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   bool
	}{
		{name: "empty", status: "", want: true},
		{name: "zero", status: "0", want: true},
		{name: "pipeline zero", status: "0|0", want: true},
		{name: "bare pipes", status: "||", want: true},
		{name: "numeric failure", status: "1", want: false},
		{name: "text failure", status: "false", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := isTrueStatus(tc.status); got != tc.want {
				t.Fatalf("isTrueStatus(%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestStatusCode(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   int
	}{
		{name: "empty success", status: "", want: 0},
		{name: "zero", status: "0", want: 0},
		{name: "numeric", status: "7", want: 7},
		{name: "pipeline failure string", status: "1|", want: 1},
		{name: "text failure", status: "false", want: 1},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := statusCode(tc.status); got != tc.want {
				t.Fatalf("statusCode(%q) = %d, want %d", tc.status, got, tc.want)
			}
		})
	}
}

func TestRunnerExitCode(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   int
	}{
		{name: "empty", status: "", want: 0},
		{name: "numeric", status: "9", want: 9},
		{name: "pipeline keeps leftmost", status: "3|0", want: 3},
		{name: "text failure", status: "failed", want: 1},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := &runner{env: &shellEnv{vars: map[string][]string{"status": {tc.status}}}}
			if got := r.exitCode(); got != tc.want {
				t.Fatalf("exitCode(%q) = %d, want %d", tc.status, got, tc.want)
			}
		})
	}
}

func TestShouldForeground(t *testing.T) {
	tempFile := func(t *testing.T) *os.File {
		t.Helper()
		file, err := os.CreateTemp(t.TempDir(), "stdin-*")
		if err != nil {
			t.Fatalf("CreateTemp: %v", err)
		}
		t.Cleanup(func() { _ = file.Close() })
		return file
	}

	cases := []struct {
		name string
		r    *runner
		want bool
	}{
		{
			name: "noninteractive",
			r:    &runner{env: &shellEnv{flags: map[string]bool{}}, stdin: strings.NewReader("x")},
			want: false,
		},
		{
			name: "pipe reader",
			r:    &runner{env: &shellEnv{flags: map[string]bool{"i": true}}, stdin: strings.NewReader("x")},
			want: false,
		},
		{
			name: "regular file",
			r:    &runner{env: &shellEnv{flags: map[string]bool{"i": true}}, stdin: tempFile(t)},
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.shouldForeground(); got != tc.want {
				t.Fatalf("shouldForeground() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSplitByIFS(t *testing.T) {
	cases := []struct {
		name   string
		ifs    []string
		output string
		want   []string
	}{
		{name: "default ifs", output: "a b\tc\n", want: []string{"a", "b", "c"}},
		{name: "custom ifs", ifs: []string{":"}, output: "a:b:c", want: []string{"a", "b", "c"}},
		{name: "empty list disables split", ifs: []string{}, output: "a b c", want: []string{"a b c"}},
		{name: "empty output with empty ifs", ifs: []string{}, output: "", want: nil},
		{name: "consecutive separators collapse", output: "a  \t \nb", want: []string{"a", "b"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			env := &shellEnv{vars: map[string][]string{}}
			if tc.ifs != nil {
				env.vars["ifs"] = tc.ifs
			}
			if got := splitByIFS(tc.output, env); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("splitByIFS(%q) = %v, want %v", tc.output, got, tc.want)
			}
		})
	}
}

func TestLookupVar(t *testing.T) {
	r := &runner{env: &shellEnv{vars: map[string][]string{
		"*": {"first", "second"},
		"x": {"value"},
	}}}

	cases := []struct {
		name string
		key  string
		want []string
	}{
		{name: "empty", key: "", want: nil},
		{name: "named", key: "x", want: []string{"value"}},
		{name: "positional", key: "2", want: []string{"second"}},
		{name: "out of range positional", key: "3", want: nil},
		{name: "zero positional", key: "0", want: nil},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := r.lookupVar(tc.key); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("lookupVar(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

func TestConcatWords(t *testing.T) {
	cases := []struct {
		name    string
		left    []string
		right   []string
		want    []string
		wantErr string
	}{
		{name: "same length", left: []string{"a", "b"}, right: []string{"1", "2"}, want: []string{"a1", "b2"}},
		{name: "left scalar", left: []string{"a"}, right: []string{"1", "2"}, want: []string{"a1", "a2"}},
		{name: "right scalar", left: []string{"a", "b"}, right: []string{"1"}, want: []string{"a1", "b1"}},
		{name: "null list", left: nil, right: []string{"1"}, wantErr: "null list in concatenation"},
		{name: "mismatched lengths", left: []string{"a", "b"}, right: []string{"1", "2", "3"}, wantErr: "mismatched list lengths in concatenation"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := concatWords(tc.left, tc.right)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("concatWords(%v, %v) error = %v, want %q", tc.left, tc.right, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("concatWords(%v, %v) unexpected error: %v", tc.left, tc.right, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("concatWords(%v, %v) = %v, want %v", tc.left, tc.right, got, tc.want)
			}
		})
	}
}

func BenchmarkRunFor(b *testing.B) {
	prog, _ := ParseSource(`for (i in 1 2 3 4 5 6 7 8 9 10) { x = $i }`)
	env, _ := newShellEnv(nil, "")
	r := &runner{env: env, ctx: context.Background()}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.exec(prog.Root)
	}
}

func BenchmarkExpandWords(b *testing.B) {
	prog, _ := ParseSource(`_out = ( $x $y $z )`)
	env, _ := newShellEnv(nil, "")
	env.set("x", []string{"1", "2", "3"})
	env.set("y", []string{"4", "5", "6"})
	env.set("z", []string{"7", "8", "9"})
	r := &runner{env: env, ctx: context.Background()}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.exec(prog.Root)
	}
}

func BenchmarkSearchPath(b *testing.B) {
	env, _ := newShellEnv(nil, "")
	env.set("path", []string{"/bin", "/usr/bin", "/usr/local/bin"})
	r := &runner{env: env, ctx: context.Background()}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.searchPath("ls")
	}
}
