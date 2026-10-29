package compile

import (
	"fmt"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// TestLookupPathFieldForms pins what cue.Value.LookupPath finds for each form
// of field, with each form of selector. The compiler looks schema paths up to
// learn what a version declares, so the selector it uses decides which fields
// it can see at all: a plain selector (cue.Str, cue.ParsePath) finds only
// regular fields, not optional or required ones, while an optional selector
// finds all three. The compiler's lookup therefore uses optional selectors.
func TestLookupPathFieldForms(t *testing.T) {
	v := cuecontext.New().CompileString(`
regular:   int
optional?: int
required!: int
nested: {
	optional?: int
}
`)
	sels := []struct {
		name string
		sel  func(string) cue.Selector
	}{
		{"plain", cue.Str},
		{"optional", func(s string) cue.Selector { return cue.Str(s).Optional() }},
		{"required", func(s string) cue.Selector { return cue.Str(s).Required() }},
	}
	fields := []string{"regular", "optional", "required"}
	var b strings.Builder
	for _, s := range sels {
		for _, f := range fields {
			fmt.Fprintf(&b, "%s selector, %s field: exists=%v\n", s.name, f, v.LookupPath(cue.MakePath(s.sel(f))).Exists())
		}
		fmt.Fprintf(&b, "%s selector, nested.optional: exists=%v\n", s.name, v.LookupPath(cue.MakePath(cue.Str("nested"), s.sel(fields[1]))).Exists())
	}
	want := `plain selector, regular field: exists=true
plain selector, optional field: exists=false
plain selector, required field: exists=false
plain selector, nested.optional: exists=false
optional selector, regular field: exists=true
optional selector, optional field: exists=true
optional selector, required field: exists=true
optional selector, nested.optional: exists=true
required selector, regular field: exists=true
required selector, optional field: exists=true
required selector, required field: exists=true
required selector, nested.optional: exists=true
`
	if got := b.String(); got != want {
		t.Errorf("LookupPath:\n%s\nwant:\n%s", got, want)
	}
}

// TestLookupOpenStruct pins that LookupPath finds any name in an open struct,
// as _, while lookup finds only the names the struct declares: a name its ...
// merely admits is not declared (#158, #159).
func TestLookupOpenStruct(t *testing.T) {
	v := cuecontext.New().CompileString(`{
	declared?: int
	...
}`)
	admitted := cue.MakePath(cue.Str("admitted").Optional())
	if got := v.LookupPath(admitted); !got.Exists() || got.IncompleteKind() != cue.TopKind {
		t.Errorf("LookupPath of a name only ... admits: exists=%v kind=%v, want an existing _", got.Exists(), got.IncompleteKind())
	}
	if lookup(v, cue.ParsePath("admitted")).Exists() {
		t.Error("lookup found a name only ... admits")
	}
	if !lookup(v, cue.ParsePath("declared")).Exists() {
		t.Error("lookup did not find a declared optional field")
	}
}
