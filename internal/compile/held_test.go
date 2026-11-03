package compile

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/util"
)

// heldSrc is package p, which holds the same lineage in each of the ways a
// framework holds lineages in definitions, and writes closedness of its own.
const heldSrc = `package p

import "github.com/sdboyer/thema"

// As #256 found it: a pattern unifies every use with a definition.
#Op: {query?: thema.Lineage}
ops: [string]: #Op
ops: get: query: {name: "q", schemas: "0": "0": schema: id: string}

// Closed by definitions at several levels, and in a list.
#A: {b: #B}
#B: {c: [...#C]}
#C: {lin?: thema.Lineage}
nested: #A & {b: c: [{lin: {name: "q", schemas: "0": "0": schema: id: string}}]}

// A field of a definition.
#Defs: q: thema.Lineage & {name: "q", schemas: "0": "0": schema: id: string}

// Closedness the author writes in the schema: close(), a definition the
// schema declares, and one a struct embeds.
_own: {
	name: "q"
	schemas: "0": "0": schema: {
		id: string
		c:  close({x: int})
		#D: {y: int}
		d:  #D
		e: {#E, #E: {z: int}}
	}
}
own: _own
heldOwn: [string]: #Op
heldOwn: get: query: _own

// A definition the lineage declares, beneath it: closedness of the author's
// that a version file cannot write.
_entry: {
	name:   "q"
	#Entry: schema: id: string
	schemas: "0": "0": #Entry
}
entry: thema.Lineage & _entry
heldEntry: [string]: #Op
heldEntry: get: query: _entry
`

// ownSchema is how the version files write _own's schema.
const ownSchema = `Schema: {
	id: string
	c:  close({x: int})
	#D: {
		y: int
	}
	d: #D
	e: {
		#E
		#E: {
			z: int
		}
	}
}
`

// TestDecompileHeldInDefinition checks that a lineage held in a definition
// decompiles as its author wrote it (#256): CUE closes the lineage, and every
// schema in it, where it is held, and writes a closed schema wrapped as
// {_#def, _#def: ...}, which is neither the author's nor something a version
// file may declare (E0151). Closedness the author wrote stays, and closedness
// the author wrote that a version file cannot (a definition beneath the
// lineage, which closes an entry) is refused, held or not. Every decompiled
// lineage compiles, binds, and decompiles the same again.
func TestDecompileHeldInDefinition(t *testing.T) {
	ctx := cuecontext.New()
	root := ctx.BuildInstance(heldModule(t, heldSrc))
	if err := root.Err(); err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	idSchema := "Schema: {\n\tid: string\n}\n"
	cases := []struct {
		path, schema, refused string
	}{
		{path: "ops.get.query", schema: idSchema},
		{path: "nested.b.c[0].lin", schema: idSchema},
		{path: "#Defs.q", schema: idSchema},
		{path: "own", schema: ownSchema},
		{path: "heldOwn.get.query", schema: ownSchema},
		{path: "entry", refused: "closed as a definition is"},
		{path: "heldEntry.get.query", refused: "closed as a definition is"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			lin := root.LookupPath(cue.ParsePath(c.path))
			if !lin.Exists() {
				t.Fatalf("no lineage at %s", c.path)
			}
			name, files, err := Decompile(lin)
			if c.refused != "" {
				if got := codes(err); len(got) != 1 || got[0] != "E0356" || !strings.Contains(err.Error(), c.refused) {
					t.Fatalf("Decompile: got %v (%v), want E0356 saying %q", err, got, c.refused)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decompile: %v", err)
			}
			for _, f := range files {
				if string(f.Text) != c.schema {
					t.Errorf("%s decompiled as\n%s\nwant\n%s", f.Path, f.Text, c.schema)
				}
			}

			comp, err := Compile(ctx, name, files)
			if err != nil {
				t.Fatalf("compiling the decompiled files: %v", cerrors.Details(err, nil))
			}
			v := ctx.BuildFile(comp.File)
			if _, berr := thema.BindLineage(v, thema.NewRuntime(ctx)); berr != nil {
				t.Fatalf("binding the decompiled files: %v", cerrors.Details(berr, nil))
			}
			_, again, err := Decompile(v)
			if err != nil {
				t.Fatalf("decompiling the compiled files: %v", err)
			}
			for i, f := range again {
				if string(f.Text) != string(files[i].Text) {
					t.Errorf("%s decompiled again as\n%s\nfirst\n%s", f.Path, f.Text, files[i].Text)
				}
			}
		})
	}
}

// heldModule loads package p, the root of the CUE module example.com/p, from
// src, serving thema's CUE module from the copy embedded in thema, as thema's
// load package does: load imports this package.
func heldModule(t *testing.T, src string) *build.Instance {
	t.Helper()
	fsys := fstest.MapFS{"cue.mod/module.cue": {Data: []byte(util.ModuleFile("example.com/p"))}, "p.cue": {Data: []byte(src)}}
	root := filepath.Join(util.Prefix, "example.com", "p")
	overlay := make(map[string]load.Source)
	if err := util.ToOverlay(root, fsys, overlay); err != nil {
		t.Fatal(err)
	}
	cfg := &load.Config{Overlay: overlay, ModuleRoot: root, Module: "example.com/p", Dir: root, Package: "p"}
	if err := embedreg.Configure(cfg, thema.CueJointFS); err != nil {
		t.Fatal(err)
	}
	inst := load.Instances(nil, cfg)[0]
	if inst.Err != nil {
		t.Fatal(inst.Err)
	}
	return inst
}
