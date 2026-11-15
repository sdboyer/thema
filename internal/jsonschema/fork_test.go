package jsonschema_test

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"
	"cuelang.org/go/encoding/json"
	upjsonschema "cuelang.org/go/encoding/jsonschema"
	"cuelang.org/go/encoding/yaml"
	"golang.org/x/tools/txtar"

	tastutil "github.com/sdboyer/thema/internal/astutil"
	"github.com/sdboyer/thema/internal/jsonschema"
)

// componentsRoot is where an OpenAPI document's schemas are.
const componentsRoot = "#/components/schemas/"

// options is an archive's comment, which holds its options. They're read as
// upstream's decode test reads them, through cuetxtar: a tag is a line that
// is #key once trimmed, and a value follows #key: at the start of the first
// line that has it.
type options []byte

func (o options) has(key string) bool {
	sc := bufio.NewScanner(bytes.NewReader(o))
	for sc.Scan() {
		if bytes.Equal(bytes.TrimSpace(sc.Bytes()), []byte("#"+key)) {
			return true
		}
	}
	return false
}

func (o options) value(key string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(o))
	for sc.Scan() {
		if rest, ok := bytes.CutPrefix(sc.Bytes(), []byte("#"+key+":")); ok {
			return string(bytes.TrimSpace(rest)), true
		}
	}
	return "", false
}

// knownOptions are the options upstream's decode test reads. noverify
// changes only how upstream's test checks its own output.
var knownOptions = map[string]bool{
	"version": true, "root": true, "roots": true, "strict": true,
	"strictFeatures": true, "strictKeywords": true, "allowNonExistentRoot": true,
	"openOnlyWhenExplicit": true, "singleRoot": true, "pkgName": true,
	"targetLanguageVersion": true, "noverify": true,
}

// optionLine matches a line cuetxtar would read as an option: a tag, once
// trimmed, or a value.
var optionLine = regexp.MustCompile(`^#(\w+)(:|$)`)

// unknown returns every option of o that knownOptions lacks, which this test
// would ignore.
func (o options) unknown() []string {
	var keys []string
	sc := bufio.NewScanner(bytes.NewReader(o))
	for sc.Scan() {
		line := sc.Bytes()
		if m := optionLine.FindSubmatch(bytes.TrimSpace(line)); m != nil && (string(m[2]) == "" || bytes.HasPrefix(line, []byte("#"))) {
			if !knownOptions[string(m[1])] {
				keys = append(keys, string(m[1]))
			}
		}
	}
	return keys
}

// readSchema reads an archive's schema.json or schema.yaml, as upstream's
// decode test does.
func readSchema(ctx *cue.Context, fsys fs.FS) (cue.Value, error) {
	jsonData, jsonErr := fs.ReadFile(fsys, "schema.json")
	yamlData, yamlErr := fs.ReadFile(fsys, "schema.yaml")
	var v cue.Value
	switch {
	case jsonErr == nil && yamlErr == nil:
		return cue.Value{}, fmt.Errorf("both schema.json and schema.yaml")
	case jsonErr == nil:
		expr, err := json.Extract("schema.json", jsonData)
		if err != nil {
			return cue.Value{}, err
		}
		v = ctx.BuildExpr(expr)
	case yamlErr == nil:
		f, err := yaml.Extract("schema.yaml", yamlData)
		if err != nil {
			return cue.Value{}, err
		}
		v = ctx.BuildFile(f)
	default:
		return cue.Value{}, fmt.Errorf("no schema.json or schema.yaml")
	}
	if err := v.Err(); err != nil {
		return cue.Value{}, err
	}
	return v, nil
}

// configs returns the config upstream's decode test uses for an archive
// with options o, as upstream's and as the fork's, except for
// targetLanguageVersion: the fork hard-codes the current language
// version's experiments and has no such option.
func configs(t *testing.T, o options, v cue.Value) (*upjsonschema.Config, *jsonschema.Config) {
	up, fork := &upjsonschema.Config{}, &jsonschema.Config{}
	mapRef := func(loc jsonschema.SchemaLoc) (string, cue.Path, error) {
		if !loc.IsLocal {
			return "", cue.Path{}, fmt.Errorf("external reference %v not supported", loc.ID)
		}
		sels := loc.Path.Selectors()
		return "", cue.MakePath(cue.Def(sels[len(sels)-1].Unquoted())), nil
	}
	upMapRef := func(loc upjsonschema.SchemaLoc) (string, cue.Path, error) {
		return mapRef(jsonschema.SchemaLoc{ID: loc.ID, IsLocal: loc.IsLocal, Path: loc.Path})
	}
	if vers, ok := o.value("version"); ok {
		switch vers {
		case "openapi":
			m := func(_ token.Pos, a []string) ([]ast.Label, error) {
				return []ast.Label{ast.NewIdent("#" + a[len(a)-1])}, nil
			}
			up.DefaultVersion, fork.DefaultVersion = upjsonschema.VersionOpenAPI, jsonschema.VersionOpenAPI
			up.Map, fork.Map = m, m //nolint:staticcheck // as upstream's test
			up.Root, fork.Root = componentsRoot, componentsRoot
			up.StrictKeywords, fork.StrictKeywords = true, true
		case "k8sAPI":
			up.DefaultVersion, fork.DefaultVersion = upjsonschema.VersionKubernetesAPI, jsonschema.VersionKubernetesAPI
			up.Root, fork.Root = componentsRoot, componentsRoot
			up.StrictKeywords, fork.StrictKeywords = true, true
		case "k8sCRD":
			if v.IncompleteKind() == cue.ListKind {
				t.Skip("as upstream's test: Extract does not extract CRDs from a list")
			}
			up.DefaultVersion, fork.DefaultVersion = upjsonschema.VersionKubernetesCRD, jsonschema.VersionKubernetesCRD
			r := "#/spec/versions/0/schema/openAPIV3Schema"
			up.Root, fork.Root = r, r
			up.StrictKeywords, fork.StrictKeywords = true, true
			up.SingleRoot, fork.SingleRoot = true, true
		default:
			uv, err := upjsonschema.ParseVersion(vers)
			if err != nil {
				t.Fatal(err)
			}
			fv, err := jsonschema.ParseVersion(vers)
			if err != nil {
				t.Fatal(err)
			}
			up.DefaultVersion, fork.DefaultVersion = uv, fv
		}
	}
	if r, ok := o.value("root"); ok {
		up.Root, fork.Root = r, r
	}
	if rs, ok := o.value("roots"); ok {
		up.Roots, fork.Roots = strings.Fields(rs), strings.Fields(rs)
		up.MapRef, fork.MapRef = upMapRef, mapRef
	}
	up.Strict, fork.Strict = o.has("strict"), o.has("strict")
	up.StrictFeatures, fork.StrictFeatures = o.has("strictFeatures"), o.has("strictFeatures")
	up.StrictKeywords = up.StrictKeywords || o.has("strictKeywords")
	fork.StrictKeywords = fork.StrictKeywords || o.has("strictKeywords")
	up.AllowNonExistentRoot, fork.AllowNonExistentRoot = o.has("allowNonExistentRoot"), o.has("allowNonExistentRoot")
	up.OpenOnlyWhenExplicit, fork.OpenOnlyWhenExplicit = o.has("openOnlyWhenExplicit"), o.has("openOnlyWhenExplicit")
	if o.has("singleRoot") {
		up.SingleRoot, fork.SingleRoot = true, true
	}
	up.PkgName, _ = o.value("pkgName")
	fork.PkgName = up.PkgName
	return up, fork
}

// golden returns what upstream's decode test expects Extract to write for
// the archive under cuecontext.New's evaluator: out/decode-v3/extract or,
// as cuetxtar falls back, out/decode/extract.
func golden(ar *txtar.Archive) (string, bool) {
	files := map[string]string{}
	for _, f := range ar.Files {
		files[f.Name] = string(f.Data)
	}
	for _, name := range []string{"out/decode-v3/extract", "out/decode/extract"} {
		if g, ok := files[name]; ok {
			return g, true
		}
	}
	return "", false
}

// extract formats the file f returns, or its error, as upstream's decode
// test does.
func extract(t *testing.T, f func() (*ast.File, error)) (*ast.File, string) {
	t.Helper()
	file, err := f()
	if err != nil {
		return nil, "ERROR:\n" + errors.Details(err, nil)
	}
	return file, formatFile(t, file)
}

func formatFile(t *testing.T, f *ast.File) string {
	t.Helper()
	b, err := format.Node(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestExtractMatchesUpstream checks that the fork writes exactly what
// cuelang.org/go/encoding/jsonschema.Extract writes, or fails as it does,
// for every archive of upstream's own decoder corpus (testdata/txtar, copied
// from cuelang.org/go v0.18.0-rc.3), under each archive's options: every
// JSON Schema dialect, OpenAPI, and Kubernetes. Without Extensions the
// output is the same; with it, it parses, attributes included, and is the
// same once they're removed, and the corpus holds some.
//
// The options are checked by upstream's output: under them, with the
// archive's targetLanguageVersion, it is the archive's golden. The fork is
// compared with upstream at the current language version, whose
// experiments it hard-codes, so an archive that targets an older one is
// compared at the current one as well.
func TestExtractMatchesUpstream(t *testing.T) {
	paths, err := filepath.Glob("testdata/txtar/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 100 {
		t.Fatalf("found %d archives, which is fewer than upstream's corpus holds", len(paths))
	}
	stripped := 0
	for _, p := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(p), ".txtar"), func(t *testing.T) {
			ar, err := txtar.ParseFile(p)
			if err != nil {
				t.Fatal(err)
			}
			o := options(ar.Comment)
			if u := o.unknown(); len(u) > 0 {
				t.Fatalf("options %v are not ones upstream's decode test reads, as this test knows it", u)
			}
			fsys, err := txtar.FS(ar)
			if err != nil {
				t.Fatal(err)
			}
			v, err := readSchema(cuecontext.New(), fsys)
			if err != nil {
				t.Fatal(err)
			}
			up, fork := configs(t, o, v)

			at := *up
			at.TargetLanguageVersion, _ = o.value("targetLanguageVersion")
			g, ok := golden(ar)
			if !ok {
				t.Fatal("the archive has no golden for upstream's Extract")
			}
			if _, got := extract(t, func() (*ast.File, error) { return upjsonschema.Extract(v, &at) }); strings.TrimSpace(got) != strings.TrimSpace(g) {
				t.Fatalf("upstream, under the options as read here, writes:\n%s\nits golden is:\n%s", got, g)
			}

			_, want := extract(t, func() (*ast.File, error) { return upjsonschema.Extract(v, up) })
			if _, got := extract(t, func() (*ast.File, error) { return jsonschema.Extract(v, fork) }); got != want {
				t.Errorf("without Extensions, the fork writes:\n%s\nupstream writes:\n%s", got, want)
			}
			fork.Extensions = true
			file, got := extract(t, func() (*ast.File, error) { return jsonschema.Extract(v, fork) })
			if file != nil {
				if _, err := parser.ParseFile(p, got); err != nil {
					t.Errorf("with Extensions, the fork's output does not parse: %v\n%s", err, got)
				}
				stripped += tastutil.StripAttrs(file, jsonschema.ExtensionsAttr)
				got = formatFile(t, file)
			}
			if got != want {
				t.Errorf("with Extensions, its attributes removed, the fork writes:\n%s\nupstream writes:\n%s", got, want)
			}
		})
	}
	if stripped == 0 {
		t.Error("with Extensions, the fork wrote no @openapi attribute for any archive, so that half compares output without them")
	}
}
