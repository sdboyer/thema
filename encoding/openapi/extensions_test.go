package openapi

import (
	"bytes"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	"github.com/google/go-cmp/cmp"
	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
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
				if a.Name() == "openapi" {
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
	// Beside @deprecated(); a struct's goes on the field with the struct,
	// not on the second field @deprecated() is on.
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

// TestExtractExtensions checks the @openapi attribute Extract writes for each
// property of the fixtures whose schema has x- keywords, and that no other
// field has one: not a property without them, a component schema with them,
// or an array's items with them.
func TestExtractExtensions(t *testing.T) {
	cases := map[string]map[string][]string{
		"extensions-3.0.json": extensions30,
		"experiments-3.1.json": {
			"#Dependent.a-b": {`@openapi(x-a=1)`},
			// A property of the schema dependentSchemas has for c, which
			// is in a comprehension, not under c.
			"#Dependent.g":  {`@openapi(x-g=true)`},
			"#Consts.one":   {`@openapi(x-c="one")`},
			"#Consts.types": {`@openapi(x-t=["string","null"])`},
			"#Consts.tuple": {`@openapi(x-p={})`},
		},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			got := extensionAttrs(extractFixture(t, name))
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("@openapi attributes by field (-want +got):\n%s", diff)
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
// property's schema in the document, and nothing for a property with none.
func TestExtensionsRoundTrip(t *testing.T) {
	const name = "extensions-3.0.json"
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
	props := func(path ...string) []string {
		return append([]string{"components", "schemas"}, path...)
	}
	cases := map[string][]string{
		"#Scalars.str":              props("Scalars", "properties", "str"),
		"#Scalars.yes":              props("Scalars", "properties", "yes"),
		"#Scalars.num":              props("Scalars", "properties", "num"),
		"#Scalars.int":              props("Scalars", "properties", "int"),
		"#Scalars.nul":              props("Scalars", "properties", "nul"),
		"#Scalars.obj":              props("Scalars", "properties", "obj"),
		"#Scalars.arr":              props("Scalars", "properties", "arr"),
		"#Scalars.esc":              props("Scalars", "properties", "esc"),
		"#Scalars.sorted":           props("Scalars", "properties", "sorted"),
		"#Scalars.odd-keys":         props("Scalars", "properties", "odd-keys"),
		"#Scalars.ref":              props("Scalars", "properties", "ref"),
		"#Scalars.nested.inner":     props("Scalars", "properties", "nested", "properties", "inner"),
		"#Scalars.list[].item":      props("Scalars", "properties", "list", "items", "properties", "item"),
		"#Composed.inAllOf":         props("Composed", "allOf", "1", "properties", "inAllOf"),
		"#SchemaMap.non-ident.a-b":  props("non-ident", "properties", "a-b"),
		"#Scalars.deprecatedObject": props("Scalars", "properties", "deprecatedObject"),
	}
	for path, at := range cases {
		t.Run(path, func(t *testing.T) {
			fs := fields[path]
			if len(fs) == 0 {
				t.Fatalf("no field %s", path)
			}
			// The field with the attribute is the last of a label two
			// fields share.
			got, err := Extensions(fs[len(fs)-1])
			if err != nil {
				t.Fatal(err)
			}
			want := xKeys(t, doc, at...)
			if diff := cmp.Diff(want, got, numbersEqual); diff != "" {
				t.Errorf("Extensions (-want +got):\n%s", diff)
			}
		})
	}
	t.Run("none", func(t *testing.T) {
		got, err := Extensions(fields["#Scalars.plain"][0])
		if err != nil || got != nil {
			t.Errorf("Extensions of a field with no @openapi attribute: %v, %v; want nil, nil", got, err)
		}
	})
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

// TestExtensionsRefused checks what Extensions refuses in an @openapi
// attribute that Extract would not have written.
func TestExtensionsRefused(t *testing.T) {
	for name, c := range map[string]struct{ attr, want string }{
		"not an x- key":     {`@openapi(foo=1)`, "key foo is not an x- keyword"},
		"quoted, not x-":    {`@openapi("foo"=1)`, `key "foo" is not an x- keyword`},
		"single-quoted key": {`@openapi('x-a'=1)`, "key 'x-a' is not an x- keyword"},
		"no value":          {`@openapi(x-a)`, "argument x-a is not key=value"},
		"key given twice":   {`@openapi(x-a=1, x-a=2)`, "key x-a is given more than once"},
		"twice across two":  {`@openapi(x-a=1) @openapi(x-a=2)`, "key x-a is given more than once"},
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
