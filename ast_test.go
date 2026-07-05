package main

import (
	"testing"
)

func TestTreeHelpers(t *testing.T) {
	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "format tree word and concat",
			check: func(t *testing.T) {
				t.Helper()
				left := Token("a", tokenWord)
				right := Token("b", tokenWord)
				tree := NewBinaryNode('^', left, right)
				if got := FormatTree(tree); got != "a^b" {
					t.Fatalf("FormatTree(concat) = %q, want %q", got, "a^b")
				}
			},
		},
		{
			name: "simple mung lifts redirection",
			check: func(t *testing.T) {
				t.Helper()
				cmd := NewBinaryNode(tokenArgList, Token("echo", tokenWord), NewBinaryNode(tokenArgList, Token("ok", tokenWord), nil))
				redir := &Tree{Type: tokenRedir, RType: redirWrite, FD0: 1, Child: [3]*Tree{Token("out", tokenWord), nil, nil}}
				root := SimpleMung(NewBinaryNode(tokenArgList, cmd, redir))
				if root.Type != tokenRedir {
					t.Fatalf("SimpleMung root type = %s, want REDIR", tokenName(root.Type))
				}
				if got := FormatTree(root); got == "" {
					t.Fatal("FormatTree returned empty string for lifted redirection")
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
