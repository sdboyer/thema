package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/sdboyer/thema/internal/corpus"
	"github.com/sdboyer/thema/internal/diagtest"
)

// archive writes a case: its directives, its input files, and one bind golden
// per suite in goldens.
func archive(expect string, known, goldens map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#description: A synthetic case.\n#expect: %s\n#spec: docs/invariants.md:140-145\n#at: lin\n", expect)
	for _, s := range slices.Sorted(maps.Keys(known)) {
		fmt.Fprintf(&b, "#known/%s: %s\n", s, known[s])
	}
	b.WriteString("-- text.cue --\nschemas: {}\n-- in.cue --\nlin: _\n")
	for _, s := range slices.Sorted(maps.Keys(goldens)) {
		fmt.Fprintf(&b, "-- out/%s/bind --\n%s", s, goldens[s])
	}
	return b.String()
}

// golden is a bind golden whose diagnostics have the given codes, each
// positioned at line.
func golden(line int, codes ...string) string {
	if len(codes) == 0 {
		return "verdict: ok\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "verdict: %s\n", strings.Join(codes, " "))
	for _, c := range codes {
		fmt.Fprintf(&b, "%s a reference leaves the lineage @ in.cue:%d:5\n", c, line)
	}
	return b.String()
}

func suites(names ...string) string {
	var reg struct {
		Suites []Suite `json:"suites"`
	}
	for _, n := range names {
		reg.Suites = append(reg.Suites, Suite{Name: n, Description: "Builds the value the way " + n + " does."})
	}
	b, _ := json.MarshalIndent(reg, "", "  ")
	return string(b) + "\n"
}

func TestStatus(t *testing.T) {
	c := Parse(map[string][]byte{
		SuitesFile: []byte(suites("pass", "known", "regressed", "xpass", "na")),
		"c.txtar": []byte(archive("E0141 E0140",
			map[string]string{"known": tag264, "xpass": tag260},
			map[string]string{
				"pass":      golden(1, "E0140", "E0141"),
				"known":     golden(1),
				"regressed": golden(1, "E0141"),
				"xpass":     golden(1, "E0140", "E0141"),
			})),
	})
	if len(c.Problems) > 0 {
		t.Fatalf("problems: %v", c.Problems)
	}
	k := c.Case("c")
	for _, s := range c.Suites {
		want := Status(s.Name)
		if s.Name == "na" {
			want = NotApplicable
		}
		if got := CellStatus(k, s.Name); got != want {
			t.Errorf("suite %s: status %s, want %s", s.Name, got, want)
		}
	}
	if got, want := k.Known["known"], (corpus.Known{Issue: 264, Reason: "the lineage is at the top of a root"}); got != want {
		t.Errorf("known tag %+v, want %+v", got, want)
	}
}

func TestProblems(t *testing.T) {
	bad := `#description: A case that breaks the format.
#expect: ok E0141
#spec: invariants
#known/s1: 264 no hash
#known/s2: #12 tags a suite that does not run it
#known/nope: #1 an undeclared suite
-- out/s1/bind --
E0141 no verdict line
-- out/zz/bind --
verdict: ok
-- out/s3/other --
x
`
	c := Parse(map[string][]byte{
		SuitesFile:  []byte(suites("s1", "s2", "s3")),
		"bad.txtar": []byte(bad),
	})
	want := []string{
		`bad.txtar: #expect: "ok" is not a diagnostic code; write ok alone, or only codes such as E0141`,
		`bad.txtar: #known/nope names a suite that is not registered`,
		`bad.txtar: #known/nope tags a suite that does not run this case`,
		`bad.txtar: #known/s1: "264 no hash" must start with the issue that tracks it, as "#<number> <reason>"`,
		`bad.txtar: #known/s2 tags a suite that does not run this case`,
		`bad.txtar: #spec: "invariants" is not <path>:<line>[-<line>]`,
		`bad.txtar: out/s1/bind: the first line is not "verdict: " followed by the verdict`,
		`bad.txtar: out/s3/other is not a section the corpus writes; a suite's only section is out/s3/bind`,
		`bad.txtar: out/zz/bind is an orphan: no suite "zz" is registered`,
		`bad.txtar: the required directive #at is missing`,
	}
	if diff := cmp.Diff(want, c.Problems); diff != "" {
		t.Errorf("problems (-want +got):\n%s", diff)
	}
	// A suite that ran without a readable verdict is never taken to agree,
	// and a cell exists only where its bind section does.
	for suite, want := range map[string]Status{"s1": Regressed, "s3": NotApplicable} {
		if got := CellStatus(c.Case("bad"), suite); got != want {
			t.Errorf("%s is %s, want %s", suite, got, want)
		}
	}
}

// TestEveryCase checks that a case without a section for a suite that
// applies to every case breaks the format: the section was deleted, since
// the suite can't have stopped applying.
func TestEveryCase(t *testing.T) {
	reg := `{"suites": [{"name": "all", "description": "d", "everyCase": true}, {"name": "some", "description": "d"}]}`
	c := Parse(map[string][]byte{
		SuitesFile: []byte(reg),
		"e.txtar":  []byte(archive("ok", nil, map[string]string{"some": golden(1)})),
	})
	want := []string{"e.txtar: out/all/bind is missing, but suite all applies to every case"}
	if diff := cmp.Diff(want, c.Problems); diff != "" {
		t.Errorf("problems (-want +got):\n%s", diff)
	}
}

// TestUnreadableSuites checks that a suites.json that can't be read is the
// corpus's only problem, and that the report then compares no cells instead
// of reporting each one as removed.
func TestUnreadableSuites(t *testing.T) {
	broken := Parse(map[string][]byte{
		SuitesFile: []byte("{"),
		"u.txtar":  []byte(archive("ok", nil, map[string]string{"s1": golden(1)})),
	})
	if want := []string{"suites.json: unexpected end of JSON input"}; !broken.Unreadable || !slices.Equal(broken.Problems, want) {
		t.Fatalf("unreadable %v, problems %q; want %q", broken.Unreadable, broken.Problems, want)
	}
	ok := Parse(map[string][]byte{
		SuitesFile: []byte(suites("s1")),
		"u.txtar":  []byte(archive("ok", nil, map[string]string{"s1": golden(1)})),
	})
	r := Build(corpusDir, Range{Before: "a", After: "b", Label: "a..b"}, ok, broken)
	if len(r.Cells) != 0 || !r.Failing() || !slices.Equal(r.Unreadable, []string{"b"}) {
		t.Errorf("cells %d, failing %v, unreadable %q", len(r.Cells), r.Failing(), r.Unreadable)
	}
	var md bytes.Buffer
	if err := Markdown(&md, r); err != nil {
		t.Fatal(err)
	}
	want := "### Corpus `testdata/refs`: `a..b`\n\n" +
		"**The cells were not compared: suites.json could not be read in b.**\n\n" +
		"**The corpus in b breaks its format:**\n\n- suites.json: unexpected end of JSON input\n\n"
	if md.String() != want {
		t.Errorf("markdown:\n%s\nwant:\n%s", md.String(), want)
	}
}

// TestRefsCorpus reads testdata/refs as it is on disk: the report must find
// in it nothing that TestRefs, which runs it, accepts.
func TestRefsCorpus(t *testing.T) {
	c, err := Repo{Dir: "."}.Load(WorkingTree, "../../../testdata/refs")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Cases) == 0 || len(c.Suites) == 0 {
		t.Fatalf("read %d cases and %d suites", len(c.Cases), len(c.Suites))
	}
	for _, p := range c.Problems {
		t.Errorf("problem: %s", p)
	}
	for _, k := range c.Cases {
		for _, s := range c.Suites {
			if st := CellStatus(k, s.Name); st == Regressed || st == XPass {
				t.Errorf("%s/%s is %s", k.Name, s.Name, st)
			}
		}
	}
}

// gitRepo is a git repository in a temporary directory, isolated from the
// user's git configuration.
type gitRepo struct {
	t   *testing.T
	dir string
}

func newGitRepo(t *testing.T) *gitRepo {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	r := &gitRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	return r
}

func (r *gitRepo) git(args ...string) string {
	r.t.Helper()
	args = append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", args...) //nolint:gosec // git, in a repository this test made
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// write replaces the corpus with files.
func (r *gitRepo) write(files map[string]string) {
	r.t.Helper()
	full := filepath.Join(r.dir, corpusDir)
	if err := os.RemoveAll(full); err != nil {
		r.t.Fatal(err)
	}
	if err := os.MkdirAll(full, 0o750); err != nil {
		r.t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(full, name), []byte(data), 0o600); err != nil {
			r.t.Fatal(err)
		}
	}
}

func (r *gitRepo) commit(msg string) {
	r.t.Helper()
	r.git("add", ".")
	r.git("commit", "-q", "--allow-empty", "-m", msg)
}

const (
	corpusDir = "testdata/refs"
	tag264    = "#264 the lineage is at the top of a root"
	tag260    = "#260 the narrowing refers beside the use"

	newCasePass = "absent pass new case"
)

// Two revisions of a corpus, in which each cell of the after revision changes
// in the way its case's name says, or not at all.
var (
	beforeCorpus = map[string]string{
		SuitesFile: suites("s1", "s2", "s3", "old"),
		"fixed.txtar": archive("E0141", map[string]string{"s1": tag264},
			map[string]string{"s1": golden(3)}),
		"regress.txtar":  archive("ok", nil, map[string]string{"s1": golden(3)}),
		"accepted.txtar": archive("ok", nil, map[string]string{"s1": golden(3)}),
		"xpass.txtar": archive("ok", map[string]string{"s1": tag260},
			map[string]string{"s1": golden(3, "E0141")}),
		"render.txtar": archive("E0141", nil, map[string]string{"s1": golden(3, "E0141")}),
		"steady.txtar": archive("E0141", map[string]string{"s3": "#130 the let is bound to a literal"},
			map[string]string{"s1": golden(3, "E0141"), "s3": golden(3), "old": golden(3, "E0141")}),
		"removed.txtar": archive("ok", nil, map[string]string{"s1": golden(3)}),
		"applies.txtar": archive("ok", nil, map[string]string{"s1": golden(3)}),
		"expect.txtar":  archive("ok", nil, map[string]string{"s1": golden(3)}),
		"retag.txtar": archive("ok", map[string]string{"s1": tag260},
			map[string]string{"s1": golden(3, "E0141")}),
		"moved.txtar": archive("ok", map[string]string{"s1": tag264},
			map[string]string{"s1": golden(3, "E0141")}),
		"respec.txtar": archive("E0141", map[string]string{"s1": tag264},
			map[string]string{"s1": golden(3)}),
	}
	afterCorpus = map[string]string{
		SuitesFile:    suites("s1", "s2", "s3", "s4"),
		"fixed.txtar": archive("E0141", nil, map[string]string{"s1": golden(3, "E0141")}),
		"regress.txtar": archive("ok", nil,
			map[string]string{"s1": golden(3, "E0141")}),
		"accepted.txtar": archive("ok", map[string]string{"s1": tag264},
			map[string]string{"s1": golden(3, "E0141")}),
		"xpass.txtar": archive("ok", map[string]string{"s1": tag260},
			map[string]string{"s1": golden(3)}),
		"render.txtar": archive("E0141", nil, map[string]string{"s1": golden(4, "E0141")}),
		"steady.txtar": archive("E0141", map[string]string{"s3": "#130 the let is bound to a literal"},
			map[string]string{"s1": golden(3, "E0141"), "s3": golden(3), "s4": golden(3, "E0141")}),
		"added.txtar": archive("ok", map[string]string{"s2": tag264},
			map[string]string{"s1": golden(3), "s2": golden(3, "E0141")}),
		"wide.txtar": archive("ok", nil,
			map[string]string{"s1": golden(3), "s3": golden(3), "s4": golden(3)}),
		"applies.txtar": archive("ok", nil, map[string]string{"s2": golden(3)}),
		"expect.txtar":  archive("E0141", nil, map[string]string{"s1": golden(3, "E0141")}),
		"retag.txtar": archive("ok", map[string]string{"s1": tag264},
			map[string]string{"s1": golden(3, "E0141")}),
		"moved.txtar": archive("ok", map[string]string{"s1": tag264},
			map[string]string{"s1": golden(3, "E0142")}),
		"respec.txtar": archive("ok", nil, map[string]string{"s1": golden(3)}),
	}
)

// wantCells are the cells of the report from beforeCorpus to afterCorpus that
// a suite runs in either revision: before, after, and transitions. Every other
// cell is not-applicable or absent on both sides, and unchanged.
var wantCells = map[string]string{ //nolint:gosec // cell descriptions, not credentials
	"accepted/s1": "pass known regressed",
	"added/s1":    newCasePass,
	"added/s2":    "absent known new case",
	"applies/s1":  "pass not-applicable no longer applies",
	"applies/s2":  "not-applicable pass now applies",
	"expect/s1":   "pass pass #expect changed, rendering changed",
	"fixed/s1":    "known pass fixed",
	"moved/s1":    "known known known verdict changed, rendering changed",
	"respec/s1":   "known pass status changed, #expect changed",
	"regress/s1":  "pass regressed regressed",
	"removed/s1":  "pass absent removed case",
	"render/s1":   "pass pass rendering changed",
	"retag/s1":    "known known #known changed",
	"steady/old":  "pass absent removed suite",
	"steady/s1":   "pass pass",
	"steady/s3":   "known known",
	"steady/s4":   "absent pass new suite",
	"wide/s1":     newCasePass,
	"wide/s3":     newCasePass,
	"wide/s4":     "absent pass new case, new suite",
	"xpass/s1":    "known xpass xpass",
}

func joinTransitions(ts []Transition) string {
	s := make([]string, len(ts))
	for i, t := range ts {
		s[i] = string(t)
	}
	return strings.Join(s, ", ")
}

func describe(c Cell) string {
	return strings.TrimSpace(fmt.Sprintf("%s %s %s", c.Before, c.After, joinTransitions(c.Transitions)))
}

func buildReport(t *testing.T, repo Repo, rng string) *Report {
	t.Helper()
	rg, err := repo.ParseRange(rng)
	if err != nil {
		t.Fatal(err)
	}
	before, err := repo.Load(rg.Before, corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	after, err := repo.Load(rg.After, corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	return Build(corpusDir, rg, before, after)
}

func TestReport(t *testing.T) {
	g := newGitRepo(t)
	g.write(beforeCorpus)
	g.commit("before")
	g.git("tag", "before")
	g.write(afterCorpus)
	g.commit("after")
	g.git("tag", "after")

	r := buildReport(t, Repo{Dir: g.dir}, "before..after")
	if len(r.BeforeProblems)+len(r.AfterProblems) > 0 {
		t.Fatalf("problems: %v %v", r.BeforeProblems, r.AfterProblems)
	}

	got := map[string]string{}
	for _, c := range r.Cells {
		runs := func(s Status) bool { return s != Absent && s != NotApplicable }
		if runs(c.Before) || runs(c.After) {
			got[c.Case+"/"+c.Suite] = describe(c)
		} else if c.Changed() {
			t.Errorf("%s/%s runs in neither revision, but changed: %s", c.Case, c.Suite, describe(c))
		}
	}
	if diff := cmp.Diff(wantCells, got); diff != "" {
		t.Errorf("cells (-want +got):\n%s", diff)
	}
	if got := r.Cell(slices.IndexFunc(r.Cases, func(k CaseInfo) bool { return k.Name == "expect" }), 0); got.Suite != "s1" {
		t.Errorf("cell (expect, 0) is in suite %s", got.Suite)
	}
	for _, k := range r.Cases {
		if k.Name == "expect" && k.ExpectBefore != "ok" {
			t.Errorf("expect: ExpectBefore %q, want ok", k.ExpectBefore)
		}
	}
	if !r.Failing() {
		t.Error("the after revision has regressed and xpass cells, but the report isn't failing")
	}
	// Run from a subdirectory, the corpus directory is relative to it.
	sub := Repo{Dir: filepath.Join(g.dir, "testdata")}
	rg, err := sub.ParseRange("before..after")
	if err != nil {
		t.Fatal(err)
	}
	for _, rev := range []string{rg.Before, rg.After} {
		c, err := sub.Load(rev, "refs")
		if err != nil {
			t.Fatal(err)
		}
		if want := 10; len(c.Cases) < want {
			t.Errorf("from a subdirectory, %s has %d cases, want at least %d", rev, len(c.Cases), want)
		}
	}
	if clean := buildReport(t, Repo{Dir: g.dir}, "before..before"); clean.Failing() || len(clean.Changed()) > 0 {
		t.Errorf("a revision compared with itself: failing %v, %d changed cells", clean.Failing(), len(clean.Changed()))
	}

	var md bytes.Buffer
	if err := Markdown(&md, r); err != nil {
		t.Fatal(err)
	}
	// The range's revisions are tags, so the golden doesn't depend on hashes.
	diagtest.CompareFile(t, "testdata/report.md", md.String())

	t.Run("html", func(t *testing.T) {
		var b bytes.Buffer
		if err := HTML(&b, r); err != nil {
			t.Fatal(err)
		}
		page := b.String()
		if m := regexp.MustCompile(`(?i)https?:`).FindString(page); m != "" {
			t.Errorf("the page refers to the network (%q); it must be self-contained", m)
		}
		for _, tag := range []string{"<link", "<img", "<iframe", "src="} {
			if strings.Contains(page, tag) {
				t.Errorf("the page loads something (%s); it must be self-contained", tag)
			}
		}
		_, rest, ok := strings.Cut(page, `<script id="data" type="application/json">`)
		raw, _, ok2 := strings.Cut(rest, "</script>")
		if !ok || !ok2 {
			t.Fatal("the page has no data script")
		}
		var d htmlData
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatalf("the page's data: %v", err)
		}
		if !slices.Equal(d.Loud, Loud) {
			t.Errorf("the page marks %v as loud, want %v", d.Loud, Loud)
		}
		if len(d.Cases) != len(r.Cases) || len(d.Suites) != len(r.Suites) {
			t.Fatalf("the page has %d cases and %d suites, want %d and %d", len(d.Cases), len(d.Suites), len(r.Cases), len(r.Suites))
		}
		for i, k := range d.Cases {
			if len(k.Cells) != len(r.Suites) {
				t.Errorf("case %s has %d cells, want %d", k.Name, len(k.Cells), len(r.Suites))
				continue
			}
			for j, c := range k.Cells {
				want := r.Cell(i, j)
				if c.Before != want.Before || c.After != want.After || !slices.Equal(c.Transitions, want.Transitions) {
					t.Errorf("cell %s/%s on the page is %s %s %v, want %s", k.Name, d.Suites[j].Name, c.Before, c.After, c.Transitions, describe(want))
				}
				if want.GoldenChanged != (c.Diff != nil) {
					t.Errorf("cell %s/%s: golden changed %v, but the page's diff is %v", k.Name, d.Suites[j].Name, want.GoldenChanged, c.Diff)
				}
			}
		}
	})
}

func TestWorkingTree(t *testing.T) {
	g := newGitRepo(t)
	g.write(beforeCorpus)
	g.commit("before")

	repo := Repo{Dir: g.dir}
	rg, err := repo.ParseRange("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if rg.After != WorkingTree || rg.AfterLabel() != "working tree" {
		t.Fatalf("range HEAD: after is %q (%s), want the working tree", rg.After, rg.AfterLabel())
	}
	// An uncommitted fix, and an untracked case.
	fixed := beforeCorpus["fixed.txtar"]
	fixed = strings.Replace(fixed, "#known/s1: #264 the lineage is at the top of a root\n", "", 1)
	fixed = strings.Replace(fixed, "-- out/s1/bind --\nverdict: ok\n", "-- out/s1/bind --\n"+golden(3, "E0141"), 1)
	files := maps.Clone(beforeCorpus)
	files["fixed.txtar"] = fixed
	files["untracked.txtar"] = archive("ok", nil, map[string]string{"s2": golden(3, "E0141")})
	g.write(files)

	r := buildReport(t, repo, "HEAD")
	got := map[string]string{}
	for _, c := range r.Changed() {
		got[c.Case+"/"+c.Suite] = describe(c)
	}
	want := map[string]string{
		"fixed/s1":     "known pass fixed",
		"untracked/s2": "absent regressed regressed, new case",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("changed cells (-want +got):\n%s", diff)
	}
	if !r.Failing() {
		t.Error("the working tree has a regressed cell, but the report isn't failing")
	}
}

func TestRangeErrors(t *testing.T) {
	g := newGitRepo(t)
	g.write(beforeCorpus)
	g.commit("before")
	repo := Repo{Dir: g.dir}
	for _, rng := range []string{"nope", "HEAD..nope", "..HEAD", "HEAD...", "", " "} {
		if _, err := repo.ParseRange(rng); err == nil {
			t.Errorf("range %q: no error", rng)
		}
	}
	// A revision without the corpus is an empty corpus, marked missing.
	before := g.git("rev-parse", "HEAD")
	g.git("checkout", "-q", "--orphan", "empty")
	g.git("rm", "-rq", "--cached", ".")
	g.git("commit", "-q", "--allow-empty", "-m", "empty")
	empty := g.git("rev-parse", "HEAD")
	c, err := repo.Load(empty, corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Cases)+len(c.Suites)+len(c.Problems) != 0 || !c.Missing {
		t.Errorf("a revision without the corpus: %+v", c)
	}
	// Only the before revision may lack it: that is the revision before the
	// corpus was added.
	r, err := repo.Compare(corpusDir, empty+".."+before)
	if err != nil {
		t.Fatal(err)
	}
	var md bytes.Buffer
	if err := Markdown(&md, r); err != nil {
		t.Fatal(err)
	}
	if want := "The corpus directory does not exist in " + empty + ", so every cell is new."; !strings.Contains(md.String(), want) {
		t.Errorf("the markdown does not say %q:\n%s", want, md.String())
	}
	for _, c := range r.Changed() {
		if !slices.Equal(c.Transitions, []Transition{TNewCase}) {
			t.Errorf("before the corpus exists, %s × %s should only be a new case; got %v", c.Case, c.Suite, c.Transitions)
		}
	}
	if _, err := repo.Compare(corpusDir, before+".."+empty); err == nil {
		t.Error("the after revision has no corpus, but there is no error")
	}
	if _, err := repo.Compare("testdata/ref", before+".."+before); err == nil || !strings.Contains(err.Error(), "the corpus directory testdata/ref does not exist in "+before) {
		t.Errorf("a corpus directory that does not exist: %v", err)
	}
}

func TestLineDiff(t *testing.T) {
	got := LineDiff("verdict: E0141\nE0141 a @ in.cue:3:5\n", "verdict: E0141\nE0141 a @ in.cue:4:5\nE0142 b @ in.cue:5:1\n")
	want := []DiffLine{
		{' ', "verdict: E0141"},
		{'-', "E0141 a @ in.cue:3:5"},
		{'+', "E0141 a @ in.cue:4:5"},
		{'+', "E0142 b @ in.cue:5:1"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
