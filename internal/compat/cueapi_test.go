package compat

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/envvars"
)

// TestCueAPI records what the cue API reports for the constructs the checker
// classifies: field forms, defaults, validators, references, closedness,
// lists and comprehensions. Each archive in testdata/cueapi holds its cases as
// the fields of a top-level cases struct, compiled and then closed with
// cuetil.CloseRecursive as Check does, and records in out what each API call
// returns for every field beneath each case. The archive comment says which
// observations the checker relies on.
//
// This is reference material: a diff here is a change in CUE's behavior that
// the checker's rules must be re-examined against. Set THEMA_UPDATE_GOLDEN=1
// to rewrite out.
func TestCueAPI(t *testing.T) {
	files, err := filepath.Glob("testdata/cueapi/*.txtar")
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
			outIdx := -1
			for i, f := range ar.Files {
				switch f.Name {
				case "in.cue":
					in = f.Data
				case outFile:
					outIdx = i
				}
			}
			ctx := cuecontext.New()
			v := ctx.CompileBytes(in, cue.Filename("in.cue"))
			cases := v.LookupPath(cue.MakePath(cue.Str("cases")))
			if !cases.Exists() {
				t.Fatalf("no cases struct: %v", v.Err())
			}
			var b bytes.Buffer
			it, _ := cases.Fields()
			for it.Next() {
				fmt.Fprintf(&b, "## %s\n", it.Selector())
				c := cuetil.CloseRecursive(ctx, it.Value())
				describe(&b, c, c, "")
			}
			got := b.String()
			if envvars.UpdateGoldenFiles {
				f := txtar.File{Name: outFile, Data: []byte(got)}
				if outIdx >= 0 {
					ar.Files[outIdx] = f
				} else {
					ar.Files = append(ar.Files, f)
				}
				if err := os.WriteFile(file, txtar.Format(ar), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			if outIdx < 0 {
				t.Fatalf("missing out section; run with %s=1", envvars.VarUpdateGolden)
			}
			if diff := cmp.Diff(string(ar.Files[outIdx].Data), got); diff != "" {
				t.Errorf("cue API output changed (-want +got):\n%s", diff)
			}
		})
	}
}

// describe prints what the checker asks of v, then of each of v's fields.
func describe(b *bytes.Buffer, v, root cue.Value, indent string) {
	op, args := v.Expr()
	fmt.Fprintf(b, "%skind=%v incomplete=%v concrete=%v", indent, v.Kind(), v.IncompleteKind(), v.Validate(cue.Concrete(true)) == nil)
	if v.Err() != nil {
		fmt.Fprintf(b, " err=%q", v.Err())
	}
	fmt.Fprintf(b, "\n%sexpr=%v nargs=%d", indent, op, len(args))
	if op != cue.NoOp {
		var as []string
		for _, a := range args {
			as = append(as, fmtv(a))
		}
		fmt.Fprintf(b, " args=[%s]", strings.Join(as, " ; "))
	}
	if rr, rp := v.ReferencePath(); rr.Exists() && len(rp.Selectors()) > 0 {
		within := cuetil.PathHasPrefix(rp, root.Path())
		fmt.Fprintf(b, "\n%sref=%s withinSchema=%v target=%s", indent, rp, within, fmtv(cue.Dereference(v)))
		if bi := rr.BuildInstance(); bi != nil {
			fmt.Fprintf(b, " rootImport=%q", bi.ImportPath)
		}
	}
	if op == cue.CallOp {
		if name, ok := packageMember(args[0]); ok {
			fmt.Fprintf(b, "\n%scallee=%s", indent, name)
		} else {
			fmt.Fprintf(b, "\n%scallee=%s (predeclared)", indent, fmtv(args[0]))
		}
	}
	if d, ok := v.Default(); ok {
		fmt.Fprintf(b, "\n%sdefault=%s concrete=%v", indent, fmtv(d), d.Validate(cue.Concrete(true)) == nil)
	}
	switch kindOf(v) {
	case cue.StructKind:
		fmt.Fprintf(b, "\n%sallowsAnyString=%v anyStringLookup=%s", indent, v.Allows(cue.AnyString), exists(v.LookupPath(cue.MakePath(cue.AnyString))))
	case cue.ListKind:
		fmt.Fprintf(b, "\n%slen=%s anyIndexLookup=%s", indent, fmtv(v.Len()), exists(v.LookupPath(cue.MakePath(cue.AnyIndex))))
	}
	b.WriteString("\n")
	if kindOf(v) != cue.StructKind && kindOf(v) != cue.ListKind {
		return
	}
	it, err := v.Fields(cue.Optional(true), cue.Patterns(true))
	if err != nil {
		return
	}
	for it.Next() {
		sel := it.Selector()
		fmt.Fprintf(b, "%s- %s (%v) lookup=%s\n", indent, sel, sel.ConstraintType(), exists(v.LookupPath(cue.MakePath(sel))))
		if sel.ConstraintType() == cue.PatternConstraint && kindOf(v) == cue.ListKind {
			// The element constraint is read by lookup; see lists.
			if isStruct(it.Value()) {
				fmt.Fprintf(b, "%s    viaFields: allowsAnyString=%v\n", indent, it.Value().Allows(cue.AnyString))
			}
			describe(b, v.LookupPath(cue.MakePath(cue.AnyIndex)), root, indent+"    ")
			continue
		}
		if _, rp := it.Value().ReferencePath(); len(rp.Selectors()) > 0 && strings.Count(indent, " ") >= 8 {
			// Bound the walk of recursive definitions.
			fmt.Fprintf(b, "%s    (recursion stopped)\n", indent)
			continue
		}
		describe(b, it.Value(), root, indent+"    ")
	}
}

func exists(v cue.Value) string {
	if !v.Exists() {
		return "none"
	}
	return fmtv(v)
}
