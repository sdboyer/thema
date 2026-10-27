package jsonschema

import (
	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cuejson "cuelang.org/go/encoding/json"
	cuejsonschema "cuelang.org/go/encoding/jsonschema"
	"github.com/sdboyer/thema"
)

// GenerateSchema generates a JSON Schema (draft 2020-12) of the provided Thema
// schema, unified with its lineage's joinSchema. Definitions the schema refers
// to are under $defs.
//
// CUE's generator emits no defaults, so GenerateSchema adds them: a field, list
// element or pattern value whose CUE value has a default gets a "default"
// keyword.
func GenerateSchema(sch thema.Schema) (*ast.File, error) {
	v, err := standalone(sch)
	if err != nil {
		return nil, err
	}

	defs := make(map[string]cue.Value)
	x, err := cuejsonschema.Generate(v, &cuejsonschema.GenerateConfig{
		NamesFunc: func(refs []*cuejsonschema.CUERef) {
			cuejsonschema.DefaultNamesFunc(refs)
			for _, r := range refs {
				defs[r.Name] = r.Inst.LookupPath(r.Path)
			}
		},
	})
	if err != nil {
		return nil, err
	}

	root := x.(*ast.StructLit)
	if err := addDefaults(v, root); err != nil {
		return nil, err
	}
	if ds, ok := field(root, "$defs").(*ast.StructLit); ok {
		for _, el := range ds.Elts {
			f := el.(*ast.Field)
			name, _, _ := ast.LabelName(f.Label)
			if d, ok := f.Value.(*ast.StructLit); ok {
				if err := addDefaults(defs[name], d); err != nil {
					return nil, err
				}
			}
		}
	}
	return &ast.File{Decls: []ast.Decl{root}}, nil
}

// standalone returns the schema, unified with the lineage's joinSchema, as a
// value of its own.
//
// CUE's generator resolves a reference by looking its path up from the root
// the reference reports. Within a bound lineage, that root is thema's Lineage,
// where the path to a schema's own definitions does not exist. Rebuilding the
// schema from its syntax makes it its own root.
func standalone(sch thema.Schema) (cue.Value, error) {
	ctx := sch.Lineage().Runtime().Context()
	v, err := rebuild(ctx, sch.Underlying().LookupPath(cue.MakePath(cue.Str("schema"))))
	if err != nil {
		return v, err
	}
	join := sch.Lineage().Underlying().LookupPath(cue.MakePath(cue.Str("joinSchema")))
	if !join.Exists() || join.IncompleteKind() == cue.TopKind {
		return v, nil
	}
	j, err := rebuild(ctx, join)
	if err != nil {
		return j, err
	}
	return j.Unify(v), nil
}

func rebuild(ctx *cue.Context, v cue.Value) (cue.Value, error) {
	var b cue.Value
	switch n := v.Syntax(cue.Definitions(true), cue.Docs(true), cue.Optional(true)).(type) {
	case *ast.File:
		b = ctx.BuildFile(n)
	case ast.Expr:
		b = ctx.BuildExpr(n)
	}
	return b, b.Err()
}

// addDefaults adds a "default" to n, the JSON Schema generated for v, when v
// has a concrete default, and does the same for the fields, list elements and
// pattern values below it. It follows properties, items and
// additionalProperties; a default within a branch of a disjunction is not
// placed.
func addDefaults(v cue.Value, n *ast.StructLit) error {
	if d, ok := v.Default(); ok && d.Validate(cue.Concrete(true)) == nil {
		b, err := d.MarshalJSON()
		if err != nil {
			return err
		}
		x, err := cuejson.Extract("", b)
		if err != nil {
			return err
		}
		n.Elts = append(n.Elts, &ast.Field{Label: ast.NewString("default"), Value: x})
	}

	if props, ok := field(n, "properties").(*ast.StructLit); ok {
		iter, err := v.Fields(cue.Optional(true))
		if err != nil {
			return err
		}
		for iter.Next() {
			if p, ok := field(props, iter.Selector().Unquoted()).(*ast.StructLit); ok {
				if err := addDefaults(iter.Value(), p); err != nil {
					return err
				}
			}
		}
	}
	if items, ok := field(n, "items").(*ast.StructLit); ok {
		if err := addDefaults(v.LookupPath(cue.MakePath(cue.AnyIndex)), items); err != nil {
			return err
		}
	}
	if ap, ok := field(n, "additionalProperties").(*ast.StructLit); ok {
		if err := addDefaults(v.LookupPath(cue.MakePath(cue.AnyString)), ap); err != nil {
			return err
		}
	}
	return nil
}

// field returns the value of the field of s named name, or nil.
func field(s *ast.StructLit, name string) ast.Expr {
	for _, el := range s.Elts {
		if f, ok := el.(*ast.Field); ok {
			if l, _, _ := ast.LabelName(f.Label); l == name {
				return f.Value
			}
		}
	}
	return nil
}
