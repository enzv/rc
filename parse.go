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
	prog, err := parseTokens(tokens)
	if err != nil {
		return nil, err
	}
	attachHereDocs(prog, prepared.HereDocs)
	return prog, nil
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
	var pending []pendingHereDoc
	var docs []hereDoc
	var out strings.Builder
	var dirty bool
	last := 0
	inQuote := false
	inComment := false
	flush := func(next int) {
		if !dirty {
			dirty = true
			out.Grow(len(src))
			out.WriteString(src[:next])
		} else {
			out.WriteString(src[last:next])
		}
		last = next
	}

	for i := 0; i < len(src); {
		ch := src[i]
		if inComment {
			i++
			if ch == '\n' {
				inComment = false
				if len(pending) != 0 {
					flush(i)
					next, found := collectHereDocs(src, i, pending)
					docs = append(docs, found...)
					pending = nil
					i = next
					last = next
				}
			}
			continue
		}
		if inQuote {
			i++
			if ch == '\'' {
				if i < len(src) && src[i] == '\'' {
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
				i += 2
				continue
			}
			i++
		case '\n':
			i++
			if len(pending) != 0 {
				flush(i)
				next, found := collectHereDocs(src, i, pending)
				docs = append(docs, found...)
				pending = nil
				i = next
				last = next
			}
		default:
			i++
		}
	}
	if dirty {
		out.WriteString(src[last:])
		return preparedSource{
			Stripped: out.String(),
			HereDocs: docs,
		}
	}
	return preparedSource{
		Stripped: src,
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

func attachHereDocs(prog *Program, docs []hereDoc) {
	if prog == nil || len(docs) == 0 || prog.Root < 0 {
		return
	}
	if prog.HereDocs == nil {
		prog.HereDocs = make(map[int]hereDoc, len(docs))
	}
	index := 0
	visited := make([]bool, len(prog.Nodes))
	stack := make([]int, 0, len(prog.Nodes))
	stack = append(stack, prog.Root)
	for len(stack) > 0 && index < len(docs) {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id < 0 || id >= len(prog.Nodes) || visited[id] {
			continue
		}
		visited[id] = true
		node := &prog.Nodes[id]
		if node.Type == tokenRedir && node.RType == redirHere {
			prog.HereDocs[id] = docs[index]
			index++
		}
		if node.Child[2] >= 0 {
			stack = append(stack, node.Child[2])
		}
		if node.Child[1] >= 0 {
			stack = append(stack, node.Child[1])
		}
		if node.Child[0] >= 0 {
			stack = append(stack, node.Child[0])
		}
	}
}

type parser struct {
	tokens  []LexToken
	pos     int
	nodes   []Node
	offsets []int
}

func containsToken(stops []NodeType, kind NodeType) bool {
	for _, stop := range stops {
		if stop == kind {
			return true
		}
	}
	return false
}

func parseTokens(tokens []LexToken) (*Program, error) {
	p := &parser{
		tokens:  tokens,
		nodes:   make([]Node, 0, len(tokens)),
		offsets: make([]int, 0, len(tokens)),
	}
	root, err := p.parseList(tokenEOF)
	if err != nil {
		return nil, err
	}
	if p.current().Kind != tokenEOF {
		return nil, p.unexpected("unexpected trailing token")
	}
	return &Program{Tokens: tokens, Nodes: p.nodes, Offsets: p.offsets, Root: root}, nil
}

func (p *parser) addNode(node Node) int {
	p.nodes = append(p.nodes, node)
	p.offsets = append(p.offsets, 0)
	return len(p.nodes) - 1
}

func (p *parser) newNode(kind NodeType, pos Position) int {
	id := p.addNode(Node{Type: kind, Child: noChildren()})
	p.offsets[id] = pos.Offset
	return id
}

func (p *parser) syntaxNode(tok LexToken) int {
	id := p.addNode(Node{
		Type:  tok.Kind,
		RType: tok.RType,
		FD0:   tok.FD0,
		FD1:   tok.FD1,
		Child: noChildren(),
	})
	p.offsets[id] = tok.Pos.Offset
	return id
}

func (p *parser) wordNode(tok LexToken) int {
	id := p.addNode(Node{
		Type:  tokenWord,
		Tok:   p.pos,
		Child: noChildren(),
	})
	p.offsets[id] = tok.Pos.Offset
	return id
}

func (p *parser) attach(id, c0, c1, c2 int) int {
	p.nodes[id].Child = [3]int{c0, c1, c2}
	return id
}

func (p *parser) combine(kind NodeType, pos Position, c0, c1, c2 int) int {
	if kind == ';' {
		if c0 < 0 {
			return c1
		}
		if c1 < 0 {
			return c0
		}
	}
	return p.attach(p.newNode(kind, pos), c0, c1, c2)
}

func (p *parser) parseList(stops ...NodeType) (int, error) {
	list := -1
	for {
		p.skipSeparators()
		kind := p.current().Kind
		if containsToken(stops, kind) || (kind == tokenEOF && containsToken(stops, tokenEOF)) {
			return list, nil
		}
		cmd, err := p.parseAndOr()
		if err != nil {
			return -1, err
		}
		switch p.current().Kind {
		case '&':
			cmd = p.attach(p.newNode('&', p.current().Pos), cmd, -1, -1)
			p.next()
		}
		list = p.combine(';', Position{}, list, cmd, -1)

		hasSep := false
		switch p.current().Kind {
		case ';', '\n':
			p.next()
			hasSep = true
		}

		if !hasSep && (cmd < 0 || p.nodes[cmd].Type != '&') {
			return list, nil
		}
	}
}

func (p *parser) parseAndOr() (int, error) {
	left, err := p.parsePrefixed()
	if err != nil {
		return -1, err
	}
	for {
		switch p.current().Kind {
		case tokenAndAnd, tokenOrOr:
			op := p.current()
			p.next()
			right, err := p.parsePrefixed()
			if err != nil {
				return -1, err
			}
			left = p.combine(op.Kind, op.Pos, left, right, -1)
		default:
			return left, nil
		}
	}
}

func (p *parser) parsePrefixed() (int, error) {
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
			return -1, err
		}
		return p.attach(p.syntaxNode(op), child, -1, -1), nil
	}
	if p.canStartFirst() {
		save := p.pos
		left, err := p.parseFirst()
		if err == nil && p.current().Kind == '=' {
			assignTok := p.current()
			p.next()
			right, err := p.parseWord()
			if err != nil {
				return -1, err
			}
			child := -1
			if !p.isCommandBoundary(p.current().Kind) {
				child, err = p.parsePrefixed()
				if err != nil {
					return -1, err
				}
			}
			return p.attach(p.newNode('=', assignTok.Pos), left, right, child), nil
		}
		p.pos = save
	}
	return p.parsePipe()
}

func (p *parser) parsePipe() (int, error) {
	left, err := p.parsePrimaryCommand()
	if err != nil {
		return -1, err
	}
	for p.current().Kind == tokenPipe {
		op := p.syntaxNode(p.current())
		p.next()
		right, err := p.parsePrefixed()
		if err != nil {
			return -1, err
		}
		left = p.attach(op, left, right, -1)
	}
	return left, nil
}

func (p *parser) parsePrimaryCommand() (int, error) {
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

func (p *parser) parseBraceWithEpilog() (int, error) {
	brace, err := p.parseBrace()
	if err != nil {
		return -1, err
	}
	epilog, err := p.parseEpilog()
	if err != nil {
		return -1, err
	}
	return p.combineEpilog(brace, epilog), nil
}

func (p *parser) parseBrace() (int, error) {
	open := p.current()
	if err := p.expect('{'); err != nil {
		return -1, err
	}
	body, err := p.parseList('}')
	if err != nil {
		return -1, err
	}
	if err := p.expect('}'); err != nil {
		return -1, err
	}
	return p.attach(p.newNode(tokenBrace, open.Pos), body, -1, -1), nil
}

func (p *parser) parseParenCommand() (int, error) {
	open := p.current()
	if err := p.expect('('); err != nil {
		return -1, err
	}
	body, err := p.parseList(')')
	if err != nil {
		return -1, err
	}
	if err := p.expect(')'); err != nil {
		return -1, err
	}
	return p.attach(p.newNode(tokenPCmd, open.Pos), body, -1, -1), nil
}

func (p *parser) parseIf() (int, error) {
	ifTok := p.current()
	if err := p.expect(tokenIf); err != nil {
		return -1, err
	}
	if p.current().Kind == tokenNot {
		notTok := p.current()
		p.next()
		p.skipNewlines()
		child, err := p.parseAndOr()
		if err != nil {
			return -1, err
		}
		node := p.syntaxNode(notTok)
		p.offsets[node] = ifTok.Pos.Offset
		return p.attach(node, child, -1, -1), nil
	}
	cond, err := p.parseParenCommand()
	if err != nil {
		return -1, err
	}
	p.skipNewlines()
	body, err := p.parseAndOr()
	if err != nil {
		return -1, err
	}
	return p.attach(p.newNode(tokenIf, ifTok.Pos), cond, body, -1), nil
}

func (p *parser) parseFor() (int, error) {
	forTok := p.current()
	if err := p.expect(tokenFor); err != nil {
		return -1, err
	}
	if err := p.expect('('); err != nil {
		return -1, err
	}
	name, err := p.parseWord()
	if err != nil {
		return -1, err
	}
	words := -1
	if p.current().Kind == tokenIn {
		p.next()
		words, err = p.parseWords(')')
		if err != nil {
			return -1, err
		}
		if words < 0 {
			words = p.newNode(tokenParen, p.current().Pos)
		}
	}
	if err := p.expect(')'); err != nil {
		return -1, err
	}
	p.skipNewlines()
	body, err := p.parseAndOr()
	if err != nil {
		return -1, err
	}
	return p.attach(p.newNode(tokenFor, forTok.Pos), name, words, body), nil
}

func (p *parser) parseWhile() (int, error) {
	whileTok := p.current()
	if err := p.expect(tokenWhile); err != nil {
		return -1, err
	}
	cond, err := p.parseParenCommand()
	if err != nil {
		return -1, err
	}
	p.skipNewlines()
	body, err := p.parseAndOr()
	if err != nil {
		return -1, err
	}
	return p.attach(p.newNode(tokenWhile, whileTok.Pos), cond, body, -1), nil
}

func (p *parser) parseSwitch() (int, error) {
	switchTok := p.current()
	if err := p.expect(tokenSwitch); err != nil {
		return -1, err
	}
	arg, err := p.parseWord()
	if err != nil {
		return -1, err
	}
	p.skipNewlines()
	body, err := p.parseBrace()
	if err != nil {
		return -1, err
	}
	return p.attach(p.newNode(tokenSwitch, switchTok.Pos), arg, body, -1), nil
}

func (p *parser) parseFn() (int, error) {
	fnTok := p.current()
	if err := p.expect(tokenFn); err != nil {
		return -1, err
	}
	names, err := p.parseWords('{', '\n', ';', '&', tokenEOF, '}')
	if err != nil {
		return -1, err
	}
	if names < 0 {
		return -1, p.unexpected("fn requires a name")
	}
	body := -1
	if p.current().Kind == '{' {
		body, err = p.parseBrace()
		if err != nil {
			return -1, err
		}
	}
	node := p.newNode(tokenFn, fnTok.Pos)
	if body >= 0 {
		return p.attach(node, names, body, -1), nil
	}
	return p.attach(node, names, -1, -1), nil
}

func (p *parser) parseTwiddle() (int, error) {
	twiddleTok := p.current()
	if err := p.expect(tokenTwiddle); err != nil {
		return -1, err
	}
	subject, err := p.parseWord()
	if err != nil {
		return -1, err
	}
	patterns, err := p.parseWords(';', '&', '\n', ')', '}', tokenEOF)
	if err != nil {
		return -1, err
	}
	return p.attach(p.newNode(tokenTwiddle, twiddleTok.Pos), subject, patterns, -1), nil
}

func (p *parser) parseSimple() (int, error) {
	first, err := p.parseFirst()
	if err != nil {
		return -1, err
	}
	simple := first
	for {
		switch p.current().Kind {
		case tokenRedir, tokenDup:
			if p.current().Kind == tokenRedir && p.nextKind() == '{' {
				word, err := p.parseWord()
				if err != nil {
					return -1, err
				}
				simple = p.combine(tokenArgList, Position{}, simple, word, -1)
				continue
			}
			redir, err := p.parseRedir()
			if err != nil {
				return -1, err
			}
			simple = p.combine(tokenArgList, Position{}, simple, redir, -1)
		default:
			if !p.canStartWord() {
				return p.simpleNode(simple), nil
			}
			word, err := p.parseWord()
			if err != nil {
				return -1, err
			}
			simple = p.combine(tokenArgList, Position{}, simple, word, -1)
		}
	}
}

func (p *parser) parsePrefixRedir() (int, error) {
	redir, err := p.parseRedir()
	if err != nil {
		return -1, err
	}
	child := -1
	if !p.isCommandBoundary(p.current().Kind) {
		child, err = p.parsePrefixed()
		if err != nil {
			return -1, err
		}
	}
	return p.attach(redir, p.nodes[redir].Child[0], child, -1), nil
}

func (p *parser) parseEpilog() (int, error) {
	epilog := -1
	for p.current().Kind == tokenRedir || p.current().Kind == tokenDup {
		redir, err := p.parseRedir()
		if err != nil {
			return -1, err
		}
		epilog = p.attach(redir, p.nodes[redir].Child[0], epilog, -1)
	}
	return epilog, nil
}

func (p *parser) parseRedir() (int, error) {
	tok := p.current()
	if tok.Kind != tokenRedir && tok.Kind != tokenDup {
		return -1, p.unexpected("expected redirection")
	}
	node := p.syntaxNode(tok)
	p.next()
	if tok.Kind == tokenDup {
		return node, nil
	}
	word, err := p.parseWord()
	if err != nil {
		return -1, err
	}
	return p.attach(node, word, -1, -1), nil
}

func (p *parser) parseFirst() (int, error) {
	left, err := p.parseComWord()
	if err != nil {
		return -1, err
	}
	for p.current().Kind == '^' {
		op := p.current()
		p.next()
		right, err := p.parseWordAtom()
		if err != nil {
			return -1, err
		}
		left = p.combine('^', op.Pos, left, right, -1)
	}
	return left, nil
}

func (p *parser) parseWord() (int, error) {
	left, err := p.parseWordAtom()
	if err != nil {
		return -1, err
	}
	for p.current().Kind == '^' {
		op := p.current()
		p.next()
		right, err := p.parseWordAtom()
		if err != nil {
			return -1, err
		}
		left = p.combine('^', op.Pos, left, right, -1)
	}
	return left, nil
}

func (p *parser) parseWordAtom() (int, error) {
	switch p.current().Kind {
	case tokenFor, tokenIn, tokenWhile, tokenIf, tokenNot, tokenTwiddle, tokenBang, tokenSubshell, tokenSwitch, tokenFn:
		word := p.wordNode(p.current())
		p.next()
		return word, nil
	default:
		return p.parseComWord()
	}
}

func (p *parser) parseComWord() (int, error) {
	switch p.current().Kind {
	case '$':
		dollar := p.current()
		p.next()
		name, err := p.parseWordAtom()
		if err != nil {
			return -1, err
		}
		if p.current().Kind == tokenSub {
			subTok := p.current()
			p.next()
			sub, err := p.parseWords(')')
			if err != nil {
				return -1, err
			}
			if err := p.expect(')'); err != nil {
				return -1, err
			}
			return p.attach(p.newNode(tokenSub, subTok.Pos), name, sub, -1), nil
		}
		return p.attach(p.newNode('$', dollar.Pos), name, -1, -1), nil
	case '"':
		quoteTok := p.current()
		p.next()
		word, err := p.parseWordAtom()
		if err != nil {
			return -1, err
		}
		return p.attach(p.newNode('"', quoteTok.Pos), word, -1, -1), nil
	case tokenCount:
		countTok := p.current()
		p.next()
		word, err := p.parseWordAtom()
		if err != nil {
			return -1, err
		}
		return p.attach(p.newNode(tokenCount, countTok.Pos), word, -1, -1), nil
	case tokenWord:
		word := p.wordNode(p.current())
		p.next()
		return word, nil
	case '`':
		backquote := p.current()
		p.next()
		brace, err := p.parseBrace()
		if err != nil {
			return -1, err
		}
		return p.attach(p.newNode('`', backquote.Pos), brace, -1, -1), nil
	case '(':
		open := p.current()
		p.next()
		words, err := p.parseWords(')')
		if err != nil {
			return -1, err
		}
		if err := p.expect(')'); err != nil {
			return -1, err
		}
		return p.attach(p.newNode(tokenParen, open.Pos), words, -1, -1), nil
	case tokenRedir:
		redir := p.syntaxNode(p.current())
		p.next()
		brace, err := p.parseBrace()
		if err != nil {
			return -1, err
		}
		p.nodes[redir].Type = tokenPipeFD
		return p.attach(redir, brace, -1, -1), nil
	}
	return -1, p.unexpected("expected word")
}

func (p *parser) parseWords(stops ...NodeType) (int, error) {
	words := -1
	for !containsToken(stops, p.current().Kind) && p.current().Kind != tokenEOF {
		if !p.canStartWord() {
			break
		}
		word, err := p.parseWord()
		if err != nil {
			return -1, err
		}
		words = p.combine(tokenWords, Position{}, words, word, -1)
	}
	return words, nil
}

func (p *parser) simpleNode(list int) int {
	root := p.attach(p.newNode(tokenSimple, Position{}), list, -1, -1)
	for u := list; u >= 0 && p.nodes[u].Type == tokenArgList; u = p.nodes[u].Child[0] {
		tail := p.nodes[u].Child[1]
		if tail >= 0 && (p.nodes[tail].Type == tokenDup || p.nodes[tail].Type == tokenRedir) {
			p.nodes[tail].Child[1] = root
			root = tail
			p.nodes[u].Child[1] = -1
		}
	}
	return root
}

func (p *parser) combineEpilog(comp, epi int) int {
	if epi < 0 {
		return comp
	}
	node := epi
	for p.nodes[node].Child[1] >= 0 {
		node = p.nodes[node].Child[1]
	}
	p.nodes[node].Child[1] = comp
	return epi
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
