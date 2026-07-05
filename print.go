package main

import (
	"strconv"
	"strings"
)

// FormatTree returns a readable string representation of an AST.
func FormatTree(t *Tree) string {
	var b strings.Builder
	writeTree(&b, t)
	return b.String()
}

func writeTree(b *strings.Builder, t *Tree) {
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
		writeTree(b, c0)
	case '"':
		b.WriteString("$\"")
		writeTree(b, c0)
	case '&':
		writeTree(b, c0)
		b.WriteByte('&')
	case '^':
		writeTree(b, c0)
		b.WriteByte('^')
		writeTree(b, c1)
	case '`':
		b.WriteByte('`')
		writeTree(b, c0)
	case tokenAndAnd:
		writeTree(b, c0)
		b.WriteString(" && ")
		writeTree(b, c1)
	case tokenBang:
		b.WriteString("! ")
		writeTree(b, c0)
	case tokenBrace:
		b.WriteByte('{')
		writeTree(b, c0)
		b.WriteByte('}')
	case tokenCount:
		b.WriteString("$#")
		writeTree(b, c0)
	case tokenFn:
		b.WriteString("fn ")
		writeTree(b, c0)
		if c1 != nil {
			b.WriteByte(' ')
			writeTree(b, c1)
		}
	case tokenIf:
		b.WriteString("if")
		writeTree(b, c0)
		writeTree(b, c1)
	case tokenNot:
		b.WriteString("if not ")
		writeTree(b, c0)
	case tokenOrOr:
		writeTree(b, c0)
		b.WriteString(" || ")
		writeTree(b, c1)
	case tokenPCmd, tokenParen:
		b.WriteByte('(')
		writeTree(b, c0)
		b.WriteByte(')')
	case tokenSub:
		b.WriteByte('$')
		writeTree(b, c0)
		b.WriteByte('(')
		writeTree(b, c1)
		b.WriteByte(')')
	case tokenSimple:
		writeTree(b, c0)
	case tokenSubshell:
		b.WriteString("@ ")
		writeTree(b, c0)
	case tokenSwitch:
		b.WriteString("switch ")
		writeTree(b, c0)
		b.WriteByte(' ')
		writeTree(b, c1)
	case tokenTwiddle:
		b.WriteString("~ ")
		writeTree(b, c0)
		if c1 != nil {
			b.WriteByte(' ')
			writeTree(b, c1)
		}
	case tokenWhile:
		b.WriteString("while ")
		writeTree(b, c0)
		writeTree(b, c1)
	case tokenArgList:
		if c0 == nil {
			writeTree(b, c1)
		} else if c1 == nil {
			writeTree(b, c0)
		} else {
			writeTree(b, c0)
			b.WriteByte(' ')
			writeTree(b, c1)
		}
	case ';':
		if c0 != nil {
			writeTree(b, c0)
			if c1 != nil {
				b.WriteByte('\n')
				writeTree(b, c1)
			}
		} else {
			writeTree(b, c1)
		}
	case tokenWords:
		if c0 != nil {
			writeTree(b, c0)
			b.WriteByte(' ')
		}
		writeTree(b, c1)
	case tokenFor:
		b.WriteString("for(")
		writeTree(b, c0)
		if c1 != nil {
			b.WriteString(" in ")
			writeTree(b, c1)
		}
		b.WriteByte(')')
		writeTree(b, c2)
	case tokenWord:
		if t.Quoted {
			b.WriteString(rcQuote(t.Str))
		} else {
			b.WriteString(deglobString(t.Str))
		}
	case tokenDup:
		b.WriteString(">[")
		b.WriteString(strconv.Itoa(t.FD1))
		b.WriteByte('=')
		if t.RType == redirDupFD {
			b.WriteString(strconv.Itoa(t.FD0))
		}
		b.WriteByte(']')
		writeTree(b, c1)
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
		writeTree(b, c0)
		if c1 != nil {
			b.WriteByte(' ')
			writeTree(b, c1)
		}
	case '=':
		writeTree(b, c0)
		b.WriteByte('=')
		writeTree(b, c1)
		if c2 != nil {
			b.WriteByte(' ')
			writeTree(b, c2)
		}
	case tokenPipe:
		writeTree(b, c0)
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
		writeTree(b, c1)
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
