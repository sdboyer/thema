package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sdboyer/thema/diag"
)

var setupOnce sync.Once

// extraArg is a positional argument a command does not take.
const extraArg = "extra"

// dataName is the data command's name.
const dataName = "data"

// commandTree builds the command tree in this process, once, as main does,
// for the tests that inspect it or execute it in-process.
func commandTree() *cobra.Command {
	setupOnce.Do(setupCommands)
	return rootCmd
}

// cobrasOwn reports whether cmd is cobra's help or completion command, or
// beneath one. cobra adds them itself, they take none of thema's inputs, and
// they report their failures as cobra does.
func cobrasOwn(cmd *cobra.Command) bool {
	for c := cmd; c.HasParent(); c = c.Parent() {
		if !c.Parent().HasParent() && (c.Name() == "help" || c.Name() == "completion") {
			return true
		}
	}
	return false
}

// Every command main builds leaves its usage and errors to report, and keeps
// its usage line as its Use writes it, without cobra's [flags].
func TestCommandSettings(t *testing.T) {
	walkCommands(commandTree(), func(cmd *cobra.Command) {
		if cobrasOwn(cmd) {
			return
		}
		if !cmd.SilenceErrors || !cmd.SilenceUsage || !cmd.DisableFlagsInUseLine {
			t.Errorf("%s: SilenceErrors %v, SilenceUsage %v, DisableFlagsInUseLine %v, want all true",
				cmd.CommandPath(), cmd.SilenceErrors, cmd.SilenceUsage, cmd.DisableFlagsInUseLine)
		}
	})
}

// A command line cobra cannot take is E0620, whichever part of it is wrong,
// and is reported as --diag-format asks even where it follows what cobra
// stopped at. An error a command's own code returns keeps its code.
func TestCommandLine(t *testing.T) {
	const bogus, data = "bogus", "data"
	for _, args := range [][]string{
		{bogus},
		{lineageCmd, tipName, extraArg},
		{"--bogus"},
		{lineageCmd, tipName, "--bogus"},
		{data, "validate", "a.json", "b.json"},
		{bogus, "--help"},
		{lineageCmd, bogus},
		{lineageCmd, "gen", bogus},
		{data, bogus},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if got := initCodes(t, t.TempDir(), args...); !slices.Equal(got, []diag.Code{diag.E0620}) {
				t.Errorf("codes %v, want [E0620]", got)
			}
		})
	}

	// A flag whose value is missing at the end of the line: --diag-format
	// comes first, since after -l it would be -l's value.
	t.Run("a flag without its value", func(t *testing.T) {
		out, errOut, err := runThema(t, t.TempDir(), "", "--diag-format", "json", lineageCmd, "check", "-l")
		if err == nil || !strings.Contains(out, `"code": "E0620"`) {
			t.Errorf("want E0620 as JSON on stdout, got err %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
		}
	})

	// A format thema does not write is refused, even for a command line
	// that would succeed, in text on stderr, leaving stdout empty.
	for _, format := range []string{"xml", "JSON"} {
		t.Run("--diag-format "+format, func(t *testing.T) {
			out, errOut, err := runThema(t, t.TempDir(), "", lineageCmd, initName, "empty", stdoutFlg, "--diag-format", format)
			if err == nil || out != "" || !strings.HasPrefix(errOut, "error[E0620]") {
				t.Errorf("want E0620 as text on stderr and nothing on stdout, got err %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
			}
		})
	}

	t.Run("a command's own error", func(t *testing.T) {
		got := initCodes(t, t.TempDir(), data, "validate", "-l", "missing", "data.json")
		if slices.Contains(got, diag.E0620) || len(got) == 0 {
			t.Errorf("codes %v, want the command's own, not E0620", got)
		}
	})
}

// needsNoInput holds the leaves whose every input has a default, which run
// with none given, and why each cannot be run that way here.
var needsNoInput = map[string]string{
	"thema lineage init empty": "it writes a new lineage in the working directory, named for it",
	"thema serve playground":   "it serves the playground until it is stopped",
}

// TestCommandTree checks every leaf of the tree main builds, found by
// walking it, so that a command added later is covered without being listed
// here. It guards how a leaf is wired and what fails before the leaf's own
// code runs, not what that code does: each leaf must run with RunE, not
// Run, and have an Args validator; and each is run with a flag it does not
// take, with one argument more than it takes, and with none of its flags or
// arguments, in an empty directory. The first two fail in cobra, and the
// third, for every leaf now, in loading the lineage, so none reaches a
// PreRunE or a RunE body. Each run must exit 1, write nothing to stderr, and
// write diagnostics to stdout, none of them E09xx, the codes of thema's own
// failures. What a command's own code reports is for its own tests.
func TestCommandTree(t *testing.T) {
	var leaves []*cobra.Command
	walkCommands(commandTree(), func(cmd *cobra.Command) {
		if !cmd.HasSubCommands() && !cobrasOwn(cmd) {
			leaves = append(leaves, cmd)
		}
	})
	for path := range needsNoInput {
		if !slices.ContainsFunc(leaves, func(cmd *cobra.Command) bool { return cmd.CommandPath() == path }) {
			t.Errorf("needsNoInput names %s, which is not a command", path)
		}
	}

	for _, cmd := range leaves {
		t.Run(cmd.CommandPath(), func(t *testing.T) {
			// A Run hook cannot return an error, so whatever fails in it
			// cannot reach report.
			if cmd.Run != nil || cmd.RunE == nil {
				t.Fatal("the command runs with Run, want RunE")
			}
			// Without an Args validator, cobra takes any arguments, and the
			// command ignores those it does not read.
			if cmd.Args == nil {
				t.Fatal("the command has no Args validator, want one, such as cobra.NoArgs")
			}
			words := strings.Fields(cmd.CommandPath())[1:]
			lines := map[string][]string{"an unknown flag": append(slices.Clone(words), "--bogus")}
			if why, ok := needsNoInput[cmd.CommandPath()]; ok {
				t.Logf("not run without its inputs: %s", why)
			} else {
				lines["without its inputs"] = words
			}
			if n, ok := maxArgs(cmd); ok {
				extra := slices.Repeat([]string{extraArg}, n+1)
				lines["an extra argument"] = append(slices.Clone(words), extra...)
			}
			for name, args := range lines {
				t.Run(name, func(t *testing.T) { requireReported(t, args) })
			}
		})
	}
}

// maxArgs is the most positional arguments cmd takes, and false where it
// takes any number.
func maxArgs(cmd *cobra.Command) (int, bool) {
	const probe = 8
	took := false
	for n := 0; n <= probe; n++ {
		if cmd.ValidateArgs(slices.Repeat([]string{extraArg}, n)) == nil {
			took = true
		} else if took {
			return n - 1, true
		}
	}
	return 0, false
}

// requireReported runs thema with args in an empty directory, with nothing
// on stdin, and requires that it fail as report reports a failure, with
// diagnostics none of which is E09xx.
func requireReported(t *testing.T, args []string) {
	t.Helper()
	codes := initCodes(t, t.TempDir(), args...)
	if len(codes) == 0 {
		t.Fatalf("thema %s: no diagnostics", strings.Join(args, " "))
	}
	for _, c := range codes {
		if strings.HasPrefix(string(c), "E09") {
			t.Errorf("thema %s: %s, a code of thema's own failures", strings.Join(args, " "), c)
		}
	}
}

// A command whose output cannot be written to stdout fails, as E0616, rather
// than exiting 0 with the output lost. stdout is a file opened read-only.
func TestStdoutWriteFails(t *testing.T) {
	dir := writeTipDir(t, "Schema: {\n\tname: string\n}\n")
	dataPath := filepath.Join(t.TempDir(), "data.json")
	ro := filepath.Join(t.TempDir(), "ro.txt")
	for _, p := range []string{dataPath, ro} {
		if err := os.WriteFile(p, []byte(`{"name": "x"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, args := range map[string][]string{
		"gen openapi":             {lineageCmd, genCmd, openapiCmd, "-l", dir},
		"gen jsonschema":          {lineageCmd, genCmd, jsonschemaCmd, "-l", dir},
		"gen gobindings --stdout": {lineageCmd, genCmd, gobindingsCmd, "-l", dir, stdoutFlg},
		"gen gobindings":          {lineageCmd, genCmd, gobindingsCmd, "-l", dir},
		"data validate-any":       {dataName, "validate-any", "-l", dir, dataPath},
		"data translate":          {dataName, "translate", "-l", dir, toFlag, v00, dataPath},
	} {
		t.Run(name, func(t *testing.T) {
			stdout, err := os.Open(ro) //nolint:gosec // a file this test wrote
			if err != nil {
				t.Fatal(err)
			}
			defer stdout.Close()                     //nolint:errcheck // a read-only file
			cmd := exec.Command(os.Args[0], args...) //nolint:gosec // re-executes this test binary
			cmd.Env = append(os.Environ(), runMainEnv+"=1")
			cmd.Stdout = stdout
			var errOut bytes.Buffer
			cmd.Stderr = &errOut
			err = cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.HasPrefix(errOut.String(), "error[E0616]: stdout cannot be written") {
				t.Errorf("got %v, want E0616 for stdout, exit 1:\n%s", err, errOut.String())
			}
		})
	}
}
