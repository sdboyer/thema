package lenscheck

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

	"github.com/sdboyer/thema/internal/envvars"
)

// outFile is the archive section holding the recorded output.
const outFile = "out"

// TestVerdicts runs the corpus in testdata/*.txtar: each archive holds a
// source.cue and a target.cue, both schemas written as a file's top-level
// fields, and a lens.cue holding the lens (v0_0, v1_0, lacunas) as a file's
// top-level fields, and records in out the verdict of [Check] and its findings.
// The archive comment says what the case exercises and which rule it belongs
// to.
//
// Every verdict here is part of what binding a lineage guarantees: a lens that
// binds is total, so a verdict that moves can make a published lens fail to
// bind, or let one through that fails at translate. Set THEMA_UPDATE_GOLDEN=1
// to rewrite out, then review the diff as a contract change.
func TestVerdicts(t *testing.T) {
	files, err := filepath.Glob("testdata/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no corpus")
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".txtar"), func(t *testing.T) {
			ar, err := txtar.ParseFile(file)
			if err != nil {
				t.Fatal(err)
			}
			ctx := cuecontext.New()
			var source, target, lens cue.Value
			var want string
			outIdx := -1
			for i, f := range ar.Files {
				switch f.Name {
				case "source.cue":
					source = ctx.CompileBytes(f.Data, cue.Filename("source.cue"))
				case "target.cue":
					target = ctx.CompileBytes(f.Data, cue.Filename("target.cue"))
				case "lens.cue":
					lens = ctx.CompileBytes(f.Data, cue.Filename("lens.cue"))
				case outFile:
					want = string(f.Data)
					outIdx = i
				}
			}
			if !source.Exists() || !target.Exists() || !lens.Exists() {
				t.Fatal("archive needs source.cue, target.cue and lens.cue")
			}
			for name, v := range map[string]cue.Value{"source.cue": source, "target.cue": target, "lens.cue": lens} {
				if err := v.Err(); err != nil && v.Validate() != nil {
					t.Fatalf("%s: %v", name, err)
				}
			}

			got := render(Check(ctx, lens, source, target, "v0_0", "v1_0"))
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
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("verdict changed (-want +got):\n%s", diff)
			}
		})
	}
}

func render(r Result) string {
	var b bytes.Buffer
	fmt.Fprintln(&b, r.Verdict())
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "%s @ %s\n", f, f.Pos)
	}
	return b.String()
}
