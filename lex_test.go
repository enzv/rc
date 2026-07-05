package main

import (
	"strings"
	"testing"
)

func TestLexCases(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		check func(t *testing.T, tokens []LexToken)
	}{
		{
			name: "free carets",
			src:  "cc -$flags $stem.c",
			check: func(t *testing.T, tokens []LexToken) {
				t.Helper()
				var got []string
				for _, tok := range tokens {
					if tok.Kind == tokenEOF {
						break
					}
					got = append(got, tok.Text)
				}
				want := []string{"cc", "-", "^", "$", "flags", "$", "stem", "^", ".c"}
				if strings.Join(got, "|") != strings.Join(want, "|") {
					t.Fatalf("free caret tokenization mismatch\ngot:  %q\nwant: %q", got, want)
				}
			},
		},
		{
			name: "quoted word",
			src:  "a'b''c' d",
			check: func(t *testing.T, tokens []LexToken) {
				t.Helper()
				gotKinds := []NodeType{tokens[0].Kind, tokens[1].Kind, tokens[2].Kind, tokens[3].Kind}
				wantKinds := []NodeType{tokenWord, '^', tokenWord, tokenWord}
				for i := range wantKinds {
					if gotKinds[i] != wantKinds[i] {
						t.Fatalf("token %d kind = %s, want %s", i, tokenName(gotKinds[i]), tokenName(wantKinds[i]))
					}
				}
				if !tokens[2].Quoted || tokens[2].Text != "b'c" {
					t.Fatalf("quoted token mismatch: quoted=%v text=%q", tokens[2].Quoted, tokens[2].Text)
				}
			},
		},
		{
			name: "comments and continuation",
			src:  "a \\\n b # comment\nc",
			check: func(t *testing.T, tokens []LexToken) {
				t.Helper()
				var got []string
				for _, tok := range tokens {
					if tok.Kind == tokenEOF {
						break
					}
					got = append(got, tok.Text)
				}
				want := []string{"a", "b", "\n", "c"}
				if strings.Join(got, "|") != strings.Join(want, "|") {
					t.Fatalf("continuation/comment mismatch\ngot:  %q\nwant: %q", got, want)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tokens, err := Lex(tc.src)
			if err != nil {
				t.Fatalf("Lex(%q) returned error: %v", tc.src, err)
			}
			tc.check(t, tokens)
		})
	}
}

func TestTokenName(t *testing.T) {
	cases := []struct {
		name string
		kind NodeType
		want string
	}{
		{name: "eof", kind: tokenEOF, want: "EOF"},
		{name: "keyword", kind: tokenIf, want: "IF"},
		{name: "operator", kind: tokenPipe, want: "PIPE"},
		{name: "ascii", kind: ';', want: ";"},
		{name: "unknown", kind: 9999, want: "UNKNOWN"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tokenName(tc.kind); got != tc.want {
				t.Fatalf("tokenName(%d) = %q, want %q", tc.kind, got, tc.want)
			}
		})
	}
}

func BenchmarkLex(b *testing.B) {
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
		_, _ = Lex(src)
	}
}
