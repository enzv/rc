package main

import (
	"strconv"
	"strings"
)

// FormatTree returns a readable string representation of an AST root.
func FormatTree(prog *Program, root int) string {
	var b strings.Builder
	writeTree(&b, prog, root)
	return b.String()
}

func writeTree(b *strings.Builder, prog *Program, id int) {
	if prog == nil || id < 0 {
		return
	}
	t := prog.Node(id)
	if t == nil {
		return
	}
	c0, c1, c2 := t.Child[0], t.Child[1], t.Child[2]
	switch t.Type {
	default:
		b.WriteString("bad cmd ")
		b.WriteString(strconv.Itoa(int(t.Type)))
	case '$':
		b.WriteByte('$')
		writeTree(b, prog, c0)
	case '"':
		b.WriteString("$\"")
		writeTree(b, prog, c0)
	case '&':
		writeTree(b, prog, c0)
		b.WriteByte('&')
	case '^':
		writeTree(b, prog, c0)
		b.WriteByte('^')
		writeTree(b, prog, c1)
	case '`':
		b.WriteByte('`')
		writeTree(b, prog, c0)
	case tokenAndAnd:
		writeTree(b, prog, c0)
		b.WriteString(" && ")
		writeTree(b, prog, c1)
	case tokenBang:
		b.WriteString("! ")
		writeTree(b, prog, c0)
	case tokenBrace:
		b.WriteByte('{')
		writeTree(b, prog, c0)
		b.WriteByte('}')
	case tokenCount:
		b.WriteString("$#")
		writeTree(b, prog, c0)
	case tokenFn:
		b.WriteString("fn ")
		writeTree(b, prog, c0)
		if c1 >= 0 {
			b.WriteByte(' ')
			writeTree(b, prog, c1)
		}
	case tokenIf:
		b.WriteString("if")
		writeTree(b, prog, c0)
		writeTree(b, prog, c1)
	case tokenNot:
		b.WriteString("if not ")
		writeTree(b, prog, c0)
	case tokenOrOr:
		writeTree(b, prog, c0)
		b.WriteString(" || ")
		writeTree(b, prog, c1)
	case tokenPCmd, tokenParen:
		b.WriteByte('(')
		writeTree(b, prog, c0)
		b.WriteByte(')')
	case tokenSub:
		b.WriteByte('$')
		writeTree(b, prog, c0)
		b.WriteByte('(')
		writeTree(b, prog, c1)
		b.WriteByte(')')
	case tokenSimple:
		writeTree(b, prog, c0)
	case tokenSubshell:
		b.WriteString("@ ")
		writeTree(b, prog, c0)
	case tokenSwitch:
		b.WriteString("switch ")
		writeTree(b, prog, c0)
		b.WriteByte(' ')
		writeTree(b, prog, c1)
	case tokenTwiddle:
		b.WriteString("~ ")
		writeTree(b, prog, c0)
		if c1 >= 0 {
			b.WriteByte(' ')
			writeTree(b, prog, c1)
		}
	case tokenWhile:
		b.WriteString("while ")
		writeTree(b, prog, c0)
		writeTree(b, prog, c1)
	case tokenArgList:
		if c0 < 0 {
			writeTree(b, prog, c1)
		} else if c1 < 0 {
			writeTree(b, prog, c0)
		} else {
			writeTree(b, prog, c0)
			b.WriteByte(' ')
			writeTree(b, prog, c1)
		}
	case ';':
		if c0 >= 0 {
			writeTree(b, prog, c0)
			if c1 >= 0 {
				b.WriteByte('\n')
				writeTree(b, prog, c1)
			}
		} else {
			writeTree(b, prog, c1)
		}
	case tokenWords:
		if c0 >= 0 {
			writeTree(b, prog, c0)
			b.WriteByte(' ')
		}
		writeTree(b, prog, c1)
	case tokenFor:
		b.WriteString("for(")
		writeTree(b, prog, c0)
		if c1 >= 0 {
			b.WriteString(" in ")
			writeTree(b, prog, c1)
		}
		b.WriteByte(')')
		writeTree(b, prog, c2)
	case tokenWord:
		tok := prog.Token(t.Tok)
		if tok == nil {
			b.WriteString("bad word")
			return
		}
		if tok.Quoted {
			b.WriteString(rcQuote(tok.Text))
		} else {
			b.WriteString(tok.Text)
		}
	case tokenDup:
		b.WriteString(">[")
		b.WriteString(strconv.Itoa(t.FD1))
		b.WriteByte('=')
		if t.RType == redirDupFD {
			b.WriteString(strconv.Itoa(t.FD0))
		}
		b.WriteByte(']')
		writeTree(b, prog, c1)
	case tokenPipeFD, tokenRedir:
		switch t.RType {
		case redirHere:
			b.WriteByte('<')
			fallthrough
		case redirRead, redirRDWR:
			b.WriteByte('<')
			if t.RType == redirRDWR {
				b.WriteByte('>')
			}
			if t.FD0 != 0 {
				b.WriteByte('[')
				b.WriteString(strconv.Itoa(t.FD0))
				b.WriteByte(']')
			}
		case redirAppend:
			b.WriteByte('>')
			fallthrough
		case redirWrite:
			b.WriteByte('>')
			if t.FD0 != 1 {
				b.WriteByte('[')
				b.WriteString(strconv.Itoa(t.FD0))
				b.WriteByte(']')
			}
		}
		writeTree(b, prog, c0)
		if c1 >= 0 {
			b.WriteByte(' ')
			writeTree(b, prog, c1)
		}
	case '=':
		writeTree(b, prog, c0)
		b.WriteByte('=')
		writeTree(b, prog, c1)
		if c2 >= 0 {
			b.WriteByte(' ')
			writeTree(b, prog, c2)
		}
	case tokenPipe:
		writeTree(b, prog, c0)
		b.WriteByte('|')
		if t.FD1 == 0 {
			if t.FD0 != 1 {
				b.WriteByte('[')
				b.WriteString(strconv.Itoa(t.FD0))
				b.WriteByte(']')
			}
		} else {
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(t.FD0))
			b.WriteByte('=')
			b.WriteString(strconv.Itoa(t.FD1))
			b.WriteByte(']')
		}
		writeTree(b, prog, c1)
	}
}

func deglobString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == globMark {
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func rcQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
