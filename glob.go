package main

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

func hasGlob(pattern string) bool {
	return hasGlobPattern(pattern)
}

func hasGlobPattern(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case globMark, '*', '?', '[':
			return true
		}
	}
	return false
}

func stripGlobMark(s string) string {
	if !strings.ContainsRune(s, globMark) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == globMark {
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func deglob(s string) string {
	if strings.IndexByte(s, globMark) < 0 {
		return s
	}
	return deglobString(s)
}

func globPattern(value wordValue) string {
	if !value.glob {
		return value.text
	}
	return markGlobPattern(value.text)
}

func markGlobPattern(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case globMark, '*', '?', '[':
			b.WriteByte(globMark)
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func matchPattern(subject, pattern string, relaxed bool) bool {
	if !relaxed && isDotName(subject) && !strings.HasPrefix(deglob(pattern), ".") {
		return false
	}
	return matchSegment(subject, pattern, 0, relaxed)
}

func matchSegment(subject, pattern string, stop rune, relaxed bool) bool {
	for len(pattern) != 0 {
		r, size := utf8.DecodeRuneInString(pattern)
		if r == stop {
			return subject == ""
		}
		if r != globMark {
			sr, ssize := utf8.DecodeRuneInString(subject)
			if subject == "" || sr != r {
				return false
			}
			subject = subject[ssize:]
			pattern = pattern[size:]
			continue
		}
		pattern = pattern[size:]
		op, opSize := utf8.DecodeRuneInString(pattern)
		switch op {
		case globMark:
			sr, ssize := utf8.DecodeRuneInString(subject)
			if subject == "" || sr != globMark {
				return false
			}
			subject = subject[ssize:]
			pattern = pattern[opSize:]
		case '*':
			pattern = pattern[opSize:]
			for {
				if matchSegment(subject, pattern, stop, relaxed) {
					return true
				}
				if subject == "" {
					return false
				}
				sr, ssize := utf8.DecodeRuneInString(subject)
				if stop != 0 && sr == stop {
					return false
				}
				subject = subject[ssize:]
			}
		case '?':
			if subject == "" {
				return false
			}
			sr, ssize := utf8.DecodeRuneInString(subject)
			if stop != 0 && sr == stop {
				return false
			}
			subject = subject[ssize:]
			pattern = pattern[opSize:]
		case '[':
			if subject == "" {
				return false
			}
			sr, ssize := utf8.DecodeRuneInString(subject)
			hit, rest, ok := matchClass(sr, pattern[opSize:])
			if !ok {
				return false
			}
			if !hit {
				return false
			}
			subject = subject[ssize:]
			pattern = rest
		default:
			return false
		}
	}
	return subject == ""
}

func matchClass(subject rune, pattern string) (bool, string, bool) {
	compl := false
	if strings.HasPrefix(pattern, "~") {
		compl = true
		pattern = pattern[1:]
	}
	hit := false
	for len(pattern) != 0 {
		r, size := utf8.DecodeRuneInString(pattern)
		if r == ']' {
			if compl {
				hit = !hit
			}
			return hit, pattern[size:], true
		}
		lo := r
		pattern = pattern[size:]
		hi := lo
		if strings.HasPrefix(pattern, "-") {
			pattern = pattern[1:]
			if pattern == "" {
				return false, "", false
			}
			next, nsize := utf8.DecodeRuneInString(pattern)
			pattern = pattern[nsize:]
			hi = next
			if hi < lo {
				lo, hi = hi, lo
			}
		}
		if lo <= subject && subject <= hi {
			hit = true
		}
	}
	return false, "", false
}

func isDotName(name string) bool {
	return name == "." || name == ".."
}

func globPaths(pattern, cwd string) ([]string, error) {
	if !hasGlob(pattern) {
		return []string{deglob(pattern)}, nil
	}
	var matches []string
	startBase := cwd
	startPrefix := ""
	rest := pattern
	if strings.HasPrefix(pattern, "/") {
		startBase = "/"
		startPrefix = "/"
		rest = strings.TrimPrefix(pattern, "/")
	}
	if err := globDir(&matches, startBase, startPrefix, rest); err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return []string{deglob(pattern)}, nil
	}
	sort.Strings(matches)
	return matches, nil
}

func globDir(matches *[]string, base, prefix, pattern string) error {
	if pattern == "" {
		target := prefix
		if target == "" {
			target = "."
		}
		if _, err := os.Stat(base); err == nil {
			*matches = append(*matches, target)
		}
		return nil
	}
	component, rest := splitPatternComponent(pattern)
	if !hasGlob(component) {
		nextBase := joinFS(base, deglob(component))
		nextPrefix := joinPattern(prefix, deglob(component))
		return globDir(matches, nextBase, nextPrefix, rest)
	}
	dirEntries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	for _, entry := range dirEntries {
		name := entry.Name()
		if isDotName(name) && !strings.HasPrefix(deglob(component), ".") {
			continue
		}
		if !matchSegment(name, component, 0, false) {
			continue
		}
		nextBase := joinFS(base, name)
		nextPrefix := joinPattern(prefix, name)
		if rest != "" && !entry.IsDir() {
			continue
		}
		if err := globDir(matches, nextBase, nextPrefix, rest); err != nil {
			return err
		}
	}
	return nil
}

func splitPatternComponent(pattern string) (string, string) {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '/' {
			return pattern[:i], pattern[i+1:]
		}
	}
	return pattern, ""
}

func joinFS(base, name string) string {
	if base == "/" {
		return "/" + name
	}
	if base == "" || base == "." {
		return name
	}
	return base + "/" + name
}

func joinPattern(prefix, name string) string {
	if prefix == "" {
		return name
	}
	if prefix == "/" {
		return "/" + name
	}
	return prefix + "/" + name
}

func expandGlobWords(words []wordValue, cwd string) ([]string, error) {
	var out []string
	for _, word := range words {
		if !word.glob {
			out = append(out, word.text)
			continue
		}
		matches, err := globPaths(globPattern(word), cwd)
		if err != nil {
			return nil, err
		}
		out = append(out, matches...)
	}
	return out, nil
}

func parseSubscript(part string, size int) ([]int, error) {
	if strings.Contains(part, "-") {
		bounds := strings.SplitN(part, "-", 2)
		start, err := strconv.Atoi(bounds[0])
		if err != nil {
			return nil, err
		}
		end := size
		if bounds[1] != "" {
			end, err = strconv.Atoi(bounds[1])
			if err != nil {
				return nil, err
			}
		}
		var out []int
		for i := start; i <= end && i <= size; i++ {
			if i >= 1 {
				out = append(out, i-1)
			}
		}
		return out, nil
	}
	index, err := strconv.Atoi(part)
	if err != nil {
		return nil, err
	}
	if index < 1 || index > size {
		return nil, nil
	}
	return []int{index - 1}, nil
}
