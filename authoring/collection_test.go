package authoring_test

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
)

// A collection's members are the directories directly beneath it that hold
// version files, sorted by name, each read as Load reads it.
func TestLoadCollection(t *testing.T) {
	fsys := fstest.MapFS{
		"c/b/tip.thema":                {Data: []byte(tip)},
		"c/b/example.cue":              {Data: []byte("category: \"B\"\n")},
		"c/a/history/0.0.thema":        {Data: []byte(v00)},
		"c/a/history/notes.txt":        {Data: []byte("notes\n")},
		"c/notes/a.md":                 {Data: []byte("notes\n")},
		"c/deeper/d/history/0.0.thema": {Data: []byte(v00)},
	}
	ms, err := authoring.LoadCollection(fsys, "c")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range ms {
		names = append(names, m.Name)
		if m.Err != nil {
			t.Errorf("%s: %v", m.Name, m.Err)
		}
	}
	if !slices.Equal(names, []string{"a", "b"}) {
		t.Fatalf("members %v, want a and b", names)
	}
	if len(ms[0].Files) != 1 || ms[0].Files[0].Path != "history/0.0.thema" {
		t.Errorf("a's files are %+v, want its one version file", ms[0].Files)
	}
	if len(ms[1].Files) != 1 || ms[1].Files[0].Path != authoring.TipFile {
		t.Errorf("b's files are %+v, want its tip", ms[1].Files)
	}
}

// A lineage whose directory cannot be read is a member with its error, and
// the others are read.
func TestLoadCollectionUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory its owner cannot read")
	}
	root := t.TempDir()
	for name, text := range map[string]string{
		"a/history/0.0.thema": v00,
		"b/history/0.0.thema": v00,
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	locked := filepath.Join(root, "a")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	ms, err := authoring.LoadCollection(os.DirFS(root), ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Name != "a" || ms[1].Name != "b" {
		t.Fatalf("members %+v, want a and b", ms)
	}
	if ds := diag.Diagnostics(ms[0].Err); len(ds) != 1 || ds[0].Code != diag.E0601 || ms[0].Files != nil {
		t.Errorf("a is %+v, want E0601 and no files", ms[0])
	}
	if ms[1].Err != nil || len(ms[1].Files) != 1 {
		t.Errorf("b is %+v, want its one version file", ms[1])
	}
}
