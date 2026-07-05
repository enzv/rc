package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		pattern string
		relaxed bool
		want    bool
	}{
		{name: "literal", subject: "abc", pattern: "abc", want: true},
		{name: "star", subject: "abcdef", pattern: string([]byte{globMark, '*'}) + "def", want: true},
		{name: "question", subject: "abc", pattern: "a" + string([]byte{globMark, '?'}) + "c", want: true},
		{name: "class", subject: "b", pattern: string([]byte{globMark, '['}) + "abc]", want: true},
		{name: "class complement", subject: "z", pattern: string([]byte{globMark, '['}) + "~abc]", want: true},
		{name: "range", subject: "m", pattern: string([]byte{globMark, '['}) + "a-z]", want: true},
		{name: "dot strict", subject: ".", pattern: string([]byte{globMark, '*'}), want: false},
		{name: "dot hidden strict", subject: ".hidden", pattern: string([]byte{globMark, '*'}), want: true},
		{name: "dot relaxed", subject: ".", pattern: string([]byte{globMark, '*'}), relaxed: true, want: true},
		{name: "slash explicit", subject: "a/b", pattern: "a/" + string([]byte{globMark, '*'}) + "b", relaxed: true, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchPattern(tc.subject, tc.pattern, tc.relaxed); got != tc.want {
				t.Fatalf("matchPattern(%q, %q, relaxed=%v) = %v, want %v", tc.subject, tc.pattern, tc.relaxed, got, tc.want)
			}
		})
	}
}

func TestGlobPaths(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		setup   func(t *testing.T, dir string)
		want    []string
	}{
		{
			name:    "matches sorted entries",
			pattern: string([]byte{globMark, '*'}) + ".txt",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				mustWrite(t, filepath.Join(dir, "a.txt"), "")
				mustWrite(t, filepath.Join(dir, "b.txt"), "")
				mustWrite(t, filepath.Join(dir, ".hidden.txt"), "")
			},
			want: []string{".hidden.txt", "a.txt", "b.txt"},
		},
		{
			name:    "unmatched remains literal",
			pattern: string([]byte{globMark, '*'}) + ".nomatch",
			want:    []string{"*.nomatch"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, dir)
			}
			got, err := globPaths(tc.pattern, dir)
			if err != nil {
				t.Fatalf("globPaths(%q) returned error: %v", tc.pattern, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("globPaths(%q) = %v, want %v", tc.pattern, got, tc.want)
			}
		})
	}
}

func TestParseSubscript(t *testing.T) {
	cases := []struct {
		name    string
		part    string
		size    int
		want    []int
		wantErr bool
	}{
		{name: "single in range", part: "2", size: 4, want: []int{1}},
		{name: "single out of range", part: "9", size: 4, want: nil},
		{name: "closed range", part: "2-3", size: 4, want: []int{1, 2}},
		{name: "open ended range", part: "3-", size: 4, want: []int{2, 3}},
		{name: "range clipped by size", part: "3-99", size: 4, want: []int{2, 3}},
		{name: "invalid", part: "a-b", size: 4, wantErr: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSubscript(tc.part, tc.size)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseSubscript(%q, %d) error = nil, want error", tc.part, tc.size)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSubscript(%q, %d) unexpected error: %v", tc.part, tc.size, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseSubscript(%q, %d) = %v, want %v", tc.part, tc.size, got, tc.want)
			}
		})
	}
}

func mustWrite(tb testing.TB, path, content string) {
	tb.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		tb.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func BenchmarkMatchPattern(b *testing.B) {
	subject := "abcdefghijklmnopqrstuvwxyz"
	pattern := string([]byte{globMark, '*'}) + "m" + string([]byte{globMark, '*'}) + "z"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		matchPattern(subject, pattern, false)
	}
}

func BenchmarkGlobPaths(b *testing.B) {
	dir := b.TempDir()
	mustWrite(b, filepath.Join(dir, "a.txt"), "")
	mustWrite(b, filepath.Join(dir, "b.txt"), "")
	mustWrite(b, filepath.Join(dir, ".hidden.txt"), "")
	pattern := string([]byte{globMark, '*'}) + ".txt"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = globPaths(pattern, dir)
	}
}
