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
		v00 = "Schema: {\n\tname: string\n}\n"
		// v00Edit's Schema is on its third line, where v00 has its closing
		// brace: the excerpt tells whose file it was read from.
		v00Edit = "// The ship.\n// Its name may not be empty.\nSchema: {\n\tname: string & !=\"\"\n}\n"
	)
	root := t.TempDir()
	published := writeHistory(t, filepath.Join(root, "published"), "ship", v00, map[string]string{"0.0": v00})
	cases := []struct {
		name    string
		tip     string
		history map[string]string
		code    string // in stderr, when the check fails
		excerpt string // in stderr, from the candidate's files
	}{
		{name: "identical", tip: v00, history: map[string]string{"0.0": v00}},
		{name: "appended", tip: minorTip, history: map[string]string{"0.0": v00, "0.1": minorTip}},
		{name: "a tip that differs", tip: majorTip, history: map[string]string{"0.0": v00}},
		{name: "changed", tip: v00Edit, history: map[string]string{"0.0": v00Edit}, code: "E0250", excerpt: "3 | Schema: {"},
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

// --published is required, and must bind.
func TestLineageCheckPublished(t *testing.T) {
	root := t.TempDir()
	candidate := writeHistory(t, root, "ship", cleanTip, map[string]string{"0.0": cleanTip})
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
