package corpus

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/cue/parser"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/util"
)

// TestRefs runs the reference-placement corpus in testdata/refs: each case
// is one lineage text and one value that uses it, and each suite builds from
// them, in its own way, the package the lineage is bound from. Every suite must give
// the verdict docs/invariants.md gives, which the case records as #expect.
// testdata/refs/README.md says how to add a case and how to read a failure.
func TestRefs(t *testing.T) {
	Corpus{Dir: repoRoot + "/testdata/refs", Root: repoRoot, Layout: refsLayout, Suites: refSuites, Check: checkAt}.Run(t)
}

// refsLayout is how the refs corpus is read: an archive is one case, and
// says where the lineage goes (#at).
var refsLayout = Layout{Require: []string{"at"}}

// checkAt fails a case whose #at is not a field of package m without the
// text. Such a case would bind the text alone, and test nothing about the use.
func checkAt(t testing.TB, c *Case) {
	t.Helper()
	if !loadUse(t, cuecontext.New(), c, "").LookupPath(at(c)).Exists() {
		t.Fatalf("case %s: #at: in.cue has no field %s, so the case binds the text alone; "+
			"declare the field in in.cue, or correct #at", c.Name, c.Directive("at"))
	}
}

// The suites of the refs corpus, in registry order: the ways a lineage and
// the value that uses it reach BindLineage as one package. A lineage combined
// with its use in Go (FillPath, Unify) is refused before any reference is
// read (E0152); TestRefsGoAssembly checks that for every case.
var refSuites = []Suite{
	{
		Name: "together",
		Description: "text.cue is written into package m at #at, as a file of its own beside in.cue, and the package is loaded and looked up at #at. " +
			"This is a lineage declared in a field of a loaded package, as cmd/thema -p, exemplars and testdata/cases bind it.",
		Run: func(t testing.TB, c *Case) (string, error) {
			ctx := cuecontext.New()
			return "", bindAt(ctx, loadUse(t, ctx, c, textAt(t, c)), at(c))
		},
	},
	{
		Name: "compose",
		Description: "text.cue is parsed on its own and composed into package m at #at with thema.Compose, and the package is loaded with the composed file beside in.cue. " +
			"This is how a tool composes separately compiled lineages into the package that uses them (#272).",
		Run: func(t testing.TB, c *Case) (string, error) {
			ctx := cuecontext.New()
			return "", bindAt(ctx, loadUse(t, ctx, c, "", composedText(t, c)...), at(c))
		},
	},
}

// TestRefsGoAssembly checks that every case whose text compiles on its own
// is refused (E0152) when the text is combined with its use in Go, in each
// way #269 found a caller might: filled into the use, the use filled into
// it, unified in either order, or both filled into a third root.
func TestRefsGoAssembly(t *testing.T) {
	paths, err := filepath.Glob("../../testdata/refs/*.txtar")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no cases: %v", err)
	}
	ways := map[string]func(ctx *cue.Context, use, text cue.Value, p cue.Path) (cue.Value, cue.Path){
		"fill-text": func(_ *cue.Context, use, text cue.Value, p cue.Path) (cue.Value, cue.Path) {
			return use.FillPath(p, text), p
		},
		"fill-use": func(_ *cue.Context, use, text cue.Value, p cue.Path) (cue.Value, cue.Path) {
			return text.FillPath(cue.Path{}, use.LookupPath(p)), cue.Path{}
		},
		"unify-use-text": func(_ *cue.Context, use, text cue.Value, p cue.Path) (cue.Value, cue.Path) {
			return use.LookupPath(p).Unify(text), cue.Path{}
		},
		"unify-text-use": func(_ *cue.Context, use, text cue.Value, p cue.Path) (cue.Value, cue.Path) {
			return text.Unify(use.LookupPath(p)), cue.Path{}
		},
		"third-root": func(ctx *cue.Context, use, text cue.Value, p cue.Path) (cue.Value, cue.Path) {
			x := cue.ParsePath("x")
			return ctx.CompileString("x: _").FillPath(x, text).FillPath(x, use.LookupPath(p)), x
		},
	}
	for _, path := range paths {
		a, err := ReadArchive(path, refsLayout)
		if err != nil {
			t.Fatal(err)
		}
		c := a.Cases[0]
		if ok, _ := textCompilesAlone(c); !ok {
			continue
		}
		for name, way := range ways {
			t.Run(c.Name+"/"+name, func(t *testing.T) {
				ctx := cuecontext.New()
				root, p := way(ctx, loadUse(t, ctx, c, ""), compileText(t, ctx, c), at(c))
				ds := diag.Diagnostics(bindAt(ctx, root, p))
				if len(ds) != 1 || ds[0].Code != diag.E0152 {
					t.Errorf("want E0152, got %v", ds)
				}
			})
		}
	}
}

func bindAt(ctx *cue.Context, root cue.Value, p cue.Path) error {
	_, err := thema.BindLineage(root, p, thema.NewRuntime(ctx))
	return err
}

// composedText returns text.cue composed into package m at #at
// (thema.Compose).
func composedText(t testing.TB, c *Case) []*ast.File {
	t.Helper()
	text, ok := c.File("text.cue")
	if !ok {
		t.Fatalf("%s has no text.cue", c.Path)
	}
	f, err := parser.ParseFile("text.cue", text)
	if err != nil {
		t.Fatalf("parsing text.cue: %v", err)
	}
	files, err := thema.Compose("m", thema.Placement{Path: at(c), File: f})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func at(c *Case) cue.Path { return cue.ParsePath(c.Directive("at")) }

// textCompilesAlone applies a suite only to a case whose text compiles in a
// root of its own: one that refers to nothing outside itself but CUE's
// builtin packages.
func textCompilesAlone(c *Case) (bool, string) {
	text, _ := c.File("text.cue")
	if err := cuecontext.New().CompileString(text).Err(); err != nil {
		first, _, _ := strings.Cut(err.Error(), "\n")
		return false, "the text does not compile on its own: " + first
	}
	return true, ""
}

func compileText(t testing.TB, ctx *cue.Context, c *Case, opts ...cue.BuildOption) cue.Value {
	t.Helper()
	text, ok := c.File("text.cue")
	if !ok {
		t.Fatalf("%s has no text.cue", c.Path)
	}
	v := ctx.CompileString(text, opts...)
	if err := v.Err(); err != nil {
		t.Fatalf("compiling text.cue: %v", err)
	}
	return v
}

// textAt returns text.cue as a file of package m that declares the text at
// #at: its imports, then the rest of it as the body of the field.
func textAt(t testing.TB, c *Case) string {
	t.Helper()
	text, ok := c.File("text.cue")
	if !ok {
		t.Fatalf("%s has no text.cue", c.Path)
	}
	f, err := parser.ParseFile("text.cue", text, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing text.cue: %v", err)
	}
	if f.PackageName() != "" {
		t.Fatalf("%s: text.cue has a package clause; the text is a struct body without one", c.Path)
	}
	split := 0
	for d := range f.ImportDecls() {
		split = d.End().Offset()
	}
	if i := strings.IndexByte(text[split:], '\n'); split > 0 && i >= 0 {
		split += i + 1
	}
	var field strings.Builder
	for _, sel := range at(c).Selectors() {
		fmt.Fprintf(&field, "%s: ", sel)
	}
	// The package clause is the only line added before the text, so a
	// position in the file is one line below the same position in text.cue.
	return fmt.Sprintf("package m\n%s%s{%s}\n", text[:split], field.String(), text[split:])
}

// loadUse loads package m, the use: in.cue and the case's other packages,
// text as /m/text.cue when it is not empty, and the extra files by their
// names. It returns the package's root.
func loadUse(t testing.TB, ctx *cue.Context, c *Case, text string, extra ...*ast.File) cue.Value {
	t.Helper()
	cfg := &load.Config{
		Dir: "/m",
		Overlay: map[string]load.Source{
			"/m/cue.mod/module.cue": load.FromString(util.ModuleFile("example.com/m")),
		},
	}
	for _, f := range c.Inputs() {
		switch {
		case f.Name == "text.cue" || !strings.HasSuffix(f.Name, ".cue"):
		case f.Name == "in.cue" && !strings.Contains(string(f.Data), "package "):
			cfg.Overlay["/m/in.cue"] = load.FromString("package m\n" + string(f.Data))
		default:
			cfg.Overlay["/m/"+f.Name] = load.FromString(string(f.Data))
		}
	}
	if _, ok := cfg.Overlay["/m/in.cue"]; !ok {
		t.Fatalf("%s has no in.cue", c.Path)
	}
	if text != "" {
		cfg.Overlay["/m/text.cue"] = load.FromString(text)
	}
	for _, f := range extra {
		cfg.Overlay["/m/"+f.Filename] = load.FromFile(f)
	}
	if err := embedreg.Configure(cfg, thema.CueJointFS); err != nil {
		t.Fatal(err)
	}
	bi := load.Instances([]string{"."}, cfg)[0]
	if bi.Err != nil {
		t.Fatalf("loading package m: %v", bi.Err)
	}
	return ctx.BuildInstance(bi)
}
