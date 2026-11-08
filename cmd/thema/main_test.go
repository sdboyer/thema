package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/sdboyer/thema/diag"
)

// A command line cobra cannot take is E0620, whichever part of it is wrong,
// and is reported as --diag-format asks even where it follows what cobra
// stopped at. An error a command's own code returns keeps its code.
func TestCommandLine(t *testing.T) {
	for _, args := range [][]string{
		{"bogus"},
		{lineageCmd, tipName, "extra"},
		{"--bogus"},
		{lineageCmd, tipName, "--bogus"},
		{"data", "validate", "a.json", "b.json"},
		{lineageCmd, "check", "-l"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if got := initCodes(t, t.TempDir(), args...); !slices.Equal(got, []diag.Code{diag.E0620}) {
				t.Errorf("codes %v, want [E0620]", got)
			}
		})
	}

	t.Run("a command's own error", func(t *testing.T) {
		got := initCodes(t, t.TempDir(), "data", "validate", "-l", "missing", "data.json")
		if slices.Contains(got, diag.E0620) || len(got) == 0 {
			t.Errorf("codes %v, want the command's own, not E0620", got)
		}
	})
}
