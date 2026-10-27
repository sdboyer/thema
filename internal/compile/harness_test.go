package compile

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cuelang.org/go/cue/ast"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/envvars"
	"github.com/sdboyer/thema/internal/util"
)

// source is an authoring file the tests stand in for the compiler's input. Its
// text is only there for positions: the tests build the normal form's AST by
// hand, positioned in this file, as the compiler would.
type source struct {
	name, text string
	file       *token.File
}

// parseSource parses text as CUE to get a token.File for it. Parsing, rather
// than token.NewFile, gives the file the language version and experiments the
// evaluator reads from each node's position (see TestPositionsCarrySemantics).
// The authoring form is CUE syntax, so the compiler can do the same.
func parseSource(t *testing.T, name, text string) *source {
	t.Helper()
	f, err := parser.ParseFile(name, text)
	if err != nil {
		t.Fatalf("authoring file %s must be CUE syntax: %v", name, err)
	}
	return &source{name: name, text: text, file: f.Pos().File()}
}

// at returns the position of sub, which must occur exactly once in the file.
func (s *source) at(sub string) token.Pos {
	if strings.Count(s.text, sub) != 1 {
		panic(fmt.Sprintf("%q must occur exactly once in %s", sub, s.name))
	}
	return s.file.Pos(strings.Index(s.text, sub), token.NoRelPos)
}

// header returns the position of the start of the file.
func (s *source) header() token.Pos {
	return s.file.Pos(0, token.NoRelPos)
}

func field(label string, v ast.Expr) *ast.Field {
	return &ast.Field{Label: ast.NewIdent(label), Value: v}
}

// key builds a field with a string label, for keyed schemas and lenses.
func key(label string, v ast.Expr) *ast.Field {
	return &ast.Field{Label: ast.NewString(label), Value: v}
}

func optional(label string, v ast.Expr) *ast.Field {
	return &ast.Field{Label: ast.NewIdent(label), Constraint: token.OPTION, Value: v}
}

func strct(elts ...ast.Decl) *ast.StructLit {
	return &ast.StructLit{Elts: elts}
}

func ident(name string) *ast.Ident {
	return ast.NewIdent(name)
}

func num(s string) *ast.BasicLit {
	return ast.NewLit(token.INT, s)
}

// sel builds input.<name>, positioned at p.
func sel(p token.Pos, base, name string) ast.Expr {
	return at(p, &ast.SelectorExpr{X: at(p, ident(base)), Sel: at(p, ident(name))})
}

// Where a position points.
const (
	// In an authoring file: what the compiler set.
	whereAuthored = "authored"
	// In thema's own CUE: the kernel the normal form is unified with.
	whereKernel = "kernel"
	// In the data being validated or translated.
	whereData = "data"
)

// classify says where p points.
func classify(p token.Pos) string {
	switch name := p.Filename(); {
	case strings.HasSuffix(name, ".thema"):
		return whereAuthored
	case strings.HasPrefix(name, embedreg.Root()+string(filepath.Separator)),
		// The Runtime loads thema's CUE at a path without a version; see
		// TestUnpositionedResultFallsToKernel.
		strings.HasPrefix(name, filepath.Join(util.Prefix, "github.com", "sdboyer", "thema")+string(filepath.Separator)):
		return whereKernel
	case strings.HasSuffix(name, ".json"):
		return whereData
	}
	return "other"
}

// positions describes the positions err carries as structured data: for each
// distinct error, its message's first line, then cerrors.Positions for it (its
// own position first, then its input positions in order), each classified.
// These are what a tool can map back to source, as opposed to positions that
// appear only in an error's text.
func positions(err error) string {
	var all cerrors.Error
	for _, e := range cerrors.Errors(err) {
		all = cerrors.Append(all, e)
	}
	var b strings.Builder
	for _, e := range cerrors.Errors(cerrors.Sanitize(all)) {
		fmt.Fprintf(&b, "%s\n", firstLine(e.Error()))
		ps := cerrors.Positions(e)
		if len(ps) == 0 {
			b.WriteString("\tno positions\n")
		}
		for _, p := range ps {
			fmt.Fprintf(&b, "\t%s %s\n", classify(p), p)
		}
	}
	return b.String()
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// archive is a txtar test case: authoring files and data as input, out/
// sections as goldens.
type archive struct {
	path string
	ar   *txtar.Archive
}

func readArchive(t *testing.T, path string) *archive {
	t.Helper()
	ar, err := txtar.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return &archive{path: path, ar: ar}
}

// file returns the contents of the archive's file name.
func (a *archive) file(t *testing.T, name string) string {
	t.Helper()
	for _, f := range a.ar.Files {
		if f.Name == name {
			return string(f.Data)
		}
	}
	t.Fatalf("%s has no file %s", a.path, name)
	return ""
}

// source parses the archive's file name as an authoring file.
func (a *archive) source(t *testing.T, name string) *source {
	t.Helper()
	return parseSource(t, name, a.file(t, name))
}

// compare checks got against the archive's out/ sections, or with
// THEMA_UPDATE_GOLDEN set, rewrites them.
func (a *archive) compare(t *testing.T, got map[string]string) {
	t.Helper()
	if envvars.UpdateGoldenFiles {
		var kept []txtar.File
		for _, f := range a.ar.Files {
			if !strings.HasPrefix(f.Name, "out/") {
				kept = append(kept, f)
			}
		}
		for _, name := range slices.Sorted(maps.Keys(got)) {
			kept = append(kept, txtar.File{Name: name, Data: []byte(got[name])})
		}
		a.ar.Files = kept
		if err := os.WriteFile(a.path, txtar.Format(a.ar), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, f := range a.ar.Files {
		if !strings.HasPrefix(f.Name, "out/") {
			continue
		}
		want, ok := got[f.Name]
		if !ok {
			t.Errorf("golden section %s is no longer produced; run with %s=1", f.Name, envvars.VarUpdateGolden)
			continue
		}
		if diff := cmp.Diff(string(f.Data), want); diff != "" {
			t.Errorf("%s (-golden +got):\n%s", f.Name, diff)
		}
		delete(got, f.Name)
	}
	for name := range got {
		t.Errorf("missing golden section %s; run with %s=1", name, envvars.VarUpdateGolden)
	}
}

func fileOf(decls ...ast.Decl) *ast.File {
	return &ast.File{Decls: decls}
}
