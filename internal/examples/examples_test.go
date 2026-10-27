package examples

import (
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"
)

// The copies are identical to their originals: ship.cue to docs/ship.cue,
// and each of invalid/ to testdata/invalidlineage. Edit the original and copy
// it here.
func TestCopiesMatchOriginals(t *testing.T) {
	pairs := map[string]string{"ship.cue": "../../docs/ship.cue"}
	names, err := fs.Glob(copies, "invalid/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		pairs[n] = "../../testdata/invalidlineage/" + path.Base(n)
	}
	for cp, orig := range pairs {
		got, err := copies.ReadFile(cp)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(orig) //nolint:gosec // a fixture of this repository
		if err != nil {
			t.Fatalf("%s copies %s, which cannot be read: %v", cp, orig, err)
		}
		if string(got) != string(want) {
			t.Errorf("internal/examples/%s differs from %s; copy it again", cp, strings.TrimPrefix(orig, "../../"))
		}
	}
}

// Every example has an ID, a title, a summary and its lineage.
func TestExamplesComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range All() {
		if seen[e.ID] {
			t.Errorf("two examples have ID %s", e.ID)
		}
		seen[e.ID] = true
		if e.Title == "" || e.Category == "" || e.Summary == "" {
			t.Errorf("%s has no title, category or summary", e.ID)
		}
		if e.Authoring && (e.Name == "" || len(e.Files) == 0) {
			t.Errorf("%s is in the authoring form but has no name or files", e.ID)
		}
		if !e.Authoring && e.Normal.Text == "" {
			t.Errorf("%s has no normal-form file", e.ID)
		}
	}
}
