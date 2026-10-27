package diag_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/diagdocs"
	"github.com/sdboyer/thema/internal/diagtest"
)

// Every code has a reproducer: diag/testdata/codes/<code>.txtar, whose comment
// names the operation that produces it (#op) and explains the code in prose,
// whose files are the operation's input, and whose out/ sections record the
// diagnostics it produces, rendered as text and as JSON. docs/errors is
// generated from them.

const codesDir = "testdata/codes"

var codeName = regexp.MustCompile(`^[EW]\d{4}$`)

func TestCodesTable(t *testing.T) {
	seen := map[diag.Code]bool{}
	for i, ci := range diag.Codes {
		if !codeName.MatchString(string(ci.Code)) {
			t.Errorf("code %q is not of the form E0000", ci.Code)
		}
		if seen[ci.Code] {
			t.Errorf("code %s is declared twice", ci.Code)
		}
		seen[ci.Code] = true
		if i > 0 && diag.Codes[i-1].Code >= ci.Code {
			t.Errorf("Codes is not sorted at %s", ci.Code)
		}
		if ci.Title == "" {
			t.Errorf("code %s has no title", ci.Code)
		}
		diagtest.Lint(t, []diag.Diagnostic{{Code: ci.Code, Severity: ci.Severity, Headline: ci.Title, Primary: &diag.Pos{Origin: diag.OriginUser}}}, diagtest.Authoring)
	}
}

func TestCodesLockstep(t *testing.T) {
	archives := diagtest.Archives(t, codesDir)
	have := map[diag.Code]bool{}
	for _, a := range archives {
		c := diag.Code(a.Name())
		if _, ok := diag.Lookup(c); !ok {
			t.Errorf("%s: no such code in diag.Codes", a.Path)
		}
		have[c] = true
	}
	for _, ci := range diag.Codes {
		if !have[ci.Code] {
			t.Errorf("code %s has no reproducer at %s/%s.txtar", ci.Code, codesDir, ci.Code)
		}
	}
}

func TestCodeReproducers(t *testing.T) {
	for _, a := range diagtest.Archives(t, codesDir) {
		t.Run(a.Name(), func(t *testing.T) {
			ds := reproduce(t, a)
			code := diag.Code(a.Name())
			if !slices.ContainsFunc(ds, func(d diag.Diagnostic) bool { return d.Code == code }) {
				t.Errorf("the reproducer for %s produced no diagnostic with that code", code)
			}
			for _, d := range ds {
				if _, ok := diag.Lookup(d.Code); !ok {
					t.Errorf("produced undeclared code %s", d.Code)
				}
			}
			diagtest.Lint(t, ds, formOf(a))
			a.Compare(t, map[string]string{
				"out/text": renderText(ds, a),
				"out/json": renderJSON(t, ds),
			})
		})
	}
}

// TestDocsErrors keeps docs/errors in lockstep with the codes and their
// reproducers.
func TestDocsErrors(t *testing.T) {
	archives := diagtest.Archives(t, codesDir)
	byCode := map[diag.Code]*diagtest.Archive{}
	for _, a := range archives {
		byCode[diag.Code(a.Name())] = a
	}
	var pages []diagdocs.Page
	for _, ci := range diag.Codes {
		a, ok := byCode[ci.Code]
		if !ok {
			continue
		}
		_, prose := a.Directives()
		text, _ := a.File("out/text")
		pages = append(pages, diagdocs.Page{Info: ci, Prose: prose, Inputs: a.Inputs(), Rendered: text})
	}
	dir := filepath.Join("..", "docs", "errors")
	for _, p := range pages {
		diagtest.CompareFile(t, filepath.Join(dir, string(p.Info.Code)+".md"), diagdocs.ErrorPage(p))
	}
	diagtest.CompareFile(t, filepath.Join(dir, "README.md"), diagdocs.Index(pages))

	// No page for a code that no longer exists.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".md")
		if name == "README" {
			continue
		}
		if _, ok := byCode[diag.Code(name)]; !ok {
			t.Errorf("%s/%s documents a code that does not exist", dir, e.Name())
		}
	}
}

// reproduce runs the archive's operation and returns its diagnostics.
func reproduce(t *testing.T, a *diagtest.Archive) []diag.Diagnostic {
	t.Helper()
	dirs, _ := a.Directives()
	op := dirs["op"]
	if run, ok := reproducers[op]; ok {
		return run(t, a)
	}
	t.Fatalf("%s: unknown #op %q", a.Path, op)
	return nil
}

// reproducers run one operation over an archive's inputs. Operations that
// need the thema root are registered by the tests that migrate each source
// of errors.
var reproducers = map[string]func(t *testing.T, a *diagtest.Archive) []diag.Diagnostic{
	// unify: compile every .cue input, unify them in order, and report the
	// result's errors through From with no context: the raw CUE path.
	"unify": func(t *testing.T, a *diagtest.Archive) []diag.Diagnostic {
		ctx := cuecontext.New()
		var v cue.Value
		for _, f := range a.Inputs() {
			if !strings.HasSuffix(f.Name, ".cue") {
				continue
			}
			fv := ctx.CompileBytes(f.Data, cue.Filename(f.Name))
			if !v.Exists() {
				v = fv
			} else {
				v = v.Unify(fv)
			}
		}
		err := v.Validate(cue.Concrete(true))
		if err == nil {
			t.Fatal("unify produced no error")
		}
		return diag.From(err, diag.Context{})
	},
	// go-error: a plain Go error with the text of error.txt.
	"go-error": func(t *testing.T, a *diagtest.Archive) []diag.Diagnostic {
		text, ok := a.File("error.txt")
		if !ok {
			t.Fatal("go-error needs error.txt")
		}
		return diag.From(errors.New(strings.TrimSpace(text)), diag.Context{})
	},
	// panic: a panic with the text of panic.txt, recovered into E0901. The
	// stack is replaced, since it names this test's frames.
	"panic": func(t *testing.T, a *diagtest.Archive) []diag.Diagnostic {
		text, ok := a.File("panic.txt")
		if !ok {
			t.Fatal("panic needs panic.txt")
		}
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = diag.Internal(r)
				}
			}()
			panic(strings.TrimSpace(text))
		}()
		ds := diag.From(err, diag.Context{})
		for i := range ds {
			first, _, _ := strings.Cut(ds[i].Detail, "\n")
			ds[i].Detail = first + "\n\n<the goroutine stack>"
		}
		return ds
	},
}

func formOf(a *diagtest.Archive) diagtest.Form {
	for _, f := range a.Inputs() {
		if strings.HasSuffix(f.Name, ".thema") {
			return diagtest.Authoring
		}
	}
	return diagtest.Normal
}

func renderText(ds []diag.Diagnostic, a *diagtest.Archive) string {
	var b bytes.Buffer
	diag.Render(&b, ds, diag.RenderOptions{Source: a.Source})
	return b.String()
}

func renderJSON(t *testing.T, ds []diag.Diagnostic) string {
	t.Helper()
	var b bytes.Buffer
	if err := diag.RenderJSON(&b, ds); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
