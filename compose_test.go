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
	src := `@extern(embed)

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

	want := []string{`@extern(embed)
package lineages

import (
	"strings"
)

ops: #Op: "content-type": {

	// the lineage
	name: "q"
	loud: strings.ToUpper(name)
}
`, `@extern(embed)
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

// File attributes stay ahead of the new package clause, whether they precede
// an old one or lead a file without one, and what follows an old package
// clause, with the clause's comments, stays after the new one.
func TestComposeAttributes(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"after the package clause", `// about q
package q

@extern(embed)

import "strings"

a: strings.ToUpper("q")
`, `// about q
package p

@extern(embed)

import "strings"

at:
	a: strings.ToUpper("q")
`},
		{"without a package clause", `@if(debug)

a: 1
`, `@if(debug)
package p

at:
	a: 1
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parser.ParseFile("q.cue", tc.src, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			files, err := thema.Compose("p", thema.Placement{Path: cue.ParsePath("at"), File: f})
			if err != nil {
				t.Fatal(err)
			}
			got := formatted(t, files[0])
			if got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
			if _, err := parser.ParseFile("composed.cue", got); err != nil {
				t.Errorf("the composed file does not parse: %v", err)
			}
		})
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
