package main

type NodeType int
type RedirType int

// Node represents a syntactic node in the rc Abstract Syntax Tree.
type Node struct {
	Type  NodeType
	RType RedirType
	FD0   int
	FD1   int
	Tok   int
	Child [3]int
}

// Code represents a block of code, containing an AST and optionally its raw source text.
type Code struct {
	F func()
	I int
	S string
}

// Program represents a complete parsed script.
type Program struct {
	Tokens   []LexToken
	Nodes    []Node
	Offsets  []int
	HereDocs map[int]hereDoc
	Root     int
}

// Node returns the node for id, or nil if id is invalid.
func (p *Program) Node(id int) *Node {
	if p == nil || id < 0 || id >= len(p.Nodes) {
		return nil
	}
	return &p.Nodes[id]
}

// Token returns the lexical token for id, or nil if invalid.
func (p *Program) Token(id int) *LexToken {
	if p == nil || id < 0 || id >= len(p.Tokens) {
		return nil
	}
	return &p.Tokens[id]
}

// Pos returns the source position for node id.
func (p *Program) Pos(id int) Position {
	if p == nil || id < 0 || id >= len(p.Offsets) || id >= len(p.Nodes) {
		return Position{}
	}
	return Position{Offset: p.Offsets[id]}
}

// Child returns the child id at index i, or -1 if the node or child is invalid.
func (p *Program) Child(id, i int) int {
	if p == nil || id < 0 || id >= len(p.Nodes) || i < 0 || i >= len(p.Nodes[id].Child) {
		return -1
	}
	return p.Nodes[id].Child[i]
}

// AddNode appends a node and returns its id.
func (p *Program) AddNode(node Node) int {
	p.Nodes = append(p.Nodes, node)
	p.Offsets = append(p.Offsets, 0)
	return len(p.Nodes) - 1
}

// HereDoc returns the body and quoting mode for a here-doc redirection node.
func (p *Program) HereDoc(id int) (string, bool, bool) {
	if p == nil || p.HereDocs == nil {
		return "", false, false
	}
	doc, ok := p.HereDocs[id]
	if !ok {
		return "", false, false
	}
	return doc.body, doc.quoted, true
}

// NodeType constructors for tests and small helpers.
func WordNode(str string, quoted bool) Node {
	_ = str
	_ = quoted
	return Node{Type: tokenWord, Tok: -1, Child: noChildren()}
}

func SyntaxNode(kind NodeType) Node {
	return Node{Type: kind, Child: noChildren()}
}

func noChildren() [3]int {
	return [3]int{-1, -1, -1}
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
