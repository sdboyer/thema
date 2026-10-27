package diag_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/diagdocs"
	"github.com/sdboyer/thema/internal/diagtest"
	"github.com/sdboyer/thema/internal/envvars"
)

// The menagerie is a catalogue of every shape of error the CUE evaluator
// produces that thema's shapes table and its operations depend on: for each,
// a minimal input and everything cue/errors exposes about the result. It is
// recorded in diag/testdata/menagerie.txtar and rendered to docs/cue-errors.md.
//
// menagerieDigest is the SHA-256 of the recorded shapes. A CUE upgrade that
// changes any of them, in wording, argument types, paths or positions, fails
// TestMenagerie here before it can silently stop a shape from matching. To
// accept a change: regenerate with THEMA_UPDATE_GOLDEN=1, read the diff of the
// archive and of docs/cue-errors.md, re-check every shape in shapes.go that
// the diff touches, then paste the digest the failure prints.
const menagerieDigest = "sha256:baa669a1e787a9f5793317eb841a783696c2cfc2795e75f62915f6134777dbae"

// A menagerieCase produces one error shape.
type menagerieCase struct {
	name    string
	section string
	// files are the CUE sources, by name; the names are the positions'
	// filenames. Files ending in .json are data.
	files []file
	// run produces the error from the compiled files, in order.
	run func(ctx *cue.Context, vals []cue.Value) error
}

type file struct{ name, text string }

var menagerie = []menagerieCase{
	// Conflicts between concrete values.
	{name: "conflict-string-string", section: "Conflict", files: []file{{"a.cue", `x: "hello"`}, {"b.cue", `x: "world"`}}, run: unify},
	{name: "conflict-string-int", section: "Conflict", files: []file{{"a.cue", `x: "hello"`}, {"b.cue", `x: 42`}}, run: unify},
	{name: "conflict-struct-scalar", section: "Conflict", files: []file{{"a.cue", `x: {a: 1}`}, {"b.cue", `x: "s"`}}, run: unify},
	{name: "conflict-list-element", section: "Conflict", files: []file{{"a.cue", `x: [1, 2]`}, {"b.cue", `x: [1, 3]`}}, run: unify},
	{name: "conflict-two-fields", section: "Conflict", files: []file{{"a.cue", "x: 1\ny: 2"}, {"b.cue", "x: 2\ny: 3"}}, run: unify},
	{name: "conflict-in-one-file", section: "Conflict", files: []file{{"a.cue", "x: 1\nx: 2"}}, run: unify},

	// Constraints a concrete value fails.
	{name: "bound", section: "Constraint", files: []file{{"schema.cue", `x: int & <5`}, {"data.json", `{"x": 7}`}}, run: unify},
	{name: "bound-two-sided", section: "Constraint", files: []file{{"schema.cue", `x: >=0 & <=10`}, {"data.json", `{"x": 11}`}}, run: unify},
	{name: "regex", section: "Constraint", files: []file{{"schema.cue", `x: =~"^a"`}, {"data.json", `{"x": "b"}`}}, run: unify},
	{name: "kind-schema-data", section: "Constraint", files: []file{{"schema.cue", `x: int`}, {"data.json", `{"x": "seven"}`}}, run: unify},
	{name: "kind-data-schema", section: "Constraint", files: []file{{"data.json", `{"x": "seven"}`}, {"schema.cue", `x: int`}}, run: unify},
	{name: "validator-builtin", section: "Constraint", files: []file{{"schema.cue", "import \"strings\"\nx: strings.MinRunes(3)"}, {"data.json", `{"x": "ab"}`}}, run: unify},

	// Closedness and presence.
	{name: "closed-field-not-allowed", section: "Fields", files: []file{{"schema.cue", "#D: {a: int}\nd: #D"}, {"data.cue", `d: {a: 1, b: 2}`}}, run: unify},
	{name: "closed-two-unknown-fields", section: "Fields", files: []file{{"schema.cue", "#D: {a: int}\nd: #D"}, {"data.cue", `d: {a: 1, b: 2, c: 3}`}}, run: unify},
	{name: "required-field-absent", section: "Fields", files: []file{{"schema.cue", `x!: int`}, {"data.cue", `y: 1`}}, run: validate},
	{name: "incomplete-value", section: "Fields", files: []file{{"schema.cue", `x: int`}}, run: validate},
	{name: "pattern-error-guard", section: "Fields", files: []file{{"schema.cue", "s: {\n\ta: int\n\t[!~\"^(a)$\"]~(K,_): error(\"\\(K) is not a field of s\")\n}"}, {"data.cue", `s: {a: 1, b: 2}`}}, run: unify},

	// Disjunctions.
	{name: "disjunction-no-arm", section: "Disjunction", files: []file{{"schema.cue", `x: int | string`}, {"data.json", `{"x": true}`}}, run: unify},
	{name: "disjunction-struct-arms", section: "Disjunction", files: []file{{"schema.cue", `x: {a: int} | {b: string}`}, {"data.json", `{"x": {"c": 1}}`}}, run: unify},
	{name: "disjunction-incomplete", section: "Disjunction", files: []file{{"schema.cue", `x: int | string`}}, run: validate},

	// References.
	{name: "reference-not-found", section: "Reference", files: []file{{"a.cue", `b: a`}}, run: compileErr},
	{name: "reference-optional", section: "Reference", files: []file{{"a.cue", "a?: int\nb: a"}}, run: validate},
	{name: "reference-cycle", section: "Reference", files: []file{{"a.cue", "a: b + 1\nb: a + 1"}}, run: validate},
	{name: "lookup-missing-field", section: "Reference", files: []file{{"a.cue", `a: 1`}}, run: func(_ *cue.Context, vals []cue.Value) error {
		return vals[0].LookupPath(cue.ParsePath("missing")).Err()
	}},

	// Builtins and native errors.
	{name: "error-builtin", section: "Native", files: []file{{"a.cue", `x: error("the author's message")`}}, run: validate},
	{name: "call-argument-kind", section: "Native", files: []file{{"a.cue", "import \"strings\"\nx: strings.ToUpper(1)"}}, run: validate},
	{name: "call-list-drop", section: "Native", files: []file{{"a.cue", "import \"list\"\nx: list.Drop(1, 1)"}}, run: validate},

	// Syntax.
	{name: "syntax-unclosed", section: "Syntax", files: []file{{"a.cue", `x: {`}}, run: compileErr},
	{name: "syntax-shorthand-ok-but-bad-token", section: "Syntax", files: []file{{"a.cue", `x: 1 2`}}, run: compileErr},

	// Subsumption.
	{name: "subsume-narrower", section: "Subsume", files: []file{{"a.cue", `x: int`}, {"b.cue", `x: string`}}, run: func(_ *cue.Context, vals []cue.Value) error {
		return vals[0].Subsume(vals[1])
	}},
	{name: "subsume-missing-field", section: "Subsume", files: []file{{"a.cue", `x: {a: int, b: int}`}, {"b.cue", `x: {a: int}`}}, run: func(_ *cue.Context, vals []cue.Value) error {
		return vals[0].Subsume(vals[1], cue.Raw())
	}},

	// Loading.
	{name: "load-no-files", section: "Load", files: nil, run: func(_ *cue.Context, _ []cue.Value) error {
		cfg := &load.Config{Dir: "/virtual/empty", Overlay: map[string]load.Source{
			"/virtual/empty/cue.mod/module.cue": load.FromString(`module: "example.com/empty"` + "\nlanguage: version: \"v0.18.0\"\n"),
		}}
		return load.Instances([]string{"./nothing"}, cfg)[0].Err
	}},
	{name: "load-multiple-packages", section: "Load", files: nil, run: func(_ *cue.Context, _ []cue.Value) error {
		cfg := &load.Config{Dir: "/virtual/two", Overlay: map[string]load.Source{
			"/virtual/two/cue.mod/module.cue": load.FromString(`module: "example.com/two"` + "\nlanguage: version: \"v0.18.0\"\n"),
			"/virtual/two/a.cue":              load.FromString("package a\nx: 1\n"),
			"/virtual/two/b.cue":              load.FromString("package b\nx: 1\n"),
		}}
		return load.Instances([]string{"."}, cfg)[0].Err
	}},
	{name: "load-syntax-error", section: "Load", files: nil, run: func(_ *cue.Context, _ []cue.Value) error {
		cfg := &load.Config{Dir: "/virtual/bad", Overlay: map[string]load.Source{
			"/virtual/bad/cue.mod/module.cue": load.FromString(`module: "example.com/bad"` + "\nlanguage: version: \"v0.18.0\"\n"),
			"/virtual/bad/a.cue":              load.FromString("package a\nx: {\n"),
		}}
		return load.Instances([]string{"."}, cfg)[0].Err
	}},
}

func compileAll(ctx *cue.Context, files []file) []cue.Value {
	vals := make([]cue.Value, len(files))
	for i, f := range files {
		vals[i] = ctx.CompileString(f.text, cue.Filename(f.name))
	}
	return vals
}

// unify unifies the values in order and validates the result concretely.
func unify(_ *cue.Context, vals []cue.Value) error {
	v := vals[0]
	for _, w := range vals[1:] {
		v = v.Unify(w)
	}
	if err := v.Err(); err != nil {
		return err
	}
	return v.Validate(cue.Concrete(true))
}

// validate validates the unified values concretely.
func validate(ctx *cue.Context, vals []cue.Value) error {
	return unify(ctx, vals)
}

// compileErr returns the compile error of the first file.
func compileErr(_ *cue.Context, vals []cue.Value) error {
	return vals[0].Err()
}

// record describes err the way a tool sees it.
func record(err error) string {
	var b strings.Builder
	if err == nil {
		b.WriteString("no error\n")
		return b.String()
	}
	fmt.Fprintf(&b, "error: %s\n", err.Error())
	fmt.Fprintf(&b, "type: %T\n", err)
	entries := cerrors.Errors(err)
	fmt.Fprintf(&b, "entries: %d\n", len(entries))
	for i, e := range entries {
		format, args := e.Msg()
		types := make([]string, len(args))
		for j, a := range args {
			types[j] = fmt.Sprintf("%T", a)
		}
		fmt.Fprintf(&b, "- [%d] %s\n", i, e.Error())
		fmt.Fprintf(&b, "  msg: %q\n", format)
		fmt.Fprintf(&b, "  args: [%s]\n", strings.Join(types, ", "))
		fmt.Fprintf(&b, "  path: %s\n", strings.Join(e.Path(), "."))
		if p := e.Position(); p.IsValid() {
			fmt.Fprintf(&b, "  position: %s %s\n", diag.OriginOf(p.Filename()), p)
		} else {
			b.WriteString("  position: none\n")
		}
		for _, p := range e.InputPositions() {
			if p.IsValid() {
				fmt.Fprintf(&b, "  input: %s %s\n", diag.OriginOf(p.Filename()), p)
			} else {
				b.WriteString("  input: none\n")
			}
		}
	}
	fmt.Fprintf(&b, "details:\n%s", indent(cerrors.Details(err, &cerrors.Config{Cwd: "/virtual"})))
	return b.String()
}

func indent(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("    " + l + "\n")
	}
	return b.String()
}

func TestMenagerie(t *testing.T) {
	a := diagtest.ReadArchive(t, filepath.Join("testdata", "menagerie.txtar"))
	ctx := cuecontext.New()
	got := map[string]string{}
	var docCases []diagdocs.MenagerieCase
	names := map[string]bool{}
	for _, c := range menagerie {
		if names[c.name] {
			t.Fatalf("menagerie case %q is declared twice", c.name)
		}
		names[c.name] = true
		err := c.run(ctx, compileAll(ctx, c.files))
		if err == nil {
			t.Errorf("%s: produced no error", c.name)
		}
		rec := record(err)
		got["out/"+c.name] = rec
		var inputs []diagdocs.MenagerieFile
		for _, f := range c.files {
			inputs = append(inputs, diagdocs.MenagerieFile{Name: f.name, Text: f.text})
		}
		docCases = append(docCases, diagdocs.MenagerieCase{Name: c.name, Section: c.section, Files: inputs, Record: rec})
	}
	// The digest canary, over the records in name order.
	h := sha256.New()
	for _, name := range slices.Sorted(maps.Keys(got)) {
		fmt.Fprintf(h, "%s\n%s\n", name, got[name])
	}
	digest := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if digest != menagerieDigest {
		t.Errorf("CUE's error shapes changed: the menagerie digest is now\n\t%s\nRegenerate with %s=1, read the diffs of diag/testdata/menagerie.txtar and docs/cue-errors.md, re-check every shape in diag/shapes.go the diff touches, then update menagerieDigest.", digest, envvars.VarUpdateGolden)
	}

	a.Compare(t, got)
	diagtest.CompareFile(t, filepath.Join("..", "docs", "cue-errors.md"), diagdocs.CueErrors(docCases))
}

// TestShapesCovered: every shape in the table matches a message format the
// menagerie records, so that a CUE change to that format is caught by the
// digest; and every recorded format is either matched by a shape or listed
// here as one thema deliberately reports generically.
func TestShapesCovered(t *testing.T) {
	a := diagtest.ReadArchive(t, filepath.Join("testdata", "menagerie.txtar"))
	recorded := map[string]bool{}
	for _, f := range a.Ar.Files {
		for _, line := range strings.Split(string(f.Data), "\n") {
			if msg, ok := strings.CutPrefix(strings.TrimSpace(line), "msg: "); ok {
				recorded[msg] = true
			}
		}
	}
	for _, s := range diag.Shapes() {
		if s.Msg == "" {
			continue
		}
		if !recorded[fmt.Sprintf("%q", s.Msg)] {
			t.Errorf("shape %s matches %q, which no menagerie case records; add a case so a CUE change to it is caught", s.Name, s.Msg)
		}
	}
	matched := map[string]bool{}
	for _, s := range diag.Shapes() {
		matched[fmt.Sprintf("%q", s.Msg)] = true
	}
	for msg := range recorded {
		if !matched[msg] && !unmatchedByDesign[msg] && !unmatchedYet[msg] {
			t.Errorf("the menagerie records %s, which no shape matches and neither unmatchedByDesign nor unmatchedYet lists; such errors are reported as %s", msg, diag.E0902)
		}
	}
	for msg := range unmatchedYet {
		if matched[msg] {
			t.Errorf("unmatchedYet still lists %s, which a shape now matches; remove it", msg)
		}
	}
}

// unmatchedByDesign lists recorded message formats thema reports generically
// on purpose, as the menagerie quotes them.
var unmatchedByDesign = map[string]bool{
	// A plain Go error promoted by cue/errors; thema's own sites stop
	// producing these as they migrate, and E0903 marks the ones that remain.
	`"%s"`: true,
}

// unmatchedYet is the backlog: recorded formats no shape matches yet. Each
// migration step that adds a shape removes its entry, and the test above
// insists on it.
var unmatchedYet = map[string]bool{
	`"%d errors in empty disjunction:"`:                    true,
	`"%v is not a field of s"`:                             true,
	`"cannot find package %q"`:                             true,
	`"cannot reference optional field: %s"`:                true,
	`"cannot use %s (type %s) as %s in argument %d to %v"`: true,
	`"cycle with field: %v"`:                               true,
	`"field %v not present in %v"`:                         true,
	`"field not found: %v%s"`:                              true,
	`"missing ',' in %s"`:                                  true,
	`"missing field %q"`:                                   true,
	`"regular field is constraint in subsumed value: %v"`:  true,
	`"expected %s, found '%s'"`:                            true,
	`"reference %q not found"`:                             true,
	`"found packages %q (%s) and %q (%s) in %q"`:           true,
}
