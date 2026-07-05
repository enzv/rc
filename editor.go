package main

import (
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
}

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

	var buf []rune
	pos := 0
	histIdx := len(e.history)

	refresh := func() {
		fmt.Printf("\r\x1b[0K%s%s", prompt, string(buf))
		if diff := len(buf) - pos; diff > 0 {
			fmt.Printf("\x1b[%dD", diff)
		}
	}

	refresh()

	b := make([]byte, 1)
	var escSeq []byte
	var utf8buf []byte

	for {
		n, err := os.Stdin.Read(b)
		if err != nil || n == 0 {
			return "", io.EOF
		}
		c := b[0]

		if len(escSeq) > 0 {
			escSeq = append(escSeq, c)
			if len(escSeq) == 3 && escSeq[0] == 27 && escSeq[1] == '[' {
				switch escSeq[2] {
				case 'C': // Right
					if pos < len(buf) {
						pos++
						refresh()
					}
				case 'D': // Left
					if pos > 0 {
						pos--
						refresh()
					}
				case 'A': // Up
					if histIdx > 0 {
						histIdx--
						buf = []rune(e.history[histIdx])
						pos = len(buf)
						refresh()
					}
				case 'B': // Down
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
				}
				escSeq = nil
			} else if len(escSeq) > 3 {
				escSeq = nil // abort sequence
			}
			continue
		}

		switch c {
		case 3: // Ctrl-C
			return "", ErrInterrupted
		case 4: // Ctrl-D
			if len(buf) == 0 {
				return "", io.EOF
			}
		case 13: // Enter
			fmt.Print("\r\n")
			return string(buf), nil
		case 127, 8: // Backspace
			if pos > 0 {
				buf = append(buf[:pos-1], buf[pos:]...)
				pos--
				refresh()
			}
		case 27: // Esc
			escSeq = append(escSeq, c)
		case 1: // Ctrl-A
			pos = 0
			refresh()
		case 5: // Ctrl-E
			pos = len(buf)
			refresh()
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
					buf = append(buf[:pos], append([]rune{r}, buf[pos:]...)...)
					pos++
					refresh()
				}
			}
		}
	}
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
