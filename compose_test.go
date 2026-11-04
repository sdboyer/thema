package thema_test

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
)

func TestComposeFiles(t *testing.T) {
	src := `// about the lineage
package elsewhere

import (
	"strings"
)

// the lineage
name: "q"
loud: strings.ToUpper(name)
`
	f, err := parser.ParseFile("q.cue", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	before := formatted(t, f)

	files, err := thema.Compose("lineages",
		thema.Placement{Path: cue.MakePath(cue.Str("ops"), cue.Def("#Op"), cue.Str("content-type")), File: f},
		thema.Placement{File: f},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := formatted(t, f); got != before {
		t.Errorf("Compose changed the placement's file:\n%s", got)
	}

	want := []string{`// about the lineage
package lineages

import (
	"strings"
)

ops: #Op: "content-type": {

	// the lineage
	name: "q"
	loud: strings.ToUpper(name)
}
`, `// about the lineage
package lineages

import (
	"strings"
)

// the lineage
name: "q"
loud: strings.ToUpper(name)
`}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d", len(files), len(want))
	}
	for i, file := range files {
		if wantName := "thema_compose_" + string(rune('0'+i)) + ".cue"; file.Filename != wantName {
			t.Errorf("file %d is named %s, want %s", i, file.Filename, wantName)
		}
		if got := formatted(t, file); got != want[i] {
			t.Errorf("file %d:\n%s\nwant:\n%s", i, got, want[i])
		}
	}
}

func TestComposeRefused(t *testing.T) {
	f, _ := parser.ParseFile("q.cue", "a: 1")
	imports, _ := parser.ParseFile("i.cue", "import (\n\t\"strings\"\n\ts \"list\"\n)\n\na: strings.ToUpper(\"q\")\nb: s.Concat([[1]])\n")
	one := []thema.Placement{{File: f}}
	at := func(p cue.Path) []thema.Placement { return []thema.Placement{{Path: p, File: f}} }
	for _, tc := range []struct {
		name     string
		pkg      string
		ps       []thema.Placement
		headline string
	}{
		{"empty package name", "", one, `a composed package is named by an identifier, and "" is not one`},
		{"package name with a digit first", "1x", one, `a composed package is named by an identifier, and "1x" is not one`},
		{"definition as package name", "#x", one, `a composed package is named by an identifier, and "#x" is not one`},
		{"hidden package name", "_x", one, `a composed package is named by an identifier, and "_x" is not one`},
		{"keyword as package name", "if", one, `a composed package is named by an identifier, and "if" is not one`},
		{"literal as package name", "true", one, `a composed package is named by an identifier, and "true" is not one`},
		{"no placements", "p", nil, "a composed package declares at least one placement, and none was given"},
		{"index", "p", at(cue.ParsePath("a[0]")), "a placement's path is made of field and definition labels, and placement 0's path a[0] is not"},
		{"optional", "p", at(cue.MakePath(cue.Str("a").Optional())), "a placement's path is made of field and definition labels, and placement 0's path a? is not"},
		{"required", "p", at(cue.MakePath(cue.Str("a").Required())), "a placement's path is made of field and definition labels, and placement 0's path a! is not"},
		{"pattern", "p", at(cue.MakePath(cue.AnyString)), "a placement's path is made of field and definition labels, and placement 0's path [_] is not"},
		{"hidden", "p", at(cue.MakePath(cue.Hid("_a", "p"))), "a placement's path is made of field and definition labels, and placement 0's path _a is not"},
		{"invalid", "p", at(cue.ParsePath("a.b c")), "a placement's path is a valid CUE path, and placement 0's has an error"},
		{"an import's name", "p", []thema.Placement{{Path: cue.ParsePath("ops.strings"), File: imports}}, "a placement's path does not name its file's imports, and placement 0's path ops.strings names strings"},
		{"an import's own name", "p", []thema.Placement{{Path: cue.ParsePath("s"), File: imports}}, "a placement's path does not name its file's imports, and placement 0's path s names s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := thema.Compose(tc.pkg, tc.ps...)
			ds := diag.Diagnostics(err)
			if len(ds) != 1 || ds[0].Code != diag.E0627 || ds[0].Headline != tc.headline {
				t.Errorf("got %v, want one E0627 %q", ds, tc.headline)
			}
		})
	}
}

// An attribute at the top of a placement's file is refused wherever it is:
// before the package clause or leading a file without one, CUE's loader reads
// it as deciding whether the file is loaded. An attribute of a field, or
// inside a struct, is the file's content, and is kept.
func TestComposeAttributes(t *testing.T) {
	for _, tc := range []struct{ name, src, attr string }{
		{"before the package clause", "@extern(embed)\n\npackage q\n\na: 1\n", "@extern(embed)"},
		{"leading a file without a package clause", "@if(debug)\n\na: 1\n", "@if(debug)"},
		{"among the imports", "package q\n\n@x()\n\nimport \"strings\"\n\na: strings.ToUpper(\"q\")\n", "@x()"},
		{"among the fields", "a: 1\n\n@x()\n\nb: 2\n", "@x()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parser.ParseFile("q.cue", tc.src)
			if err != nil {
				t.Fatal(err)
			}
			_, err = thema.Compose("p", thema.Placement{Path: cue.ParsePath("at"), File: f})
			ds := diag.Diagnostics(err)
			want := "a placement's file declares no attributes at its top level, and placement 0's file declares " + tc.attr
			if len(ds) != 1 || ds[0].Code != diag.E0627 || ds[0].Headline != want {
				t.Errorf("got %v, want one E0627 %q", ds, want)
			}
		})
	}

	f, err := parser.ParseFile("q.cue", "a: int @x()\nb: {\n\t@y()\n\tc: 1\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	files, err := thema.Compose("p", thema.Placement{Path: cue.ParsePath("at"), File: f})
	if err != nil {
		t.Fatal(err)
	}
	want := `package p

at: {
	a: int @x()
	b: {
		@y()
		c: 1
	}
}
`
	if got := formatted(t, files[0]); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func formatted(t *testing.T, f *ast.File) string {
	t.Helper()
	b, err := format.Node(f)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimLeft(string(b), "\n")
}
