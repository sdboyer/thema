package contenthash

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// versions lists the versions of lin's schemas, in key order: by major, then
// minor.
func versions(t *testing.T, lin cue.Value) []Version {
	t.Helper()
	var out []Version
	majors, err := lin.LookupPath(pathSchemas).Fields()
	if err != nil {
		t.Fatal(err)
	}
	for majors.Next() {
		minors, err := majors.Value().Fields()
		if err != nil {
			t.Fatal(err)
		}
		for minors.Next() {
			var v Version
			for i, s := range []string{majors.Selector().Unquoted(), minors.Selector().Unquoted()} {
				n, err := strconv.ParseUint(s, 10, 64)
				if err != nil {
					t.Fatalf("schema key %q: %v", s, err)
				}
				v[i] = uint(n)
			}
			out = append(out, v)
		}
	}
	return out
}

// lineage returns a lineage with versions 0.0, 0.1 and 1.0, whose lens from 1.0
// to 0.1 has the result back.
func lineage(back string) string {
	return `lin: {
	schemas: {
		"0": {
			"0": {schema: {a: string}}
			"1": {schema: {a: string, b?: int}}
		}
		"1": {"0": {schema: {a: string, b: int}}}
	}
	lenses: {
		"0.1->0.0": {
			v0_1: _
			v0_0: {a: v0_1.a}
		}
		"0.1->1.0": {
			v0_1: _
			v1_0: {a: v0_1.a, b: *v0_1.b | 0}
		}
		"1.0->0.1": {
			v1_0: _
			v0_1: ` + back + `
		}
	}
}`
}

// Which lenses each version's lens digest covers: the edge into it.
func TestLensFormEdge(t *testing.T) {
	lin := compileLineage(t, lineage(`{a: v1_0.a, b: v1_0.b}`))

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
	buggy := compileLineage(t, lineage(`{a: v1_0.a, b: v1_0.b}`))
	fixed := compileLineage(t, lineage(`{a: v1_0.a, if v1_0.b > 0 {b: v1_0.b}}`))
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
	schemas: {"0": {"0": {schema: {a: string}}}, "1": {"0": {schema: {b: string}}}}
	lenses: {"1.0->0.0": {v1_0: _, v0_0: {a: v1_0.b}}}
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
	schemas: {"0": {"0": {schema: {a: string}}}, "1": {"0": {schema: {b: string}}}}
	lenses: {
		"0.0->1.0": {v0_0: _, v1_0: {b: v0_0.a}}
		"1.0->0.0": {v1_0: _, v0_0: {a: v1_0.b}}
	}
}`)
	b := compileLineage(t, `lin: {
	schemas: {
		"0": {"0": {schema: {z: string}}}
		"1": {"0": {schema: {a: string}}}
		"2": {"0": {schema: {b: string}}}
	}
	lenses: {
		"0.0->1.0": {v0_0: _, v1_0: {a: v0_0.z}}
		"1.0->0.0": {v1_0: _, v0_0: {z: v1_0.a}}
		"1.0->2.0": {v1_0: _, v2_0: {b: v1_0.a}}
		"2.0->1.0": {v2_0: _, v1_0: {a: v2_0.b}}
	}
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
	schemas: {"0": {"0": {schema: {a: string, b: int}}, "1": {schema: {a: string, b: int, c?: int}}}}
	lenses: {"0.1->0.0": {v0_1: _, `+lens+`}}
}`)
		return mustLensForm(t, lin, Version{0, 1})
	}
	base := form(`v0_0: {a: v0_1.a, b: v0_1.b}`)
	for name, c := range map[string]struct {
		lens  string
		equal bool
		why   string
	}{
		"field order": {`v0_0: {b: v0_1.b, a: v0_1.a}`, true,
			"as for schemas"},
		"let beside result": {`let A = v0_1.a, v0_0: {a: A, b: v0_1.b}`, true,
			"each field of a lens is exported on its own, so a let outside it is inlined by CUE's export"},
		"alias of input": {`v0_1: _, let I = v0_1, v0_0: {a: I.a, b: v0_1.b}`, true,
			"as for a let beside result"},
		"let inside result": {`v0_0: {let A = v0_1.a, a: A, b: v0_1.b}`, false,
			"lets within a value are kept: see the let vs inline case for schemas"},
		"empty lacunas": {`v0_0: {a: v0_1.a, b: v0_1.b}, lacunas: []`, false,
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
			for _, ver := range versions(t, lin) {
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

// What thema contributes to a lineage is not hashed: a lineage has the same IDs
// whether its author unified thema.Lineage in, embedded it in the lineage's
// package, re-exported a lineage that does, or left it out. Each case is the
// same lineage, declared in package p of a module or in its package ship and
// re-exported from p.
func TestVersionIDAuthored(t *testing.T) {
	const body = `name: "ship"
schemas: {
	"0": "0": schema: {a: string, n: int & <=10}
	"1": "0": schema: {b: string}
}
lenses: {
	"0.0->1.0": {
		v0_0: _
		v1_0: b: v0_0.a
		lacunas: [thema.Lacuna & {
			sourceFields: [{path: "n", value: v0_0.n}]
			message: "n is dropped"
			type:    thema.LacunaTypes.DroppedField
		}]
	}
	"1.0->0.0": {
		v1_0: _
		v0_0: {a: v1_0.b, n: 0}
	}
}
`
	const imp = "import \"github.com/sdboyer/thema\"\n\n"
	const in = "in.cue"
	ship := "package ship\n\n" + imp + body
	load := func(t *testing.T, files map[string]string, path string) cue.Value {
		t.Helper()
		fs := fstest.MapFS{"cue.mod/module.cue": {Data: []byte(util.ModuleFile("example.com/p"))}}
		for n, text := range files {
			fs[n] = &fstest.MapFile{Data: []byte(text)}
		}
		insts, err := tload.InstancesWithThema(fs, ".")
		if err != nil {
			t.Fatal(err)
		}
		v := cuecontext.New().BuildInstance(insts)
		if err := v.Err(); err != nil {
			t.Fatal(err)
		}
		return v.LookupPath(cue.ParsePath(path))
	}
	plain := load(t, map[string]string{in: "package p\n\n" + imp + "lin: {\n" + body + "}\n"}, "lin")
	for name, c := range map[string]struct {
		files map[string]string
		path  string
	}{
		"unified":  {files: map[string]string{in: "package p\n\n" + imp + "lin: thema.Lineage & {\n" + body + "}\n"}, path: "lin"},
		"embedded": {files: map[string]string{in: "package p\n\n" + imp + "thema.Lineage\n\n" + body}, path: ""},
		"reexported": {files: map[string]string{
			"ship/ship.cue":  ship,
			"ship/embed.cue": "package ship\n\n" + imp + "thema.Lineage\n",
			in:               "package p\n\nimport \"example.com/p/ship\"\n\nflagship: ship\n",
		}, path: "flagship"},
		"reexported-unified": {files: map[string]string{
			"ship/ship.cue": ship,
			in:              "package p\n\n" + imp + "import \"example.com/p/ship\"\n\nflagship: thema.Lineage & ship\n",
		}, path: "flagship"},
	} {
		t.Run(name, func(t *testing.T) {
			lin := load(t, c.files, c.path)
			for _, ver := range []Version{{0, 0}, {1, 0}} {
				if got, want := mustID(t, lin, ver), mustID(t, plain, ver); got != want {
					t.Errorf("%s: ID %s, want %s, the ID of the lineage with nothing of thema's in it", ver, got, want)
				}
			}
		})
	}
}
