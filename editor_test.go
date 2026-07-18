package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestAddHistory_empty(t *testing.T) {
	e := NewEditor()
	e.AddHistory("")
	if len(e.history) != 0 {
		t.Errorf("expected empty history, got %v", e.history)
	}
}

func TestAddHistory_deduplicatesConsecutive(t *testing.T) {
	e := NewEditor()
	e.AddHistory("ls")
	e.AddHistory("ls")
	e.AddHistory("ls -l")
	e.AddHistory("ls -l")
	e.AddHistory("ls")

	want := []string{"ls", "ls -l", "ls"}
	if len(e.history) != len(want) {
		t.Fatalf("history = %v, want %v", e.history, want)
	}
	for i, v := range want {
		if e.history[i] != v {
			t.Errorf("history[%d] = %q, want %q", i, e.history[i], v)
		}
	}
}

func TestNewEditorForUsesExplicitStreams(t *testing.T) {
	in, err := os.CreateTemp(t.TempDir(), "editor-stdin-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer in.Close()

	var out, errOut bytes.Buffer
	e := NewEditorFor(in, &out, &errOut)
	if e.in != in {
		t.Fatalf("editor input = %v, want %v", e.in, in)
	}
	if e.out != &out {
		t.Fatalf("editor output = %T, want explicit output buffer", e.out)
	}
	if e.errOut != &errOut {
		t.Fatalf("editor errOut = %T, want explicit error buffer", e.errOut)
	}
}

func TestShouldIgnoreByte(t *testing.T) {
	cases := []struct {
		name     string
		skipLF   bool
		c        byte
		buf      []rune
		want     bool
		wantSkip bool
	}{
		{"LF ignored when skipLF set and buf empty", true, 10, nil, true, false},
		{"LF not ignored when buf non-empty", true, 10, []rune("x"), false, false},
		{"LF not ignored when skipLF false", false, 10, nil, false, false},
		{"non-LF byte always passes", false, 'a', nil, false, false},
		{"non-LF clears skipLF", true, 'a', nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &Editor{skipLF: tc.skipLF}
			got := e.shouldIgnoreByte(tc.c, tc.buf)
			if got != tc.want {
				t.Errorf("shouldIgnoreByte(%d) = %v, want %v", tc.c, got, tc.want)
			}
			if e.skipLF != tc.wantSkip {
				t.Errorf("skipLF after = %v, want %v", e.skipLF, tc.wantSkip)
			}
		})
	}
}

func TestNoteLineEnding(t *testing.T) {
	e := &Editor{}
	e.noteLineEnding(13)
	if !e.skipLF {
		t.Error("expected skipLF=true after CR")
	}
	e.noteLineEnding(10)
	if e.skipLF {
		t.Error("expected skipLF=false after LF")
	}
}

func TestClassifyEscapeSequence(t *testing.T) {
	cases := []struct {
		name string
		seq  []byte
		want editorEscapeAction
	}{
		{"nil", nil, escapeUnhandled},
		{"empty", []byte{}, escapeUnhandled},
		{"non-ESC start", []byte{'a'}, escapeUnhandled},
		{"ESC alone", []byte{27}, escapeIncomplete},
		{"ESC [", []byte{27, '['}, escapeIncomplete},
		{"cursor up", []byte{27, '[', 'A'}, escapeHistoryUp},
		{"cursor down", []byte{27, '[', 'B'}, escapeHistoryDown},
		{"cursor right", []byte{27, '[', 'C'}, escapeCursorRight},
		{"cursor left", []byte{27, '[', 'D'}, escapeCursorLeft},
		{"delete partial", []byte{27, '[', '3'}, escapeIncomplete},
		{"delete full", []byte{27, '[', '3', '~'}, escapeDelete},
		{"paste start partial 1", []byte{27, '[', '2'}, escapeIncomplete},
		{"paste start partial 2", []byte{27, '[', '2', '0'}, escapeIncomplete},
		{"paste start partial 3", []byte{27, '[', '2', '0', '0'}, escapeIncomplete},
		{"paste start full", []byte{27, '[', '2', '0', '0', '~'}, escapePasteStart},
		{"paste end partial", []byte{27, '[', '2', '0', '1'}, escapeIncomplete},
		{"paste end full", []byte{27, '[', '2', '0', '1', '~'}, escapePasteEnd},
		{"unrecognized CSI", []byte{27, '[', 'Z'}, escapeUnhandled},
		{"ESC O partial", []byte{27, 'O'}, escapeIncomplete},
		{"ESC O full", []byte{27, 'O', 'P'}, escapeUnhandled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyEscapeSequence(tc.seq)
			if got != tc.want {
				t.Errorf("classifyEscapeSequence(%q) = %v, want %v", tc.seq, got, tc.want)
			}
		})
	}
}

func TestReadBracketedPaste(t *testing.T) {
	end := "\x1b[201~"
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"empty paste", end, "", false},
		{"simple text", "hello" + end, "hello", false},
		{"text with newline", "foo\nbar" + end, "foo\nbar", false},
		{"text with CR LF", "foo\r\nbar" + end, "foo\r\nbar", false},
		{"end marker immediately preceded by content", "abc" + end, "abc", false},
		{"EOF without end marker", "foo", "", true},
		{"empty reader", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := strings.NewReader(tc.input)
			got, err := readBracketedPaste(r)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil; data = %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadBracketedPaste_readerError(t *testing.T) {
	r := io.MultiReader(strings.NewReader("partial"), errReader{})
	_, err := readBracketedPaste(r)
	if err == nil {
		t.Error("expected error from underlying reader, got nil")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestNormalizePastedText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{"a\r\nb", "a\nb"},
		{"a\rb", "a\nb"},
		{"\r\n\r\n", "\n\n"},
		{"\r\r\n", "\n\n"},
		{"no CR", "no CR"},
		{"", ""},
	}
	for _, tc := range cases {
		got := string(normalizePastedText([]byte(tc.in)))
		if got != tc.want {
			t.Errorf("normalizePastedText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShouldSubmitPastedText(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"foo\n", true},
		{"foo\nbar\n", true},
		{"foo", false},
		{"\n", true},
		{"", false},
	}
	for _, tc := range cases {
		if got := shouldSubmitPastedText(tc.in); got != tc.want {
			t.Errorf("shouldSubmitPastedText(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestTrimTrailingSubmittedNewline(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo\n", "foo"},
		{"foo", "foo"},
		{"foo\n\n", "foo\n"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := trimTrailingSubmittedNewline(tc.in); got != tc.want {
			t.Errorf("trimTrailingSubmittedNewline(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDisplayPastedText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo\nbar", "foo\r\nbar"},
		{"foo", "foo"},
		{"\n\n", "\r\n\r\n"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := displayPastedText(tc.in); got != tc.want {
			t.Errorf("displayPastedText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEditorCursorLayout(t *testing.T) {
	cases := []struct {
		name                                   string
		prompt                                 string
		buf                                    string
		pos, cols                              int
		wantRows, wantCursorRow, wantCursorCol int
	}{
		{
			name:   "empty buf at prompt end",
			prompt: "% ", buf: "", pos: 0, cols: 80,
			wantRows: 1, wantCursorRow: 0, wantCursorCol: 2,
		},
		{
			name:   "cursor at end of short line",
			prompt: "% ", buf: "ls", pos: 2, cols: 80,
			wantRows: 1, wantCursorRow: 0, wantCursorCol: 4,
		},
		{
			name:   "cursor at start of buf",
			prompt: "% ", buf: "ls", pos: 0, cols: 80,
			wantRows: 1, wantCursorRow: 0, wantCursorCol: 2,
		},
		{
			name:   "cursor mid buf",
			prompt: "% ", buf: "ls -l", pos: 2, cols: 80,
			wantRows: 1, wantCursorRow: 0, wantCursorCol: 4,
		},
		{
			name:   "buf wraps to second row",
			prompt: strings.Repeat("x", 78), buf: "abc", pos: 3, cols: 80,
			wantRows: 2, wantCursorRow: 1, wantCursorCol: 1,
		},
		{
			name:   "newline in buf advances row",
			prompt: "% ", buf: "a\nb", pos: 3, cols: 80,
			wantRows: 2, wantCursorRow: 1, wantCursorCol: 1,
		},
		{
			name:   "cursor before newline in buf",
			prompt: "% ", buf: "a\nb", pos: 1, cols: 80,
			wantRows: 2, wantCursorRow: 0, wantCursorCol: 3,
		},
		{
			name:   "zero cols defaults to 80",
			prompt: "% ", buf: "ls", pos: 2, cols: 0,
			wantRows: 1, wantCursorRow: 0, wantCursorCol: 4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, cr, cc := editorCursorLayout(tc.prompt, []rune(tc.buf), tc.pos, tc.cols)
			if rows != tc.wantRows || cr != tc.wantCursorRow || cc != tc.wantCursorCol {
				t.Errorf("editorCursorLayout(%q, %q, pos=%d, cols=%d) = (%d,%d,%d), want (%d,%d,%d)",
					tc.prompt, tc.buf, tc.pos, tc.cols,
					rows, cr, cc,
					tc.wantRows, tc.wantCursorRow, tc.wantCursorCol)
			}
		})
	}
}

func TestEditorCursorMove(t *testing.T) {
	cases := []struct {
		name                           string
		fromRow, fromCol, toRow, toCol int
		want                           string
	}{
		{"same position", 0, 2, 0, 2, "\r\x1b[2C"},
		{"move right same row", 0, 0, 0, 3, "\r\x1b[3C"},
		{"move to col 0", 0, 5, 0, 0, "\r"},
		{"move up one row", 1, 3, 0, 3, "\x1b[1A\r\x1b[3C"},
		{"move down one row", 0, 3, 1, 3, "\x1b[1B\r\x1b[3C"},
		{"move up two rows", 2, 0, 0, 0, "\x1b[2A\r"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := editorCursorMove(tc.fromRow, tc.fromCol, tc.toRow, tc.toCol)
			if got != tc.want {
				t.Errorf("editorCursorMove(%d,%d,%d,%d) = %q, want %q",
					tc.fromRow, tc.fromCol, tc.toRow, tc.toCol, got, tc.want)
			}
		})
	}
}

func TestEditorClearPrefix(t *testing.T) {
	single := editorClearPrefix(1, 0)
	if !strings.HasPrefix(single, "\r") {
		t.Errorf("single-row clear should start with CR, got %q", single)
	}

	multi := editorClearPrefix(3, 0)
	if !strings.Contains(multi, "\x1b[2B") {
		t.Errorf("multi-row clear should move down 2 rows, got %q", multi)
	}
}

func TestEditorDisplayText(t *testing.T) {
	got := editorDisplayText("% ", []rune("ls -l"))
	if want := "% ls -l"; got != want {
		t.Errorf("editorDisplayText = %q, want %q", got, want)
	}

	got = editorDisplayText("% ", []rune("a\nb"))
	if want := "% a\r\nb"; got != want {
		t.Errorf("editorDisplayText with newline = %q, want %q", got, want)
	}
}

func TestLongestCommonPrefix(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"abc"}, "abc"},
		{[]string{"abc", "abd"}, "ab"},
		{[]string{"abc", "abcd"}, "abc"},
		{[]string{"abc", "xyz"}, ""},
		{[]string{"", "abc"}, ""},
		{[]string{"foo/bar", "foo/baz"}, "foo/ba"},
		{[]string{"a", "a", "a"}, "a"},
	}
	for _, tc := range cases {
		got := longestCommonPrefix(tc.in)
		if got != tc.want {
			t.Errorf("longestCommonPrefix(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
