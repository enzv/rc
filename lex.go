package main

import (
	"fmt"
	"strings"
)

var asciiText = func() [128]string {
	var out [128]string
	for i := range out {
		out[i] = string(rune(i))
	}
	return out
}()

var wordCharTable = func() [128]bool {
	var out [128]bool
	for i := range out {
		c := byte(i)
		out[i] = c != '\n' && c != ' ' && c != '\t' && c != '#' && c != ';' && c != '&' && c != '|' && c != '^' && c != '$' && c != '=' && c != '`' && c != '\'' && c != '{' && c != '}' && c != '(' && c != ')' && c != '<' && c != '>'
	}
	return out
}()

var idCharTable = func() [128]bool {
	var out [128]bool
	for i := range out {
		c := byte(i)
		out[i] = c > ' ' && c != '!' && c != '"' && c != '#' && c != '$' && c != '%' && c != '&' && c != '\'' && c != '(' && c != ')' && c != '+' && c != ',' && c != '-' && c != '.' && c != '/' && c != ':' && c != ';' && c != '<' && c != '=' && c != '>' && c != '?' && c != '@' && c != '[' && c != '\\' && c != ']' && c != '^' && c != '`' && c != '{' && c != '|' && c != '}' && c != '~'
	}
	return out
}()

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
	Glob   bool
	Quoted bool
	RType  RedirType
	FD0    int
	FD1    int
	Pos    Position
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
	// Shell source tends to be token-dense, so reserve more aggressively than
	// the original heuristic to avoid repeated slice growth on large inputs.
	out := make([]LexToken, 0, len(src)/7+2)
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
	return c >= 0 && c < len(wordCharTable) && wordCharTable[c]
}

func idchr(c int) bool {
	return c >= 0 && c < len(idCharTable) && idCharTable[c]
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
		return LexToken{Kind: '&', Text: asciiText['&'], Pos: pos}, nil
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
		return LexToken{Kind: tokenWord, Text: text, Quoted: true, Pos: pos}, nil
	case '`':
		lx.lastDol = false
		lx.bqPending = true
		return LexToken{Kind: '`', Text: asciiText['`'], Pos: pos}, nil
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
		return LexToken{Kind: NodeType(c), Text: asciiText[c], Pos: pos}, nil
	}
	text, glob := lx.scanWord(c)
	lx.lastWord = true
	lx.lastDol = false
	kind, ok := keywordType(text)
	if ok {
		lx.lastWord = false
	}
	return LexToken{Kind: kind, Text: text, Glob: glob, Pos: pos}, nil
}

func (lx *Lexer) scanRedir(first int, pos Position) (LexToken, error) {
	start := lx.pos - 1
	kind := tokenRedir
	rtype := RedirType(0)
	fd0 := 0
	fd1 := 0
	switch first {
	case '|':
		kind = tokenPipe
		fd0 = 1
	case '>':
		if lx.nextIs('>') {
			rtype = redirAppend
		} else {
			rtype = redirWrite
		}
		fd0 = 1
	case '<':
		if lx.nextIs('<') {
			rtype = redirHere
		} else if lx.nextIs('>') {
			rtype = redirRDWR
		} else {
			rtype = redirRead
		}
	}
	if lx.nextIs('[') {
		qualFD := 0
		c := lx.advance()
		if c < '0' || c > '9' {
			return LexToken{}, lx.syntaxError(pos, "redirection syntax")
		}
		for c >= '0' && c <= '9' {
			qualFD = qualFD*10 + c - '0'
			c = lx.advance()
		}
		fd0 = qualFD
		if c == '=' {
			if kind == tokenRedir {
				kind = tokenDup
			}
			c = lx.advance()
			if c >= '0' && c <= '9' {
				rtype = redirDupFD
				fd1 = fd0
				fd0 = 0
				for c >= '0' && c <= '9' {
					fd0 = fd0*10 + c - '0'
					c = lx.advance()
				}
			} else {
				if kind == tokenPipe {
					return LexToken{}, lx.syntaxError(pos, "pipe syntax")
				}
				rtype = redirClose
			}
		}
		if c != ']' || (kind == tokenDup && (rtype == redirHere || rtype == redirAppend)) {
			return LexToken{}, lx.syntaxError(pos, "redirection syntax")
		}
	} else if kind == tokenPipe {
		lx.skipNL()
	}
	return LexToken{
		Kind:  kind,
		Text:  lx.src[start:lx.pos],
		RType: rtype,
		FD0:   fd0,
		FD1:   fd1,
		Pos:   pos,
	}, nil
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

func (lx *Lexer) scanWord(first int) (string, bool) {
	start := lx.pos - 1
	hasGlob := false
	c := first
	for {
		if !lx.lastDol && (c == '*' || c == '[' || c == '?') {
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
		return lx.src[start:end], false
	}
	return lx.src[start:end], true
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
