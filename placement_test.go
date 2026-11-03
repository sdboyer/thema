package thema

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema/diag"
)

// A lineage compiled in a root of its own and filled whole into a value that
// narrows its joinSchema is placed where it is used and where its text was
// compiled, whichever conjunct comes first (#260). Filling only its schemas,
// which then belong to the use's root, is the control.
func TestFilledLineage(t *testing.T) {
	ctx := cuecontext.New()
	rt := NewRuntime(ctx)
	pkg := loadLineageValue(t, ctx, `
#J: {[string]: string, ...}
#Op: h: thema.Lineage & {
	name:       "h"
	joinSchema: #J
}
lin: #Op
`)
	text := ctx.CompileString(`schemas: "0": "0": schema: {a?: string}`)

	h := pkg.FillPath(cue.ParsePath("h"), text).LookupPath(cue.ParsePath("h"))
	if _, err := BindLineage(h, rt); err != nil {
		t.Errorf("filled from another root: %v", err)
	}

	same := pkg.FillPath(cue.ParsePath("h.schemas"), text.LookupPath(cue.ParsePath("schemas"))).LookupPath(cue.ParsePath("h"))
	if _, err := BindLineage(same, rt); err != nil {
		t.Errorf("schemas filled: %v", err)
	}
}

// The reference rules hold of a lineage filled whole from a root of its own
// as they hold of the lineage bound where it is written, whichever conjunct
// comes first: a narrowing may refer out of the lineage, not into its
// schemas, and the lineage's text may not refer to its own name (#260).
func TestFilledLineageReferences(t *testing.T) {
	const (
		text     = `schemas: "0": "0": schema: {a?: string}`
		textMeta = "name: \"h\"\nschemas: \"0\": \"0\": schema: {a?: string, n?: name}"
	)
	// ok is no diagnostic.
	const ok diag.Code = ""

	// A narrowing at the use, which every order of assembly can take.
	use := map[string]struct {
		pkg, text string
		want      diag.Code
	}{
		"narrowing refers beside the use": {
			pkg:  "#J: {[string]: string, ...}\nlin: h: thema.Lineage & {name: \"h\", joinSchema: #J}",
			text: text, want: ok,
		},
		"narrowing refers into the schemas": {
			pkg:  "lin: h: thema.Lineage & {name: \"h\", joinSchema: {a?: lin.h.schemas[\"0\"][\"0\"].schema.a, ...}}",
			text: text, want: diag.E0140,
		},
		"text refers to its name": {
			pkg:  "#J: {[string]: string, ...}\nlin: h: thema.Lineage & {name: \"h\", joinSchema: #J}",
			text: textMeta, want: diag.E0141,
		},
	}
	assemble := map[string]func(ctx *cue.Context, pkg, text cue.Value) cue.Value{
		"text filled into the use": func(_ *cue.Context, pkg, text cue.Value) cue.Value {
			return pkg.FillPath(cue.ParsePath("h"), text).LookupPath(cue.ParsePath("h"))
		},
		"use filled into the text": func(_ *cue.Context, pkg, text cue.Value) cue.Value {
			return text.FillPath(cue.Path{}, pkg.LookupPath(cue.ParsePath("h")))
		},
		"both filled into a third root": func(ctx *cue.Context, pkg, text cue.Value) cue.Value {
			x := cue.ParsePath("x")
			return ctx.CompileString("x: _").FillPath(x, text).FillPath(x, pkg.LookupPath(cue.ParsePath("h"))).LookupPath(x)
		},
		"use unified with the text": func(_ *cue.Context, pkg, text cue.Value) cue.Value {
			return pkg.LookupPath(cue.ParsePath("h")).Unify(text)
		},
		"text unified with the use": func(_ *cue.Context, pkg, text cue.Value) cue.Value {
			return text.Unify(pkg.LookupPath(cue.ParsePath("h")))
		},
	}
	for name, c := range use {
		for how, fn := range assemble {
			t.Run(name+"/"+how, func(t *testing.T) {
				ctx := cuecontext.New()
				lin := fn(ctx, loadLineageValue(t, ctx, c.pkg), ctx.CompileString(c.text))
				checkCode(t, ctx, lin, c.want)
			})
		}
	}

	// A framework's definition narrows the lineage, as in #238; only filling
	// the text where the definition is used keeps the definition's
	// closedness out of the text.
	def := map[string]struct {
		pkg, text string
		want      diag.Code
	}{
		"definition refers beside it": {
			pkg:  "#J: {[string]: string, ...}\n#Op: h: thema.Lineage & {name: \"h\", joinSchema: #J}\nlin: #Op",
			text: text, want: ok,
		},
		"definition refers into the schemas": {
			pkg:  "#Op: h: thema.Lineage & {name: \"h\", joinSchema: {a?: h.schemas[\"0\"][\"0\"].schema.a, ...}}\nlin: #Op",
			text: text, want: diag.E0140,
		},
		"text refers to its name": {
			pkg:  "#J: {[string]: string, ...}\n#Op: h: thema.Lineage & {name: \"h\", joinSchema: #J}\nlin: #Op",
			text: textMeta, want: diag.E0141,
		},
	}
	for name, c := range def {
		t.Run("definition/"+name, func(t *testing.T) {
			ctx := cuecontext.New()
			lin := assemble["text filled into the use"](ctx, loadLineageValue(t, ctx, c.pkg), ctx.CompileString(c.text))
			checkCode(t, ctx, lin, c.want)
		})
	}

	// The text bound alone reports what it reports filled.
	ctx := cuecontext.New()
	checkCode(t, ctx, ctx.CompileString(textMeta), diag.E0141)
}

// checkCode binds lin and requires the one diagnostic want, or none.
func checkCode(t *testing.T, ctx *cue.Context, lin cue.Value, want diag.Code) {
	t.Helper()
	_, err := BindLineage(lin, NewRuntime(ctx))
	ds := diag.Diagnostics(err)
	switch {
	case want == "" && err != nil:
		t.Fatalf("want it bound, got: %v", err)
	case want == "":
	case len(ds) != 1 || ds[0].Code != want:
		t.Fatalf("want one %s, got: %v", want, err)
	}
}
