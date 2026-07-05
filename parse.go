package main

import (
	"fmt"
	"strings"
)

// ParseSource converts rc source code into a Program AST.
func ParseSource(src string) (*Program, error) {
	prepared := prepareSource(src)
	tokens, err := Lex(prepared.Stripped)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	root, err := p.parseList(tokenEOF)
	if err != nil {
		return nil, err
	}
	if p.current().Kind != tokenEOF {
		return nil, p.unexpected("unexpected trailing token")
	}
	attachHereDocs(root, prepared.HereDocs)
	return &Program{Tokens: tokens, Root: root}, nil
}

type preparedSource struct {
	Stripped string
	HereDocs []hereDoc
}

type pendingHereDoc struct {
	tag    string
	quoted bool
}

type hereDoc struct {
	tag    string
	quoted bool
	body   string
}

func prepareSource(src string) preparedSource {
	var out strings.Builder
	out.Grow(len(src))
	var pending []pendingHereDoc
	var docs []hereDoc
	inQuote := false
	inComment := false

	for i := 0; i < len(src); {
		ch := src[i]
		out.WriteByte(ch)

		if inComment {
			i++
			if ch == '\n' {
				inComment = false
				if len(pending) != 0 {
					next, found := collectHereDocs(src, i, pending)
					docs = append(docs, found...)
					pending = nil
					i = next
				}
			}
			continue
		}
		if inQuote {
			i++
			if ch == '\'' {
				if i < len(src) && src[i] == '\'' {
					out.WriteByte(src[i])
					i++
				} else {
					inQuote = false
				}
			}
			continue
		}

		switch ch {
		case '\'':
			inQuote = true
			i++
		case '#':
			inComment = true
			i++
		case '<':
			if i+1 < len(src) && src[i+1] == '<' {
				doc := scanHereTag(src, i+2)
				if doc.tag != "" {
					pending = append(pending, doc)
				}
				out.WriteByte('<')
				i += 2
				continue
			}
			i++
		case '\n':
			i++
			if len(pending) != 0 {
				next, found := collectHereDocs(src, i, pending)
				docs = append(docs, found...)
				pending = nil
				i = next
			}
		default:
			i++
		}
	}
	return preparedSource{
		Stripped: out.String(),
		HereDocs: docs,
	}
}

func scanHereTag(src string, start int) pendingHereDoc {
	i := start
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	if i >= len(src) || src[i] == '\n' {
		return pendingHereDoc{}
	}
	if src[i] == '\'' {
		i++
		var tag strings.Builder
		for i < len(src) {
			if src[i] == '\'' {
				if i+1 < len(src) && src[i+1] == '\'' {
					tag.WriteByte('\'')
					i += 2
					continue
				}
				return pendingHereDoc{tag: tag.String(), quoted: true}
			}
			tag.WriteByte(src[i])
			i++
		}
		return pendingHereDoc{tag: tag.String(), quoted: true}
	}
	j := i
	for j < len(src) && src[j] != ' ' && src[j] != '\t' && src[j] != '\n' {
		j++
	}
	return pendingHereDoc{tag: src[i:j]}
}

func collectHereDocs(src string, start int, pending []pendingHereDoc) (int, []hereDoc) {
	i := start
	docs := make([]hereDoc, 0, len(pending))
	for _, doc := range pending {
		var body strings.Builder
		for i < len(src) {
			lineStart := i
			for i < len(src) && src[i] != '\n' {
				i++
			}
			line := src[lineStart:i]
			if line == doc.tag {
				if i < len(src) && src[i] == '\n' {
					i++
				}
				break
			}
			body.WriteString(line)
			if i < len(src) && src[i] == '\n' {
				body.WriteByte('\n')
				i++
			}
		}
		docs = append(docs, hereDoc{
			tag:    doc.tag,
			quoted: doc.quoted,
			body:   body.String(),
		})
	}
	return i, docs
}

func attachHereDocs(root *Tree, docs []hereDoc) {
	if root == nil || len(docs) == 0 {
		return
	}
	var index int
	var walk func(*Tree)
	walk = func(node *Tree) {
		if node == nil || index >= len(docs) {
			return
		}
		if node.Type == tokenRedir && node.RType == redirHere {
			node.HereBody = docs[index].body
			node.HereQuoted = docs[index].quoted
			index++
		}
		walk(node.Child[0])
		walk(node.Child[1])
		walk(node.Child[2])
	}
	walk(root)
}

type parser struct {
	tokens []LexToken
	pos    int
}

func containsToken(stops []NodeType, kind NodeType) bool {
	for _, stop := range stops {
		if stop == kind {
			return true
		}
	}
	return false
}

func (p *parser) parseList(stops ...NodeType) (*Tree, error) {
	var list *Tree
	for {
		p.skipSeparators()
		kind := p.current().Kind
		if containsToken(stops, kind) || (kind == tokenEOF && containsToken(stops, tokenEOF)) {
			return list, nil
		}
		cmd, err := p.parseAndOr()
		if err != nil {
			return nil, err
		}
		switch p.current().Kind {
		case '&':
			cmd = NewUnaryNode('&', cmd)
			cmd.Pos = p.current().Pos
			p.next()
		}
		list = NewBinaryNode(';', list, cmd)

		hasSep := false
		switch p.current().Kind {
		case ';', '\n':
			p.next()
			hasSep = true
		}

		if !hasSep && cmd.Type != '&' {
			return list, nil
		}
	}
}

func (p *parser) parseAndOr() (*Tree, error) {
	left, err := p.parsePrefixed()
	if err != nil {
		return nil, err
	}
	for {
		switch p.current().Kind {
		case tokenAndAnd, tokenOrOr:
			op := p.current()
			p.next()
			right, err := p.parsePrefixed()
			if err != nil {
				return nil, err
			}
			left = NewBinaryNode(op.Kind, left, right)
			left.Pos = op.Pos
		default:
			return left, nil
		}
	}
}

func (p *parser) parsePrefixed() (*Tree, error) {
	switch p.current().Kind {
	case tokenRedir, tokenDup:
		if p.current().Kind == tokenRedir && p.nextKind() == '{' {
			break
		}
		return p.parsePrefixRedir()
	case tokenBang, tokenSubshell:
		op := p.current()
		p.next()
		child, err := p.parsePrefixed()
		if err != nil {
			return nil, err
		}
		node := cloneTree(op.Tree)
		node.Type = op.Kind
		node.Pos = op.Pos
		return Mung1(node, child), nil
	}
	if p.canStartFirst() {
		save := p.pos
		left, err := p.parseFirst()
		if err == nil && p.current().Kind == '=' {
			assignTok := p.current()
			p.next()
			right, err := p.parseWord()
			if err != nil {
				return nil, err
			}
			var child *Tree
			if !p.isCommandBoundary(p.current().Kind) {
				child, err = p.parsePrefixed()
				if err != nil {
					return nil, err
				}
			}
			node := NewTernaryNode('=', left, right, child)
			node.Pos = assignTok.Pos
			return node, nil
		}
		p.pos = save
	}
	return p.parsePipe()
}

func (p *parser) parsePipe() (*Tree, error) {
	left, err := p.parsePrimaryCommand()
	if err != nil {
		return nil, err
	}
	for p.current().Kind == tokenPipe {
		op := cloneTree(p.current().Tree)
		op.Pos = p.current().Pos
		p.next()
		right, err := p.parsePrefixed()
		if err != nil {
			return nil, err
		}
		left = Mung2(op, left, right)
	}
	return left, nil
}

func (p *parser) parsePrimaryCommand() (*Tree, error) {
	switch p.current().Kind {
	case '{':
		return p.parseBraceWithEpilog()
	case tokenIf:
		return p.parseIf()
	case tokenFor:
		return p.parseFor()
	case tokenWhile:
		return p.parseWhile()
	case tokenSwitch:
		return p.parseSwitch()
	case tokenFn:
		return p.parseFn()
	case tokenTwiddle:
		return p.parseTwiddle()
	}
	return p.parseSimple()
}

func (p *parser) parseBraceWithEpilog() (*Tree, error) {
	brace, err := p.parseBrace()
	if err != nil {
		return nil, err
	}
	epilog, err := p.parseEpilog()
	if err != nil {
		return nil, err
	}
	return EpiMung(brace, epilog), nil
}

func (p *parser) parseBrace() (*Tree, error) {
	open := p.current()
	if err := p.expect('{'); err != nil {
		return nil, err
	}
	body, err := p.parseList('}')
	if err != nil {
		return nil, err
	}
	if err := p.expect('}'); err != nil {
		return nil, err
	}
	node := NewUnaryNode(tokenBrace, body)
	node.Pos = open.Pos
	return node, nil
}

func (p *parser) parseParenCommand() (*Tree, error) {
	open := p.current()
	if err := p.expect('('); err != nil {
		return nil, err
	}
	body, err := p.parseList(')')
	if err != nil {
		return nil, err
	}
	if err := p.expect(')'); err != nil {
		return nil, err
	}
	node := NewUnaryNode(tokenPCmd, body)
	node.Pos = open.Pos
	return node, nil
}

func (p *parser) parseIf() (*Tree, error) {
	ifTok := p.current()
	if err := p.expect(tokenIf); err != nil {
		return nil, err
	}
	if p.current().Kind == tokenNot {
		notTok := p.current()
		p.next()
		p.skipNewlines()
		child, err := p.parseAndOr()
		if err != nil {
			return nil, err
		}
		node := cloneTree(notTok.Tree)
		node.Type = tokenNot
		node.Pos = ifTok.Pos
		return Mung1(node, child), nil
	}
	cond, err := p.parseParenCommand()
	if err != nil {
		return nil, err
	}
	p.skipNewlines()
	body, err := p.parseAndOr()
	if err != nil {
		return nil, err
	}
	node := &Tree{Type: tokenIf, Pos: ifTok.Pos}
	return Mung2(node, cond, body), nil
}

func (p *parser) parseFor() (*Tree, error) {
	forTok := p.current()
	if err := p.expect(tokenFor); err != nil {
		return nil, err
	}
	if err := p.expect('('); err != nil {
		return nil, err
	}
	name, err := p.parseWord()
	if err != nil {
		return nil, err
	}
	var words *Tree
	if p.current().Kind == tokenIn {
		p.next()
		words, err = p.parseWords(')')
		if err != nil {
			return nil, err
		}
		if words == nil {
			words = NewUnaryNode(tokenParen, nil)
			words.Pos = p.current().Pos
		}
	}
	if err := p.expect(')'); err != nil {
		return nil, err
	}
	p.skipNewlines()
	body, err := p.parseAndOr()
	if err != nil {
		return nil, err
	}
	node := &Tree{Type: tokenFor, Pos: forTok.Pos}
	return Mung3(node, name, words, body), nil
}

func (p *parser) parseWhile() (*Tree, error) {
	whileTok := p.current()
	if err := p.expect(tokenWhile); err != nil {
		return nil, err
	}
	cond, err := p.parseParenCommand()
	if err != nil {
		return nil, err
	}
	p.skipNewlines()
	body, err := p.parseAndOr()
	if err != nil {
		return nil, err
	}
	node := &Tree{Type: tokenWhile, Pos: whileTok.Pos}
	return Mung2(node, cond, body), nil
}

func (p *parser) parseSwitch() (*Tree, error) {
	switchTok := p.current()
	if err := p.expect(tokenSwitch); err != nil {
		return nil, err
	}
	arg, err := p.parseWord()
	if err != nil {
		return nil, err
	}
	p.skipNewlines()
	body, err := p.parseBrace()
	if err != nil {
		return nil, err
	}
	node := &Tree{Type: tokenSwitch, Pos: switchTok.Pos}
	return Mung2(node, arg, body), nil
}

func (p *parser) parseFn() (*Tree, error) {
	fnTok := p.current()
	if err := p.expect(tokenFn); err != nil {
		return nil, err
	}
	names, err := p.parseWords('{', '\n', ';', '&', tokenEOF, '}')
	if err != nil {
		return nil, err
	}
	if names == nil {
		return nil, p.unexpected("fn requires a name")
	}
	var body *Tree
	if p.current().Kind == '{' {
		body, err = p.parseBrace()
		if err != nil {
			return nil, err
		}
	}
	node := &Tree{Type: tokenFn, Pos: fnTok.Pos}
	if body != nil {
		return Mung2(node, names, body), nil
	}
	return Mung1(node, names), nil
}

func (p *parser) parseTwiddle() (*Tree, error) {
	twiddleTok := p.current()
	if err := p.expect(tokenTwiddle); err != nil {
		return nil, err
	}
	subject, err := p.parseWord()
	if err != nil {
		return nil, err
	}
	patterns, err := p.parseWords(';', '&', '\n', ')', '}', tokenEOF)
	if err != nil {
		return nil, err
	}
	node := &Tree{Type: tokenTwiddle, Pos: twiddleTok.Pos}
	return Mung2(node, subject, patterns), nil
}

func (p *parser) parseSimple() (*Tree, error) {
	first, err := p.parseFirst()
	if err != nil {
		return nil, err
	}
	simple := first
	for {
		switch p.current().Kind {
		case tokenRedir, tokenDup:
			if p.current().Kind == tokenRedir && p.nextKind() == '{' {
				word, err := p.parseWord()
				if err != nil {
					return nil, err
				}
				simple = NewBinaryNode(tokenArgList, simple, word)
				continue
			}
			redir, err := p.parseRedir()
			if err != nil {
				return nil, err
			}
			simple = NewBinaryNode(tokenArgList, simple, redir)
		default:
			if !p.canStartWord() {
				return SimpleMung(simple), nil
			}
			word, err := p.parseWord()
			if err != nil {
				return nil, err
			}
			simple = NewBinaryNode(tokenArgList, simple, word)
		}
	}
}

func (p *parser) parsePrefixRedir() (*Tree, error) {
	redir, err := p.parseRedir()
	if err != nil {
		return nil, err
	}
	var child *Tree
	if !p.isCommandBoundary(p.current().Kind) {
		child, err = p.parsePrefixed()
		if err != nil {
			return nil, err
		}
	}
	return Mung2(redir, redir.Child[0], child), nil
}

func (p *parser) parseEpilog() (*Tree, error) {
	var epilog *Tree
	for p.current().Kind == tokenRedir || p.current().Kind == tokenDup {
		redir, err := p.parseRedir()
		if err != nil {
			return nil, err
		}
		epilog = Mung2(redir, redir.Child[0], epilog)
	}
	return epilog, nil
}

func (p *parser) parseRedir() (*Tree, error) {
	tok := p.current()
	if tok.Kind != tokenRedir && tok.Kind != tokenDup {
		return nil, p.unexpected("expected redirection")
	}
	node := cloneTree(tok.Tree)
	node.Pos = tok.Pos
	p.next()
	if tok.Kind == tokenDup {
		return node, nil
	}
	word, err := p.parseWord()
	if err != nil {
		return nil, err
	}
	return Mung1(node, word), nil
}

func (p *parser) parseFirst() (*Tree, error) {
	left, err := p.parseComWord()
	if err != nil {
		return nil, err
	}
	for p.current().Kind == '^' {
		op := p.current()
		p.next()
		right, err := p.parseWordAtom()
		if err != nil {
			return nil, err
		}
		left = NewBinaryNode('^', left, right)
		left.Pos = op.Pos
	}
	return left, nil
}

func (p *parser) parseWord() (*Tree, error) {
	left, err := p.parseWordAtom()
	if err != nil {
		return nil, err
	}
	for p.current().Kind == '^' {
		op := p.current()
		p.next()
		right, err := p.parseWordAtom()
		if err != nil {
			return nil, err
		}
		left = NewBinaryNode('^', left, right)
		left.Pos = op.Pos
	}
	return left, nil
}

func (p *parser) parseWordAtom() (*Tree, error) {
	switch p.current().Kind {
	case tokenFor, tokenIn, tokenWhile, tokenIf, tokenNot, tokenTwiddle, tokenBang, tokenSubshell, tokenSwitch, tokenFn:
		word := cloneTree(p.current().Tree)
		word.Type = tokenWord
		word.Pos = p.current().Pos
		p.next()
		return word, nil
	default:
		return p.parseComWord()
	}
}

func (p *parser) parseComWord() (*Tree, error) {
	switch p.current().Kind {
	case '$':
		dollar := p.current()
		p.next()
		name, err := p.parseWordAtom()
		if err != nil {
			return nil, err
		}
		if p.current().Kind == tokenSub {
			subTok := p.current()
			p.next()
			sub, err := p.parseWords(')')
			if err != nil {
				return nil, err
			}
			if err := p.expect(')'); err != nil {
				return nil, err
			}
			node := &Tree{Type: tokenSub, Pos: subTok.Pos}
			return Mung2(node, name, sub), nil
		}
		node := &Tree{Type: '$', Pos: dollar.Pos}
		return Mung1(node, name), nil
	case '"':
		quoteTok := p.current()
		p.next()
		word, err := p.parseWordAtom()
		if err != nil {
			return nil, err
		}
		node := &Tree{Type: '"', Pos: quoteTok.Pos}
		return Mung1(node, word), nil
	case tokenCount:
		countTok := p.current()
		p.next()
		word, err := p.parseWordAtom()
		if err != nil {
			return nil, err
		}
		node := &Tree{Type: tokenCount, Pos: countTok.Pos}
		return Mung1(node, word), nil
	case tokenWord:
		word := cloneTree(p.current().Tree)
		word.Pos = p.current().Pos
		p.next()
		return word, nil
	case '`':
		backquote := p.current()
		p.next()
		brace, err := p.parseBrace()
		if err != nil {
			return nil, err
		}
		node := &Tree{Type: '`', Pos: backquote.Pos}
		return Mung1(node, brace), nil
	case '(':
		open := p.current()
		p.next()
		words, err := p.parseWords(')')
		if err != nil {
			return nil, err
		}
		if err := p.expect(')'); err != nil {
			return nil, err
		}
		node := NewUnaryNode(tokenParen, words)
		node.Pos = open.Pos
		return node, nil
	case tokenRedir:
		redir := cloneTree(p.current().Tree)
		redir.Pos = p.current().Pos
		p.next()
		brace, err := p.parseBrace()
		if err != nil {
			return nil, err
		}
		redir.Type = tokenPipeFD
		return Mung1(redir, brace), nil
	}
	return nil, p.unexpected("expected word")
}

func (p *parser) parseWords(stops ...NodeType) (*Tree, error) {
	var words *Tree
	for !containsToken(stops, p.current().Kind) && p.current().Kind != tokenEOF {
		if !p.canStartWord() {
			break
		}
		word, err := p.parseWord()
		if err != nil {
			return nil, err
		}
		words = NewBinaryNode(tokenWords, words, word)
	}
	return words, nil
}

func (p *parser) canStartFirst() bool {
	switch p.current().Kind {
	case '$', '"', tokenCount, tokenWord, '`', '(':
		return true
	}
	return p.current().Kind == tokenRedir && p.nextKind() == '{'
}

func (p *parser) canStartWord() bool {
	switch p.current().Kind {
	case tokenFor, tokenIn, tokenWhile, tokenIf, tokenNot, tokenTwiddle, tokenBang, tokenSubshell, tokenSwitch, tokenFn:
		return true
	}
	return p.canStartFirst() || p.current().Kind == tokenRedir
}

func (p *parser) isCommandBoundary(kind NodeType) bool {
	switch kind {
	case tokenEOF, '\n', ';', '&', ')', '}':
		return true
	}
	return false
}

func (p *parser) skipSeparators() {
	for {
		switch p.current().Kind {
		case ';', '\n':
			p.next()
		default:
			return
		}
	}
}

func (p *parser) skipNewlines() {
	for p.current().Kind == '\n' {
		p.next()
	}
}

func (p *parser) current() LexToken {
	if p.pos >= len(p.tokens) {
		return LexToken{Kind: tokenEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) nextKind() NodeType {
	if p.pos+1 >= len(p.tokens) {
		return tokenEOF
	}
	return p.tokens[p.pos+1].Kind
}

func (p *parser) next() {
	if p.pos < len(p.tokens) {
		p.pos++
	}
}

func (p *parser) expect(kind NodeType) error {
	if p.current().Kind != kind {
		want := tokenName(kind)
		if kind >= 0 && kind < 128 {
			want = fmt.Sprintf("%q", string(rune(kind)))
		}
		return p.unexpected("expected " + want)
	}
	p.next()
	return nil
}

func (p *parser) unexpected(msg string) error {
	tok := p.current()
	text := tok.Text
	if text == "" {
		text = tokenName(tok.Kind)
	}
	return fmt.Errorf("line %d:%d: %s near %s", tok.Pos.Line, tok.Pos.Column, msg, strconvQuote(text))
}

func strconvQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
