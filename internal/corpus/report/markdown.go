package report

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// Markdown writes a summary of r for a pull request's description: the count
// of each transition, then a table of every changed cell, most severe first.
//
// Cells that only a new case or a new suite brings, and that pass, are
// collapsed into one row per case (or per suite, for a new suite), so that
// adding a corpus doesn't list every cell of it. Every other changed cell has
// its own row.
func Markdown(w io.Writer, r *Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "### Corpus `%s`: `%s`\n\n", r.Dir, r.Range.Label)

	counts := r.TransitionCounts()
	var parts []string
	for _, t := range Transitions {
		if n := counts[t]; n > 0 {
			part := fmt.Sprintf("%s: %d", t, n)
			if t == TRegressed || t == TXPass {
				part = "**" + part + "**"
			}
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		b.WriteString("No cell changed.\n\n")
	} else {
		fmt.Fprintf(&b, "Changes: %s.\n\n", strings.Join(parts, ", "))
	}

	statuses := r.StatusCounts()
	parts = parts[:0]
	for _, s := range Statuses {
		if n := statuses[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", s, n))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, "Cells in %s: %s.\n\n", r.Range.AfterLabel(), strings.Join(parts, ", "))
	}

	if len(r.AfterProblems) > 0 {
		fmt.Fprintf(&b, "**The corpus in %s breaks its format:**\n\n", r.Range.AfterLabel())
		for _, p := range r.AfterProblems {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		b.WriteString("\n")
	}

	if rows := markdownRows(r.Changed()); len(rows) > 0 {
		b.WriteString("| Case | Suite | Before → After | Change |\n|---|---|---|---|\n")
		for _, row := range rows {
			fmt.Fprintf(&b, "| %s | %s | %s → %s | %s |\n", row.cases, row.suites, row.before, row.after, row.change)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

type markdownRow struct {
	cases, suites, before, after, change string
}

func markdownRows(changed []Cell) []markdownRow {
	type group struct {
		key   string
		cells []Cell
	}
	var groups []*group
	byKey := map[string]*group{}
	for _, c := range changed {
		key := ""
		if c.After == Pass {
			switch {
			case slices.Equal(c.Transitions, []Transition{TNewCase}),
				slices.Equal(c.Transitions, []Transition{TNewCase, TNewSuite}):
				key = "case\x00" + c.Case
			case slices.Equal(c.Transitions, []Transition{TNewSuite}):
				key = "suite\x00" + c.Suite
			}
		}
		if g, ok := byKey[key]; ok && key != "" {
			g.cells = append(g.cells, c)
			continue
		}
		g := &group{key: key, cells: []Cell{c}}
		groups = append(groups, g)
		if key != "" {
			byKey[key] = g
		}
	}

	rows := make([]markdownRow, 0, len(groups))
	for _, g := range groups {
		c := g.cells[0]
		row := markdownRow{
			cases:  "`" + c.Case + "`",
			suites: "`" + c.Suite + "`",
			before: string(c.Before),
			after:  string(c.After),
			change: joinTransitions(c.Transitions),
		}
		if n := len(g.cells); n > 1 {
			if strings.HasPrefix(g.key, "case") {
				row.suites = fmt.Sprintf("%d suites", n)
			} else {
				row.cases = fmt.Sprintf("%d cases", n)
			}
			row.change = string(c.Transitions[0])
		}
		rows = append(rows, row)
	}
	return rows
}

func joinTransitions(ts []Transition) string {
	s := make([]string, len(ts))
	for i, t := range ts {
		s[i] = string(t)
	}
	return strings.Join(s, ", ")
}
