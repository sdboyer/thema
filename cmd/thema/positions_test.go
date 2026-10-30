package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/sdboyer/thema/diag"
)

// v01File is the file ship, boat and barge fail in.
const v01File = "history/0.1.thema"

// alikeFailures are where each lineage written by writeAlikeFleet fails
// lineage compile --check: the file below its directory, and the line.
var alikeFailures = map[string]struct {
	rel  string
	line int
}{
	shipName: {v01File, 3},
	"boat":   {v01File, 4},
	"barge":  {v01File, 5},
	"cutter": {"normalized.gen.cue", 0},
	"sloop":  {"normalized.gen.cue", 0},
}

// writeAlikeFleet writes, to a temporary directory, a collection named fleet
// whose lineages are laid out alike, file for file, and each of which fails
// lineage compile --check in a file of the same path below its directory, at
// a line of its own and on text naming the lineage: ship, boat and barge in
// history/0.1.thema, which narrows a field, and cutter and sloop in their
// normalized.gen.cue, written by lineage compile and then edited by hand. It
// returns the collection's directory.
func writeAlikeFleet(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fleet")
	write := func(name, file, text string) {
		p := filepath.Join(root, name, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, f := range alikeFailures {
		write(name, "history/0.0.thema", "Schema: {\n\t"+name+"Name: string\n}\n")
		if f.rel == v01File {
			write(name, f.rel, "Schema: {"+strings.Repeat("\n", f.line-2)+"\n\t"+name+"Name: int\n}\n")
		} else {
			write(name, v01File, "Schema: {\n\t"+name+"Name: string\n\t"+name+"Mast?: int\n}\n")
		}
	}
	if _, errOut, err := runThema(t, filepath.Dir(root), "", "lineage", "compile", "-l", "fleet"); err == nil {
		t.Fatalf("lineage compile succeeded, and ship, boat and barge should fail:\n%s", errOut)
	}
	for _, name := range []string{"cutter", "sloop"} {
		p := filepath.Join(root, name, "normalized.gen.cue")
		text := readFile(t, p)
		field := name + "Mast?: int"
		if !strings.Contains(text, field) {
			t.Fatalf("%s has no %s:\n%s", p, field, text)
		}
		write(name, "normalized.gen.cue", strings.Replace(text, field, name+"Mast?: string", 1))
	}
	return root
}

// Through the CLI, every lineage of a collection laid out alike is reported
// in its own file, and the excerpt under each position is that file's text:
// from the collection's parent, where positions are shown relative to the
// working directory, and from elsewhere, where they are shown as the host's
// paths.
func TestCollectionPositionsAlike(t *testing.T) {
	root := writeAlikeFleet(t)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []struct {
		name, cwd, arg, shown string
	}{
		{"beneath", filepath.Dir(root), "fleet", "fleet"},
		{"elsewhere", t.TempDir(), root, resolved},
	} {
		t.Run(run.name, func(t *testing.T) {
			_, errOut, err := runThema(t, run.cwd, "", "lineage", "compile", "--check", "-l", run.arg)
			if err == nil {
				t.Fatalf("lineage compile --check succeeded:\n%s", errOut)
			}
			shown := excerpts(errOut)
			for name, f := range alikeFailures {
				file := filepath.Join(run.shown, name, filepath.FromSlash(f.rel))
				lines := shown[file]
				if len(lines) == 0 {
					t.Errorf("%s: no excerpt of %s in\n%s", name, file, errOut)
					continue
				}
				if f.line != 0 {
					if _, ok := lines[f.line]; !ok {
						t.Errorf("%s: no excerpt of line %d of %s in\n%s", name, f.line, file, errOut)
					}
				}
				src := strings.Split(readFile(t, filepath.Join(root, name, filepath.FromSlash(f.rel))), "\n")
				named := false
				for n, got := range lines {
					if want := strings.TrimSpace(src[n-1]); got != want {
						t.Errorf("%s: line %d of %s is shown as %q, want %q", name, n, file, got, want)
					}
					named = named || strings.Contains(got, name)
				}
				if !named {
					t.Errorf("%s: no excerpt of %s names the lineage: %v", name, file, lines)
				}
			}

			out, _, _ := runThema(t, run.cwd, "", "lineage", "compile", "--check", "-l", run.arg, "--diag-format", "json")
			var report struct {
				Diagnostics []diag.Diagnostic `json:"diagnostics"`
			}
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatalf("%v:\n%s", err, out)
			}
			want := map[string]bool{}
			for name, f := range alikeFailures {
				want[filepath.Join(run.shown, name, filepath.FromSlash(f.rel))] = true
			}
			got := map[string]bool{}
			for _, d := range report.Diagnostics {
				if d.Primary == nil {
					t.Errorf("%s has no position", d.Code)
					continue
				}
				got[d.Primary.File] = true
				if !want[d.Primary.File] {
					t.Errorf("%s is positioned in %s, which is none of the files that fail", d.Code, d.Primary.File)
				}
			}
			if len(got) != len(want) {
				t.Errorf("positioned in %v, want each of %v", got, want)
			}
		})
	}
}

var (
	fileLine    = regexp.MustCompile(`^\s*(?:-->|:::) (.+):\d+:\d+$`)
	excerptLine = regexp.MustCompile(`^\s*(\d+) \|(.*)$`)
)

// excerpts reads rendered diagnostics back into the text of each excerpt
// line, trimmed, by the file each section shows and the line's number.
func excerpts(rendered string) map[string]map[int]string {
	m := map[string]map[int]string{}
	file := ""
	for _, l := range strings.Split(rendered, "\n") {
		if sub := fileLine.FindStringSubmatch(l); sub != nil {
			file = sub[1]
			if m[file] == nil {
				m[file] = map[int]string{}
			}
			continue
		}
		if sub := excerptLine.FindStringSubmatch(l); sub != nil && file != "" {
			n, _ := strconv.Atoi(sub[1])
			m[file][n] = strings.TrimSpace(sub[2])
		}
	}
	return m
}
