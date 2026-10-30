package compile

import (
	"errors"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	terrors "github.com/sdboyer/thema/errors"
)

// files builds a lineage's files from path, text pairs, named by their paths.
func files(pairs ...string) []File {
	var fs []File
	for i := 0; i+1 < len(pairs); i += 2 {
		fs = append(fs, File{Path: pairs[i], Name: pairs[i], Text: []byte(pairs[i+1])})
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
			TipFile, v10), false, []string{"E0111"}},
		{"a tip lens naming its version", files(
			"history/0.0.thema", v00,
			TipFile, v10+"\nForwardLens: {\n\tv0_0: _\n\tv1_0: {crew: 1}\n}\n"), true, []string{"E0111"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var err error
			if c.dev {
				_, err = CompileDevelopment(ctx, "ship", c.files)
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

func TestCompileDevelopment(t *testing.T) {
	ctx := cuecontext.New()
	t.Run("a clean tip adds no version", func(t *testing.T) {
		c, err := CompileDevelopment(ctx, "ship", files("history/0.0.thema", v00, TipFile, v00))
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
		c, err := CompileDevelopment(ctx, "ship", files("history/0.0.thema", v00, TipFile, v01))
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
		c, err := CompileDevelopment(ctx, "ship", files("history/0.0.thema", v00, TipFile, v10))
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
	t.Run("the tip's lenses name it tip", func(t *testing.T) {
		c, err := CompileDevelopment(ctx, "ship", files("history/0.0.thema", v00,
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
		_, err := CompileDevelopment(ctx, "ship", files("history/0.0.thema", v00,
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
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
			"E0150", "ForwardLens in tip.thema names v0_0, but the tip's parent is 0.1",
			"declare v0_1: _ in place of v0_0, and review the mappings against 0.1's schema"},
		{"backward, against an older parent",
			v01 + "\nBackwardLens: {\n\ttip: _\n\tv0_0: {crew: _drop}\n}\n",
			"E0150", "BackwardLens in tip.thema names v0_0, but the tip's parent is 0.1",
			"name the body v0_1 in place of v0_0, and review its mappings against 0.1's schema"},
		{"forward, naming neither end, against an older parent",
			v10 + "\nForwardLens: {\n\tv0_0: _\n\tv1_0: {crew: 1}\n}\n",
			"E0150", "ForwardLens in tip.thema names v0_0, but the tip's parent is 0.1", ""},
		{"forward, numbering the tip",
			v10 + "\nForwardLens: {\n\tv0_1: _\n\tv1_0: {crew: 1}\n}\n",
			"E0111", "ForwardLens names the tip's instance v1_0, which has no version yet", ""},
		{"backward, numbering the tip",
			v01 + "\nBackwardLens: {\n\tv0_2: _\n\tv0_1: {}\n}\n",
			"E0111", "BackwardLens names the tip's instance v0_2, which has no version yet", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := CompileDevelopment(ctx, "ship", withTip(c.tip))
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
