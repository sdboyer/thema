package jsonschema

// This file is thema's, not upstream's: it keeps a property's x- keywords,
// which upstream skips, as an attribute on the property's field.

import (
	"bytes"
	"cmp"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
)

// ExtensionsAttr is the name of the attribute that holds a property's x-
// keywords: @openapi(x-a=1, x-b="b").
const ExtensionsAttr = "openapi"

// extension is one x- keyword of a schema, with its value as compact JSON.
type extension struct {
	key   string
	value []byte
}

// addExtension records the x- keyword key, whose value is n.
func (s *state) addExtension(key string, n cue.Value) {
	// MarshalJSON writes compact JSON, keeping the order of an object's
	// keys, and does not escape HTML.
	b, err := n.MarshalJSON()
	if err != nil {
		s.errf(n, "cannot encode the value of %q as JSON: %v", key, err)
		return
	}
	s.extensions = append(s.extensions, extension{key, b})
}

// rawKey matches the keys an attribute holds as written: words of letters,
// digits and _, each starting with a letter or _, joined by -. Every other
// key is written as a string literal, as some, such as x-1e or x-a//b, do
// not scan as an attribute's tokens.
var rawKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(-[A-Za-z_][A-Za-z0-9_]*)*$`)

// extensionsAttr returns the attribute that holds the schema's x- keywords,
// or nil if it has none: @openapi(key=value, ...), with the keys sorted and
// each value its compact JSON, as written. A key matching rawKey is written
// as it is, and any other as a JSON string.
func (s schemaInfo) extensionsAttr() *ast.Attribute {
	if len(s.extensions) == 0 {
		return nil
	}
	exts := slices.SortedFunc(slices.Values(s.extensions), func(a, b extension) int {
		return cmp.Compare(a.key, b.key)
	})
	var b strings.Builder
	b.WriteString("@" + ExtensionsAttr + "(")
	for i, e := range exts {
		if i > 0 {
			b.WriteString(", ")
		}
		if rawKey.MatchString(e.key) {
			b.WriteString(e.key)
		} else {
			b.Write(jsonString(e.key))
		}
		b.WriteByte('=')
		b.Write(e.value)
	}
	b.WriteByte(')')
	return &ast.Attribute{Text: b.String()}
}

// jsonString returns s as a JSON string, without escaping HTML. It is a CUE
// string too: JSON writes a \ as \\, so it holds no interpolation, which an
// attribute may not hold.
func jsonString(s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Encoding a string cannot fail.
	_ = enc.Encode(s)
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}
