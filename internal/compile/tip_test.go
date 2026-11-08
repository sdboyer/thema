package compile

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	terrors "github.com/sdboyer/thema/errors"
)

// The codes the tip tests expect.
const (
	codeNumberedTip = "E0111"
	codeOlderParent = "E0150"
)

// files builds a lineage's files from path, text pairs.
func files(pairs ...string) []File {
	var fs []File
	for i := 0; i+1 < len(pairs); i += 2 {
		fs = append(fs, File{Path: pairs[i], Text: []byte(pairs[i+1])})
	}
	return fs
}

func codes(err error) []string {
	var cs []string
	for _, d := range diag.From(err, diag.Context{}) {
		cs = append(cs, string(d.Code))
	}
	return cs
}

const (
	v00 = "Schema: {\n\tname: string\n}\n"
	// A minor change from v00: an optional field.
	v01 = "Schema: {\n\tname:  string\n\tcrew?: int\n}\n"
	// A major change from v00: a new required field.
	v10 = "Schema: {\n\tname: string\n\tcrew: int\n}\n"
)

func TestTipLayout(t *testing.T) {
	ctx := cuecontext.New()
	cases := []struct {
		name  string
		files []File
		dev   bool
		want  []string
	}{
		{"a numbered file at the top", files("1.0.thema", v10, "history/0.0.thema", v00, TipFile, v10), false, []string{"E0148"}},
		{"a numbered file at the top, on the development path", files("1.0.thema", v10, "history/0.0.thema", v00, TipFile, v10), true, []string{"E0148"}},
		{"no tip, on the development path", files("history/0.0.thema", v00), true, []string{"E0149"}},
		{"no history", files(TipFile, v00), false, []string{"E0110"}},
		{"a history lens naming the tip", files(
			"history/0.0.thema", v00,
			"history/1.0.thema", v10+"\nForwardLens: {\n\tv0_0: _\n\ttip: {crew: 1}\n}\n",
			TipFile, v10), false, []string{codeNumberedTip}},
		{"a tip lens naming its version", files(
			"history/0.0.thema", v00,
			TipFile, v10+"\nForwardLens: {\n\tv0_0: _\n\tv1_0: {crew: 1}\n}\n"), true, []string{codeNumberedTip}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var err error
			if c.dev {
				_, err = Development(ctx, "ship", c.files)
			} else {
				_, err = Compile(ctx, "ship", c.files)
			}
			if got := codes(err); strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("codes = %v, want %v; err: %v", got, c.want, err)
			}
		})
	}
}

// The normal path reads the history only: the tip is neither compiled nor
// required.
func TestCompileIgnoresTip(t *testing.T) {
	ctx := cuecontext.New()
	for name, fs := range map[string][]File{
		"a broken tip": files("history/0.0.thema", v00, TipFile, "Schema: {\n\tname: \n"),
		"no tip":       files("history/0.0.thema", v00),
	} {
		t.Run(name, func(t *testing.T) {
			c, err := Compile(ctx, "ship", fs)
			if err != nil {
				t.Fatal(err)
			}
			if want := []thema.SyntacticVersion{thema.SV(0, 0)}; !equalVersions(c.Versions, want) {
				t.Errorf("versions = %v, want %v", c.Versions, want)
			}
			if c.Tip != nil {
				t.Errorf("tip = %v, want none", c.Tip)
			}
			if _, ok := c.Provisional(); ok {
				t.Error("a version is provisional")
			}
		})
	}
}

func TestTipVersion(t *testing.T) {
	ctx := cuecontext.New()
	cases := []struct {
		name  string
		files []File
		want  thema.SyntacticVersion
		clean bool
		label string
	}{
		{"empty history", files(TipFile, v00), thema.SV(0, 0), false, "tip (cuts as 0.0)"},
		{"a minor change", files("history/0.0.thema", v00, TipFile, v01), thema.SV(0, 1), false, "tip (cuts as 0.1)"},
		{"a major change", files("history/0.0.thema", v00, TipFile, v10), thema.SV(1, 0), false, "tip (cuts as 1.0)"},
		{"a major change after a minor", files("history/0.0.thema", v00, "history/0.1.thema", v01,
			TipFile, "Schema: {\n\tname: string\n\tcrew: string\n}\n"), thema.SV(1, 0), false, "tip (cuts as 1.0)"},
		{"clean", files("history/0.0.thema", v00, "history/0.1.thema", v01, TipFile, v01), thema.SV(0, 1), true, "tip (clean at 0.1)"},
		// The canonical form leaves out comments, field order and layout.
		{"clean, written differently", files("history/0.0.thema", v00, "history/0.1.thema", v01,
			TipFile, "// The ship.\nSchema: {crew?: int, name: string}\n"), thema.SV(0, 1), true, "tip (clean at 0.1)"},
		{"the newest schema, with a lens", files("history/0.0.thema", v00,
			TipFile, v00+"\nBackwardLens: {\n\ttip: _\n\tv0_0: {}\n}\n"), thema.SV(0, 1), false, "tip (cuts as 0.1)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tip, err := ReadTip(ctx, c.files)
			if err != nil {
				t.Fatal(err)
			}
			if tip.Version != c.want || tip.Clean != c.clean || tip.String() != c.label {
				t.Errorf("tip = %v (version %v, clean %v), want %s (version %v, clean %v)", tip, tip.Version, tip.Clean, c.label, c.want, c.clean)
			}
			cerr := CheckClean(ctx, c.files)
			if c.clean != (cerr == nil) {
				t.Errorf("CheckClean = %v, want clean %v", cerr, c.clean)
			}
			if cerr != nil && !errors.Is(cerr, terrors.ErrProvisional) {
				t.Errorf("CheckClean's error is not ErrProvisional: %v", cerr)
			}
		})
	}
}

// CheckClean reports each way the tip differs from the newest version.
func TestCheckCleanReportsEachDifference(t *testing.T) {
	ctx := cuecontext.New()
	fs := files("history/0.0.thema", v00, TipFile, v10+
		"\nForwardLens: {\n\tv0_0: _\n\ttip: {crew: 1}\n}\n\nBackwardLens: {\n\ttip: _\n\tv0_0: {crew: _drop}\n}\n")
	ds := diag.From(CheckClean(ctx, fs), diag.Context{})
	var heads []string
	for _, d := range ds {
		if d.Code != "E1003" {
			t.Errorf("code %s, want E1003", d.Code)
		}
		heads = append(heads, d.Headline)
	}
	want := []string{
		"the Schema in tip.thema differs from the one in history/0.0.thema",
		"tip.thema declares a ForwardLens",
		"tip.thema declares a BackwardLens",
	}
	if strings.Join(heads, "\n") != strings.Join(want, "\n") {
		t.Errorf("headlines:\n%s\nwant:\n%s", strings.Join(heads, "\n"), strings.Join(want, "\n"))
	}
}

func TestDevelopment(t *testing.T) {
	ctx := cuecontext.New()
	t.Run("a clean tip adds no version", func(t *testing.T) {
		c, err := Development(ctx, "ship", files("history/0.0.thema", v00, TipFile, v00))
		if err != nil {
			t.Fatal(err)
		}
		if want := []thema.SyntacticVersion{thema.SV(0, 0)}; !equalVersions(c.Versions, want) {
			t.Errorf("versions = %v, want %v", c.Versions, want)
		}
		if _, ok := c.Provisional(); ok || c.Pending != nil {
			t.Errorf("provisional %v, pending %v; want neither", ok, c.Pending)
		}
	})
	t.Run("a tip adds its version, provisionally", func(t *testing.T) {
		c, err := Development(ctx, "ship", files("history/0.0.thema", v00, TipFile, v01))
		if err != nil {
			t.Fatal(err)
		}
		if want := []thema.SyntacticVersion{thema.SV(0, 0), thema.SV(0, 1)}; !equalVersions(c.Versions, want) {
			t.Errorf("versions = %v, want %v", c.Versions, want)
		}
		if p, ok := c.Provisional(); !ok || p != thema.SV(0, 1) {
			t.Errorf("provisional = %v, %v; want 0.1", p, ok)
		}
		if c.Pending != nil {
			t.Errorf("pending: %v", c.Pending)
		}
	})
	t.Run("a major tip without its ForwardLens is pending", func(t *testing.T) {
		c, err := Development(ctx, "ship", files("history/0.0.thema", v00, TipFile, v10))
		if err != nil {
			t.Fatal(err)
		}
		if got := codes(c.Pending); strings.Join(got, ",") != "E1001" {
			t.Errorf("pending codes = %v, want [E1001]", got)
		}
		if !errors.Is(c.Pending, terrors.ErrProvisional) {
			t.Error("pending is not ErrProvisional")
		}
		for _, d := range diag.From(c.Pending, diag.Context{}) {
			if d.Severity != diag.SeverityWarning {
				t.Errorf("severity = %v, want warning", d.Severity)
			}
		}
	})
	// The derivation's findings are E1001's notes, each with its code and
	// followed by its own notes where they were, and their fixes are its help,
	// never dropped or run on into a note: the help that writes the block
	// first, then what goes in it.
	t.Run("a pending lens keeps each finding's code and help", func(t *testing.T) {
		from := "Schema: {\n\tid:    string\n\tname:  string\n}\n"
		to := "Schema: {\n\tid:   int\n\tcrew: int\n}\n"
		c, err := Development(ctx, "ship", files("history/0.0.thema", from, TipFile, to))
		if err != nil {
			t.Fatal(err)
		}
		ds := diag.From(c.Pending, diag.Context{})
		if len(ds) == 0 || ds[0].Code != diag.E1001 || !strings.Contains(ds[0].Headline, "ForwardLens") {
			t.Fatalf("want the ForwardLens pending first, got %v", ds)
		}
		var notes []string
		for _, n := range ds[0].Notes {
			if n.Pos != nil {
				notes = append(notes, fmt.Sprintf("%s:%d: %s", n.Pos.File, n.Pos.Line, n.Label))
				continue
			}
			notes = append(notes, n.Label)
		}
		wantNotes := []string{
			"tip.thema:4: the ForwardLens in tip.thema would copy v0_0.id into id, which does not accept every value of it (E0358)",
			"both 0.0 and tip declare id, so a lens that does not map it copies it",
			"id in tip was string and is now int (copying v0_0.id; compatibility rule value-narrowed)",
			"tip.thema:4: tip.thema has no ForwardLens, and tip requires crew (E0346)",
			"tip.thema:4: 0.0 declares name and tip does not, and nothing maps it (E0347)",
			"translating from 0.0 to tip needs it; until tip.thema writes it, that translation fails (E1002)",
		}
		if !slices.Equal(notes, wantNotes) {
			t.Errorf("notes:\n got %q\nwant %q", notes, wantNotes)
		}
		wantHelp := []string{
			"write ForwardLens: {v0_0: _, tip: {crew: <mapping>}} in tip.thema",
			"map id in the ForwardLens: a _match, a computed value, or a constant",
			"write <field>: v0_0.name to move it, or name: _drop",
		}
		if !slices.Equal(ds[0].Help, wantHelp) {
			t.Errorf("help:\n got %q\nwant %q", ds[0].Help, wantHelp)
		}
	})
	// A lens that is not written and must produce several fields is one
	// finding with one block to write, not one per field each offering a
	// different whole block.
	t.Run("an unwritten lens names every field it must produce once", func(t *testing.T) {
		from := "Schema: {\n\tid: string\n}\n"
		to := "Schema: {\n\tid:   string\n\tcrew: int\n\trank: int\n}\n"
		c, err := Development(ctx, "ship", files("history/0.0.thema", from, TipFile, to))
		if err != nil {
			t.Fatal(err)
		}
		ds := diag.From(c.Pending, diag.Context{})
		if len(ds) == 0 || ds[0].Code != diag.E1001 {
			t.Fatalf("want E1001 first, got %v", ds)
		}
		var notes []string
		for _, n := range ds[0].Notes {
			notes = append(notes, n.Label)
		}
		if want := "tip.thema has no ForwardLens, and tip requires crew, rank (E0346)"; !slices.Contains(notes, want) {
			t.Errorf("notes: got %q, want one %q", notes, want)
		}
		wantHelp := []string{"write ForwardLens: {v0_0: _, tip: {crew: <mapping>, rank: <mapping>}} in tip.thema"}
		if !slices.Equal(ds[0].Help, wantHelp) {
			t.Errorf("help:\n got %q\nwant %q", ds[0].Help, wantHelp)
		}
	})
	t.Run("an unwritten lens with many fields lists them in a note", func(t *testing.T) {
		var fields []string
		to := "Schema: {\n\tid: string\n"
		for _, f := range []string{"commander", "navigator", "engineer", "quartermaster", "boatswain", "surgeon", "carpenter", "lookout", "gunner", "purser", "steward"} {
			fields = append(fields, f)
			to += "\t" + f + ": string\n"
		}
		to += "}\n"
		_, err := Compile(ctx, "ship", files("history/0.0.thema", "Schema: {\n\tid: string\n}\n", "history/1.0.thema", to))
		var e0346 []diag.Diagnostic
		for _, d := range diag.From(err, diag.Context{}) {
			if d.Code == diag.E0346 {
				e0346 = append(e0346, d)
			}
		}
		if len(e0346) != 1 {
			t.Fatalf("want one E0346, got %v", e0346)
		}
		d := e0346[0]
		if want := "history/1.0.thema has no ForwardLens, and 1.0 requires 11 fields"; d.Headline != want {
			t.Errorf("headline:\n got %q\nwant %q", d.Headline, want)
		}
		if len(d.Notes) != 1 || d.Notes[0].Label != "1.0 requires "+strings.Join(fields, ", ") {
			t.Errorf("notes: %v", d.Notes)
		}
	})
	t.Run("the tip's lenses name it tip", func(t *testing.T) {
		c, err := Development(ctx, "ship", files("history/0.0.thema", v00,
			TipFile, v10+"\nForwardLens: {\n\tv0_0: _\n\ttip: {crew: len(v0_0.name)}\n}\n"))
		if err != nil {
			t.Fatal(err)
		}
		if c.Pending != nil {
			t.Errorf("pending: %v", c.Pending)
		}
		text, err := c.Text()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, `"0.0->1.0"`) {
			t.Errorf("no lens 0.0->1.0 in:\n%s", text)
		}
	})
	t.Run("a written but wrong tip lens is an error, not pending", func(t *testing.T) {
		_, err := Development(ctx, "ship", files("history/0.0.thema", v00,
			TipFile, v10+"\nForwardLens: {\n\tv0_0: _\n\ttip: {}\n}\n"))
		if err == nil {
			t.Fatal("no error")
		}
		if errors.Is(err, terrors.ErrProvisional) {
			t.Errorf("the error is ErrProvisional: %v", err)
		}
	})
}

func TestDecompileWritesTip(t *testing.T) {
	ctx := cuecontext.New()
	c, err := Compile(ctx, "ship", files("history/0.0.thema", v00,
		"history/1.0.thema", v10+"\nForwardLens: {\n\tv0_0: _\n\tv1_0: {crew: 1}\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, out, err := Decompile(ctx.BuildFile(c.File))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	var tip string
	for _, f := range out {
		paths = append(paths, f.Path)
		if f.Path == TipFile {
			tip = string(f.Text)
		}
	}
	if want := "history/0.0.thema,history/1.0.thema,tip.thema"; strings.Join(paths, ",") != want {
		t.Errorf("paths = %v, want %s", paths, want)
	}
	if strings.Contains(tip, "Lens") {
		t.Errorf("the tip has a lens:\n%s", tip)
	}
	got, err := ReadTip(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Clean || got.Version != thema.SV(1, 0) {
		t.Errorf("the decompiled tip is %v, want clean at 1.0", got)
	}
}

func equalVersions(a, b []thema.SyntacticVersion) bool {
	return slices.Equal(a, b)
}

// A lens of the tip that names a version at the parent's end, other than the
// newest under history/, was written against an older parent (E0150); a
// version named at the tip's end names the tip, whose instance is tip (E0111).
func TestTipLensStaleParent(t *testing.T) {
	ctx := cuecontext.New()
	history := []string{"history/0.0.thema", v00, "history/0.1.thema", v01}
	withTip := func(tip string) []File { return files(append(history, TipFile, tip)...) }
	cases := []struct {
		name, tip            string
		code, headline, help string
	}{
		{"forward, against an older parent",
			v10 + "\nForwardLens: {\n\tv0_0: _\n\ttip: {crew: 1}\n}\n",
			codeOlderParent, "ForwardLens in tip.thema names v0_0, but the tip's parent is 0.1",
			"declare v0_1: _ in place of v0_0, and review the mappings against 0.1's schema"},
		{"backward, against an older parent",
			v01 + "\nBackwardLens: {\n\ttip: _\n\tv0_0: {crew: _drop}\n}\n",
			codeOlderParent, "BackwardLens in tip.thema names v0_0, but the tip's parent is 0.1",
			"name the body v0_1 in place of v0_0, and review its mappings against 0.1's schema"},
		{"forward, naming neither end, against an older parent",
			v10 + "\nForwardLens: {\n\tv0_0: _\n\tv1_0: {crew: 1}\n}\n",
			codeOlderParent, "ForwardLens in tip.thema names v0_0, but the tip's parent is 0.1", ""},
		{"forward, numbering the tip",
			v10 + "\nForwardLens: {\n\tv0_1: _\n\tv1_0: {crew: 1}\n}\n",
			codeNumberedTip, "ForwardLens names the tip's instance v1_0, which has no version yet", ""},
		{"backward, numbering the tip",
			v01 + "\nBackwardLens: {\n\tv0_2: _\n\tv0_1: {}\n}\n",
			codeNumberedTip, "BackwardLens names the tip's instance v0_2, which has no version yet", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Development(ctx, "ship", withTip(c.tip))
			ds := diag.From(err, diag.Context{})
			if len(ds) != 1 {
				t.Fatalf("%d diagnostics, want 1: %v", len(ds), err)
			}
			d := ds[0]
			if string(d.Code) != c.code || d.Headline != c.headline {
				t.Errorf("got %s %q, want %s %q", d.Code, d.Headline, c.code, c.headline)
			}
			if c.help != "" && (len(d.Help) != 1 || d.Help[0] != c.help) {
				t.Errorf("help = %q, want %q", d.Help, c.help)
			}
			if d.Primary == nil || d.Primary.File != TipFile {
				t.Errorf("primary = %v, want in %s", d.Primary, TipFile)
			}
		})
	}
}

// NewTip never writes CUE's wrapper for a closed value, {_#def, _#def: ...},
// into a schema: a version file holding it declares a hidden definition,
// which bind refuses (E0151). A value reached through a definition is
// recursively closed where it is held, and is written without that
// closedness, keeping what its contents close themselves (#256); a schema
// declaring a definition of its own, or a field named _#def, is written as it
// is.
func TestNewTipClosedSchema(t *testing.T) {
	ctx := cuecontext.New()
	v := ctx.CompileString("#Foo: {a!: string, c: close({x: int}), e: {#E, #E: {z: int}}}\nopen: {#Foo: {a!: string}, b?: int}\nown: {_#def, _#def: {a!: string}}")
	f, err := NewTip(v.LookupPath(cue.ParsePath("#Foo")))
	if err != nil {
		t.Fatalf("NewTip of a definition: %v", err)
	}
	want := "Schema: {\n\ta!: string\n\tc:  close({x: int})\n\te: {\n\t\t#E\n\t\t#E: {\n\t\t\tz: int\n\t\t}\n\t}\n}\n"
	if string(f.Text) != want {
		t.Errorf("NewTip of a definition wrote\n%s\nwant\n%s", f.Text, want)
	}
	f, err = NewTip(v.LookupPath(cue.ParsePath("open")))
	if err != nil {
		t.Fatalf("NewTip of an open schema with a definition: %v", err)
	}
	if strings.Contains(string(f.Text), "_#def") {
		t.Errorf("NewTip wrote the wrapper:\n%s", f.Text)
	}
	f, err = NewTip(v.LookupPath(cue.ParsePath("own")))
	if err != nil {
		t.Fatalf("NewTip of a schema declaring _#def: %v", err)
	}
	if !strings.Contains(string(f.Text), "_#def: {") {
		t.Errorf("NewTip did not write the schema's own _#def:\n%s", f.Text)
	}
}
