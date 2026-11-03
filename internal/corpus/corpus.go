package corpus

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/txtar"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/diagtest"
	"github.com/sdboyer/thema/internal/envvars"
)

// Suite is one way of building the value passed to BindLineage from a case's
// files.
type Suite struct {
	// Name is the suite's name in #known/<name> tags and out/<name>/ sections.
	Name string
	// Description says, in one to three sentences, how the suite builds the
	// value and which real caller builds it that way.
	Description string
	// Applies reports whether the suite can build the case, and if not, why.
	// A nil Applies applies to every case.
	Applies func(c *Case) (ok bool, why string)
	// Bind builds the value and returns BindLineage's error: nil when the
	// lineage binds. It calls t.Fatal only when the case cannot be built at
	// all, such as when its files do not load.
	Bind func(t testing.TB, c *Case) error
}

func (s Suite) applies(c *Case) (bool, string) {
	if s.Applies == nil {
		return true, ""
	}
	return s.Applies(c)
}

// Corpus is a directory of cases and the suites that run them.
type Corpus struct {
	// Dir holds the cases (*.txtar), suites.json and README.md.
	Dir    string
	Suites []Suite
	// Require names directives every case of this corpus must have, beyond
	// description, expect and spec.
	Require []string
}

// The directives every case has.
const (
	dirDescription = "description"
	dirExpect      = "expect"
	dirSpec        = "spec"
	knownPrefix    = "known/"
)

// Run checks the suite registry against suites.json, then runs every case
// through every suite, as the subtest <case>/<suite>. With THEMA_UPDATE_GOLDEN
// set it rewrites suites.json and the cases' out/ sections, and nothing else.
func (co Corpus) Run(t *testing.T) {
	t.Helper()
	for _, msg := range checkRegistry(co.Suites) {
		t.Error(msg)
	}
	diagtest.CompareFile(t, filepath.Join(co.Dir, "suites.json"), RegistryJSON(co.Suites))

	paths, err := filepath.Glob(filepath.Join(co.Dir, "*.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no cases in %s", co.Dir)
	}
	slices.Sort(paths)
	for _, p := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(p), ".txtar"), func(t *testing.T) {
			c, err := ReadCase(p)
			if err != nil {
				t.Fatal(err)
			}
			if !co.checkCase(t, c) {
				return
			}
			sections := map[string]string{}
			for _, s := range co.Suites {
				t.Run(s.Name, func(t *testing.T) {
					if sec, ok := co.cell(t, c, s); ok {
						sections[s.Name] = sec
					}
				})
			}
			if envvars.UpdateGoldenFiles {
				co.update(t, c, sections)
			}
		})
	}
}

// RegistryJSON renders suites.json: each suite's name and description, in
// registry order.
func RegistryJSON(suites []Suite) string {
	type entry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	reg := struct {
		Suites []entry `json:"suites"`
	}{Suites: []entry{}}
	for _, s := range suites {
		reg.Suites = append(reg.Suites, entry{s.Name, s.Description})
	}
	b, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b) + "\n"
}

var suiteName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func checkRegistry(suites []Suite) []string {
	var msgs []string
	seen := map[string]bool{}
	for i, s := range suites {
		switch {
		case !suiteName.MatchString(s.Name):
			msgs = append(msgs, fmt.Sprintf("suite %d: name %q is not kebab-case", i, s.Name))
		case seen[s.Name]:
			msgs = append(msgs, fmt.Sprintf("suite %q is registered twice", s.Name))
		}
		seen[s.Name] = true
		if strings.TrimSpace(s.Description) == "" {
			msgs = append(msgs, fmt.Sprintf("suite %q has no description", s.Name))
		}
		if s.Bind == nil {
			msgs = append(msgs, fmt.Sprintf("suite %q has no Bind", s.Name))
		}
	}
	return msgs
}

// Case is one archive of a corpus.
type Case struct {
	// Name is the archive's file name without .txtar.
	Name string
	// Path is the archive's path.
	Path        string
	Description string
	// Expect is the verdict the spec gives.
	Expect Verdict
	// Spec cites where the spec decides Expect.
	Spec string
	// Known holds the #known/<suite> tags, by suite.
	Known map[string]Known

	ar         *diagtest.Archive
	directives map[string]string
	// problems are what is wrong with the case's own text, found on reading.
	problems []string
}

// Known is a #known/<suite> tag: the suite disagrees with #expect today, for
// the reason given, and the issue tracks it.
type Known struct {
	Issue  int
	Reason string
}

var knownTag = regexp.MustCompile(`^#(\d+)\s+(\S.*)$`)

// ReadCase reads the case at path. What is wrong with the case's directives
// is reported when a corpus runs it, not here.
func ReadCase(path string) (*Case, error) {
	ar, err := txtar.ParseFile(path)
	if err != nil {
		return nil, err
	}
	a := &diagtest.Archive{Path: path, Ar: ar}
	dirs, _ := a.Directives()
	c := &Case{
		Name:        a.Name(),
		Path:        path,
		Description: dirs[dirDescription],
		Spec:        dirs[dirSpec],
		Known:       map[string]Known{},
		ar:          a,
		directives:  dirs,
	}
	if raw, ok := dirs[dirExpect]; ok {
		v, err := ParseVerdict(raw)
		if err != nil {
			c.problems = append(c.problems, fmt.Sprintf("#expect: %v", err))
		}
		c.Expect = v
	}
	for k, v := range dirs {
		suite, ok := strings.CutPrefix(k, knownPrefix)
		if !ok {
			continue
		}
		m := knownTag.FindStringSubmatch(v)
		if m == nil {
			c.problems = append(c.problems, fmt.Sprintf("#%s: %q must start with the issue that tracks it, as \"#<number> <reason>\"", k, v))
			continue
		}
		n, _ := strconv.Atoi(m[1])
		c.Known[suite] = Known{Issue: n, Reason: m[2]}
	}
	return c, nil
}

// Directive returns the value of the case's #key directive, or "".
func (c *Case) Directive(key string) string { return c.directives[key] }

// File returns the contents of the case's file name, and whether it exists.
func (c *Case) File(name string) (string, bool) { return c.ar.File(name) }

// Inputs returns the case's files that are not goldens.
func (c *Case) Inputs() []txtar.File { return c.ar.Inputs() }

func sectionName(suite string) string { return "out/" + suite + "/bind" }

// checkCase reports what is wrong with c's directives and sections, given
// the registry, and whether its cells can run.
func (co Corpus) checkCase(t testing.TB, c *Case) bool {
	t.Helper()
	ok := true
	fail := func(format string, args ...any) {
		t.Errorf("%s: "+format, append([]any{c.Path}, args...)...)
		ok = false
	}
	for _, key := range append([]string{dirDescription, dirExpect, dirSpec}, co.Require...) {
		if strings.TrimSpace(c.directives[key]) == "" {
			fail("the required directive #%s is missing; see %s", key, co.readme())
		}
	}
	for _, p := range c.problems {
		fail("%s", p)
	}
	registered := map[string]bool{}
	for _, s := range co.Suites {
		registered[s.Name] = true
	}
	for _, suite := range slices.Sorted(maps.Keys(c.Known)) {
		if !registered[suite] {
			fail("#known/%s names a suite that is not registered; the suites are in %s", suite, filepath.Join(co.Dir, "suites.json"))
		}
	}
	for _, f := range c.ar.Ar.Files {
		rest, isOut := strings.CutPrefix(f.Name, "out/")
		if !isOut {
			continue
		}
		suite, file, _ := strings.Cut(rest, "/")
		switch {
		case !registered[suite]:
			if !envvars.UpdateGoldenFiles {
				fail("%s is an orphan: no suite %q is registered; run with %s=1 to remove it", f.Name, suite, envvars.VarUpdateGolden)
			}
		case file != "bind":
			fail("%s is not a section the corpus writes; a suite's only section is %s", f.Name, sectionName(suite))
		}
	}
	return ok
}

func (co Corpus) readme() string { return filepath.Join(co.Dir, "README.md") }

// cell runs c through s, checks the result, and returns the section it
// rendered, if s applies to c.
func (co Corpus) cell(t testing.TB, c *Case, s Suite) (string, bool) {
	t.Helper()
	known, isKnown := c.Known[s.Name]
	golden, hasGolden := c.File(sectionName(s.Name))
	if ok, why := s.applies(c); !ok {
		if isKnown {
			t.Errorf("%s: #known/%s is stale: the suite does not apply to this case (%s); remove the tag", c.Path, s.Name, why)
		}
		if hasGolden && !envvars.UpdateGoldenFiles {
			t.Errorf("%s: %s is an orphan: the suite does not apply to this case (%s); run with %s=1 to remove it",
				c.Path, sectionName(s.Name), why, envvars.VarUpdateGolden)
		}
		t.Skipf("does not apply: %s", why)
		return "", false
	}

	got := Render(bind(t, s, c))
	status := statusOf(c, s.Name, verdictOfSection(got))
	header := co.header(c, s, got)
	diff := ""
	if hasGolden {
		diff = cmp.Diff(golden, got)
	}
	switch status {
	case StatusRegressed:
		t.Errorf("%s\n%s%s", header, instructionRegressed, diffNote(diff))
	case StatusXPass:
		t.Errorf("%s\n%s%s", header, InstructionXPass(s.Name, c.Path, known.Issue), diffNote(diff))
	default:
		switch {
		case envvars.UpdateGoldenFiles:
		case !hasGolden:
			t.Errorf("%s\nThere is no golden section %s. Run with %s=1 to write it.", header, sectionName(s.Name), envvars.VarUpdateGolden)
		case diff != "":
			t.Errorf("%s\n%s%s", header, instructionRendering, diffNote(diff))
		}
		if status == StatusKnown {
			t.Logf("known: #%d %s", known.Issue, known.Reason)
		}
	}
	return got, true
}

// The instructions a failing cell ends with. They are the main instruction
// channel to whoever sees the failure, so each says what the failure means
// and what to do.
const (
	instructionRegressed = "This is a regression: the suite now disagrees with the verdict the spec gives. " +
		"Do not update goldens or add a #known tag to make it pass. " +
		"If you believe #expect is wrong, it comes from the spec cited above; raise it with the owner instead of changing it."
	instructionRendering = "The verdict is unchanged, but what bind reports changed (codes, headlines or positions). " +
		"Review the diff below; if it is intended, run with " + envvars.VarUpdateGolden + "=1."
)

// InstructionXPass is the instruction for a cell whose suite now agrees with
// #expect despite a #known tag.
func InstructionXPass(suite, file string, issue int) string {
	return fmt.Sprintf("This suite now gives the verdict the spec expects: your change fixed it. "+
		"Remove the '#known/%s' line from %s, and mention issue #%d in your commit.", suite, file, issue)
}

func diffNote(diff string) string {
	if diff == "" {
		return ""
	}
	return "\n\n" + sectionDiffHeader + "\n" + diff
}

const sectionDiffHeader = "What bind reports, against the golden section (-golden +got):"

func (co Corpus) header(c *Case, s Suite, got string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "case %s: %s\n", c.Name, c.Description)
	fmt.Fprintf(&b, "suite %s: %s\n", s.Name, s.Description)
	fmt.Fprintf(&b, "#expect: %s (spec: %s)\n", c.Expect, c.Spec)
	v, rest, _ := strings.Cut(got, "\n")
	fmt.Fprintf(&b, "got: %s\n", strings.TrimPrefix(v, verdictPrefix))
	for _, l := range strings.Split(strings.TrimSuffix(rest, "\n"), "\n") {
		if l != "" {
			fmt.Fprintf(&b, "\t%s\n", l)
		}
	}
	if k, ok := c.Known[s.Name]; ok {
		fmt.Fprintf(&b, "#known/%s: #%d %s\n", s.Name, k.Issue, k.Reason)
	}
	fmt.Fprintf(&b, "How to read this corpus: %s\n", co.readme())
	return b.String()
}

// update rewrites c's out/ sections to the ones its cells rendered, keeping
// its comment and input files as they are.
func (co Corpus) update(t *testing.T, c *Case, sections map[string]string) {
	t.Helper()
	for _, s := range co.Suites {
		if ok, _ := s.applies(c); ok {
			if _, done := sections[s.Name]; !done {
				t.Errorf("%s: not updated, because suite %s rendered nothing for it", c.Path, s.Name)
				return
			}
		}
	}
	got := map[string]string{}
	for name, sec := range sections {
		got[sectionName(name)] = sec
	}
	c.ar.Compare(t, got)
}

// bind runs s.Bind, turning a panic into the diagnostic thema reports for
// one (E0901): the correct behavior is never a panic, and a panicking cell
// still gets a verdict.
func bind(t testing.TB, s Suite, c *Case) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = diag.Internal(r)
		}
	}()
	return s.Bind(t, c)
}

// Verdict is "ok" when the lineage binds, or else the sorted, space-separated
// set of diagnostic codes bind reports.
type Verdict string

// VerdictOK is the verdict of a lineage that binds.
const VerdictOK Verdict = "ok"

var codePattern = regexp.MustCompile(`^E\d{4}$`)

// ParseVerdict reads an #expect value: "ok", or a set of codes in any order.
func ParseVerdict(s string) (Verdict, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty; write ok or the codes bind reports")
	}
	if len(fields) == 1 && fields[0] == string(VerdictOK) {
		return VerdictOK, nil
	}
	set := map[string]bool{}
	for _, f := range fields {
		if !codePattern.MatchString(f) {
			return "", fmt.Errorf("%q is not a diagnostic code; write ok alone, or only codes such as E0141", f)
		}
		if _, ok := diag.Lookup(diag.Code(f)); !ok {
			return "", fmt.Errorf("%s is not declared in diag.Codes", f)
		}
		set[f] = true
	}
	return Verdict(strings.Join(slices.Sorted(maps.Keys(set)), " ")), nil
}

const verdictPrefix = "verdict: "

// Render renders what bind reported as a golden section: the verdict line,
// then one line per diagnostic, sorted.
func Render(err error) string {
	if err == nil {
		return verdictPrefix + string(VerdictOK) + "\n"
	}
	ds := diag.Diagnostics(err)
	if len(ds) == 0 {
		// BindLineage reports only diagnostics; anything else is converted as
		// any error is (E0902, E0903), so that nothing it says is dropped.
		ds = diag.From(err, diag.Context{Op: diag.OpBind})
	}
	codes := map[string]bool{}
	lines := make([]string, 0, len(ds))
	for _, d := range ds {
		codes[string(d.Code)] = true
		pos := "no position"
		if d.Primary != nil {
			pos = d.Primary.String()
		}
		lines = append(lines, fmt.Sprintf("%s %s @ %s", d.Code, d.Headline, pos))
	}
	slices.Sort(lines)
	return verdictPrefix + strings.Join(slices.Sorted(maps.Keys(codes)), " ") + "\n" + strings.Join(lines, "\n") + "\n"
}

func verdictOfSection(section string) Verdict {
	first, _, _ := strings.Cut(section, "\n")
	return Verdict(strings.TrimPrefix(first, verdictPrefix))
}

// Status is the state of one cell: one case run through one suite.
type Status int

const (
	// StatusNotApplicable means the case has no section for the suite.
	StatusNotApplicable Status = iota
	// StatusPass means the suite's verdict is #expect, and no tag says
	// otherwise.
	StatusPass
	// StatusKnown means the suite's verdict is not #expect, and a #known tag
	// says so.
	StatusKnown
	// StatusRegressed means the suite's verdict is not #expect, and no tag
	// says so.
	StatusRegressed
	// StatusXPass means the suite's verdict is #expect, but a #known tag says
	// it is not.
	StatusXPass
)

func (s Status) String() string {
	switch s {
	case StatusNotApplicable:
		return "not-applicable"
	case StatusPass:
		return "pass"
	case StatusKnown:
		return "known"
	case StatusRegressed:
		return "regressed"
	case StatusXPass:
		return "xpass"
	}
	return fmt.Sprintf("Status(%d)", int(s))
}

// CellStatus is the status of c's cell for suite, read from the archive
// alone: its golden section, #expect and #known tags.
func CellStatus(c *Case, suite string) Status {
	sec, ok := c.File(sectionName(suite))
	if !ok {
		return StatusNotApplicable
	}
	return statusOf(c, suite, verdictOfSection(sec))
}

func statusOf(c *Case, suite string, got Verdict) Status {
	_, known := c.Known[suite]
	switch {
	case got == c.Expect && !known:
		return StatusPass
	case got == c.Expect:
		return StatusXPass
	case known:
		return StatusKnown
	}
	return StatusRegressed
}
