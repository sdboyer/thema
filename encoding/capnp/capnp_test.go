package capnp

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/google/go-cmp/cmp"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/encoding/capnp/compiled"
	"github.com/sdboyer/thema/exemplars"
	"github.com/sdboyer/thema/internal/envvars"
	"github.com/sdboyer/thema/internal/txtartest/bindlin"
	"github.com/sdboyer/thema/internal/txtartest/vanilla"
)

// haveTool reports whether the capnp tool is on PATH; without it the layout
// is not computed and its goldens are not checked.
var haveTool = HaveTool("capnp")

func compileOpts() []Option {
	if haveTool {
		return nil
	}
	return []Option{WithoutLayout()}
}

// render writes what the goldens record of a compilation: the .capnp text,
// or the rejection's verdict and findings; and, where the tool ran, the
// decoded layout.
func render(t testing.TB, c *Compiled, err error, schema io.Writer, layout func() io.Writer) {
	t.Helper()
	var b strings.Builder
	if err != nil {
		var res Result
		if !asResult(err, &res) {
			t.Fatalf("compile: %v", err)
		}
		b.WriteString(res.Verdict().String() + "\n")
		for _, f := range res.Findings {
			fmt.Fprintf(&b, "%s @ %s\n", f, f.Pos)
		}
	} else {
		b.WriteString(c.Schema)
	}
	_, _ = io.WriteString(schema, b.String()) // the writer is the test's golden buffer
	if err == nil && layout != nil && c.Layout != nil {
		desc, err := describeLayout(c.Layout)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(layout(), desc)
	}
}

func asResult(err error, res *Result) bool {
	r, ok := err.(Result)
	if ok {
		*res = r
	}
	return ok
}

// TestGoldens runs the corpus in testdata/*.txtar: each archive holds a
// lineage in in.cue and records in out/capnp the schema it compiles to, or
// the verdict and findings of its rejection, and in out/capnp/layout the
// layout the capnp tool computed. The archive comment says what the case
// exercises and which rule it belongs to. Set THEMA_UPDATE_GOLDEN=1 to
// rewrite the outputs, then review the diff: a moved ordinal or ID is a
// change to every artifact already compiled.
func TestGoldens(t *testing.T) {
	test := vanilla.TxTarTest{
		Root:    "./testdata",
		Name:    "capnp",
		ThemaFS: thema.CueJointFS,
	}
	rt := thema.NewRuntime(cuecontext.New())
	test.Run(t, func(tc *vanilla.Test) {
		lin, err := bindlin.BindTxtarLineage(tc, rt)
		if err != nil {
			tc.Fatalf("bind: %+v", err)
		}
		c, err := Compile(lin, compileOpts()...)
		render(tc, c, err, tc, func() io.Writer { return tc.Writer("layout") })
	})
}

// TestCorpus compiles every lineage of the repository's lineage corpus
// (testdata/lineage), recording the schema or the rejection in out/capnp.
func TestCorpus(t *testing.T) {
	test := vanilla.TxTarTest{
		Root:    "../../testdata/lineage",
		Name:    "encoding/capnp",
		ThemaFS: thema.CueJointFS,
	}
	rt := thema.NewRuntime(cuecontext.New())
	test.Run(t, func(tc *vanilla.Test) {
		lin, err := bindlin.BindTxtarLineage(tc, rt)
		if err != nil {
			tc.Fatalf("bind: %+v", err)
		}
		c, err := Compile(lin, WithoutLayout())
		render(tc, c, err, tc, nil)
	})
}

// TestExemplars compiles each exemplar, recording the schema or the
// rejection in testdata/exemplars/<name>.txt.
func TestExemplars(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	all := exemplars.All(rt)
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var b strings.Builder
			c, err := Compile(all[name], WithoutLayout())
			render(t, c, err, &b, nil)
			golden := filepath.Join("testdata", "exemplars", name+".txt")
			if envvars.UpdateGoldenFiles {
				if err = os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(golden, []byte(b.String()), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden) //nolint:gosec // the path is the exemplar's name under testdata
			if err != nil {
				t.Fatalf("%v; run with %s=1", err, envvars.VarUpdateGolden)
			}
			if diff := cmp.Diff(string(want), b.String()); diff != "" {
				t.Errorf("schema changed (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDeterministic compiles a lineage twice and expects the same schema,
// nodes and layout.
func TestDeterministic(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	lin := bindString(t, rt, stabilityBase)
	a, err := Compile(lin, compileOpts()...)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Compile(lin, compileOpts()...)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(a, b); diff != "" {
		t.Errorf("two compilations differ (-first +second):\n%s", diff)
	}
}

// TestNamespace expects the namespace to change every type ID and nothing
// else.
func TestNamespace(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	lin := bindString(t, rt, stabilityBase)
	a, err := Compile(lin, WithoutLayout())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Compile(lin, WithoutLayout(), Namespace("example.com/schemas"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Namespace != "stability" || b.Namespace != "example.com/schemas" {
		t.Errorf("namespaces %q and %q", a.Namespace, b.Namespace)
	}
	if len(a.Nodes) != len(b.Nodes) {
		t.Fatalf("%d and %d nodes", len(a.Nodes), len(b.Nodes))
	}
	for i := range a.Nodes {
		if a.Nodes[i].ID == b.Nodes[i].ID {
			t.Errorf("%s has the same ID under both namespaces", a.Nodes[i].Name)
		}
		if a.Nodes[i].Name != b.Nodes[i].Name {
			t.Errorf("node %d is %s and %s", i, a.Nodes[i].Name, b.Nodes[i].Name)
		}
	}
}

// TestStability appends versions to a lineage and expects every node of the
// shorter lineage to be in the longer one with the same ID, the same fields
// at the same ordinals with the same members, and the same enumerants: what
// an artifact compiled from the shorter lineage relies on.
func TestStability(t *testing.T) {
	rt := thema.NewRuntime(cuecontext.New())
	base, err := Compile(bindString(t, rt, stabilityBase), WithoutLayout())
	if err != nil {
		t.Fatal(err)
	}
	ext, err := Compile(bindString(t, rt, stabilityExtended), WithoutLayout())
	if err != nil {
		t.Fatal(err)
	}
	if len(ext.Nodes) <= len(base.Nodes) {
		t.Fatalf("the extended lineage has %d nodes, the base %d", len(ext.Nodes), len(base.Nodes))
	}
	for i, v := range base.Versions {
		if ext.Versions[i] != v {
			t.Errorf("version %d is %+v in the base and %+v extended", i, v, ext.Versions[i])
		}
	}
	for _, bn := range base.Nodes {
		en := ext.Node(bn.ID)
		if en == nil {
			t.Errorf("node %s @%#x is not in the extended lineage", bn.Name, bn.ID)
			continue
		}
		if en.Name != bn.Name || en.Kind != bn.Kind || en.Role != bn.Role {
			t.Errorf("node %s changed to %s", bn.Name, en.Name)
		}
		for i, bf := range bn.Fields {
			if i >= len(en.Fields) {
				t.Errorf("%s: field %s is gone", bn.Name, bf.Name)
				continue
			}
			ef := en.Fields[i]
			if ef.Name != bf.Name || ef.Label != bf.Label {
				t.Errorf("%s: field %d is %s in the base and %s extended", bn.Name, i, bf.Name, ef.Name)
				continue
			}
			if bf.Union == nil && ef.Union == nil {
				if ef.Ordinal != bf.Ordinal || !cmp.Equal(ef.Type, bf.Type) {
					t.Errorf("%s.%s: ordinal or type changed: %+v → %+v", bn.Name, bf.Name, bf, ef)
				}
				continue
			}
			// A plain field may have become optional: its value keeps its
			// ordinal as the first member.
			bm := bf.Union
			if bm == nil {
				bm = []*compiled.Member{{Name: valueMember, Role: compiled.Value, Ordinal: bf.Ordinal, Type: bf.Type}}
			}
			for j, m := range bm {
				if j >= len(ef.Union) || ef.Union[j].Name != m.Name || ef.Union[j].Ordinal != m.Ordinal || !cmp.Equal(ef.Union[j].Type, m.Type) {
					t.Errorf("%s.%s: member %s changed", bn.Name, bf.Name, m.Name)
				}
			}
		}
		for i, be := range bn.Enumerants {
			if i >= len(en.Enumerants) || en.Enumerants[i] != be {
				t.Errorf("%s: enumerant %s changed", bn.Name, be.Name)
			}
		}
	}
}

// stabilityBase is the first three versions of testdata/generations.txtar:
// two majors, a generation split in the first (a widened int), an enum and
// a nested struct. stabilityExtended is the whole archive: a minor appended
// to the last major (growing the enum, the union and the nested struct) and
// a new major.
const stabilityBase = stabilitySchemas + `
	"1": "0": schema: {
		count: int64
		color: "red" | "blue" | "green"
		kind: string | int32 | null
		inner: {a: string, b?: int8}
	}
}
lenses: {` + stabilityLenses + `
}
`

const stabilityExtended = stabilitySchemas + `
	"1": "0": schema: {
		count: int64
		color: "red" | "blue" | "green"
		kind: string | int32 | null
		inner: {a: string, b?: int8}
	}
	"1": "1": schema: {
		count: int64
		color: "red" | "blue" | "green" | "black"
		kind: string | int32 | null | bool
		inner: {a: string, b?: int8, c?: string}
		later?: int16
	}
	"2": "0": schema: {
		count: int64
		label: string
	}
}
lenses: {` + stabilityLenses + `
	"1.1->1.0": {
		v1_1: _
		v1_0: {
			count: v1_1.count
			if v1_1.color != "black" {color: v1_1.color}
			if v1_1.color == "black" {color: "red"}
			kind: null
			inner: {
				a: v1_1.inner.a
				if v1_1.inner.b != _|_ {b: v1_1.inner.b}
			}
		}
	}
	"1.1->2.0": {
		v1_1: _
		v2_0: {
			count: v1_1.count
			label: v1_1.inner.a
		}
	}
	"2.0->1.1": {
		v2_0: _
		v1_1: {
			count: v2_0.count
			color: "red"
			kind: null
			inner: a: v2_0.label
		}
	}
}
`

const stabilitySchemas = `
name: "stability"
schemas: {
	"0": "0": schema: {
		n: int32
		color: "red" | "blue"
		inner: {a: string}
		opt?: bool
	}
	"0": "1": schema: {
		n: int64
		color: "red" | "blue" | "green"
		inner: {a: string, b?: int8}
		opt?: bool
		added?: string
	}`

const stabilityLenses = `
	"0.1->0.0": {
		v0_1: _
		v0_0: {
			if (v0_1.n & int32) != _|_ {n: v0_1.n}
			if (v0_1.n & int32) == _|_ {n: 0}
			if v0_1.color != "green" {color: v0_1.color}
			if v0_1.color == "green" {color: "red"}
			inner: a: v0_1.inner.a
			if v0_1.opt != _|_ {opt: v0_1.opt}
		}
	}
	"0.1->1.0": {
		v0_1: _
		v1_0: {
			count: v0_1.n
			color: v0_1.color
			kind: null
			inner: v0_1.inner
		}
	}
	"1.0->0.1": {
		v1_0: _
		v0_1: {
			n: v1_0.count
			color: v1_0.color
			inner: v1_0.inner
		}
	}`

// bindString binds a lineage written as the fields of a lineage struct.
func bindString(t testing.TB, rt *thema.Runtime, body string) thema.Lineage {
	t.Helper()
	v := rt.Context().CompileString(body)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	lin, err := thema.BindLineage(v, rt)
	if err != nil {
		t.Fatalf("bind: %+v", err)
	}
	return lin
}

func TestNames(t *testing.T) {
	for _, tc := range []struct{ in, field, typ string }{
		{"foo", "foo", "Foo"},
		{"Bar", "bar", "Bar"},
		{"foo-bar", "fooBar", "FooBar"},
		{"foo_bar", "fooBar", "FooBar"},
		{"with Default", "withDefault", "WithDefault"},
		{"1x", "v1x", "V1x"},
		{"", "empty", "Empty"},
		{"héllo", "hLlo", "HLlo"},
		{"branch 1", "branch1", "Branch1"},
	} {
		if got := fieldName(tc.in); got != tc.field {
			t.Errorf("fieldName(%q) = %q, want %q", tc.in, got, tc.field)
		}
		if got := typeName(tc.in); got != tc.typ {
			t.Errorf("typeName(%q) = %q, want %q", tc.in, got, tc.typ)
		}
	}
	s := newScope("extras")
	for _, tc := range []struct{ in, want string }{{"extras", "extras2"}, {"a", "a"}, {"a", "a2"}, {"a", "a3"}, {"a2", "a22"}} {
		if got := s.unique(tc.in); got != tc.want {
			t.Errorf("unique(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
