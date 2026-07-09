package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// ErrInterrupted is returned when the user presses Ctrl+C.
var ErrInterrupted = errors.New("interrupted")

// Editor manages the interactive line reading and history state.
type Editor struct {
	history []string
	skipLF  bool
}

var (
	bracketedPasteStart = []byte{27, '[', '2', '0', '0', '~'}
	bracketedPasteEnd   = []byte{27, '[', '2', '0', '1', '~'}
)

type editorEscapeAction int

const (
	escapeUnhandled editorEscapeAction = iota
	escapeIncomplete
	escapeDelete
	escapeCursorRight
	escapeCursorLeft
	escapeHistoryUp
	escapeHistoryDown
	escapePasteStart
	escapePasteEnd
)

// NewEditor initializes and returns a new Editor.
func NewEditor() *Editor {
	return &Editor{}
}

// AddHistory appends a line to the history buffer, avoiding consecutive duplicates.
func (e *Editor) AddHistory(line string) {
	if line == "" {
		return
	}
	if len(e.history) > 0 && e.history[len(e.history)-1] == line {
		return
	}
	e.history = append(e.history, line)
}

// ReadLine puts the terminal in raw mode and reads an interactive line of input.
func (e *Editor) ReadLine(prompt string, cwd string) (string, error) {
	fd := int(os.Stdin.Fd())

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, oldState)
	enableBracketedPaste()
	defer disableBracketedPaste()

	var buf []rune
	pos := 0
	histIdx := len(e.history)
	cols := editorColumns(fd)
	oldRows := 1
	oldCursorRow := 0
	oldCursorCol := len(prompt)

	refresh := func() {
		rows, cursorRow, cursorCol := editorCursorLayout(prompt, buf, pos, cols)

		var out strings.Builder
		out.WriteString(editorClearPrefix(oldRows, oldCursorRow))
		out.WriteString(editorDisplayText(prompt, buf))
		if rows-1 > cursorRow {
			fmt.Fprintf(&out, "\x1b[%dA", rows-1-cursorRow)
		}
		out.WriteString("\r")
		if cursorCol > 0 {
			fmt.Fprintf(&out, "\x1b[%dC", cursorCol)
		}

		fmt.Print(out.String())
		oldRows = rows
		oldCursorRow = cursorRow
		oldCursorCol = cursorCol
	}

	refresh()

	b := make([]byte, 1)
	var escSeq []byte
	var utf8buf []byte
	insertRune := func(r rune) {
		buf = append(buf[:pos], append([]rune{r}, buf[pos:]...)...)
		pos++
		refresh()
	}
	moveCursor := func(newPos int) {
		rows, cursorRow, cursorCol := editorCursorLayout(prompt, buf, newPos, cols)
		fmt.Print(editorCursorMove(oldCursorRow, oldCursorCol, cursorRow, cursorCol))
		pos = newPos
		oldRows = rows
		oldCursorRow = cursorRow
		oldCursorCol = cursorCol
	}

	for {
		n, err := os.Stdin.Read(b)
		if err != nil || n == 0 {
			return "", io.EOF
		}
		c := b[0]
		if e.shouldIgnoreByte(c, buf) {
			continue
		}

		if len(escSeq) > 0 {
			escSeq = append(escSeq, c)
			switch classifyEscapeSequence(escSeq) {
			case escapeIncomplete:
				continue
			case escapeDelete:
				if pos < len(buf) {
					buf = append(buf[:pos], buf[pos+1:]...)
					refresh()
				}
			case escapeCursorRight:
				if pos < len(buf) {
					moveCursor(pos + 1)
				}
			case escapeCursorLeft:
				if pos > 0 {
					moveCursor(pos - 1)
				}
			case escapeHistoryUp:
				if histIdx > 0 {
					histIdx--
					buf = []rune(e.history[histIdx])
					pos = len(buf)
					refresh()
				}
			case escapeHistoryDown:
				if histIdx < len(e.history)-1 {
					histIdx++
					buf = []rune(e.history[histIdx])
					pos = len(buf)
					refresh()
				} else if histIdx == len(e.history)-1 {
					histIdx++
					buf = []rune{}
					pos = 0
					refresh()
				}
			case escapePasteStart:
				pasted, err := readBracketedPaste(os.Stdin)
				if err != nil {
					return "", io.EOF
				}
				pasted = normalizePastedText(pasted)
				if len(pasted) == 0 {
					escSeq = nil
					continue
				}

				text := string(pasted)
				combined := string(buf[:pos]) + text + string(buf[pos:])
				if shouldSubmitPastedText(text) {
					fmt.Print(displayPastedText(text))
					return trimTrailingSubmittedNewline(combined), nil
				}

				buf = []rune(combined)
				pos += utf8.RuneCountInString(text)
				refresh()
			}
			escSeq = nil
			continue
		}
		if c == 27 { // Esc
			escSeq = append(escSeq, c)
			continue
		}

		switch c {
		case 3: // Ctrl-C
			return "", ErrInterrupted
		case 4: // Ctrl-D
			if len(buf) == 0 {
				return "", io.EOF
			}
		case 10, 13: // LF or CR
			e.noteLineEnding(c)
			moveCursor(len(buf))
			fmt.Print("\r\n")
			return string(buf), nil
		case 127, 8: // Backspace
			if pos > 0 {
				buf = append(buf[:pos-1], buf[pos:]...)
				pos--
				refresh()
			}
		case 1: // Ctrl-A
			moveCursor(0)
		case 5: // Ctrl-E
			moveCursor(len(buf))
		case 12: // Ctrl-L
			fmt.Print("\x1b[H\x1b[2J")
			refresh()
		case 21: // Ctrl-U
			buf = buf[pos:]
			pos = 0
			refresh()
		case 11: // Ctrl-K
			buf = buf[:pos]
			refresh()
		case 22: // Ctrl-V
			term.Restore(fd, oldState)
			f, err := os.CreateTemp("", "rc-edit-*.rc")
			if err == nil {
				f.WriteString(string(buf))
				f.Close()

				editorCmd := os.Getenv("VISUAL")
				if editorCmd == "" {
					editorCmd = os.Getenv("EDITOR")
				}
				if editorCmd == "" {
					editorCmd = "vi"
				}

				cmd := exec.Command(editorCmd, f.Name())
				cmd.Stdin = os.Stdin
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				cmd.Run()

				content, _ := os.ReadFile(f.Name())
				os.Remove(f.Name())

				s := string(content)
				if strings.HasSuffix(s, "\n") {
					s = s[:len(s)-1]
				}
				buf = []rune(s)
				pos = len(buf)
			}
			term.MakeRaw(fd)
			fmt.Print("\r\n")
			refresh()
		case 9: // Tab
			wordStart := pos
			for wordStart > 0 && buf[wordStart-1] != ' ' {
				wordStart--
			}
			prefix := string(buf[wordStart:pos])
			matches := globMatches(prefix, cwd)

			if len(matches) > 0 {
				common := longestCommonPrefix(matches)
				if len(common) > len(prefix) {
					newRunes := []rune(common[len(prefix):])
					buf = append(buf[:pos], append(newRunes, buf[pos:]...)...)
					pos += len(newRunes)
					refresh()
				} else if len(matches) > 1 {
					fmt.Print("\r\n")
					for _, m := range matches {
						fmt.Print(m, "  ")
					}
					fmt.Print("\r\n")
					refresh()
				}
			}
		default:
			if c >= 32 {
				utf8buf = append(utf8buf, c)
				if utf8.FullRune(utf8buf) {
					r, _ := utf8.DecodeRune(utf8buf)
					utf8buf = nil
					insertRune(r)
				}
			}
		}
	}
}

func enableBracketedPaste() {
	_, _ = os.Stdout.WriteString("\x1b[?2004h")
}

func disableBracketedPaste() {
	_, _ = os.Stdout.WriteString("\x1b[?2004l")
}

func classifyEscapeSequence(seq []byte) editorEscapeAction {
	if len(seq) == 0 || seq[0] != 27 {
		return escapeUnhandled
	}

	if bytes.Equal(seq, bracketedPasteStart[:min(len(seq), len(bracketedPasteStart))]) {
		if len(seq) == len(bracketedPasteStart) {
			return escapePasteStart
		}
		return escapeIncomplete
	}
	if bytes.Equal(seq, bracketedPasteEnd[:min(len(seq), len(bracketedPasteEnd))]) {
		if len(seq) == len(bracketedPasteEnd) {
			return escapePasteEnd
		}
		return escapeIncomplete
	}
	if bytes.Equal(seq, []byte{27, '[', '3', '~'}) {
		return escapeDelete
	}
	if bytes.Equal(seq, []byte{27, '[', '3'}) {
		return escapeIncomplete
	}
	if len(seq) == 3 && seq[1] == '[' {
		switch seq[2] {
		case 'C':
			return escapeCursorRight
		case 'D':
			return escapeCursorLeft
		case 'A':
			return escapeHistoryUp
		case 'B':
			return escapeHistoryDown
		}
	}

	if len(seq) == 1 {
		return escapeIncomplete
	}

	switch seq[1] {
	case '[':
		if len(seq) > 2 && seq[len(seq)-1] >= 0x40 && seq[len(seq)-1] <= 0x7E {
			return escapeUnhandled
		}
		return escapeIncomplete
	case ']':
		if seq[len(seq)-1] == '\a' {
			return escapeUnhandled
		}
		if len(seq) >= 3 && seq[len(seq)-2] == 27 && seq[len(seq)-1] == '\\' {
			return escapeUnhandled
		}
		return escapeIncomplete
	case 'O':
		if len(seq) == 3 {
			return escapeUnhandled
		}
		return escapeIncomplete
	case 'P', 'X', '^', '_':
		if seq[len(seq)-1] == '\a' {
			return escapeUnhandled
		}
		if len(seq) >= 3 && seq[len(seq)-2] == 27 && seq[len(seq)-1] == '\\' {
			return escapeUnhandled
		}
		return escapeIncomplete
	}

	return escapeUnhandled
}

func readBracketedPaste(r io.Reader) ([]byte, error) {
	var pasted []byte
	pending := make([]byte, 0, len(bracketedPasteEnd))
	b := make([]byte, 1)

	for {
		n, err := r.Read(b)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, io.EOF
		}

		pending = append(pending, b[0])
		if len(pending) > len(bracketedPasteEnd) {
			pasted = append(pasted, pending[0])
			pending = pending[1:]
		}

		switch {
		case bytes.Equal(pending, bracketedPasteEnd):
			return pasted, nil
		case bytes.Equal(pending, bracketedPasteEnd[:len(pending)]):
			continue
		default:
			pasted = append(pasted, pending...)
			pending = pending[:0]
		}
	}
}

func normalizePastedText(p []byte) []byte {
	out := p[:0]
	for i := 0; i < len(p); i++ {
		if p[i] != '\r' {
			out = append(out, p[i])
			continue
		}

		out = append(out, '\n')
		if i+1 < len(p) && p[i+1] == '\n' {
			i++
		}
	}
	return out
}

func trimTrailingSubmittedNewline(s string) string {
	return strings.TrimSuffix(s, "\n")
}

func displayPastedText(s string) string {
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func shouldSubmitPastedText(s string) bool {
	return strings.HasSuffix(s, "\n")
}

func editorColumns(fd int) int {
	cols, _, err := term.GetSize(fd)
	if err != nil || cols <= 0 {
		return 80
	}
	return cols
}

func editorDisplayText(prompt string, buf []rune) string {
	return prompt + displayPastedText(string(buf))
}

func editorClearPrefix(oldRows, oldCursorRow int) string {
	var out strings.Builder
	if down := oldRows - 1 - oldCursorRow; down > 0 {
		fmt.Fprintf(&out, "\r\x1b[%dB", down)
	} else {
		out.WriteString("\r")
	}
	for i := 0; i < oldRows-1; i++ {
		out.WriteString("\r\x1b[0K\x1b[1A")
	}
	out.WriteString("\r\x1b[0K")
	return out.String()
}

func editorCursorMove(fromRow, fromCol, toRow, toCol int) string {
	var out strings.Builder
	switch {
	case toRow < fromRow:
		fmt.Fprintf(&out, "\x1b[%dA", fromRow-toRow)
	case toRow > fromRow:
		fmt.Fprintf(&out, "\x1b[%dB", toRow-fromRow)
	}
	out.WriteString("\r")
	if toCol > 0 {
		fmt.Fprintf(&out, "\x1b[%dC", toCol)
	}
	return out.String()
}

func editorCursorLayout(prompt string, buf []rune, pos, cols int) (rows, cursorRow, cursorCol int) {
	if cols <= 0 {
		cols = 80
	}

	row, col := 0, 0
	advance := func(r rune) {
		if r == '\n' {
			row++
			col = 0
			return
		}
		col++
		if col >= cols {
			row++
			col = 0
		}
	}

	for _, r := range prompt {
		advance(r)
	}
	cursorRow, cursorCol = row, col
	for i, r := range buf {
		if i == pos {
			cursorRow, cursorCol = row, col
		}
		advance(r)
	}
	if pos == len(buf) {
		cursorRow, cursorCol = row, col
	}
	return row + 1, cursorRow, cursorCol
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (e *Editor) shouldIgnoreByte(c byte, buf []rune) bool {
	if c == 10 && e.skipLF && len(buf) == 0 {
		e.skipLF = false
		return true
	}
	e.skipLF = false
	return false
}

func (e *Editor) noteLineEnding(c byte) {
	e.skipLF = c == 13
}

func globMatches(prefix, cwd string) []string {
	if cwd == "" {
		return nil
	}
	rcPattern := prefix + string(globMark) + "*"
	matches, err := globPaths(rcPattern, cwd)
	if err != nil {
		return nil
	}

	deglobbed := deglob(rcPattern)
	var results []string
	for _, m := range matches {
		if len(matches) == 1 && m == deglobbed {
			if _, err := os.Stat(m); os.IsNotExist(err) {
				continue
			}
		}
		if stat, err := os.Stat(m); err == nil && stat.IsDir() {
			results = append(results, m+"/")
		} else {
			results = append(results, m)
		}
	}
	return results
}

func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		for !strings.HasPrefix(s, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
}
