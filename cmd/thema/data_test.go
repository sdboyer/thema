package main

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// When set, the test binary runs the thema CLI's main instead of the tests, so
// tests can drive the real command with a real stdin.
const runMainEnv = "THEMA_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestStdinHasInput(t *testing.T) {
	tests := map[string]struct {
		mode fs.FileMode
		want bool
	}{
		"pipe":            {mode: fs.ModeNamedPipe, want: true},
		"redirected file": {mode: 0, want: true},
		"socket":          {mode: fs.ModeSocket, want: true},
		"terminal":        {mode: fs.ModeDevice | fs.ModeCharDevice, want: false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := stdinHasInput(tt.mode | 0o600); got != tt.want {
				t.Fatalf("stdinHasInput(%v) = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

const stdinTestLineage = `package x

import "github.com/sdboyer/thema"

lin: thema.Lineage & {name: "x", schemas: [{version: [0, 0], schema: {a: string}}]}
`

// runValidateAny runs `thema data validate-any` against a minimal lineage, with
// stdin set to the provided file.
func runValidateAny(t *testing.T, stdin *os.File) (string, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ship.cue"), []byte(stdinTestLineage), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "data", "validate-any", "-l", "ship.cue", "-p", "lin") //nolint:gosec // re-executes this test binary
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	cmd.Stdin = stdin
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func TestDataRedirectedStdin(t *testing.T) {
	in := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(in, []byte(`{"a": "foo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(in) //nolint:gosec // a file this test just wrote
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // nothing to do on a failed close in a test

	out, err := runValidateAny(t, f)
	if err != nil {
		t.Fatalf("validate-any with redirected stdin failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "0.0" {
		t.Fatalf("expected data to validate against 0.0, got output:\n%s", out)
	}
}

func TestDataPipedStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck // nothing to do on a failed close in a test
	if _, err = w.WriteString(`{"a": "foo"}`); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}

	out, err := runValidateAny(t, r)
	if err != nil {
		t.Fatalf("validate-any with piped stdin failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "0.0" {
		t.Fatalf("expected data to validate against 0.0, got output:\n%s", out)
	}
}

// A character device on stdin, as with an interactive terminal, is not read.
func TestDataCharDeviceStdin(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // nothing to do on a failed close in a test

	out, err := runValidateAny(t, f)
	if err == nil {
		t.Fatalf("expected validate-any to fail with a character device on stdin, got output:\n%s", out)
	}
	if !strings.Contains(out, "no path provided and nothing on stdin") {
		t.Fatalf("unexpected error output:\n%s", out)
	}
}
