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

// extension is one x- keyword of a schema, with its value.
type extension struct {
	key   string
	value cue.Value
}

// addExtension records the x- keyword key, whose value is n. It's encoded
// only if the schema is a property's, by extensionsAttr: every other
// schema's x- keywords are dropped, as upstream drops them, whatever their
// values.
func (s *state) addExtension(key string, n cue.Value) {
	s.extensions = append(s.extensions, extension{key, n})
}

// rawKey matches the keys an attribute holds as written: words of letters,
// digits and _, each starting with a letter or _, joined by -. Every other
// key is written as a string literal, as some, such as x-1e or x-a//b, do
// not scan as an attribute's tokens.
var rawKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(-[A-Za-z_][A-Za-z0-9_]*)*$`)

// extensionsAttr returns the attribute that holds the property schema's x-
// keywords, or nil if it has none: @openapi(key=value, ...), with the keys
// sorted and each value its compact JSON. A key matching rawKey is written
// as it is, and any other as a JSON string. A value that isn't JSON, which
// a document written in CUE can hold, is an error, reported on s, the
// state of the schema with the property.
func (si schemaInfo) extensionsAttr(s *state) *ast.Attribute {
	if len(si.extensions) == 0 {
		return nil
	}
	exts := slices.SortedFunc(slices.Values(si.extensions), func(a, b extension) int {
		return cmp.Compare(a.key, b.key)
	})
	var b strings.Builder
	b.WriteString("@" + ExtensionsAttr + "(")
	for i, e := range exts {
		// MarshalJSON writes compact JSON, keeping the order of an
		// object's keys, and does not escape HTML.
		v, err := e.value.MarshalJSON()
		if err != nil {
			s.errf(e.value, "cannot encode the value of %q as JSON: %v", e.key, err)
			return nil
		}
		if i > 0 {
			b.WriteString(", ")
		}
		if rawKey.MatchString(e.key) {
			b.WriteString(e.key)
		} else {
			b.Write(jsonString(e.key))
		}
		b.WriteByte('=')
		b.Write(escapeBOM(v))
	}
	b.WriteByte(')')
	return &ast.Attribute{Text: b.String()}
}

// jsonString returns s as a JSON string, without escaping HTML. It is a CUE
// string too: JSON writes a \ as \\, so it holds no interpolation, which an
// attribute may not hold, and escapeBOM escapes the one rune JSON leaves
// that CUE's scanner refuses.
func jsonString(s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Encoding a string cannot fail.
	_ = enc.Encode(s)
	return escapeBOM(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}

// bom is U+FEFF in UTF-8.
var bom = []byte{0xEF, 0xBB, 0xBF}

// escapeBOM escapes each U+FEFF in the JSON b, which JSON leaves as it is
// and CUE's scanner refuses anywhere but at the start of a file. In JSON it
// can only be inside a string, where \ufeff is the same string.
func escapeBOM(b []byte) []byte {
	return bytes.ReplaceAll(b, bom, []byte(`\ufeff`))
}
