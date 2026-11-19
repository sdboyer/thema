package openapi

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	"github.com/sdboyer/thema/internal/jsonschema"
)

// closedDoc holds a component for each way Extract closes an object, or
// leaves it open. Its schemas are CUE, which a JSON document compiles to:
// req.a is an object that requires a string property a.
const closedDoc = `
let req = {for n in ["a", "b", "c", "d", "oar", "hull", "sail"] {(n): {type: "object", required: [n], properties: (n): type: "string"}}}
openapi: "3.0.3"
info: {title: "ships", version: "1"}
paths: {}
components: schemas: {
	Ship: {type: "object", properties: name: type: "string"}
	OpenShip: {type: "object", additionalProperties: true, properties: name: type: "string"}
	ShutShip: {type: "object", additionalProperties: false, properties: name: type: "string"}
	Cargo: {type: "object", additionalProperties: type: "integer", properties: name: type: "string"}
	Anything: {type: "object"}
	Nothing: {type: "object", additionalProperties: false}
	Empty: {type: "object", properties: a: {}}
	RequiredEmpty: {type: "object", required: ["a"], properties: a: {}}
	Untyped: properties: name: type: "string"
	Deep: {type: "object", properties: inner: {type: "object", properties: x: type: "string"}}
	RequiredOnly: required: ["name"]
	Crewed: allOf: [{$ref: "#/components/schemas/Ship"}, {properties: crew: type: "integer"}]
	RequiredPart: allOf: [{$ref: "#/components/schemas/Ship"}, {required: ["name"]}]
	RequiredRef: allOf: [{$ref: "#/components/schemas/Ship"}, {$ref: "#/components/schemas/RequiredOnly"}]
	FreeRef: allOf: [{properties: name: type: "string"}, {$ref: "#/components/schemas/Anything"}]
	NullPart: allOf: [{$ref: "#/components/schemas/Ship"}, {nullable: true, required: ["name"]}]
	UntypedRef: allOf: [{$ref: "#/components/schemas/Untyped"}, {required: ["name"]}]
	Nested: allOf: [{$ref: "#/components/schemas/Ship"}, {allOf: [{required: ["name"]}, {properties: crew: type: "integer"}]}]
	Beside: {type: "object", properties: crew: type: "integer", allOf: [{$ref: "#/components/schemas/Ship"}]}
	Frees: allOf: [{$ref: "#/components/schemas/Anything"}, {required: ["name"]}]
	Choice: oneOf: [{type: "object", properties: a: type: "string"}, {type: "object", properties: b: type: "string"}]
	AnyChoice: anyOf: [{type: "object", properties: a: type: "string"}, {type: "object", properties: b: type: "string"}]
	RefChoice: oneOf: [{$ref: "#/components/schemas/Ship"}, {$ref: "#/components/schemas/Deep"}]
	Pair: allOf: [{oneOf: [req.a, req.b]}, {oneOf: [req.c, req.d]}]
	Both: {oneOf: [req.a, req.b], anyOf: [req.c, req.d]}
	Pinned: {type: "object", required: ["kind"], oneOf: [req.a, req.b]}
	Patterned: {type: "object", additionalProperties: type: "string", oneOf: [req.a, req.b]}
	Hulled: oneOf: [{type: "object", required: ["crew"], properties: crew: type: "integer", allOf: [{$ref: "#/components/schemas/Ship"}]}, req.oar]
	Hull: req.hull
	RefBeside: {type: "object", properties: k: type: "string", oneOf: [{$ref: "#/components/schemas/Ship"}, {$ref: "#/components/schemas/Hull"}]}
	Sails: oneOf: [{$ref: "#/components/schemas/Hull"}, req.sail]
	UnionPart: allOf: [{$ref: "#/components/schemas/Sails"}, {properties: k: type: "string"}]
	UnionPair: allOf: [{$ref: "#/components/schemas/Sails"}, {oneOf: [req.c, req.d]}]
	NestedBeside: allOf: [{oneOf: [{oneOf: [req.a, req.b]}, req.c]}, {properties: k: type: "string"}]
	Either: {type: "object", properties: {name: type: "string", sail: type: "string"}, oneOf: [{required: ["name"]}, {required: ["sail"]}]}
	AnyEither: {type: "object", properties: {name: type: "string", sail: type: "string"}, anyOf: [{required: ["name"]}, {required: ["sail"]}]}
	Oared: {type: "object", properties: name: type: "string", oneOf: [req.sail, req.oar]}
	Rigged: allOf: [{$ref: "#/components/schemas/Ship"}, {oneOf: [req.sail, req.oar]}]
}
`

// TestExtractClosed checks what Extract's objects admit: an object that
// declares a property is closed, whatever additionalProperties says unless
// it's a schema; one that declares none stays open; an allOf is one object,
// whose parts close with those that declare a property, a reference as the
// schema it refers to; and each alternative of a oneOf or an anyOf with
// nothing beside it is closed alone, while alternatives beside other
// schemas admit the properties those declare. TestKnownGaps has what the
// latter admit beyond them.
func TestExtractClosed(t *testing.T) {
	ctx := cuecontext.New()
	doc := ctx.CompileString(closedDoc)
	if err := doc.Err(); err != nil {
		t.Fatal(err)
	}
	f, err := Extract(doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := ctx.BuildFile(f)
	if err := v.Err(); err != nil {
		t.Fatalf("%v\n%s", err, formatFile(t, f))
	}
	// named is data every Ship-like schema admits, undeclared that data with
	// a property none of them declares, and crewed that data with a crew.
	const named, undeclared, crewed = `{name: "x"}`, `{name: "x", z: 1}`, `{name: "x", crew: 2}`
	for _, c := range []struct {
		def             string
		admits, refuses []string
	}{
		{"Ship", []string{named}, []string{undeclared}},
		{"OpenShip", nil, []string{undeclared}},
		{"ShutShip", nil, []string{undeclared}},
		{"Cargo", []string{undeclared}, []string{`{name: "x", z: "y"}`}},
		{"Anything", []string{`{z: 1}`}, nil},
		{"Nothing", []string{`{}`}, []string{`{z: 1}`}},
		{"Empty", []string{`{a: 1}`}, []string{`{a: 1, z: 1}`}},
		{"RequiredEmpty", []string{`{a: 1}`}, []string{`{a: 1, z: 1}`}},
		{"Untyped", []string{named, `"x"`}, []string{undeclared}},
		{"Deep", []string{`{inner: {x: "y"}}`}, []string{`{inner: {x: "y", z: 1}}`}},
		{"RequiredOnly", []string{`{name: 1, z: 1}`}, nil},
		{"Crewed", []string{crewed}, []string{undeclared}},
		{"RequiredPart", []string{named}, []string{undeclared}},
		{"RequiredRef", []string{named}, []string{undeclared}},
		{"FreeRef", []string{named}, []string{undeclared}},
		{"NullPart", []string{named}, []string{undeclared}},
		{"UntypedRef", []string{named}, []string{undeclared}},
		{"Nested", []string{crewed}, []string{`{name: "x", crew: 2, z: 1}`}},
		{"Beside", []string{crewed}, []string{`{name: "x", crew: 2, z: 1}`}},
		{"Frees", []string{`{name: 1, z: 1}`}, nil},
		{"Choice", []string{`{a: "x"}`}, []string{`{a: "x", z: 1}`, `{a: "x", b: "y"}`}},
		{"AnyChoice", []string{`{b: "x"}`}, []string{`{b: "x", z: 1}`}},
		{"RefChoice", []string{named}, []string{undeclared}},
		{"Pair", []string{`{a: "x", c: "y"}`}, nil},
		{"Both", []string{`{a: "x", c: "y"}`}, nil},
		{"Pinned", []string{`{kind: "k", a: "x"}`}, nil},
		{"Patterned", []string{`{a: "x", k: "y"}`}, nil},
		{"Hulled", []string{`{name: "x", crew: 1}`}, []string{`{name: "x", crew: 1, z: 1}`}},
		{"RefBeside", []string{`{k: "x", name: "y"}`}, nil},
		{"UnionPart", []string{`{sail: "x", k: "y"}`}, nil},
		{"UnionPair", []string{`{sail: "x", c: "y"}`}, nil},
		{"NestedBeside", []string{`{a: "x", k: "y"}`}, nil},
		{"Either", []string{named}, nil},
		{"AnyEither", []string{named}, nil},
		{"Oared", []string{`{name: "x", sail: "y"}`}, nil},
		{"Rigged", []string{`{name: "x", sail: "y"}`}, nil},
	} {
		def := v.LookupPath(cue.MakePath(cue.Def(c.def)))
		if !def.Exists() {
			t.Fatalf("no #%s in:\n%s", c.def, formatFile(t, f))
		}
		check := func(data string, valid bool) {
			err := def.Unify(ctx.CompileString(data)).Validate(cue.Concrete(true))
			if (err == nil) != valid {
				t.Errorf("#%s admits %s: %v, want %v (%v)", c.def, data, err == nil, valid, err)
			}
		}
		for _, data := range c.admits {
			check(data, true)
		}
		for _, data := range c.refuses {
			check(data, false)
		}
	}
	if t.Failed() {
		t.Logf("extracted:\n%s", formatFile(t, f))
	}
}

// TestExtractClosedCycle checks that an allOf part whose schema refers, in
// a cycle, only to schemas that declare no property is extracted: the
// cycle is referred to, not decoded in place again.
func TestExtractClosedCycle(t *testing.T) {
	ctx := cuecontext.New()
	doc := ctx.CompileString(`
openapi: "3.0.3"
info: {title: "ships", version: "1"}
paths: {}
components: schemas: {
	Ship: allOf: [{properties: name: type: "string"}, {$ref: "#/components/schemas/B"}]
	B: allOf: [{required: ["name"]}, {$ref: "#/components/schemas/C"}]
	C: allOf: [{$ref: "#/components/schemas/B"}]
}
`)
	if err := doc.Err(); err != nil {
		t.Fatal(err)
	}
	f, err := Extract(doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(formatFile(t, f)), "#Ship") {
		t.Errorf("no #Ship in:\n%s", formatFile(t, f))
	}
}

// TestExtractClosedErrors checks that a schema decoded in place reports
// its errors once, where it is defined, as upstream does.
func TestExtractClosedErrors(t *testing.T) {
	ctx := cuecontext.New()
	doc := ctx.CompileString(`
openapi: "3.0.3"
info: {title: "ships", version: "1"}
paths: {}
components: schemas: {
	Free: {type: "object", foo: 1}
	Ship: allOf: [{$ref: "#/components/schemas/Free"}, {properties: name: type: "string"}]
}
`)
	_, closedErr := Extract(doc, nil)
	_, upErr := extract(doc, nil, false)
	if got, want := len(errors.Errors(closedErr)), len(errors.Errors(upErr)); got != want || want == 0 {
		t.Errorf("Extract reports %d errors, upstream %d:\n%v\n%v", got, want, closedErr, upErr)
	}
}

// TestExtractClosedHiddenRef checks that an allOf part referring into a
// schema's properties, which jsonschema.Extract writes as a field of
// _#defs, is embedded as a definition: the object holds the other parts'
// properties too.
func TestExtractClosedHiddenRef(t *testing.T) {
	ctx := cuecontext.New()
	doc := ctx.CompileString(`{
	$defs: holder: {type: "object", properties: inner: {type: "object", properties: x: type: "string"}}
	type: "object"
	properties: n: allOf: [{$ref: "#/$defs/holder/properties/inner"}, {properties: y: type: "string"}]
}`)
	f, err := jsonschema.Extract(doc, &jsonschema.Config{Closed: true})
	if err != nil {
		t.Fatal(err)
	}
	// The schema is a regular value, which validation closes, as a
	// definition does.
	s := cue.MakePath(cue.Def("S"))
	v := ctx.CompileString("#S: _").FillPath(s, ctx.BuildFile(f)).LookupPath(s)
	for data, valid := range map[string]bool{
		`{n: {x: "a", y: "b"}}`:       true,
		`{n: {x: "a", y: "b", z: 1}}`: false,
	} {
		err := v.Unify(ctx.CompileString(data)).Validate(cue.Concrete(true))
		if (err == nil) != valid {
			t.Errorf("admits %s: %v, want %v (%v)\n%s", data, err == nil, valid, err, formatFile(t, f))
		}
	}
}
