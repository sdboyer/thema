package corpus

import (
	"fmt"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/cue/parser"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/util"
)

// TestRefs runs the reference-placement corpus in testdata/refs: each case
// is one lineage text and one value that uses it, and each suite builds the
// value passed to BindLineage from them in its own way. Every suite must give
// the verdict docs/invariants.md gives, which the case records as #expect.
// testdata/refs/README.md says how to add a case and how to read a failure.
func TestRefs(t *testing.T) {
	Corpus{Dir: "../../testdata/refs", Suites: refSuites, Require: []string{"at"}}.Run(t)
}

// The suites of the refs corpus, in registry order. Each Description names
// the assemblies of the reference-placement ground truth (#269) the suite
// stands for, by their ids there.
var refSuites = []Suite{
	{
		Name: "together",
		Description: "text.cue is written into package m at #at, as a file of its own beside in.cue, and the package is loaded and looked up at #at. " +
			"This is a lineage declared in a field of a loaded package, as cmd/thema -p, exemplars and testdata/cases bind it " +
			"(ground truth: field-in-loaded-package, split-declaration-reunified, definition-holder-literal). Every other suite should agree with it.",
		Bind: func(t testing.TB, c *Case) error {
			ctx := cuecontext.New()
			v := loadUse(t, ctx, c, textAt(t, c)).LookupPath(at(c))
			return bindValue(ctx, v)
		},
	},
	{
		Name: "fill-text",
		Description: "package m is loaded without the text, the text is compiled on its own with ctx.CompileString and no file name, " +
			"and it is filled into the use: use.FillPath(at, text).LookupPath(at). This is how a tool composes separately compiled lineages into one value (#260, authoring.At) " +
			"(ground truth: fill-text-into-use-field, fill-text-into-definition-use).",
		Applies: textCompilesAlone,
		Bind: func(t testing.TB, c *Case) error {
			ctx := cuecontext.New()
			use, text := loadUse(t, ctx, c, ""), compileText(t, ctx, c)
			return bindValue(ctx, use.FillPath(at(c), text).LookupPath(at(c)))
		},
	},
	{
		Name: "fill-text-named",
		Description: "As fill-text, but the text is compiled with cue.Filename(\"text.cue\"), as a tool that compiles a lineage from its own file does " +
			"(ground truth: fill-text-into-use-field and fill-text-into-definition-use with the text's file name, which the ground truth found never changes a verdict).",
		Applies: textCompilesAlone,
		Bind: func(t testing.TB, c *Case) error {
			ctx := cuecontext.New()
			use, text := loadUse(t, ctx, c, ""), compileText(t, ctx, c, cue.Filename("text.cue"))
			return bindValue(ctx, use.FillPath(at(c), text).LookupPath(at(c)))
		},
	},
	{
		Name: "fill-use",
		Description: "The text is compiled on its own and the use's value at #at is filled into the text's root: text.FillPath(cue.Path{}, use.LookupPath(at)). " +
			"No caller in the tree does this; #264 records it (ground truth: go-root-receiver, its fillpath-root variant).",
		Applies: textCompilesAlone,
		Bind: func(t testing.TB, c *Case) error {
			ctx := cuecontext.New()
			use, text := loadUse(t, ctx, c, ""), compileText(t, ctx, c)
			return bindValue(ctx, text.FillPath(cue.Path{}, use.LookupPath(at(c))))
		},
	},
	{
		Name: "unify-use-text",
		Description: "The use's value at #at is unified in Go with the text compiled on its own: use.LookupPath(at).Unify(text). " +
			"No caller in the tree does this; #263's test matrix did (ground truth: go-unify-field-receiver).",
		Applies: textCompilesAlone,
		Bind: func(t testing.TB, c *Case) error {
			ctx := cuecontext.New()
			use, text := loadUse(t, ctx, c, ""), compileText(t, ctx, c)
			return bindValue(ctx, use.LookupPath(at(c)).Unify(text))
		},
	},
	{
		Name: "unify-text-use",
		Description: "The text compiled on its own is unified in Go with the use's value at #at: text.Unify(use.LookupPath(at)). " +
			"No caller in the tree does this; #264 records it (ground truth: go-root-receiver).",
		Applies: textCompilesAlone,
		Bind: func(t testing.TB, c *Case) error {
			ctx := cuecontext.New()
			use, text := loadUse(t, ctx, c, ""), compileText(t, ctx, c)
			return bindValue(ctx, text.Unify(use.LookupPath(at(c))))
		},
	},
	{
		Name: "third-root",
		Description: "The text and the use's value at #at are both filled into field x of a third root: " +
			"ctx.CompileString(\"x: _\").FillPath(x, text).FillPath(x, use.LookupPath(at)).LookupPath(x). " +
			"No caller in the tree does this; #264 records it (ground truth: third-root-both-filled).",
		Applies: textCompilesAlone,
		Bind: func(t testing.TB, c *Case) error {
			ctx := cuecontext.New()
			use, text := loadUse(t, ctx, c, ""), compileText(t, ctx, c)
			x := cue.ParsePath("x")
			return bindValue(ctx, ctx.CompileString("x: _").FillPath(x, text).FillPath(x, use.LookupPath(at(c))).LookupPath(x))
		},
	},
}

func bindValue(ctx *cue.Context, v cue.Value) error {
	_, err := thema.BindLineage(v, thema.NewRuntime(ctx))
	return err
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
// and text as /m/text.cue when it is not empty. It returns the package's
// root.
func loadUse(t testing.TB, ctx *cue.Context, c *Case, text string) cue.Value {
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
	if err := embedreg.Configure(cfg, thema.CueJointFS); err != nil {
		t.Fatal(err)
	}
	bi := load.Instances([]string{"."}, cfg)[0]
	if bi.Err != nil {
		t.Fatalf("loading package m: %v", bi.Err)
	}
	return ctx.BuildInstance(bi)
}
