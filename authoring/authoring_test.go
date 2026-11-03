package authoring_test

import (
	"bytes"
	"errors"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/parser"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
	terrors "github.com/sdboyer/thema/errors"
	tload "github.com/sdboyer/thema/load"
)

const v00 = "Schema: {\n\tbefore: string\n}\n"

const v10 = `Schema: {
	after: string
}

ForwardLens: {
	v0_0: _
	v1_0: {
		after: v0_0.before
	}
}
`

// tip is the tip just after 1.0 was cut: 1.0's schema, with no lenses.
const tip = "Schema: {\n\tafter: string\n}\n"

// The pieces, used as a tool would: load, compile, build, decompile, and
// format, with the decompiled files equal to the formatted input.
func TestPieces(t *testing.T) {
	fsys := fstest.MapFS{
		"ship/history/0.0.thema": {Data: []byte(v00)},
		"ship/history/1.0.thema": {Data: []byte(v10)},
		"ship/tip.thema":         {Data: []byte(tip)},
	}
	name, files, err := authoring.Load(fsys, "ship")
	if err != nil {
		t.Fatal(err)
	}
	if name != "ship" || len(files) != 3 {
		t.Fatalf("Load: name %q, %d files", name, len(files))
	}
	ctx := cuecontext.New()
	c, err := authoring.Compile(ctx, name, files)
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if len(c.Versions) != 2 {
		t.Fatalf("versions: %v", c.Versions)
	}
	v := ctx.BuildFile(c.File)
	if verr := v.Err(); verr != nil {
		t.Fatal(verr)
	}
	dname, dfiles, err := authoring.Decompile(v)
	if err != nil {
		t.Fatal(err)
	}
	if dname != name {
		t.Errorf("Decompile: name %q", dname)
	}
	want := map[string]string{"history/0.0.thema": v00, "history/1.0.thema": v10, "tip.thema": tip}
	if len(dfiles) != len(want) {
		t.Errorf("Decompile: %d files, want %d", len(dfiles), len(want))
	}
	for _, f := range dfiles {
		formatted, err := authoring.Format([]byte(want[f.Path]))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(f.Text, formatted) {
			t.Errorf("%s decompiled as\n%s\nwant\n%s", f.Path, f.Text, formatted)
		}
	}
}

// devHistory and devTip are a lineage whose tip removes b and writes no
// lens for it, so the lenses on its edge are pending (E1001).
const (
	devHistory = "Schema: {\n\ta: string\n\tb: int\n}\n"
	devTip     = "Schema: {\n\ta: string\n}\n"
)

// A tip whose edge lacks a lens, compiled on the development path, built,
// and unified with a joinSchema in an enclosing struct, binds as that struct
// holds it: provisional at the version the tip cuts as, and with the
// joinSchema applied (#255).
func TestBindDevelopmentValue(t *testing.T) {
	ctx := cuecontext.New()
	rt := thema.NewRuntime(ctx)
	c, err := authoring.CompileDevelopment(ctx, "query", []authoring.File{
		{Path: "history/0.0.thema", Text: []byte(devHistory)},
		{Path: authoring.TipFile, Text: []byte(devTip)},
	})
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	ds := diag.Diagnostics(c.Pending)
	if len(ds) == 0 {
		t.Fatal("want the tip's lenses pending (E1001)")
	}
	for _, d := range ds {
		if d.Code != diag.E1001 {
			t.Fatalf("want only E1001 pending, got %v", ds)
		}
	}
	tipV, ok := c.Provisional()
	if !ok {
		t.Fatal("the tip is not provisional")
	}

	ops, err := parser.ParseFile("ops.cue", `operations: get: query: joinSchema: {kind?: "get"}`)
	if err != nil {
		t.Fatal(err)
	}
	at := cue.ParsePath("operations.get.query")
	bi, err := tload.ComposedInstance("api", thema.Placement{File: ops}, authoring.Place(at, c))
	if err != nil {
		t.Fatal(err)
	}
	lin, err := authoring.BindDevelopmentValue(rt, c, ctx.BuildInstance(bi), at)
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if got := lin.Latest().Version(); got != tipV {
		t.Errorf("newest version %s, want the tip's, %s", got, tipV)
	}
	if _, verr := lin.Latest().Validate(ctx.CompileString(`{a: "x", kind: "get"}`)); verr != nil {
		t.Errorf("the joinSchema does not apply to the tip: %v", verr)
	}
	if _, verr := lin.Latest().Validate(ctx.CompileString(`{a: "x", kind: "list"}`)); verr == nil {
		t.Error("the tip accepts a kind the joinSchema rejects")
	}
	alone, err := authoring.BindDevelopment(rt, c)
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	if _, verr := alone.Latest().Validate(ctx.CompileString(`{a: "x", kind: "get"}`)); verr == nil {
		t.Error("the tip bound alone accepts kind, so the joinSchema is not what admits it")
	}
	inst, err := lin.First().Validate(ctx.CompileString(`{a: "x", b: 1}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := inst.Translate(tipV); !errors.Is(err, terrors.ErrProvisional) {
		t.Errorf("translating across the pending lens: want E1002, got %v", err)
	}
}

// BindDevelopmentValue binds only the lineage c compiled to: its tolerance
// of lenses missing from the tip's edge is c's, and applied to another
// lineage it would tolerate lenses c never reported pending.
func TestBindDevelopmentValueGuard(t *testing.T) {
	ctx := cuecontext.New()
	rt := thema.NewRuntime(ctx)
	compileDev := func(name, tip string) *authoring.Compiled {
		t.Helper()
		c, err := authoring.CompileDevelopment(ctx, name, []authoring.File{
			{Path: "history/0.0.thema", Text: []byte(devHistory)},
			{Path: authoring.TipFile, Text: []byte(tip)},
		})
		if err != nil {
			t.Fatal(cerrors.Details(err, nil))
		}
		return c
	}
	pending := compileDev("query", devTip)
	// written is the same lineage with the tip's lenses written: nothing is
	// pending, but its tip is still provisional.
	written := compileDev("query", devTip+"\nForwardLens: {\n\tv0_0: _\n\ttip: {\n\t\tb: _drop\n\t}\n}\n\nBackwardLens: {\n\ttip: _\n\tv0_0: {\n\t\tb: 0\n\t}\n}\n")
	if written.Pending != nil {
		t.Fatalf("want nothing pending: %v", written.Pending)
	}
	history, err := authoring.Compile(ctx, "query", []authoring.File{{Path: "history/0.0.thema", Text: []byte(devHistory)}})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		c    *authoring.Compiled
		v    cue.Value
	}{
		{"another name", compileDev("boat", devTip), ctx.BuildFile(pending.File)},
		{"another version set", pending, ctx.BuildFile(history.File)},
		{"a lens compiled and missing", written, ctx.BuildFile(pending.File)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := authoring.BindDevelopmentValue(rt, tc.c, tc.v, cue.Path{})
			if ds := diag.Diagnostics(err); len(ds) != 1 || ds[0].Code != diag.E0626 {
				t.Errorf("want E0626, got %v", ds)
			}
		})
	}

	// More lenses than were compiled narrow nothing c tolerates, and the
	// lineage as compiled always binds.
	if _, err := authoring.BindDevelopmentValue(rt, pending, ctx.BuildFile(written.File), cue.Path{}); err != nil {
		t.Errorf("a value with every lens compiled, and more: %v", err)
	}
}

// Two lineages loaded from different directories of one fs.FS, compiled with
// their files positioned under their directories, and composed into one
// value, name their own files: in the value, and in bind's diagnostics, though
// each lineage's files have the same paths within its directory (#254).
func TestAtComposed(t *testing.T) {
	// The forward lens computes a value its target may reject, which bind
	// reports in history/1.0.thema.
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
	fsys := fstest.MapFS{}
	dirs := map[string]string{"get": "operations/get/query", "list": "operations/list/query"}
	for _, dir := range dirs {
		fsys[dir+"/history/0.0.thema"] = &fstest.MapFile{Data: []byte("Schema: {\n\ta: int32\n}\n")}
		fsys[dir+"/history/1.0.thema"] = &fstest.MapFile{Data: []byte(partial)}
	}

	ctx := cuecontext.New()
	rt := thema.NewRuntime(ctx)
	compiled := map[string]*authoring.Compiled{}
	var ps []thema.Placement
	for _, op := range []string{"get", "list"} {
		dir := dirs[op]
		name, files, err := authoring.Load(fsys, dir)
		if err != nil {
			t.Fatal(err)
		}
		c, err := authoring.Compile(ctx, name, files, authoring.At(dir))
		if err != nil {
			t.Fatal(cerrors.Details(err, nil))
		}
		compiled[op] = c
		ps = append(ps, authoring.Place(cue.MakePath(cue.Str(op)), c))
	}
	inst, err := tload.ComposedInstance("api", ps...)
	if err != nil {
		t.Fatal(err)
	}
	whole := ctx.BuildInstance(inst)

	for op, dir := range dirs {
		lin := whole.LookupPath(cue.MakePath(cue.Str(op)))
		a := lin.LookupPath(cue.ParsePath(`schemas."0"."0".schema.a`))
		if got, want := a.Pos().Filename(), dir+"/history/0.0.thema"; got != want {
			t.Errorf("%s: 0.0's a is positioned in %s, want %s", op, got, want)
		}

		_, err := thema.BindLineage(whole, cue.MakePath(cue.Str(op)), rt, thema.WithVocabulary(authoring.Vocabulary(compiled[op])))
		ds := diag.Diagnostics(err)
		if len(ds) != 1 || ds[0].Code != diag.E0367 {
			t.Fatalf("%s: want bind to refuse the forward lens (E0367), got %v", op, ds)
		}
		if d := ds[0]; d.Primary == nil || d.Primary.File != dir+"/history/1.0.thema" {
			t.Errorf("%s: %s %q is positioned at %v, want %s/history/1.0.thema", op, d.Code, d.Headline, d.Primary, dir)
		}
	}
}
