package load_test

import (
	"slices"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/cue/parser"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/util"
	tload "github.com/sdboyer/thema/load"
)

// Lineages compiled from different directories and composed into one
// package keep their own files' positions, and each binds as it would on its
// own: its forward lens is refused (E0367) at its own history/1.0.thema.
func TestComposedPositions(t *testing.T) {
	const partial = `Schema: {
	a: int32 & >=0
}

ForwardLens: {
	v0_0: _
	v1_0: {
		a: v0_0.a * 2
	}
}

BackwardLens: {
	v1_0: _
	v0_0: {
		a: v1_0.a
	}
}
`
	dirs := map[string]string{"get": "operations/get/query", "list": "operations/list/query"}
	fsys := fstest.MapFS{}
	for _, dir := range dirs {
		fsys[dir+"/history/0.0.thema"] = &fstest.MapFile{Data: []byte("Schema: {\n\ta: int32\n}\n")}
		fsys[dir+"/history/1.0.thema"] = &fstest.MapFile{Data: []byte(partial)}
	}

	ctx := cuecontext.New()
	rt := thema.NewRuntime(ctx)
	compiled := map[string]*authoring.Compiled{}
	var ps []thema.Placement
	for _, op := range []string{"get", "list"} {
		name, files, err := authoring.Load(fsys, dirs[op])
		if err != nil {
			t.Fatal(err)
		}
		c, err := authoring.Compile(ctx, name, files, authoring.At(dirs[op]))
		if err != nil {
			t.Fatal(err)
		}
		compiled[op] = c
		ps = append(ps, authoring.Place(cue.MakePath(cue.Str(op)), c))
	}
	inst, err := tload.ComposedInstance("api", ps...)
	if err != nil {
		t.Fatal(err)
	}
	whole := ctx.BuildInstance(inst)
	if whole.BuildInstance() == nil {
		t.Fatal("the composed value has no build instance")
	}

	for op, dir := range dirs {
		_, err := thema.BindLineage(whole, cue.MakePath(cue.Str(op)), rt, thema.WithVocabulary(authoring.Vocabulary(compiled[op])))
		ds := diag.Diagnostics(err)
		if len(ds) != 1 || ds[0].Code != diag.E0367 {
			t.Fatalf("%s: got %v, want one E0367", op, ds)
		}
		if p := ds[0].Primary; p == nil || p.File != dir+"/history/1.0.thema" || p.Line != 8 {
			t.Errorf("%s: E0367 is positioned at %v, want %s/history/1.0.thema:8", op, p, dir)
		}
	}
}

// Each placement is a file of its own, so two placements may import different
// packages under one name.
func TestComposedImports(t *testing.T) {
	a, _ := parser.ParseFile("a.cue", "import x \"strings\"\nv: x.ToUpper(\"q\")\n")
	b, _ := parser.ParseFile("b.cue", "import x \"list\"\nv: x.Concat([[1], [2]])\n")
	inst, err := tload.ComposedInstance("p",
		thema.Placement{Path: cue.ParsePath("a"), File: a},
		thema.Placement{Path: cue.ParsePath("b"), File: b},
	)
	if err != nil {
		t.Fatal(err)
	}
	v := cuecontext.New().BuildInstance(inst)
	if s, _ := v.LookupPath(cue.ParsePath("a.v")).String(); s != "Q" {
		t.Errorf("a.v = %q, want Q", s)
	}
	if n, _ := v.LookupPath(cue.ParsePath("b.v")).Len().Int64(); n != 2 {
		t.Errorf("b.v has %d elements, want 2", n)
	}
}

// A placement's path never captures a reference: not its file's own import
// of the same name, not a builtin another placement refers to, and a keyword
// label is still a label. The printed files, as a caller writing them to disk
// would load them, read the same as the syntax.
func TestComposedLabelsCaptureNothing(t *testing.T) {
	parse := func(src string) *ast.File {
		f, err := parser.ParseFile("p.cue", src)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	ps := []thema.Placement{
		{Path: cue.ParsePath("strings"), File: parse("import \"strings\"\nv: strings.ToUpper(\"q\")\n")},
		{Path: cue.ParsePath("int"), File: parse("v: 1\n")},
		{Path: cue.ParsePath("c"), File: parse("v: int\n")},
		{Path: cue.MakePath(cue.Str("import"), cue.Str("x")), File: parse("v: 2\n")},
	}
	check := func(t *testing.T, v cue.Value) {
		t.Helper()
		if err := v.Err(); err != nil {
			t.Fatal(err)
		}
		if s, _ := v.LookupPath(cue.ParsePath("strings.v")).String(); s != "Q" {
			t.Errorf("strings.v = %q, want Q", s)
		}
		if k := v.LookupPath(cue.ParsePath("c.v")).IncompleteKind(); k != cue.IntKind {
			t.Errorf("c.v is %v, want int", k)
		}
		if n, _ := v.LookupPath(cue.MakePath(cue.Str("import"), cue.Str("x"), cue.Str("v"))).Int64(); n != 2 {
			t.Errorf("import.x.v = %d, want 2", n)
		}
	}

	t.Run("syntax", func(t *testing.T) {
		inst, err := tload.ComposedInstance("p", ps...)
		if err != nil {
			t.Fatal(err)
		}
		check(t, cuecontext.New().BuildInstance(inst))
	})
	t.Run("printed", func(t *testing.T) {
		files, err := thema.Compose("p", ps...)
		if err != nil {
			t.Fatal(err)
		}
		cfg := &load.Config{Dir: "/p", Overlay: map[string]load.Source{
			"/p/cue.mod/module.cue": load.FromString(util.ModuleFile("example.com/p")),
		}}
		for _, f := range files {
			b, err := format.Node(f)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Overlay["/p/"+f.Filename] = load.FromBytes(b)
		}
		inst := load.Instances([]string{"."}, cfg)[0]
		if inst.Err != nil {
			t.Fatal(inst.Err)
		}
		check(t, cuecontext.New().BuildInstance(inst))
	})
}

// A definition closes what it declares, so a lineage composed under one binds
// where the package declares it a thema.Lineage, and not where nothing does:
// thema's fields, added at bind, are not allowed in the closed lineage.
func TestComposedUnderDefinition(t *testing.T) {
	for _, tc := range []struct {
		name, use string
		want      []diag.Code
	}{
		{"declared a lineage", "import \"github.com/sdboyer/thema\"\n\n#Op: h: thema.Lineage\n", nil},
		{"not declared", "#Op: h: _\n", []diag.Code{diag.E0126, diag.E0126}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, err := parser.ParseFile("text.cue", "name: \"h\"\nschemas: \"0\": \"0\": schema: {a?: string}\n")
			if err != nil {
				t.Fatal(err)
			}
			files, err := thema.Compose("m", thema.Placement{Path: cue.ParsePath("#Op.h"), File: text})
			if err != nil {
				t.Fatal(err)
			}
			cfg := &load.Config{
				Dir: "/m",
				Overlay: map[string]load.Source{
					"/m/cue.mod/module.cue": load.FromString(util.ModuleFile("example.com/m")),
					"/m/in.cue":             load.FromString("package m\n\n" + tc.use),
				},
			}
			for _, f := range files {
				cfg.Overlay["/m/"+f.Filename] = load.FromFile(f)
			}
			if err = tload.Configure(cfg); err != nil {
				t.Fatal(err)
			}
			inst := load.Instances([]string{"."}, cfg)[0]
			if inst.Err != nil {
				t.Fatal(inst.Err)
			}
			ctx := cuecontext.New()
			_, err = thema.BindLineage(ctx.BuildInstance(inst).LookupPath(cue.ParsePath("#Op.h")), thema.NewRuntime(ctx))
			var got []diag.Code
			for _, d := range diag.Diagnostics(err) {
				got = append(got, d.Code)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("bind reported %v, want %v", diag.Diagnostics(err), tc.want)
			}
		})
	}
}

// A lineage composed into the caller's own package, at a field typed by a
// definition that narrows its joinSchema with a definition beside it, binds:
// the package's own declarations stay outside the lineage. #260 is this case
// with the lineage filled in with FillPath, where bind reports #J as declared
// in the lineage.
func TestComposedIntoPackage(t *testing.T) {
	text, err := parser.ParseFile("text.cue", "schemas: \"0\": \"0\": schema: {a?: string}\n")
	if err != nil {
		t.Fatal(err)
	}
	files, err := thema.Compose("m", thema.Placement{Path: cue.ParsePath("lin.h"), File: text})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &load.Config{
		Dir: "/m",
		Overlay: map[string]load.Source{
			"/m/cue.mod/module.cue": load.FromString(util.ModuleFile("example.com/m")),
			"/m/in.cue": load.FromString(`package m

import "github.com/sdboyer/thema"

#J: {[string]: string, ...}
#Op: h: thema.Lineage & {name: "h", joinSchema: #J}
lin: #Op
`),
		},
	}
	for _, f := range files {
		cfg.Overlay["/m/"+f.Filename] = load.FromFile(f)
	}
	if err := tload.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	inst := load.Instances([]string{"."}, cfg)[0]
	if inst.Err != nil {
		t.Fatal(inst.Err)
	}
	ctx := cuecontext.New()
	if _, err := thema.BindLineage(ctx.BuildInstance(inst), cue.ParsePath("lin.h"), thema.NewRuntime(ctx)); err != nil {
		t.Errorf("the composed lineage does not bind: %v", diag.Diagnostics(err))
	}
}
