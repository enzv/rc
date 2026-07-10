package main

import "testing"

func TestFormatTreeCases(t *testing.T) {
	cases := []struct {
		name string
		prog *Program
		want string
	}{
		{
			name: "subshell",
			prog: &Program{
				Tokens: []LexToken{
					{Text: "echo"},
					{Text: "hi"},
				},
				Nodes: []Node{
					{Type: tokenWord, Tok: 0},
					{Type: tokenWord, Tok: 1},
					{Type: tokenArgList, Child: [3]int{0, 1, -1}},
					{Type: tokenSimple, Child: [3]int{2, -1, -1}},
					{Type: tokenSubshell, Child: [3]int{3, -1, -1}},
				},
				Root: 4,
			},
			want: "@ echo hi",
		},
		{
			name: "dup redirection",
			prog: &Program{
				Tokens: []LexToken{
					{Text: "echo"},
					{Text: "hi"},
				},
				Nodes: []Node{
					{Type: tokenWord, Tok: 0},
					{Type: tokenWord, Tok: 1},
					{Type: tokenArgList, Child: [3]int{0, 1, -1}},
					{Type: tokenSimple, Child: [3]int{2, -1, -1}},
					{Type: tokenDup, RType: redirDupFD, FD0: 1, FD1: 2, Child: [3]int{-1, 3, -1}},
				},
				Root: 4,
			},
			want: ">[2=1]echo hi",
		},
		{
			name: "function body",
			prog: &Program{
				Tokens: []LexToken{
					{Text: "myfn"},
					{Text: "echo"},
					{Text: "ok"},
				},
				Nodes: []Node{
					{Type: tokenWord, Tok: 0},
					{Type: tokenWord, Tok: 1},
					{Type: tokenWord, Tok: 2},
					{Type: tokenArgList, Child: [3]int{1, 2, -1}},
					{Type: tokenSimple, Child: [3]int{3, -1, -1}},
					{Type: tokenBrace, Child: [3]int{4, -1, -1}},
					{Type: tokenFn, Child: [3]int{0, 5, -1}},
				},
				Root: 6,
			},
			want: "fn myfn {echo ok}",
		},
		{
			name: "pipe fd pair",
			prog: &Program{
				Tokens: []LexToken{
					{Text: "left"},
					{Text: "right"},
				},
				Nodes: []Node{
					{Type: tokenWord, Tok: 0},
					{Type: tokenSimple, Child: [3]int{0, -1, -1}},
					{Type: tokenWord, Tok: 1},
					{Type: tokenSimple, Child: [3]int{2, -1, -1}},
					{Type: tokenPipe, FD0: 1, FD1: 2, Child: [3]int{1, 3, -1}},
				},
				Root: 4,
			},
			want: "left|[1=2]right",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatTree(tc.prog, tc.prog.Root); got != tc.want {
				t.Fatalf("FormatTree() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestQuoteAndDeglobHelpers(t *testing.T) {
	quotedCases := []struct {
		name string
		src  string
		want string
	}{
		{name: "empty", src: "", want: "''"},
		{name: "plain", src: "abc", want: "'abc'"},
		{name: "single quote doubled", src: "don't", want: "'don''t'"},
	}

	for _, tc := range quotedCases {
		tc := tc
		t.Run("quote/"+tc.name, func(t *testing.T) {
			if got := rcQuote(tc.src); got != tc.want {
				t.Fatalf("rcQuote(%q) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}

	deglobCases := []struct {
		name string
		src  string
		want string
	}{
		{name: "unchanged", src: "plain", want: "plain"},
		{name: "marked glob", src: string([]byte{'a', globMark, '*', '.', 't', 'x', 't'}), want: "a*.txt"},
	}

	for _, tc := range deglobCases {
		tc := tc
		t.Run("deglob/"+tc.name, func(t *testing.T) {
			if got := deglobString(tc.src); got != tc.want {
				t.Fatalf("deglobString(%q) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}
