package diag_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cjson "cuelang.org/go/encoding/json"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compile"
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
	// validate: bind lineage.cue (a normal form that does not import thema),
	// validate data.json against the version #version names (default: the
	// latest), and report the failure.
	"validate": func(t *testing.T, a *diagtest.Archive) []diag.Diagnostic {
		dirs, _ := a.Directives()
		ctx := cuecontext.New()
		rt := thema.NewRuntime(ctx)
		src, ok := a.File("lineage.cue")
		if !ok {
			t.Fatal("validate needs lineage.cue")
		}
		lin, err := thema.BindLineage(ctx.CompileString(src, cue.Filename("lineage.cue")), rt)
		if err != nil {
			t.Fatalf("lineage.cue does not bind: %v", err)
		}
		sch := lin.Latest()
		if v, ok := dirs["version"]; ok {
			sv, err := thema.ParseSyntacticVersion(v)
			if err != nil {
				t.Fatal(err)
			}
			if sch, err = lin.Schema(sv); err != nil {
				t.Fatal(err)
			}
		}
		var data cue.Value
		if src, ok := a.File("data.cue"); ok {
			data = ctx.CompileString(src, cue.Filename("data.cue"))
		} else {
			text, ok := a.File("data.json")
			if !ok {
				t.Fatal("validate needs data.json or data.cue")
			}
			expr, err := cjson.Extract("data.json", []byte(text))
			if err != nil {
				t.Fatal(err)
			}
			data = ctx.BuildExpr(expr)
		}
		_, err = sch.Validate(data)
		if err == nil {
			t.Fatal("the data is valid")
		}
		return diag.Diagnostics(err)
	},
	// bind: bind lineage.cue (a normal form that does not import thema) and
	// report the failure. With #path, the lineage is looked up there first.
	"bind": func(t *testing.T, a *diagtest.Archive) []diag.Diagnostic {
		dirs, _ := a.Directives()
		ctx := cuecontext.New()
		rt := thema.NewRuntime(ctx)
		src, ok := a.File("lineage.cue")
		if !ok {
			t.Fatal("bind needs lineage.cue")
		}
		v := ctx.CompileString(src, cue.Filename("lineage.cue"))
		if p, ok := dirs["path"]; ok {
			v = v.LookupPath(cue.ParsePath(p))
		}
		var opts []thema.BindOption
		if dirs["skip-buggy-checks"] == "true" {
			opts = append(opts, thema.SkipBuggyChecks())
		}
		_, err := thema.BindLineage(v, rt, opts...)
		if err == nil {
			t.Fatal("the lineage bound")
		}
		return diag.Diagnostics(err)
	},
	// translate: bind lineage.cue (with #skip-buggy-checks, when the totality
	// checker would refuse the lens at bind), validate data.json against
	// #from, and translate it to #to.
	"translate": func(t *testing.T, a *diagtest.Archive) []diag.Diagnostic {
		dirs, _ := a.Directives()
		ctx := cuecontext.New()
		rt := thema.NewRuntime(ctx)
		src, ok := a.File("lineage.cue")
		if !ok {
			t.Fatal("translate needs lineage.cue")
		}
		var opts []thema.BindOption
		if dirs["skip-buggy-checks"] == "true" {
			opts = append(opts, thema.SkipBuggyChecks())
		}
		lin, err := thema.BindLineage(ctx.CompileString(src, cue.Filename("lineage.cue")), rt, opts...)
		if err != nil {
			t.Fatalf("lineage.cue does not bind: %v", err)
		}
		from, err := thema.ParseSyntacticVersion(dirs["from"])
		if err != nil {
			t.Fatal(err)
		}
		to, err := thema.ParseSyntacticVersion(dirs["to"])
		if err != nil {
			t.Fatal(err)
		}
		text, ok := a.File("data.json")
		if !ok {
			t.Fatal("translate needs data.json")
		}
		expr, err := cjson.Extract("data.json", []byte(text))
		if err != nil {
			t.Fatal(err)
		}
		inst, err := thema.SchemaP(lin, from).Validate(ctx.BuildExpr(expr))
		if err != nil {
			t.Fatalf("data.json is not an instance of %s: %v", from, err)
		}
		_, _, err = inst.Translate(to)
		if err == nil {
			t.Fatal("the instance translated")
		}
		return diag.Diagnostics(err)
	},
	// compile: compile the authoring files under the archive's one directory,
	// whose name is the lineage's, and report what fails. When the files
	// compile, the normal form is bound with the authoring vocabulary and
	// bind's diagnostics are reported instead.
	"compile": func(t *testing.T, a *diagtest.Archive) []diag.Diagnostic {
		dirs, _ := a.Directives()
		name := dirs["name"]
		var files []compile.File
		for _, f := range a.Inputs() {
			dir, rest, ok := strings.Cut(f.Name, "/")
			if !ok {
				continue
			}
			if name == "" {
				name = dir
			}
			files = append(files, compile.File{Path: rest, Text: f.Data})
		}
		if name == "" {
			t.Fatal("compile needs authoring files under the lineage's directory")
		}
		ctx := cuecontext.New()
		c, err := compile.Compile(ctx, name, files)
		if err != nil {
			return diag.Diagnostics(err)
		}
		_, err = thema.BindLineage(ctx.BuildFile(c.File), thema.NewRuntime(ctx), thema.WithVocabulary(compile.Vocabulary(c)))
		if err == nil {
			t.Fatal("the authoring files compiled and bound")
		}
		return diag.Diagnostics(err)
	},
	// decompile: bind lineage.cue and decompile it to the authoring form.
	"decompile": func(t *testing.T, a *diagtest.Archive) []diag.Diagnostic {
		ctx := cuecontext.New()
		src, ok := a.File("lineage.cue")
		if !ok {
			t.Fatal("decompile needs lineage.cue")
		}
		v := ctx.CompileString(src, cue.Filename("lineage.cue"))
		if _, err := thema.BindLineage(v, thema.NewRuntime(ctx)); err != nil {
			t.Fatalf("lineage.cue does not bind: %v", err)
		}
		_, _, err := compile.Decompile(v)
		if err == nil {
			t.Fatal("the lineage decompiled")
		}
		return diag.Diagnostics(err)
	},
	// append-only: bind old.cue and new.cue and check that new only appends
	// to old (thema.IsAppendOnly).
	"append-only": func(t *testing.T, a *diagtest.Archive) []diag.Diagnostic {
		ctx := cuecontext.New()
		rt := thema.NewRuntime(ctx)
		bind := func(name string) thema.Lineage {
			src, ok := a.File(name)
			if !ok {
				t.Fatalf("append-only needs %s", name)
			}
			lin, err := thema.BindLineage(ctx.CompileString(src, cue.Filename(name)), rt)
			if err != nil {
				t.Fatalf("%s does not bind: %v", name, err)
			}
			return lin
		}
		err := thema.IsAppendOnly(bind("old.cue"), bind("new.cue"))
		if err == nil {
			t.Fatal("the new lineage only appends")
		}
		return diag.Diagnostics(err)
	},
	// schema: bind lineage.cue and look up the version #version names.
	"schema": func(t *testing.T, a *diagtest.Archive) []diag.Diagnostic {
		dirs, _ := a.Directives()
		ctx := cuecontext.New()
		rt := thema.NewRuntime(ctx)
		src, ok := a.File("lineage.cue")
		if !ok {
			t.Fatal("schema needs lineage.cue")
		}
		lin, err := thema.BindLineage(ctx.CompileString(src, cue.Filename("lineage.cue")), rt)
		if err != nil {
			t.Fatalf("lineage.cue does not bind: %v", err)
		}
		sv, err := thema.ParseSyntacticVersion(dirs["version"])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = lin.Schema(sv); err == nil {
			t.Fatalf("the lineage has schema %s", sv)
		}
		return diag.Diagnostics(err)
	},
	// cli: run the thema command with the arguments of #args, from a directory
	// holding the archive's files, and read its JSON diagnostics. Nothing is
	// on stdin. Positions beneath that directory are made relative to it.
	"cli": func(t *testing.T, a *diagtest.Archive) []diag.Diagnostic {
		dirs, _ := a.Directives()
		args, ok := dirs["args"]
		if !ok {
			t.Fatal("cli needs #args")
		}
		dir := t.TempDir()
		for _, f := range a.Inputs() {
			p := filepath.Join(dir, f.Name)
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, f.Data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command(themaBinary(t), append(strings.Fields(args), "--diag-format", "json")...) //nolint:gosec // the arguments are the test's
		cmd.Dir = dir
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if err == nil {
			t.Fatalf("thema %s succeeded:\n%s", args, stdout.String())
		}
		var out struct {
			Diagnostics []diag.Diagnostic `json:"diagnostics"`
		}
		if jerr := json.Unmarshal(stdout.Bytes(), &out); jerr != nil {
			t.Fatalf("thema %s: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
		// The loader's positions are absolute; the archive's files are not.
		rel := func(p *diag.Pos) {
			if p != nil {
				p.File = strings.TrimPrefix(p.File, dir+string(filepath.Separator))
			}
		}
		for i := range out.Diagnostics {
			d := &out.Diagnostics[i]
			rel(d.Primary)
			for j := range d.Notes {
				rel(d.Notes[j].Pos)
			}
		}
		return out.Diagnostics
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

var (
	themaOnce sync.Once
	themaBin  string
	themaErr  error
)

// themaBinary builds cmd/thema once per test run and returns its path.
func themaBinary(t *testing.T) string {
	t.Helper()
	themaOnce.Do(func() {
		dir, err := os.MkdirTemp("", "thema-cli")
		if err != nil {
			themaErr = err
			return
		}
		themaBin = filepath.Join(dir, "thema")
		// go test caches by the files a test reads; reading the command's
		// sources here makes a change to them rerun these tests.
		entries, err := os.ReadDir("../cmd/thema")
		if err != nil {
			themaErr = err
			return
		}
		for _, e := range entries {
			if _, err := os.Stat(filepath.Join("../cmd/thema", e.Name())); err != nil {
				themaErr = err
				return
			}
		}
		cmd := exec.Command("go", "build", "-o", themaBin, "../cmd/thema")
		if out, err := cmd.CombinedOutput(); err != nil {
			themaErr = fmt.Errorf("building cmd/thema: %v\n%s", err, out)
		}
	})
	if themaErr != nil {
		t.Fatal(themaErr)
	}
	return themaBin
}

func formOf(a *diagtest.Archive) diagtest.Form {
	for _, f := range a.Inputs() {
		if strings.HasSuffix(f.Name, ".thema") {
			return diagtest.Authoring
		}
	}
	return diagtest.Normal
}

// renderText renders with the archive's files as sources. Authoring files are
// stored under the lineage's directory but positioned by their path within it.
func renderText(ds []diag.Diagnostic, a *diagtest.Archive) string {
	var b bytes.Buffer
	diag.Render(&b, ds, diag.RenderOptions{Source: func(file string) ([]byte, bool) {
		if src, ok := a.Source(file); ok {
			return src, true
		}
		for _, f := range a.Inputs() {
			if strings.HasSuffix(f.Name, "/"+file) {
				return f.Data, true
			}
		}
		return nil, false
	}})
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
