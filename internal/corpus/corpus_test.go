package corpus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "cuelang.org/go/cue/errors"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/envvars"
)

// recorder stands in for a *testing.T, so that a test can assert what a
// failing cell says. Only the methods the corpus calls are recorded.
type recorder struct {
	testing.TB
	errs []string
	logs []string
	skip string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}
func (r *recorder) Error(args ...any) { r.errs = append(r.errs, fmt.Sprint(args...)) }
func (r *recorder) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}
func (r *recorder) Skipf(format string, args ...any) { r.skip = fmt.Sprintf(format, args...) }

// fails returns a suite named name that reports the given codes, or binds
// when there are none.
func fails(name string, codes ...diag.Code) Suite {
	return Suite{
		Name:        name,
		Description: "a synthetic suite named " + name,
		Run: func(testing.TB, *Case) (string, error) {
			var err cerrors.Error
			for _, c := range codes {
				err = cerrors.Append(err, diag.New(c).Headline("headline of "+string(c)).Err())
			}
			if err == nil {
				return "", nil
			}
			return "", err
		},
	}
}

func notApplicable(s Suite) Suite {
	s.Applies = func(*Case) (bool, string) { return false, "the case has no use" }
	return s
}

const header = `#description: A synthetic case.
#expect: E0141
#spec: docs/invariants.md:220
`

// writeCase writes the archive c.txtar with the comment and files to dir,
// and reads it back.
func writeCase(t *testing.T, dir, comment string, files ...string) *Case {
	t.Helper()
	return writeNamedCase(t, dir, "c", comment, files...)
}

// writeNamedCase writes the archive <name>.txtar with the comment and files
// to dir, and reads it back.
func writeNamedCase(t *testing.T, dir, name, comment string, files ...string) *Case {
	t.Helper()
	ar := &txtar.Archive{Comment: []byte(comment)}
	for i := 0; i+1 < len(files); i += 2 {
		ar.Files = append(ar.Files, txtar.File{Name: files[i], Data: []byte(files[i+1])})
	}
	p := filepath.Join(dir, name+".txtar")
	if err := os.WriteFile(p, txtar.Format(ar), 0o600); err != nil {
		t.Fatal(err)
	}
	return readArchive(t, p, Layout{}).Cases[0]
}

// writeArchive writes the archive <name>.txtar with the comment and files to
// dir, and reads it back as the layout says.
func writeArchive(t *testing.T, dir, name string, l Layout, comment string, files ...string) *Archive {
	t.Helper()
	ar := &txtar.Archive{Comment: []byte(comment)}
	for i := 0; i+1 < len(files); i += 2 {
		ar.Files = append(ar.Files, txtar.File{Name: files[i], Data: []byte(files[i+1])})
	}
	p := filepath.Join(dir, name+".txtar")
	if err := os.WriteFile(p, txtar.Format(ar), 0o600); err != nil {
		t.Fatal(err)
	}
	return readArchive(t, p, l)
}

func readArchive(t *testing.T, p string, l Layout) *Archive {
	t.Helper()
	a, err := ReadArchive(p, l)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// renderOf is the section s renders for a case it ignores.
func renderOf(t testing.TB, s Suite) string {
	out, err := s.Run(t, nil)
	return Render(diag.OpBind, out, err)
}

// testDir is the directory the cell tests name; they write their cases
// elsewhere.
const testDir = "corpus"

// runCell runs c's cell for s in a run of co over cases, or over c alone.
func runCell(t testing.TB, co Corpus, c *Case, s Suite, cases ...*Case) (string, bool) {
	if len(cases) == 0 {
		cases = []*Case{c}
	}
	return co.start(cases).cell(t, c, s)
}

// sectionS is the section of suite s for an archive that is one case, and
// inputA an input of an archive of inputs.
const (
	sectionS = "out/s/bind"
	inputA   = "data/a.json"
)

const goldenOK = "verdict: ok\n"

const goldenE0141 = "verdict: E0141\nE0141 headline of E0141 @ no position\n"

// rendered stands for the golden section a suite renders, in the cell
// tests.
const rendered = "rendered"

func TestCellStatus(t *testing.T) {
	untracked := InstructionUntracked("known/s", "corpus/README.md")
	for _, tc := range []struct {
		name  string
		known bool
		suite Suite
		// golden is the case's out/s/bind: none when empty, or what the
		// suite renders when it is rendered.
		golden string
		want   Status
		// The instruction the failure ends with, or "" for no failure.
		instruction string
	}{
		{name: "pass", suite: fails("s", diag.E0141), golden: rendered, want: StatusPass},
		{name: "known", known: true, suite: fails("s", diag.E0142), golden: rendered, want: StatusKnown},
		{name: "regressed", suite: fails("s", diag.E0142), golden: goldenE0141, want: StatusRegressed, instruction: instructionRegressed},
		{name: "regressed-binds", suite: fails("s"), golden: goldenE0141, want: StatusRegressed, instruction: instructionRegressed},
		// A cell that never agreed, by its golden, is a new disagreement
		// rather than a regression.
		{name: "untracked", suite: fails("s", diag.E0142), golden: rendered, want: StatusRegressed, instruction: untracked},
		{name: "untracked-no-golden", suite: fails("s", diag.E0142), want: StatusRegressed, instruction: untracked},
		{name: "xpass", known: true, suite: fails("s", diag.E0141), golden: rendered, want: StatusXPass},
		{name: "known-moved", known: true, suite: fails("s", diag.E0140), golden: "verdict: E0142\n", want: StatusKnown,
			instruction: InstructionKnownMoved("known/s", "E0142", "E0140")},
		{name: "known-accepts-regression", known: true, suite: fails("s", diag.E0140), golden: goldenE0141, want: StatusKnown,
			instruction: InstructionKnownMoved("known/s", "E0141", "E0140")},
		{name: "now-passes", suite: fails("s", diag.E0141), golden: "verdict: E0142\n", want: StatusPass,
			instruction: InstructionNowPasses("E0142")},
		{name: "not-applicable", suite: notApplicable(fails("s", diag.E0141)), want: StatusNotApplicable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comment := header
			if tc.known {
				comment += "#known/s: #264 a reason naming the mechanism.\n"
			}
			var files []string
			switch tc.golden {
			case "":
			case rendered:
				files = []string{sectionS, renderOf(t, tc.suite)}
			default:
				files = []string{sectionS, tc.golden}
			}
			c := writeCase(t, t.TempDir(), comment, files...)
			co := Corpus{Dir: testDir, Suites: []Suite{tc.suite}}

			r := &recorder{TB: t}
			sec, _ := runCell(r, co, c, tc.suite)
			_, v, _ := sectionVerdict(renderOf(t, tc.suite))
			if got := statusOf(c, "s", v); tc.suite.Applies == nil && got != tc.want {
				t.Errorf("status = %s, want %s", got, tc.want)
			}
			// CellStatus reads the archive alone: it agrees with the cell
			// when the golden section is what the suite renders.
			switch got := CellStatus(c, "s"); {
			case tc.golden == "" && got != StatusNotApplicable:
				t.Errorf("CellStatus = %s without a golden section, want not-applicable", got)
			case tc.golden == rendered && got != tc.want:
				t.Errorf("CellStatus = %s, want %s", got, tc.want)
			}
			if tc.want == StatusXPass {
				tc.instruction = InstructionXPass("known/s", c.Path, 264, nil)
			}
			switch {
			case tc.instruction == "" && len(r.errs) > 0:
				t.Errorf("unexpected failure: %q", r.errs)
			case tc.instruction != "" && (len(r.errs) != 1 || !strings.Contains(r.errs[0], tc.instruction)):
				t.Errorf("want one failure ending in %q, got %q", tc.instruction, r.errs)
			}
			if want := renderOf(t, tc.suite); tc.suite.Applies == nil && sec != want {
				t.Errorf("the cell returned %q, want what it rendered, %q", sec, want)
			}
			// What an update writes for the cell reads back with the cell's
			// status, so a regression stays visible to the report.
			if tc.suite.Applies == nil {
				rewritten := writeCase(t, t.TempDir(), comment, sectionS, sec)
				if got := CellStatus(rewritten, "s"); got != tc.want {
					t.Errorf("after an update, CellStatus = %s, want %s", got, tc.want)
				}
			}
			if tc.want == StatusKnown && (len(r.logs) != 1 || r.logs[0] != "known: #264 a reason naming the mechanism.") {
				t.Errorf("a known cell logs one line naming the issue; got %q", r.logs)
			}
			if tc.suite.Applies != nil && r.skip != "does not apply: the case has no use" {
				t.Errorf("a cell that does not apply is skipped with the reason; got %q", r.skip)
			}
		})
	}
}

// TestKnownMovedOnUpdate checks that a golden update rewrites the section
// of a known cell whose verdict changed, but still fails: the tag isn't
// rewritten, and nothing else would say it may be stale.
func TestKnownMovedOnUpdate(t *testing.T) {
	update := envvars.UpdateGoldenFiles
	envvars.UpdateGoldenFiles = true
	t.Cleanup(func() { envvars.UpdateGoldenFiles = update })

	s := fails("s", diag.E0140)
	c := writeCase(t, t.TempDir(), header+"#known/s: #264 a reason.\n", sectionS, "verdict: E0142\n")
	r := &recorder{TB: t}
	sec, _ := runCell(r, Corpus{Dir: testDir, Suites: []Suite{s}}, c, s)
	if sec != renderOf(t, s) {
		t.Errorf("the section is not rewritten: %q", sec)
	}
	if want := InstructionKnownMoved("known/s", "E0142", "E0140"); len(r.errs) != 1 || !strings.Contains(r.errs[0], want) {
		t.Errorf("want one failure ending in %q, got %q", want, r.errs)
	}
}

// TestNoLongerApplies checks that a suite that stops running a case fails
// the cell, with or without a golden update, and that the update keeps its
// section.
func TestNoLongerApplies(t *testing.T) {
	never := notApplicable(fails("n", diag.E0141))
	for _, updating := range []bool{false, true} {
		t.Run(fmt.Sprint("update=", updating), func(t *testing.T) {
			update := envvars.UpdateGoldenFiles
			envvars.UpdateGoldenFiles = updating
			t.Cleanup(func() { envvars.UpdateGoldenFiles = update })

			c := writeCase(t, t.TempDir(), header+"#known/n: #264 a reason.\n", "out/n/bind", goldenE0141)
			r := &recorder{TB: t}
			sec, ok := runCell(r, Corpus{Dir: testDir, Suites: []Suite{never}}, c, never)
			if !ok || sec != goldenE0141 {
				t.Errorf("the section is not kept: %q, %v", sec, ok)
			}
			want := "suite n no longer applies to this case (the case has no use), but out/n/bind records it running"
			if len(r.errs) != 1 || !strings.Contains(r.errs[0], want) || !strings.Contains(r.errs[0], "delete the section and the #known/n tag by hand") {
				t.Errorf("want one failure containing %q, got %q", want, r.errs)
			}
		})
	}
}

// TestUnusedSuites checks that a suite that applies to no case fails the
// run.
func TestUnusedSuites(t *testing.T) {
	c := writeCase(t, t.TempDir(), header)
	co := Corpus{Dir: testDir, Suites: []Suite{fails("s"), notApplicable(fails("n"))}}
	want := []string{"suite n applies to no case in corpus, so it checks nothing; add a case it applies to, or remove the suite"}
	if got := co.unusedSuites([]*Case{c}); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestXPassOthers checks that an xpass names the other cells its issue
// tracks.
func TestXPassOthers(t *testing.T) {
	s, k := fails("s", diag.E0141), fails("k", diag.E0142)
	dir := t.TempDir()
	other := writeNamedCase(t, dir, "other", header+"#known/k: #264 a reason.\n#known/s: #264 a reason.\n")
	c := writeCase(t, dir, header+"#known/s: #264 a reason.\n#known/k: #264 a reason.\n#known/x: #12 another issue.\n", sectionS, goldenE0141)
	r := &recorder{TB: t}
	runCell(r, Corpus{Dir: testDir, Suites: []Suite{s, k}}, c, s, c, other)
	want := InstructionXPass("known/s", c.Path, 264, []string{"c/k", "other/k", "other/s"})
	if !strings.Contains(want, "#264 also tracks c/k, other/k, other/s, so leave it open") {
		t.Errorf("the instruction does not name the other cells: %s", want)
	}
	if len(r.errs) != 1 || !strings.Contains(r.errs[0], want) {
		t.Errorf("want one failure containing %q, got %q", want, r.errs)
	}
}

// TestDescribedOnce checks that a run describes a case and a suite in full
// only in the first failure that names them.
func TestDescribedOnce(t *testing.T) {
	s1, s2 := fails("s1", diag.E0142), fails("s2", diag.E0142)
	c := writeCase(t, t.TempDir(), header)
	rn := Corpus{Dir: testDir, Suites: []Suite{s1, s2}}.start([]*Case{c})
	r := &recorder{TB: t}
	rn.cell(r, c, s1)
	rn.cell(r, c, s2)
	rn.cell(r, c, s1)
	if len(r.errs) != 3 {
		t.Fatalf("want three failures, got %q", r.errs)
	}
	for i, want := range []string{
		"case c: A synthetic case.\nsuite s1: a synthetic suite named s1\n",
		"case c (described above)\nsuite s2: a synthetic suite named s2\n",
		"case c (described above)\nsuite s1 (described above, and in corpus/suites.json)\n",
	} {
		if !strings.HasPrefix(r.errs[i], want) {
			t.Errorf("failure %d starts\n%s\nwant\n%s", i, r.errs[i][:min(len(r.errs[i]), len(want))], want)
		}
	}
}

// TestDisplay checks that a corpus that knows the repository's root prints
// paths relative to it.
func TestDisplay(t *testing.T) {
	co := Corpus{Dir: "../../testdata/refs", Root: "../.."}
	if got := co.display(co.readme()); got != "testdata/refs/README.md" {
		t.Errorf("display = %s, want testdata/refs/README.md", got)
	}
	if got := (Corpus{Dir: "corpus"}).display("corpus/README.md"); got != "corpus/README.md" {
		t.Errorf("without a root, display = %s", got)
	}
}

// TestCitations checks that a corpus that knows the repository's root
// resolves each #spec citation.
func TestCitations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "spec.md"), []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := writeCase(t, t.TempDir(), "#description: d\n#expect: ok\n#spec: spec.md:1-3, spec.md:2, spec.md:3-4, nope.md:1\n")
	want := []string{
		"#spec: spec.md has 3 lines, so it has no line 4",
		"#spec: nope.md is not a file of the repository",
	}
	if got := c.resolveCitations(root); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestCellStatusFromArchive checks how CellStatus reads a verdict line that
// Run would not have written.
func TestCellStatusFromArchive(t *testing.T) {
	for _, tc := range []struct {
		name, known, golden string
		want                Status
	}{
		{name: "unsorted", golden: "verdict: E0141  E0141\n", want: StatusPass},
		{name: "no-verdict-line", golden: goldenE0141[len("verdict: E0141\n"):], want: StatusRegressed},
		{name: "no-verdict-line-known", known: "#known/s: #264 a reason.\n", golden: "E0141\n", want: StatusKnown},
		{name: "undeclared-code", golden: "verdict: E9999\n", want: StatusRegressed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := writeCase(t, t.TempDir(), header+tc.known, sectionS, tc.golden)
			if got := CellStatus(c, "s"); got != tc.want {
				t.Errorf("CellStatus = %s, want %s", got, tc.want)
			}
		})
	}
	// An #expect that could not be read agrees with nothing, not even a
	// verdict line that could not be read either.
	c := writeCase(t, t.TempDir(), "#description: d\n#expect: E0141 ok\n#spec: docs/invariants.md:1\n", sectionS, "verdict: E0141 ok\n")
	if got := CellStatus(c, "s"); got != StatusRegressed {
		t.Errorf("CellStatus = %s, want regressed", got)
	}
}

func TestFailureMessage(t *testing.T) {
	c := writeCase(t, t.TempDir(), header+"#known/s: #264 a reason naming the mechanism.\n",
		sectionS, "verdict: E0142\nE0142 an older headline @ no position\n")
	s := fails("s", diag.E0142)
	r := &recorder{TB: t}
	runCell(r, Corpus{Dir: testDir, Suites: []Suite{s}}, c, s)
	want := `case c: A synthetic case.
suite s: a synthetic suite named s
#expect: E0141 (spec: docs/invariants.md:220)
got: E0142
	E0142 headline of E0142 @ no position
#known/s: #264 a reason naming the mechanism.
How to read this corpus: corpus/README.md
`
	if len(r.errs) != 1 || !strings.HasPrefix(r.errs[0], want) {
		t.Fatalf("want a failure starting\n%s\ngot %q", want, r.errs)
	}
	// A known cell whose rendering moved fails on the rendering, with the diff.
	if !strings.Contains(r.errs[0], instructionRendering) || !strings.Contains(r.errs[0], sectionDiffHeader) {
		t.Errorf("want the rendering instruction and the diff, got\n%s", r.errs[0])
	}
}

func TestRenderingChanged(t *testing.T) {
	c := writeCase(t, t.TempDir(), header, sectionS, "verdict: E0141\nE0141 an older headline @ no position\n")
	s := fails("s", diag.E0141)
	r := &recorder{TB: t}
	runCell(r, Corpus{Dir: testDir, Suites: []Suite{s}}, c, s)
	if len(r.errs) != 1 {
		t.Fatalf("want one failure, got %q", r.errs)
	}
	// cmp's output is deliberately unstable, so only what the diff names is
	// checked.
	msg, diff, _ := strings.Cut(r.errs[0], sectionDiffHeader)
	if !strings.Contains(msg, instructionRendering) {
		t.Errorf("failure does not give the rendering instruction:\n%s", r.errs[0])
	}
	for _, want := range []string{"E0141 an older headline", "E0141 headline of E0141"} {
		if !strings.Contains(diff, want) {
			t.Errorf("the diff does not show %q:\n%s", want, r.errs[0])
		}
	}
}

func TestInstructions(t *testing.T) {
	for got, want := range map[string]string{
		instructionRegressed: "This is a regression: the golden section records this suite giving #expect, and it no longer does. " +
			"Do not add a #known tag to make it pass. THEMA_UPDATE_GOLDEN=1 records what the suite gives now, and the cell keeps failing until the suite agrees again. " +
			"If you believe #expect is wrong, it comes from the spec cited above: open an issue citing those lines instead of changing it.",
		InstructionUntracked("known/fill-text", "testdata/refs/README.md"): "This suite disagrees with #expect, no golden section records it agreeing, and no #known tag tracks the disagreement. " +
			"If the case is new, this is step 5 of \"Adding a case\" in testdata/refs/README.md: find the issue that covers the mechanism and add " +
			"'#known/fill-text: #<issue> <one sentence naming the mechanism>', or, if no issue covers it, open one citing the #spec lines. " +
			"If git shows the section agreeing with #expect before your change, your change broke it: fix the code instead of tagging the cell.",
		InstructionXPass("known/fill-text", "testdata/refs/c.txtar", 260, nil): "This suite now gives the verdict the spec expects: your change fixed it. " +
			"Remove the '#known/fill-text' line from testdata/refs/c.txtar, run with THEMA_UPDATE_GOLDEN=1 to rewrite the section, and mention issue #260 in your commit. " +
			"No other cell of this corpus carries #260, so it can be closed unless it tracks more than this corpus.",
		InstructionXPass("known/fill-text", "testdata/refs/c.txtar", 260, []string{"c/fill-text-named"}): "This suite now gives the verdict the spec expects: your change fixed it. " +
			"Remove the '#known/fill-text' line from testdata/refs/c.txtar, run with THEMA_UPDATE_GOLDEN=1 to rewrite the section, and mention issue #260 in your commit. " +
			"#260 also tracks c/fill-text-named, so leave it open until those pass too.",
		InstructionKnownMoved("known/fill-use", "E0126", "E0141"): "This suite's verdict changed from E0126 (the golden section) to E0141. " +
			"The #known tag accepts any verdict but #expect, so it may now name the wrong mechanism. " +
			"Check that #known/fill-use explains E0141, or whether this is a different bug; update the tag, then run with THEMA_UPDATE_GOLDEN=1, only once it does.",
		InstructionNowPasses("E0141"): "This suite now gives #expect; the golden section records E0141. " +
			"If your change fixed this cell and you removed its #known tag, run with THEMA_UPDATE_GOLDEN=1 to rewrite the section.",
		instructionRendering: "The verdict is unchanged, but what the suite reports changed (codes, headlines, positions or what it produced). " +
			"Review the diff below; if it is intended, run with THEMA_UPDATE_GOLDEN=1.",
	} {
		if got != want {
			t.Errorf("instruction\n got: %s\nwant: %s", got, want)
		}
	}
}

func TestValidation(t *testing.T) {
	applies, never := fails("s", diag.E0141), notApplicable(fails("n", diag.E0141))
	co := Corpus{Dir: testDir, Layout: Layout{Require: []string{"at"}}, Suites: []Suite{applies, never}}
	for _, tc := range []struct {
		name string
		// caseName names the archive, c when empty.
		caseName string
		comment  string
		files    []string
		// A cell to run, for the failures only a cell finds.
		cell Suite
		want string
	}{
		{
			name:    "missing-description",
			comment: "#expect: ok\n#spec: docs/invariants.md:1\n#at: lin\n",
			want:    "the required directive #description is missing",
		},
		{
			name:    "missing-corpus-directive",
			comment: header,
			want:    "the required directive #at is missing",
		},
		{
			name:    "expect-not-a-code",
			comment: "#description: d\n#expect: ok E0141\n#spec: docs/invariants.md:1\n#at: lin\n",
			want:    `#expect: "ok" is not a diagnostic code`,
		},
		{
			name:    "spec-not-a-citation",
			comment: "#description: d\n#expect: ok\n#spec: docs/invariants.md:1, the References section\n#at: lin\n",
			want:    `#spec: "the References section" is not <path>:<line>[-<line>]`,
		},
		{
			name:    "known-unregistered",
			comment: header + "#at: lin\n#known/gone: #264 a reason.\n",
			want:    "#known/gone names a suite that is not registered",
		},
		{
			name:    "known-without-issue",
			comment: header + "#at: lin\n#known/s: a reason with no issue.\n",
			want:    `#known/s: "a reason with no issue." must start with the issue that tracks it`,
		},
		{
			name:    "known-stale",
			comment: header + "#at: lin\n#known/n: #264 a reason.\n",
			cell:    never,
			want:    "#known/n is stale: the suite does not apply to this case (the case has no use)",
		},
		{
			name:    "orphan-unregistered",
			comment: header + "#at: lin\n",
			files:   []string{"out/gone/bind", goldenE0141},
			want:    "out/gone/bind is an orphan: no suite \"gone\" is registered",
		},
		{
			name:    "no-longer-applies",
			comment: header + "#at: lin\n",
			files:   []string{"out/n/bind", goldenE0141},
			cell:    never,
			want:    "suite n no longer applies to this case (the case has no use), but out/n/bind records it running",
		},
		{
			name:    "directive-repeated",
			comment: header + "#at: lin\n#expect: ok\n",
			want:    "#expect is given more than once",
		},
		{
			name:    "known-repeated",
			comment: header + "#at: lin\n#known/s: #264 a reason.\n#known/s: #12 another.\n",
			want:    "#known/s is given more than once",
		},
		{
			name:    "section-repeated",
			comment: header + "#at: lin\n",
			files:   []string{sectionS, goldenE0141, sectionS, goldenOK},
			want:    "out/s/bind appears more than once; only the first is compared",
		},
		{
			name:    "input-repeated",
			comment: header + "#at: lin\n",
			files:   []string{"x.cue", "lin: _\n", "x.cue", "lin: 1\n"},
			want:    "the file x.cue appears more than once; only the first is read",
		},
		{
			name:    "spec-reversed",
			comment: "#description: d\n#expect: ok\n#spec: docs/invariants.md:9-2\n#at: lin\n",
			want:    `#spec: "docs/invariants.md:9-2" is not a range of lines from the first to the last`,
		},
		{
			name:     "name-not-kebab",
			caseName: "Bad_Name",
			comment:  header + "#at: lin\n",
			want:     `the case's name "Bad_Name" is not kebab-case`,
		},
		{
			name:    "unknown-section",
			comment: header + "#at: lin\n",
			files:   []string{"out/s/verdict", goldenE0141},
			want:    "out/s/verdict is not a section the corpus writes",
		},
		{
			name:    "section-no-verdict-line",
			comment: header + "#at: lin\n",
			files:   []string{sectionS, "E0141 headline of E0141 @ no position\n"},
			want:    `out/s/bind: the first line is not "verdict: " followed by the verdict`,
		},
		{
			name:    "section-verdict-unsorted",
			comment: header + "#at: lin\n",
			files:   []string{sectionS, "verdict: E0141 E0140\n"},
			want:    `out/s/bind: verdict "E0141 E0140" is not sorted and de-duplicated ("E0140 E0141")`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := tc.caseName
			if name == "" {
				name = "c"
			}
			a := writeArchive(t, t.TempDir(), name, co.Layout, tc.comment, tc.files...)
			r := &recorder{TB: t}
			if tc.cell.Name != "" {
				runCell(r, co, a.Cases[0], tc.cell)
			} else {
				co.start(nil).checkArchive(r, a)
			}
			if len(r.errs) != 1 || !strings.Contains(r.errs[0], tc.want) {
				t.Errorf("want one failure containing %q, got %q", tc.want, r.errs)
			}
		})
	}
}

func TestRegistry(t *testing.T) {
	msgs := checkRegistry([]Suite{
		fails("ok"),
		fails("ok"),
		{Name: "Not_Kebab", Description: "d", Run: fails("x").Run},
		{Name: "no-description", Run: fails("x").Run},
	})
	want := []string{
		`suite "ok" is registered twice`,
		`suite 2: name "Not_Kebab" is not kebab-case`,
		`suite "no-description" has no description`,
	}
	if strings.Join(msgs, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(msgs, "\n"), strings.Join(want, "\n"))
	}
}

func TestVerdict(t *testing.T) {
	v, err := ParseVerdict("E0141  E0140 E0141")
	if err != nil || v != "E0140 E0141" {
		t.Errorf("ParseVerdict = %q, %v; want the sorted set E0140 E0141", v, err)
	}
	if v, err := ParseVerdict("ok"); err != nil || v != VerdictOK {
		t.Errorf("ParseVerdict(ok) = %q, %v", v, err)
	}
	if _, err := ParseVerdict("E9999"); err == nil {
		t.Error("ParseVerdict accepted a code that is not declared")
	}

	got := renderOf(t, fails("s", diag.E0141, diag.E0140, diag.E0141))
	want := "verdict: E0140 E0141\n" +
		"E0140 headline of E0140 @ no position\n" +
		"E0141 headline of E0141 @ no position\n" +
		"E0141 headline of E0141 @ no position\n"
	if got != want {
		t.Errorf("Render =\n%s\nwant\n%s", got, want)
	}
	if got := Render(diag.OpBind, "", nil); got != goldenOK {
		t.Errorf("Render(nil) = %q", got)
	}
	if got := Render(diag.OpValidate, "value: {}", nil); got != "verdict: ok\nvalue: {}\n" {
		t.Errorf("Render with what the operation produced = %q", got)
	}
	if got := Render(diag.OpBind, "", errors.New("not a diagnostic")); !strings.HasPrefix(got, "verdict: E0903\n") {
		t.Errorf("an error that is not a diagnostic is rendered as one with no code; got %q", got)
	}
}

// TestWrong checks that a suite's wrong result is its own verdict, which a
// section records with its detail and #expect may not give, and which a
// #known tag tracks.
func TestWrong(t *testing.T) {
	got := Render(diag.OpValidate, "", fmt.Errorf("checking: %w", &Wrong{Detail: "the value is 0, and the instance's is 1"}))
	if want := "verdict: wrong\nthe value is 0, and the instance's is 1\n"; got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
	if _, err := ParseVerdict("wrong"); err == nil {
		t.Error("ParseVerdict accepted wrong, which no spec gives")
	}
	c := writeCase(t, t.TempDir(), "#description: d\n#expect: ok\n#spec: docs/invariants.md:1\n#known/s: #295 a reason.\n",
		sectionS, got)
	if st := CellStatus(c, "s"); st != StatusKnown {
		t.Errorf("a cell whose section is wrong, with a tag, is %s; want known", st)
	}
	if d, s := c.archive.Problems([]string{"s"}); len(d)+len(s) > 0 {
		t.Errorf("problems: %q %q", d, s)
	}
}

func TestSuitePanics(t *testing.T) {
	s := Suite{Name: "p", Description: "d", Run: func(testing.TB, *Case) (string, error) { panic("boom") }}
	out, err := runSuite(t, s, nil)
	if got := Render(diag.OpBind, out, err); !strings.HasPrefix(got, "verdict: E0901\n") {
		t.Errorf("a panicking suite's verdict is E0901; got %q", got)
	}
}

// TestUpdate checks that THEMA_UPDATE_GOLDEN rewrites a case's out/
// sections, drops the ones of suites no longer registered, and leaves its
// comment and input files as they were. A suite registered but no longer
// applying keeps its section (TestNoLongerApplies).
func TestUpdate(t *testing.T) {
	update := envvars.UpdateGoldenFiles
	envvars.UpdateGoldenFiles = true
	t.Cleanup(func() { envvars.UpdateGoldenFiles = update })

	dir := t.TempDir()
	comment := header + "#known/k: #264 a reason naming the mechanism.\n\nFree prose stays too.\n"
	c := writeCase(t, dir, comment,
		"in.cue", "lin: _\n",
		sectionS, "verdict: E0141\nE0141 an older headline @ no position\n",
		"out/gone/bind", goldenE0141)
	co := Corpus{Dir: dir, Suites: []Suite{fails("s", diag.E0141), fails("k", diag.E0142)}}
	t.Run("run", co.Run)

	updated := readArchive(t, c.Path, Layout{})
	ar := updated.ar.Ar
	if string(ar.Comment) != comment {
		t.Errorf("the comment changed:\n%s", ar.Comment)
	}
	var names []string
	for _, f := range ar.Files {
		names = append(names, f.Name)
	}
	if got, want := strings.Join(names, " "), "in.cue out/k/bind out/s/bind"; got != want {
		t.Errorf("files are %s, want %s", got, want)
	}
	if got, _ := updated.Cases[0].File(sectionS); got != goldenE0141 {
		t.Errorf("out/s/bind was not rewritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "suites.json")); err != nil {
		t.Errorf("suites.json was not written: %v", err)
	}
}

// byInput is a suite of a corpus of inputs: it accepts an input whose file
// holds "ok", producing a value, and refuses any other with the codes the
// file holds.
var byInput = Suite{
	Name:        "s",
	Description: "a synthetic suite that reads its verdict from the input",
	Run: func(t testing.TB, c *Case) (string, error) {
		data, ok := c.File(c.InputFile)
		if !ok {
			t.Fatalf("case %s has no input file %s", c.Name, c.InputFile)
		}
		if strings.TrimSpace(data) == "ok" {
			return "value: " + c.Input, nil
		}
		var err cerrors.Error
		for _, code := range strings.Fields(data) {
			err = cerrors.Append(err, diag.New(diag.Code(code)).Headline("headline of "+code).Err())
		}
		return "", err
	},
}

var inputsLayout = Layout{Inputs: "data/*.json"}

// TestInputs checks that a corpus of inputs runs each input of an archive as
// a case of its own, under its own directives, and that an update writes one
// section for each input and suite.
func TestInputs(t *testing.T) {
	update := envvars.UpdateGoldenFiles
	t.Cleanup(func() { envvars.UpdateGoldenFiles = update })

	dir := t.TempDir()
	comment := "#description: Inputs of one schema.\n#spec: docs/invariants.md:1\n" +
		"#expect/fine: ok\n#expect/broken: E0504\n#description/broken: An input out of bounds.\n"
	a := writeArchive(t, dir, "c", inputsLayout, comment,
		"in.cue", "lin: _\n",
		"data/fine.json", "ok\n",
		"data/broken.json", "E0504\n",
		"notes.md", "not an input\n")
	if got := len(a.Cases); got != 2 {
		t.Fatalf("%d cases, want one for each of the two inputs", got)
	}
	fine, broken := a.Cases[0], a.Cases[1]
	if fine.Name != "c/fine" || fine.Input != "fine" || fine.InputFile != "data/fine.json" {
		t.Errorf("first case is %s (input %s in %s), want c/fine from data/fine.json", fine.Name, fine.Input, fine.InputFile)
	}
	if fine.Description != "Inputs of one schema." || broken.Description != "An input out of bounds." {
		t.Errorf("descriptions %q and %q: an input's own #description/<input> overrides the archive's", fine.Description, broken.Description)
	}
	if fine.Expect != VerdictOK || broken.Expect != "E0504" {
		t.Errorf("verdicts %s and %s, want ok and E0504", fine.Expect, broken.Expect)
	}
	if got := fine.SectionFile("s"); got != "out/s/fine" {
		t.Errorf("an input's section is %s, want out/s/fine", got)
	}

	co := Corpus{Dir: dir, Layout: inputsLayout, Op: diag.OpValidate, Suites: []Suite{byInput}}
	envvars.UpdateGoldenFiles = true
	t.Run("update", co.Run)
	updated := readArchive(t, a.Path, inputsLayout)
	for file, want := range map[string]string{
		"out/s/fine":   "verdict: ok\nvalue: fine\n",
		"out/s/broken": "verdict: E0504\nE0504 headline of E0504 @ no position\n",
	} {
		if got, _ := updated.Cases[0].File(file); got != want {
			t.Errorf("%s is %q, want %q", file, got, want)
		}
	}
	if string(updated.ar.Ar.Comment) != comment {
		t.Errorf("the comment changed:\n%s", updated.ar.Ar.Comment)
	}

	envvars.UpdateGoldenFiles = false
	r := &recorder{TB: t}
	for _, c := range updated.Cases {
		runCell(r, co, c, byInput, updated.Cases...)
	}
	if len(r.errs) > 0 {
		t.Errorf("cells fail after the update: %q", r.errs)
	}
	if d, s := updated.Problems([]string{"s"}); len(d)+len(s) > 0 {
		t.Errorf("problems after the update: %q %q", d, s)
	}
}

// TestInputKnown checks that a #known tag in an archive of inputs tracks the
// one input it names, and that the instructions name the input's own tag.
func TestInputKnown(t *testing.T) {
	comment := "#description: d\n#spec: docs/invariants.md:1\n#expect: E0141\n" +
		"#known/s/two: #12 this input.\n"
	a := writeArchive(t, t.TempDir(), "c", inputsLayout, comment,
		"data/one.json", "ok\n", "data/two.json", "ok\n")
	one, two := a.Cases[0], a.Cases[1]
	if _, ok := one.Known["s"]; ok {
		t.Errorf("%s is tagged by another input's tag: %+v", one.Name, one.Known)
	}
	if got, want := two.Known["s"], (Known{Issue: 12, Reason: "this input."}); got != want {
		t.Errorf("%s: tag %+v, want %+v", two.Name, got, want)
	}
	for _, c := range a.Cases {
		if got, want := c.KnownKey("s"), "known/s/"+c.Input; got != want {
			t.Errorf("%s: tag key %s, want %s", c.Name, got, want)
		}
	}
	r := &recorder{TB: t}
	runCell(r, Corpus{Dir: testDir, Layout: inputsLayout}, one, fails("u", diag.E0140), a.Cases...)
	if want := "'#known/u/one: #<issue>"; len(r.errs) != 1 || !strings.Contains(r.errs[0], want) {
		t.Errorf("want one failure containing %q, got %q", want, r.errs)
	}
	// An xpass names the tag to remove, which tracks this input alone.
	r = &recorder{TB: t}
	runCell(r, Corpus{Dir: testDir, Layout: inputsLayout}, two, fails("s", diag.E0141), a.Cases...)
	if want := "Remove the '#known/s/two' line"; len(r.errs) != 1 || !strings.Contains(r.errs[0], want) {
		t.Errorf("want one failure containing %q, got %q", want, r.errs)
	}
}

// TestUnreadDirectives checks that a directive the corpus does not read is
// prose, whatever its key, and that a malformed tag is one problem even when
// it names a suite that is not registered.
func TestUnreadDirectives(t *testing.T) {
	a := writeArchive(t, t.TempDir(), "c", Layout{}, header+"#see/also: prose\n#known/zz: no issue\n")
	d, s := a.Problems([]string{"s"})
	want := `#known/zz: "no issue" must start with the issue that tracks it, as "#<number> <reason>"`
	if len(d) != 1 || d[0] != want || len(s) > 0 {
		t.Errorf("problems %q %q, want only %q", d, s, want)
	}
}

func TestInputValidation(t *testing.T) {
	const head = "#description: d\n#spec: docs/invariants.md:1\n"
	for _, tc := range []struct {
		name    string
		layout  Layout
		comment string
		files   []string
		want    string
	}{
		{
			name:    "expect-missing-for-input",
			layout:  inputsLayout,
			comment: head + "#expect/a: ok\n",
			files:   []string{inputA, "ok", "data/b.json", "ok"},
			want:    "the required directive #expect is missing for input b; write #expect/b, or #expect for every input",
		},
		{
			name:    "directive-names-no-input",
			layout:  inputsLayout,
			comment: head + "#expect: ok\n#expect/gone: E0504\n",
			files:   []string{inputA, "ok"},
			want:    "#expect/gone names gone, which is not an input of the archive",
		},
		{
			name:    "known-names-no-input",
			layout:  inputsLayout,
			comment: head + "#expect: ok\n#known/s/gone: #264 a reason.\n",
			files:   []string{inputA, "ok"},
			want:    "#known/s/gone names gone, which is not an input of the archive",
		},
		{
			name:    "no-inputs",
			layout:  inputsLayout,
			comment: head + "#expect: ok\n",
			files:   []string{"in.cue", "lin: _\n"},
			want:    "the archive has no input: no file matches data/*.json",
		},
		{
			name:    "input-not-kebab",
			layout:  inputsLayout,
			comment: head + "#expect: ok\n",
			files:   []string{"data/Bad_Input.json", "ok"},
			want:    `the input's name "Bad_Input" (data/Bad_Input.json) is not kebab-case`,
		},
		{
			name:    "inputs-same-name",
			layout:  Layout{Inputs: "data/*"},
			comment: head + "#expect: ok\n",
			files:   []string{inputA, "ok", "data/a.yaml", "ok"},
			want:    "the inputs data/a.json and data/a.yaml have the same name, a; rename one",
		},
		{
			name:    "input-section-not-written",
			layout:  inputsLayout,
			comment: head + "#expect: ok\n",
			files:   []string{inputA, "ok", sectionS, goldenOK},
			want:    "out/s/bind is not a section the corpus writes; a suite's sections are out/s/<input>, one for each input",
		},
		{
			name:    "input-directive-without-inputs",
			comment: head + "#expect: ok\n#expect/a: E0504\n",
			want:    "#expect/a names an input, but the cases of this corpus have none; write #expect",
		},
		{
			name:    "known-for-every-input",
			layout:  inputsLayout,
			comment: head + "#expect: ok\n#known/s: #264 a reason.\n",
			files:   []string{inputA, "ok"},
			want:    "#known/s tags every input, but a tag tracks one input's cell; write #known/s/<input> for each input the suite disagrees on",
		},
		{
			name:    "input-without-name",
			layout:  inputsLayout,
			comment: head + "#expect: ok\n",
			files:   []string{inputA, "ok", "data/.json", "ok"},
			want:    "the input data/.json has no name, only an extension; name the file",
		},
		{
			name:    "overridden-expect-read",
			layout:  inputsLayout,
			comment: head + "#expect: E9999\n#expect/a: ok\n",
			files:   []string{inputA, "ok"},
			want:    "#expect: E9999 is not declared in diag.Codes",
		},
		{
			name:    "input-part-empty",
			layout:  inputsLayout,
			comment: head + "#expect: ok\n#expect/: E0504\n",
			files:   []string{inputA, "ok"},
			want:    "#expect/ names no input; write #expect/<input>, or #expect for every input",
		},
		{
			name:    "known-input-part-empty",
			layout:  inputsLayout,
			comment: head + "#expect: ok\n#known/s/: #264 a reason.\n",
			files:   []string{inputA, "ok"},
			want:    "#known/s/ names no input; write #known/s/<input>, or #known/s for every input",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := writeArchive(t, t.TempDir(), "c", tc.layout, tc.comment, tc.files...)
			r := &recorder{TB: t}
			Corpus{Dir: testDir, Layout: tc.layout, Suites: []Suite{byInput}}.start(nil).checkArchive(r, a)
			if len(r.errs) != 1 || !strings.Contains(r.errs[0], tc.want) {
				t.Errorf("want one failure containing %q, got %q", tc.want, r.errs)
			}
		})
	}
}
