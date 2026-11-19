package openapi

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
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
	ShutCrew: allOf: [{$ref: "#/components/schemas/ShutShip"}, {properties: crew: type: "integer"}]
	CargoCrew: allOf: [{$ref: "#/components/schemas/Cargo"}, {properties: crew: type: "string"}]
	AltInner: oneOf: [{type: "object", properties: k: type: "string", oneOf: [req.a, req.c]}, req.b]
	NestAlt: oneOf: [{type: "object", properties: inner: {type: "object", properties: x: type: "string"}}, req.b]
	NestItems: oneOf: [{type: "object", properties: l: {type: "array", items: {type: "object", properties: x: type: "string"}}}, req.b]
	NestBeside: {type: "object", required: ["kind"], oneOf: [{properties: meta: {type: "object", properties: x: type: "string"}}, req.b]}
	NestPart: oneOf: [{allOf: [{$ref: "#/components/schemas/Ship"}, {properties: inner: {type: "object", properties: x: type: "string"}}]}, req.b]
	ShutPart: allOf: [{$ref: "#/components/schemas/Ship"}, {type: "object", properties: crew: type: "integer", additionalProperties: false}]
	TypedPart: allOf: [{$ref: "#/components/schemas/Ship"}, {type: "object", properties: crew: type: "integer", additionalProperties: type: "integer"}]
	ShutBeside: {type: "object", additionalProperties: false, properties: crew: type: "integer", allOf: [{$ref: "#/components/schemas/Ship"}]}
	AnyReq: anyOf: [req.a, req.b]
	AnyNest: {type: "object", properties: p: anyOf: [req.a, req.b]}
	NotBeside: {type: "object", properties: a: type: "string", not: required: ["b"]}
	NotAlt: oneOf: [{type: "object", properties: {a: type: "string", b: type: "string"}, not: {required: ["b"], properties: b: enum: ["bad"]}}, req.c]
	MergeAlt: oneOf: [{allOf: [{properties: inner: {type: "object", properties: x: type: "string"}}, {properties: inner: {type: "object", properties: y: type: "string"}}]}, req.b]
	MergeRef: oneOf: [{allOf: [{$ref: "#/components/schemas/Deep"}, {properties: inner: {type: "object", properties: y: type: "string"}}]}, req.b]
	Inner: {type: "object", properties: p: {type: "object", properties: z: type: "string"}}
	UnionMerge: allOf: [{$ref: "#/components/schemas/Inner"}, {properties: p: oneOf: [req.a, req.b]}]
	UnionBase: {type: "object", properties: p: oneOf: [req.a, req.b]}
	UnionRev: allOf: [{$ref: "#/components/schemas/UnionBase"}, {properties: p: {type: "object", properties: z: type: "string"}}]
	MergeItems: oneOf: [{allOf: [{properties: l: {type: "array", items: {type: "object", properties: x: type: "string"}}}, {properties: l: {type: "array", items: {type: "object", properties: y: type: "string"}}}]}, req.b]
	RefMerge: allOf: [{properties: p: $ref: "#/components/schemas/Ship"}, {properties: p: {type: "object", properties: z: type: "string"}}]
	UnionOwn: allOf: [{$ref: "#/components/schemas/Ship"}, {properties: p: oneOf: [req.a, req.b]}]
}
`

// named is data every Ship-like schema of closedDoc admits, undeclared
// that data with a property none of them declares, and crewed that data
// with a crew. crewOnly is a crew alone, ab and ac data with properties
// of two alternatives, and aUndeclared a with a property no schema
// declares.
const (
	named, undeclared, crewed = `{name: "x"}`, `{name: "x", z: 1}`, `{name: "x", crew: 2}`
	crewOnly                  = `{crew: 1}`
	ab, ac, aUndeclared       = `{a: "x", b: "y"}`, `{a: "x", c: "y"}`, `{a: "x", z: 1}`
)

// TestExtractClosed checks what Extract's objects admit: an object that
// declares a property is closed, whatever additionalProperties says unless
// it's a schema; one that declares none stays open; an allOf is one object,
// whose parts close with those that declare a property, a reference as the
// schema it refers to; and each alternative of a oneOf with nothing beside
// it is closed alone, while alternatives beside other schemas, and those of
// an anyOf, admit the properties those declare. An object beneath an
// alternative is closed too, unless another schema declares the property
// whose value it is. TestKnownGaps has what is admitted beyond that.
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
		{"Choice", []string{`{a: "x"}`}, []string{aUndeclared, ab}},
		{"AnyChoice", []string{`{b: "x"}`, ab}, nil},
		{"RefChoice", []string{named}, []string{undeclared}},
		{"Pair", []string{ac}, nil},
		{"Both", []string{ac}, nil},
		{"Pinned", []string{`{kind: "k", a: "x"}`}, nil},
		{"Patterned", []string{`{a: "x", k: "y"}`}, nil},
		{"Hulled", []string{crewed}, []string{`{name: "x", crew: 1, z: 1}`}},
		{"RefBeside", []string{`{k: "x", name: "y"}`}, nil},
		{"UnionPart", []string{`{sail: "x", k: "y"}`}, nil},
		{"UnionPair", []string{`{sail: "x", c: "y"}`}, nil},
		{"NestedBeside", []string{`{a: "x", k: "y"}`}, nil},
		{"Either", []string{named}, nil},
		{"AnyEither", []string{named}, nil},
		{"Oared", []string{`{name: "x", sail: "y"}`}, nil},
		{"Rigged", []string{`{name: "x", sail: "y"}`}, nil},
		{"ShutCrew", []string{named}, []string{crewed}},
		{"CargoCrew", []string{named, undeclared}, []string{`{name: "x", crew: "y"}`, `{name: "x", z: "y"}`}},
		{"AltInner", []string{`{k: "x", a: "y"}`}, nil},
		{"NestAlt", []string{`{inner: {x: "y"}}`}, []string{`{inner: {x: "y", z: 1}}`, `{inner: {x: "y"}, z: 1}`}},
		{"NestItems", []string{`{l: [{x: "y"}]}`}, []string{`{l: [{x: "y", z: 1}]}`}},
		{"NestBeside", []string{`{kind: "k", meta: {x: "y"}}`}, []string{`{kind: "k", meta: {x: "y", z: 1}}`}},
		{"NestPart", []string{`{name: "x", inner: {x: "y"}}`}, []string{`{name: "x", inner: {x: "y", z: 1}}`, `{name: "x", z: 1}`}},
		{"ShutPart", []string{crewOnly}, []string{named}},
		{"TypedPart", []string{crewOnly, `{crew: 1, z: 2}`}, []string{named}},
		{"ShutBeside", []string{crewOnly}, []string{named}},
		{"AnyReq", []string{ab}, nil},
		{"AnyNest", []string{`{p: {a: "x", b: "y"}}`}, nil},
		{"NotBeside", []string{`{a: "x"}`}, []string{ab}},
		{"NotAlt", []string{`{a: "x", b: "good"}`}, []string{`{a: "x", b: "bad"}`}},
		{"MergeAlt", []string{`{inner: {x: "a", y: "b"}}`}, []string{`{inner: {x: "a"}, z: 1}`}},
		{"MergeRef", []string{`{inner: {x: "a", y: "b"}}`}, []string{`{inner: {x: "a"}, z: 1}`}},
		{"UnionMerge", []string{`{p: {a: "x", z: "y"}}`}, []string{`{p: {a: "x"}, k: 1}`}},
		{"UnionRev", []string{`{p: {a: "x", z: "y"}}`}, []string{`{p: {a: "x"}, k: 1}`}},
		{"MergeItems", []string{`{l: [{x: "a", y: "b"}]}`}, nil},
		{"RefMerge", []string{`{p: {name: "x", z: "y"}}`}, []string{`{p: {name: "x", k: 1}}`}},
		{"UnionOwn", []string{`{name: "x", p: {a: "y"}}`}, []string{`{p: {a: "y", z: 1}}`}},
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

// TestExtractClosedSubpath checks a schema decoded in place when the
// extraction converts one schema, as --src-subpath does: the schemas
// beneath it that are referred to are defined, and its errors are
// reported, as upstream reports them.
func TestExtractClosedSubpath(t *testing.T) {
	ctx := cuecontext.New()
	extract := func(doc string, closed bool) (*ast.File, error) {
		return jsonschema.Extract(ctx.CompileString(doc), &jsonschema.Config{Root: "#/$defs/ship", SingleRoot: true, Closed: closed})
	}
	f, err := extract(`{
	$defs: free: {type: "object", additionalProperties: {type: "string", minLength: 1}}
	$defs: ship: allOf: [{$ref: "#/$defs/free"}, {properties: name: $ref: "#/$defs/free/additionalProperties"}]
}`, true)
	if err != nil {
		t.Fatal(err)
	}
	v := ctx.BuildFile(f)
	if err := v.Unify(ctx.CompileString(`{name: "x"}`)).Validate(cue.Concrete(true)); err != nil {
		t.Errorf("refuses {name: \"x\"}: %v\n%s", err, formatFile(t, f))
	}

	bad := `{
	$defs: free: {type: "object", minProperties: "x"}
	$defs: ship: allOf: [{$ref: "#/$defs/free"}, {properties: name: type: "string"}]
}`
	_, closedErr := extract(bad, true)
	_, upErr := extract(bad, false)
	if got, want := len(errors.Errors(closedErr)), len(errors.Errors(upErr)); got != want || want == 0 {
		t.Errorf("Extract reports %d errors, upstream %d:\n%v\n%v", got, want, closedErr, upErr)
	}
}

// closedDoc31 holds the components of closedDoc's kind that only OpenAPI
// 3.1 can have.
const closedDoc31 = `
openapi: "3.1.0"
info: {title: "ships", version: "1"}
paths: {}
components: schemas: {
	Ship: {type: "object", properties: name: type: "string"}
	Anything: {type: "object"}
	RefCrewed: {$ref: "#/components/schemas/Ship", properties: crew: type: "integer"}
	Free: {$ref: "#/components/schemas/Anything", properties: crew: type: "integer"}
	Hull: {type: "object", required: ["hull"], properties: hull: type: "string"}
	Sails: oneOf: [{$ref: "#/components/schemas/Hull"}, {type: "object", required: ["sail"], properties: sail: type: "string"}]
	RefOneOf: {$ref: "#/components/schemas/Sails", oneOf: [{required: ["c"], properties: c: type: "string"}, {required: ["d"], properties: d: type: "string"}]}
	RefRequired: {$ref: "#/components/schemas/Sails", required: ["k"]}
	ShutRef: {$ref: "#/components/schemas/Ship", properties: crew: type: "integer", additionalProperties: false}
	Skipped: {type: "object", properties: name: type: "string", patternProperties: "^(?!name)": type: "string"}
	IfAlt: oneOf: [{type: "object", properties: a: type: "string", if: {required: ["a"]}, then: {properties: c: type: "string"}}, Hull]
	ElseAlt: oneOf: [{type: "object", properties: {k: type: "string", p: type: "string"}, if: {properties: k: enum: ["x"]}, then: {}, else: {required: ["q"]}}, Hull]
	DepAlt: oneOf: [{type: "object", properties: a: type: "string", dependentSchemas: a: properties: c: type: "string"}, Hull]
	ContainsAlt: oneOf: [{type: "object", properties: l: {type: "array", contains: {type: "object", properties: x: type: "string"}}}, Hull]
	CondNest: oneOf: [{type: "object", properties: a: type: "string", if: {required: ["a"]}, then: properties: c: {type: "object", properties: x: type: "string"}}, Hull]
	CondBeside: {type: "object", properties: a: type: "string", if: {required: ["a"]}, then: properties: c: type: "string"}
	CondPart: allOf: [{properties: name: type: "string"}, {if: {required: ["name"]}, then: properties: c: type: "string"}]
}
`

// TestExtractClosedRefSiblings checks a $ref beside other keywords. From
// OpenAPI 3.1 and JSON Schema 2019-09 they apply with it, as an allOf of
// the two does. Before, they are ignored, and so declare nothing. It also
// checks patternProperties and conditionals, which OpenAPI 3.0 doesn't
// have.
func TestExtractClosedRefSiblings(t *testing.T) {
	ctx := cuecontext.New()
	check := func(v cue.Value, f *ast.File, data string, valid bool) {
		t.Helper()
		err := v.Unify(ctx.CompileString(data)).Validate(cue.Concrete(true))
		if (err == nil) != valid {
			t.Errorf("admits %s: %v, want %v (%v)\n%s", data, err == nil, valid, err, formatFile(t, f))
		}
	}
	f, err := Extract(ctx.CompileString(closedDoc31), nil)
	if err != nil {
		t.Fatal(err)
	}
	v := ctx.BuildFile(f)
	for _, c := range []struct {
		def, data string
		valid     bool
	}{
		{"RefCrewed", crewed, true},
		{"RefCrewed", `{name: "x", crew: 1, z: 1}`, false},
		{"Free", crewOnly, true},
		{"Free", `{crew: 1, z: 1}`, false},
		{"RefOneOf", `{sail: "x", c: "y"}`, true},
		{"RefOneOf", `{hull: "x", c: "y"}`, true},
		{"RefRequired", `{sail: "x", k: 1}`, true},
		{"ShutRef", crewOnly, true},
		{"ShutRef", `{name: "x"}`, false},
		// A pattern Go can't compile leaves the properties it matches
		// unknown, so the object stays open, as upstream leaves it.
		{"Skipped", `{name: "x", foo: "y"}`, true},
		{"IfAlt", ac, true},
		{"ElseAlt", `{k: "x", p: "y"}`, true},
		{"DepAlt", ac, true},
		{"ContainsAlt", `{l: [{x: "a", w: 1}]}`, true},
		{"CondNest", `{a: "x", c: {x: "y"}}`, true},
		{"CondNest", `{a: "x", c: {x: "y", z: 1}}`, false},
	} {
		check(v.LookupPath(cue.MakePath(cue.Def(c.def))), f, c.data, c.valid)
	}

	// In draft-07 the properties beside the $ref are ignored, so the allOf
	// declares nothing and stays open.
	f, err = jsonschema.Extract(ctx.CompileString(`{
	$schema: "http://json-schema.org/draft-07/schema#"
	definitions: anything: type: "object"
	allOf: [{$ref: "#/definitions/anything", properties: a: type: "string"}, {required: ["b"]}]
}`), &jsonschema.Config{Closed: true})
	if err != nil {
		t.Fatal(err)
	}
	s := cue.MakePath(cue.Def("S"))
	check(ctx.CompileString("#S: _").FillPath(s, ctx.BuildFile(f)).LookupPath(s), f, `{b: 1, z: 1}`, true)
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
