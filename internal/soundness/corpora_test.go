package soundness

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/cue/parser"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/exemplars"
	"github.com/sdboyer/thema/internal/compile"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/txtartest/vanilla"
	"github.com/sdboyer/thema/internal/util"
)

const root = "../.."

// entry is one lineage a corpus offers: bound, or the reason it is skipped.
type entry struct {
	name string
	lin  thema.Lineage
	// skip says why the case yields no bound lineage: it does not bind, or
	// could not be adapted into a lineage.
	skip string
	// adapted says how the case was turned into a lineage, when it is not one.
	adapted string
}

type corpus struct {
	name string
	load func(t *testing.T) []entry
}

var corpora = []corpus{
	{"compat", loadCompat},
	{"lenscheck", loadLenscheck},
	{"cases", loadCases},
	{"lineage", loadLineageCorpus},
	{"refs", loadRefs},
	{"exemplars", loadExemplars},
	{"docs", loadDocs},
	{"controls", loadControls},
}

func bindErr(err error) string {
	ds := diag.Diagnostics(err)
	if len(ds) == 0 {
		return "does not bind: " + firstLine(err.Error())
	}
	var codes []string
	for _, d := range ds {
		codes = append(codes, string(d.Code))
	}
	slices.Sort(codes)
	return "does not bind: " + strings.Join(slices.Compact(codes), " ")
}

func bind(name string, root cue.Value, p cue.Path, rt *thema.Runtime, adapted string) entry {
	var lin thema.Lineage
	var err error
	if perr := catch(func() { lin, err = thema.BindLineage(root, p, rt) }); perr != nil {
		return entry{name: name, skip: "bind panicked: " + firstLine(perr.Error()), adapted: adapted}
	}
	if err != nil {
		return entry{name: name, skip: bindErr(err), adapted: adapted}
	}
	return entry{name: name, lin: lin, adapted: adapted}
}

func archives(t *testing.T, glob string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, glob))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no archives at %s: %v", glob, err)
	}
	slices.Sort(paths)
	return paths
}

func caseName(p string) string { return strings.TrimSuffix(filepath.Base(p), ".txtar") }

// loadPackage loads files as package m of module example.com/m, with thema's
// module served, as testdata/cases and the refs corpus load theirs.
func loadPackage(ctx *cue.Context, files map[string]string) (cue.Value, error) {
	cfg := &load.Config{
		Dir: "/m",
		Overlay: map[string]load.Source{
			"/m/cue.mod/module.cue": load.FromString(util.ModuleFile("example.com/m")),
		},
	}
	for name, data := range files {
		if !strings.Contains(name, "/") && !strings.Contains(data, "package ") {
			data = "package m\n" + data
		}
		cfg.Overlay["/m/"+name] = load.FromString(data)
	}
	if err := embedreg.Configure(cfg, thema.CueJointFS); err != nil {
		return cue.Value{}, err
	}
	bi := load.Instances([]string{"."}, cfg)[0]
	if bi.Err != nil {
		return cue.Value{}, bi.Err
	}
	v := ctx.BuildInstance(bi)
	return v, v.Err()
}

// testdata/cases: in.cue declares the lineage at lin, or at the root; other
// .cue files are files of the module. Loaded as TestCases loads them.
func loadCases(t *testing.T) []entry {
	var out []entry
	for _, p := range archives(t, "testdata/cases/*.txtar") {
		ar, err := txtar.ParseFile(p)
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{}
		for _, f := range ar.Files {
			if strings.HasSuffix(f.Name, ".cue") {
				files[f.Name] = string(f.Data)
			}
		}
		ctx := cuecontext.New()
		v, err := loadPackage(ctx, files)
		if err != nil {
			out = append(out, entry{name: caseName(p), skip: "does not load: " + firstLine(err.Error())})
			continue
		}
		var lp cue.Path
		if v.LookupPath(cue.ParsePath("lin")).Exists() {
			lp = cue.ParsePath("lin")
		}
		out = append(out, bind(caseName(p), v, lp, thema.NewRuntime(ctx), ""))
	}
	return out
}

// testdata/lineage: the vanilla txtar loader, the lineage at the root.
func loadLineageCorpus(t *testing.T) []entry {
	var out []entry
	paths := append(archives(t, "testdata/lineage/*.txtar"), archives(t, "testdata/lineage/join/*.txtar")...)
	for _, p := range paths {
		ar, err := txtar.ParseFile(p)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(strings.TrimSuffix(p, ".txtar"), root+"/testdata/lineage/")
		insts := vanilla.LoadVanilla(thema.CueJointFS, ar)
		if insts[0].Err != nil {
			out = append(out, entry{name: name, skip: "does not load: " + firstLine(insts[0].Err.Error())})
			continue
		}
		ctx := cuecontext.New()
		v := ctx.BuildInstance(insts[0])
		out = append(out, bind(name, v, cue.Path{}, thema.NewRuntime(ctx), ""))
	}
	return out
}

// testdata/refs: the "together" suite. text.cue is written into package m at
// #at beside in.cue (a copy of the refs corpus's textAt and loadUse, which are
// in that package's tests).
func loadRefs(t *testing.T) []entry {
	var out []entry
	for _, p := range archives(t, "testdata/refs/*.txtar") {
		ar, err := txtar.ParseFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var at string
		for _, l := range strings.Split(string(ar.Comment), "\n") {
			if s, ok := strings.CutPrefix(l, "#at:"); ok {
				at = strings.TrimSpace(s)
			}
		}
		files := map[string]string{}
		var text string
		for _, f := range ar.Files {
			switch {
			case f.Name == "text.cue":
				text = string(f.Data)
			case strings.HasSuffix(f.Name, ".cue") && !strings.HasPrefix(f.Name, "out/"):
				files[f.Name] = string(f.Data)
			}
		}
		imports, body, err := splitImports(text)
		if err != nil {
			t.Fatal(err)
		}
		var field strings.Builder
		for _, sel := range cue.ParsePath(at).Selectors() {
			fmt.Fprintf(&field, "%s: ", sel)
		}
		files["text.cue"] = fmt.Sprintf("package m\n%s\n%s{%s}\n", imports, field.String(), body)
		ctx := cuecontext.New()
		v, err := loadPackage(ctx, files)
		if err != nil {
			out = append(out, entry{name: caseName(p), skip: "does not load: " + firstLine(err.Error())})
			continue
		}
		out = append(out, bind(caseName(p), v, cue.ParsePath(at), thema.NewRuntime(ctx), ""))
	}
	return out
}

func loadExemplars(t *testing.T) []entry {
	var out []entry
	rt := thema.NewRuntime(cuecontext.New())
	var all map[string]thema.Lineage
	if perr := catch(func() { all = exemplars.All(rt) }); perr != nil {
		return []entry{{name: "exemplars", skip: "exemplars.All panicked: " + firstLine(perr.Error())}}
	}
	for name, lin := range all {
		out = append(out, entry{name: name, lin: lin})
	}
	slices.SortFunc(out, func(a, b entry) int { return strings.Compare(a.name, b.name) })
	return out
}

// docs/*.cue: each file is a package of its own; every field of it that holds
// a lineage (has schemas) is bound.
func loadDocs(t *testing.T) []entry {
	var out []entry
	paths, _ := filepath.Glob(filepath.Join(root, "docs/*.cue"))
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		ctx := cuecontext.New()
		v, err := loadPackage(ctx, map[string]string{filepath.Base(p): string(src)})
		if err != nil {
			out = append(out, entry{name: filepath.Base(p), skip: "does not load: " + firstLine(err.Error())})
			continue
		}
		it, err := v.Fields()
		if err != nil {
			t.Fatal(err)
		}
		for it.Next() {
			if !it.Value().LookupPath(cue.ParsePath("schemas")).Exists() {
				continue
			}
			lp := cue.MakePath(it.Selector())
			out = append(out, bind(filepath.Base(p)+":"+lp.String(), v, lp, thema.NewRuntime(ctx), ""))
		}
	}
	return out
}

// splitImports splits CUE source into its import declarations and the rest.
func splitImports(text string) (imports, body string, err error) {
	f, err := parser.ParseFile("x.cue", text, parser.ImportsOnly)
	if err != nil {
		return "", "", err
	}
	split := 0
	for d := range f.ImportDecls() {
		split = d.End().Offset()
	}
	if i := strings.IndexByte(text[split:], '\n'); split > 0 && i >= 0 {
		split += i + 1
	}
	return text[:split], text[split:], nil
}

// versionFile writes a schema, given as a file's top-level fields, as an
// authoring version file, with a ForwardLens and a BackwardLens when given.
func versionFile(schema, forward, backward string) (string, error) {
	imports, body, err := splitImports(schema)
	if err != nil {
		return "", err
	}
	fimports, fbody, err := splitImports(forward)
	if err != nil {
		return "", err
	}
	bimports, bbody, err := splitImports(backward)
	if err != nil {
		return "", err
	}
	s := fmt.Sprintf("%s\nSchema: {\n%s\n}\n", mergeImports(imports, fimports, bimports), body)
	if forward != "" {
		s += "\nForwardLens: {\n" + fbody + "\n}\n"
	}
	if backward != "" {
		s += "\nBackwardLens: {\n" + bbody + "\n}\n"
	}
	return s, nil
}

// constLens is a lens from param from to param to that writes one generated
// instance of the target schema as a constant, and drops each field drop
// names. It is total whenever the instance is valid, so it binds where no
// lens derives; it exercises little of the lens checker.
func constLens(ctx *cue.Context, target string, drop []string, from, to string) (string, bool) {
	sch := ctx.CompileString(target)
	if sch.Err() != nil {
		return "", false
	}
	closed := cuetil.CloseRecursive(ctx, sch)
	cands, _ := NewGenerator(ctx, DefaultLimits).Generate(sch)
	for _, c := range cands {
		if closed.Unify(ctx.BuildExpr(c.Expr)).Validate(cue.Concrete(true)) != nil || c.Undeclared {
			continue
		}
		st, ok := c.Expr.(*ast.StructLit)
		if !ok {
			continue
		}
		for _, d := range drop {
			st.Elts = append(st.Elts, &ast.Field{Label: ast.NewIdent(d), Value: ast.NewIdent("_drop")})
		}
		return fmt.Sprintf("%s: _\n%s: %s\n", from, to, text(st)), true
	}
	return "", false
}

// mergeImports joins import declarations, one import per line, without
// duplicates.
func mergeImports(decls ...string) string {
	var specs []string
	for _, d := range decls {
		f, err := parser.ParseFile("x.cue", d, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for spec := range f.ImportSpecs() {
			s := spec.Path.Value
			if spec.Name != nil {
				s = spec.Name.Name + " " + s
			}
			if !slices.Contains(specs, s) {
				specs = append(specs, s)
			}
		}
	}
	var b strings.Builder
	for _, s := range specs {
		fmt.Fprintf(&b, "import %s\n", s)
	}
	return b.String()
}

// removedFields lists the top-level fields old declares and new does not,
// read from the compiled schemas.
func removedFields(ctx *cue.Context, old, new string) []string {
	labels := func(src string) []string {
		var out []string
		it, err := ctx.CompileString(src).Fields(cue.Optional(true))
		if err != nil {
			return nil
		}
		for it.Next() {
			out = append(out, it.Selector().String())
		}
		return out
	}
	nl := labels(new)
	var out []string
	for _, l := range labels(old) {
		if !slices.Contains(nl, strings.TrimRight(l, "?!")) && !slices.Contains(nl, strings.TrimRight(l, "?!")+"?") && !slices.Contains(nl, strings.TrimRight(l, "?!")+"!") {
			out = append(out, strings.TrimRight(l, "?!"))
		}
	}
	return out
}

func dropLens(fields []string, from, to string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: _\n%s: {\n", from, to)
	for _, f := range fields {
		fmt.Fprintf(&b, "\t%s: _drop\n", f)
	}
	b.WriteString("}\n")
	return b.String()
}

// compileAuthoring compiles version files into a normal form, and binds it
// after replacing the lens on edge swap, when swap is set, with lens (given
// as a file's top-level fields).
func compileAuthoring(ctx *cue.Context, files []compile.File, swap, lens string) (*ast.File, error) {
	var c *compile.Compiled
	var err error
	if perr := catch(func() { c, err = compile.Compile(ctx, "c", files) }); perr != nil {
		return nil, fmt.Errorf("compiler panicked: %v", perr)
	}
	if err != nil {
		return nil, err
	}
	if swap == "" {
		return c.File, nil
	}
	imports, body, err := splitImports(lens)
	if err != nil {
		return nil, err
	}
	lf, err := parser.ParseFile("lens.cue", imports+"\nX: {\n"+body+"\n}\n")
	if err != nil {
		return nil, err
	}
	var lensX ast.Expr
	var importDecls []ast.Decl
	for _, d := range lf.Decls {
		switch d := d.(type) {
		case *ast.ImportDecl:
			importDecls = append(importDecls, d)
		case *ast.Field:
			lensX = d.Value
		}
	}
	replaced := false
	for _, d := range c.File.Decls {
		f, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		if name, _, _ := ast.LabelName(f.Label); name != "lenses" {
			continue
		}
		s, ok := f.Value.(*ast.StructLit)
		if !ok {
			continue
		}
		for _, e := range s.Elts {
			lf, ok := e.(*ast.Field)
			if !ok {
				continue
			}
			if name, _, _ := ast.LabelName(lf.Label); name == swap {
				lf.Value = lensX
				replaced = true
			}
		}
	}
	if !replaced {
		return nil, fmt.Errorf("no lens %s in the normal form", swap)
	}
	have := map[string]bool{}
	for spec := range c.File.ImportSpecs() {
		have[spec.Path.Value] = true
	}
	var add []ast.Decl
	for _, d := range importDecls {
		for _, spec := range d.(*ast.ImportDecl).Specs {
			if !have[spec.Path.Value] {
				have[spec.Path.Value] = true
				add = append(add, &ast.ImportDecl{Specs: []*ast.ImportSpec{spec}})
			}
		}
	}
	c.File.Decls = append(add, c.File.Decls...)
	return c.File, nil
}

func bindFile(name string, ctx *cue.Context, f *ast.File, adapted string) entry {
	v := ctx.BuildFile(f)
	if err := v.Err(); err != nil {
		return entry{name: name, skip: "normal form does not build: " + firstLine(err.Error()), adapted: adapted}
	}
	return bind(name, v, cue.Path{}, thema.NewRuntime(ctx), adapted)
}

// The compat corpus holds schema pairs. A compatible pair becomes versions 0.0
// and 0.1 of an authoring-form lineage; any other pair becomes 0.0 and 1.0.
// Each lens the compiler cannot derive is written: first a ForwardLens that
// drops the fields 0.0 declares and 1.0 does not, then a constant lens (see
// constLens) for each direction that still fails.
func loadCompat(t *testing.T) []entry {
	var out []entry
	for _, p := range archives(t, "internal/compat/testdata/*.txtar") {
		ar, err := txtar.ParseFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var old, new, verdict string
		for _, f := range ar.Files {
			switch f.Name {
			case "old.cue":
				old = string(f.Data)
			case "new.cue":
				new = string(f.Data)
			case "out":
				verdict = firstLine(string(f.Data))
			}
		}
		ctx := cuecontext.New()
		nv, fp, tp := "1.0", "v0_0", "v1_0"
		if verdict == "compatible" {
			nv, tp = "0.1", "v0_1"
		}
		rm := removedFields(ctx, old, new)
		type attempt struct{ fwd, bwd, how string }
		attempts := []attempt{{how: "lenses derived"}}
		if nv == "1.0" && len(rm) > 0 {
			attempts = append(attempts, attempt{fwd: dropLens(rm, fp, tp), how: "ForwardLens drops " + strings.Join(rm, ", ") + ", BackwardLens derived"})
		}
		cbwd, okb := constLens(ctx, old, nil, tp, fp)
		if okb {
			a := attempt{bwd: cbwd, how: "constant BackwardLens"}
			if nv == "1.0" && len(rm) > 0 {
				a.fwd, a.how = dropLens(rm, fp, tp), a.how+", ForwardLens drops "+strings.Join(rm, ", ")
			}
			attempts = append(attempts, a)
		}
		if nv == "1.0" {
			if cfwd, ok := constLens(ctx, new, rm, fp, tp); ok {
				attempts = append(attempts, attempt{fwd: cfwd, how: "constant ForwardLens, BackwardLens derived"})
				if okb {
					attempts = append(attempts, attempt{fwd: cfwd, bwd: cbwd, how: "constant lenses both ways"})
				}
			}
		}
		var f *ast.File
		var adapted string
		for _, at := range attempts {
			of, err1 := versionFile(old, "", "")
			nf, err2 := versionFile(new, at.fwd, at.bwd)
			if err1 != nil || err2 != nil {
				err = fmt.Errorf("cannot split imports")
				break
			}
			files := []compile.File{{Path: "history/0.0.thema", Text: []byte(of)}, {Path: "history/" + nv + ".thema", Text: []byte(nf)}}
			if f, err = compileAuthoring(ctx, files, "", ""); err == nil {
				adapted = fmt.Sprintf("%s pair as 0.0 and %s, %s", verdict, nv, at.how)
				break
			}
		}
		if err != nil {
			out = append(out, entry{name: caseName(p), skip: "authoring form does not compile: " + compileCodes(err)})
			continue
		}
		out = append(out, bindFile(caseName(p), ctx, f, adapted))
	}
	return out
}

// The lenscheck corpus holds a source and target schema and a forward lens in
// the normal form. Each becomes 0.0 and 1.0 of an authoring-form lineage whose
// backward lens the compiler derives, from a ForwardLens of the case's lens
// without its lacunas, or failing that from one that drops the fields 1.0
// lacks. The compiled forward lens is then replaced by the case's own.
func loadLenscheck(t *testing.T) []entry {
	var out []entry
	for _, p := range archives(t, "internal/lenscheck/testdata/*.txtar") {
		ar, err := txtar.ParseFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var src, tgt, lens, verdict string
		for _, f := range ar.Files {
			switch f.Name {
			case "source.cue":
				src = string(f.Data)
			case "target.cue":
				tgt = string(f.Data)
			case "lens.cue":
				lens = string(f.Data)
			case "out":
				verdict = firstLine(string(f.Data))
			}
		}
		if verdict != "total" {
			out = append(out, entry{name: caseName(p), skip: "verdict " + verdict + ": the lens does not bind"})
			continue
		}
		ctx := cuecontext.New()
		of, _ := versionFile(src, "", "")
		var f *ast.File
		var adapted string
		rm := removedFields(ctx, src, tgt)
		type attempt struct{ fwd, bwd, how string }
		attempts := []attempt{
			{fwd: withoutLacunas(lens), how: "BackwardLens derived from the case's lens"},
			{fwd: dropLens(rm, "v0_0", "v1_0"), how: "BackwardLens derived from a ForwardLens that drops"},
		}
		cbwd, okb := constLens(ctx, src, nil, "v1_0", "v0_0")
		if okb {
			attempts = append(attempts, attempt{fwd: dropLens(rm, "v0_0", "v1_0"), bwd: cbwd, how: "constant BackwardLens"})
		}
		if cfwd, ok := constLens(ctx, tgt, rm, "v0_0", "v1_0"); ok {
			attempts = append(attempts, attempt{fwd: cfwd, how: "BackwardLens derived from a constant ForwardLens"})
			if okb {
				attempts = append(attempts, attempt{fwd: cfwd, bwd: cbwd, how: "constant BackwardLens"})
			}
		}
		for _, at := range attempts {
			nf, verr := versionFile(tgt, at.fwd, at.bwd)
			if verr != nil {
				err = verr
				continue
			}
			files := []compile.File{{Path: "history/0.0.thema", Text: []byte(of)}, {Path: "history/1.0.thema", Text: []byte(nf)}}
			f, err = compileAuthoring(ctx, files, "0.0->1.0", lens)
			if err == nil {
				adapted = at.how + "; the forward lens is the case's"
				break
			}
		}
		if err != nil {
			out = append(out, entry{name: caseName(p), skip: "no backward lens derives: " + compileCodes(err)})
			continue
		}
		out = append(out, bindFile(caseName(p), ctx, f, adapted))
	}
	return out
}

// withoutLacunas is a lens's text without its top-level lacunas field.
func withoutLacunas(lens string) string {
	f, err := parser.ParseFile("lens.cue", lens)
	if err != nil {
		return lens
	}
	var keep []ast.Decl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.Field); ok {
			if name, _, _ := ast.LabelName(fd.Label); name == "lacunas" {
				continue
			}
		}
		keep = append(keep, d)
	}
	f.Decls = keep
	s, err := textOf(f)
	if err != nil {
		return lens
	}
	return s
}

func textOf(f *ast.File) (string, error) {
	b, err := format.Node(f)
	return string(b), err
}

func compileCodes(err error) string {
	ds := diag.Diagnostics(err)
	if len(ds) == 0 {
		return firstLine(err.Error())
	}
	var codes []string
	for _, d := range ds {
		codes = append(codes, string(d.Code))
	}
	slices.Sort(codes)
	return strings.Join(slices.Compact(codes), " ")
}
