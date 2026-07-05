package main

type NodeType int
type RedirType int

// Tree represents a syntactic node in the rc Abstract Syntax Tree.
type Tree struct {
	Type       NodeType
	RType      RedirType
	FD0        int
	FD1        int
	Str        string
	Quoted     bool
	HereBody   string
	HereQuoted bool
	IsKeyword  bool
	Pos        Position
	Child      [3]*Tree
}

// Code represents a block of code, containing an AST and optionally its raw source text.
type Code struct {
	F func()
	I int
	S string
}

// Program represents a complete parsed script.
type Program struct {
	Tokens []LexToken
	Root   *Tree
}

// NewUnaryNode creates a new AST node with one child.
func NewUnaryNode(kind NodeType, c0 *Tree) *Tree {
	return NewTernaryNode(kind, c0, nil, nil)
}

// NewBinaryNode creates a new AST node with two children.
func NewBinaryNode(kind NodeType, c0, c1 *Tree) *Tree {
	return NewTernaryNode(kind, c0, c1, nil)
}

// NewTernaryNode creates a new AST node with three children.
func NewTernaryNode(kind NodeType, c0, c1, c2 *Tree) *Tree {
	if kind == ';' {
		if c0 == nil {
			return c1
		}
		if c1 == nil {
			return c0
		}
	}
	return &Tree{
		Type:  kind,
		Child: [3]*Tree{c0, c1, c2},
	}
}

// Token creates a new terminal AST node from a string.
func Token(str string, kind NodeType) *Tree {
	return &Tree{Type: kind, Str: str}
}

func cloneTree(t *Tree) *Tree {
	if t == nil {
		return nil
	}
	cp := *t
	cp.Child = [3]*Tree{}
	return &cp
}

// Mung1 is an internal helper that allocates a one-child tree efficiently.
func Mung1(t, c0 *Tree) *Tree {
	t.Child[0] = c0
	return t
}

// Mung2 is an internal helper that allocates a two-child tree efficiently.
func Mung2(t, c0, c1 *Tree) *Tree {
	t.Child[0] = c0
	t.Child[1] = c1
	return t
}

// Mung3 is an internal helper that allocates a three-child tree efficiently.
func Mung3(t, c0, c1, c2 *Tree) *Tree {
	t.Child[0] = c0
	t.Child[1] = c1
	t.Child[2] = c2
	return t
}

// EpiMung attaches an epilogue to an existing tree node.
func EpiMung(comp, epi *Tree) *Tree {
	if epi == nil {
		return comp
	}
	p := epi
	for p.Child[1] != nil {
		p = p.Child[1]
	}
	p.Child[1] = comp
	return epi
}

// SimpleMung ensures a tree is properly wrapped as a simple command if needed.
func SimpleMung(t *Tree) *Tree {
	t = NewUnaryNode(tokenSimple, t)
	t.Str = FormatTree(t)
	for u := t.Child[0]; u != nil && u.Type == tokenArgList; u = u.Child[0] {
		if u.Child[1] != nil && (u.Child[1].Type == tokenDup || u.Child[1].Type == tokenRedir) {
			u.Child[1].Child[1] = t
			t = u.Child[1]
			u.Child[1] = nil
		}
	}
	return t
}

const (
	tokenEOF = -1

	tokenBase NodeType = 256 + iota
	tokenFor
	tokenIn
	tokenWhile
	tokenIf
	tokenNot
	tokenTwiddle
	tokenBang
	tokenSubshell
	tokenSwitch
	tokenFn
	tokenWord
	tokenRedir
	tokenDup
	tokenPipe
	tokenSub
	tokenSimple
	tokenArgList
	tokenWords
	tokenBrace
	tokenParen
	tokenPCmd
	tokenPipeFD
	tokenAndAnd
	tokenOrOr
	tokenCount
)

const (
	redirAppend RedirType = 1
	redirWrite  RedirType = 2
	redirRead   RedirType = 3
	redirHere             = 4
	redirDupFD            = 5
	redirClose            = 6
	redirRDWR             = 7
)

const (
	nVar     = 521
	nKeyword = 30
	globMark = '\x01'
)

func tokenName(kind NodeType) string {
	switch kind {
	case tokenEOF:
		return "EOF"
	case tokenFor:
		return "FOR"
	case tokenIn:
		return "IN"
	case tokenWhile:
		return "WHILE"
	case tokenIf:
		return "IF"
	case tokenNot:
		return "NOT"
	case tokenTwiddle:
		return "TWIDDLE"
	case tokenBang:
		return "BANG"
	case tokenSubshell:
		return "SUBSHELL"
	case tokenSwitch:
		return "SWITCH"
	case tokenFn:
		return "FN"
	case tokenWord:
		return "WORD"
	case tokenRedir:
		return "REDIR"
	case tokenDup:
		return "DUP"
	case tokenPipe:
		return "PIPE"
	case tokenSub:
		return "SUB"
	case tokenSimple:
		return "SIMPLE"
	case tokenArgList:
		return "ARGLIST"
	case tokenWords:
		return "WORDS"
	case tokenBrace:
		return "BRACE"
	case tokenParen:
		return "PAREN"
	case tokenPCmd:
		return "PCMD"
	case tokenPipeFD:
		return "PIPEFD"
	case tokenAndAnd:
		return "ANDAND"
	case tokenOrOr:
		return "OROR"
	case tokenCount:
		return "COUNT"
	}
	if kind >= 0 && kind < 128 {
		return string(rune(kind))
	}
	return "UNKNOWN"
}
