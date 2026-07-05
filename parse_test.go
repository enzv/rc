package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestParserFormatCases(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{name: "simple command", src: "echo hi\n", want: "echo hi"},
		{name: "quoted words", src: "echo 'a b'\n", want: "echo 'a b'"},
		{name: "doubled quote", src: "echo 'a''b'\n", want: "echo 'a''b'"},
		{name: "comments", src: "a # comment\nb\n", want: "a\nb"},
		{name: "continuation", src: "echo a\\\n b\n", want: "echo a b"},
		{name: "free caret suffix", src: "cc -$flags $stem.c\n", want: "cc -^$flags $stem^.c"},
		{name: "free caret quote", src: "echo a'b'\n", want: "echo a^'b'"},
		{name: "list literal", src: "echo (a b c)\n", want: "echo (a b c)"},
		{name: "variable reference", src: "echo $x\n", want: "echo $x"},
		{name: "variable count", src: "echo $#x\n", want: "echo $#x"},
		{name: "variable join", src: "echo $\"x\n", want: "echo $\"x"},
		{name: "positional variable", src: "echo $1\n", want: "echo $1"},
		{name: "subscript single", src: "echo $x(1)\n", want: "echo $x(1)"},
		{name: "subscript range", src: "echo $x(1-3)\n", want: "echo $x(1-3)"},
		{name: "concatenation", src: "echo a^b^c\n", want: "echo a^b^c"},
		{name: "pipeline", src: "a | b\n", want: "a|b"},
		{name: "logical and or", src: "a && b || c\n", want: "a && b || c"},
		{name: "if", src: "if(x) y\n", want: "if(x)y"},
		{name: "if not", src: "if not z\n", want: "if not z"},
		{name: "for in", src: "for(i in a b) echo $i\n", want: "for(i in a b)echo $i"},
		{name: "for implicit", src: "for(i) echo $i\n", want: "for(i)echo $i"},
		{name: "while", src: "while(x) y\n", want: "while (x)y"},
		{name: "switch", src: "switch x {case a echo b}\n", want: "switch x {case a echo b}"},
		{name: "fn definition", src: "fn f { echo ok }\n", want: "fn f {echo ok}"},
		{name: "fn deletion", src: "fn f\n", want: "fn f"},
		{name: "assignment", src: "x=y\n", want: "x=y"},
		{name: "temporary assignment", src: "x=y echo ok\n", want: "x=y echo ok"},
		{name: "heredoc syntax", src: "cat <<EOF\nhello\nEOF\necho ok\n", want: "<<EOF cat\necho ok"},
		{name: "process substitution read", src: "echo <{a;b}\n", want: "echo <{a\nb}"},
		{name: "process substitution write", src: "echo >{a;b}\n", want: "echo >{a\nb}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := parseProgram(t, tc.src)
			if got := FormatTree(prog.Root); got != tc.want {
				t.Fatalf("FormatTree mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestParserNodeShapes(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		rootType NodeType
		check    func(t *testing.T, root *Tree)
	}{
		{
			name:     "redir write",
			src:      "a >x\n",
			rootType: tokenRedir,
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.RType != redirWrite || root.FD0 != 1 || root.Child[0].Str != "x" {
					t.Fatalf("unexpected redir node: rtype=%d fd0=%d child0=%q", root.RType, root.FD0, root.Child[0].Str)
				}
			},
		},
		{
			name:     "redir append",
			src:      "a >>x\n",
			rootType: tokenRedir,
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.RType != redirAppend {
					t.Fatalf("append redirection rtype=%d, want %d", root.RType, redirAppend)
				}
			},
		},
		{
			name:     "redir read",
			src:      "a <x\n",
			rootType: tokenRedir,
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.RType != redirRead || root.FD0 != 0 {
					t.Fatalf("read redirection rtype=%d fd0=%d", root.RType, root.FD0)
				}
			},
		},
		{
			name:     "redir readwrite",
			src:      "a <>x\n",
			rootType: tokenRedir,
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.RType != redirRDWR {
					t.Fatalf("readwrite redirection rtype=%d, want %d", root.RType, redirRDWR)
				}
			},
		},
		{
			name:     "descriptor target",
			src:      "a >[2]x\n",
			rootType: tokenRedir,
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.FD0 != 2 {
					t.Fatalf("fd-qualified redirection fd0=%d, want 2", root.FD0)
				}
			},
		},
		{
			name:     "descriptor dup",
			src:      "a >[2=1]\n",
			rootType: tokenDup,
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.RType != redirDupFD || root.FD1 != 2 || root.FD0 != 1 {
					t.Fatalf("dup node mismatch: rtype=%d fd1=%d fd0=%d", root.RType, root.FD1, root.FD0)
				}
			},
		},
		{
			name:     "descriptor close",
			src:      "a >[2=]\n",
			rootType: tokenDup,
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.RType != redirClose || root.FD0 != 2 {
					t.Fatalf("close node mismatch: rtype=%d fd0=%d", root.RType, root.FD0)
				}
			},
		},
		{
			name:     "pipe fd",
			src:      "a |[2] b\n",
			rootType: tokenPipe,
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.FD0 != 2 || root.FD1 != 0 {
					t.Fatalf("pipe fd mismatch: fd0=%d fd1=%d", root.FD0, root.FD1)
				}
			},
		},
		{
			name:     "pipe fd pair",
			src:      "a |[2=1] b\n",
			rootType: tokenPipe,
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.FD0 != 1 || root.FD1 != 2 {
					t.Fatalf("pipe fd pair mismatch: fd0=%d fd1=%d", root.FD0, root.FD1)
				}
			},
		},
		{
			name:     "bang precedence",
			src:      "! x | y | z\n",
			rootType: tokenBang,
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.Child[0] == nil || root.Child[0].Type != tokenPipe {
					t.Fatalf("bang child type=%v, want PIPE", childType(root, 0))
				}
			},
		},
		{
			name:     "subshell precedence",
			src:      "x | @ y | z\n",
			rootType: tokenPipe,
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.Child[1] == nil || root.Child[1].Type != tokenSubshell {
					t.Fatalf("pipe rhs type=%v, want SUBSHELL", childType(root, 1))
				}
			},
		},
		{
			name:     "switch root",
			src:      "switch x {case a echo b}\n",
			rootType: tokenSwitch,
		},
		{
			name:     "fn delete root",
			src:      "fn f\n",
			rootType: tokenFn,
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.Child[1] != nil {
					t.Fatalf("fn deletion should not have body")
				}
			},
		},
		{
			name:     "assignment command chain",
			src:      "x=y z=w echo ok\n",
			rootType: '=',
			check: func(t *testing.T, root *Tree) {
				t.Helper()
				if root.Child[2] == nil || root.Child[2].Type != '=' {
					t.Fatalf("expected chained assignment, child2 type=%v", childType(root, 2))
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := parseProgram(t, tc.src).Root
			if root == nil {
				t.Fatal("ParseSource returned nil root")
			}
			if root.Type != tc.rootType {
				t.Fatalf("root type = %s, want %s", tokenName(root.Type), tokenName(tc.rootType))
			}
			if tc.check != nil {
				tc.check(t, root)
			}
		})
	}
}

func parseProgram(t *testing.T, src string) *Program {
	t.Helper()
	prog, err := ParseSource(src)
	if err != nil {
		t.Fatalf("ParseSource(%q) returned error: %v", src, err)
	}
	return prog
}

func childType(root *Tree, index int) NodeType {
	if root == nil || root.Child[index] == nil {
		return 0
	}
	return root.Child[index].Type
}

func TestParseCorpus(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("ReadDir(testdata): %v", err)
	}
	var cases []rcCase
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("testdata contains nested directory: %s", entry.Name())
		}
		if !strings.HasSuffix(entry.Name(), ".rc") {
			t.Fatalf("testdata contains non-.rc file: %s", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join("testdata", entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(testdata/%s): %v", entry.Name(), err)
		}
		if strings.HasPrefix(string(data), "#!") {
			t.Fatalf("testdata fixture has shebang: %s", entry.Name())
		}
		cases = append(cases, rcCase{
			name:      strings.TrimSuffix(entry.Name(), ".rc"),
			file:      entry.Name(),
			parseOnly: true,
		})
	}
	if len(cases) != 93 {
		t.Fatalf("testdata fixture count = %d, want 93", len(cases))
	}
	sort.Slice(cases, func(i, j int) bool {
		return cases[i].file < cases[j].file
	})
	runRCCases(t, cases)
}

func BenchmarkParseSource(b *testing.B) {
	src := `
fn build {
	echo building...
	cc -O2 -o $1 $2.c
	if (~ $status 0) {
		echo success
	}
}
build prog main
`
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = ParseSource(src)
	}
}
