package compile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/google/go-cmp/cmp"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/diagtest"
)

// TestRoundTrip compiles each archive in testdata/roundtrip, binds the result
// and decompiles it again. The archive holds a lineage's authoring files under
// its name (ship/1.0.thema, ship/history/0.0.thema) and, as goldens:
//
//   - out/normal.cue: the compiled normal form;
//   - out/bind: "ok", or BindLineage's error;
//   - out/decompiled/<path>: the authoring files decompiled from the normal
//     form, which must equal the input up to formatting;
//   - out/error: the compile error, for an archive that must not compile; the
//     other sections are then absent;
//   - out/translate/<from>/<to>/<name>: for each in/translate/<from>/<to>/<name>.json,
//     an instance of <from>: what translating it to <to> gives, and what
//     translating that back to <from> gives, each with its lacunas.
func TestRoundTrip(t *testing.T) {
	files, err := filepath.Glob("testdata/roundtrip/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".txtar"), func(t *testing.T) {
			a := readArchive(t, file)
			name, inputs := a.lineage(t)
			got := make(map[string]string)

			ctx := cuecontext.New()
			c, err := Compile(ctx, name, inputs)
			if err != nil {
				ds := diag.Diagnostics(err)
				if len(ds) == 0 {
					t.Fatalf("a compile failure is made of diagnostics, got: %v", err)
				}
				diagtest.Lint(t, ds, diagtest.Authoring)
				got["out/error"] = rendered(ds, a)
				a.compare(t, got)
				return
			}
			text, terr := c.Text()
			if terr != nil {
				t.Fatal(terr)
			}
			got["out/normal.cue"] = text

			v := ctx.BuildFile(c.File)
			if v.Err() != nil {
				t.Fatalf("building the normal form: %v", cerrors.Details(v.Err(), nil))
			}
			// Bound with the authoring form's vocabulary, so that what bind
			// reports names the author's files and lenses.
			if lin, berr := thema.BindLineage(v, thema.NewRuntime(ctx), thema.WithVocabulary(Vocabulary(c))); berr != nil {
				ds := diag.Diagnostics(berr)
				if len(ds) == 0 {
					t.Fatalf("a bind failure is made of diagnostics, got: %v", berr)
				}
				diagtest.Lint(t, ds, diagtest.Authoring)
				got["out/bind"] = rendered(ds, a)
			} else {
				got["out/bind"] = "ok\n"
				a.translate(t, ctx, lin, got)
			}

			dname, dfiles, err := Decompile(v)
			if err != nil {
				t.Fatalf("decompiling: %v", err)
			}
			if dname != name {
				t.Errorf("decompiled name %q, want %q", dname, name)
			}
			want := make(map[string]string)
			for _, f := range inputs {
				want[f.Path] = formatted(t, f.Path, f.Text)
			}
			for _, f := range dfiles {
				got["out/decompiled/"+f.Path] = string(f.Text)
				if w, ok := want[f.Path]; !ok {
					t.Errorf("decompiled %s, which was not an input", f.Path)
				} else if diff := cmp.Diff(w, string(f.Text)); diff != "" {
					t.Errorf("decompiled %s differs from the formatted input (-input +decompiled):\n%s", f.Path, diff)
				}
				delete(want, f.Path)
			}
			for p := range want {
				t.Errorf("input %s was not decompiled", p)
			}

			// Compiling the decompiled form gives the same normal form.
			c2, err := Compile(ctx, dname, dfiles)
			if err != nil {
				t.Fatalf("recompiling the decompiled form: %v", cerrors.Details(err, nil))
			}
			text2, err := c2.Text()
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(formatted(t, "normal.cue", []byte(text)), formatted(t, "normal.cue", []byte(text2))); diff != "" {
				t.Errorf("recompiled normal form differs (-first +recompiled):\n%s", diff)
			}
			a.compare(t, got)
		})
	}
}

// translate runs the archive's translate cases against lin, recording each in
// got. A case is in/translate/<from>/<to>/<name>.json, an instance of <from>.
func (a *archive) translate(t *testing.T, ctx *cue.Context, lin thema.Lineage, got map[string]string) {
	t.Helper()
	for _, f := range a.ar.Files {
		rest, ok := strings.CutPrefix(f.Name, "in/translate/")
		if !ok {
			continue
		}
		name := strings.TrimSuffix(rest, ".json")
		parts := strings.Split(name, "/")
		if len(parts) != 3 {
			t.Fatalf("%s: a translate case is in/translate/<from>/<to>/<name>.json", f.Name)
		}
		from, err := thema.ParseSyntacticVersion(parts[0])
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		to, err := thema.ParseSyntacticVersion(parts[1])
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		inst, err := thema.SchemaP(lin, from).Validate(ctx.CompileBytes(f.Data))
		if err != nil {
			t.Fatalf("%s: %v", f.Name, cerrors.Details(err, nil))
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s: %s\n", from, bytes.TrimSpace(f.Data))
		step := func(inst *thema.Instance, to thema.SyntacticVersion, label string) *thema.Instance {
			out, lac, err := inst.Translate(to)
			if err != nil {
				t.Fatalf("%s: translating to %s: %v", f.Name, to, cerrors.Details(err, nil))
			}
			data, err := json.Marshal(out.Underlying())
			if err != nil {
				t.Fatal(err)
			}
			lacs, err := json.Marshal(lac.AsList())
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s %s: %s\nlacunas: %s\n", label, to, data, lacs)
			return out
		}
		step(step(inst, to, "to"), from, "back to")
		got["out/translate/"+name] = b.String()
	}
}

// rendered is the diagnostics as the author sees them, with the archive's
// authoring files as the source of excerpts. Positions name the files by their
// path under the lineage's directory.
func rendered(ds []diag.Diagnostic, a *archive) string {
	var b bytes.Buffer
	diag.Render(&b, ds, diag.RenderOptions{Source: func(file string) ([]byte, bool) {
		for _, f := range a.ar.Files {
			if f.Name == file || strings.HasSuffix(f.Name, "/"+file) {
				return f.Data, true
			}
		}
		return nil, false
	}})
	return b.String()
}

// formatted returns text in thema's layout, which is what Decompile emits.
func formatted(t *testing.T, name string, text []byte) string {
	t.Helper()
	b, err := Format(text)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(b)
}

// lineage returns the archive's authoring files: those under its first
// directory, which names the lineage.
func (a *archive) lineage(t *testing.T) (string, []File) {
	t.Helper()
	var name string
	var files []File
	for _, f := range a.ar.Files {
		if strings.HasPrefix(f.Name, "out/") || strings.HasPrefix(f.Name, "in/") {
			continue
		}
		dir, rest, ok := strings.Cut(f.Name, "/")
		if !ok {
			t.Fatalf("%s: file %s is not under the lineage's directory", a.path, f.Name)
		}
		if name == "" {
			name = dir
		} else if dir != name {
			t.Fatalf("%s: files under %s and %s; an archive holds one lineage", a.path, name, dir)
		}
		files = append(files, File{Path: rest, Text: f.Data})
	}
	return name, files
}
