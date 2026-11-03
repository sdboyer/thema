package report

import (
	_ "embed"
	"html/template"
	"io"
	"slices"

	"github.com/sdboyer/thema/internal/corpus"
)

//go:embed report.html.tmpl
var htmlTemplate string

var htmlPage = template.Must(template.New("report").Parse(htmlTemplate))

// htmlData is what the page's script reads: the report, laid out for the
// matrix.
type htmlData struct {
	Dir, Range, Before, After string
	Statuses                  []Status
	Transitions               []Transition
	Suites                    []SuiteInfo
	Cases                     []htmlCase
	BeforeProblems            []string
	AfterProblems             []string
}

type htmlCase struct {
	CaseInfo
	Cells []htmlCell
}

type htmlCell struct {
	Before, After           Status
	KnownBefore, KnownAfter *corpus.Known
	Transitions             []Transition
	// Golden is the golden when it didn't change, and Diff the line diff
	// when it did.
	Golden string     `json:",omitempty"`
	Diff   []htmlLine `json:",omitempty"`
}

type htmlLine struct {
	Op   string
	Text string
}

// HTML writes r as one self-contained page: a matrix of cases by suites, each
// cell colored by its status in the after revision and marked when it changed,
// with a panel that shows a cell's detail and the diff of its golden.
func HTML(w io.Writer, r *Report) error {
	d := htmlData{
		Dir:            r.Dir,
		Range:          r.Range.Label,
		Before:         r.Range.Before,
		After:          r.Range.AfterLabel(),
		Statuses:       append(slices.Clone(Statuses), Absent),
		Transitions:    Transitions,
		Suites:         r.Suites,
		BeforeProblems: r.BeforeProblems,
		AfterProblems:  r.AfterProblems,
	}
	for i, k := range r.Cases {
		hc := htmlCase{CaseInfo: k}
		for j := range r.Suites {
			c := r.Cell(i, j)
			cell := htmlCell{
				Before: c.Before, After: c.After,
				KnownBefore: c.KnownBefore, KnownAfter: c.KnownAfter,
				Transitions: c.Transitions,
			}
			if c.GoldenChanged {
				for _, l := range LineDiff(c.GoldenBefore, c.GoldenAfter) {
					cell.Diff = append(cell.Diff, htmlLine{Op: string(l.Op), Text: l.Text})
				}
			} else {
				cell.Golden = c.GoldenAfter
			}
			hc.Cells = append(hc.Cells, cell)
		}
		d.Cases = append(d.Cases, hc)
	}
	return htmlPage.Execute(w, d)
}
