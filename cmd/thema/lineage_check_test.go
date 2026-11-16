package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeHistory writes a lineage named name under root, with the given
// history, each a version's number and text, and a tip, and returns its
// directory.
func writeHistory(t *testing.T, root, name, tip string, history map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "history"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"tip.thema": tip}
	for v, text := range history {
		files["history/"+v+".thema"] = text
	}
	for p, text := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// thema lineage check compares the candidate's history with the published
// lineage's, never the tip, and positions what it finds in the candidate.
func TestLineageCheck(t *testing.T) {
	const (
		ship00 = "Schema: {\n\tname: string\n}\n"
		// ship00Edit's Schema is on its third line, where ship00 has its closing
		// brace: the excerpt tells whose file it was read from.
		ship00Edit = "// The ship.\n// Its name may not be empty.\nSchema: {\n\tname: string & !=\"\"\n}\n"
	)
	root := t.TempDir()
	published := writeHistory(t, filepath.Join(root, "published"), "ship", ship00, map[string]string{v00: ship00})
	cases := []struct {
		name    string
		tip     string
		history map[string]string
		code    string // in stderr, when the check fails
		excerpt string // in stderr, from the candidate's files
	}{
		{name: "identical", tip: ship00, history: map[string]string{v00: ship00}},
		{name: "appended", tip: minorTip, history: map[string]string{v00: ship00, "0.1": minorTip}},
		{name: "a tip that differs", tip: majorTip, history: map[string]string{v00: ship00}},
		{name: "changed", tip: ship00Edit, history: map[string]string{v00: ship00Edit}, code: "E0250", excerpt: "3 | Schema: {"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			candidate := writeHistory(t, filepath.Join(t.TempDir(), "candidate"), "ship", c.tip, c.history)
			out, errOut, err := runThema(t, root, "", "lineage", "check", "-l", candidate, "--published", published)
			if out != "" {
				t.Errorf("stdout = %q, want nothing", out)
			}
			if (c.code == "") != (err == nil) || !strings.Contains(errOut, c.code) || !strings.Contains(errOut, c.excerpt) {
				t.Errorf("err = %v, stderr:\n%s\nwant code %q and excerpt %q", err, errOut, c.code, c.excerpt)
			}
		})
	}
}

// A candidate whose history has an immutability violation does not compile, so
// thema lineage check reports the violation, not how the candidate's history
// differs from the published lineage's.
func TestLineageCheckViolation(t *testing.T) {
	const (
		// crew is optional at v0.0 and a string, which v0.1's int does not
		// accept: v0.1 is numbered minor, and is not backward compatible.
		ship00Edit = "Schema: {\n\tname:  string\n\tcrew?: string\n}\n"
		v10        = "1.0"
		ship10     = "Schema: {\n\tcrew: string\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\tcrew: v0_0.name\n\t}\n}\n\nBackwardLens: {\n\tv1_0: _\n\tv0_0: {\n\t\tname: v1_0.crew\n\t}\n}\n"
		// The same lenses, with the ForwardLens reading a field v0.0 does not
		// declare.
		ship10Edit = "Schema: {\n\tcrew: string\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\tcrew: v0_0.nope\n\t}\n}\n\nBackwardLens: {\n\tv1_0: _\n\tv0_0: {\n\t\tname: v1_0.crew\n\t}\n}\n"
	)
	root := t.TempDir()
	for name, c := range map[string]struct {
		published, candidate map[string]string
		tip, code            string
	}{
		"a number the schemas contradict": {
			published: map[string]string{v00: cleanTip, "0.1": minorTip},
			candidate: map[string]string{v00: ship00Edit, "0.1": minorTip},
			tip:       minorTip, code: "E0207",
		},
		"a lens error": {
			published: map[string]string{v00: cleanTip, v10: ship10},
			candidate: map[string]string{v00: cleanTip, v10: ship10Edit},
			tip:       "Schema: {\n\tcrew: string\n}\n", code: "E0209",
		},
	} {
		t.Run(name, func(t *testing.T) {
			published := writeHistory(t, filepath.Join(root, name, "published"), "ship", c.tip, c.published)
			if _, errOut, err := runThema(t, root, "", "lineage", "check", "-l", published, "--published", published); err != nil {
				t.Fatalf("the published lineage does not check against itself: %v\n%s", err, errOut)
			}
			candidate := writeHistory(t, filepath.Join(root, name, "candidate"), "ship", c.tip, c.candidate)
			_, errOut, err := runThema(t, root, "", "lineage", "check", "-l", candidate, "--published", published)
			if err == nil || !strings.Contains(errOut, c.code) || strings.Contains(errOut, "E025") {
				t.Errorf("err = %v, stderr:\n%s\nwant %s and no E025x", err, errOut, c.code)
			}
		})
	}
}

// --published is required, and must bind.
func TestLineageCheckPublished(t *testing.T) {
	root := t.TempDir()
	candidate := writeHistory(t, root, "ship", cleanTip, map[string]string{v00: cleanTip})
	for name, c := range map[string]struct {
		args []string
		code string
	}{
		"none":    {args: nil, code: "E0620"},
		"missing": {args: []string{"--published", filepath.Join(root, "nope")}, code: "E0601"},
	} {
		t.Run(name, func(t *testing.T) {
			_, errOut, err := runThema(t, root, "", append([]string{"lineage", "check", "-l", candidate}, c.args...)...)
			if err == nil || !strings.Contains(errOut, c.code) {
				t.Errorf("err = %v, stderr:\n%s\nwant %s", err, errOut, c.code)
			}
		})
	}
}
