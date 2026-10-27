package cueview

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema/internal/envvars"
)

var views = []struct {
	name string
	fn   func(cue.Value) (ast.Node, error)
}{
	{"evaluated", Evaluated},
	{"raw", Raw},
	{"final", Final},
	{"data", Data},
	{"documented", Documented},
	{"selfcontained", SelfContained},
}

// TestViewGoldens records what every view produces for each case in
// testdata/*.txtar. Each archive's in.cue holds its cases as the fields of a
// top-level cases struct; anything else in in.cue is scope the cases refer to.
// The archive comment, and comments on the cases, say what is surprising.
//
// Set THEMA_UPDATE_GOLDEN=1 to rewrite the out/ sections, then review the diff:
// a change here is a change in how CUE presents values, which code relying on
// that view must be checked against.
func TestViewGoldens(t *testing.T) {
	files, err := filepath.Glob("testdata/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".txtar"), func(t *testing.T) {
			ar, err := txtar.ParseFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var in []byte
			for _, f := range ar.Files {
				if f.Name == "in.cue" {
					in = f.Data
				}
			}
			v := cuecontext.New().CompileBytes(in, cue.Filename("in.cue"))
			// Cases may deliberately hold errors, so only a missing cases struct (e.g.
			// from a syntax error) fails the test.
			cases := v.LookupPath(cue.MakePath(cue.Str("cases")))
			if !cases.Exists() {
				t.Fatalf("no cases in in.cue: %v", v.Err())
			}
			compareGoldens(t, file, ar, renderViews(t, cases))
		})
	}
}

// compareGoldens checks got against ar's out/ sections, or with
// THEMA_UPDATE_GOLDEN set, rewrites file with them.
func compareGoldens(t *testing.T, file string, ar *txtar.Archive, got map[string][]byte) {
	t.Helper()
	if envvars.UpdateGoldenFiles {
		var kept []txtar.File
		for _, f := range ar.Files {
			if !strings.HasPrefix(f.Name, "out/") {
				kept = append(kept, f)
			}
		}
		ar.Files = kept
		for _, name := range []string{"out/written", "out/evaluated", "out/raw", "out/final", "out/data", "out/documented", "out/selfcontained"} {
			ar.Files = append(ar.Files, txtar.File{Name: name, Data: got[name]})
		}
		if err := os.WriteFile(file, txtar.Format(ar), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, f := range ar.Files {
		want, ok := got[f.Name]
		if !ok {
			continue
		}
		if diff := cmp.Diff(string(f.Data), string(want)); diff != "" {
			t.Errorf("%s (-golden +got):\n%s", f.Name, diff)
		}
		delete(got, f.Name)
	}
	for name := range got {
		t.Errorf("missing golden section %s; run with %s=1", name, envvars.VarUpdateGolden)
	}
}

// renderViews renders every view of each case, by golden section name.
func renderViews(t *testing.T, cases cue.Value) map[string][]byte {
	t.Helper()
	got := map[string][]byte{"out/written": render(t, cases, func(c cue.Value) string {
		var b strings.Builder
		for i, n := range Written(c) {
			s := "<nil>"
			if n != nil {
				s = mustText(t, n)
			}
			fmt.Fprintf(&b, ">> operand %d\n%s\n", i, strings.TrimRight(s, "\n"))
		}
		return b.String()
	})}
	for _, vw := range views {
		got["out/"+vw.name] = render(t, cases, func(c cue.Value) string {
			n, err := vw.fn(c)
			if err != nil {
				return "error: " + strings.ReplaceAll(err.Error(), "\n", "\n  ") + "\n"
			}
			return mustText(t, n)
		})
	}
	return got
}

func render(t *testing.T, cases cue.Value, show func(cue.Value) string) []byte {
	t.Helper()
	iter, err := cases.Fields()
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	for iter.Next() {
		fmt.Fprintf(&b, "## %s\n%s\n", iter.Selector(), strings.TrimRight(show(iter.Value()), "\n"))
	}
	return b.Bytes()
}

func mustText(t *testing.T, n ast.Node) string {
	t.Helper()
	s, err := Text(n)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// SelfContained differs from Evaluated only in references into imported
// packages, which the goldens can't show: they compile a single file. Evaluated
// leaves shared.#Thing and shared.Reg as references, so their content isn't in
// its result. SelfContained binds a definition in a generated let and inlines a
// regular field. A builtin package stays a reference in both.
func TestSelfContainedInlinesImports(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{
		"cue.mod/module.cue": "module: \"example.com/m\"\nlanguage: version: \"v0.18.0\"\n",
		"shared/shared.cue":  "package shared\n#Thing: {p: int}\nReg: {r: int}\n",
		"m.cue": `package m
import (
	"example.com/m/shared"
	"strings"
)
v: {a: shared.#Thing, b: shared.Reg, c: strings.MinRunes(1)}
`,
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	insts := load.Instances([]string{"."}, &load.Config{Dir: dir})
	v := cuecontext.New().BuildInstance(insts[0]).LookupPath(cue.ParsePath("v"))
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		fn   func(cue.Value) (ast.Node, error)
		want string
	}{
		{Evaluated, `import (
	"example.com/m/shared"
	"strings"
)

a: shared.#Thing
b: shared.Reg
c: strings.MinRunes(1)
`},
		{SelfContained, `import "strings"

a: THING.#x
b: r: int
c: strings.MinRunes(1)

//cue:path: "example.com/m/shared".#Thing
let THING = {#x: p: int}
`},
	} {
		n, err := c.fn(v)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(c.want, mustText(t, n)); diff != "" {
			t.Errorf("(-want +got):\n%s", diff)
		}
	}
}
