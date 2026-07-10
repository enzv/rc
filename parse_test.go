package main

import (
	"fmt"
	"os"
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
			if got := FormatTree(prog, prog.Root); got != tc.want {
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
		check    func(t *testing.T, prog *Program, root int)
	}{
		{
			name:     "redir write",
			src:      "a >x\n",
			rootType: tokenRedir,
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				node := prog.Node(root)
				child := prog.Node(node.Child[0])
				childStr := ""
				if child != nil {
					if tok := prog.Token(child.Tok); tok != nil {
						childStr = tok.Text
					}
				}
				if node.RType != redirWrite || node.FD0 != 1 || child == nil || childStr != "x" {
					t.Fatalf("unexpected redir node: rtype=%d fd0=%d child0=%q", node.RType, node.FD0, childStr)
				}
			},
		},
		{
			name:     "redir append",
			src:      "a >>x\n",
			rootType: tokenRedir,
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				if prog.Node(root).RType != redirAppend {
					t.Fatalf("append redirection rtype=%d, want %d", prog.Node(root).RType, redirAppend)
				}
			},
		},
		{
			name:     "redir read",
			src:      "a <x\n",
			rootType: tokenRedir,
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				node := prog.Node(root)
				if node.RType != redirRead || node.FD0 != 0 {
					t.Fatalf("read redirection rtype=%d fd0=%d", node.RType, node.FD0)
				}
			},
		},
		{
			name:     "redir readwrite",
			src:      "a <>x\n",
			rootType: tokenRedir,
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				if prog.Node(root).RType != redirRDWR {
					t.Fatalf("readwrite redirection rtype=%d, want %d", prog.Node(root).RType, redirRDWR)
				}
			},
		},
		{
			name:     "descriptor target",
			src:      "a >[2]x\n",
			rootType: tokenRedir,
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				if prog.Node(root).FD0 != 2 {
					t.Fatalf("fd-qualified redirection fd0=%d, want 2", prog.Node(root).FD0)
				}
			},
		},
		{
			name:     "descriptor dup",
			src:      "a >[2=1]\n",
			rootType: tokenDup,
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				node := prog.Node(root)
				if node.RType != redirDupFD || node.FD1 != 2 || node.FD0 != 1 {
					t.Fatalf("dup node mismatch: rtype=%d fd1=%d fd0=%d", node.RType, node.FD1, node.FD0)
				}
			},
		},
		{
			name:     "descriptor close",
			src:      "a >[2=]\n",
			rootType: tokenDup,
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				node := prog.Node(root)
				if node.RType != redirClose || node.FD0 != 2 {
					t.Fatalf("close node mismatch: rtype=%d fd0=%d", node.RType, node.FD0)
				}
			},
		},
		{
			name:     "pipe fd",
			src:      "a |[2] b\n",
			rootType: tokenPipe,
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				node := prog.Node(root)
				if node.FD0 != 2 || node.FD1 != 0 {
					t.Fatalf("pipe fd mismatch: fd0=%d fd1=%d", node.FD0, node.FD1)
				}
			},
		},
		{
			name:     "pipe fd pair",
			src:      "a |[2=1] b\n",
			rootType: tokenPipe,
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				node := prog.Node(root)
				if node.FD0 != 1 || node.FD1 != 2 {
					t.Fatalf("pipe fd pair mismatch: fd0=%d fd1=%d", node.FD0, node.FD1)
				}
			},
		},
		{
			name:     "bang precedence",
			src:      "! x | y | z\n",
			rootType: tokenBang,
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				if childType(prog, root, 0) != tokenPipe {
					t.Fatalf("bang child type=%v, want PIPE", childType(prog, root, 0))
				}
			},
		},
		{
			name:     "subshell precedence",
			src:      "x | @ y | z\n",
			rootType: tokenPipe,
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				if childType(prog, root, 1) != tokenSubshell {
					t.Fatalf("pipe rhs type=%v, want SUBSHELL", childType(prog, root, 1))
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
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				if prog.Node(root).Child[1] >= 0 {
					t.Fatalf("fn deletion should not have body")
				}
			},
		},
		{
			name:     "assignment command chain",
			src:      "x=y z=w echo ok\n",
			rootType: '=',
			check: func(t *testing.T, prog *Program, root int) {
				t.Helper()
				if childType(prog, root, 2) != '=' {
					t.Fatalf("expected chained assignment, child2 type=%v", childType(prog, root, 2))
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := parseProgram(t, tc.src)
			root := prog.Root
			if root < 0 {
				t.Fatal("ParseSource returned nil root")
			}
			if prog.Node(root).Type != tc.rootType {
				t.Fatalf("root type = %s, want %s", tokenName(prog.Node(root).Type), tokenName(tc.rootType))
			}
			if tc.check != nil {
				tc.check(t, prog, root)
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

func childType(prog *Program, id int, index int) NodeType {
	node := prog.Node(id)
	if node == nil || node.Child[index] < 0 {
		return 0
	}
	child := prog.Node(node.Child[index])
	if child == nil {
		return 0
	}
	return child.Type
}

func TestParseCorpus(t *testing.T) {
	runRCCases(t, parseCorpusFixtures())
}

func parseCorpusFixtures() []rcCase {
	return []rcCase{
		{file: "syntax_comments_continuation.rc", parseOnly: true},
		{file: "words_quotes.rc", parseOnly: true},
		{file: "list_flattening.rc", parseOnly: true},
		{file: "var_assignment_scalar_list.rc", parseOnly: true},
		{file: "glob_basic.rc", parseOnly: true},
	}
}

type sourceSnippet interface {
	writeTo(*strings.Builder)
}

type lineSnippet struct {
	text string
}

func (s lineSnippet) writeTo(b *strings.Builder) {
	b.WriteString(s.text)
	b.WriteByte('\n')
}

type blockSnippet struct {
	head string
	body []sourceSnippet
}

func (s blockSnippet) writeTo(b *strings.Builder) {
	b.WriteString(s.head)
	b.WriteByte('\n')
	for _, snippet := range s.body {
		snippet.writeTo(b)
	}
	b.WriteString("}\n")
}

type switchCase struct {
	pattern string
	body    []sourceSnippet
}

type switchSnippet struct {
	subject string
	cases   []switchCase
}

func (s switchSnippet) writeTo(b *strings.Builder) {
	b.WriteString("switch(")
	b.WriteString(s.subject)
	b.WriteString(") {\n")
	for _, c := range s.cases {
		b.WriteString("case ")
		b.WriteString(c.pattern)
		b.WriteByte('\n')
		for _, snippet := range c.body {
			snippet.writeTo(b)
		}
	}
	b.WriteString("}\n")
}

type heredocSnippet struct {
	command string
	tag     string
	quoted  bool
	body    []string
}

func (s heredocSnippet) writeTo(b *strings.Builder) {
	b.WriteString(s.command)
	b.WriteString(" <<")
	if s.quoted {
		b.WriteByte('\'')
		b.WriteString(s.tag)
		b.WriteString("'\n")
	} else {
		b.WriteString(s.tag)
		b.WriteByte('\n')
	}
	for _, line := range s.body {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(s.tag)
	b.WriteByte('\n')
}

type benchmarkCase struct {
	name  string
	build func(int) []sourceSnippet
}

type benchmarkSize struct {
	name        string
	targetBytes int
}

type benchmarkInput struct {
	source   string
	prepared preparedSource
	tokens   []LexToken
	prog     *Program
}

func BenchmarkParseTokens(b *testing.B) {
	runCorpusBenchmark(b, defaultBenchmarkCases(), func(b *testing.B, input benchmarkInput) {
		input.prepared = preparedSource{}
		input.prog = nil
		b.SetBytes(int64(len(input.source)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = parseTokens(input.tokens)
		}
	})
}

func BenchmarkAttachHereDocs(b *testing.B) {
	if !benchStressEnabled() {
		b.Skip("stress benchmark")
	}
	cases := stressBenchmarkCases()
	runCorpusBenchmark(b, cases, func(b *testing.B, input benchmarkInput) {
		b.SetBytes(int64(len(input.source)))
		input.source = ""
		input.tokens = nil
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			attachHereDocs(input.prog, input.prepared.HereDocs)
		}
	})
}

func BenchmarkParseSource(b *testing.B) {
	runCorpusBenchmark(b, defaultBenchmarkCases(), func(b *testing.B, input benchmarkInput) {
		input.prepared = preparedSource{}
		input.tokens = nil
		input.prog = nil
		b.SetBytes(int64(len(input.source)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = ParseSource(input.source)
		}
	})
}

func BenchmarkParseCorpusStress(b *testing.B) {
	if !benchStressEnabled() {
		b.Skip("stress benchmark")
	}
	src, err := os.ReadFile("testdata/parser_stress.rc")
	if err != nil {
		b.Fatalf("ReadFile(testdata/parser_stress.rc): %v", err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseSource(string(src)); err != nil {
			b.Fatalf("ParseSource(parser_stress.rc): %v", err)
		}
	}
}

func defaultBenchmarkCases() []benchmarkCase {
	return []benchmarkCase{
		{name: "flat", build: buildFlatChunk},
		{name: "control", build: buildControlChunk},
	}
}

func stressBenchmarkCases() []benchmarkCase {
	return []benchmarkCase{
		{name: "reparse", build: buildReparseChunk},
		{name: "mixed", build: buildMixedChunk},
	}
}

func runCorpusBenchmark(b *testing.B, cases []benchmarkCase, run func(*testing.B, benchmarkInput)) {
	for _, tc := range cases {
		tc := tc
		for _, size := range benchmarkSizes() {
			size := size
			b.Run(tc.name+"/"+size.name, func(b *testing.B) {
				input := buildBenchmarkInput(b, size.targetBytes, tc.build)
				run(b, input)
			})
		}
	}
}

func benchmarkSizes() []benchmarkSize {
	sizes := []benchmarkSize{
		{name: "small", targetBytes: 1 << 7},
		{name: "medium", targetBytes: 1 << 10},
	}
	if benchStressEnabled() {
		sizes = append(sizes,
			benchmarkSize{name: "large", targetBytes: 1 << 12},
			benchmarkSize{name: "mega", targetBytes: 1 << 14},
			benchmarkSize{name: "giant", targetBytes: 1 << 16},
		)
	}
	return sizes
}

func benchStressEnabled() bool {
	v := os.Getenv("RC_BENCH_STRESS")
	return v == "1" || v == "true" || v == "yes"
}

func buildBenchmarkInput(tb testing.TB, targetBytes int, makeChunk func(int) []sourceSnippet) benchmarkInput {
	tb.Helper()
	source := buildBenchmarkSource(targetBytes, makeChunk)
	prepared := prepareSource(source)
	tokens, err := Lex(prepared.Stripped)
	if err != nil {
		tb.Fatalf("Lex(prepared source) returned error: %v", err)
	}
	prog, err := parseTokens(tokens)
	if err != nil {
		tb.Fatalf("parseTokens precheck failed: %v", err)
	}
	return benchmarkInput{
		source:   source,
		prepared: prepared,
		tokens:   tokens,
		prog:     prog,
	}
}

func buildBenchmarkSource(targetBytes int, makeChunk func(int) []sourceSnippet) string {
	var b strings.Builder
	b.Grow(targetBytes)
	for i := 0; b.Len() < targetBytes; i++ {
		for _, snippet := range makeChunk(i) {
			snippet.writeTo(&b)
		}
	}
	return b.String()
}

func buildFlatChunk(i int) []sourceSnippet {
	return []sourceSnippet{
		lineSnippet{
			text: fmt.Sprintf("x%d=alpha%d y%d=beta%d echo flat:%d $x%d^$y%d >flat-%d.out", i, i, i, i+1, i, i, i, i),
		},
		lineSnippet{
			text: fmt.Sprintf("cc -O2 -o flat%d src%d.c", i, i),
		},
		lineSnippet{
			text: fmt.Sprintf("echo flat:%d | cat |[2] tr a-z A-Z", i),
		},
		lineSnippet{
			text: fmt.Sprintf("if(~ $status 0) echo flat:%d:ok", i),
		},
		lineSnippet{
			text: fmt.Sprintf("for(item in red blue green) echo flat:%d:$item", i),
		},
	}
}

func buildControlChunk(i int) []sourceSnippet {
	return []sourceSnippet{
		blockSnippet{
			head: fmt.Sprintf("fn worker%d {", i),
			body: []sourceSnippet{
				lineSnippet{
					text: fmt.Sprintf("if(~ $state%d ready) echo control:%d:ready", i, i),
				},
				blockSnippet{
					head: "for(item in red blue green) {",
					body: []sourceSnippet{
						switchSnippet{
							subject: fmt.Sprintf("$kind%d", i),
							cases: []switchCase{
								{
									pattern: "alpha",
									body: []sourceSnippet{
										lineSnippet{
											text: fmt.Sprintf("echo control:%d:alpha", i),
										},
									},
								},
								{
									pattern: "beta",
									body: []sourceSnippet{
										blockSnippet{
											head: fmt.Sprintf("if(~ $toggle%d on) {", i),
											body: []sourceSnippet{
												lineSnippet{
													text: fmt.Sprintf("echo control:%d:beta", i),
												},
											},
										},
									},
								},
								{
									pattern: "*",
									body: []sourceSnippet{
										lineSnippet{
											text: fmt.Sprintf("echo control:%d:other", i),
										},
									},
								},
							},
						},
						lineSnippet{
							text: fmt.Sprintf("echo control:%d:after-switch", i),
						},
					},
				},
				lineSnippet{
					text: fmt.Sprintf("while(~ $loop%d again) echo control:%d:loop", i, i),
				},
				blockSnippet{
					head: "@{",
					body: []sourceSnippet{
						lineSnippet{
							text: fmt.Sprintf("echo control:%d:subshell", i),
						},
					},
				},
			},
		},
		lineSnippet{
			text: fmt.Sprintf("worker%d", i),
		},
	}
}

func buildReparseChunk(i int) []sourceSnippet {
	return []sourceSnippet{
		lineSnippet{
			text: fmt.Sprintf("CODE='echo reparse:%d; if(~ alpha a*) echo reparse:%d:if; switch(beta){case beta echo reparse:%d:beta; case * echo reparse:%d:miss}'", i, i, i, i),
		},
		lineSnippet{
			text: "eval $CODE",
		},
		lineSnippet{
			text: fmt.Sprintf(". ./stage%d.rc arg%d", i, i),
		},
		heredocSnippet{
			command: "cat",
			tag:     fmt.Sprintf("DOC%d", i),
			quoted:  i%2 == 0,
			body: []string{
				fmt.Sprintf("reparse body %d", i),
				fmt.Sprintf("literal $x%d^$y%d", i, i),
				fmt.Sprintf("pair %d %d", i, i+1),
			},
		},
		lineSnippet{
			text: fmt.Sprintf("cmp <{left%d} <{right%d}", i, i),
		},
	}
}

func buildMixedChunk(i int) []sourceSnippet {
	return []sourceSnippet{
		blockSnippet{
			head: fmt.Sprintf("fn mix%d {", i),
			body: []sourceSnippet{
				lineSnippet{
					text: fmt.Sprintf("x%d=alpha%d y%d=beta%d echo mixed:%d $x%d^$y%d >mix-%d.out", i, i, i, i+1, i, i, i, i),
				},
				switchSnippet{
					subject: fmt.Sprintf("$mode%d", i),
					cases: []switchCase{
						{
							pattern: "alpha",
							body: []sourceSnippet{
								lineSnippet{
									text: fmt.Sprintf("echo mixed:%d:alpha", i),
								},
							},
						},
						{
							pattern: "beta",
							body: []sourceSnippet{
								lineSnippet{
									text: fmt.Sprintf("if(~ $gate%d open) echo mixed:%d:beta", i, i),
								},
							},
						},
						{
							pattern: "*",
							body: []sourceSnippet{
								lineSnippet{
									text: fmt.Sprintf("echo mixed:%d:other", i),
								},
							},
						},
					},
				},
				heredocSnippet{
					command: "cat",
					tag:     fmt.Sprintf("MIX%d", i),
					quoted:  false,
					body: []string{
						fmt.Sprintf("mixed body %d", i),
						fmt.Sprintf("$x%d^$y%d literal", i, i),
					},
				},
				lineSnippet{
					text: fmt.Sprintf("CODE='echo mixed:%d:eval; if(~ alpha a*) echo mixed:%d:yes; switch(beta){case beta echo mixed:%d:beta; case * echo mixed:%d:miss}'", i, i, i, i),
				},
				lineSnippet{
					text: "eval $CODE",
				},
				blockSnippet{
					head: "@{",
					body: []sourceSnippet{
						lineSnippet{
							text: fmt.Sprintf("echo mixed:%d:subshell", i),
						},
					},
				},
			},
		},
		lineSnippet{
			text: fmt.Sprintf("mix%d", i),
		},
	}
}
