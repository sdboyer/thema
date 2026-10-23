package cuetil

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
)

// TestHiddenFieldSemantics pins how CUE treats hidden fields, which thema's
// checks of lens results depend on. In short: closedness and pattern
// constraints don't apply to a hidden label itself, but do apply beneath a
// hidden field the schema declares; hidden fields are evaluated, so their errors
// are errors of the whole value; and they reach regular fields only through
// references and embedding.
func TestHiddenFieldSemantics(t *testing.T) {
	ctx := cuecontext.New()
	for name, c := range map[string]struct {
		src     string
		errText string // "" if the value is valid
	}{
		// Closedness and patterns don't apply to a hidden label.
		"undeclared hidden in definition":               {`#C: {a: int}, x: #C & {a: 1, _h: 2}`, ""},
		"undeclared hidden in close()":                  {`x: close({a: 1}) & {_h: 2}`, ""},
		"pattern does not match hidden":                 {`#P: {[string]: int}, x: #P & {y: 1, _h: "str"}`, ""},
		"undeclared hidden struct beneath a definition": {`#C: {a: int}, x: #C & {a: 1, _h: {extra: 2}}`, ""},

		// Beneath a hidden field the schema declares, closedness applies.
		"declared hidden, extra field":          {`#T: {a: int, _h: {k: int}}, x: #T & {a: 1, _h: {k: 1, extra: 2}}`, "_h.extra: field not allowed"},
		"declared optional hidden, extra field": {`#S: {a: int, _h?: {k: int}}, x: #S & {a: 1, _h: {k: 1, extra: 2}}`, "_h.extra: field not allowed"},
		"declared hidden, conflicting type":     {`#T: {a: int, _h: {k: int}}, x: #T & {a: 1, _h: {k: "s"}}`, "_h.k: conflicting values"},

		// Hidden fields are evaluated: their errors are errors of the value.
		"conflict in hidden":                      {`x: {a: 1, _h: 1 & 2}`, "_h: conflicting values"},
		"conflict beneath hidden":                 {`x: {a: 1, _h: {k: 1 & 2}}`, "_h.k: conflicting values"},
		"hidden conflicting with a regular":       {`x: {a: 1, _h: a & "s"}`, "_h: conflicting values"},
		"hidden refers to a regular, no conflict": {`x: {a: int, _h: a + 1, b: _h}`, ""},

		// Regular fields reach hidden ones only through references and embedding.
		"embedded hidden surfaces its fields": {`x: {_h: {extra: 1}, _h} & close({})`, "extra: field not allowed"},
		"referenced hidden":                   {`#Y: {y: {}}, x: {_h: {extra: 1}, y: _h} & #Y`, "y.extra: field not allowed"},
	} {
		t.Run(name, func(t *testing.T) {
			x := ctx.CompileString(c.src).LookupPath(cue.MakePath(cue.Str("x")))
			err := x.Validate()
			switch {
			case c.errText == "" && err != nil:
				t.Fatalf("unexpected error: %s", cerrors.Details(err, nil))
			case c.errText != "" && err == nil:
				t.Fatalf("valid, want an error mentioning %q", c.errText)
			case c.errText != "" && !strings.Contains(cerrors.Details(err, nil), c.errText):
				t.Fatalf("error does not mention %q: %s", c.errText, cerrors.Details(err, nil))
			}
		})
	}
}

// Hidden fields are excluded from Fields() unless asked for, and a hidden
// label is scoped to its package: cue.Str("_h") names a regular field spelled
// _h, not the hidden field, and DeclaresPath does not yet know hidden selectors.
func TestHiddenFieldSelectors(t *testing.T) {
	ctx := cuecontext.New()
	v := ctx.CompileString(`{a: 1, _h: {k: int}}`)

	var regular, all []string
	iter, _ := v.Fields()
	for iter.Next() {
		regular = append(regular, iter.Selector().String())
	}
	iter, _ = v.Fields(cue.Hidden(true))
	for iter.Next() {
		all = append(all, iter.Selector().String())
	}
	if got := strings.Join(regular, ","); got != "a" {
		t.Errorf("Fields(): got %s, want a", got)
	}
	if got := strings.Join(all, ","); got != "a,_h" {
		t.Errorf("Fields(cue.Hidden(true)): got %s, want a,_h", got)
	}

	if v.LookupPath(cue.MakePath(cue.Str("_h"))).Exists() {
		t.Error(`cue.Str("_h") found the hidden field`)
	}
	if !v.LookupPath(cue.MakePath(cue.Hid("_h", "_"))).Exists() {
		t.Error(`cue.Hid("_h", "_") did not find the hidden field of an anonymous package`)
	}
}
