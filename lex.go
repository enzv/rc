package main

import (
	"fmt"
	"strings"
)

// Position tracks the line and column number in the source text.
type Position struct {
	Offset int
	Line   int
	Column int
}

// LexToken represents a single lexical token produced by the scanner.
type LexToken struct {
	Kind   NodeType
	Text   string
	Tree   *Tree
	Quoted bool
	Pos    Position
}

func (t LexToken) KindString() string {
	return tokenName(t.Kind)
}

// Lexer converts source text into a stream of tokens for parsing.
type Lexer struct {
	src       string
	pos       int
	line      int
	column    int
	lastWord  bool
	lastDol   bool
	inQuote   bool
	inComment bool
	bqPending bool
	bqDepth   int
}

// Lex scans the provided source string and returns a complete slice of tokens.
func Lex(src string) ([]LexToken, error) {
	lx := &Lexer{src: src, line: 1, column: 1}
	out := make([]LexToken, 0, len(src)/8+2)
	for {
		tok, err := lx.nextToken()
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
		if tok.Kind == tokenEOF {
			return out, nil
		}
	}
}

func wordchr(c int) bool {
	return c != tokenEOF && !strings.ContainsRune("\n \t#;&|^$=`'{}()<>", rune(c))
}

func idchr(c int) bool {
	return c > ' ' && !strings.ContainsRune("!\"#$%&'()+,-./:;<=>?@[\\]^`{|}~", rune(c))
}

func (lx *Lexer) nextToken() (LexToken, error) {
	if lx.lastWord {
		lx.lastWord = false
		d := lx.peek()
		pos := lx.position()
		if d == '(' {
			lx.advance()
			return LexToken{Kind: tokenSub, Text: "( [SUB]", Pos: pos}, nil
		}
		if wordchr(d) || d == '\'' || d == '`' || d == '$' || d == '"' {
			return LexToken{Kind: '^', Text: "^", Pos: pos}, nil
		}
	}
	lx.inQuote = false
	lx.skipWhite()
	pos := lx.position()
	c := lx.advance()
	switch c {
	case tokenEOF:
		lx.lastDol = false
		return LexToken{Kind: tokenEOF, Text: "EOF", Pos: pos}, nil
	case '$':
		lx.lastDol = true
		if lx.nextIs('#') {
			return LexToken{Kind: tokenCount, Text: "$#", Pos: pos}, nil
		}
		if lx.nextIs('"') {
			return LexToken{Kind: '"', Text: "$\"", Pos: pos}, nil
		}
		return LexToken{Kind: '$', Text: "$", Pos: pos}, nil
	case '&':
		lx.lastDol = false
		if lx.nextIs('&') {
			lx.skipNL()
			return LexToken{Kind: tokenAndAnd, Text: "&&", Pos: pos}, nil
		}
		return LexToken{Kind: '&', Text: "&", Pos: pos}, nil
	case '|':
		lx.lastDol = false
		if lx.nextIs('|') {
			lx.skipNL()
			return LexToken{Kind: tokenOrOr, Text: "||", Pos: pos}, nil
		}
		return lx.scanRedir('|', pos)
	case '<', '>':
		lx.lastDol = false
		return lx.scanRedir(c, pos)
	case '\'':
		lx.lastDol = false
		lx.lastWord = true
		lx.inQuote = true
		text, err := lx.scanQuoted()
		if err != nil {
			return LexToken{}, err
		}
		t := Token(text, tokenWord)
		t.Quoted = true
		return LexToken{Kind: tokenWord, Text: text, Tree: t, Quoted: true, Pos: pos}, nil
	case '`':
		lx.lastDol = false
		lx.bqPending = true
		return LexToken{Kind: '`', Text: "`", Pos: pos}, nil
	}
	if !wordchr(c) {
		if c == '{' {
			if lx.bqPending {
				lx.bqPending = false
				lx.bqDepth = 1
			} else if lx.bqDepth > 0 {
				lx.bqDepth++
			}
		}
		if c == '}' && lx.bqDepth > 0 {
			lx.bqDepth--
			if lx.bqDepth == 0 {
				lx.lastWord = true
			}
		}
		lx.lastDol = false
		return LexToken{Kind: NodeType(c), Text: string(rune(c)), Pos: pos}, nil
	}
	text := lx.scanWord(c)
	lx.lastWord = true
	lx.lastDol = false
	t := Klook(text)
	if t.Type != tokenWord {
		lx.lastWord = false
	}
	t.Quoted = false
	return LexToken{Kind: t.Type, Text: text, Tree: t, Pos: pos}, nil
}

func (lx *Lexer) scanRedir(first int, pos Position) (LexToken, error) {
	start := lx.pos - 1
	t := &Tree{}
	switch first {
	case '|':
		t.Type = tokenPipe
		t.FD0 = 1
		t.FD1 = 0
	case '>':
		t.Type = tokenRedir
		if lx.nextIs('>') {
			t.RType = redirAppend
		} else {
			t.RType = redirWrite
		}
		t.FD0 = 1
	case '<':
		t.Type = tokenRedir
		if lx.nextIs('<') {
			t.RType = redirHere
		} else if lx.nextIs('>') {
			t.RType = redirRDWR
		} else {
			t.RType = redirRead
		}
		t.FD0 = 0
	}
	if lx.nextIs('[') {
		c := lx.advance()
		if c < '0' || c > '9' {
			return LexToken{}, lx.syntaxError(pos, "redirection syntax")
		}
		fd0 := 0
		for c >= '0' && c <= '9' {
			fd0 = fd0*10 + c - '0'
			c = lx.advance()
		}
		t.FD0 = fd0
		if c == '=' {
			if t.Type == tokenRedir {
				t.Type = tokenDup
			}
			c = lx.advance()
			if c >= '0' && c <= '9' {
				t.RType = redirDupFD
				t.FD1 = t.FD0
				t.FD0 = 0
				for c >= '0' && c <= '9' {
					t.FD0 = t.FD0*10 + c - '0'
					c = lx.advance()
				}
			} else {
				if t.Type == tokenPipe {
					return LexToken{}, lx.syntaxError(pos, "pipe syntax")
				}
				t.RType = redirClose
			}
		}
		if c != ']' || (t.Type == tokenDup && (t.RType == redirHere || t.RType == redirAppend)) {
			return LexToken{}, lx.syntaxError(pos, "redirection syntax")
		}
	} else if t.Type == tokenPipe {
		lx.skipNL()
	}
	return LexToken{Kind: t.Type, Text: lx.src[start:lx.pos], Tree: t, Pos: pos}, nil
}

func (lx *Lexer) scanQuoted() (string, error) {
	start := lx.pos
	hasEscape := false
	for {
		c := lx.advance()
		if c == tokenEOF {
			return "", fmt.Errorf("line %d:%d: unterminated quoted word", lx.line, lx.column)
		}
		if c == '\'' {
			if lx.peek() != '\'' {
				end := lx.pos - 1
				if !hasEscape {
					return lx.src[start:end], nil
				}
				break
			}
			lx.advance()
			hasEscape = true
		}
	}

	var b strings.Builder
	b.Grow(lx.pos - start)
	p := start
	for p < lx.pos-1 {
		c := lx.src[p]
		b.WriteByte(c)
		p++
		if c == '\'' && lx.src[p] == '\'' {
			p++ // skip escaped quote
		}
	}
	return b.String(), nil
}

func (lx *Lexer) scanWord(first int) string {
	start := lx.pos - 1
	hasGlob := false
	c := first
	for {
		if !lx.lastDol && (c == '*' || c == '[' || c == '?' || c == int(globMark)) {
			hasGlob = true
		}
		c = lx.peek()
		if lx.lastDol {
			if !idchr(c) {
				break
			}
		} else if !wordchr(c) {
			break
		}
		lx.advance()
	}
	end := lx.pos

	if !hasGlob {
		return lx.src[start:end]
	}

	// Slow path for words with globs
	var b strings.Builder
	b.Grow(end - start + 4)
	p := start
	for p < end {
		c := int(lx.src[p])
		if !lx.lastDol && (c == '*' || c == '[' || c == '?' || c == int(globMark)) {
			b.WriteByte(globMark)
		}
		b.WriteByte(byte(c))
		p++
	}
	return b.String()
}

func (lx *Lexer) skipWhite() {
	for {
		c := lx.peek()
		if c == '#' {
			lx.inComment = true
			for {
				c = lx.peek()
				if c == '\n' || c == tokenEOF {
					lx.inComment = false
					break
				}
				lx.advance()
			}
		}
		if c == ' ' || c == '\t' {
			lx.advance()
			continue
		}
		return
	}
}

func (lx *Lexer) skipNL() {
	for {
		lx.skipWhite()
		if lx.peek() != '\n' {
			return
		}
		lx.advance()
	}
}

func (lx *Lexer) nextIs(want int) bool {
	if lx.peek() == want {
		lx.advance()
		return true
	}
	return false
}

func (lx *Lexer) peek() int {
	if lx.pos >= len(lx.src) {
		return tokenEOF
	}
	if lx.src[lx.pos] == '\\' && !lx.inQuote {
		if lx.pos+1 < len(lx.src) && lx.src[lx.pos+1] == '\n' && !lx.inComment {
			return ' '
		}
	}
	return int(lx.src[lx.pos])
}

func (lx *Lexer) advance() int {
	if lx.pos >= len(lx.src) {
		return tokenEOF
	}
	c := int(lx.src[lx.pos])
	lx.pos++
	if !lx.inQuote && c == '\\' {
		if lx.pos < len(lx.src) && lx.src[lx.pos] == '\n' && !lx.inComment {
			lx.pos++
			lx.line++
			lx.column = 1
			return ' '
		}
	}
	if c == '\n' {
		lx.line++
		lx.column = 1
	} else {
		lx.column++
	}
	return c
}

func (lx *Lexer) position() Position {
	return Position{Offset: lx.pos, Line: lx.line, Column: lx.column}
}

func (lx *Lexer) syntaxError(pos Position, msg string) error {
	return fmt.Errorf("line %d:%d: %s", pos.Line, pos.Column, msg)
}
