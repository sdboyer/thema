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
		{lineageCmd, tipName, "extra"},
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
