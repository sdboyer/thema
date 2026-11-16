package openapi

import (
	"bytes"
	"encoding/json"
	"maps"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	cuejson "cuelang.org/go/encoding/json"
	"github.com/google/go-cmp/cmp"
	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	tastutil "github.com/sdboyer/thema/internal/astutil"
	"github.com/sdboyer/thema/internal/jsonschema"
)

// readFixture reads the fixture testdata/extract/<name>.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata/extract", name)) //nolint:gosec // a fixture of this package's testdata
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// extractFixture extracts the fixture testdata/extract/<name>.
func extractFixture(t *testing.T, name string) *ast.File {
	t.Helper()
	v := cuecontext.New().CompileBytes(readFixture(t, name))
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	f, err := Extract(v, nil)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// fieldsByPath returns the fields of n, keyed by their labels joined by ".",
// through every struct, embedding, conjunction, disjunction, call and
// comprehension; a list's elements add "[]". A label two fields share, such
// as a deprecated struct's, has both, in order.
func fieldsByPath(n ast.Node) map[string][]*ast.Field {
	fields := map[string][]*ast.Field{}
	var walk func(n ast.Node, at string)
	walk = func(n ast.Node, at string) {
		switch x := n.(type) {
		case *ast.File:
			for _, d := range x.Decls {
				walk(d, at)
			}
		case *ast.StructLit:
			for _, d := range x.Elts {
				walk(d, at)
			}
		case *ast.Field:
			name, _, _ := ast.LabelName(x.Label)
			p := strings.TrimPrefix(at+"."+name, ".")
			fields[p] = append(fields[p], x)
			walk(x.Value, p)
		case *ast.EmbedDecl:
			walk(x.Expr, at)
		case *ast.BinaryExpr:
			walk(x.X, at)
			walk(x.Y, at)
		case *ast.ParenExpr:
			walk(x.X, at)
		case *ast.CallExpr:
			for _, a := range x.Args {
				walk(a, at)
			}
		case *ast.Comprehension:
			walk(x.Value, at)
		case *ast.ListLit:
			for _, e := range x.Elts {
				if el, ok := e.(*ast.Ellipsis); ok {
					e = el.Type
				}
				if e != nil {
					walk(e, at+"[]")
				}
			}
		}
	}
	walk(n, "")
	return fields
}

// extensionAttrs returns the text of every @openapi attribute in f, keyed by
// the path of its field.
func extensionAttrs(f *ast.File) map[string][]string {
	got := map[string][]string{}
	for p, fs := range fieldsByPath(f) {
		for _, fd := range fs {
			for _, a := range fd.Attrs {
				if a.Name() == jsonschema.ExtensionsAttr {
					got[p] = append(got[p], a.Text)
				}
			}
		}
	}
	return got
}

// extensions30 is the text of every @openapi attribute Extract writes for
// testdata/extract/extensions-3.0.json, keyed by the path of its field.
var extensions30 = map[string][]string{
	// Each kind of JSON value.
	"#Scalars.str": {`@openapi(x-property-name="ship-registry-id")`},
	"#Scalars.yes": {`@openapi(x-sensitive=true)`},
	"#Scalars.num": {`@openapi(x-precision=1.5E-7)`},
	"#Scalars.int": {`@openapi(x-big=100000000000000000000000)`},
	"#Scalars.nul": {`@openapi(x-nothing=null)`},
	"#Scalars.obj": {`@openapi(x-obj={"b":"c,d","a":[1,{"e":"f)"}],"g":{}})`},
	"#Scalars.arr": {`@openapi(x-arr=[1,"(",")","a, b",{"k":[true]},[]])`},
	// Quotes and backslashes escaped as JSON escapes them, and HTML not.
	"#Scalars.esc": {`@openapi(x-esc="<a & b> \"q\" \\(x) \n \t é")`},
	// Several keys, sorted.
	"#Scalars.sorted": {`@openapi(x-a=2, x-m=3, x-z=1)`},
	// Keys that are not words joined by -, as JSON strings.
	"#Scalars.odd-keys": {`@openapi("x-"=4, "x-1e"=1, "x-a b"=6, "x-a\"b"=5, "x-a.b"=2, "x-a//b"=3)`},
	// A required property keeps its attribute.
	"#Scalars.required": {`@openapi(x-req=1)`},
	// Beside @deprecated(), on the one field of a property with every
	// attribute: for a struct, the first of the two fields its label has.
	"#Scalars.deprecated":       {`@openapi(x-dep=1)`},
	"#Scalars.deprecatedObject": {`@openapi(x-dep=1)`},
	// Beside a $ref.
	"#Scalars.ref": {`@openapi(x-beside-ref="kept")`},
	// Nested properties, and the struct's own.
	"#Scalars.nested":       {`@openapi(x-outer=1)`},
	"#Scalars.nested.inner": {`@openapi(x-inner="deep")`},
	// The properties of an array's items, and the array's own; the items'
	// own are not kept.
	"#Scalars.list":        {`@openapi(x-list=1)`},
	"#Scalars.list[].item": {`@openapi(x-item=2)`},
	"#Scalars.enum":        {`@openapi(x-enum="e")`},
	"#Scalars.nullable":    {`@openapi(x-nullable=1)`},
	"#Nested.n":            {`@openapi(x-n="n")`},
	// Within allOf.
	"#Composed.inAllOf": {`@openapi(x-all-of=[1,2])`},
	// A property whose label is quoted, of a component under #SchemaMap.
	"#SchemaMap.non-ident.a-b": {`@openapi(x-quoted-label=1)`},
}

// extensions31 is extensions30's counterpart for
// testdata/extract/experiments-3.1.json.
var extensions31 = map[string][]string{
	"#Dependent.a-b": {`@openapi(x-a=1)`},
	// A property of the schema dependentSchemas has for c, which is in a
	// comprehension, not under c.
	"#Dependent.g":  {`@openapi(x-g=true)`},
	"#Consts.one":   {`@openapi(x-c="one")`},
	"#Consts.types": {`@openapi(x-t=["string","null"])`},
	"#Consts.tuple": {`@openapi(x-p={})`},
}

// TestExtractExtensions checks the @openapi attribute Extract writes for each
// property of the fixtures whose schema has x- keywords, and that no other
// field has one: not a property without them, a component schema with them,
// or an array's items with them. A property's attributes are all on one of
// its fields.
func TestExtractExtensions(t *testing.T) {
	cases := map[string]map[string][]string{
		"extensions-3.0.json":  extensions30,
		"experiments-3.1.json": extensions31,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			f := extractFixture(t, name)
			got := extensionAttrs(f)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("@openapi attributes by field (-want +got):\n%s", diff)
			}
			for p, fs := range fieldsByPath(f) {
				with := 0
				for _, fd := range fs {
					if len(fd.Attrs) > 0 {
						with++
					}
				}
				if with > 1 {
					t.Errorf("%s: %d of its fields have attributes, want them on one", p, with)
				}
			}
		})
	}
}

// TestExtractNoExtensions checks that a document whose schemas have no x-
// keywords gets no @openapi attribute.
func TestExtractNoExtensions(t *testing.T) {
	v := cuecontext.New().CompileString(`{
		openapi: "3.0.0"
		info: {title: "plain", version: "1"}
		paths: {}
		components: schemas: Foo: {
			type: "object"
			properties: {
				a: {type: "string", description: "a"}
				b: {type: "array", items: {type: "object", properties: c: type: "integer"}}
			}
		}
	}`)
	f, err := Extract(v, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := extensionAttrs(f); len(got) != 0 {
		t.Errorf("@openapi attributes on a document with no x- keywords: %v", got)
	}
}

// numbersEqual compares two JSON numbers by value, as the attribute holds a
// number as CUE writes it (1.5E-7 for 1.5e-7).
var numbersEqual = cmp.Comparer(func(a, b json.Number) bool {
	x, okx := new(big.Rat).SetString(string(a))
	y, oky := new(big.Rat).SetString(string(b))
	return okx && oky && x.Cmp(y) == 0
})

// fixtureJSON decodes the fixture testdata/extract/<name> as Extensions
// decodes a value, with numbers as json.Number.
func fixtureJSON(t *testing.T, name string) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(readFixture(t, name)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// xKeys returns the x- keywords of the schema at the JSON path at in doc.
func xKeys(t *testing.T, doc any, at ...string) map[string]any {
	t.Helper()
	for _, k := range at {
		switch x := doc.(type) {
		case map[string]any:
			doc = x[k]
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i >= len(x) {
				t.Fatalf("no element %s at %v", k, at)
			}
			doc = x[i]
		default:
			t.Fatalf("no object at %v", at)
		}
	}
	m, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("no object at %v", at)
	}
	x := map[string]any{}
	for k, v := range m {
		if strings.HasPrefix(k, "x-") {
			x[k] = v
		}
	}
	return x
}

// TestExtensionsRoundTrip checks that Extensions reads back, from Extract's
// output formatted and parsed again, exactly the x- keywords of each
// property's schema in the document, from the property's first field, and
// nothing for a property with none. Every field TestExtractExtensions expects
// an attribute on is read back: each has the JSON path of its schema here,
// and one without fails the test.
func TestExtensionsRoundTrip(t *testing.T) {
	props := func(path ...string) []string {
		return append([]string{"components", "schemas"}, path...)
	}
	scalar := func(p string) []string { return props("Scalars", "properties", p) }
	for name, c := range map[string]struct {
		want  map[string][]string
		paths map[string][]string
	}{
		"extensions-3.0.json": {extensions30, map[string][]string{
			"#Scalars.str":              scalar("str"),
			"#Scalars.yes":              scalar("yes"),
			"#Scalars.num":              scalar("num"),
			"#Scalars.int":              scalar("int"),
			"#Scalars.nul":              scalar("nul"),
			"#Scalars.obj":              scalar("obj"),
			"#Scalars.arr":              scalar("arr"),
			"#Scalars.esc":              scalar("esc"),
			"#Scalars.sorted":           scalar("sorted"),
			"#Scalars.odd-keys":         scalar("odd-keys"),
			"#Scalars.required":         scalar("required"),
			"#Scalars.deprecated":       scalar("deprecated"),
			"#Scalars.deprecatedObject": scalar("deprecatedObject"),
			"#Scalars.ref":              scalar("ref"),
			"#Scalars.nested":           scalar("nested"),
			"#Scalars.nested.inner":     props("Scalars", "properties", "nested", "properties", "inner"),
			"#Scalars.list":             scalar("list"),
			"#Scalars.list[].item":      props("Scalars", "properties", "list", "items", "properties", "item"),
			"#Scalars.enum":             scalar("enum"),
			"#Scalars.nullable":         scalar("nullable"),
			"#Nested.n":                 props("Nested", "properties", "n"),
			"#Composed.inAllOf":         props("Composed", "allOf", "1", "properties", "inAllOf"),
			"#SchemaMap.non-ident.a-b":  props("non-ident", "properties", "a-b"),
		}},
		"experiments-3.1.json": {extensions31, map[string][]string{
			"#Dependent.a-b": props("Dependent", "properties", "a-b"),
			"#Dependent.g":   props("Dependent", "dependentSchemas", "c", "properties", "g"),
			"#Consts.one":    props("Consts", "properties", "one"),
			"#Consts.types":  props("Consts", "properties", "types"),
			"#Consts.tuple":  props("Consts", "properties", "tuple"),
		}},
	} {
		t.Run(name, func(t *testing.T) {
			src, err := format.Node(extractFixture(t, name))
			if err != nil {
				t.Fatal(err)
			}
			f, err := parser.ParseFile(name, src, parser.ParseComments)
			if err != nil {
				t.Fatalf("Extract's output does not parse: %v\n%s", err, src)
			}
			fields := fieldsByPath(f)
			doc := fixtureJSON(t, name)
			for path := range c.want {
				at, ok := c.paths[path]
				if !ok {
					t.Errorf("%s has an attribute and no JSON path to read it back against", path)
					continue
				}
				fs := fields[path]
				if len(fs) == 0 {
					t.Errorf("no field %s", path)
					continue
				}
				got, err := Extensions(fs[0])
				if err != nil {
					t.Errorf("%s: %v", path, err)
					continue
				}
				if diff := cmp.Diff(xKeys(t, doc, at...), got, numbersEqual); diff != "" {
					t.Errorf("Extensions of %s (-want +got):\n%s", path, diff)
				}
			}
		})
	}
	t.Run("none", func(t *testing.T) {
		src, err := format.Node(extractFixture(t, "extensions-3.0.json"))
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile("extensions-3.0.json", src)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Extensions(fieldsByPath(f)["#Scalars.plain"][0])
		if err != nil || got != nil {
			t.Errorf("Extensions of a field with no @openapi attribute: %v, %v; want nil, nil", got, err)
		}
	})
}

// oneProperty returns an OpenAPI document whose one schema, #S, is an
// object with one property, p: a string, with the keywords kw.
func oneProperty(kw map[string]any) map[string]any {
	p := map[string]any{"type": "string"}
	maps.Copy(p, kw)
	return map[string]any{
		"openapi": "3.0.0",
		"info":    map[string]any{"title": "t", "version": "1"},
		"paths":   map[string]any{},
		"components": map[string]any{"schemas": map[string]any{"S": map[string]any{
			"type":       "object",
			"properties": map[string]any{"p": p},
		}}},
	}
}

// TestExtensionsEveryRune checks that a key or value holding any of the
// runes JSON and CUE treat specially round-trips: Extract's output parses,
// and Extensions reads back exactly what the document held. CUE's scanner
// refuses U+FEFF anywhere but at the start of a file, and JSON leaves it
// unescaped. Each document is read both as JSON is, through CUE's JSON
// decoder, and as YAML is, encoded from Go values.
func TestExtensionsEveryRune(t *testing.T) {
	var runes []rune
	for r := rune(0); r < 0x80; r++ {
		runes = append(runes, r)
	}
	runes = append(runes, 0x80, 0xa0, 0xad, 0x2028, 0x2029, 0xfeff, 0xfffd, 0xfffe, 0x10ffff)
	for _, r := range runes {
		s := "a" + string(r) + "b"
		doc := oneProperty(map[string]any{"x-v": s, "x-" + s: 1})
		// CUE's JSON decoder refuses a raw U+FEFF, as its scanner does,
		// and reads the escape \ufeff, as a JSON document can hold it.
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		expr, err := cuejson.Extract("doc.json", bytes.ReplaceAll(b, []byte(string(rune(0xfeff))), []byte(`\`+`ufeff`)))
		if err != nil {
			t.Fatalf("U+%04X: %v", r, err)
		}
		ctx := cuecontext.New()
		for from, v := range map[string]cue.Value{"JSON": ctx.BuildExpr(expr), "YAML": ctx.Encode(doc)} {
			if err := v.Err(); err != nil {
				t.Fatalf("U+%04X, read as %s: %v", r, from, err)
			}
			f, err := Extract(v, nil)
			if err != nil {
				t.Errorf("U+%04X, read as %s: %v", r, from, err)
				continue
			}
			src, err := format.Node(f)
			if err != nil {
				t.Errorf("U+%04X, read as %s: %v", r, from, err)
				continue
			}
			pf, err := parser.ParseFile("p.cue", src)
			if err != nil {
				t.Errorf("U+%04X, read as %s: Extract's output does not parse: %v", r, from, err)
				continue
			}
			got, err := Extensions(fieldsByPath(pf)["#S.p"][0])
			if err != nil {
				t.Errorf("U+%04X, read as %s: %v", r, from, err)
				continue
			}
			want := map[string]any{"x-v": s, "x-" + s: json.Number("1")}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("U+%04X, read as %s: Extensions (-want +got):\n%s", r, from, diff)
			}
		}
	}
}

// TestExtractIncompleteExtension checks that an x- keyword whose value isn't
// JSON, as a document written in CUE can hold, is refused on a property,
// whose x- keywords are kept, and ignored anywhere else, as upstream ignores
// it.
func TestExtractIncompleteExtension(t *testing.T) {
	doc := func(component, property string) string {
		return `{
			openapi: "3.0.0"
			info: {title: "t", version: "1"}
			paths: {}
			components: schemas: S: {
				type: "object"
				` + component + `
				properties: p: {type: "string", ` + property + `}
				properties: q: {type: "array", items: {type: "string", ` + component + `}}
			}
		}`
	}
	t.Run("elsewhere", func(t *testing.T) {
		if _, err := Extract(cuecontext.New().CompileString(doc(`"x-big": int`, "")), nil); err != nil {
			t.Errorf("an x- keyword that isn't kept, whose value isn't JSON: %v", err)
		}
	})
	t.Run("on a property", func(t *testing.T) {
		_, err := Extract(cuecontext.New().CompileString(doc("", `"x-big": int`)), nil)
		if err == nil || !strings.Contains(err.Error(), `cannot encode the value of "x-big" as JSON`) {
			t.Errorf("a property's x- keyword whose value isn't JSON: %v; want it refused", err)
		}
	})
	t.Run("every key", func(t *testing.T) {
		_, err := Extract(cuecontext.New().CompileString(doc("", `"x-a": int, "x-b": string`)), nil)
		for _, k := range []string{"x-a", "x-b"} {
			if err == nil || !strings.Contains(cueerrors.Details(err, nil), `cannot encode the value of "`+k+`" as JSON`) {
				t.Errorf("two x- keywords whose values aren't JSON: %s; want %s refused", cueerrors.Details(err, nil), k)
			}
		}
	})
	// MarshalJSON writes bytes as a base64 string, which would be kept as
	// a different value.
	for name, v := range map[string]string{
		"bytes":                    `'hi'`,
		"bytes within":             `{a: [1, 'hi']}`,
		"bytes by default":         `*'abc' | 'def'`,
		"bytes within by default":  `*{a: 'x'} | null`,
		"a list element's default": `[*'q' | "r"]`,
		"a field's default":        `{a: *'x' | 'y'}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Extract(cuecontext.New().CompileString(doc("", `"x-b": `+v)), nil)
			if err == nil || !strings.Contains(err.Error(), `cannot encode the value of "x-b" as JSON: it holds bytes`) {
				t.Errorf("a property's x- keyword %s: %v; want it refused", v, err)
			}
		})
	}
}

// TestExtensionsSurviveCut checks that the @openapi attributes Extract writes
// are in a new lineage's tip, made from Extract's output as init makes it,
// and, once the tip is cut, in both the frozen version and the clean tip.
func TestExtensionsSurviveCut(t *testing.T) {
	ctx := cuecontext.New()
	rt := thema.NewRuntime(ctx)
	tip, err := authoring.NewTip(ctx.BuildFile(extractFixture(t, "extensions-3.0.json")))
	if err != nil {
		t.Fatal(err)
	}
	cut, err := authoring.CutTip(rt, "ext", []authoring.File{tip})
	if err != nil {
		t.Fatal(err)
	}
	if cut.Clean || cut.Version != thema.SV(0, 0) {
		t.Fatalf("cut as %s, clean %v; want 0.0, not clean", cut.Version, cut.Clean)
	}
	want := map[string][]string{}
	for p, attrs := range extensions30 {
		want["Schema."+p] = attrs
	}
	files := map[string][]byte{"the new tip": tip.Text}
	for _, f := range cut.Files {
		files[f.Path] = f.Text
	}
	for _, p := range []string{"the new tip", "history/0.0.thema", authoring.TipFile} {
		t.Run(p, func(t *testing.T) {
			text, ok := files[p]
			if !ok {
				t.Fatalf("the cut has no %s", p)
			}
			f, err := parser.ParseFile(p, text, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(want, extensionAttrs(f)); diff != "" {
				t.Errorf("@openapi attributes by field (-want +got):\n%s", diff)
			}
		})
	}
}

// givenTwice is what Extensions says of a key it reads twice.
const givenTwice = "key x-a is given more than once"

// TestExtensionsRefused checks what Extensions refuses in an @openapi
// attribute that Extract would not have written.
func TestExtensionsRefused(t *testing.T) {
	for name, c := range map[string]struct{ attr, want string }{
		"not an x- key":     {`@openapi(foo=1)`, "key foo is not an x- keyword"},
		"quoted, not x-":    {`@openapi("foo"=1)`, `key "foo" is not an x- keyword`},
		"single-quoted key": {`@openapi('x-a'=1)`, "key 'x-a' is not an x- keyword"},
		"no value":          {`@openapi(x-a)`, "argument x-a is not key=value"},
		"key given twice":   {`@openapi(x-a=1, x-a=2)`, givenTwice},
		"twice across two":  {`@openapi(x-a=1) @openapi(x-a=2)`, givenTwice},
		"the same twice":    {`@openapi(x-a=1) @openapi(x-a=1)`, givenTwice},
		"not JSON":          {`@openapi(x-a=b)`, "the value of x-a: invalid character"},
		"two JSON values":   {`@openapi(x-a=1 2)`, "the value of x-a: 1 2 is not one JSON value"},
		// \a is an escape of CUE's, not of JSON's.
		"quoted key escaped": {`@openapi("x-\a"=1)`, `key "x-\a" is not a JSON string`},
	} {
		t.Run(name, func(t *testing.T) {
			f, err := parser.ParseFile(name, "a: _ "+c.attr)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Extensions(f.Decls[0].(*ast.Field))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Extensions of %s: %v, %v; want an error with %q", c.attr, got, err, c.want)
			}
		})
	}
}

// TestExtensionsMerged checks that Extensions reads every @openapi attribute
// on a field, beside attributes of other names, and leaves numbers as their
// JSON text.
func TestExtensionsMerged(t *testing.T) {
	f, err := parser.ParseFile("merged", `a: _ @openapi(x-a=1.50) @deprecated() @openapi("x-b.c"={"d":[2]})`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Extensions(f.Decls[0].(*ast.Field))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"x-a":   json.Number("1.50"),
		"x-b.c": map[string]any{"d": []any{json.Number("2")}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Extensions (-want +got):\n%s", diff)
	}
}

// TestGenerateIgnoresExtensions checks that GenerateSchema, which runs
// upstream's generator, writes the same document for a lineage whose fields
// carry @openapi attributes as for the same lineage without them. Upstream's
// generator reads @openapi too, as a rename of a top-level field; a lineage's
// fields are never at its top level, so today the two don't meet.
func TestGenerateIgnoresExtensions(t *testing.T) {
	const doc = `{
		openapi: "3.0.0"
		info: {title: "t", version: "1"}
		paths: {}
		components: schemas: Ship: {
			type: "object"
			required: ["shipRegistryId"]
			properties: {
				shipRegistryId: {type: "string", "x-property-name": "ship-registry-id"}
				hull: {type: "object", "x-h": 1, properties: code: {type: "string", "x-c": [1]}}
				crew: {type: "array", items: {type: "object", properties: name: {type: "string", "x-n": null}}}
			}
		}
	}`
	generate := func(strip bool) string {
		ctx := cuecontext.New()
		f, err := Extract(ctx.CompileString(doc), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(extensionAttrs(f)); got != 4 && !strip {
			t.Fatalf("Extract wrote %d fields with @openapi attributes, want 4", got)
		}
		if strip {
			tastutil.StripAttrs(f, jsonschema.ExtensionsAttr)
		}
		tip, err := authoring.NewTip(ctx.BuildFile(f))
		if err != nil {
			t.Fatal(err)
		}
		c, err := authoring.CompileDevelopment(ctx, "ext", []authoring.File{tip})
		if err != nil {
			t.Fatal(err)
		}
		lin, err := authoring.BindDevelopment(thema.NewRuntime(ctx), c)
		if err != nil {
			t.Fatal(err)
		}
		out, err := GenerateSchema(lin.Latest(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return string(formatFile(t, out))
	}
	with, without := generate(false), generate(true)
	if with != without {
		t.Errorf("with @openapi attributes, GenerateSchema writes:\n%s\nwithout them:\n%s", with, without)
	}
	if !strings.Contains(with, "shipRegistryId") {
		t.Errorf("GenerateSchema's document has no shipRegistryId, so compares nothing:\n%s", with)
	}
}
