package compile

import (
	"fmt"
	"maps"
	"regexp"
	"slices"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
)

// Compiled is a lineage in normal form, built from authoring files.
type Compiled struct {
	Name string
	// File is the normal form: name, schemas and lenses, every node positioned
	// in the authoring file it came from. Build it with cue.Context.BuildFile
	// and bind it with thema.BindLineage, passing [Vocabulary] so that its
	// diagnostics name the author's files and lenses.
	File *ast.File
	// Versions are the lineage's versions in order. From CompileDevelopment,
	// the last is the tip's, unless the tip is clean.
	Versions []thema.SyntacticVersion
	// Tip is the lineage's tip as CompileDevelopment found it. Compile, which
	// ignores the tip, leaves it nil.
	Tip *Tip
	// Pending holds what the development path tolerates on the edge into the
	// tip: each lens the tip does not write and derivation cannot produce, as
	// an E1001 warning. The normal form leaves those lenses out. It is nil
	// when the edge is complete, and always from Compile. Each of its errors
	// unwraps to thema/errors.ErrProvisional.
	Pending error
	// paths is the version file each version was read from.
	paths map[thema.SyntacticVersion]string
	// provisional is the version the tip was compiled as, when the normal form
	// includes it.
	provisional *thema.SyntacticVersion
}

// Provisional returns the version the tip was compiled as, when the normal
// form includes the tip as its newest version.
func (c *Compiled) Provisional() (thema.SyntacticVersion, bool) {
	if c.provisional == nil {
		return thema.SyntacticVersion{}, false
	}
	return *c.provisional, true
}

// Compile compiles the frozen versions of the lineage name, those under
// history/, to its normal form. The tip is not read: what binds is what was
// cut. Errors are positioned in the authoring files.
func Compile(ctx *cue.Context, name string, files []File) (*Compiled, error) {
	history, _, err := splitLayout(files)
	if err != nil {
		return nil, err
	}
	vs, err := parseVersions(history)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, noHistory()
	}
	return compileVersions(ctx, name, vs)
}

// CompileDevelopment compiles the lineage name with its tip, for the
// development path. The tip becomes the newest version, at the version it
// cuts as (see [Tip]); a clean tip adds nothing, and the result is the
// history's. The normal form leaves out each lens on the edge into the tip
// that tip.thema does not write and derivation cannot produce, and reports it
// in Pending, as a warning: the edge is still being written. A lens the tip
// does write is compiled like any other, and its errors are errors.
func CompileDevelopment(ctx *cue.Context, name string, files []File) (*Compiled, error) {
	history, tipFile, err := splitLayout(files)
	if err != nil {
		return nil, err
	}
	vs, err := parseVersions(history)
	if err != nil {
		return nil, err
	}
	if tipFile == nil {
		return nil, noTip()
	}
	tv, tip, err := readTip(ctx, vs, *tipFile)
	if err != nil {
		return nil, err
	}
	if tip.Clean {
		c, err := compileVersions(ctx, name, vs)
		if err != nil {
			return nil, err
		}
		c.Tip = tip
		return c, nil
	}
	c, err := compileVersions(ctx, name, append(vs, tv))
	if err != nil {
		return nil, err
	}
	c.Tip = tip
	c.provisional = &tv.v
	return c, nil
}

// evalSchema evaluates v's schema alone.
func evalSchema(ctx *cue.Context, v *version) (cue.Value, error) {
	sv := ctx.BuildFile(&ast.File{Decls: append(v.importDecls(), v.schemaField())}).LookupPath(cue.MakePath(cue.Str(normalSchema)))
	if err := sv.Err(); err != nil {
		return cue.Value{}, diag.Convert(err, diag.Context{Op: diag.OpCompile, Generic: diag.E0112})
	}
	return sv, nil
}

// compileVersions compiles vs, in order and contiguous, to the normal form.
// The last may be the tip.
func compileVersions(ctx *cue.Context, name string, vs []*version) (*Compiled, error) {
	// Each schema evaluated alone, for the lens compiler's queries.
	vals := make([]cue.Value, len(vs))
	var errs cerrors.Error
	for i, v := range vs {
		sv, err := evalSchema(ctx, v)
		if err != nil {
			errs = cerrors.Append(errs, cerrors.Promote(err, ""))
			continue
		}
		vals[i] = sv
	}
	if errs != nil {
		return nil, errs
	}

	tip := vs[len(vs)-1]
	out := &Compiled{Name: name, paths: make(map[thema.SyntacticVersion]string, len(vs))}
	imports := make(map[string]bool)
	schemas := &ast.StructLit{}
	lenses := &ast.StructLit{}
	majors := make(map[uint]*ast.StructLit)
	for i, v := range vs {
		out.Versions = append(out.Versions, v.v)
		out.paths[v.v] = v.path
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
				at := v.header()
				if v.forward != nil {
					at = v.forward.Pos()
				} else {
					at = v.backward.Pos()
				}
				errs = cerrors.Append(errs, diag.New(diag.E0354).At(at).
					Headlinef("%s is the first version, and has no previous version to map from", v.path).
					Help("remove the lens; a lens maps between a version and its predecessor").
					Err())
			}
			continue
		}
		prev := vs[i-1]
		res := compat.Check(ctx, vals[i-1], vals[i])
		minor := res.Compatible()
		switch {
		case minor && v.v[1] == 0:
			errs = cerrors.Append(errs, diag.New(diag.E0206).At(v.header()).
				Headlinef("%s is backward compatible with %s, so it is minor version %s, not a major version", v.path, prev.v, thema.SV(prev.v[0], prev.v[1]+1)).
				Helpf("rename the file %s/%s%s, or make a breaking change", HistoryDir, thema.SV(prev.v[0], prev.v[1]+1), Ext).
				Err())
			continue
		case !minor && v.v[1] != 0:
			// One diagnostic per finding, under the finding's rule, each saying
			// what to do about the file.
			for _, f := range res.Findings {
				if f.Verdict == compat.Compatible {
					continue
				}
				errs = cerrors.Append(errs, compatDiag(f, prev, v))
			}
			continue
		}

		var tipV *thema.SyntacticVersion
		if v.tip {
			tipV = &v.v
		}
		// Forward: previous version to this one. Only a major version has one.
		fe := &edge{ctx: ctx, S: vals[i-1], T: vals[i], sv: prev.v, tv: v.v, tip: tipV, dir: forward, identity: minor, file: v.path, blockPos: v.header(), root: prev.param(), lacunas: &lacunaTable{}, imports: imports}
		fb := &body{}
		if v.forward != nil {
			fe.written, fe.blockPos = true, v.forward.Pos()
			lit, lac, lerr := lensBlock(v.forward, prev, v)
			if lerr != nil {
				errs = cerrors.Append(errs, lerr)
				continue
			}
			if minor && (len(lit.Elts) > 0 || lac != nil && len(lac.Elts) > 0) {
				errs = cerrors.Append(errs, diag.New(diag.E0355).At(v.forward.Pos()).
					Headlinef("%s is backward compatible with %s, so it has no %s", v.path, prev.v, fieldForward).
					Note(token.NoPos, "an instance of a version is already an instance of its minor successor; translating forward is implicit").
					Helpf("remove the %s, or make a breaking change", fieldForward).
					Err())
				continue
			}
			fe.parseBody(lit, cue.MakePath(), cue.MakePath(), fb)
			if lac != nil {
				fe.parseLacunas(lac, cue.MakePath(), fe.lacunas)
			}
		}
		fwd := fe.assemble(fb)
		switch {
		case fe.errs != nil && v.tip && !minor && v.forward == nil:
			// The tip writes no ForwardLens and derivation cannot produce
			// one: the edge is still being written. The backward lens is
			// derived with no forward moves to invert.
			out.Pending = diag.Join(out.Pending, missingLens(v, prev, fieldForward, fe.errs))
			fwd = &compiledLens{}
		case fe.errs != nil:
			// The backward lens is derived from the forward one; with the forward
			// lens wrong, its errors would only repeat the forward lens's.
			errs = cerrors.Append(errs, fe.errs)
			continue
		case !minor:
			lenses.Elts = append(lenses.Elts, v.tipLens(lensField(fe.blockPos, prev.v, v.v, fwd)))
		}

		// Backward: this version to the previous one, derived from the forward
		// lens and overridden by what the author wrote.
		be := &edge{ctx: ctx, S: vals[i], T: vals[i-1], sv: v.v, tv: prev.v, tip: tipV, dir: backward, file: v.path, blockPos: v.header(), root: v.param(), lacunas: &lacunaTable{}, imports: imports}
		ab := &body{}
		if v.backward != nil {
			be.written, be.blockPos = true, v.backward.Pos()
			lit, lac, lerr := lensBlock(v.backward, v, prev)
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
		if be.errs != nil && v.tip && v.backward == nil {
			out.Pending = diag.Join(out.Pending, missingLens(v, prev, fieldBackward, be.errs))
			continue
		}
		errs = cerrors.Append(errs, be.errs)
		lenses.Elts = append(lenses.Elts, v.tipLens(lensField(be.blockPos, v.v, prev.v, bwd)))
	}
	if errs != nil {
		return nil, errs
	}

	out.File = &ast.File{}
	// One import per package: those the version files import, whose specs
	// the schemas' references were bound to (references bound to another
	// file's spec for the same package resolve to it too), then those the
	// lenses' expressions call into. In a fixed order.
	specs := make(map[string]*ast.ImportSpec)
	for _, v := range vs {
		for _, spec := range v.imports {
			path, _ := literal.Unquote(spec.Path.Value)
			if _, ok := specs[path]; !ok {
				specs[path] = spec
			}
		}
	}
	for pkg := range imports {
		if _, ok := specs[pkg]; !ok {
			specs[pkg] = ast.NewImport(nil, pkg)
		}
	}
	if len(specs) > 0 {
		decl := &ast.ImportDecl{}
		for _, path := range slices.Sorted(maps.Keys(specs)) {
			decl.Specs = append(decl.Specs, specs[path])
		}
		out.File.Decls = append(out.File.Decls, decl)
	}
	out.File.Decls = append(out.File.Decls,
		at(tip.header(), &ast.Field{Label: at(tip.header(), ast.NewIdent("name")), Value: at(tip.header(), ast.NewString(name))}),
		at(tip.header(), &ast.Field{Label: at(tip.header(), ast.NewIdent("schemas")), Value: schemas}),
		at(tip.header(), &ast.Field{Label: at(tip.header(), ast.NewIdent("lenses")), Value: lenses}),
	)
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
// {v<from>: _, v<to>: {...}, lacunas: {...}}, where the tip's instance is
// named tip. The first field declares the instance the lens takes, the second
// is the body producing the instance it returns, and the optional third
// declares the lacunas of the body's mappings.
func lensBlock(f *ast.Field, from, to *version) (body, lacunas *ast.StructLit, err cerrors.Error) {
	name, _, _ := ast.LabelName(f.Label)
	fromName, toName := from.param(), to.param()
	shape := fmt.Sprintf("%s for the lens from %s to %s is {%s: _, %s: {...}}, with an optional %s: {...}", name, from.name(), to.name(), fromName, toName, fieldLacunas)
	fail := func(p token.Pos, format string, args ...any) cerrors.Error {
		return diag.New(diag.E0111).At(p).Headlinef(format, args...).Help(shape).Err()
	}
	var param, result, lac *ast.Field
	for _, d := range f.Value.(*ast.StructLit).Elts {
		switch d := d.(type) {
		case *ast.CommentGroup:
		case *ast.Field:
			label, _, err := ast.LabelName(d.Label)
			if err != nil {
				return nil, nil, fail(d.Pos(), "%s has a field whose label is not a name", name)
			}
			switch label {
			case fromName:
				if id, ok := d.Value.(*ast.Ident); !ok || id.Name != "_" {
					return nil, nil, fail(d.Value.Pos(), "%s takes an instance of %s: declare it as %s: _", name, from.name(), fromName)
				}
				param = d
			case toName:
				if _, ok := d.Value.(*ast.StructLit); !ok {
					return nil, nil, fail(d.Value.Pos(), "%s produces an instance of %s: %s must be a struct of mappings", name, to.name(), toName)
				}
				result = d
			case fieldLacunas:
				if _, ok := d.Value.(*ast.StructLit); !ok {
					return nil, nil, fail(d.Value.Pos(), "%s is a struct, keyed by the mappings of %s", fieldLacunas, toName)
				}
				lac = d
			case TipParam:
				// The name is reserved for the tip's instance.
				return nil, nil, diag.New(diag.E0111).At(d.Pos()).
					Headlinef("%s names the tip's instance, and %s is not the tip", TipParam, from.path).
					Helpf("name the instance after its version: %s", shape).
					Err()
			default:
				if (from.tip || to.tip) && paramPattern.MatchString(label) {
					return nil, nil, diag.New(diag.E0111).At(d.Pos()).
						Headlinef("%s names the tip's instance %s, which has no version yet", name, label).
						Notef(token.NoPos, "the tip's version is derived when it is cut, so its lenses name its instance %s", TipParam).
						Help(shape).
						Err()
				}
				return nil, nil, fail(d.Pos(), "%s is not part of %s", label, name)
			}
		default:
			return nil, nil, fail(d.Pos(), "%s holds a declaration that is not a field", name)
		}
	}
	if param == nil {
		return nil, nil, fail(f.Pos(), "%s does not declare the instance it takes", name)
	}
	if result == nil {
		return nil, nil, fail(f.Pos(), "%s does not declare the instance it produces", name)
	}
	if lac != nil {
		lacunas = lac.Value.(*ast.StructLit)
	}
	return result.Value.(*ast.StructLit), lacunas, nil
}

// paramPattern matches a lens parameter named after a version, v0_0.
var paramPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)_(0|[1-9][0-9]*)$`)

// name is the version as messages name it: its number, or the tip.
func (v *version) name() string {
	if v.tip {
		return TipParam
	}
	return v.v.String()
}

// tipLens renames, in a lens on the edge into the tip, the author's name for
// the tip's instance to the normal form's, which names it after the version
// the tip was compiled as: tip.a becomes v2_1.a. A lens refers to nothing but
// its instance, the operations and the strings functions (E0418), so every
// identifier tip that is neither a label nor a selector is the instance. On
// any other version's lens it does nothing.
func (v *version) tipLens(f *ast.Field) *ast.Field {
	if !v.tip {
		return f
	}
	to := paramName(v.v)
	skip := make(map[*ast.Ident]bool)
	ast.Walk(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := n.Sel.(*ast.Ident); ok {
				skip[id] = true
			}
		case *ast.Field:
			if id, ok := n.Label.(*ast.Ident); ok {
				skip[id] = true
			}
		case *ast.Ident:
			if !skip[n] && n.Name == TipParam {
				n.Name = to
			}
		}
		return true
	}, nil)
	return f
}

// missingLens is the E1001 warning for the lens in block on the edge between
// prev and the tip v, which the tip does not write and derivation cannot
// produce. Each of derivation's errors is a note.
func missingLens(v, prev *version, block string, errs cerrors.Error) error {
	b := diag.New(diag.E1001).At(v.header())
	from, to := prev.v, v.v
	if block == fieldBackward {
		from, to = to, from
	}
	b.Headlinef("%s has no %s, and one cannot be derived", v.path, block)
	b.Notef(token.NoPos, "translating from %s to %s needs it; until %s writes it, that translation fails (E1002)", nameOf(from, v), nameOf(to, v), v.path)
	for _, e := range cerrors.Errors(errs) {
		if de, ok := e.(*diag.Error); ok {
			d := de.Diagnostic()
			p := token.NoPos
			if pp := e.Position(); pp.IsValid() {
				p = pp
			}
			b.Note(p, d.Headline)
			continue
		}
		msg, args := e.Msg()
		b.Note(e.Position(), fmt.Sprintf(msg, args...))
	}
	if block == fieldForward {
		b.Helpf("write %s: {%s: _, %s: {...}} in %s", block, prev.param(), TipParam, v.path)
	} else {
		b.Helpf("write %s: {%s: _, %s: {...}} in %s", block, TipParam, prev.param(), v.path)
	}
	return b.Err()
}

// nameOf names version x for a message about the edge into the tip t.
func nameOf(x thema.SyntacticVersion, t *version) string {
	if x == t.v {
		return TipParam
	}
	return x.String()
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
