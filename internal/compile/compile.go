package compile

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/compat"
)

// Compiled is a lineage in normal form, built from authoring files.
type Compiled struct {
	Name string
	// File is the normal form: name, schemas and lenses, every node positioned
	// in the authoring file it came from. Build it with cue.Context.BuildFile
	// and bind it with thema.BindLineage.
	File *ast.File
	// Versions are the lineage's versions in order.
	Versions []thema.SyntacticVersion
}

// Compile compiles the authoring files of the lineage name to its normal form.
// Errors are positioned in the authoring files.
func Compile(ctx *cue.Context, name string, files []File) (*Compiled, error) {
	vs, err := parseVersions(files)
	if err != nil {
		return nil, err
	}

	// Each schema evaluated alone, for the lens compiler's queries.
	vals := make([]cue.Value, len(vs))
	var errs cerrors.Error
	for i, v := range vs {
		sv := ctx.BuildFile(&ast.File{Decls: []ast.Decl{v.schemaField()}}).LookupPath(cue.MakePath(cue.Str(normalSchema)))
		if err := sv.Err(); err != nil {
			errs = cerrors.Append(errs, cerrors.Promote(err, v.path))
			continue
		}
		vals[i] = sv
	}
	if errs != nil {
		return nil, errs
	}

	tip := vs[len(vs)-1]
	out := &Compiled{Name: name}
	schemas := &ast.StructLit{}
	lenses := &ast.StructLit{}
	majors := make(map[uint]*ast.StructLit)
	for i, v := range vs {
		out.Versions = append(out.Versions, v.v)
		m, ok := majors[v.v[0]]
		if !ok {
			m = at(v.header(), &ast.StructLit{})
			majors[v.v[0]] = m
			schemas.Elts = append(schemas.Elts, at(v.header(), &ast.Field{Label: at(v.header(), ast.NewString(fmt.Sprint(v.v[0]))), Value: m}))
		}
		entry := at(v.header(), &ast.StructLit{Elts: []ast.Decl{v.schemaField()}})
		m.Elts = append(m.Elts, at(v.header(), &ast.Field{Label: at(v.header(), ast.NewString(fmt.Sprint(v.v[1]))), Value: entry}))

		if i == 0 {
			if v.forward != nil || v.backward != nil {
				errs = cerrors.Append(errs, cerrors.Newf(v.header(), "%s is the first version, and has no previous version to map from", v.path))
			}
			continue
		}
		prev := vs[i-1]
		res := compat.Check(ctx, vals[i-1], vals[i])
		minor := res.Compatible()
		switch {
		case minor && v.v[1] == 0:
			errs = cerrors.Append(errs, cerrors.Newf(v.header(), "%s: this version is backward compatible with %s, so it is minor version %s: rename the file, or make the change breaking", v.path, prev.v, thema.SV(prev.v[0], prev.v[1]+1)))
			continue
		case !minor && v.v[1] != 0:
			errs = cerrors.Append(errs, cerrors.Newf(v.header(), "%s: this version is not backward compatible with %s, so it is major version %s: rename the file, or make the change compatible:\n%s", v.path, prev.v, thema.SV(prev.v[0]+1, 0), cerrors.Details(res.Error(), nil)))
			continue
		}

		// Forward: previous version to this one. Only a major version has one.
		fe := &edge{ctx: ctx, S: vals[i-1], T: vals[i], sv: prev.v, tv: v.v, dir: forward, file: v.path, blockPos: v.header(), root: paramName(prev.v), lacunas: &lacunaTable{}}
		fb := &body{}
		if v.forward != nil {
			fe.written, fe.blockPos = true, v.forward.Pos()
			lit, lac, lerr := lensBlock(v.forward, prev.v, v.v)
			if lerr != nil {
				errs = cerrors.Append(errs, lerr)
				continue
			}
			if minor && (len(lit.Elts) > 0 || lac != nil && len(lac.Elts) > 0) {
				errs = cerrors.Append(errs, cerrors.Newf(v.forward.Pos(), "%s: this version is backward compatible with %s, so translating forward is implicit: remove the ForwardLens entries, or make the change breaking", v.path, prev.v))
				continue
			}
			fe.parseBody(lit, cue.MakePath(), cue.MakePath(), fb)
			if lac != nil {
				fe.parseLacunas(lac, cue.MakePath(), fe.lacunas)
			}
		}
		fwd := fe.assemble(fb)
		if fe.errs != nil {
			// The backward lens is derived from the forward one; with the forward
			// lens wrong, its errors would only repeat the forward lens's.
			errs = cerrors.Append(errs, fe.errs)
			continue
		}
		if !minor {
			lenses.Elts = append(lenses.Elts, lensField(fe.blockPos, prev.v, v.v, fwd))
		}

		// Backward: this version to the previous one, derived from the forward
		// lens and overridden by what the author wrote.
		be := &edge{ctx: ctx, S: vals[i], T: vals[i-1], sv: v.v, tv: prev.v, dir: backward, file: v.path, blockPos: v.header(), root: paramName(v.v), lacunas: &lacunaTable{}}
		ab := &body{}
		if v.backward != nil {
			be.written, be.blockPos = true, v.backward.Pos()
			lit, lac, lerr := lensBlock(v.backward, v.v, prev.v)
			if lerr != nil {
				errs = cerrors.Append(errs, lerr)
				continue
			}
			be.parseBody(lit, cue.MakePath(), cue.MakePath(), ab)
			if lac != nil {
				be.parseLacunas(lac, cue.MakePath(), be.lacunas)
			}
		}
		bwd := be.assemble(be.derive(fwd, ab))
		errs = cerrors.Append(errs, be.errs)
		lenses.Elts = append(lenses.Elts, lensField(be.blockPos, v.v, prev.v, bwd))
	}
	if errs != nil {
		return nil, errs
	}

	out.File = &ast.File{Decls: []ast.Decl{
		at(tip.header(), &ast.Field{Label: at(tip.header(), ast.NewIdent("name")), Value: at(tip.header(), ast.NewString(name))}),
		at(tip.header(), &ast.Field{Label: at(tip.header(), ast.NewIdent("schemas")), Value: schemas}),
		at(tip.header(), &ast.Field{Label: at(tip.header(), ast.NewIdent("lenses")), Value: lenses}),
	}}
	return out, nil
}

// normalSchema is the normal form's name for a version's schema field; the
// authoring form writes it as Schema.
const normalSchema = "schema"

// paramName is the identifier a lens binds an instance of version v to:
// v0_0 for 0.0.
func paramName(v thema.SyntacticVersion) string {
	return fmt.Sprintf("v%d_%d", v[0], v[1])
}

// lensBlock reads a ForwardLens or BackwardLens block for the edge from -> to:
// {v<from>: _, v<to>: {...}, lacunas: {...}}. The first field declares the
// instance the lens takes, the second is the body producing the instance it
// returns, and the optional third declares the lacunas of the body's mappings.
func lensBlock(f *ast.Field, from, to thema.SyntacticVersion) (body, lacunas *ast.StructLit, err cerrors.Error) {
	name, _, _ := ast.LabelName(f.Label)
	shape := fmt.Sprintf("%s for the lens from %s to %s is {%s: _, %s: {...}}, with an optional %s: {...}", name, from, to, paramName(from), paramName(to), fieldLacunas)
	var param, result, lac *ast.Field
	for _, d := range f.Value.(*ast.StructLit).Elts {
		switch d := d.(type) {
		case *ast.CommentGroup:
		case *ast.Field:
			label, _, err := ast.LabelName(d.Label)
			if err != nil {
				return nil, nil, cerrors.Newf(d.Pos(), "%s", shape)
			}
			switch label {
			case paramName(from):
				if id, ok := d.Value.(*ast.Ident); !ok || id.Name != "_" {
					return nil, nil, cerrors.Newf(d.Value.Pos(), "%s takes an instance of %s: declare it as %s: _", name, from, paramName(from))
				}
				param = d
			case paramName(to):
				if _, ok := d.Value.(*ast.StructLit); !ok {
					return nil, nil, cerrors.Newf(d.Value.Pos(), "%s produces an instance of %s: %s must be a struct of mappings", name, to, paramName(to))
				}
				result = d
			case fieldLacunas:
				if _, ok := d.Value.(*ast.StructLit); !ok {
					return nil, nil, cerrors.Newf(d.Value.Pos(), "%s is a struct, keyed by the mappings of %s", fieldLacunas, paramName(to))
				}
				lac = d
			default:
				return nil, nil, cerrors.Newf(d.Pos(), "%s is not part of %s; %s", label, name, shape)
			}
		default:
			return nil, nil, cerrors.Newf(d.Pos(), "%s", shape)
		}
	}
	if param == nil {
		return nil, nil, cerrors.Newf(f.Pos(), "%s does not declare the instance it takes; %s", name, shape)
	}
	if result == nil {
		return nil, nil, cerrors.Newf(f.Pos(), "%s does not declare the instance it produces; %s", name, shape)
	}
	if lac != nil {
		lacunas = lac.Value.(*ast.StructLit)
	}
	return result.Value.(*ast.StructLit), lacunas, nil
}

// lensField builds the lenses entry for the edge from -> to.
func lensField(p token.Pos, from, to thema.SyntacticVersion, l *compiledLens) *ast.Field {
	elts := []ast.Decl{
		at(p, &ast.Field{Label: at(p, ast.NewIdent(paramName(from))), Value: at(p, ast.NewIdent("_"))}),
	}
	elts = append(elts, l.lets...)
	elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent(paramName(to))), Value: at(p, l.result)}))
	if len(l.lacunas) > 0 {
		elts = append(elts, at(p, &ast.Field{Label: at(p, ast.NewIdent("lacunas")), Value: at(p, ast.NewList(l.lacunas...))}))
	}
	return at(p, &ast.Field{Label: at(p, ast.NewString(fmt.Sprintf("%s->%s", from, to))), Value: at(p, &ast.StructLit{Elts: elts})})
}

// Text formats the normal form as CUE source.
func (c *Compiled) Text() (string, error) {
	b, err := format.Node(c.File)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// exprText formats an expression for a message.
func exprText(x ast.Expr) string {
	b, err := format.Node(x)
	if err != nil {
		return fmt.Sprintf("%T", x)
	}
	return string(b)
}
