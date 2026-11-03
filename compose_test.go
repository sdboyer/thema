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
	for _, tc := range []struct {
		name     string
		pkg      string
		path     cue.Path
		headline string
	}{
		{"empty package name", "", cue.Path{}, `"" cannot name a composed package`},
		{"package name with a digit first", "1x", cue.Path{}, `"1x" cannot name a composed package`},
		{"definition as package name", "#x", cue.Path{}, `"#x" cannot name a composed package`},
		{"hidden package name", "_x", cue.Path{}, `"_x" cannot name a composed package`},
		{"index", "p", cue.ParsePath("a[0]"), "placement 0's path a[0] cannot be written as fields"},
		{"optional", "p", cue.MakePath(cue.Str("a").Optional()), "placement 0's path a? cannot be written as fields"},
		{"required", "p", cue.MakePath(cue.Str("a").Required()), "placement 0's path a! cannot be written as fields"},
		{"pattern", "p", cue.MakePath(cue.AnyString), "placement 0's path [_] cannot be written as fields"},
		{"hidden", "p", cue.MakePath(cue.Hid("_a", "p")), "placement 0's path _a cannot be written as fields"},
		{"invalid", "p", cue.ParsePath("a.b c"), "placement 0's path is not a valid path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := thema.Compose(tc.pkg, thema.Placement{Path: tc.path, File: f})
			ds := diag.Diagnostics(err)
			if len(ds) != 1 || ds[0].Code != diag.E0627 || ds[0].Headline != tc.headline {
				t.Errorf("got %v, want one E0627 %q", ds, tc.headline)
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
