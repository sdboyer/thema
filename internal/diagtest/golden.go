package diagtest

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/envvars"
)

// Archive is a txtar test case: input files, and out/ sections as goldens.
type Archive struct {
	Path string
	Ar   *txtar.Archive
}

// ReadArchive parses the archive at path.
func ReadArchive(t testing.TB, path string) *Archive {
	t.Helper()
	ar, err := txtar.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return &Archive{Path: path, Ar: ar}
}

// Archives reads every .txtar under dir.
func Archives(t testing.TB, dir string) []*Archive {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	out := make([]*Archive, 0, len(paths))
	for _, p := range paths {
		out = append(out, ReadArchive(t, p))
	}
	return out
}

// Name is the archive's file name without its extension.
func (a *Archive) Name() string {
	return strings.TrimSuffix(filepath.Base(a.Path), ".txtar")
}

// File returns the contents of the archive's file name, and whether it exists.
func (a *Archive) File(name string) (string, bool) {
	for _, f := range a.Ar.Files {
		if f.Name == name {
			return string(f.Data), true
		}
	}
	return "", false
}

// Inputs returns the archive's files that are not goldens.
func (a *Archive) Inputs() []txtar.File {
	var out []txtar.File
	for _, f := range a.Ar.Files {
		if !strings.HasPrefix(f.Name, "out/") {
			out = append(out, f)
		}
	}
	return out
}

// Directives reads "#key: value" lines from the archive's comment, and returns
// the remaining comment text as prose.
func (a *Archive) Directives() (map[string]string, string) {
	dirs := map[string]string{}
	var prose []string
	for _, line := range strings.Split(string(a.Ar.Comment), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.HasPrefix(k, "#") && !strings.ContainsAny(k, " \t") {
			dirs[strings.TrimPrefix(k, "#")] = strings.TrimSpace(v)
			continue
		}
		prose = append(prose, line)
	}
	return dirs, strings.TrimSpace(strings.Join(prose, "\n"))
}

// Source returns a reader over the archive's input files, for rendering
// excerpts.
func (a *Archive) Source(file string) ([]byte, bool) {
	s, ok := a.File(file)
	return []byte(s), ok
}

// Compare checks got against the archive's out/ sections, or with
// THEMA_UPDATE_GOLDEN set, rewrites them.
func (a *Archive) Compare(t testing.TB, got map[string]string) {
	t.Helper()
	if envvars.UpdateGoldenFiles {
		kept := a.Inputs()
		for _, name := range slices.Sorted(maps.Keys(got)) {
			kept = append(kept, txtar.File{Name: name, Data: []byte(got[name])})
		}
		a.Ar.Files = kept
		if err := os.WriteFile(a.Path, txtar.Format(a.Ar), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, f := range a.Ar.Files {
		if !strings.HasPrefix(f.Name, "out/") {
			continue
		}
		want, ok := got[f.Name]
		if !ok {
			t.Errorf("golden section %s is no longer produced; run with %s=1", f.Name, envvars.VarUpdateGolden)
			continue
		}
		if diff := cmp.Diff(string(f.Data), want); diff != "" {
			t.Errorf("%s (-golden +got):\n%s", f.Name, diff)
		}
		delete(got, f.Name)
	}
	for name := range got {
		t.Errorf("missing golden section %s; run with %s=1", name, envvars.VarUpdateGolden)
	}
}

// CompareFile checks got against the file at path, or with THEMA_UPDATE_GOLDEN
// set, writes it.
func CompareFile(t testing.TB, path, got string) {
	t.Helper()
	if envvars.UpdateGoldenFiles {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; run with %s=1", err, envvars.VarUpdateGolden)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("%s (-golden +got):\n%s", path, diff)
	}
}

// Positions describes the positions err carries as structured data: for each
// entry of cerrors.Errors, the first line of its message, then its position
// and input positions, each classified by origin. These are what a tool can
// map back to source, as opposed to positions in an error's text.
func Positions(err error) string {
	var b strings.Builder
	for _, e := range cerrors.Errors(err) {
		fmt.Fprintf(&b, "%s\n", firstLine(e.Error()))
		ps := positionsOf(e)
		if len(ps) == 0 {
			b.WriteString("\tno positions\n")
		}
		for _, p := range ps {
			fmt.Fprintf(&b, "\t%s %s\n", diag.OriginOf(p.Filename()), p)
		}
	}
	return b.String()
}

func positionsOf(e cerrors.Error) []token.Pos {
	var out []token.Pos
	seen := map[token.Pos]bool{}
	for _, p := range append([]token.Pos{e.Position()}, e.InputPositions()...) {
		if !p.IsValid() || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
