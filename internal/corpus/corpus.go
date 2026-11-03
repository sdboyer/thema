package corpus

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
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
	Dir string
	// Root is the repository's root, relative to the test's directory. #spec
	// citations are resolved against it, and failures print paths relative to
	// it. When it is empty, citations are checked only for their form.
	Root   string
	Suites []Suite
	// Require names directives every case of this corpus must have, beyond
	// description, expect and spec.
	Require []string
	// Check, when set, checks what the corpus needs of a case beyond its
	// directives, before its cells run. It calls t.Fatal when the case cannot
	// run as written.
	Check func(t testing.TB, c *Case)
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
	cases := make([]*Case, 0, len(paths))
	for _, p := range paths {
		c, err := ReadCase(p)
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, c)
	}
	for _, msg := range co.unusedSuites(cases) {
		t.Error(msg)
	}

	r := co.start(cases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if !r.checkCase(t, c) {
				return
			}
			if co.Check != nil {
				co.Check(t, c)
			}
			sections := map[string]string{}
			for _, s := range co.Suites {
				t.Run(s.Name, func(t *testing.T) {
					if sec, ok := r.cell(t, c, s); ok {
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

// unusedSuites reports each registered suite that applies to none of cases:
// it would run nothing, and pass.
func (co Corpus) unusedSuites(cases []*Case) []string {
	var msgs []string
	for _, s := range co.Suites {
		if !slices.ContainsFunc(cases, func(c *Case) bool { ok, _ := s.applies(c); return ok }) {
			msgs = append(msgs, fmt.Sprintf("suite %s applies to no case in %s, so it checks nothing; add a case it applies to, or remove the suite", s.Name, co.display(co.Dir)))
		}
	}
	return msgs
}

// RegistryJSON renders suites.json: each suite's name and description, in
// registry order, and whether it applies to every case (its Applies is nil).
func RegistryJSON(suites []Suite) string {
	type entry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		EveryCase   bool   `json:"everyCase,omitempty"`
	}
	reg := struct {
		Suites []entry `json:"suites"`
	}{Suites: []entry{}}
	for _, s := range suites {
		reg.Suites = append(reg.Suites, entry{s.Name, s.Description, s.Applies == nil})
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
	data, err := os.ReadFile(path) //nolint:gosec // a case of the corpus the caller named
	if err != nil {
		return nil, err
	}
	return ParseCase(path, data), nil
}

// ParseCase reads a case from its archive's contents; path names the
// archive. [Case.Problems] says what is wrong with it.
func ParseCase(path string, data []byte) *Case {
	a := &diagtest.Archive{Path: path, Ar: txtar.Parse(data)}
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
	// Directives keeps the last of a repeated key, so a repeated directive
	// would override the first without a word.
	seen := map[string]bool{}
	for _, line := range strings.Split(string(a.Ar.Comment), "\n") {
		k, _, ok := strings.Cut(line, ":")
		if !ok || !strings.HasPrefix(k, "#") || strings.ContainsAny(k, " \t") {
			continue
		}
		if seen[k] {
			c.problems = append(c.problems, fmt.Sprintf("%s is given more than once; give it once", k))
		}
		seen[k] = true
	}
	if raw := dirs[dirExpect]; raw != "" {
		v, err := ParseVerdict(raw)
		if err != nil {
			c.problems = append(c.problems, fmt.Sprintf("#expect: %v", err))
		}
		c.Expect = v
	}
	for _, k := range slices.Sorted(maps.Keys(dirs)) {
		suite, ok := strings.CutPrefix(k, knownPrefix)
		if !ok {
			continue
		}
		v := dirs[k]
		m := knownTag.FindStringSubmatch(v)
		if m == nil {
			c.problems = append(c.problems, fmt.Sprintf("#%s: %q must start with the issue that tracks it, as \"#<number> <reason>\"", k, v))
			continue
		}
		n, _ := strconv.Atoi(m[1])
		c.Known[suite] = Known{Issue: n, Reason: m[2]}
	}
	return c
}

// Directive returns the value of the case's #key directive, or "".
func (c *Case) Directive(key string) string { return c.directives[key] }

// File returns the contents of the case's file name, and whether it exists.
func (c *Case) File(name string) (string, bool) { return c.ar.File(name) }

// Inputs returns the case's files that are not goldens.
func (c *Case) Inputs() []txtar.File { return c.ar.Inputs() }

// SectionFile is the name of suite's golden section in a case's archive.
func SectionFile(suite string) string { return "out/" + suite + "/bind" }

var specCitation = regexp.MustCompile(`^([^\s:,]+):(\d+)(?:-(\d+))?$`)

// citation is one #spec citation: a file, and the lines it cites.
type citation struct {
	file       string
	start, end int
}

// citations reads c's #spec citations, and says which are not of the form
// <path>:<line>[-<line>].
func (c *Case) citations() ([]citation, []string) {
	if c.Spec == "" {
		return nil, nil
	}
	var cites []citation
	var problems []string
	for _, raw := range strings.Split(c.Spec, ",") {
		raw = strings.TrimSpace(raw)
		m := specCitation.FindStringSubmatch(raw)
		if m == nil {
			problems = append(problems, fmt.Sprintf("#spec: %q is not <path>:<line>[-<line>]", raw))
			continue
		}
		start, _ := strconv.Atoi(m[2])
		end := start
		if m[3] != "" {
			end, _ = strconv.Atoi(m[3])
		}
		if start < 1 || end < start {
			problems = append(problems, fmt.Sprintf("#spec: %q is not a range of lines from the first to the last", raw))
			continue
		}
		cites = append(cites, citation{m[1], start, end})
	}
	return cites, problems
}

// resolveCitations reports each of c's #spec citations that names no file
// under root, or lines beyond the end of the file.
func (c *Case) resolveCitations(root string) []string {
	cites, _ := c.citations()
	var problems []string
	for _, cite := range cites {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cite.file))) //nolint:gosec // a file the case cites, under the repository's root
		if err != nil {
			problems = append(problems, fmt.Sprintf("#spec: %s is not a file of the repository", cite.file))
			continue
		}
		if n := strings.Count(strings.TrimSuffix(string(data), "\n"), "\n") + 1; cite.end > n {
			problems = append(problems, fmt.Sprintf("#spec: %s has %d lines, so it has no line %d", cite.file, n, cite.end))
		}
	}
	return problems
}

// Problems returns what is wrong with c as its archive stands, given the
// suites the corpus registers and the directives it requires beyond
// description, expect and spec. Directive problems are in the case's
// comment, which only its author can fix. Section problems are in its out/
// sections, which running with THEMA_UPDATE_GOLDEN=1 rewrites.
func (c *Case) Problems(registered, require []string) (directives, sections []string) {
	for _, key := range append([]string{dirDescription, dirExpect, dirSpec}, require...) {
		if strings.TrimSpace(c.directives[key]) == "" {
			directives = append(directives, fmt.Sprintf("the required directive #%s is missing", key))
		}
	}
	if !suiteName.MatchString(c.Name) {
		directives = append(directives, fmt.Sprintf("the case's name %q is not kebab-case; rename the file", c.Name))
	}
	_, citeProblems := c.citations()
	directives = append(directives, citeProblems...)
	directives = append(directives, c.problems...)
	for _, suite := range slices.Sorted(maps.Keys(c.Known)) {
		if !slices.Contains(registered, suite) {
			directives = append(directives, fmt.Sprintf("#known/%s names a suite that is not registered", suite))
		}
	}
	files := map[string]bool{}
	for _, f := range c.ar.Ar.Files {
		rest, isOut := strings.CutPrefix(f.Name, "out/")
		repeated := files[f.Name]
		files[f.Name] = true
		if !isOut {
			if repeated {
				directives = append(directives, fmt.Sprintf("the file %s appears more than once; only the first is read", f.Name))
			}
			continue
		}
		suite, _, _ := strings.Cut(rest, "/")
		switch {
		case repeated:
			sections = append(sections, fmt.Sprintf("%s appears more than once; only the first is compared", f.Name))
		case !slices.Contains(registered, suite):
			sections = append(sections, fmt.Sprintf("%s is an orphan: no suite %q is registered", f.Name, suite))
		case f.Name != SectionFile(suite):
			sections = append(sections, fmt.Sprintf("%s is not a section the corpus writes; a suite's only section is %s", f.Name, SectionFile(suite)))
		default:
			raw, v, err := sectionVerdict(string(f.Data))
			switch {
			case err != nil:
				sections = append(sections, fmt.Sprintf("%s: %v", f.Name, err))
			case raw != string(v):
				sections = append(sections, fmt.Sprintf("%s: verdict %q is not sorted and de-duplicated (%q)", f.Name, raw, v))
			}
		}
	}
	return directives, sections
}

// run is one run of a corpus: what its cells share.
type run struct {
	Corpus
	// tracked lists, by issue, the cells (<case>/<suite>) whose #known tags
	// name it.
	tracked map[int][]string
	// described holds the cases and suites a failure has described in full;
	// later failures only name them.
	described map[string]bool
}

func (co Corpus) start(cases []*Case) *run {
	r := &run{Corpus: co, tracked: map[int][]string{}, described: map[string]bool{}}
	for _, c := range cases {
		for _, suite := range slices.Sorted(maps.Keys(c.Known)) {
			issue := c.Known[suite].Issue
			r.tracked[issue] = append(r.tracked[issue], c.Name+"/"+suite)
		}
	}
	return r
}

// checkCase reports what is wrong with c, given the registry, and whether
// its cells can run. A problem in its out/ sections does not stop them, and
// is not reported when the run rewrites the sections.
func (r *run) checkCase(t testing.TB, c *Case) bool {
	t.Helper()
	registered := make([]string, 0, len(r.Suites))
	for _, s := range r.Suites {
		registered = append(registered, s.Name)
	}
	directives, sections := c.Problems(registered, r.Require)
	if r.Root != "" {
		directives = append(directives, c.resolveCitations(r.Root)...)
	}
	for _, p := range directives {
		t.Errorf("%s: %s; the format is in %s and the suites in %s",
			r.display(c.Path), p, r.display(r.readme()), r.display(filepath.Join(r.Dir, "suites.json")))
	}
	if !envvars.UpdateGoldenFiles {
		for _, p := range sections {
			t.Errorf("%s: %s; run with %s=1 to rewrite the out/ sections", r.display(c.Path), p, envvars.VarUpdateGolden)
		}
	}
	return len(directives) == 0
}

func (co Corpus) readme() string { return filepath.Join(co.Dir, "README.md") }

// display is path as a failure prints it: relative to the repository's root
// when the corpus knows the root, so that it reads the same wherever go test
// ran.
func (co Corpus) display(path string) string {
	if co.Root == "" {
		return path
	}
	rel, err := filepath.Rel(co.Root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// cell runs c through s, checks the result, and returns the section the case
// should hold for s: the one it rendered, or the golden section when that
// must not be rewritten. It returns false when s does not run c.
func (r *run) cell(t testing.TB, c *Case, s Suite) (string, bool) {
	t.Helper()
	known, isKnown := c.Known[s.Name]
	golden, hasGolden := c.File(SectionFile(s.Name))
	file := r.display(c.Path)
	if ok, why := s.applies(c); !ok {
		switch {
		case hasGolden:
			tag := ""
			if isKnown {
				tag = fmt.Sprintf(" and the #known/%s tag", s.Name)
			}
			// The section is kept, so that a golden update can't make a
			// suite stop running a case without anyone noticing.
			t.Errorf("%s: suite %s no longer applies to this case (%s), but %s records it running. "+
				"If the suite should still run this case, fix the suite. If it should not, delete the section%s by hand; %s=1 keeps it.",
				file, s.Name, why, SectionFile(s.Name), tag, envvars.VarUpdateGolden)
			return golden, true
		case isKnown:
			t.Errorf("%s: #known/%s is stale: the suite does not apply to this case (%s); remove the tag", file, s.Name, why)
		}
		t.Skipf("does not apply: %s", why)
		return "", false
	}

	got := Render(bind(t, s, c))
	_, v, _ := sectionVerdict(got)
	status := statusOf(c, s.Name, v)
	// was is the verdict the golden section records: "" when there is none,
	// or it can't be read.
	var was Verdict
	diff := ""
	if hasGolden {
		_, was, _ = sectionVerdict(golden)
		diff = cmp.Diff(golden, got)
	}
	fail := func(instruction string) {
		t.Errorf("%s\n%s%s", r.header(c, s, got), instruction, diffNote(diff))
	}
	switch {
	case status == StatusRegressed && was != "" && was == c.Expect:
		fail(instructionRegressed)
		// The section that records the suite agreeing is kept, so that a
		// golden update can't erase the regression.
		return golden, true
	case status == StatusRegressed:
		fail(r.instructionUntracked(s.Name))
	case status == StatusXPass:
		fail(r.instructionXPass(c, s.Name, known.Issue))
	case status == StatusKnown && hasGolden && was != v:
		// This fails with the update too: the section is rewritten, but the
		// tag is not, and nothing else would say that it may be stale.
		fail(InstructionKnownMoved(s.Name, was, v))
	case envvars.UpdateGoldenFiles:
	case !hasGolden:
		t.Errorf("%s\nThere is no golden section %s. Run with %s=1 to write it.", r.header(c, s, got), SectionFile(s.Name), envvars.VarUpdateGolden)
	case was != v:
		fail(InstructionNowPasses(was))
	case diff != "":
		fail(instructionRendering)
	}
	if status == StatusKnown {
		t.Logf("known: #%d %s", known.Issue, known.Reason)
	}
	return got, true
}

// The instructions a failing cell ends with. They are the main instruction
// channel to whoever sees the failure, so each says what the failure means
// and what to do.
const (
	instructionRegressed = "This is a regression: the golden section records this suite giving #expect, and it no longer does. " +
		"Do not add a #known tag to make it pass; " + envvars.VarUpdateGolden + "=1 keeps the section until the suite agrees again. " +
		"If you believe #expect is wrong, it comes from the spec cited above: open an issue citing those lines instead of changing it."
	instructionRendering = "The verdict is unchanged, but what bind reports changed (codes, headlines or positions). " +
		"Review the diff below; if it is intended, run with " + envvars.VarUpdateGolden + "=1."
)

// InstructionUntracked is the instruction for a cell that disagrees with
// #expect when no golden section records it agreeing and no #known tag
// tracks it: a new case, or a section rewritten to the wrong verdict.
func InstructionUntracked(suite, readme string) string {
	return fmt.Sprintf("This suite disagrees with #expect, no golden section records it agreeing, and no #known tag tracks the disagreement. "+
		"If the case is new, this is step 5 of \"Adding a case\" in %s: find the issue that covers the mechanism and add "+
		"'#known/%s: #<issue> <one sentence naming the mechanism>', or, if no issue covers it, open one citing the #spec lines. "+
		"If git shows the section agreeing with #expect before your change, your change broke it: fix the code instead of tagging the cell.",
		readme, suite)
}

func (r *run) instructionUntracked(suite string) string {
	return InstructionUntracked(suite, r.display(r.readme()))
}

// InstructionXPass is the instruction for a cell whose suite now agrees with
// #expect despite a #known tag. others are the other cells whose tags name
// the same issue.
func InstructionXPass(suite, file string, issue int, others []string) string {
	msg := fmt.Sprintf("This suite now gives the verdict the spec expects: your change fixed it. "+
		"Remove the '#known/%s' line from %s, run with %s=1 to rewrite the section, and mention issue #%d in your commit. ",
		suite, file, envvars.VarUpdateGolden, issue)
	if len(others) == 0 {
		return msg + fmt.Sprintf("No other cell of this corpus carries #%d, so it can be closed unless it tracks more than this corpus.", issue)
	}
	return msg + fmt.Sprintf("#%d also tracks %s, so leave it open until those pass too.", issue, strings.Join(others, ", "))
}

func (r *run) instructionXPass(c *Case, suite string, issue int) string {
	self := c.Name + "/" + suite
	var others []string
	for _, cell := range r.tracked[issue] {
		if cell != self {
			others = append(others, cell)
		}
	}
	return InstructionXPass(suite, r.display(c.Path), issue, others)
}

// InstructionKnownMoved is the instruction for a cell tagged #known whose
// verdict changed, from was to now, and still disagrees with #expect.
func InstructionKnownMoved(suite string, was, now Verdict) string {
	if was == "" {
		was = "an unreadable verdict"
	}
	return fmt.Sprintf("This suite's verdict changed from %s (the golden section) to %s. The #known tag accepts any verdict but #expect, so it may now name the wrong mechanism. "+
		"Check that #known/%s explains %s, or whether this is a different bug; update the tag, then run with %s=1, only once it does.",
		was, now, suite, now, envvars.VarUpdateGolden)
}

// InstructionNowPasses is the instruction for an untagged cell that gives
// #expect, where its golden section records was.
func InstructionNowPasses(was Verdict) string {
	if was == "" {
		was = "an unreadable verdict"
	}
	return fmt.Sprintf("This suite now gives #expect; the golden section records %s. "+
		"If your change fixed this cell and you removed its #known tag, run with %s=1 to rewrite the section.", was, envvars.VarUpdateGolden)
}

func diffNote(diff string) string {
	if diff == "" {
		return ""
	}
	return "\n\n" + sectionDiffHeader + "\n" + diff
}

const sectionDiffHeader = "What bind reports, against the golden section (-golden +got):"

// header describes the failing cell. A case or suite is described in full
// the first time a failure in the run names it, and only named after that.
func (r *run) header(c *Case, s Suite, got string) string {
	var b strings.Builder
	if r.describe("case/" + c.Name) {
		fmt.Fprintf(&b, "case %s: %s\n", c.Name, c.Description)
	} else {
		fmt.Fprintf(&b, "case %s (described above)\n", c.Name)
	}
	if r.describe("suite/" + s.Name) {
		fmt.Fprintf(&b, "suite %s: %s\n", s.Name, s.Description)
	} else {
		fmt.Fprintf(&b, "suite %s (described above, and in %s)\n", s.Name, r.display(filepath.Join(r.Dir, "suites.json")))
	}
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
	fmt.Fprintf(&b, "How to read this corpus: %s\n", r.display(r.readme()))
	return b.String()
}

// describe reports whether key has yet to be described in this run, and
// records that it now is.
func (r *run) describe(key string) bool {
	if r.described[key] {
		return false
	}
	r.described[key] = true
	return true
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
		got[SectionFile(name)] = sec
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

// ReadVerdict reads the verdict on a golden section's first line, as
// [ParseVerdict] reads it, or "" when it can't be read.
func ReadVerdict(section string) Verdict {
	_, v, _ := sectionVerdict(section)
	return v
}

// sectionVerdict reads the verdict on a golden section's first line: as
// written, and as [ParseVerdict] reads it.
func sectionVerdict(section string) (string, Verdict, error) {
	first, _, _ := strings.Cut(section, "\n")
	raw, ok := strings.CutPrefix(first, verdictPrefix)
	if !ok {
		return first, "", fmt.Errorf("the first line is not %q followed by the verdict", verdictPrefix)
	}
	v, err := ParseVerdict(raw)
	return raw, v, err
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
// alone: its golden section, #expect and #known tags. A verdict line is read
// as [ParseVerdict] reads it, so one whose codes are out of order still
// agrees with #expect; [Case.Problems] reports it.
func CellStatus(c *Case, suite string) Status {
	sec, ok := c.File(SectionFile(suite))
	if !ok {
		return StatusNotApplicable
	}
	_, v, _ := sectionVerdict(sec)
	return statusOf(c, suite, v)
}

// statusOf is the status of c's cell for suite, given the verdict it got. A
// verdict that could not be read, on either side, is "" and agrees with
// nothing.
func statusOf(c *Case, suite string, got Verdict) Status {
	_, known := c.Known[suite]
	agrees := got != "" && got == c.Expect
	switch {
	case agrees && !known:
		return StatusPass
	case agrees:
		return StatusXPass
	case known:
		return StatusKnown
	}
	return StatusRegressed
}
