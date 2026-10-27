package jsonschema

import (
	"bytes"
	stdjson "encoding/json"
	"strings"
	"testing"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	cuejsonschema "cuelang.org/go/encoding/jsonschema"
	sjsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/exemplars"
	"github.com/sdboyer/thema/internal/txtartest/bindlin"
	"github.com/sdboyer/thema/internal/txtartest/vanilla"
)

var rt = thema.NewRuntime(cuecontext.New())

// compile marshals f and compiles it as a draft 2020-12 JSON Schema, which
// checks it against the draft's metaschema.
func compile(t testing.TB, f *ast.File) (*sjsonschema.Schema, []byte) {
	t.Helper()
	b, err := rt.Context().BuildFile(f).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := sjsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	c := sjsonschema.NewCompiler()
	c.DefaultDraft(sjsonschema.Draft2020)
	if err = c.AddResource("schema.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("schema.json")
	if err != nil {
		t.Fatalf("not a valid JSON Schema: %v\n%s", err, b)
	}
	return s, b
}

func TestGenerate(t *testing.T) {
	test := vanilla.TxTarTest{
		Root:    "../../testdata/lineage",
		Name:    "encoding/jsonschema/TestGenerate",
		ThemaFS: thema.CueJointFS,
		ToDo: map[string]string{
			"lineage/defaultchange": "default backcompat invariants not working properly yet",
			"lineage/optional":      "Optional fields do not satisfy struct.MinFields(), causing Lineage constraints to fail",
		},
	}
	test.Run(t, func(tc *vanilla.Test) {
		lin, lerr := bindlin.BindTxtarLineage(tc, rt)
		if lerr != nil {
			tc.Fatal(lerr)
		}
		for sch := lin.First(); sch != nil; sch = sch.Successor() {
			f, err := GenerateSchema(sch)
			if err != nil {
				tc.WriteErrors(errors.Promote(err, sch.Version().String()))
				continue
			}
			compile(tc, f)
			f.Filename = sch.Version().String() + ".json"
			tc.WriteFile(f)
		}
	})
}

func TestExemplarExportIsValid(t *testing.T) {
	for name, lin := range exemplars.All(rt) {
		t.Run(name, func(t *testing.T) {
			for sch := lin.First(); sch != nil; sch = sch.Successor() {
				t.Run(sch.Version().String(), func(t *testing.T) {
					f, err := GenerateSchema(sch)
					if err != nil {
						t.Fatal(err)
					}
					s, _ := compile(t, f)
					for ename, ex := range sch.Examples() {
						b, err := ex.Underlying().MarshalJSON()
						if err != nil {
							t.Fatal(err)
						}
						inst, _ := sjsonschema.UnmarshalJSON(bytes.NewReader(b))
						if err := s.Validate(inst); err != nil {
							t.Errorf("example %s does not validate: %v", ename, err)
						}
					}
				})
			}
		})
	}
}

const defaultsLineage = `
name: "defaults"
joinSchema: {
	kind: string | *"defaults"
}
schemas: "0": "0": {
	schema: {
		// The size.
		size:   "small" | "medium" | *"large"
		count?: int | *1
		nested: {
			on: bool | *true
		}
		list: [...{
			weight: number | *0.5
		}]
		byName: [string]: {
			n: int | *7
		}
		pos?:    #Point
		origin:  #Point | *{x: 0, y: 0}
		either:  {t: "a", a: int | *1} | {t: "b", b: string}
		#Point: {
			x: number
			y: number | *0
		}
	}
	examples: full: {
		kind: "defaults"
		size: "small"
		nested: on: false
		list: [{weight: 1}]
		byName: a: n: 1
		origin: {x: 1, y: 2}
		either: {t: "b", b: "b"}
	}
}
`

func TestDefaults(t *testing.T) {
	lin, err := thema.BindLineage(rt.Context().CompileString(defaultsLineage), rt)
	if err != nil {
		t.Fatal(err)
	}
	f, err := GenerateSchema(lin.Latest())
	if err != nil {
		t.Fatal(err)
	}
	s, b := compile(t, f)

	var doc map[string]any
	if err = stdjson.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	at := func(path string) any {
		var x any = doc
		for _, p := range strings.Split(path, "/") {
			m, ok := x.(map[string]any)
			if !ok {
				return nil
			}
			x = m[p]
		}
		return x
	}
	for path, want := range map[string]any{
		"properties/kind/default":                                     "defaults",
		"properties/size/default":                                     "large",
		"properties/count/default":                                    1.0,
		"properties/nested/properties/on/default":                     true,
		"properties/list/items/properties/weight/default":             0.5,
		"properties/byName/additionalProperties/properties/n/default": 7.0,
		"$defs/Point/properties/y/default":                            0.0,
		"properties/origin/default":                                   map[string]any{"x": 0.0, "y": 0.0},
		"properties/list/default":                                     []any{},
	} {
		got, _ := stdjson.Marshal(at(path))
		exp, _ := stdjson.Marshal(want)
		if !bytes.Equal(got, exp) {
			t.Errorf("%s: got %s, want %s", path, got, exp)
		}
	}
	// A default within a branch of a disjunction, either's a, has nowhere
	// meaningful to go.
	if strings.Count(string(b), `"default"`) != 9 {
		t.Errorf("want exactly the 9 defaults above, got:\n%s", b)
	}
	if at("properties/size/description") != "The size." {
		t.Errorf("doc comment not carried as description:\n%s", b)
	}

	inst := func(s string) any {
		x, uerr := sjsonschema.UnmarshalJSON(strings.NewReader(s))
		if uerr != nil {
			t.Fatal(uerr)
		}
		return x
	}
	ex, err := lin.Latest().Examples()["full"].Underlying().MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(inst(string(ex))); err != nil {
		t.Errorf("the lineage's example does not validate: %v", err)
	}
	// Defaulted fields may be omitted; x of a #Point may not.
	if err := s.Validate(inst(`{"nested": {}, "list": [], "byName": {}, "either": {"t": "a"}}`)); err != nil {
		t.Errorf("an instance relying on defaults does not validate: %v", err)
	}
	if err := s.Validate(inst(`{"nested": {}, "list": [], "byName": {}, "either": {"t": "a"}, "pos": {"y": 1}}`)); err == nil {
		t.Error("a #Point without x validates")
	}
}

// GenerateSchema adds defaults because CUE's generator does not emit them.
// When this fails, CUE has started to: drop addDefaults, which would then
// duplicate or conflict with CUE's own, and update this package's docs.
func TestCUEEmitsNoDefaults(t *testing.T) {
	v := rt.Context().CompileString(`{a: int | *1, b: {c: string | *"c"}}`)
	x, err := cuejsonschema.Generate(v, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := rt.Context().BuildExpr(x).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(`"default"`)) {
		t.Fatalf("CUE's JSON Schema generator now emits defaults:\n%s", b)
	}
}
