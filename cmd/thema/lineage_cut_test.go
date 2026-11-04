package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sdboyer/thema/authoring"
)

func TestLineageCut(t *testing.T) {
	historyV01 := "history/0.1.thema"
	cases := []struct {
		name, tip string
		dryRun    bool
		out       string
		code      string // in stderr, when the run fails
		written   bool
	}{
		{"clean", cleanTip, false, "tip (clean at 0.0): nothing to cut\n", "", false},
		{"minor", minorTip, false, "cut the tip as 0.1\n" + historyV01 + "\n" + authoring.TipFile + "\n", "", true},
		{"dry run", minorTip, true, "would cut the tip as 0.1\n" + historyV01 + "\n" + authoring.TipFile + "\n", "", false},
		{"missing lens", majorTip, false, "", "E1004", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTipDir(t, tc.tip)
			args := []string{lineageCmd, "cut", "-l", dir}
			if tc.dryRun {
				args = append(args, "--dry-run")
			}
			out, errOut, err := runThema(t, dir, "", args...)
			if tc.code != "" {
				if err == nil || !strings.Contains(errOut, tc.code) {
					t.Fatalf("want %s, got err %v, stderr:\n%s", tc.code, err, errOut)
				}
			} else if err != nil {
				t.Fatalf("%v\n%s", err, errOut)
			}
			if out != tc.out {
				t.Errorf("stdout:\n%s\nwant:\n%s", out, tc.out)
			}

			frozen, ferr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(historyV01))) //nolint:gosec // a file under t.TempDir()
			tip, terr := os.ReadFile(filepath.Join(dir, authoring.TipFile))                 //nolint:gosec // a file under t.TempDir()
			if terr != nil {
				t.Fatal(terr)
			}
			if !tc.written {
				if ferr == nil {
					t.Errorf("%s written", historyV01)
				}
				if string(tip) != tc.tip {
					t.Errorf("tip.thema changed:\n%s", tip)
				}
				return
			}
			if ferr != nil {
				t.Fatal(ferr)
			}
			if string(frozen) != tc.tip {
				t.Errorf("%s:\n%s\nwant:\n%s", historyV01, frozen, tc.tip)
			}
			if string(tip) != tc.tip {
				t.Errorf("tip.thema after the cut:\n%s\nwant:\n%s", tip, tc.tip)
			}

			// The tip is clean after the cut.
			out, errOut, err = runThema(t, dir, "", lineageCmd, tipName, "-l", dir)
			if err != nil {
				t.Fatalf("%v\n%s", err, errOut)
			}
			if out != "tip (clean at 0.1)\n" {
				t.Errorf("tip after the cut: %q", out)
			}
		})
	}
}

// TestLineageCutFirst: the first cut, of a lineage with no history/ yet,
// creates history/ and cuts the tip as 0.0.
func TestLineageCutFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ship")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, authoring.TipFile), []byte(cleanTip), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := runThema(t, dir, "", lineageCmd, "cut", "-l", dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if want := "cut the tip as 0.0\n" + historyV00 + "\n" + authoring.TipFile + "\n"; out != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", out, want)
	}
	got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(historyV00))) //nolint:gosec // a file under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != cleanTip {
		t.Errorf("%s:\n%s", historyV00, got)
	}
}

// TestLineageCutFrozenFirst: a cut whose frozen version cannot be written
// leaves the tip as it was, lenses and notes included, rather than resetting
// it to clean with nothing frozen.
func TestLineageCutFrozenFirst(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not stop root")
	}
	dir := writeTipDir(t, minorTip)
	history := filepath.Join(dir, "history")
	if err := os.Chmod(history, 0o555); err != nil { //nolint:gosec // a directory under t.TempDir()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(history, 0o750) }) //nolint:gosec // a directory under t.TempDir()

	_, errOut, err := runThema(t, dir, "", lineageCmd, "cut", "-l", dir)
	if err == nil || !strings.Contains(errOut, "E0616") {
		t.Fatalf("want E0616, got err %v, stderr:\n%s", err, errOut)
	}
	tip, err := os.ReadFile(filepath.Join(dir, authoring.TipFile)) //nolint:gosec // a file under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if string(tip) != minorTip {
		t.Errorf("tip.thema changed though nothing was frozen:\n%s", tip)
	}
}
