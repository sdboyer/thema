package load

import (
	"bytes"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/authoring"
	"github.com/sdboyer/thema/diag"
)

// The collection of these tests is at repo/fleet, below the FS's root, so
// that a position relative to the collection, or to a lineage's directory,
// is told apart from one relative to the FS.
const posRoot = "repo/fleet"

// The alike fleet's other lineages, and the files they fail in.
const (
	sloopName  = "sloop"
	cutterName = "cutter"
	v01File    = "history/0.1.thema"
	tipFile    = "tip.thema"
)

// alikeFleet is a collection whose lineages are laid out alike, file for
// file, and each of which fails in its history/0.1.thema, at a line of its
// own and on text naming the lineage: a position that names the file below
// its lineage's directory, or names another lineage's, shows the wrong text.
//
//   - ship's 0.1 narrows a field (E0215);
//   - boat's 0.1 declares Schema twice (E0106, with a note in the same file);
//   - barge's 0.1 has a conflict of its own (E0112);
//   - cutter and sloop bind, but their normalized.gen.cue was edited by hand
//     (E0151, on the normal path only), on a line naming the lineage.
//
// Each tip is its 0.1, but with a conflict of its own (E0112), at a line
// of its own, so that on the development path every lineage fails in its
// tip.thema.
func alikeFleet(t *testing.T) fstest.MapFS {
	t.Helper()
	fsys := fstest.MapFS{}
	put := func(name, file, text string) {
		fsys[posRoot+"/"+name+"/"+file] = &fstest.MapFile{Data: []byte(text)}
	}
	pad := func(n int) string { return strings.Repeat("\n", n) }
	tip := func(name string, n int) string {
		return fmt.Sprintf("Schema: {%s\n\t%sTip: 1\n\t%sTip: 2\n}\n", pad(n), name, name)
	}

	put(shipName, "history/0.0.thema", "Schema: {\n\tshipName: string\n}\n")
	put(shipName, v01File, "Schema: {"+pad(1)+"\n\tshipName: int\n}\n")
	put(shipName, tipFile, tip(shipName, 1))

	put(boatName, "history/0.0.thema", "Schema: {\n\tboatName: string\n}\n")
	put(boatName, v01File, pad(2)+"Schema: {\n\tboatName: string\n}\n\nSchema: { // boat again\n\tboatHull?: int\n}\n")
	put(boatName, tipFile, tip(boatName, 2))

	put(bargeName, "history/0.0.thema", "Schema: {\n\tbargeName: string\n}\n")
	put(bargeName, v01File, "Schema: {"+pad(3)+"\n\tbargeName: string\n\tbargeDeck: 1\n\tbargeDeck: 2\n}\n")
	put(bargeName, tipFile, tip(bargeName, 3))

	for i, name := range []string{cutterName, sloopName} {
		first := fmt.Sprintf("Schema: {\n\t%sName: string\n}\n", name)
		v01 := fmt.Sprintf("Schema: {\n\t%sName: string\n\t%sMast?: int\n}\n", name, name)
		put(name, "history/0.0.thema", first)
		put(name, v01File, v01)
		put(name, tipFile, tip(name, 4+i))
		_, files, err := authoring.Load(fsys, posRoot+"/"+name)
		if err != nil {
			t.Fatal(cerrors.Details(err, nil))
		}
		c, err := authoring.Compile(cuecontext.New(), name, files)
		if err != nil {
			t.Fatal(cerrors.Details(err, nil))
		}
		gen, err := authoring.GenerateNormalizedFile(c)
		if err != nil {
			t.Fatal(err)
		}
		field := []byte(name + "Mast?: int")
		if !bytes.Contains(gen, field) {
			t.Fatalf("%s's normalized.gen.cue has no %s:\n%s", name, field, gen)
		}
		put(name, authoring.NormalizedFile, string(bytes.Replace(gen, field, []byte(name+"Mast?: string"), 1)))
	}
	return fsys
}

// On the normal path, every position of a lineage's failure names its file
// by its path within the FS, and the excerpt rendered from that FS by that
// name is the line of that lineage's file.
func TestCollectionPositionsAlike(t *testing.T) {
	fsys := alikeFleet(t)
	c, err := AuthoringCollection(fsys, posRoot, thema.NewRuntime(cuecontext.New()))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	want := map[string]wantPos{
		shipName:   {diag.E0215, v01File, 3},
		boatName:   {diag.E0106, v01File, 7},
		bargeName:  {diag.E0112, v01File, 7},
		cutterName: {diag.E0151, authoring.NormalizedFile, 0},
		sloopName:  {diag.E0151, authoring.NormalizedFile, 0},
	}
	if got := keys(c.Failed); !equalKeys(got, want) {
		t.Fatalf("failed %v, want %v; bound %v", got, keys(want), keys(c.Lineages))
	}
	for name, w := range want {
		checkPositions(t, fsys, name, c.Failed[name], w)
	}
}

// On the development path, every lineage fails in its own tip.thema.
func TestDevelopmentCollectionPositionsAlike(t *testing.T) {
	fsys := soundHistories(alikeFleet(t))
	c, err := DevelopmentCollection(fsys, posRoot, thema.NewRuntime(cuecontext.New()))
	if err != nil {
		t.Fatal(cerrors.Details(err, nil))
	}
	want := tipFailures
	if got := keys(c.Failed); !equalKeys(got, want) {
		t.Fatalf("failed %v, want %v; bound %v", got, keys(want), keys(c.Lineages))
	}
	for name, w := range want {
		checkPositions(t, fsys, name, c.Failed[name], w)
	}
}

// tipFailures are where each lineage of the alike fleet fails on the
// development path, once its history is sound.
var tipFailures = map[string]wantPos{
	shipName:   {diag.E0112, tipFile, 4},
	boatName:   {diag.E0112, tipFile, 5},
	bargeName:  {diag.E0112, tipFile, 6},
	cutterName: {diag.E0112, tipFile, 7},
	sloopName:  {diag.E0112, tipFile, 8},
}

// soundHistories makes the alike fleet's histories sound, by making ship's,
// boat's and barge's 0.1 their 0.0, so that only their tips fail.
func soundHistories(fsys fstest.MapFS) fstest.MapFS {
	for _, name := range []string{shipName, boatName, bargeName} {
		fsys[posRoot+"/"+name+"/history/0.1.thema"] = fsys[posRoot+"/"+name+"/history/0.0.thema"]
	}
	return fsys
}

// A lineage loaded alone is positioned as it is in its collection.
func TestLineagePositionsAlike(t *testing.T) {
	fsys := alikeFleet(t)
	rt := thema.NewRuntime(cuecontext.New())
	for name, w := range map[string]wantPos{
		shipName:  {diag.E0215, v01File, 3},
		boatName:  {diag.E0106, v01File, 7},
		bargeName: {diag.E0112, v01File, 7},
	} {
		_, err := AuthoringLineage(fsys, posRoot+"/"+name, rt)
		checkPositions(t, fsys, name, err, w)
	}
	fsys = soundHistories(fsys)
	for name, w := range tipFailures {
		_, err := DevelopmentLineage(fsys, posRoot+"/"+name, rt)
		checkPositions(t, fsys, name, err, w)
	}
}

// wantPos is the diagnostic a lineage fails with, and where: rel is the
// file's path below the lineage's directory, and line its line, or 0 for any.
type wantPos struct {
	code diag.Code
	rel  string
	line int
}

func equalKeys[V any](got []string, want map[string]V) bool {
	return strings.Join(got, " ") == strings.Join(keys(want), " ")
}

// checkPositions checks that err, lineage name's failure, is a diagnostic w
// describes, positioned at the path within fsys of the lineage's file w.rel,
// and that every position it holds, primary or a note's, names a file of the
// lineage within fsys and renders, with its excerpt read from fsys by that
// name, as the text of that file at that line; and that the excerpt of the
// primary position names the lineage.
func checkPositions(t *testing.T, fsys fs.FS, name string, err error, w wantPos) {
	t.Helper()
	ds := diag.Diagnostics(err)
	if len(ds) == 0 {
		t.Errorf("%s: no diagnostics in %v", name, err)
		return
	}
	dir := posRoot + "/" + name + "/"
	var b strings.Builder
	diag.Render(&b, ds, diag.RenderOptions{Source: func(file string) ([]byte, bool) {
		src, err := fs.ReadFile(fsys, file)
		return src, err == nil
	}})
	rendered := excerpts(b.String())

	found := false
	for _, d := range ds {
		if d.Code == w.code && d.Primary != nil && d.Primary.File == dir+w.rel && (w.line == 0 || d.Primary.Line == w.line) {
			found = true
			if line := sourceLine(t, fsys, d.Primary.File, d.Primary.Line); !strings.Contains(line, name) {
				t.Errorf("%s: %s's primary line %q does not name the lineage", name, d.Code, line)
			}
		}
		poss := []*diag.Pos{d.Primary}
		for _, n := range d.Notes {
			poss = append(poss, n.Pos)
		}
		for _, p := range poss {
			if p == nil || p.Origin != diag.OriginUser {
				continue
			}
			if !strings.HasPrefix(p.File, dir) {
				t.Errorf("%s: %s is positioned at %s, which is not a path within the FS of one of the lineage's files", name, d.Code, p)
				continue
			}
			want := strings.TrimSpace(sourceLine(t, fsys, p.File, p.Line))
			got, ok := rendered[excerptKey{p.File, p.Line}]
			if !ok {
				t.Errorf("%s: %s renders no excerpt of %s:\n%s", name, d.Code, p, b.String())
			} else if got != want {
				t.Errorf("%s: %s renders %s as %q, want %q", name, d.Code, p, got, want)
			}
		}
	}
	if !found {
		t.Errorf("%s: no %s at %s%s:%d in\n%s", name, w.code, dir, w.rel, w.line, b.String())
	}
}

// sourceLine returns line n of file in fsys.
func sourceLine(t *testing.T, fsys fs.FS, file string, n int) string {
	t.Helper()
	src, err := fs.ReadFile(fsys, file)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	lines := strings.Split(string(src), "\n")
	if n < 1 || n > len(lines) {
		t.Fatalf("%s has no line %d", file, n)
	}
	return lines[n-1]
}

type excerptKey struct {
	file string
	line int
}

var (
	fileLine    = regexp.MustCompile(`^\s*(?:-->|:::) (.+):\d+:\d+$`)
	excerptLine = regexp.MustCompile(`^\s*(\d+) \|(.*)$`)
)

// excerpts reads rendered diagnostics back into the text of each excerpt
// line, by the file each section names and the line's number.
func excerpts(rendered string) map[excerptKey]string {
	m := map[excerptKey]string{}
	file := ""
	for _, l := range strings.Split(rendered, "\n") {
		if sub := fileLine.FindStringSubmatch(l); sub != nil {
			file = sub[1]
			continue
		}
		if sub := excerptLine.FindStringSubmatch(l); sub != nil && file != "" {
			n, _ := strconv.Atoi(sub[1])
			m[excerptKey{file, n}] = strings.TrimSpace(sub[2])
		}
	}
	return m
}
