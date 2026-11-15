package openapi

import (
	"bytes"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/format"
	upopenapi "cuelang.org/go/encoding/openapi"
	"github.com/sdboyer/thema/internal/jsonschema"
	"golang.org/x/tools/txtar"
)

// docMark is the line before each document in a TestGenerate section.
var docMark = regexp.MustCompile(`== (\S+\.json)\n`)

// extractDocs returns every OpenAPI document in thema's testdata, keyed by
// where it is: the fixtures in testdata/extract, and the documents
// TestGenerate records under testdata/lineage.
func extractDocs(t *testing.T) map[string][]byte {
	t.Helper()
	docs := map[string][]byte{}
	fixtures, err := filepath.Glob("testdata/extract/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range fixtures {
		docs[p] = readFixture(t, filepath.Base(p))
	}
	err = filepath.WalkDir("../../testdata/lineage", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".txtar" {
			return err
		}
		ar, err := txtar.ParseFile(p)
		if err != nil {
			return err
		}
		for _, f := range ar.Files {
			if !strings.HasPrefix(f.Name, "out/encoding/openapi/") {
				continue
			}
			// A section holds a document per version, each after a
			// "== <version>.json" line, which may follow the previous
			// document's closing brace on its line.
			data := string(f.Data)
			marks := docMark.FindAllStringSubmatchIndex(data, -1)
			for i, m := range marks {
				end := len(data)
				if i+1 < len(marks) {
					end = marks[i+1][0]
				}
				docs[p+"/"+f.Name+"/"+data[m[2]:m[3]]] = []byte(data[m[1]:end])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) < 20 {
		t.Fatalf("found %d OpenAPI documents, which is fewer than testdata holds", len(docs))
	}
	return docs
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

func formatFile(t *testing.T, f *ast.File) []byte {
	t.Helper()
	b, err := format.Node(f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestExtractMatchesUpstream checks that, but for the @openapi attributes,
// Extract writes exactly what cuelang.org/go/encoding/openapi.Extract writes,
// for every OpenAPI document in testdata and under each config: the fork
// changes nothing else, including the spellings that hang on the language
// version's experiments, which it hard-codes.
func TestExtractMatchesUpstream(t *testing.T) {
	cfgs := map[string]*ExtractConfig{
		"nilcfg": nil,
		"strict": {PkgName: "p", StrictFeatures: true, StrictKeywords: true},
	}
	for name, doc := range extractDocs(t) {
		for cname, cfg := range cfgs {
			t.Run(name+"/"+cname, func(t *testing.T) {
				v := cuecontext.New().CompileBytes(doc, cue.Filename(name))
				if err := v.Err(); err != nil {
					t.Fatal(err)
				}
				upcfg := &upopenapi.Config{}
				if cfg != nil {
					upcfg = &upopenapi.Config{PkgName: cfg.PkgName, StrictFeatures: cfg.StrictFeatures, StrictKeywords: cfg.StrictKeywords}
				}
				want, werr := upopenapi.Extract(v, upcfg)
				got, gerr := Extract(v, cfg)
				if (werr != nil) != (gerr != nil) || (werr != nil && werr.Error() != gerr.Error()) {
					t.Fatalf("Extract's error is %v, upstream's %v", gerr, werr)
				}
				if werr != nil {
					// Some recorded documents do not extract, and the
					// errors are compared; the fixtures must, so that
					// their comparison is of output.
					if strings.HasPrefix(name, "testdata/extract/") {
						t.Fatalf("upstream does not extract the fixture: %v", werr)
					}
					return
				}
				stripExtensions(got)
				if g, w := formatFile(t, got), formatFile(t, want); !bytes.Equal(g, w) {
					t.Errorf("Extract without its @openapi attributes differs from upstream's.\ngot:\n%s\nwant:\n%s", g, w)
				}
			})
		}
	}
}
