package cuetil

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

func TestCloseRecursive(t *testing.T) {
	ctx := cuecontext.New()
	open := ctx.CompileString(`{a: int, nested: {b: int}}`)
	closed := CloseRecursive(ctx, open)

	for name, data := range map[string]string{
		"top":    `{a: 1, nested: {b: 2}, extra: 1}`,
		"nested": `{a: 1, nested: {b: 2, extra: 1}}`,
	} {
		d := ctx.CompileString(data)
		if err := closed.Unify(d).Validate(); err == nil {
			t.Errorf("%s: closed value accepted an extra field", name)
		}
		if err := open.Unify(d).Validate(); err != nil {
			t.Errorf("%s: original value was closed by CloseRecursive: %v", name, err)
		}
	}
	if err := closed.Unify(ctx.CompileString(`{a: 1, nested: {b: 2}}`)).Validate(cue.Concrete(true)); err != nil {
		t.Errorf("closed value rejected valid data: %v", err)
	}
}
