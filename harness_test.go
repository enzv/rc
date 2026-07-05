package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type rcCase struct {
	name      string
	file      string
	args      []string
	cwd       string
	stdin     string
	stdout    string
	stderr    string
	status    int
	parseOnly bool
}

func runRCCases(t *testing.T, cases []rcCase) {
	t.Helper()
	for _, tc := range cases {
		runRCCase(t, tc)
	}
}

func runRCCase(t *testing.T, tc rcCase) {
	t.Helper()
	if !strings.HasSuffix(tc.file, ".rc") {
		t.Fatalf("fixture name must end in .rc: %q", tc.file)
	}
	if strings.Contains(tc.file, "/") || strings.Contains(tc.file, "\\") {
		t.Fatalf("fixture name must be flat: %q", tc.file)
	}
	name := tc.name
	if name == "" {
		name = tc.file
	}
	t.Run(name, func(t *testing.T) {
		t.Helper()
		path := filepath.Join("testdata", tc.file)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read fixture %s: %v", tc.file, err)
		}
		if tc.parseOnly {
			if _, err := ParseSource(string(src)); err != nil {
				t.Fatalf("%s parse failed: %v", tc.file, err)
			}
			return
		}
		result, err := RunSource(string(src), RunOptions{
			Args:  tc.args,
			Cwd:   tc.cwd,
			Stdin: tc.stdin,
		})
		if err != nil {
			t.Fatalf("%s execution failed: %v", tc.file, err)
		}
		if result.Stdout != tc.stdout {
			t.Fatalf("%s stdout mismatch\ngot:\n%q\nwant:\n%q", tc.file, result.Stdout, tc.stdout)
		}
		if result.Stderr != tc.stderr {
			t.Fatalf("%s stderr mismatch\ngot:\n%q\nwant:\n%q", tc.file, result.Stderr, tc.stderr)
		}
		if result.ExitCode != tc.status {
			t.Fatalf("%s status mismatch: got %d, want %d", tc.file, result.ExitCode, tc.status)
		}
	})
}
