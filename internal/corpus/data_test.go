package corpus

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/encoding/gocode"
	"github.com/sdboyer/thema/internal/corpus/datatypes"
	"github.com/sdboyer/thema/internal/diagtest"
	"github.com/sdboyer/thema/internal/util"
	"github.com/sdboyer/thema/load"
	"github.com/sdboyer/thema/vmux"
)

// TestData runs the data corpus in testdata/data: each archive is one lineage
// in the authoring form, and each of its inputs (data/*.json) a case, which
// every suite takes from JSON to the Go type generated for the schema at
// #version. Every suite must give the verdict docs/translation.md gives
// ("What an instance may hold"), and a Go value that holds what the instance
// does. testdata/data/README.md says how to add a case and how to read a
// failure.
func TestData(t *testing.T) {
	Corpus{Dir: dataDir, Root: repoRoot, Layout: dataLayout, Op: diag.OpValidate, Suites: dataSuites, Check: checkVersion}.Run(t)
}

// checkVersion fails a case whose #version is written for its input alone:
// an archive's inputs are data of one schema, whose Go type is generated
// once.
func checkVersion(t testing.TB, c *Case) {
	t.Helper()
	if k := c.directiveKey("version"); k != "version" {
		t.Fatalf("case %s: #%s: an archive's inputs are data of one schema; write #version once, for every input", c.Name, k)
	}
}

const (
	// repoRoot is the repository's root, from this package's directory.
	repoRoot = "../.."
	dataDir  = repoRoot + "/testdata/data"
	// dataTypesDir holds the Go types generated for each archive.
	dataTypesDir = "datatypes"
)

// dataLayout is how the data corpus is read: each input is a case, and an
// archive says which schema of its lineage the inputs are data of.
var dataLayout = Layout{Inputs: "data/*.json", Require: []string{"version"}}

// The suites of the data corpus, in registry order: the ways a program takes
// JSON to a Go value of a schema's type.
var dataSuites = []Suite{
	{
		Name: "cue",
		Description: "The input is decoded as JSON (vmux), validated against the schema with ValidateTyped, and taken to the Go type with Value. " +
			"This is how a program gets a Go value from JSON with thema today.",
		Run: func(t testing.TB, c *Case) (string, error) {
			sch, typed := dataSchema(t, c)
			raw, _ := c.File(c.InputFile)
			data, err := vmux.NewJSONCodec(c.InputFile).Decode(cuecontext.New(), []byte(raw))
			if err != nil {
				return "", notJSON(c.InputFile, err)
			}
			v, err := typed.Value(data)
			if err != nil {
				return "", err
			}
			return valueLine(t, sch, data, typed, v)
		},
	},
}

// notJSON is the diagnostic for an input that does not parse, as the CLI
// reports it.
func notJSON(file string, err error) error {
	b := diag.New(diag.E0507).Headlinef("%s is not valid JSON", file)
	var ce cerrors.Error
	if errors.As(err, &ce) {
		b.At(ce.Position())
	}
	return b.Detail(err.Error()).Err()
}

// valueLine renders v, the Go value a suite produced from data, as the line
// its section records, once it is checked against the instance: CUE's value
// of data unified with the schema, every default filled, decoded onto a new
// value of the type by name, exactly as each field's tag gives it. The two
// are compared as JSON, so that a value the type holds as any compares by
// what it is, not by the Go type a decoder chose for it. A Go value that
// differs has lost or changed what the instance holds, of what the type has
// a place for, and the suite's result is wrong ([Wrong]).
func valueLine(t testing.TB, sch thema.Schema, data cue.Value, typed datatypes.Typed, v any) (string, error) {
	t.Helper()
	got, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("the Go value does not marshal: %v", err)
	}
	inst, err := sch.Underlying().LookupPath(cue.MakePath(cue.Hid("_schema", util.ThemaPackageID))).Unify(data).MarshalJSON()
	if err != nil {
		t.Fatalf("the valid instance, with the schema's defaults, does not marshal: %v", err)
	}
	ref, err := typed.FromJSON(inst)
	if err != nil {
		t.Fatalf("the instance does not decode onto the Go type: %v", err)
	}
	want, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("the instance's Go value does not marshal: %v", err)
	}
	if string(got) != string(want) {
		return "", &Wrong{Detail: fmt.Sprintf("the Go value is %s\nthe instance, decoded onto the type, is %s", got, want)}
	}
	return "value: " + string(got), nil
}

// dataLineages caches each archive's lineage and schema, by archive path.
var dataLineages = map[string]struct {
	sch   thema.Schema
	typed datatypes.Typed
	err   error
}{}

// dataSchema returns the schema at c's #version, of the lineage in c's
// archive, and the typed API bound to the Go type generated for it.
func dataSchema(t testing.TB, c *Case) (thema.Schema, datatypes.Typed) {
	t.Helper()
	got, ok := dataLineages[c.Path]
	if !ok {
		got.sch, got.err = schemaAt(c)
		if got.err == nil {
			bind, ok := datatypes.Registry[dataTypesPackage(c.archive.Name)]
			if !ok {
				got.err = fmt.Errorf("no Go types are generated for %s; run THEMA_UPDATE_GOLDEN=1 go test ./internal/corpus -run TestDataTypes", c.archive.Name)
			} else {
				got.typed, got.err = bind(got.sch)
			}
		}
		dataLineages[c.Path] = got
	}
	if got.err != nil {
		t.Fatalf("case %s: %v", c.Name, got.err)
	}
	return got.sch, got.typed
}

// schemaAt binds the lineage of c's archive, and returns its schema at
// #version.
func schemaAt(c *Case) (thema.Schema, error) {
	dir, err := lineageDir(c)
	if err != nil {
		return nil, err
	}
	fsys, err := txtar.FS(&txtar.Archive{Files: c.Inputs()})
	if err != nil {
		return nil, err
	}
	lin, err := load.AuthoringLineage(fsys, dir, thema.NewRuntime(cuecontext.New()))
	if err != nil {
		return nil, fmt.Errorf("the lineage in %s does not bind: %w", dir, err)
	}
	v, err := thema.ParseSyntacticVersion(c.Directive("version"))
	if err != nil {
		return nil, fmt.Errorf("#version: %w", err)
	}
	return lin.Schema(v)
}

// lineageDir is the directory of c's archive that holds the lineage's
// authoring files: the one directory with a history/ of .thema files.
func lineageDir(c *Case) (string, error) {
	var dirs []string
	for _, f := range c.Inputs() {
		if ok, _ := path.Match("*/history/*.thema", f.Name); ok {
			if d, _, _ := strings.Cut(f.Name, "/"); !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
	}
	if len(dirs) != 1 {
		return "", fmt.Errorf("the archive must hold one lineage, as <name>/history/<version>.thema; it holds %d", len(dirs))
	}
	return dirs[0], nil
}

// dataTypesPackage is the name of the Go package generated for the archive
// name: the name without its dashes, so that it is an identifier.
func dataTypesPackage(archive string) string { return strings.ReplaceAll(archive, "-", "") }

// TestDataTypes checks that internal/corpus/datatypes holds the Go types
// SchemaTypes generates for the schema at #version of each archive of the
// data corpus, and the registry that binds them; THEMA_UPDATE_GOLDEN=1
// rewrites them. When an archive can't be generated, nothing else is
// judged or written: a registry missing it would not say why, and one naming
// a package that is not there would not compile, so no test could repair it.
func TestDataTypes(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join(dataDir, "*.txtar"))
	if len(paths) == 0 {
		t.Fatalf("no archives in %s", dataDir)
	}
	var pkgs []dataPackage
	files := map[string]string{}
	for _, p := range paths {
		pkg, srcs, err := generateDataTypes(p, pkgs)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
			continue
		}
		pkgs = append(pkgs, pkg)
		maps.Copy(files, srcs)
	}
	if t.Failed() {
		t.Fatalf("no types are judged or written until every archive generates")
	}
	files["registry.go"] = dataRegistry(t, pkgs)
	for name, src := range files {
		diagtest.CompareFile(t, filepath.Join(dataTypesDir, name), src)
	}
	// What is generated is all that may be there, beside typed.go.
	err := filepath.WalkDir(dataTypesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dataTypesDir, p)
		if _, ok := files[filepath.ToSlash(rel)]; !ok && rel != "typed.go" {
			t.Errorf("%s is not generated for any archive of %s; remove it", p, dataDir)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// generateDataTypes generates the Go package for the archive at p, as the
// files it holds, by their paths under datatypes, given the packages of the
// archives before it.
func generateDataTypes(p string, before []dataPackage) (dataPackage, map[string]string, error) {
	a, err := ReadArchive(p, dataLayout)
	if err != nil {
		return dataPackage{}, nil, err
	}
	var suites []string
	for _, su := range dataSuites {
		suites = append(suites, su.Name)
	}
	if d, _ := a.Problems(suites); len(a.Cases) == 0 || len(d) > 0 {
		return dataPackage{}, nil, fmt.Errorf("the archive breaks the format, which TestData reports: %q", d)
	}
	name := dataTypesPackage(a.Name)
	switch {
	case token.IsKeyword(name), name == "thema", name == "datatypes":
		return dataPackage{}, nil, fmt.Errorf("its package would be %s, which is a Go keyword or a name the registry uses; rename the archive", name)
	case slices.ContainsFunc(before, func(o dataPackage) bool { return o.name == name }):
		return dataPackage{}, nil, fmt.Errorf("its package would be %s, as another archive's is; rename one", name)
	}
	sch, err := schemaAt(a.Cases[0])
	if err != nil {
		return dataPackage{}, nil, err
	}
	fl, err := gocode.SchemaTypes(gocode.TypeConfig{PackageName: name}).Generate(sch)
	if err != nil {
		return dataPackage{}, nil, err
	}
	srcs := map[string]string{}
	for _, f := range fl {
		src, err := f.Render()
		if err != nil {
			return dataPackage{}, nil, err
		}
		srcs[name+"/"+f.Path()] = string(src)
	}
	return dataPackage{name: name, root: rootName(sch)}, srcs, nil
}

// rootName is the name SchemaTypes gives sch's root type when TypeConfig
// names none: the lineage's name, its first letter capitalized.
func rootName(sch thema.Schema) string {
	r, size := utf8.DecodeRuneInString(sch.Lineage().Name())
	return string(unicode.ToUpper(r)) + sch.Lineage().Name()[size:]
}

// dataPackage is the Go package generated for an archive, and the name of
// its root type.
type dataPackage struct{ name, root string }

// dataRegistry renders registry.go, binding each package's root type.
func dataRegistry(t *testing.T, pkgs []dataPackage) string {
	t.Helper()
	var imports, entries strings.Builder
	for _, pkg := range pkgs {
		fmt.Fprintf(&imports, "\t%q\n", "github.com/sdboyer/thema/internal/corpus/datatypes/"+pkg.name)
		fmt.Fprintf(&entries, "\t%q: Bind[%s.%s],\n", pkg.name, pkg.name, pkg.root)
	}
	src := "// Code generated by TestDataTypes in internal/corpus. DO NOT EDIT.\n\n" +
		"package datatypes\n\n" +
		"import (\n\t\"github.com/sdboyer/thema\"\n\n" + imports.String() + ")\n\n" +
		"// Registry binds the Go types generated for each archive of the data\n" +
		"// corpus to its schema, by the archive's package name.\n" +
		"var Registry = map[string]func(thema.Schema) (Typed, error){\n" + entries.String() + "}\n"
	b, err := format.Source([]byte(src))
	if err != nil {
		t.Fatalf("registry.go does not format: %v\n%s", err, src)
	}
	return string(b)
}
