package main

import "testing"

func TestTreeHelpers(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "format tree word and concat",
			run: func(t *testing.T) {
				t.Helper()
				prog := &Program{
					Tokens: []LexToken{
						{Text: "a"},
						{Text: "b"},
					},
					Nodes: []Node{
						{Type: tokenWord, Tok: 0},
						{Type: tokenWord, Tok: 1},
						{Type: '^', Child: [3]int{0, 1, -1}},
					},
					Root: 2,
				}
				if got := FormatTree(prog, prog.Root); got != "a^b" {
					t.Fatalf("FormatTree(concat) = %q, want %q", got, "a^b")
				}
			},
		},
		{
			name: "redirection tree formatting",
			run: func(t *testing.T) {
				t.Helper()
				prog := &Program{
					Tokens: []LexToken{
						{Text: "out"},
						{Text: "echo"},
						{Text: "ok"},
					},
					Nodes: []Node{
						{Type: tokenWord, Tok: 0},
						{Type: tokenWord, Tok: 1},
						{Type: tokenWord, Tok: 2},
						{Type: tokenArgList, Child: [3]int{1, 2, -1}},
						{Type: tokenSimple, Child: [3]int{3, -1, -1}},
						{Type: tokenRedir, RType: redirWrite, FD0: 1, Child: [3]int{0, 4, -1}},
					},
					Root: 5,
				}
				if got := FormatTree(prog, prog.Root); got == "" {
					t.Fatal("FormatTree returned empty string for redirection tree")
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, tc.run)
	}
}
