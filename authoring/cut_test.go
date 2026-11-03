package authoring_test

import (
	"errors"
	"slices"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
	terrors "github.com/sdboyer/thema/errors"
)

// A major tip after v00: before is renamed after, with both lenses, a doc
// comment on its Schema, comments within it, and notes that belong to the
// edge.
const renameTip = `import "strings"

// Why the rename: the field was misnamed.

// The ship's log.
Schema: {
	// What comes after.
	after: string
	code?: strings.MaxRunes(8)
}

// The rename, both ways.
ForwardLens: {
	v0_0: _
	tip: {
		after: v0_0.before
	}
}

BackwardLens: {
	tip: _
	v0_0: {
		before: tip.after
	}
}
`

const renameFrozen = `import "strings"

// Why the rename: the field was misnamed.

// The ship's log.
Schema: {
	// What comes after.
	after: string
	code?: strings.MaxRunes(8)
}

// The rename, both ways.
ForwardLens: {
	v0_0: _
	v1_0: {
		after: v0_0.before
	}
}

BackwardLens: {
	v1_0: _
	v0_0: {
		before: v1_0.after
	}
}
`

const renameClean = `import "strings"

// The ship's log.
Schema: {
	// What comes after.
	after: string
	code?: strings.MaxRunes(8)
}
`

func cutFiles(pairs ...string) []authoring.File {
	var fs []authoring.File
	for i := 0; i+1 < len(pairs); i += 2 {
		fs = append(fs, authoring.File{Path: pairs[i], Text: []byte(pairs[i+1])})
	}
	return fs
}

func fileText(files []authoring.File, path string) (string, bool) {
	i := slices.IndexFunc(files, func(f authoring.File) bool { return f.Path == path })
	if i < 0 {
		return "", false
	}
	return string(files[i].Text), true
}

// TestCutTip: cutting a tip mints its derived version, freezes the tip with
// its lenses naming that version, leaves a clean tip, and the history after
// binds with the new version, appended.
func TestCutTip(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	cut, err := authoring.CutTip(rt, "ship", cutFiles("history/0.0.thema", v00, authoring.TipFile, renameTip))
	if err != nil {
		t.Fatal(err)
	}
	if cut.Clean || cut.Version != thema.SV(1, 0) {
		t.Fatalf("cut as %s, clean %v; want 1.0, not clean", cut.Version, cut.Clean)
	}
	if got := []string{cut.Written[0].Path, cut.Written[1].Path}; !slices.Equal(got, []string{"history/1.0.thema", authoring.TipFile}) {
		t.Errorf("wrote %v", got)
	}
	if got, _ := fileText(cut.Files, "history/1.0.thema"); got != renameFrozen {
		t.Errorf("history/1.0.thema:\n%s\nwant:\n%s", got, renameFrozen)
	}
	if got, _ := fileText(cut.Files, authoring.TipFile); got != renameClean {
		t.Errorf("tip.thema:\n%s\nwant:\n%s", got, renameClean)
	}
	if got, _ := fileText(cut.Files, "history/0.0.thema"); got != v00 {
		t.Errorf("history/0.0.thema changed:\n%s", got)
	}

	tip, err := authoring.ReadTip(rt.Context(), cut.Files)
	if err != nil {
		t.Fatal(err)
	}
	if !tip.Clean || tip.Version != thema.SV(1, 0) {
		t.Errorf("tip after the cut: %s", tip)
	}
	c, err := authoring.Compile(rt.Context(), "ship", cut.Files)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Versions, []thema.SyntacticVersion{thema.SV(0, 0), thema.SV(1, 0)}) {
		t.Errorf("history after the cut: %v", c.Versions)
	}

	// Cutting again: the tip is clean, and nothing changes.
	again, err := authoring.CutTip(rt, "ship", cut.Files)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Clean || again.Version != thema.SV(1, 0) || len(again.Written) != 0 {
		t.Errorf("cutting a clean tip: %+v", again)
	}
}

// TestCutTipMinor: a compatible tip cuts as the next minor version, and a tip
// with no history as 0.0.
func TestCutTipMinor(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	minor := "Schema: {\n\tbefore: string\n\tcrew?:  int\n}\n"
	cut, err := authoring.CutTip(rt, "ship", cutFiles("history/0.0.thema", v00, authoring.TipFile, minor))
	if err != nil {
		t.Fatal(err)
	}
	if cut.Version != thema.SV(0, 1) {
		t.Errorf("cut as %s, want 0.1", cut.Version)
	}
	if got, _ := fileText(cut.Files, "history/0.1.thema"); got != minor {
		t.Errorf("history/0.1.thema:\n%s", got)
	}

	first, err := authoring.CutTip(rt, "ship", cutFiles(authoring.TipFile, v00))
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != thema.SV(0, 0) || first.Clean {
		t.Errorf("a tip with no history cut as %s, clean %v", first.Version, first.Clean)
	}
	if got, ok := fileText(first.Files, "history/0.0.thema"); !ok || got != v00 {
		t.Errorf("history/0.0.thema: %q", got)
	}
}

// TestCutTipRefused: a tip whose edge still lacks a lens is not cut (E1004),
// and an error binding reports is returned as it is.
func TestCutTipRefused(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	// before is removed, and nothing says where it goes.
	_, err := authoring.CutTip(rt, "ship", cutFiles("history/0.0.thema", v00, authoring.TipFile, "Schema: {\n\tafter: string\n}\n"))
	if ds := diag.Diagnostics(err); len(ds) != 1 || ds[0].Code != diag.E1004 {
		t.Fatalf("want E1004, got %v", err)
	}
	if !errors.Is(err, terrors.ErrProvisional) {
		t.Error("E1004 does not unwrap to ErrProvisional")
	}

	_, err = authoring.CutTip(rt, "ship", cutFiles("history/0.0.thema", v00))
	if ds := diag.Diagnostics(err); len(ds) != 1 || ds[0].Code != diag.E0149 {
		t.Errorf("no tip: want E0149, got %v", err)
	}

	// Positioned under a directory, the refusal is positioned in the tip
	// there.
	_, err = authoring.CutTip(rt, "ship", cutFiles("history/0.0.thema", v00, authoring.TipFile, "Schema: {\n\tafter: string\n}\n"), authoring.At("fleet/ship"))
	if ds := diag.Diagnostics(err); len(ds) != 1 || ds[0].Primary == nil || ds[0].Primary.File != "fleet/ship/"+authoring.TipFile {
		t.Errorf("positioned under fleet/ship: want E1004 in fleet/ship/%s, got %v", authoring.TipFile, ds)
	}
}
