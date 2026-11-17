package compile

import (
	"fmt"
	"maps"
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
	// Versions are the lineage's versions in order. From Development,
	// the last is the tip's, unless the tip is clean.
	Versions []thema.SyntacticVersion
	// Tip is the lineage's tip as Development found it. Compile, which
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
func Compile(ctx *cue.Context, name string, files []File, opts ...Option) (*Compiled, error) {
	o, err := resolve(opts, files)
	if err != nil {
		return nil, err
	}
	history, _, err := splitLayout(files)
	if err != nil {
		return nil, err
	}
	vs, err := parseVersions(history, o)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, noHistory()
	}
	return compileVersions(ctx, name, vs)
}

// Development compiles the lineage name with its tip, for the
// development path. The tip becomes the newest version, at the version it
// cuts as (see [Tip]); a clean tip adds nothing, and the result is the
// history's. The normal form leaves out each lens on the edge into the tip
// that tip.thema does not write and derivation cannot produce, and reports it
// in Pending, as a warning: the edge is still being written. A lens the tip
// does write is compiled like any other, and its errors are errors.
func Development(ctx *cue.Context, name string, files []File, opts ...Option) (*Compiled, error) {
	o, err := resolve(opts, files)
	if err != nil {
		return nil, err
	}
	history, tipFile, err := splitLayout(files)
	if err != nil {
		return nil, err
	}
	vs, err := parseVersions(history, o)
	if err != nil {
		return nil, err
	}
	if tipFile == nil {
		return nil, noTip()
	}
	tv, tip, err := readTip(ctx, vs, *tipFile, o)
	if err != nil {
		return nil, err
	}
	if tip.Clean {
		c, cerr := compileVersions(ctx, name, vs)
		if cerr != nil {
			return nil, cerr
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
					Headlinef("%s is the first version, and has no previous version to map from", v.name()).
					Help("remove the lens; a lens maps between a version and its predecessor").
					Err())
			}
			continue
		}
		prev := vs[i-1]
		res := compat.Check(ctx, vals[i-1], vals[i])
		minor := res.Compatible()
		if minor != (v.v[1] != 0) {
			// Only a version under history/ has a number to disagree with:
			// the tip's is derived by this same check.
			errs = cerrors.Append(errs, historyMismatch(res, prev, v))
			continue
		}

		var tipV *thema.SyntacticVersion
		if v.tip {
			tipV = &v.v
		}
		// Forward: previous version to this one. Only a major version has one.
		fe := &edge{ctx: ctx, S: vals[i-1], T: vals[i], sv: prev.v, tv: v.v, tip: tipV, dir: forward, identity: minor, file: v.path, blockPos: v.end(), root: paramPrev, lacunas: &lacunaTable{}, imports: imports}
		fb := &body{}
		if v.forward != nil {
			fe.written, fe.blockPos = true, v.forward.Pos()
			lit, lac, lerr := lensBlock(v.forward, prev, v)
			if lerr != nil {
				errs = cerrors.Append(errs, lerr)
				continue
			}
			if minor && (len(lit.Elts) > 0 || lac != nil && len(lac.Elts) > 0) {
				b := diag.New(diag.E0355).At(v.forward.Pos())
				if v.tip {
					b.Headlinef("%s is backward compatible with %s, so it has no %s", v.name(), prev.name(), fieldForward).
						Note(token.NoPos, "an instance of a version is already an instance of its minor successor; translating forward is implicit").
						Helpf("remove the %s, or make a breaking change", fieldForward)
				} else {
					// A tip like this one does not compile, so it was not cut so.
					b.Headlinef("immutability violation: %s has a %s, and is backward compatible with %s", v.name(), fieldForward, prev.name()).
						Note(token.NoPos, "an instance of a version is already an instance of its minor successor; translating forward is implicit").
						Note(prev.header(), "the version before it")
					restoreHelp(b)
				}
				errs = cerrors.Append(errs, b.Err())
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
			lenses.Elts = append(lenses.Elts, v.normalLens(lensField(fe.blockPos, prev.v, v.v, fwd), prev))
		}

		// Backward: this version to the previous one, derived from the forward
		// lens and overridden by what the author wrote.
		be := &edge{ctx: ctx, S: vals[i], T: vals[i-1], sv: v.v, tv: prev.v, tip: tipV, dir: backward, file: v.path, blockPos: v.end(), root: paramSelf, lacunas: &lacunaTable{}, imports: imports}
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
		lenses.Elts = append(lenses.Elts, v.normalLens(lensField(be.blockPos, v.v, prev.v, bwd), prev))
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

// paramName is the normal form's name for a lens's instance of version v:
// v0_0 for 0.0. The authoring form names it prev or self.
func paramName(v thema.SyntacticVersion) string {
	return fmt.Sprintf("v%d_%d", v[0], v[1])
}

// lensParams are the names a lens block gives the instance it takes and
// the one it produces: a ForwardLens takes prev and produces self, and a
// BackwardLens takes self and produces prev.
func lensParams(block string) (in, out string) {
	if block == fieldBackward {
		return paramSelf, paramPrev
	}
	return paramPrev, paramSelf
}

// lensShape is a lens block as help writes it: ForwardLens: (prev): {self:
// {...}}.
func lensShape(block string) string {
	in, out := lensParams(block)
	return fmt.Sprintf("%s: (%s): {%s: {...}}", block, in, out)
}

// lensBlock reads a ForwardLens or BackwardLens block for the edge from -> to:
// ForwardLens: (prev): {self: {...}, lacunas: {...}}, or BackwardLens:
// (self): {prev: {...}}. The parameter names the instance the lens takes, and
// the struct after it holds the body producing the instance it returns,
// under that instance's name, and the optional lacunas of the body's
// mappings. The parameter is a field labeled by an expression, which CUE
// would read as a field named by the value of prev; the compiler reads it as
// syntax, and the normal form names both instances by their versions.
func lensBlock(f *ast.Field, from, to *version) (body, lacunas *ast.StructLit, err cerrors.Error) {
	name, _, _ := ast.LabelName(f.Label)
	in, out := lensParams(name)
	shape := fmt.Sprintf("write %s, with an optional %s: {...} beside %s", lensShape(name), fieldLacunas, out)
	roles := fmt.Sprintf("in %s, %s is an instance of %s, and %s one of %s", name, in, from.name(), out, to.name())
	fail := func(p token.Pos, format string, args ...any) cerrors.Error {
		return diag.New(diag.E0111).At(p).Headlinef(format, args...).Note(token.NoPos, roles).Help(shape).Err()
	}
	var param *ast.Field
	var stray []*ast.Field
	for _, d := range f.Value.(*ast.StructLit).Elts {
		switch d := d.(type) {
		case *ast.CommentGroup:
		case *ast.Field:
			if _, ok := d.Label.(*ast.ParenExpr); !ok {
				stray = append(stray, d)
				continue
			}
			if param != nil {
				return nil, nil, fail(d.Pos(), "%s declares the instance it takes twice", name)
			}
			param = d
		default:
			return nil, nil, fail(d.Pos(), "%s holds a declaration that is not a field", name)
		}
	}
	if param == nil {
		p := f.Pos()
		if len(stray) > 0 {
			p = stray[0].Pos()
		}
		return nil, nil, fail(p, "%s does not declare the instance it takes", name)
	}
	if len(stray) > 0 {
		label, _, lerr := ast.LabelName(stray[0].Label)
		if lerr != nil {
			return nil, nil, fail(stray[0].Pos(), "%s has a field whose label is not a name", name)
		}
		return nil, nil, fail(stray[0].Pos(), "%s is not part of %s; write it inside the braces after (%s)", label, name, in)
	}
	px := param.Label.(*ast.ParenExpr)
	if id, ok := px.X.(*ast.Ident); !ok || id.Name != in || param.Constraint != token.ILLEGAL {
		return nil, nil, fail(px.X.Pos(), "%s takes an instance of %s, so its parameter is %s", name, from.name(), in)
	}
	inner, ok := param.Value.(*ast.StructLit)
	if !ok {
		return nil, nil, fail(param.Value.Pos(), "the parameter of %s is followed by a struct holding %s", name, out)
	}
	var result, lac *ast.Field
	for _, d := range inner.Elts {
		switch d := d.(type) {
		case *ast.CommentGroup:
		case *ast.Field:
			label, _, err := ast.LabelName(d.Label)
			if err != nil {
				return nil, nil, fail(d.Pos(), "%s has a field whose label is not a name", name)
			}
			switch label {
			case out:
				if _, ok := d.Value.(*ast.StructLit); !ok {
					return nil, nil, fail(d.Value.Pos(), "%s produces an instance of %s: %s must be a struct of mappings", name, to.name(), out)
				}
				result = d
			case fieldLacunas:
				if _, ok := d.Value.(*ast.StructLit); !ok {
					return nil, nil, fail(d.Value.Pos(), "%s is a struct, keyed by the mappings of %s", fieldLacunas, out)
				}
				lac = d
			case in:
				return nil, nil, fail(d.Pos(), "%s takes %s and produces %s, so its body is %s", name, in, out, out)
			default:
				return nil, nil, fail(d.Pos(), "%s is not part of %s", label, name)
			}
		default:
			return nil, nil, fail(d.Pos(), "%s holds a declaration that is not a field", name)
		}
	}
	if result == nil {
		return nil, nil, fail(param.Pos(), "%s does not declare the instance it produces", name)
	}
	if lac != nil {
		lacunas = lac.Value.(*ast.StructLit)
	}
	return result.Value.(*ast.StructLit), lacunas, nil
}

// name is the version as messages name it: v0.1, or the tip. A message
// names the version's file only where it means the file.
func (v *version) name() string {
	if v.tip {
		return TipName
	}
	return vname(v.v)
}

// vname is version v as messages name it: v0.1. v.String() is the version as
// data writes it, in file names and keys.
func vname(v thema.SyntacticVersion) string {
	return "v" + v.String()
}

// normalLens renames, in the lens f on the edge between prev and v, the
// author's names for its instances to the normal form's, which names each
// after its version: prev.a becomes v1_0.a and self.a becomes v2_0.a, in
// either direction. On the edge into the tip, self is named after the version
// the tip was compiled as.
func (v *version) normalLens(f *ast.Field, prev *version) *ast.Field {
	renameInstances(f, map[string]string{paramPrev: paramName(prev.v), paramSelf: paramName(v.v)})
	return f
}

// renameInstances renames, in n, every reference to an instance of a lens by
// the names in to. A lens refers to nothing but its instance, the operations
// and the strings functions (E0418), so every identifier that is neither a
// label nor a selector is a reference to an instance. Labels and selectors
// name fields of the instances, and are left alone.
func renameInstances(n ast.Node, to map[string]string) {
	skip := make(map[*ast.Ident]bool)
	ast.Walk(n, func(n ast.Node) bool {
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
			if name, ok := to[n.Name]; ok && !skip[n] {
				n.Name = name
			}
		}
		return true
	}, nil)
}

// missingLens is the E1001 warning for the lens in block on the edge between
// prev and the tip v, which the tip does not write and derivation cannot
// produce. Each of derivation's errors is a note.
func missingLens(v, prev *version, block string, errs cerrors.Error) error {
	b := diag.New(diag.E1001).At(v.end())
	from, to := prev.v, v.v
	if block == fieldBackward {
		from, to = to, from
	}
	b.Headlinef("%s has no %s, and one cannot be derived", v.name(), block)
	// The derivation's findings say why: each keeps its code, which names its
	// page, its notes, after its own, and its help, which says what to write
	// in the block. The help that writes the block comes first, and the help
	// about what goes in it after: the fields the block must produce (E0346)
	// write the block out whole, with those fields in it, in place of the
	// block's bare shape.
	var shape, helps []string
	for _, e := range cerrors.Errors(errs) {
		if de, ok := e.(*diag.Error); ok {
			d := de.Diagnostic()
			p := token.NoPos
			if pp := e.Position(); pp.IsValid() {
				p = pp
			}
			b.Notef(p, "%s (%s)", d.Headline, d.Code)
			// InputPositions holds the position of each positioned note, in
			// order. A note with none of its own is about the finding and
			// takes the finding's: every renderer groups notes by position,
			// and without one it would be listed after every finding's note,
			// under the last of them.
			ps := de.InputPositions()
			for _, n := range d.Notes {
				np := p
				if n.Pos != nil && len(ps) > 0 {
					np, ps = ps[0], ps[1:]
				}
				b.Note(np, n.Label)
			}
			if d.Code == diag.E0346 {
				shape = append(shape, d.Help...)
			} else {
				helps = append(helps, d.Help...)
			}
			continue
		}
		msg, args := e.Msg()
		b.Note(e.Position(), fmt.Sprintf(msg, args...))
	}
	b.Notef(token.NoPos, "translating from %s to %s needs it; until %s writes it, that translation fails (E1002)", nameOf(from, v), nameOf(to, v), v.name())
	switch {
	case len(shape) > 0:
	default:
		shape = []string{fmt.Sprintf("write %s in %s", lensShape(block), v.path)}
	}
	for _, h := range append(shape, helps...) {
		b.Help(h)
	}
	return b.Err()
}

// nameOf names version x for a message about the edge into the tip t.
func nameOf(x thema.SyntacticVersion, t *version) string {
	if x == t.v {
		return TipName
	}
	return vname(x)
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
