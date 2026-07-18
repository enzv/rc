package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

func (r *runner) execPipe(id int) error {
	node := r.prog.Node(id)
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}

	leftRunner := r.child(r.env.clone())
	rightRunner := r.child(r.env.clone())

	mapPipeWriter(leftRunner, node.FD0, pw)
	mapPipeReader(rightRunner, node.FD1, pr)

	leftDone := make(chan struct {
		status string
		err    error
	}, 1)
	go func() {
		err := leftRunner.exec(node.Child[0])
		_ = pw.Close()
		leftDone <- struct {
			status string
			err    error
		}{
			status: leftRunner.env.status(),
			err:    err,
		}
	}()

	rightErr := rightRunner.exec(node.Child[1])
	_ = pr.Close()
	left := <-leftDone

	r.env.setStatus(pipeStatus(left.status, rightRunner.env.status()))

	if left.err != nil {
		return left.err
	}
	return rightErr
}

func mapPipeReader(sub *runner, fd int, file *os.File) {
	switch fd {
	case 0:
		sub.bindReader(0, file)
	default:
		sub.bindReader(fd, file)
	}
}

func mapPipeWriter(sub *runner, fd int, file *os.File) {
	sub.bindWriter(fd, file)
}

func pipeStatus(left, right string) string {
	return left + "|" + right
}

func (r *runner) execRedir(id int) error {
	child := r.child(r.env)
	var (
		closers []io.Closer
		ok      bool
		err     error
		next    = id
		leaf    = -1
		chain   []int
	)
	for next >= 0 {
		node := r.prog.Node(next)
		if node == nil || (node.Type != tokenRedir && node.Type != tokenDup) {
			break
		}
		chain = append(chain, next)
		if node.Child[1] < 0 {
			leaf = node.Child[1]
			break
		}
		nextNode := r.prog.Node(node.Child[1])
		if nextNode == nil || (nextNode.Type != tokenRedir && nextNode.Type != tokenDup) {
			leaf = node.Child[1]
			break
		}
		next = node.Child[1]
	}
	sort.Slice(chain, func(i, j int) bool {
		return r.prog.Pos(chain[i]).Offset < r.prog.Pos(chain[j]).Offset
	})
	for i := 0; i < len(chain); i++ {
		var closer io.Closer
		closer, ok, err = child.applyRedir(chain[i])
		if closer != nil {
			closers = append(closers, closer)
		}
		if err != nil {
			for j := len(closers) - 1; j >= 0; j-- {
				_ = closers[j].Close()
			}
			return err
		}
		if !ok {
			for j := len(closers) - 1; j >= 0; j-- {
				_ = closers[j].Close()
			}
			return nil
		}
	}
	for j := len(closers) - 1; j >= 0; j-- {
		defer closers[j].Close()
	}
	return child.exec(leaf)
}

func (r *runner) applyRedir(id int) (io.Closer, bool, error) {
	node := r.prog.Node(id)
	switch node.Type {
	case tokenDup:
		if err := r.applyDup(id); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	case tokenRedir:
		switch node.RType {
		case redirWrite, redirAppend, redirRead, redirRDWR:
			target, ok := r.expandRedirTarget(id)
			if !ok {
				return nil, false, nil
			}
			path := target
			if !filepath.IsAbs(path) {
				path = filepath.Join(r.env.cwd, path)
			}
			file, err := r.openRedirFile(id, path)
			if err != nil {
				display := target
				prefix := r.shellPrefix()
				if prefix == "" {
					_, _ = fmt.Fprintf(r.diag, "%s: can't open: %s\n", display, titleError(err))
				} else {
					_, _ = fmt.Fprintf(r.diag, "%s: %scan't open: %s\n", display, prefix, titleError(err))
				}
				r.env.setStatus("1")
				return nil, false, exitSignal{status: "1", code: 1}
			}
			r.bindFile(id, file)
			return file, true, nil
		case redirHere:
			body, quoted, ok := r.prog.HereDoc(id)
			if !ok {
				return nil, false, fmt.Errorf("missing here-doc body")
			}
			if !quoted {
				body = expandHereDoc(body, r.env)
			}
			r.bindReader(node.FD0, strings.NewReader(body))
			return nil, true, nil
		default:
			return nil, false, fmt.Errorf("unsupported redirection type %d", node.RType)
		}
	default:
		return nil, false, fmt.Errorf("unsupported redirection node %s", tokenName(node.Type))
	}
}

func (r *runner) expandRedirTarget(id int) (string, bool) {
	node := r.prog.Node(id)
	values, err := r.expandWord(node.Child[0])
	if err != nil {
		_, _ = fmt.Fprintf(r.stderr, "%v\n", err)
		r.env.setStatus("1")
		return "", false
	}
	if len(values) != 1 {
		_, _ = fmt.Fprintf(r.stderr, "%s requires singleton\n", redirNameNode(node))
		r.env.setStatus("1")
		return "", false
	}
	return values[0].text, true
}

func redirNameNode(node *Node) string {
	if node == nil {
		return "redir"
	}
	switch node.RType {
	case redirAppend:
		return ">>"
	case redirWrite:
		return ">"
	case redirRead:
		return "<"
	case redirHere:
		return "<<"
	case redirRDWR:
		return "<>"
	}
	return "redir"
}

func expandHereDoc(body string, env *shellEnv) string {
	var out strings.Builder
	for i := 0; i < len(body); {
		if body[i] != '$' {
			out.WriteByte(body[i])
			i++
			continue
		}
		if i+1 >= len(body) {
			out.WriteByte('$')
			i++
			continue
		}
		if body[i+1] == '$' {
			out.WriteByte('$')
			i += 2
			continue
		}
		j := i + 1
		if body[j] >= '0' && body[j] <= '9' {
			for j < len(body) && body[j] >= '0' && body[j] <= '9' {
				j++
			}
			out.WriteString(strings.Join(expandHereVar(body[i+1:j], env), " "))
			if j < len(body) && body[j] == '^' {
				j++
			}
			i = j
			continue
		}
		for j < len(body) && idchr(int(body[j])) {
			j++
		}
		if j == i+1 {
			out.WriteByte('$')
			i++
			continue
		}
		out.WriteString(strings.Join(expandHereVar(body[i+1:j], env), " "))
		if j < len(body) && body[j] == '^' {
			j++
		}
		i = j
	}
	return out.String()
}

func expandHereVar(name string, env *shellEnv) []string {
	if name == "" {
		return nil
	}
	if len(name) > 0 && name[0] >= '0' && name[0] <= '9' {
		r := &runner{env: env}
		return r.lookupVar(name)
	}
	return env.lookup(name)
}

func (r *runner) openRedirFile(id int, path string) (*os.File, error) {
	node := r.prog.Node(id)
	switch node.RType {
	case redirWrite:
		return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	case redirAppend:
		return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
	case redirRead:
		return os.Open(path)
	case redirRDWR:
		flags := os.O_RDWR
		return os.OpenFile(path, flags, 0o666)
	default:
		return nil, fmt.Errorf("unsupported redirection type %d", node.RType)
	}
}

func (r *runner) bindFile(id int, file *os.File) {
	node := r.prog.Node(id)
	switch node.RType {
	case redirRead:
		r.bindReader(node.FD0, file)
	case redirWrite, redirAppend:
		r.bindWriter(node.FD0, file)
	case redirRDWR:
		if node.FD0 == 0 {
			r.bindReader(0, file)
			r.fdWriters[0] = file
			return
		}
		r.bindReader(node.FD0, file)
		r.bindWriter(node.FD0, file)
	}
}

type badFDWriter struct{}

func (badFDWriter) Write(p []byte) (int, error) {
	return 0, syscall.EBADF
}

func (r *runner) applyDup(id int) error {
	node := r.prog.Node(id)
	switch node.RType {
	case redirClose:
		switch node.FD0 {
		case 0:
			r.bindReader(0, strings.NewReader(""))
		case 1:
			r.bindWriter(1, badFDWriter{})
		case 2:
			r.bindWriter(2, badFDWriter{})
		}
		return nil
	case redirDupFD:
		if node.FD1 == 0 {
			reader, ok := r.fdReaders[node.FD0]
			if !ok {
				return fmt.Errorf("unsupported dup source fd %d", node.FD0)
			}
			r.bindReader(0, reader)
			return nil
		}
		writer, ok := r.fdWriters[node.FD0]
		if !ok {
			reader, rok := r.fdReaders[node.FD0]
			if !rok {
				return fmt.Errorf("unsupported dup source fd %d", node.FD0)
			}
			rw, wok := reader.(io.Writer)
			if !wok {
				return fmt.Errorf("unsupported dup source fd %d", node.FD0)
			}
			writer = rw
		}
		if node.FD1 < 0 {
			return fmt.Errorf("unsupported dup source fd %d", node.FD0)
		}
		r.bindWriter(node.FD1, writer)
		return nil
	default:
		return fmt.Errorf("unsupported dup type %d", node.RType)
	}
}

func (r *runner) bindReader(fd int, reader io.Reader) {
	if r.fdReaders == nil {
		r.fdReaders = map[int]io.Reader{}
	}
	r.fdReaders[fd] = reader
	if fd == 0 {
		r.stdin = reader
	}
}

func (r *runner) bindWriter(fd int, writer io.Writer) {
	if r.fdWriters == nil {
		r.fdWriters = map[int]io.Writer{}
	}
	r.fdWriters[fd] = writer
	switch fd {
	case 1:
		r.stdout = writer
	case 2:
		r.stderr = writer
	}
}
