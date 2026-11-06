package corpus

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
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

// Suite is one way of running the corpus's operation on a case: of building,
// from a case's files, the value passed to BindLineage, or of taking an input
// to a Go value.
type Suite struct {
	// Name is the suite's name in #known/<name> tags and out/<name>/ sections.
	Name string
	// Description says, in one to three sentences, how the suite runs the
	// case and which real caller runs it that way.
	Description string
	// Applies reports whether the suite can run the case, and if not, why.
	// A nil Applies applies to every case.
	Applies func(c *Case) (ok bool, why string)
	// Run runs the case and returns the operation's error: nil when it
	// succeeds. When it succeeds, out is what it produced, as the lines its
	// section records beneath the verdict, or "" when there is nothing to
	// record, as for bind. It calls t.Fatal only when the case cannot be run
	// at all, such as when its files do not load.
	Run func(t testing.TB, c *Case) (out string, err error)
}

func (s Suite) applies(c *Case) (bool, string) {
	if s.Applies == nil {
		return true, ""
	}
	return s.Applies(c)
}

// Corpus is a directory of archives and the suites that run their cases.
type Corpus struct {
	// Dir holds the archives (*.txtar), suites.json and README.md.
	Dir string
	// Root is the repository's root, relative to the test's directory. #spec
	// citations are resolved against it, and failures print paths relative to
	// it. When it is empty, citations are checked only for their form.
	Root string
	// Layout is how the corpus's archives are read, which suites.json records
	// for tools that read the corpus without running it.
	Layout
	// Op is the operation the suites run, under which an error that is not a
	// diagnostic is converted (diag.From). It is diag.OpBind when empty.
	Op     diag.Op
	Suites []Suite
	// Check, when set, checks what the corpus needs of a case beyond its
	// directives, before its cells run. It calls t.Fatal when the case cannot
	// run as written.
	Check func(t testing.TB, c *Case)
}

// Layout says how a corpus's archives are read.
type Layout struct {
	// Inputs, when set, is a pattern (path.Match) naming an archive's input
	// files, such as data/*.json. Each file it matches is a case of its own,
	// named <archive>/<input>, where input is the file's name without its
	// directory or extension. When Inputs is empty, an archive is one case.
	Inputs string `json:"inputs,omitempty"`
	// Require names directives every case of this corpus must have, beyond
	// description, expect and spec.
	Require []string `json:"require,omitempty"`
}

func (co Corpus) op() diag.Op {
	if co.Op == "" {
		return diag.OpBind
	}
	return co.Op
}

// The directives every case has.
const (
	dirDescription = "description"
	dirExpect      = "expect"
	dirSpec        = "spec"
	knownPrefix    = "known/"
)

// Run checks the suite registry against suites.json, then runs every case
// through every suite, as the subtest <case>/<suite>: <archive>/<suite> when
// an archive is one case, and <archive>/<input>/<suite> when it has inputs.
// With THEMA_UPDATE_GOLDEN set it rewrites suites.json and the archives' out/
// sections, and nothing else.
func (co Corpus) Run(t *testing.T) {
	t.Helper()
	for _, msg := range checkRegistry(co.Suites) {
		t.Error(msg)
	}
	diagtest.CompareFile(t, filepath.Join(co.Dir, "suites.json"), RegistryJSON(co.Layout, co.Suites))

	paths, err := filepath.Glob(filepath.Join(co.Dir, "*.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no cases in %s", co.Dir)
	}
	slices.Sort(paths)
	archives := make([]*Archive, 0, len(paths))
	var cases []*Case
	for _, p := range paths {
		a, err := ReadArchive(p, co.Layout)
		if err != nil {
			t.Fatal(err)
		}
		archives = append(archives, a)
		cases = append(cases, a.Cases...)
	}
	for _, msg := range co.unusedSuites(cases) {
		t.Error(msg)
	}

	r := co.start(cases)
	for _, a := range archives {
		t.Run(a.Name, func(t *testing.T) {
			if !r.checkArchive(t, a) {
				return
			}
			sections := map[string]string{}
			runCase := func(t *testing.T, c *Case) {
				if co.Check != nil {
					co.Check(t, c)
				}
				for _, s := range co.Suites {
					t.Run(s.Name, func(t *testing.T) {
						if sec, ok := r.cell(t, c, s); ok {
							sections[c.SectionFile(s.Name)] = sec
						}
					})
				}
			}
			if a.layout.Inputs == "" {
				runCase(t, a.Cases[0])
			} else {
				for _, c := range a.Cases {
					t.Run(c.Input, func(t *testing.T) { runCase(t, c) })
				}
			}
			if envvars.UpdateGoldenFiles {
				co.update(t, a, sections)
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

// RegistryJSON renders suites.json: the corpus's layout, then each suite's
// name and description, in registry order, and whether it applies to every
// case (its Applies is nil).
func RegistryJSON(l Layout, suites []Suite) string {
	type entry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		EveryCase   bool   `json:"everyCase,omitempty"`
	}
	reg := struct {
		Layout
		Suites []entry `json:"suites"`
	}{Layout: l, Suites: []entry{}}
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
		if s.Run == nil {
			msgs = append(msgs, fmt.Sprintf("suite %q has no Run", s.Name))
		}
	}
	return msgs
}

// Archive is one file of a corpus, and the cases it holds: itself alone, or
// one for each input when the corpus's layout names inputs.
type Archive struct {
	// Name is the file's name without .txtar.
	Name string
	// Path is the file's path.
	Path string
	// Cases are the archive's cases, in the order of their input files.
	Cases []*Case

	ar     *diagtest.Archive
	layout Layout
	// dirs are the directives, by key, as written: expect, expect/<input>,
	// known/<suite> and known/<suite>/<input>.
	dirs map[string]string
	// problems are what is wrong with the archive's own text, found on
	// reading.
	problems []string
}

// Case is one case of a corpus: an archive, or one input of an archive.
type Case struct {
	// Name is the archive's name, followed by /<input> for an input.
	Name string
	// Input is the input's name, or "" when the archive is one case.
	Input string
	// InputFile is the name, in the archive, of the input's file, or "".
	InputFile string
	// Path is the archive's path.
	Path        string
	Description string
	// Expect is the verdict the spec gives.
	Expect Verdict
	// Spec cites where the spec decides Expect.
	Spec string
	// Known holds the #known tags that apply to the case, by suite.
	Known map[string]Known

	archive *Archive
}

// Known is a #known/<suite> tag: the suite disagrees with #expect today, for
// the reason given, and the issue tracks it.
type Known struct {
	Issue  int
	Reason string
}

var knownTag = regexp.MustCompile(`^#(\d+)\s+(\S.*)$`)

// ReadArchive reads the archive at path, as the layout says. What is wrong
// with its directives is reported when a corpus runs it, not here.
func ReadArchive(path string, l Layout) (*Archive, error) {
	data, err := os.ReadFile(path) //nolint:gosec // an archive of the corpus the caller named
	if err != nil {
		return nil, err
	}
	return ParseArchive(path, data, l), nil
}

// ParseArchive reads an archive from its contents, as the layout says; file
// names the archive. [Archive.Problems] says what is wrong with it.
func ParseArchive(file string, data []byte, l Layout) *Archive {
	ar := &diagtest.Archive{Path: file, Ar: txtar.Parse(data)}
	dirs, _ := ar.Directives()
	a := &Archive{Name: ar.Name(), Path: file, ar: ar, layout: l, dirs: dirs}
	// Directives keeps the last of a repeated key, so a repeated directive
	// would override the first without a word.
	seen := map[string]bool{}
	for _, line := range strings.Split(string(ar.Ar.Comment), "\n") {
		k, _, ok := strings.Cut(line, ":")
		if !ok || !strings.HasPrefix(k, "#") || strings.ContainsAny(k, " \t") {
			continue
		}
		if seen[k] {
			a.problems = append(a.problems, fmt.Sprintf("%s is given more than once; give it once", k))
		}
		seen[k] = true
	}

	if l.Inputs == "" {
		a.Cases = []*Case{a.newCase("", "")}
	} else {
		files := map[string]string{}
		for _, f := range ar.Inputs() {
			if ok, _ := path.Match(l.Inputs, f.Name); !ok {
				continue
			}
			in := strings.TrimSuffix(path.Base(f.Name), path.Ext(f.Name))
			if other, dup := files[in]; dup {
				if other != f.Name {
					a.problems = append(a.problems, fmt.Sprintf("the inputs %s and %s have the same name, %s; rename one", other, f.Name, in))
				}
				continue
			}
			files[in] = f.Name
			a.Cases = append(a.Cases, a.newCase(in, f.Name))
		}
		if len(a.Cases) == 0 {
			a.problems = append(a.problems, fmt.Sprintf("the archive has no input: no file matches %s", l.Inputs))
		}
	}

	for _, k := range slices.Sorted(maps.Keys(dirs)) {
		key, in := splitDirective(k)
		if in != "" && a.input(in) == nil {
			if l.Inputs == "" {
				a.problems = append(a.problems, fmt.Sprintf("#%s names an input, but the cases of this corpus have none; write #%s", k, key))
			} else {
				a.problems = append(a.problems, fmt.Sprintf("#%s names %s, which is not an input of the archive", k, in))
			}
		}
	}
	return a
}

// splitDirective splits a directive's key into the key it gives and the input
// it gives it for, or "" when it gives it for every case of the archive:
// expect/<input> and known/<suite>/<input>.
func splitDirective(k string) (key, input string) {
	if rest, ok := strings.CutPrefix(k, knownPrefix); ok {
		suite, in, _ := strings.Cut(rest, "/")
		return knownPrefix + suite, in
	}
	key, input, _ = strings.Cut(k, "/")
	return key, input
}

// input returns the archive's case for input in, or nil.
func (a *Archive) input(in string) *Case {
	for _, c := range a.Cases {
		if c.Input == in {
			return c
		}
	}
	return nil
}

func (a *Archive) newCase(in, file string) *Case {
	c := &Case{Name: a.Name, Input: in, InputFile: file, Path: a.Path, Known: map[string]Known{}, archive: a}
	if in != "" {
		c.Name += "/" + in
	}
	c.Description = c.Directive(dirDescription)
	c.Spec = c.Directive(dirSpec)
	if raw := c.Directive(dirExpect); raw != "" {
		v, err := ParseVerdict(raw)
		if err != nil {
			a.problems = append(a.problems, fmt.Sprintf("#%s: %v", c.directiveKey(dirExpect), err))
		}
		c.Expect = v
	}
	suites := map[string]bool{}
	for k := range a.dirs {
		if key, _ := splitDirective(k); strings.HasPrefix(key, knownPrefix) {
			suites[strings.TrimPrefix(key, knownPrefix)] = true
		}
	}
	for _, suite := range slices.Sorted(maps.Keys(suites)) {
		k := c.directiveKey(knownPrefix + suite)
		v, ok := a.dirs[k]
		if !ok {
			continue
		}
		m := knownTag.FindStringSubmatch(v)
		if m == nil {
			msg := fmt.Sprintf("#%s: %q must start with the issue that tracks it, as \"#<number> <reason>\"", k, v)
			if !slices.Contains(a.problems, msg) {
				a.problems = append(a.problems, msg)
			}
			continue
		}
		n, _ := strconv.Atoi(m[1])
		c.Known[suite] = Known{Issue: n, Reason: m[2]}
	}
	return c
}

// directiveKey is the key of the directive that gives c key: key/<input>
// when the archive writes one for c's input, and key otherwise.
func (c *Case) directiveKey(key string) string {
	if c.Input != "" {
		if _, ok := c.archive.dirs[key+"/"+c.Input]; ok {
			return key + "/" + c.Input
		}
	}
	return key
}

// Directive returns the value of the #key directive for c, or "": the one
// written for c's input, as #key/<input>, or else the archive's #key.
func (c *Case) Directive(key string) string { return c.archive.dirs[c.directiveKey(key)] }

// KnownKey is the key of the #known directive for suite that applies to c, or
// would: known/<suite>/<input> when c is an input and the archive tags it so,
// or has no tag for suite at all, and known/<suite> otherwise.
func (c *Case) KnownKey(suite string) string {
	k := c.directiveKey(knownPrefix + suite)
	if _, ok := c.archive.dirs[k]; !ok && c.Input != "" {
		return knownPrefix + suite + "/" + c.Input
	}
	return k
}

// File returns the contents of the archive's file name, and whether it
// exists.
func (c *Case) File(name string) (string, bool) { return c.archive.ar.File(name) }

// Inputs returns the archive's files that are not goldens.
func (c *Case) Inputs() []txtar.File { return c.archive.ar.Inputs() }

// SectionFile is the name of suite's golden section for c in its archive:
// out/<suite>/bind when the archive is one case, and out/<suite>/<input> for
// an input.
func (c *Case) SectionFile(suite string) string {
	if c.Input != "" {
		return "out/" + suite + "/" + c.Input
	}
	return "out/" + suite + "/bind"
}

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

// Problems returns what is wrong with a as it stands, given the suites the
// corpus registers. Directive problems are in the archive's comment and its
// input files, which only its author can fix. Section problems are in its out/
// sections, which running with THEMA_UPDATE_GOLDEN=1 rewrites.
func (a *Archive) Problems(registered []string) (directives, sections []string) {
	add := func(p string) {
		if !slices.Contains(directives, p) {
			directives = append(directives, p)
		}
	}
	required := append([]string{dirDescription, dirExpect, dirSpec}, a.layout.Require...)
	for _, c := range a.Cases {
		for _, key := range required {
			if strings.TrimSpace(c.Directive(key)) != "" {
				continue
			}
			if c.Input == "" {
				add(fmt.Sprintf("the required directive #%s is missing", key))
			} else {
				add(fmt.Sprintf("the required directive #%s is missing for input %s; write #%s/%s, or #%s for every input", key, c.Input, key, c.Input, key))
			}
		}
		_, citeProblems := c.citations()
		for _, p := range citeProblems {
			add(p)
		}
	}
	if !suiteName.MatchString(a.Name) {
		add(fmt.Sprintf("the case's name %q is not kebab-case; rename the file", a.Name))
	}
	for _, c := range a.Cases {
		if c.Input != "" && !suiteName.MatchString(c.Input) {
			add(fmt.Sprintf("the input's name %q (%s) is not kebab-case; rename the file", c.Input, c.InputFile))
		}
	}
	for _, p := range a.problems {
		add(p)
	}
	for _, k := range slices.Sorted(maps.Keys(a.dirs)) {
		key, _ := splitDirective(k)
		if suite, ok := strings.CutPrefix(key, knownPrefix); ok && !slices.Contains(registered, suite) {
			add(fmt.Sprintf("#%s names a suite that is not registered", k))
		}
	}
	written := map[string]bool{}
	for _, c := range a.Cases {
		for _, s := range registered {
			written[c.SectionFile(s)] = true
		}
	}
	files := map[string]bool{}
	for _, f := range a.ar.Ar.Files {
		rest, isOut := strings.CutPrefix(f.Name, "out/")
		repeated := files[f.Name]
		files[f.Name] = true
		if !isOut {
			if repeated {
				add(fmt.Sprintf("the file %s appears more than once; only the first is read", f.Name))
			}
			continue
		}
		suite, _, _ := strings.Cut(rest, "/")
		switch {
		case repeated:
			sections = append(sections, fmt.Sprintf("%s appears more than once; only the first is compared", f.Name))
		case !slices.Contains(registered, suite):
			sections = append(sections, fmt.Sprintf("%s is an orphan: no suite %q is registered", f.Name, suite))
		case !written[f.Name] && a.layout.Inputs == "":
			sections = append(sections, fmt.Sprintf("%s is not a section the corpus writes; a suite's only section is out/%s/bind", f.Name, suite))
		case !written[f.Name]:
			sections = append(sections, fmt.Sprintf("%s is not a section the corpus writes; a suite's sections are out/%s/<input>, one for each input", f.Name, suite))
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

// checkArchive reports what is wrong with a, given the registry, and whether
// its cells can run. A problem in its out/ sections does not stop them, and
// is not reported when the run rewrites the sections.
func (r *run) checkArchive(t testing.TB, a *Archive) bool {
	t.Helper()
	registered := make([]string, 0, len(r.Suites))
	for _, s := range r.Suites {
		registered = append(registered, s.Name)
	}
	directives, sections := a.Problems(registered)
	if r.Root != "" {
		for _, c := range a.Cases {
			for _, p := range c.resolveCitations(r.Root) {
				if !slices.Contains(directives, p) {
					directives = append(directives, p)
				}
			}
		}
	}
	for _, p := range directives {
		t.Errorf("%s: %s; the format is in %s and the suites in %s",
			r.display(a.Path), p, r.display(r.readme()), r.display(filepath.Join(r.Dir, "suites.json")))
	}
	if !envvars.UpdateGoldenFiles {
		for _, p := range sections {
			t.Errorf("%s: %s; run with %s=1 to rewrite the out/ sections", r.display(a.Path), p, envvars.VarUpdateGolden)
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
	golden, hasGolden := c.File(c.SectionFile(s.Name))
	file := r.display(c.Path)
	if ok, why := s.applies(c); !ok {
		switch {
		case hasGolden:
			tag := ""
			if isKnown {
				tag = fmt.Sprintf(" and the #%s tag", c.KnownKey(s.Name))
			}
			// The section is kept, so that a golden update can't make a
			// suite stop running a case without anyone noticing.
			t.Errorf("%s: suite %s no longer applies to this case (%s), but %s records it running. "+
				"If the suite should still run this case, fix the suite. If it should not, delete the section%s by hand; %s=1 keeps it.",
				file, s.Name, why, c.SectionFile(s.Name), tag, envvars.VarUpdateGolden)
			return golden, true
		case isKnown:
			t.Errorf("%s: #%s is stale: the suite does not apply to this case (%s); remove the tag", file, c.KnownKey(s.Name), why)
		}
		t.Skipf("does not apply: %s", why)
		return "", false
	}

	out, err := runSuite(t, s, c)
	got := Render(r.op(), out, err)
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
		// An update records what the suite gives now. The cell stays
		// regressed, because its section then disagrees with #expect with no
		// #known tag, and the report shows the change from pass.
		fail(instructionRegressed)
	case status == StatusRegressed:
		fail(r.instructionUntracked(c.KnownKey(s.Name)))
	case status == StatusXPass:
		fail(r.instructionXPass(c, s.Name, known.Issue))
	case status == StatusKnown && hasGolden && was != v:
		// This fails with the update too: the section is rewritten, but the
		// tag is not, and nothing else would say that it may be stale.
		fail(InstructionKnownMoved(c.KnownKey(s.Name), was, v))
	case envvars.UpdateGoldenFiles:
	case !hasGolden:
		t.Errorf("%s\nThere is no golden section %s. Run with %s=1 to write it.", r.header(c, s, got), c.SectionFile(s.Name), envvars.VarUpdateGolden)
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
		"Do not add a #known tag to make it pass. " + envvars.VarUpdateGolden + "=1 records what the suite gives now, and the cell keeps failing until the suite agrees again. " +
		"If you believe #expect is wrong, it comes from the spec cited above: open an issue citing those lines instead of changing it."
	instructionRendering = "The verdict is unchanged, but what the suite reports changed (codes, headlines, positions or what it produced). " +
		"Review the diff below; if it is intended, run with " + envvars.VarUpdateGolden + "=1."
)

// InstructionUntracked is the instruction for a cell that disagrees with
// #expect when no golden section records it agreeing and no #known tag
// tracks it: a new case, or a section rewritten to the wrong verdict. key is
// the tag's key, known/<suite> or known/<suite>/<input> ([Case.KnownKey]).
func InstructionUntracked(key, readme string) string {
	return fmt.Sprintf("This suite disagrees with #expect, no golden section records it agreeing, and no #known tag tracks the disagreement. "+
		"If the case is new, this is step 5 of \"Adding a case\" in %s: find the issue that covers the mechanism and add "+
		"'#%s: #<issue> <one sentence naming the mechanism>', or, if no issue covers it, open one citing the #spec lines. "+
		"If git shows the section agreeing with #expect before your change, your change broke it: fix the code instead of tagging the cell.",
		readme, key)
}

func (r *run) instructionUntracked(key string) string {
	return InstructionUntracked(key, r.display(r.readme()))
}

// InstructionXPass is the instruction for a cell whose suite now agrees with
// #expect despite a #known tag, whose key is key. others are the other cells
// whose tags name the same issue.
func InstructionXPass(key, file string, issue int, others []string) string {
	msg := fmt.Sprintf("This suite now gives the verdict the spec expects: your change fixed it. "+
		"Remove the '#%s' line from %s, run with %s=1 to rewrite the section, and mention issue #%d in your commit. ",
		key, file, envvars.VarUpdateGolden, issue)
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
	return InstructionXPass(c.KnownKey(suite), r.display(c.Path), issue, others)
}

// InstructionKnownMoved is the instruction for a cell tagged #known, with the
// tag key, whose verdict changed, from was to now, and still disagrees with
// #expect.
func InstructionKnownMoved(key string, was, now Verdict) string {
	if was == "" {
		was = "an unreadable verdict"
	}
	return fmt.Sprintf("This suite's verdict changed from %s (the golden section) to %s. The #known tag accepts any verdict but #expect, so it may now name the wrong mechanism. "+
		"Check that #%s explains %s, or whether this is a different bug; update the tag, then run with %s=1, only once it does.",
		was, now, key, now, envvars.VarUpdateGolden)
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

const sectionDiffHeader = "What the suite reports, against the golden section (-golden +got):"

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
		fmt.Fprintf(&b, "#%s: #%d %s\n", c.KnownKey(s.Name), k.Issue, k.Reason)
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

// update rewrites a's out/ sections, keyed by file, to the ones its cells
// rendered, keeping its comment and input files as they are.
func (co Corpus) update(t *testing.T, a *Archive, sections map[string]string) {
	t.Helper()
	for _, c := range a.Cases {
		for _, s := range co.Suites {
			if ok, _ := s.applies(c); ok {
				if _, done := sections[c.SectionFile(s.Name)]; !done {
					t.Errorf("%s: not updated, because suite %s rendered nothing for %s", a.Path, s.Name, c.Name)
					return
				}
			}
		}
	}
	a.ar.Compare(t, sections)
}

// runSuite runs s.Run, turning a panic into the diagnostic thema reports for
// one (E0901): the correct behavior is never a panic, and a panicking cell
// still gets a verdict.
func runSuite(t testing.TB, s Suite, c *Case) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = "", diag.Internal(r)
		}
	}()
	return s.Run(t, c)
}

// Verdict is "ok" when the operation succeeds, or else the sorted,
// space-separated set of diagnostic codes it reports.
type Verdict string

// VerdictOK is the verdict of an operation that succeeds: a lineage that
// binds, or an input that is accepted.
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

// Render renders what a suite reported, running op, as a golden section: the
// verdict line, then what the operation produced when it succeeded, or else
// one line per diagnostic, sorted.
func Render(op diag.Op, out string, err error) string {
	if err == nil {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return verdictPrefix + string(VerdictOK) + "\n" + out
	}
	ds := diag.Diagnostics(err)
	if len(ds) == 0 {
		// thema's operations report only diagnostics; anything else is
		// converted as any error is (E0902, E0903), so that nothing it says is
		// dropped.
		ds = diag.From(err, diag.Context{Op: op})
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
// agrees with #expect; [Archive.Problems] reports it.
func CellStatus(c *Case, suite string) Status {
	sec, ok := c.File(c.SectionFile(suite))
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
