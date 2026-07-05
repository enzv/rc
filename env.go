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

type shellEnv struct {
	vars    map[string][]string
	fns     map[string]*Tree
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
		fns:   map[string]*Tree{},
		cwd:   cwd,
		jobs:  newJobState(),
		flags: map[string]bool{},
	}
	env.importEnv(os.Environ())
	env.vars["*"] = append([]string(nil), args...)
	env.vars["pid"] = []string{strconv.Itoa(os.Getpid())}
	env.vars["status"] = []string{""}

	if username := os.Getenv("USER"); username != "" {
		env.vars["user"] = []string{username}
		env.vars["USER"] = []string{username}
	} else if u, err := user.Current(); err == nil {
		env.vars["user"] = []string{u.Username}
		env.vars["USER"] = []string{u.Username}
	}

	env.vars["PWD"] = []string{cwd}
	env.vars["pwd"] = []string{cwd}

	if home := os.Getenv("HOME"); home != "" {
		env.vars["home"] = []string{home}
	} else {
		env.vars["home"] = []string{cwd}
	}

	if len(env.vars["ifs"]) == 0 {
		env.vars["ifs"] = []string{" ", "\t", "\n"}
	}
	if len(env.vars["prompt"]) == 0 {
		env.vars["prompt"] = []string{"% ", "\t"}
	}

	path := filepathList(os.Getenv("PATH"))
	if len(path) == 0 {
		path = []string{".", "/bin", "/usr/bin"}
	}
	env.vars["path"] = path
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
			tree, err := ParseSource(val)
			if err == nil && tree != nil && tree.Root != nil {
				e.fns[fnName] = tree.Root
			}
		} else {
			e.vars[name] = strings.Split(val, "\x01")
		}
	}
}

func (e *shellEnv) exportEnv() []string {
	var env []string
	hasUpperPath := false
	for name := range e.vars {
		if name == "PATH" {
			hasUpperPath = true
		}
	}
	for name, values := range e.vars {
		if name == "argv0" || name == "*" || name == "apid" {
			continue
		}
		if name == "path" && !hasUpperPath {
			env = append(env, "PATH="+strings.Join(values, ":"))
			continue
		}
		if name == "path" && hasUpperPath {
			continue
		}
		env = append(env, name+"="+strings.Join(values, "\x01"))
	}
	for name, body := range e.fns {
		env = append(env, "fn#"+name+"="+FormatTree(body))
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

func (e *shellEnv) scalar(name string) string {
	values := e.lookup(name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (e *shellEnv) set(name string, values []string) {
	e.vars[name] = append([]string(nil), values...)
}

func (e *shellEnv) unset(name string) {
	delete(e.vars, name)
}

func (e *shellEnv) status() string {
	values := e.lookup("status")
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (e *shellEnv) setStatus(status string) {
	e.vars["status"] = []string{status}
}

func (e *shellEnv) clone() *shellEnv {
	cp := &shellEnv{
		vars:    make(map[string][]string, len(e.vars)),
		fns:     make(map[string]*Tree, len(e.fns)),
		cwd:     e.cwd,
		ifstate: e.ifstate,
		jobs:    e.jobs,
		flags:   make(map[string]bool, len(e.flags)),
	}
	for name, values := range e.vars {
		cp.vars[name] = append([]string(nil), values...)
	}
	for name, body := range e.fns {
		cp.fns[name] = body
	}
	for f, v := range e.flags {
		cp.flags[f] = v
	}
	return cp
}

func (e *shellEnv) defineFunc(name string, body *Tree) {
	e.fns[name] = body
}

func (e *shellEnv) deleteFunc(name string) {
	delete(e.fns, name)
}

func (e *shellEnv) lookupFunc(name string) (*Tree, bool) {
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

// Klook looks up a keyword tree node by its string representation.
func Klook(name string) *Tree {
	t := Token(name, tokenWord)
	for p := kw[hash(name, nKeyword)]; p != nil; p = p.Next {
		if p.Name == name {
			t.Type = p.Type
			t.IsKeyword = true
			return t
		}
	}
	return t
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
