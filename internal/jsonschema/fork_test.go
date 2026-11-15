package jsonschema_test

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/token"
	"cuelang.org/go/encoding/json"
	upjsonschema "cuelang.org/go/encoding/jsonschema"
	"cuelang.org/go/encoding/yaml"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema/internal/jsonschema"
)

// componentsRoot is where an OpenAPI document's schemas are.
const componentsRoot = "#/components/schemas/"

// options are an archive's options, as upstream's decode test reads them:
// each line of its comment that begins with #, as #tag or #key: value.
type options map[string]string

func readOptions(comment []byte) options {
	o := options{}
	sc := bufio.NewScanner(bytes.NewReader(comment))
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "#")
		if !ok {
			continue
		}
		k, v, _ := strings.Cut(line, ":")
		o[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return o
}

func (o options) has(k string) bool { _, ok := o[k]; return ok }

// readSchema reads an archive's schema.json or schema.yaml, as upstream's
// decode test does.
func readSchema(ctx *cue.Context, fsys fs.FS) (cue.Value, error) {
	jsonData, jsonErr := fs.ReadFile(fsys, "schema.json")
	yamlData, yamlErr := fs.ReadFile(fsys, "schema.yaml")
	switch {
	case jsonErr == nil && yamlErr == nil:
		return cue.Value{}, fmt.Errorf("both schema.json and schema.yaml")
	case jsonErr == nil:
		expr, err := json.Extract("schema.json", jsonData)
		if err != nil {
			return cue.Value{}, err
		}
		return ctx.BuildExpr(expr), nil
	case yamlErr == nil:
		f, err := yaml.Extract("schema.yaml", yamlData)
		if err != nil {
			return cue.Value{}, err
		}
		return ctx.BuildFile(f), nil
	}
	return cue.Value{}, fmt.Errorf("no schema.json or schema.yaml")
}

// configs returns the config upstream's decode test uses for an archive
// with options o, as upstream's and as the fork's. The fork has no
// TargetLanguageVersion, so neither sets it: the fork is compared with
// upstream at the current language version, which is all it writes.
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
	if vers, ok := o["version"]; ok {
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
	if r, ok := o["root"]; ok {
		up.Root, fork.Root = r, r
	}
	if rs, ok := o["roots"]; ok {
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
	up.PkgName, fork.PkgName = o["pkgName"], o["pkgName"]
	return up, fork
}

// stripExtensions removes every @openapi attribute from f.
func stripExtensions(f *ast.File) {
	astutil.Apply(f, func(c astutil.Cursor) bool {
		if fd, ok := c.Node().(*ast.Field); ok {
			attrs := fd.Attrs[:0]
			for _, a := range fd.Attrs {
				if a.Name() != jsonschema.ExtensionsAttr {
					attrs = append(attrs, a)
				}
			}
			fd.Attrs = attrs
		}
		return true
	}, nil)
}

// extract formats what extract makes of v, or its error.
func extract(t *testing.T, f func() (*ast.File, error), strip bool) string {
	t.Helper()
	file, err := f()
	if err != nil {
		return "ERROR:\n" + errors.Details(err, nil)
	}
	if strip {
		stripExtensions(file)
	}
	b, err := format.Node(file)
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
// output is the same; with it, the same once the attributes are removed.
func TestExtractMatchesUpstream(t *testing.T) {
	paths, err := filepath.Glob("testdata/txtar/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 100 {
		t.Fatalf("found %d archives, which is fewer than upstream's corpus holds", len(paths))
	}
	for _, p := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(p), ".txtar"), func(t *testing.T) {
			ar, err := txtar.ParseFile(p)
			if err != nil {
				t.Fatal(err)
			}
			fsys, err := txtar.FS(ar)
			if err != nil {
				t.Fatal(err)
			}
			v, err := readSchema(cuecontext.New(), fsys)
			if err != nil {
				t.Fatal(err)
			}
			up, fork := configs(t, readOptions(ar.Comment), v)
			want := extract(t, func() (*ast.File, error) { return upjsonschema.Extract(v, up) }, false)
			if got := extract(t, func() (*ast.File, error) { return jsonschema.Extract(v, fork) }, false); got != want {
				t.Errorf("without Extensions, the fork writes:\n%s\nupstream writes:\n%s", got, want)
			}
			fork.Extensions = true
			if got := extract(t, func() (*ast.File, error) { return jsonschema.Extract(v, fork) }, true); got != want {
				t.Errorf("with Extensions, its attributes removed, the fork writes:\n%s\nupstream writes:\n%s", got, want)
			}
		})
	}
}
