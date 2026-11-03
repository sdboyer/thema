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
		Bind: func(testing.TB, *Case) error {
			var err cerrors.Error
			for _, c := range codes {
				err = cerrors.Append(err, diag.New(c).Headline("headline of "+string(c)).Err())
			}
			if err == nil {
				return nil
			}
			return err
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
	ar := &txtar.Archive{Comment: []byte(comment)}
	for i := 0; i+1 < len(files); i += 2 {
		ar.Files = append(ar.Files, txtar.File{Name: files[i], Data: []byte(files[i+1])})
	}
	p := filepath.Join(dir, "c.txtar")
	if err := os.WriteFile(p, txtar.Format(ar), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := ReadCase(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// testDir is the directory the cell tests name; they write their cases
// elsewhere.
const testDir = "corpus"

const goldenE0141 = "verdict: E0141\nE0141 headline of E0141 @ no position\n"

func TestCellStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		known  bool
		suite  Suite
		golden bool
		want   Status
		// The instruction the failure ends with, or "" for no failure.
		instruction string
	}{
		{name: "pass", suite: fails("s", diag.E0141), golden: true, want: StatusPass},
		{name: "known", known: true, suite: fails("s", diag.E0142), golden: true, want: StatusKnown},
		{name: "regressed", suite: fails("s", diag.E0142), golden: true, want: StatusRegressed, instruction: instructionRegressed},
		{name: "regressed-binds", suite: fails("s"), golden: true, want: StatusRegressed, instruction: instructionRegressed},
		{name: "xpass", known: true, suite: fails("s", diag.E0141), golden: true, want: StatusXPass},
		{name: "not-applicable", suite: notApplicable(fails("s", diag.E0141)), want: StatusNotApplicable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comment := header
			if tc.known {
				comment += "#known/s: #264 a reason naming the mechanism.\n"
			}
			var files []string
			if tc.golden {
				// The golden section is what the suite renders, so the only
				// failures are the ones the status calls for.
				files = []string{"out/s/bind", Render(tc.suite.Bind(t, nil))}
			}
			c := writeCase(t, t.TempDir(), comment, files...)
			co := Corpus{Dir: testDir, Suites: []Suite{tc.suite}}

			r := &recorder{TB: t}
			co.cell(r, c, tc.suite)
			if got := CellStatus(c, "s"); got != tc.want {
				t.Errorf("CellStatus = %s, want %s", got, tc.want)
			}
			if tc.want == StatusXPass {
				tc.instruction = InstructionXPass("s", c.Path, 264)
			}
			switch {
			case tc.instruction == "" && len(r.errs) > 0:
				t.Errorf("unexpected failure: %q", r.errs)
			case tc.instruction != "" && (len(r.errs) != 1 || !strings.Contains(r.errs[0], tc.instruction)):
				t.Errorf("want one failure ending in %q, got %q", tc.instruction, r.errs)
			}
			if tc.want == StatusKnown && (len(r.logs) != 1 || r.logs[0] != "known: #264 a reason naming the mechanism.") {
				t.Errorf("a known cell logs one line naming the issue; got %q", r.logs)
			}
			if tc.want == StatusNotApplicable && r.skip != "does not apply: the case has no use" {
				t.Errorf("a cell that does not apply is skipped with the reason; got %q", r.skip)
			}
		})
	}
}

func TestFailureMessage(t *testing.T) {
	c := writeCase(t, t.TempDir(), header+"#known/s: #264 a reason naming the mechanism.\n",
		"out/s/bind", goldenE0141)
	s := fails("s", diag.E0142)
	r := &recorder{TB: t}
	Corpus{Dir: testDir, Suites: []Suite{s}}.cell(r, c, s)
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
	c := writeCase(t, t.TempDir(), header, "out/s/bind", "verdict: E0141\nE0141 an older headline @ no position\n")
	s := fails("s", diag.E0141)
	r := &recorder{TB: t}
	Corpus{Dir: testDir, Suites: []Suite{s}}.cell(r, c, s)
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
		instructionRegressed: "This is a regression: the suite now disagrees with the verdict the spec gives. " +
			"Do not update goldens or add a #known tag to make it pass. " +
			"If you believe #expect is wrong, it comes from the spec cited above; raise it with the owner instead of changing it.",
		InstructionXPass("fill-text", "testdata/refs/c.txtar", 260): "This suite now gives the verdict the spec expects: your change fixed it. " +
			"Remove the '#known/fill-text' line from testdata/refs/c.txtar, and mention issue #260 in your commit.",
		instructionRendering: "The verdict is unchanged, but what bind reports changed (codes, headlines or positions). " +
			"Review the diff below; if it is intended, run with THEMA_UPDATE_GOLDEN=1.",
	} {
		if got != want {
			t.Errorf("instruction\n got: %s\nwant: %s", got, want)
		}
	}
}

func TestValidation(t *testing.T) {
	applies, never := fails("s", diag.E0141), notApplicable(fails("n", diag.E0141))
	co := Corpus{Dir: testDir, Suites: []Suite{applies, never}, Require: []string{"at"}}
	for _, tc := range []struct {
		name    string
		comment string
		files   []string
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
			comment: "#description: d\n#expect: ok E0141\n#spec: s\n#at: lin\n",
			want:    `#expect: "ok" is not a diagnostic code`,
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
			name:    "orphan-not-applicable",
			comment: header + "#at: lin\n",
			files:   []string{"out/n/bind", goldenE0141},
			cell:    never,
			want:    "out/n/bind is an orphan: the suite does not apply to this case (the case has no use)",
		},
		{
			name:    "unknown-section",
			comment: header + "#at: lin\n",
			files:   []string{"out/s/verdict", goldenE0141},
			want:    "out/s/verdict is not a section the corpus writes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := writeCase(t, t.TempDir(), tc.comment, tc.files...)
			r := &recorder{TB: t}
			if tc.cell.Name != "" {
				co.cell(r, c, tc.cell)
			} else {
				co.checkCase(r, c)
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
		{Name: "Not_Kebab", Description: "d", Bind: fails("x").Bind},
		{Name: "no-description", Bind: fails("x").Bind},
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

	got := Render(fails("s", diag.E0141, diag.E0140, diag.E0141).Bind(t, nil))
	want := "verdict: E0140 E0141\n" +
		"E0140 headline of E0140 @ no position\n" +
		"E0141 headline of E0141 @ no position\n" +
		"E0141 headline of E0141 @ no position\n"
	if got != want {
		t.Errorf("Render =\n%s\nwant\n%s", got, want)
	}
	if got := Render(nil); got != "verdict: ok\n" {
		t.Errorf("Render(nil) = %q", got)
	}
	if got := Render(errors.New("not a diagnostic")); !strings.HasPrefix(got, "verdict: E0903\n") {
		t.Errorf("an error that is not a diagnostic is rendered as one with no code; got %q", got)
	}
}

func TestBindPanics(t *testing.T) {
	s := Suite{Name: "p", Description: "d", Bind: func(testing.TB, *Case) error { panic("boom") }}
	if got := Render(bind(t, s, nil)); !strings.HasPrefix(got, "verdict: E0901\n") {
		t.Errorf("a panicking suite's verdict is E0901; got %q", got)
	}
}

// TestUpdate checks that THEMA_UPDATE_GOLDEN rewrites a case's out/
// sections, drops the ones no suite writes, and leaves its comment and input
// files as they were.
func TestUpdate(t *testing.T) {
	update := envvars.UpdateGoldenFiles
	envvars.UpdateGoldenFiles = true
	t.Cleanup(func() { envvars.UpdateGoldenFiles = update })

	dir := t.TempDir()
	comment := header + "#known/k: #264 a reason naming the mechanism.\n\nFree prose stays too.\n"
	c := writeCase(t, dir, comment,
		"in.cue", "lin: _\n",
		"out/s/bind", "verdict: E0141\nE0141 an older headline @ no position\n",
		"out/gone/bind", goldenE0141,
		"out/n/bind", goldenE0141)
	co := Corpus{Dir: dir, Suites: []Suite{fails("s", diag.E0141), fails("k", diag.E0142), notApplicable(fails("n"))}}
	t.Run("run", co.Run)

	updated, err := ReadCase(c.Path)
	if err != nil {
		t.Fatal(err)
	}
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
	if got, _ := updated.File("out/s/bind"); got != goldenE0141 {
		t.Errorf("out/s/bind was not rewritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "suites.json")); err != nil {
		t.Errorf("suites.json was not written: %v", err)
	}
}
