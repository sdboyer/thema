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

	"github.com/sdboyer/thema/internal/envvars"
)

// outFile is the archive section holding the recorded output.
const outFile = "out"

// TestVerdicts runs the corpus in testdata/*.txtar: each archive holds an
// old.cue and a new.cue, both schemas written as a file's top-level fields, and
// records in out the verdict of [Check] and its findings. The archive comment
// says what the case exercises and which rule it belongs to.
//
// Every verdict here is part of thema's versioning contract: a lineage is valid
// only if its minor steps are compatible and its major steps are not, so a
// change in a verdict can invalidate published lineages. Set
// THEMA_UPDATE_GOLDEN=1 to rewrite out, then review the diff as a contract
// change.
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
			var pred, succ cue.Value
			var want string
			outIdx := -1
			for i, f := range ar.Files {
				switch f.Name {
				case "old.cue":
					pred = ctx.CompileBytes(f.Data, cue.Filename("old.cue"))
				case "new.cue":
					succ = ctx.CompileBytes(f.Data, cue.Filename("new.cue"))
				case outFile:
					want = string(f.Data)
					outIdx = i
				}
			}
			if !pred.Exists() || !succ.Exists() {
				t.Fatal("archive needs old.cue and new.cue")
			}
			if err := pred.Err(); err != nil && pred.Validate() != nil {
				t.Fatalf("old.cue: %v", err)
			}
			if err := succ.Err(); err != nil && succ.Validate() != nil {
				t.Fatalf("new.cue: %v", err)
			}

			got := render(Check(ctx, pred, succ))
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
