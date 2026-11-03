package report

import (
	"maps"
	"slices"

	"github.com/sdboyer/thema/internal/corpus"
)

// Transition is a way a cell changed between the two revisions.
type Transition string

// The transitions a report calls out, most severe first.
const (
	// TRegressed: the cell is regressed after and was not before, or it passed
	// before and is known after, where a new #known tag accepted the
	// regression.
	TRegressed Transition = "regressed"
	// TXPass: the cell is xpass after and was not before.
	TXPass Transition = "xpass"
	// TNoLongerApplies: the suite ran the case before and does not after.
	TNoLongerApplies Transition = "no longer applies"
	// TStatusChanged: any other change of status, between revisions that both
	// have the cell.
	TStatusChanged Transition = "status changed"
	// TKnownVerdictChanged: the cell is known in both revisions, but its
	// verdict changed, so its #known tag may name the wrong mechanism.
	TKnownVerdictChanged Transition = "known verdict changed"
	// TExpectChanged: the case's #expect changed, which is a change of the spec.
	TExpectChanged Transition = "#expect changed"
	// TFixed: the cell was known before and passes after, with the same
	// #expect.
	TFixed Transition = "fixed"
	// TNowApplies: the suite runs the case after and did not before.
	TNowApplies Transition = "now applies"
	// TKnownChanged: the status is the same, but the #known tag's issue or
	// reason changed.
	TKnownChanged Transition = "#known changed"
	// TRenderingChanged: the status is the same, but the golden differs.
	TRenderingChanged Transition = "rendering changed"
	TRemovedCase      Transition = "removed case"
	TRemovedSuite     Transition = "removed suite"
	TNewCase          Transition = "new case"
	TNewSuite         Transition = "new suite"
)

// Transitions lists every transition, most severe first.
var Transitions = []Transition{
	TRegressed, TXPass, TNoLongerApplies, TStatusChanged, TKnownVerdictChanged, TExpectChanged, TFixed,
	TNowApplies, TKnownChanged, TRenderingChanged, TRemovedCase, TRemovedSuite,
	TNewCase, TNewSuite,
}

func severity(t Transition) int { return slices.Index(Transitions, t) }

// Loud lists the transitions a reviewer must not miss: each is a change in
// what the corpus says about the code or the spec that a golden update
// alone does not make, and a report shows it in bold.
var Loud = []Transition{TRegressed, TXPass, TNoLongerApplies, TKnownVerdictChanged, TExpectChanged}

// IsLoud reports whether t is one of Loud.
func (t Transition) IsLoud() bool { return slices.Contains(Loud, t) }

// Report compares a corpus at two revisions.
type Report struct {
	// Dir is the corpus directory.
	Dir   string
	Range Range
	// Suites are the suites of either revision: the after revision's in
	// registry order, then those only the before revision has.
	Suites []SuiteInfo
	// Cases are the cases of either revision, sorted by name.
	Cases []CaseInfo
	// Cells holds one cell per case and suite, by case, then by suite in the
	// order of Suites.
	Cells []Cell
	// BeforeProblems and AfterProblems are each revision's breaches of the
	// corpus format.
	BeforeProblems, AfterProblems []string
	// BeforeMissing says the before revision has no corpus directory.
	BeforeMissing bool
	// Unreadable names the revisions whose suites.json could not be read. The
	// cells were not compared, and Suites, Cases and Cells are empty.
	Unreadable []string
}

// SuiteInfo is a suite as the report shows it: from the after revision when it
// has the suite.
type SuiteInfo struct {
	Suite
	New     bool
	Removed bool
}

// CaseInfo is a case as the report shows it: from the after revision when it
// has the case.
type CaseInfo struct {
	Name        string
	Description string
	Expect      string
	// ExpectBefore is #expect in the before revision, when it differs.
	ExpectBefore string
	Spec         string
	At           string
	New          bool
	Removed      bool
}

// Cell is one case run by one suite, in both revisions.
type Cell struct {
	Case, Suite   string
	Before, After Status
	// KnownBefore and KnownAfter are the #known/<suite> tags, or nil.
	KnownBefore, KnownAfter *corpus.Known
	// GoldenBefore and GoldenAfter are out/<suite>/bind, or empty.
	GoldenBefore, GoldenAfter string
	GoldenChanged             bool
	// Transitions are the ways the cell changed, most severe first.
	Transitions []Transition
}

// Changed reports whether the cell changed between the revisions.
func (c Cell) Changed() bool { return len(c.Transitions) > 0 }

// Failing reports whether the after revision has the cell in a status a
// committed corpus may not have.
func (c Cell) Failing() bool { return c.After == Regressed || c.After == XPass }

// Build compares before with after.
func Build(dir string, rg Range, before, after *Corpus) *Report {
	r := &Report{Dir: dir, Range: rg, BeforeProblems: before.Problems, AfterProblems: after.Problems, BeforeMissing: before.Missing}
	if before.Unreadable {
		r.Unreadable = append(r.Unreadable, rg.Before)
	}
	if after.Unreadable {
		r.Unreadable = append(r.Unreadable, rg.AfterLabel())
	}
	if len(r.Unreadable) > 0 {
		return r
	}

	for _, s := range after.Suites {
		_, had := before.Suite(s.Name)
		r.Suites = append(r.Suites, SuiteInfo{Suite: s, New: !had})
	}
	for _, s := range before.Suites {
		if _, has := after.Suite(s.Name); !has {
			r.Suites = append(r.Suites, SuiteInfo{Suite: s, Removed: true})
		}
	}

	names := map[string]bool{}
	for _, k := range append(slices.Clone(before.Cases), after.Cases...) {
		names[k.Name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		kb, ka := before.Case(name), after.Case(name)
		shown := ka
		if shown == nil {
			shown = kb
		}
		info := CaseInfo{
			Name: name, Description: shown.Description, Expect: shown.Directive("expect"),
			Spec: shown.Spec, At: shown.Directive("at"), New: kb == nil, Removed: ka == nil,
		}
		expectChanged := kb != nil && ka != nil && expectOf(kb) != expectOf(ka)
		if expectChanged {
			info.ExpectBefore = kb.Directive("expect")
		}
		r.Cases = append(r.Cases, info)

		for _, s := range r.Suites {
			c := Cell{Case: name, Suite: s.Name, Before: Absent, After: Absent}
			if kb != nil && !s.New {
				c.Before, c.GoldenBefore, c.KnownBefore = cellOf(kb, s.Name)
			}
			if ka != nil && !s.Removed {
				c.After, c.GoldenAfter, c.KnownAfter = cellOf(ka, s.Name)
			}
			c.GoldenChanged = c.GoldenBefore != c.GoldenAfter
			c.Transitions = transitions(c, info, s, expectChanged)
			r.Cells = append(r.Cells, c)
		}
	}
	return r
}

// expectOf is k's #expect as corpus.ParseVerdict reads it, so that writing
// the same codes in another order doesn't change it, or as written when it
// can't be read.
func expectOf(k *corpus.Case) string {
	if k.Expect != "" {
		return string(k.Expect)
	}
	return k.Directive("expect")
}

func cellOf(k *corpus.Case, suite string) (Status, string, *corpus.Known) {
	var tag *corpus.Known
	if t, ok := k.Known[suite]; ok {
		tag = &t
	}
	golden, _ := k.File(corpus.SectionFile(suite))
	return CellStatus(k, suite), golden, tag
}

func transitions(c Cell, k CaseInfo, s SuiteInfo, expectChanged bool) []Transition {
	var out []Transition
	b, a := c.Before, c.After
	// A cell the suite doesn't run, in the revision that has it, didn't do
	// anything that a new or removed case or suite would call out.
	runs := func(st Status) bool { return st != Absent && st != NotApplicable }
	if k.New && runs(a) {
		out = append(out, TNewCase)
	}
	if k.Removed && runs(b) {
		out = append(out, TRemovedCase)
	}
	if s.New && runs(a) {
		out = append(out, TNewSuite)
	}
	if s.Removed && runs(b) {
		out = append(out, TRemovedSuite)
	}

	regressed := (a == Regressed && b != Regressed) || (b == Pass && a == Known)
	if regressed {
		out = append(out, TRegressed)
	}
	xpass := a == XPass && b != XPass
	if xpass {
		out = append(out, TXPass)
	}

	if b != Absent && a != Absent {
		if expectChanged && (runs(a) || runs(b)) {
			out = append(out, TExpectChanged)
		}
		switch {
		case b == a:
			if b == Known && corpus.ReadVerdict(c.GoldenBefore) != corpus.ReadVerdict(c.GoldenAfter) {
				out = append(out, TKnownVerdictChanged)
			}
			if !equalTags(c.KnownBefore, c.KnownAfter) {
				out = append(out, TKnownChanged)
			}
			if c.GoldenChanged {
				out = append(out, TRenderingChanged)
			}
		case regressed || xpass:
		case b == Known && a == Pass && !expectChanged:
			// With #expect changed, the spec moved to the code, not the code
			// to the spec.
			out = append(out, TFixed)
		case b == NotApplicable:
			out = append(out, TNowApplies)
		case a == NotApplicable:
			out = append(out, TNoLongerApplies)
		default:
			out = append(out, TStatusChanged)
		}
	}
	slices.SortStableFunc(out, func(x, y Transition) int { return severity(x) - severity(y) })
	return out
}

func equalTags(x, y *corpus.Known) bool {
	if x == nil || y == nil {
		return x == y
	}
	return *x == *y
}

// Cell returns the cell for case i and suite j.
func (r *Report) Cell(i, j int) Cell { return r.Cells[i*len(r.Suites)+j] }

// Changed returns the cells that changed, most severe first, then by case and
// by suite.
func (r *Report) Changed() []Cell {
	var out []Cell
	for _, c := range r.Cells {
		if c.Changed() {
			out = append(out, c)
		}
	}
	// Cells are already by case and suite; a stable sort keeps that order
	// within a severity.
	slices.SortStableFunc(out, func(x, y Cell) int {
		return severity(x.Transitions[0]) - severity(y.Transitions[0])
	})
	return out
}

// StatusCounts counts the cells in each status in the after revision.
func (r *Report) StatusCounts() map[Status]int {
	n := map[Status]int{}
	for _, c := range r.Cells {
		n[c.After]++
	}
	return n
}

// TransitionCounts counts the cells that have each transition.
func (r *Report) TransitionCounts() map[Transition]int {
	n := map[Transition]int{}
	for _, c := range r.Cells {
		for _, t := range c.Transitions {
			n[t]++
		}
	}
	return n
}

// Failing reports whether the after revision is not a corpus that may be
// committed: a cell is regressed or xpass, or the corpus breaks its format.
func (r *Report) Failing() bool {
	if len(r.AfterProblems) > 0 {
		return true
	}
	return slices.ContainsFunc(r.Cells, Cell.Failing)
}
