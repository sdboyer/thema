package exemplars

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/envvars"
	"github.com/sdboyer/thema/internal/lensir"
)

// TestLensPrograms records, for each exemplar as All binds it, the program
// built for each of its lenses when it was proven total (internal/lensir), as
// lensir.Print renders it, in testdata/lensir/<name>.txt.
func TestLensPrograms(t *testing.T) {
	all := All(allrt)
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var b strings.Builder
			progs, err := thema.LensPrograms(all[name])
			switch {
			case err != nil:
				fmt.Fprintf(&b, "no lens programs: %v\n", err)
			case len(progs) == 0:
				fmt.Fprintln(&b, "no lenses")
			}
			for _, p := range progs.Sorted() {
				fmt.Fprintf(&b, "lens %s\n", p.Edge)
				for _, line := range strings.SplitAfter(strings.TrimSuffix(lensir.Print(p), "\n"), "\n") {
					b.WriteString("  " + line)
				}
				b.WriteString("\n")
			}
			got := b.String()

			golden := filepath.Join("testdata", "lensir", name+".txt")
			if envvars.UpdateGoldenFiles {
				if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, readErr := os.ReadFile(golden) //nolint:gosec // the path is the exemplar's name under testdata
			if readErr != nil {
				t.Fatalf("%v; run with %s=1", readErr, envvars.VarUpdateGolden)
			}
			if diff := cmp.Diff(string(want), got); diff != "" {
				t.Errorf("programs changed (-want +got):\n%s", diff)
			}
		})
	}
}
