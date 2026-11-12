package main

import (
	"encoding/json"
	"errors"
	"os/exec"
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

// Every command thema runs, found by walking the tree main builds, so that
// one added later is covered without being listed here, reports what it
// cannot do through report: with --diag-format json, it exits 1, writes
// nothing to stderr, and writes diagnostics to stdout, none of them E09xx,
// the codes of thema's own failures. Each leaf is run with a flag it does
// not take, with none of its flags or arguments, and, where it takes at
// most some number of arguments, with one more.
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
// on stdin, and requires that it fail as report reports a failure.
func requireReported(t *testing.T, args []string) {
	t.Helper()
	out, errOut, err := runThema(t, t.TempDir(), "", append(args, "--diag-format", "json")...)
	line := strings.Join(args, " ")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("thema %s: %v, want exit status 1\nstdout:\n%s\nstderr:\n%s", line, err, out, errOut)
	}
	if errOut != "" {
		t.Errorf("thema %s wrote to stderr:\n%s", line, errOut)
	}
	var res struct {
		Diagnostics []diag.Diagnostic `json:"diagnostics"`
	}
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil || len(res.Diagnostics) == 0 {
		t.Fatalf("thema %s: stdout is not diagnostics (%v):\n%s", line, jerr, out)
	}
	for _, d := range res.Diagnostics {
		if strings.HasPrefix(string(d.Code), "E09") {
			t.Errorf("thema %s: %s %s, a code of thema's own failures", line, d.Code, d.Headline)
		}
	}
}
