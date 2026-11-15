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
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	upopenapi "cuelang.org/go/encoding/openapi"
	tastutil "github.com/sdboyer/thema/internal/astutil"
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
// version's experiments, which it hard-codes. Extract's output parses as it
// is, attributes included, and the fixtures hold some.
func TestExtractMatchesUpstream(t *testing.T) {
	cfgs := map[string]*ExtractConfig{
		"nilcfg": nil,
		"strict": {PkgName: "p", StrictFeatures: true, StrictKeywords: true},
	}
	stripped := 0
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
				if _, err := parser.ParseFile(name, formatFile(t, got)); err != nil {
					t.Fatalf("Extract's output does not parse: %v", err)
				}
				stripped += tastutil.StripAttrs(got, jsonschema.ExtensionsAttr)
				if g, w := formatFile(t, got), formatFile(t, want); !bytes.Equal(g, w) {
					t.Errorf("Extract without its @openapi attributes differs from upstream's.\ngot:\n%s\nwant:\n%s", g, w)
				}
			})
		}
	}
	if stripped == 0 {
		t.Error("Extract wrote no @openapi attribute for any document, so the comparison is of output without them")
	}
}

// TestKnownGaps: what Extract does wrong today, each case naming the issue
// it waits on.
func TestKnownGaps(t *testing.T) {
	// A description holding U+FEFF is written raw into a doc comment, and
	// CUE's scanner refuses the rune anywhere but at the start of a file, so
	// the output doesn't parse. Upstream's Extract writes the same. A comment
	// has no escapes, so a fix changes the text or refuses it (#366).
	t.Run("U+FEFF in a description", func(t *testing.T) {
		v := cuecontext.New().Encode(oneProperty(map[string]any{
			"description": "a" + string(rune(0xfeff)) + "b",
		}))
		for name, extract := range map[string]func() (*ast.File, error){
			"Extract":          func() (*ast.File, error) { return Extract(v, nil) },
			"upstream Extract": func() (*ast.File, error) { return upopenapi.Extract(v, &upopenapi.Config{}) },
		} {
			f, err := extract()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			_, err = parser.ParseFile("p.cue", formatFile(t, f))
			if err == nil || !strings.Contains(err.Error(), "illegal byte order mark") {
				t.Errorf("%s's output: %v; want it refused for its byte order mark, until the issue is fixed", name, err)
			}
		}
	})
}
