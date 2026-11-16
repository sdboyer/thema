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
	// ran counts the cases run, so that the check on stripped is made only
	// when -run selects them all.
	docs := extractDocs(t)
	ran, stripped := 0, 0
	for name, doc := range docs {
		for cname, cfg := range cfgs {
			t.Run(name+"/"+cname, func(t *testing.T) {
				ran++
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
	if ran == len(docs)*len(cfgs) && stripped == 0 {
		t.Error("Extract wrote no @openapi attribute for any document, so the comparison is of output without them")
	}
}

// TestCommentBOM checks that text holding U+FEFF, which Extract writes into
// a comment, is written with U+2060 in its place, so that the output parses.
// CUE's scanner refuses U+FEFF anywhere but at the start of a file, and a
// comment has no escapes.
func TestCommentBOM(t *testing.T) {
	bom, wj := string(rune(0xfeff)), string(rune(0x2060))
	// Each BOM in a document is CUE's escape for U+FEFF.
	src := func(s string) string { return strings.ReplaceAll(s, "BOM", `\u`+"feff") }
	for name, c := range map[string]struct {
		doc     string
		extract func(v cue.Value) (*ast.File, error)
		// want is text the decoder writes into a comment.
		want []string
	}{
		"openapi": {
			doc: src(`{
				openapi: "3.0.0"
				info: {title: "tBOM", version: "1", description: "iBOMd"}
				paths: {}
				components: schemas: S: {type: "object", properties: p: {type: "string", description: "pBOMd"}}
			}`),
			extract: func(v cue.Value) (*ast.File, error) { return Extract(v, &ExtractConfig{PkgName: "p"}) },
			want:    []string{"t" + wj, "i" + wj + "d", "p" + wj + "d"},
		},
		"jsonschema": {
			doc:     src(`{type: "object", properties: p: {type: "string", title: "pBOMt", description: "pBOMd"}}`),
			extract: func(v cue.Value) (*ast.File, error) { return jsonschema.Extract(v, &jsonschema.Config{}) },
			want:    []string{"p" + wj + "t", "p" + wj + "d"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			v := cuecontext.New().CompileString(c.doc)
			if err := v.Err(); err != nil {
				t.Fatal(err)
			}
			f, err := c.extract(v)
			if err != nil {
				t.Fatal(err)
			}
			out := formatFile(t, f)
			if _, err := parser.ParseFile("p.cue", out); err != nil {
				t.Fatalf("Extract's output does not parse: %v\n%s", err, out)
			}
			var texts []string
			for _, cg := range ast.Comments(f) {
				texts = append(texts, cg.Text())
			}
			astutil.Apply(f, func(c astutil.Cursor) bool {
				for _, cg := range ast.Comments(c.Node()) {
					texts = append(texts, cg.Text())
				}
				return true
			}, nil)
			all := strings.Join(texts, "\n")
			if strings.Contains(all, bom) {
				t.Errorf("comments %q hold U+FEFF", all)
			}
			for _, w := range c.want {
				if !strings.Contains(all, w) {
					t.Errorf("comments %q: want %q, its U+FEFF written as U+2060", all, w)
				}
			}
		})
	}
}
