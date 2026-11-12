package main

import (
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
