package compile

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/contenthash"
)

// Tip is a lineage's tip as it stands against the lineage's history: the
// version it cuts as, and whether there is anything to cut.
type Tip struct {
	// Version is the version the tip cuts as. It is never written: the
	// compatibility check against the newest frozen version decides it, the
	// next minor version when the tip is backward compatible with it and the
	// next major version otherwise. With no history, the tip cuts as 0.0. A
	// clean tip has nothing to cut, and Version is the newest frozen version,
	// which it equals.
	Version thema.SyntacticVersion
	// Base is the newest frozen version, against which Version is derived, or
	// nil when the history is empty.
	Base *thema.SyntacticVersion
	// Clean reports whether the tip's schema equals Base's and the tip
	// declares no lenses: the tip is the last version cut.
	Clean bool
}

// String describes the tip as tools show it: "tip (cuts as 2.1)", or "tip
// (clean at 2.0)" when it equals the newest frozen version.
func (t Tip) String() string {
	if t.Clean {
		return fmt.Sprintf("tip (clean at %s)", t.Version)
	}
	return fmt.Sprintf("tip (cuts as %s)", t.Version)
}

// ReadTip reads the tip of the lineage in files against its history: the
// version it cuts as, and whether it is clean. It checks the layout and the
// tip's and the newest frozen version's files, not the lineage as a whole;
// for that, compile it.
func ReadTip(ctx *cue.Context, files []File) (*Tip, error) {
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
	_, tip, err := readTip(ctx, vs, *tipFile)
	return tip, err
}

// CheckClean checks that the lineage in files has a clean tip: that the tip
// equals the newest version under history/, with no lenses of its own, so
// that the history holds everything the tip says. It returns nil for a clean
// tip and an E1003 diagnostic for each way the tip differs otherwise. CI runs
// it to assert that the tip was cut before merging.
func CheckClean(ctx *cue.Context, files []File) error {
	history, tipFile, err := splitLayout(files)
	if err != nil {
		return err
	}
	vs, err := parseVersions(history)
	if err != nil {
		return err
	}
	if tipFile == nil {
		return noTip()
	}
	tv, tip, err := readTip(ctx, vs, *tipFile)
	if err != nil {
		return err
	}
	if tip.Clean {
		return nil
	}
	if tip.Base == nil {
		return diag.New(diag.E1003).At(tv.header()).
			Headlinef("%s is not clean: there is no version under %s/ for it to equal", TipFile, HistoryDir).
			Notef(token.NoPos, "the tip cuts as %s", tip.Version).
			Helpf("cut the tip to %s/%s%s", HistoryDir, tip.Version, Ext).
			Err()
	}
	newest := vs[len(vs)-1]
	var errs []error
	same, err := sameSchema(ctx, newest, tv)
	if err != nil {
		return err
	}
	if !same {
		errs = append(errs, diag.New(diag.E1003).At(tv.schema.Pos()).
			Headlinef("the Schema in %s differs from the one in %s", TipFile, newest.path).
			Notef(token.NoPos, "the tip cuts as %s", tip.Version).
			Helpf("cut the tip to %s/%s%s, or make its Schema equal %s's", HistoryDir, tip.Version, Ext, newest.path).
			Err())
	}
	for _, f := range []*ast.Field{tv.forward, tv.backward} {
		if f == nil {
			continue
		}
		name, _, _ := ast.LabelName(f.Label)
		errs = append(errs, diag.New(diag.E1003).At(f.Pos()).
			Headlinef("%s declares a %s", TipFile, name).
			Notef(token.NoPos, "a lens on the edge into the tip belongs to the version the tip cuts as, %s", tip.Version).
			Helpf("cut the tip to %s/%s%s, which takes the lens with it", HistoryDir, tip.Version, Ext).
			Err())
	}
	return diag.Join(errs...)
}

// readTip parses the tip and derives its version against vs, the frozen
// versions in order, setting the parsed tip's version to it.
func readTip(ctx *cue.Context, vs []*version, f File) (*version, *Tip, error) {
	tv, err := parseVersion(f, true)
	if err != nil {
		return nil, nil, err
	}
	tval, err := evalSchema(ctx, tv)
	if err != nil {
		return nil, nil, err
	}
	tip := &Tip{}
	if len(vs) > 0 {
		newest := vs[len(vs)-1]
		base := newest.v
		tip.Base = &base
		nval, err := evalSchema(ctx, newest)
		if err != nil {
			return nil, nil, err
		}
		if compat.Check(ctx, nval, tval).Compatible() {
			tip.Version = thema.SV(base[0], base[1]+1)
		} else {
			tip.Version = thema.SV(base[0]+1, 0)
		}
		if tv.forward == nil && tv.backward == nil {
			if tip.Clean, err = sameSchema(ctx, newest, tv); err != nil {
				return nil, nil, err
			}
		}
		if tip.Clean {
			tip.Version = base
		}
	}
	tv.v = tip.Version
	return tv, tip, nil
}

// sameSchema reports whether a and b have the same schema: the same canonical
// form, whose digest is the schema part of a version's content hash
// (internal/contenthash). Formatting, comments and the order of fields do not
// count. The tip's identity, when it is wired in, is this digest.
func sameSchema(ctx *cue.Context, a, b *version) (bool, error) {
	form := func(v *version) (string, error) {
		// The canonical form is read from a lineage's schemas; a lineage of
		// one version holds just this schema.
		entry := &ast.StructLit{Elts: []ast.Decl{v.schemaField()}}
		lin := ctx.BuildFile(&ast.File{Decls: append(v.importDecls(), &ast.Field{
			Label: ast.NewIdent("schemas"),
			Value: &ast.StructLit{Elts: []ast.Decl{&ast.Field{Label: ast.NewString("0"), Value: &ast.StructLit{Elts: []ast.Decl{&ast.Field{Label: ast.NewString("0"), Value: entry}}}}}},
		})})
		if err := lin.Err(); err != nil {
			return "", diag.Convert(err, diag.Context{Op: diag.OpCompile, Generic: diag.E0112})
		}
		s, err := contenthash.SchemaForm(lin, contenthash.Version{0, 0})
		if err != nil {
			return "", diag.InternalErr(fmt.Errorf("the canonical form of the schema in %s: %w", v.path, err))
		}
		return s, nil
	}
	fa, err := form(a)
	if err != nil {
		return false, err
	}
	fb, err := form(b)
	if err != nil {
		return false, err
	}
	return fa == fb, nil
}
