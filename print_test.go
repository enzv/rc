package main

import "testing"

func TestFormatTreeCases(t *testing.T) {
	cases := []struct {
		name string
		tree *Tree
		want string
	}{
		{name: "nil", want: ""},
		{
			name: "subshell",
			tree: NewUnaryNode(tokenSubshell, NewUnaryNode(tokenSimple, argListTree(Token("echo", tokenWord), Token("hi", tokenWord)))),
			want: "@ echo hi",
		},
		{
			name: "dup redirection",
			tree: &Tree{
				Type:  tokenDup,
				RType: redirDupFD,
				FD0:   1,
				FD1:   2,
				Child: [3]*Tree{nil, NewUnaryNode(tokenSimple, argListTree(Token("echo", tokenWord), Token("hi", tokenWord))), nil},
			},
			want: ">[2=1]echo hi",
		},
		{
			name: "function body",
			tree: NewBinaryNode(tokenFn, Token("myfn", tokenWord), NewUnaryNode(tokenBrace, NewUnaryNode(tokenSimple, argListTree(Token("echo", tokenWord), Token("ok", tokenWord))))),
			want: "fn myfn {echo ok}",
		},
		{
			name: "pipe fd pair",
			tree: &Tree{
				Type: tokenPipe,
				FD0:  1,
				FD1:  2,
				Child: [3]*Tree{
					NewUnaryNode(tokenSimple, argListTree(Token("left", tokenWord))),
					NewUnaryNode(tokenSimple, argListTree(Token("right", tokenWord))),
					nil,
				},
			},
			want: "left|[1=2]right",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatTree(tc.tree); got != tc.want {
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

func argListTree(words ...*Tree) *Tree {
	if len(words) == 0 {
		return nil
	}
	node := words[0]
	for i := 1; i < len(words); i++ {
		node = NewBinaryNode(tokenArgList, node, words[i])
	}
	return node
}
