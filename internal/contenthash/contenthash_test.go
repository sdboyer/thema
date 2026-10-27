package contenthash

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema/internal/envvars"
	"github.com/sdboyer/thema/internal/util"
	tload "github.com/sdboyer/thema/load"
)

func compileLineage(t *testing.T, src string) cue.Value {
	t.Helper()
	v := cuecontext.New().CompileString(src)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	return v.LookupPath(cue.ParsePath("lin"))
}

func mustID(t *testing.T, lin cue.Value, v Version) ID {
	t.Helper()
	id, err := VersionID(lin, v)
	if err != nil {
		t.Fatalf("%s: %v", v, err)
	}
	return id
}

func mustLensForm(t *testing.T, lin cue.Value, v Version) string {
	t.Helper()
	f, err := LensForm(lin, v)
	if err != nil {
		t.Fatalf("%s: %v", v, err)
	}
	return f
}

// lineage returns a lineage with versions 0.0, 0.1 and 1.0, whose lens from 1.0
// to 0.1 has the result back.
func lineage(back string) string {
	return `lin: {
	schemas: [
		{version: [0, 0], schema: {a: string}},
		{version: [0, 1], schema: {a: string, b?: int}},
		{version: [1, 0], schema: {a: string, b: int}},
	]
	lenses: [{
		from: [0, 1], to: [0, 0], input: _
		result: {a: input.a}
	}, {
		from: [0, 1], to: [1, 0], input: _
		result: {a: input.a, b: *input.b | 0}
	}, {
		from: [1, 0], to: [0, 1], input: _
		result: ` + back + `
	}]
}`
}

// Which lenses each version's lens digest covers: the edge into it.
func TestLensFormEdge(t *testing.T) {
	lin := compileLineage(t, lineage(`{a: input.a, b: input.b}`))

	// The first version has no edge into it: its lens form is that of no
	// lenses, a constant.
	if got := mustLensForm(t, lin, Version{0, 0}); got != "{}" {
		t.Errorf("0.0: lens form %q, want {}", got)
	}
	if got, want := mustID(t, lin, Version{0, 0}).Lens, Digest(sha256.Sum256([]byte("{}"))); got != want {
		t.Errorf("0.0: lens digest %s, want sha256(\"{}\") %s", got, want)
	}

	// A minor version's forward lens is implicit: only the backward one is
	// hashed.
	minor := mustLensForm(t, lin, Version{0, 1})
	if strings.Contains(minor, "forward") || !strings.Contains(minor, "backward") {
		t.Errorf("0.1: want only a backward lens:\n%s", minor)
	}
	major := mustLensForm(t, lin, Version{1, 0})
	if !strings.Contains(major, "forward") || !strings.Contains(major, "backward") {
		t.Errorf("1.0: want a forward and a backward lens:\n%s", major)
	}
	// from and to are position, not content.
	for _, f := range []string{minor, major} {
		if strings.Contains(f, "from") || strings.Contains(f, "to:") {
			t.Errorf("lens form holds from or to:\n%s", f)
		}
	}
}

// Fixing a published lens changes the lens digest of the version whose edge
// it is on, and nothing else.
func TestLensFix(t *testing.T) {
	buggy := compileLineage(t, lineage(`{a: input.a, b: input.b}`))
	fixed := compileLineage(t, lineage(`{a: input.a, if input.b > 0 {b: input.b}}`))
	for _, v := range []Version{{0, 0}, {0, 1}, {1, 0}} {
		b, f := mustID(t, buggy, v), mustID(t, fixed, v)
		if b.Schema != f.Schema {
			t.Errorf("%s: schema digest changed with a lens", v)
		}
		if changed := b.Lens != f.Lens; changed != (v == Version{1, 0}) {
			t.Errorf("%s: lens digest changed: %v", v, changed)
		}
	}
}

// A tip under development may lack the lenses its edge needs; its lens form
// holds the ones it has.
func TestLensFormMissing(t *testing.T) {
	lin := compileLineage(t, `lin: {
	schemas: [{version: [0, 0], schema: {a: string}}, {version: [1, 0], schema: {b: string}}]
	lenses: [{from: [1, 0], to: [0, 0], input: _, result: {a: input.b}}]
}`)
	got := mustLensForm(t, lin, Version{1, 0})
	if strings.Contains(got, "forward") || !strings.Contains(got, "backward") {
		t.Errorf("want only the backward lens:\n%s", got)
	}
}

// A version's identity is its content, not its position: the same schemas and
// lenses, numbered differently, have the same IDs.
func TestIDPositionIndependent(t *testing.T) {
	a := compileLineage(t, `lin: {
	schemas: [{version: [0, 0], schema: {a: string}}, {version: [1, 0], schema: {b: string}}]
	lenses: [
		{from: [0, 0], to: [1, 0], input: _, result: {b: input.a}},
		{from: [1, 0], to: [0, 0], input: _, result: {a: input.b}},
	]
}`)
	b := compileLineage(t, `lin: {
	schemas: [
		{version: [0, 0], schema: {z: string}},
		{version: [1, 0], schema: {a: string}},
		{version: [2, 0], schema: {b: string}},
	]
	lenses: [
		{from: [0, 0], to: [1, 0], input: _, result: {a: input.z}},
		{from: [1, 0], to: [0, 0], input: _, result: {z: input.a}},
		{from: [1, 0], to: [2, 0], input: _, result: {b: input.a}},
		{from: [2, 0], to: [1, 0], input: _, result: {a: input.b}},
	]
}`)
	if ida, idb := mustID(t, a, Version{1, 0}), mustID(t, b, Version{2, 0}); ida != idb {
		t.Errorf("a 1.0 %s != b 2.0 %s", ida, idb)
	}
}

// What the canonical form of a lens makes equal, and doesn't.
func TestLensFormConstruction(t *testing.T) {
	form := func(lens string) string {
		t.Helper()
		lin := compileLineage(t, `lin: {
	schemas: [{version: [0, 0], schema: {a: string, b: int}}, {version: [0, 1], schema: {a: string, b: int, c?: int}}]
	lenses: [{from: [0, 1], to: [0, 0], input: _, `+lens+`}]
}`)
		return mustLensForm(t, lin, Version{0, 1})
	}
	base := form(`result: {a: input.a, b: input.b}`)
	for name, c := range map[string]struct {
		lens  string
		equal bool
		why   string
	}{
		"field order": {`result: {b: input.b, a: input.a}`, true,
			"as for schemas"},
		"let beside result": {`let A = input.a, result: {a: A, b: input.b}`, true,
			"each field of a lens is exported on its own, so a let outside it is inlined by CUE's export"},
		"alias of input": {`input: _, let I = input, result: {a: I.a, b: input.b}`, true,
			"as for a let beside result"},
		"let inside result": {`result: {let A = input.a, a: A, b: input.b}`, false,
			"lets within a value are kept: see the let vs inline case for schemas"},
		"empty lacunas": {`result: {a: input.a, b: input.b}, lacunas: []`, false,
			"lacunas: [] is written, and lacunas absent is not; thema's kernel fills it in only when the lineage is unified with thema.Lineage, which a hashed lineage isn't"},
	} {
		t.Run(name, func(t *testing.T) {
			got := form(c.lens)
			if (got == base) != c.equal {
				t.Errorf("want equal=%v (%s):\n%s\n---\n%s", c.equal, c.why, got, base)
			}
		})
	}
}

// TestGoldens records the canonical forms and IDs of every version of the
// lineages in testdata/*.txtar. Each archive holds a CUE module's files (its
// cue.mod/module.cue is supplied), whose package p declares the lineage as lin;
// each out/<version> section holds that version's ID, schema form and lens
// form.
//
// The digests are a stored contract: an ID computed today must be computed the
// same way for as long as instances recorded against it exist. A diff here,
// including one from a CUE upgrade alone, changes every stored ID it touches;
// see the contenthash seam. Set THEMA_UPDATE_GOLDEN=1 to rewrite the out/
// sections, then review the diff.
func TestGoldens(t *testing.T) {
	files, err := filepath.Glob("testdata/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no golden files")
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".txtar"), func(t *testing.T) {
			ar, err := txtar.ParseFile(file)
			if err != nil {
				t.Fatal(err)
			}
			fs := fstest.MapFS{"cue.mod/module.cue": {Data: []byte(util.ModuleFile("example.com/p"))}}
			for _, f := range ar.Files {
				if !strings.HasPrefix(f.Name, "out/") {
					fs[f.Name] = &fstest.MapFile{Data: f.Data}
				}
			}
			insts, err := tload.InstancesWithThema(fs, ".")
			if err != nil {
				t.Fatal(err)
			}
			v := cuecontext.New().BuildInstance(insts)
			if verr := v.Err(); verr != nil {
				t.Fatal(verr)
			}
			lin := v.LookupPath(cue.ParsePath("lin"))

			got := map[string][]byte{}
			var order []string
			iter, err := lin.LookupPath(pathSchemas).List()
			if err != nil {
				t.Fatal(err)
			}
			for iter.Next() {
				ver, err := schemaVersion(iter.Value())
				if err != nil {
					t.Fatal(err)
				}
				id := mustID(t, lin, ver)
				sch, err := SchemaForm(lin, ver)
				if err != nil {
					t.Fatal(err)
				}
				name := "out/" + ver.String()
				order = append(order, name)
				got[name] = fmt.Appendf(nil, "# id\n%s\n# schema\n%s\n# lenses\n%s\n", id, sch, mustLensForm(t, lin, ver))
			}

			if envvars.UpdateGoldenFiles {
				var kept []txtar.File
				for _, f := range ar.Files {
					if !strings.HasPrefix(f.Name, "out/") {
						kept = append(kept, f)
					}
				}
				for _, name := range order {
					kept = append(kept, txtar.File{Name: name, Data: got[name]})
				}
				ar.Files = kept
				if err := os.WriteFile(file, txtar.Format(ar), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			for _, f := range ar.Files {
				if !strings.HasPrefix(f.Name, "out/") {
					continue
				}
				want, ok := got[f.Name]
				if !ok {
					t.Errorf("golden section %s has no version", f.Name)
					continue
				}
				if diff := cmp.Diff(string(f.Data), string(want)); diff != "" {
					t.Errorf("%s (-golden +got):\n%s", f.Name, diff)
				}
				delete(got, f.Name)
			}
			for name := range got {
				t.Errorf("missing golden section %s; run with %s=1", name, envvars.VarUpdateGolden)
			}
		})
	}
}
