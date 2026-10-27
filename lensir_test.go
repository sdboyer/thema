package thema

import (
	"fmt"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/sdboyer/thema/internal/lensir"
	"github.com/sdboyer/thema/internal/txtartest/vanilla"
)

// lineageCorpus holds the corpus lineages, one txtar per lineage.
const lineageCorpus = "./testdata/lineage"

// TestLensPrograms records, for each corpus lineage, the program built for
// each of its lenses when it was proven total at bind (internal/lensir), as
// lensir.Print renders it, in out/lensir. A lineage's programs are what its
// lenses compile to; a diff here is a change to what every backend renders.
func TestLensPrograms(t *testing.T) {
	test := vanilla.TxTarTest{
		Root:    lineageCorpus,
		Name:    "lensir",
		ThemaFS: CueJointFS,
	}

	rt := NewRuntime(cuecontext.New())

	test.Run(t, func(tc *vanilla.Test) {
		lin, err := bindTxtarLineage(tc, rt, "lineagePath")
		if err != nil {
			tc.Fatalf("error binding lineage: %+v", err)
		}
		progs, err := LensPrograms(lin)
		if err != nil {
			tc.Fatalf("lens programs: %v", err)
		}
		if _, err := tc.Write([]byte(printPrograms(progs))); err != nil {
			tc.Fatal(err)
		}
	})
}

// printPrograms renders every program under a line naming its edge, indented
// beneath it.
func printPrograms(progs lensir.Programs) string {
	if len(progs) == 0 {
		return "no lenses\n"
	}
	var b strings.Builder
	for _, p := range progs.Sorted() {
		fmt.Fprintf(&b, "lens %s\n", p.Edge)
		for _, line := range strings.SplitAfter(strings.TrimSuffix(lensir.Print(p), "\n"), "\n") {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
	}
	return b.String()
}
