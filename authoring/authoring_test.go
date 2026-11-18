package authoring_test

import (
	"bytes"
	"errors"
	"maps"
	"path"
	"slices"
	"strings"
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

ForwardLens: (prev): {
	self: {
		after: prev.before
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
	written := compileDev("query", devTip+"\nForwardLens: (prev): {\n\tself: {\n\t\tb: _drop\n\t}\n}\n\nBackwardLens: (self): {\n\tprev: {\n\t\tb: 0\n\t}\n}\n")
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

ForwardLens: (prev): {
	self: {
		a: prev.a * 2
	}
}

BackwardLens: (self): {
	prev: {
		a: self.a
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

// v00File and v10File are where 0.0 and 1.0 are in a lineage, by File.Path.
var (
	v00File = path.Join(authoring.HistoryDir, "0.0.thema")
	v10File = path.Join(authoring.HistoryDir, "1.0.thema")
)

// A lineage whose history is kept in a tree of its own, HistoryAt's: loaded
// from there, compiled with its versions positioned there, and cut to
// history/ as File.Path, for the caller to write there.

func TestHistoryAt(t *testing.T) {
	const dir, hist = "ship", ".history/ship"
	split := fstest.MapFS{
		hist + "/" + v00File: {Data: []byte(v00)},
		hist + "/" + v10File: {Data: []byte(v10)},
		dir + "/tip.thema":   {Data: []byte(tip)},
	}
	opts := []authoring.Option{authoring.At(dir), authoring.HistoryAt(hist)}
	ctx := cuecontext.New()

	t.Run("load and compile", func(t *testing.T) {
		name, files, err := authoring.Load(split, dir, opts...)
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		for _, f := range files {
			paths = append(paths, f.Path)
		}
		if want := []string{authoring.TipFile, v00File, v10File}; name != dir || !slices.Equal(paths, want) {
			t.Fatalf("Load: %q %v, want %q %v", name, paths, dir, want)
		}
		c, err := authoring.Compile(ctx, name, files, opts...)
		if err != nil {
			t.Fatal(cerrors.Details(err, nil))
		}
		v := ctx.BuildFile(c.File)
		if got, want := v.LookupPath(cue.ParsePath(`schemas."0"."0".schema.before`)).Pos().Filename(), hist+"/history/0.0.thema"; got != want {
			t.Errorf("0.0's before is positioned in %s, want %s", got, want)
		}
		if err := authoring.CheckClean(ctx, files, opts...); err != nil {
			t.Errorf("the tip is clean: %v", diag.Diagnostics(err))
		}
	})

	t.Run("never cut", func(t *testing.T) {
		_, files, err := authoring.Load(fstest.MapFS{dir + "/tip.thema": {Data: []byte(tip)}}, dir, opts...)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 1 || files[0].Path != authoring.TipFile {
			t.Errorf("Load: %v, want the tip alone", files)
		}
	})

	t.Run("cut", func(t *testing.T) {
		fsys := fstest.MapFS{
			hist + "/" + v00File: {Data: []byte(v00)},
			dir + "/tip.thema":   {Data: []byte(renameTip)},
		}
		_, files, err := authoring.Load(fsys, dir, opts...)
		if err != nil {
			t.Fatal(err)
		}
		cut, err := authoring.CutTip(thema.NewRuntime(ctx), dir, files, opts...)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range cut.Written {
			got = append(got, f.Path)
		}
		if !slices.Equal(got, []string{v10File, authoring.TipFile}) {
			t.Errorf("wrote %v", got)
		}
	})

	t.Run("the lineage's own directory", func(t *testing.T) {
		local := fstest.MapFS{
			dir + "/history/0.0.thema": {Data: []byte(v00)},
			dir + "/tip.thema":         {Data: []byte(v00)},
		}
		ownOpts := []authoring.Option{authoring.At(dir), authoring.HistoryAt(dir)}
		name, files, err := authoring.Load(local, dir, ownOpts...)
		if err != nil || len(files) != 2 {
			t.Fatalf("HistoryAt(%q): %d files, %v", dir, len(files), diag.Diagnostics(err))
		}
		c, err := authoring.Compile(ctx, name, files, ownOpts...)
		if err != nil {
			t.Fatal(cerrors.Details(err, nil))
		}
		v := ctx.BuildFile(c.File)
		if got, want := v.LookupPath(cue.ParsePath(`schemas."0"."0".schema.before`)).Pos().Filename(), dir+"/"+v00File; got != want {
			t.Errorf("0.0's before is positioned in %s, want %s", got, want)
		}
	})

	t.Run("two histories", func(t *testing.T) {
		both := fstest.MapFS{dir + "/history/0.0.thema": {Data: []byte(v00)}}
		maps.Copy(both, split)
		_, _, err := authoring.Load(both, dir, opts...)
		ds := diag.Diagnostics(err)
		if len(ds) != 1 || ds[0].Code != diag.E0629 {
			t.Fatalf("want E0629, got %v", ds)
		}
		if !strings.Contains(ds[0].Headline, dir+"/history") || !strings.Contains(ds[0].Headline, hist+"/history") {
			t.Errorf("%q names both histories", ds[0].Headline)
		}
	})

	t.Run("a history/ with no versions", func(t *testing.T) {
		// Only version files make a history: the directory a tool left
		// behind, or one holding another file, is not one.
		fsys := fstest.MapFS{dir + "/history/README": {Data: []byte("moved\n")}}
		maps.Copy(fsys, split)
		if _, files, err := authoring.Load(fsys, dir, opts...); err != nil || len(files) != 3 {
			t.Errorf("Load: %d files, %v", len(files), err)
		}
	})

	t.Run("errors", func(t *testing.T) {
		for _, c := range []struct {
			name string
			dir  string
			opt  authoring.Option
			want diag.Code
		}{
			{"invalid HistoryAt", dir, authoring.HistoryAt("../ship"), diag.E0625},
			{"no lineage directory", "boat", authoring.HistoryAt(hist), diag.E0601},
			{"invalid At", dir, authoring.At("../ship"), diag.E0625},
		} {
			_, _, err := authoring.Load(split, c.dir, c.opt)
			if got := diag.Diagnostics(err); len(got) != 1 || got[0].Code != c.want {
				t.Errorf("%s: %v, want %s", c.name, got, c.want)
			}
		}

		// A lineage directory that is a file is reported as itself, before
		// its own history/ is looked for.
		file := fstest.MapFS{"boat": {Data: []byte(v00)}}
		maps.Copy(file, split)
		_, _, err := authoring.Load(file, "boat", authoring.HistoryAt(hist))
		if got := diag.Diagnostics(err); len(got) != 1 || got[0].Code != diag.E0601 || got[0].Headline != "boat cannot be read" {
			t.Errorf("a file for the lineage's directory: %v, want E0601 naming boat", got)
		}
	})
}
