package main

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type jobState struct {
	mu      sync.Mutex
	jobs    map[string]chan string
	nextPID int
}

func newJobState() *jobState {
	return &jobState{
		jobs:    make(map[string]chan string),
		nextPID: 10000,
	}
}

type funcBody struct {
	prog *Program
	root int
}

type wordValue struct {
	text string
	glob bool
}

type shellEnv struct {
	vars    map[string][]string
	words   map[string][]wordValue
	fns     map[string]funcBody
	cwd     string
	ifstate int // 0: none, 1: false, 2: true
	jobs    *jobState
	flags   map[string]bool
}

func newShellEnv(args []string, cwd string) (*shellEnv, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	env := &shellEnv{
		vars:  map[string][]string{},
		words: map[string][]wordValue{},
		fns:   map[string]funcBody{},
		cwd:   cwd,
		jobs:  newJobState(),
		flags: map[string]bool{},
	}
	env.importEnv(os.Environ())
	env.set("*", append([]string(nil), args...))
	env.set("pid", []string{strconv.Itoa(os.Getpid())})
	env.set("status", []string{""})

	if username := os.Getenv("USER"); username != "" {
		env.set("user", []string{username})
		env.set("USER", []string{username})
	} else if u, err := user.Current(); err == nil {
		env.set("user", []string{u.Username})
		env.set("USER", []string{u.Username})
	}

	env.set("PWD", []string{cwd})
	env.set("pwd", []string{cwd})

	if home := os.Getenv("HOME"); home != "" {
		env.set("home", []string{home})
	} else {
		env.set("home", []string{cwd})
	}

	if len(env.vars["ifs"]) == 0 {
		env.set("ifs", []string{" ", "\t", "\n"})
	}
	if len(env.vars["prompt"]) == 0 {
		env.set("prompt", []string{"% ", ""})
	}

	path := filepathList(os.Getenv("PATH"))
	if len(path) == 0 {
		path = []string{".", "/bin", "/usr/bin"}
	}
	env.set("path", path)
	return env, nil
}

func (e *shellEnv) importEnv(environ []string) {
	for _, str := range environ {
		name, val, ok := strings.Cut(str, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(name, "fn#") {
			fnName := strings.TrimPrefix(name, "fn#")
			prog, err := ParseSource(val)
			if err == nil && prog != nil && prog.Root >= 0 {
				e.fns[fnName] = funcBody{prog: prog, root: prog.Root}
			}
		} else {
			e.setEncoded(name, strings.Split(val, "\x01"))
		}
	}
}

func (e *shellEnv) exportEnv() []string {
	var env []string

	if path, ok := e.words["path"]; ok {
		env = append(env, "PATH="+strings.Join(wordTexts(path), string(os.PathListSeparator)))
	} else if path, ok := e.vars["path"]; ok {
		env = append(env, "PATH="+strings.Join(path, string(os.PathListSeparator)))
	} else if path, ok := e.words["PATH"]; ok {
		env = append(env, "PATH="+strings.Join(wordTexts(path), "\x01"))
	} else if path, ok := e.vars["PATH"]; ok {
		env = append(env, "PATH="+strings.Join(path, "\x01"))
	}

	for name, values := range e.words {
		if name == "argv0" || name == "*" || name == "apid" || name == "path" || name == "PATH" {
			continue
		}
		env = append(env, name+"="+strings.Join(encodeWordValues(values), "\x01"))
	}
	for name, body := range e.fns {
		env = append(env, "fn#"+name+"="+FormatTree(body.prog, body.root))
	}
	return env
}

func filepathList(path string) []string {
	if path == "" {
		return nil
	}
	return filepath.SplitList(path)
}

func (e *shellEnv) lookup(name string) []string {
	if values, ok := e.vars[name]; ok {
		return append([]string(nil), values...)
	}
	return nil
}

func (e *shellEnv) lookupWords(name string) []wordValue {
	if values, ok := e.words[name]; ok {
		return append([]wordValue(nil), values...)
	}
	if values, ok := e.vars[name]; ok {
		out := make([]wordValue, len(values))
		for i, value := range values {
			out[i] = wordValue{text: value, glob: hasGlobPattern(value)}
		}
		return out
	}
	return nil
}

func wordTexts(values []wordValue) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = value.text
	}
	return out
}

func encodeWordValues(values []wordValue) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	for i, value := range values {
		if !value.glob {
			out[i] = value.text
			continue
		}
		var b strings.Builder
		b.Grow(len(value.text) + 2)
		for j := 0; j < len(value.text); j++ {
			switch value.text[j] {
			case globMark, '*', '?', '[':
				b.WriteByte(globMark)
			}
			b.WriteByte(value.text[j])
		}
		out[i] = b.String()
	}
	return out
}

func decodeWordValue(s string) wordValue {
	if strings.ContainsRune(s, globMark) {
		return wordValue{text: stripGlobMark(s), glob: true}
	}
	return wordValue{text: s, glob: hasGlobPattern(s)}
}

func (e *shellEnv) scalar(name string) string {
	values := e.lookup(name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (e *shellEnv) set(name string, values []string) {
	words := make([]wordValue, len(values))
	for i, value := range values {
		words[i] = wordValue{text: value, glob: hasGlobPattern(value)}
	}
	e.setWords(name, words)
}

func (e *shellEnv) setWords(name string, values []wordValue) {
	if e.vars == nil {
		e.vars = make(map[string][]string)
	}
	if e.words == nil {
		e.words = make(map[string][]wordValue)
	}
	e.vars[name] = wordTexts(values)
	e.words[name] = append([]wordValue(nil), values...)
}

func (e *shellEnv) setEncoded(name string, values []string) {
	words := make([]wordValue, len(values))
	for i, value := range values {
		words[i] = decodeWordValue(value)
	}
	e.setWords(name, words)
}

func (e *shellEnv) unset(name string) {
	delete(e.vars, name)
	delete(e.words, name)
}

func (e *shellEnv) status() string {
	values := e.lookup("status")
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (e *shellEnv) setStatus(status string) {
	if e.vars == nil {
		e.vars = make(map[string][]string)
	}
	e.vars["status"] = []string{status}
}

func (e *shellEnv) clone() *shellEnv {
	cp := &shellEnv{
		vars:    make(map[string][]string, len(e.vars)),
		words:   make(map[string][]wordValue, len(e.words)),
		fns:     make(map[string]funcBody, len(e.fns)),
		cwd:     e.cwd,
		ifstate: e.ifstate,
		jobs:    e.jobs,
		flags:   make(map[string]bool, len(e.flags)),
	}
	for name, values := range e.vars {
		cp.vars[name] = append([]string(nil), values...)
	}
	for name, values := range e.words {
		cp.words[name] = append([]wordValue(nil), values...)
	}
	for name, body := range e.fns {
		cp.fns[name] = body
	}
	for f, v := range e.flags {
		cp.flags[f] = v
	}
	return cp
}

func (e *shellEnv) defineFunc(name string, prog *Program, root int) {
	e.fns[name] = funcBody{prog: prog, root: root}
}

func (e *shellEnv) deleteFunc(name string) {
	delete(e.fns, name)
}

func (e *shellEnv) lookupFunc(name string) (funcBody, bool) {
	body, ok := e.fns[name]
	return body, ok
}

// Var represents an rc environment variable containing a list of words.
type Var struct {
	Name      string
	Val       *Word
	Changed   bool
	Fn        []Code
	FNChanged bool
	PC        int
	Next      *Var
}

// Scope represents a lexical environment scope for variables.
type Scope struct {
	Local   *Var
	globals [nVar]*Var
}

type keyword struct {
	Name string
	Type NodeType
	Next *keyword
}

var kw [nKeyword]*keyword

func init() {
	kinit()
}

func hash(s string, mod int) int {
	i := 1
	h := 0
	for j := 0; j < len(s); j++ {
		h += int(s[j]) * i
		i++
	}
	return h % mod
}

func kenter(kind NodeType, name string) {
	h := hash(name, nKeyword)
	kw[h] = &keyword{Name: name, Type: kind, Next: kw[h]}
}

func kinit() {
	for i := range kw {
		kw[i] = nil
	}
	kenter(tokenFor, "for")
	kenter(tokenIn, "in")
	kenter(tokenWhile, "while")
	kenter(tokenIf, "if")
	kenter(tokenNot, "not")
	kenter(tokenTwiddle, "~")
	kenter(tokenBang, "!")
	kenter(tokenSubshell, "@")
	kenter(tokenSwitch, "switch")
	kenter(tokenFn, "fn")
}

func keywordType(name string) (NodeType, bool) {
	for p := kw[hash(name, nKeyword)]; p != nil; p = p.Next {
		if p.Name == name {
			return p.Type, true
		}
	}
	return tokenWord, false
}

// Klook looks up a keyword tree node by its string representation.
func Klook(name string) Node {
	kind, _ := keywordType(name)
	_ = name
	return Node{Type: kind, Child: noChildren()}
}

// NewScope creates and initializes a new variable scope.
func NewScope() *Scope {
	return &Scope{}
}

func newVar(name string, next *Var) *Var {
	return &Var{Name: name, Next: next}
}

func (s *Scope) GVlook(name string) *Var {
	h := hash(name, nVar)
	for v := s.globals[h]; v != nil; v = v.Next {
		if v.Name == name {
			return v
		}
	}
	v := newVar(name, s.globals[h])
	s.globals[h] = v
	return v
}

func (s *Scope) Vlook(name string) *Var {
	for v := s.Local; v != nil; v = v.Next {
		if v.Name == name {
			return v
		}
	}
	return s.GVlook(name)
}

func (s *Scope) SetVar(name string, val *Word) {
	v := s.Vlook(name)
	v.Val = val
	v.Changed = true
}

// Word represents a single element in an rc variable list.
type Word struct {
	Word string
	Next *Word
}

// List is an alias for Word, representing a linked list of words.
type List struct {
	Words *Word
	Next  *List
}

// NewWord allocates a new word and prepends it to the provided next word.
func NewWord(w string, next *Word) *Word {
	return &Word{Word: w, Next: next}
}

// CopyWords creates a deep copy of a word list, appending tail to the end.
func CopyWords(src, tail *Word) *Word {
	if src == nil {
		return tail
	}
	head := &Word{Word: src.Word}
	cur := head
	for src = src.Next; src != nil; src = src.Next {
		cur.Next = &Word{Word: src.Word}
		cur = cur.Next
	}
	cur.Next = tail
	return head
}

// Count returns the number of words in a list.
func Count(w *Word) int {
	count := 0
	for ; w != nil; w = w.Next {
		count++
	}
	return count
}

// List2Str converts a word list into a single space-separated string.
func List2Str(words *Word) string {
	if words == nil {
		return ""
	}
	parts := make([]string, 0, Count(words))
	for ; words != nil; words = words.Next {
		parts = append(parts, words.Word)
	}
	return strings.Join(parts, " ")
}
