package thema

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema/diag"
)

// A lineage's text compiled on its own and filled into a value that narrows
// its joinSchema binds, as it does when only its schemas are filled in. The
// lineage is placed at the path it was bound from, whichever conjunct the
// rebuilt value takes its path from (#260).
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
		t.Errorf("text filled into the use: %v", err)
	}

	same := pkg.FillPath(cue.ParsePath("h.schemas"), text.LookupPath(cue.ParsePath("schemas"))).LookupPath(cue.ParsePath("h"))
	if _, err := BindLineage(same, rt); err != nil {
		t.Errorf("schemas filled into the use: %v", err)
	}
}

// Filled into the value that uses it, the lineage's text is held to the
// reference rules as it is bound alone: a narrowing may refer beside the use,
// not into the lineage's schemas, and the text may not refer to the lineage's
// name. The use is loaded as a package, or compiled from a string with or
// without a file name, as a tool assembling lineages might build it.
func TestFilledLineageReferences(t *testing.T) {
	const (
		text     = `schemas: "0": "0": schema: {a?: string}`
		textName = "name: \"h\"\nschemas: \"0\": \"0\": schema: {a?: string, n?: name}"
		beside   = "#J: {[string]: string, ...}\n#Op: h: thema.Lineage & {name: \"h\", joinSchema: #J}\nlin: #Op"
		into     = "#Op: h: thema.Lineage & {name: \"h\", joinSchema: {a?: h.schemas[\"0\"][\"0\"].schema.a, ...}}\nlin: #Op"
		useNamed = "#J: {[string]: string, ...}\nh: {name: \"h\", joinSchema: #J}"
	)
	// ok is no diagnostic.
	const ok diag.Code = ""

	for name, c := range map[string]struct {
		pkg, text string
		want      diag.Code
	}{
		"narrowing refers beside the use":   {beside, text, ok},
		"narrowing refers into the schemas": {into, text, diag.E0140},
		"text refers to the lineage's name": {"#J: {[string]: string, ...}\nlin: h: thema.Lineage & {name: \"h\", joinSchema: #J}", textName, diag.E0141},
	} {
		t.Run("package/"+name, func(t *testing.T) {
			ctx := cuecontext.New()
			pkg := loadLineageValue(t, ctx, c.pkg)
			lin := pkg.FillPath(cue.ParsePath("h"), ctx.CompileString(c.text)).LookupPath(cue.ParsePath("h"))
			checkCode(t, ctx, lin, c.want)
		})
	}

	for name, opts := range map[string]struct{ use, text []cue.BuildOption }{
		"unnamed":    {},
		"use named":  {use: []cue.BuildOption{cue.Filename("use.cue")}},
		"both named": {use: []cue.BuildOption{cue.Filename("use.cue")}, text: []cue.BuildOption{cue.Filename("tip.cue")}},
	} {
		t.Run("string/"+name, func(t *testing.T) {
			ctx := cuecontext.New()
			rt := NewRuntime(ctx)
			use := ctx.CompileString(useNamed, opts.use...).FillPath(cue.ParsePath("h"), rt.linDef())
			lin := use.FillPath(cue.ParsePath("h"), ctx.CompileString(text, opts.text...)).LookupPath(cue.ParsePath("h"))
			checkCode(t, ctx, lin, ok)
		})
	}
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
