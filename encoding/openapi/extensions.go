package openapi

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"github.com/sdboyer/thema/internal/jsonschema"
)

// Extensions returns the x- keywords that [Extract] kept from the schema of
// the property f is the field of, keyed by name, or nil if f has no @openapi
// attribute.
//
// Extract writes them as one attribute on the property's first field, after
// any other; a deprecated struct's property has two fields, and this is the
// one with @deprecated():
//
//	shipRegistryId?: string @openapi(x-property-name="ship-registry-id", x-sensitive=true)
//
// The attribute's arguments are key=value, separated by ", ", with the keys
// sorted by their bytes. A key is written as it is when it is words of ASCII
// letters, digits and _, each word starting with a letter or _, joined by -,
// as x-sensitive and x-property-name are; any other key, such as x-1e or
// x-a.b, is written as a JSON string ("x-a.b"). A value is the keyword's value
// as compact JSON: an object's keys in the document's order, <, > and & not
// escaped, and a number as CUE writes it, which may differ in form but not
// in value from the document's (1.5E-7 for 1.5e-7). JSON writes a \ as \\, so
// neither a key nor a value holds an interpolation, which an attribute may
// not.
//
// Each value is decoded from its JSON as [encoding/json] decodes into an any,
// except that a number is a [json.Number], holding the number's JSON text, so
// that no precision is lost. Every @openapi attribute on f is read. A key that
// is not an x- keyword, an argument without a value, a key given twice, or a
// value that is not one JSON value is an error.
func Extensions(f *ast.Field) (map[string]any, error) {
	var exts map[string]any
	// The arguments are split as CUE splits them, by the attribute's own
	// tokens, so each attribute is read on a field of a value of CUE's.
	// Each is read on a field of its own, as CUE drops a field's second
	// attribute of the same text.
	ctx := cuecontext.New()
	for _, at := range f.Attrs {
		if at.Name() != jsonschema.ExtensionsAttr {
			continue
		}
		v := ctx.BuildExpr(ast.NewStruct(&ast.Field{
			Label: ast.NewIdent("f"),
			Value: ast.NewIdent("_"),
			Attrs: []*ast.Attribute{{Text: at.Text}},
		}))
		if err := v.Err(); err != nil {
			return nil, err
		}
		a := v.LookupPath(cue.MakePath(cue.Str("f"))).Attribute(jsonschema.ExtensionsAttr)
		if err := a.Err(); err != nil {
			return nil, fmt.Errorf("@%s(%s): %w", a.Name(), a.Contents(), err)
		}
		if exts == nil {
			exts = map[string]any{}
		}
		for arg := range a.Args(0) {
			key := arg.Key
			if strings.HasPrefix(key, `"`) {
				if err := json.Unmarshal([]byte(key), &key); err != nil {
					return nil, fmt.Errorf("@%s: key %s is not a JSON string: %w", a.Name(), arg.Key, err)
				}
			}
			switch {
			case arg.Key == "":
				return nil, fmt.Errorf("@%s: argument %s is not key=value", a.Name(), arg.Value)
			case !strings.HasPrefix(key, "x-"):
				return nil, fmt.Errorf("@%s: key %s is not an x- keyword", a.Name(), arg.Key)
			}
			if _, ok := exts[key]; ok {
				return nil, fmt.Errorf("@%s: key %s is given more than once", a.Name(), key)
			}
			val, err := decodeJSON(arg.Value)
			if err != nil {
				return nil, fmt.Errorf("@%s: the value of %s: %w", a.Name(), key, err)
			}
			exts[key] = val
		}
	}
	return exts, nil
}

// decodeJSON decodes s, which must be exactly one JSON value, keeping numbers
// as json.Number.
func decodeJSON(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%s is not one JSON value", s)
	}
	return v, nil
}
