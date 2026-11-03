package compile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/parser"

	"github.com/google/go-cmp/cmp"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/diagtest"
)

// TestRoundTrip compiles each archive in testdata/roundtrip, binds the result
// and decompiles it again. The archive holds a lineage's authoring files under
// its name (ship/1.0.thema, ship/history/0.0.thema) and, as goldens:
//
//   - out/normal.cue: the compiled normal form;
//   - out/bind: "ok", or BindLineage's error;
//   - out/decompiled/<path>: the authoring files decompiled from the normal
//     form, which must equal the input up to formatting;
//   - out/error: the compile error, for an archive that must not compile; the
//     other sections are then absent;
//   - out/translate/<version>/<name>-><to>: for each instance data/<version>/<name>
//     the archive holds, as JSON, the result of translating it to every other
//     version and the lacunas reported, as JSON on a line each.
func TestRoundTrip(t *testing.T) {
	files, err := filepath.Glob("testdata/roundtrip/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".txtar"), func(t *testing.T) {
			a := readArchive(t, file)
			name, inputs := a.lineage(t)
			got := make(map[string]string)

			ctx := cuecontext.New()
			c, err := Compile(ctx, name, inputs)
			if err != nil {
				ds := diag.Diagnostics(err)
				if len(ds) == 0 {
					t.Fatalf("a compile failure is made of diagnostics, got: %v", err)
				}
				diagtest.Lint(t, ds, diagtest.Authoring)
				got["out/error"] = rendered(ds, a)
				a.compare(t, got)
				return
			}
			text, terr := c.Text()
			if terr != nil {
				t.Fatal(terr)
			}
			got["out/normal.cue"] = text

			v := ctx.BuildFile(c.File)
			if v.Err() != nil {
				t.Fatalf("building the normal form: %v", cerrors.Details(v.Err(), nil))
			}
			// Bound with the authoring form's vocabulary, so that what bind
			// reports names the author's files and lenses.
			lin, berr := thema.BindLineage(v, thema.NewRuntime(ctx), thema.WithVocabulary(Vocabulary(c)))
			if berr != nil {
				ds := diag.Diagnostics(berr)
				if len(ds) == 0 {
					t.Fatalf("a bind failure is made of diagnostics, got: %v", berr)
				}
				diagtest.Lint(t, ds, diagtest.Authoring)
				got["out/bind"] = rendered(ds, a)
			} else {
				got["out/bind"] = "ok\n"
				a.translate(t, ctx, lin, got)
			}

			dname, dfiles, err := Decompile(v)
			if err != nil {
				t.Fatalf("decompiling: %v", err)
			}
			if dname != name {
				t.Errorf("decompiled name %q, want %q", dname, name)
			}
			want := make(map[string]string)
			for _, f := range inputs {
				want[f.Path] = formatted(t, f.Path, f.Text)
			}
			for _, f := range dfiles {
				got["out/decompiled/"+f.Path] = string(f.Text)
				if w, ok := want[f.Path]; !ok {
					t.Errorf("decompiled %s, which was not an input", f.Path)
				} else if diff := cmp.Diff(w, string(f.Text)); diff != "" {
					t.Errorf("decompiled %s differs from the formatted input (-input +decompiled):\n%s", f.Path, diff)
				}
				delete(want, f.Path)
			}
			for p := range want {
				t.Errorf("input %s was not decompiled", p)
			}

			// Held in a definition, the lineage and everything in it is
			// recursively closed, which is where it is held, not what its
			// author wrote: it decompiles the same (#256).
			hname, hfiles, err := Decompile(heldInDefinition(t, ctx, text))
			if err != nil {
				t.Fatalf("decompiling the lineage held in a definition: %v", err)
			}
			if hname != dname || !cmp.Equal(hfiles, dfiles) {
				t.Errorf("held in a definition, the lineage decompiles otherwise (-plain +held):\n%s", cmp.Diff(dfiles, hfiles))
			}

			// Compiling the decompiled form gives the same normal form.
			c2, err := Compile(ctx, dname, dfiles)
			if err != nil {
				t.Fatalf("recompiling the decompiled form: %v", cerrors.Details(err, nil))
			}
			text2, err := c2.Text()
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(formatted(t, "normal.cue", []byte(text)), formatted(t, "normal.cue", []byte(text2))); diff != "" {
				t.Errorf("recompiled normal form differs (-first +recompiled):\n%s", diff)
			}
			a.compare(t, got)
		})
	}
}

// heldInDefinition returns the lineage in text, the normal form, held as a
// framework holds lineages to treat them alike: in a field of a definition
// that a pattern unifies with each use (#256),
//
//	#Op: query?: {name: string, schemas: ..., lenses: ...}
//	ops: [string]: #Op
//	ops: get: query: <lineage>
//
// so that the lineage, and each schema and lens in it, is recursively closed.
// thema.Lineage is not unified in, which Decompile does not yet see past
// (#224): the definition declares the fields it holds.
func heldInDefinition(t *testing.T, ctx *cue.Context, text string) cue.Value {
	t.Helper()
	f, err := parser.ParseFile("normal.cue", text, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var imports, decls []ast.Decl
	for _, d := range f.Decls {
		if _, ok := d.(*ast.ImportDecl); ok {
			imports = append(imports, d)
		} else {
			decls = append(decls, d)
		}
	}
	holder, err := parser.ParseFile("holder.cue", `
#Op: query?: {
	name:        string
	joinSchema?: _
	schemas: [string]: [string]: schema: _
	lenses: [string]: _
}
ops: [string]: #Op
`)
	if err != nil {
		t.Fatal(err)
	}
	use := &ast.Field{
		Label: ast.NewIdent("ops"),
		Value: ast.NewStruct("get", ast.NewStruct("query", &ast.StructLit{Elts: decls})),
	}
	held := ctx.BuildFile(&ast.File{Decls: slices.Concat(imports, holder.Decls, []ast.Decl{use})})
	if err := held.Err(); err != nil {
		t.Fatalf("building the lineage held in a definition: %v", cerrors.Details(err, nil))
	}
	lin := held.LookupPath(cue.ParsePath("ops.get.query"))
	if !lin.LookupPath(cue.ParsePath(`schemas."0"."0".schema`)).IsClosedRecursively() {
		t.Fatal("held in a definition, the lineage's first schema is not recursively closed")
	}
	return lin
}

// rendered is the diagnostics as the author sees them, with the archive's
// authoring files as the source of excerpts. Positions name the files by their
// path under the lineage's directory.
func rendered(ds []diag.Diagnostic, a *archive) string {
	var b bytes.Buffer
	diag.Render(&b, ds, diag.RenderOptions{Source: func(file string) ([]byte, bool) {
		for _, f := range a.ar.Files {
			if f.Name == file || strings.HasSuffix(f.Name, "/"+file) {
				return f.Data, true
			}
		}
		return nil, false
	}})
	return b.String()
}

// formatted returns text in thema's layout, which is what Decompile emits.
func formatted(t *testing.T, name string, text []byte) string {
	t.Helper()
	b, err := Format(text)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(b)
}

// translate translates each instance the archive holds under data/ from its
// version to every other version of lin, recording the result and lacunas.
func (a *archive) translate(t *testing.T, ctx *cue.Context, lin thema.Lineage, got map[string]string) {
	t.Helper()
	for _, f := range a.ar.Files {
		rest, ok := strings.CutPrefix(f.Name, "data/")
		if !ok {
			continue
		}
		vs, name, ok := strings.Cut(rest, "/")
		if !ok {
			t.Fatalf("%s: data file %s is not under data/<version>/", a.path, f.Name)
		}
		from, err := thema.ParseSyntacticVersion(vs)
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		sch, err := lin.Schema(from)
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		inst, err := sch.Validate(ctx.CompileBytes(f.Data, cue.Filename(f.Name)))
		if err != nil {
			t.Fatalf("%s is not an instance of %s: %v", f.Name, from, cerrors.Details(err, nil))
		}
		for to := lin.First(); to != nil; to = to.Successor() {
			if to.Version() == from {
				continue
			}
			key := fmt.Sprintf("out/translate/%s/%s->%s", vs, strings.TrimSuffix(name, ".json"), to.Version())
			out, lac, err := inst.Translate(to.Version())
			if err != nil {
				got[key] = "error: " + cerrors.Details(err, nil) + "\n"
				continue
			}
			res, err := json.Marshal(out.Underlying())
			if err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			lacs, err := json.Marshal(lac.AsList())
			if err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			got[key] = string(res) + "\n" + string(lacs) + "\n"
		}
	}
}

// lineage returns the archive's authoring files: those under its first
// directory, which names the lineage.
func (a *archive) lineage(t *testing.T) (string, []File) {
	t.Helper()
	var name string
	var files []File
	for _, f := range a.ar.Files {
		if strings.HasPrefix(f.Name, "out/") || strings.HasPrefix(f.Name, "data/") {
			continue
		}
		dir, rest, ok := strings.Cut(f.Name, "/")
		if !ok {
			t.Fatalf("%s: file %s is not under the lineage's directory", a.path, f.Name)
		}
		if name == "" {
			name = dir
		} else if dir != name {
			t.Fatalf("%s: files under %s and %s; an archive holds one lineage", a.path, name, dir)
		}
		files = append(files, File{Path: rest, Text: f.Data})
	}
	return name, files
}

// TestDisjunctiveSourceWrittenCopy compiles the lens of #229's repro B as the
// issue wrote it, with route and path copied by reference from the
// disjunctive source: the same normal form as leaving them to copy
// implicitly, which is the form the disjunctive-source archive holds.
func TestDisjunctiveSourceWrittenCopy(t *testing.T) {
	a := readArchive(t, "testdata/roundtrip/disjunctive-source.txtar")
	compiled := func(lens string) string {
		t.Helper()
		files := []File{
			{Path: "history/0.0.thema", Text: []byte(a.file(t, "ship/history/0.0.thema"))},
			{Path: "history/1.0.thema", Text: []byte(`Schema: {method!: "GET", route!: "/things/{id}", path!: {id!: string}}

ForwardLens: {v0_0: _, v1_0: ` + lens + `}
`)},
		}
		c, err := Compile(cuecontext.New(), "ship", files)
		if err != nil {
			t.Fatalf("compiling %s: %v", lens, cerrors.Details(err, nil))
		}
		text, err := c.Text()
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	written := compiled(`{method: "GET", route: v0_0.route, path: v0_0.path}`)
	if diff := cmp.Diff(compiled(`{method: "GET"}`), written); diff != "" {
		t.Errorf("the written copies compile differently from the implicit ones (-implicit +written):\n%s", diff)
	}
}

// TestDecompileEachIntoClosedList pins that Decompile refuses an each into a
// list with no open element, which Compile never produces: the element is
// what Decompile reads the each body's copies against.
func TestDecompileEachIntoClosedList(t *testing.T) {
	ctx := cuecontext.New()
	v := ctx.CompileString(`
name: "ship"
schemas: {
	"0": "0": schema: {l: [...{id: string}]}
	"1": "0": schema: {l: [{id: string}]}
}
lenses: {
	"0.0->1.0": {v0_0: _, v1_0: {l: [for _item in v0_0.l {id: _item.id}]}}
	"1.0->0.0": {v1_0: _, v0_0: {l: [for _item in v1_0.l {id: _item.id}]}}
}
`)
	_, _, err := Decompile(v)
	if err == nil || !strings.Contains(err.Error(), "l is mapped from a list and is not an open list") {
		t.Errorf("Decompile: got %v, want the each into l refused", err)
	}
}
