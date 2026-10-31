package thema

import (
	"encoding/json"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// A lacuna's value is JSON-shaped data whose object keys are ordered by the
// code points of their labels and whose list elements keep their order; its
// path is a CUE path, in the form cue.Path prints one (docs/translation.md,
// How a lacuna is written).
func TestLacunaEncoding(t *testing.T) {
	ctx := cuecontext.New()
	lin, err := bindString(t, NewRuntime(ctx), `lin: thema.Lineage & {
	name: "encoding"
	schemas: {
		"0": "0": schema: {
			obj: {b: int, a: int, Z: int, "é": int, "10": int, "9": int, "a-b": int}
			list: [...int]
			pair: [int, int]
			"a-b": {c: string}
		}
		"1": "0": schema: {
			v: int
		}
	}
	lenses: {
		"0.0->1.0": {
			v0_0: _
			v1_0: {v: 1}
			lacunas: [{
				type:    "DroppedField"
				message: "dropped"
				sourceFields: [
					{path: "obj", value: v0_0.obj},
					{path: "pair[1]", value: v0_0.pair[1]},
					{path: "list", value: v0_0.list},
					{path: "\"a-b\".c", value: v0_0."a-b".c},
				]
			}]
		}
		"1.0->0.0": {
			v1_0: _
			v0_0: {obj: {b: 0, a: 0, Z: 0, "é": 0, "10": 0, "9": 0, "a-b": 0}, list: [], pair: [0, 0], "a-b": c: ""}
		}
	}
}`)
	if err != nil {
		t.Fatal(err)
	}
	sch, err := lin.Schema(SV(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	in, err := sch.Validate(ctx.CompileString(`{obj: {b: 6, a: 4, Z: 3, "é": 7, "10": 1, "9": 2, "a-b": 5}, list: [3, 1, 2], pair: [8, 9], "a-b": {c: "x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	_, lac, err := in.Translate(SV(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(lac[0].Lacunas[0].SourceFields)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"path":"obj","value":{"10":1,"9":2,"Z":3,"a":4,"a-b":5,"b":6,"é":7}},` +
		`{"path":"pair[1]","value":9},{"path":"list","value":[3,1,2]},{"path":"\"a-b\".c","value":"x"}]`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}

	// The forms of a path, as cue.Path prints them and as the derivations
	// write them.
	for _, c := range []struct {
		path cue.Path
		want string
	}{
		{cue.MakePath(cue.Str("a"), cue.Str("b")), `a.b`},
		{cue.MakePath(cue.Str("items"), cue.Index(0), cue.Str("name")), `items[0].name`},
		{cue.MakePath(cue.Index(2)), `[2]`},
		{cue.MakePath(cue.Str("a-b"), cue.Str("c")), `"a-b".c`},
		{cue.MakePath(cue.Str("1a")), `"1a"`},
		{cue.MakePath(cue.Str("_x")), `"_x"`},
		{cue.MakePath(cue.Str("#x")), `"#x"`},
		{cue.MakePath(cue.Str("")), `""`},
		{cue.MakePath(cue.Str(`x"y`)), `"x\"y"`},
		{cue.MakePath(cue.Str("é")), `é`},
		{cue.MakePath(cue.Str("$a")), `$a`},
		{cue.MakePath(cue.Str("for")), `for`},
	} {
		if got := c.path.String(); got != c.want {
			t.Errorf("path prints as %s, want %s", got, c.want)
		}
	}
}
