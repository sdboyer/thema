package report

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// Markdown writes a summary of r for a pull request's description: the count
// of each transition, what a reviewer must judge (cases whose #expect changed,
// regressions a new #known tag accepts, and the cells that fail), then a table
// of every changed cell, most severe first.
//
// Cells that only a new case or a new suite brings, and that pass, are
// collapsed into one row per case (or per suite, for a new suite), so that
// adding a corpus doesn't list every cell of it. Every other changed cell has
// its own row.
func Markdown(w io.Writer, r *Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "### Corpus `%s`: `%s`\n\n", r.Dir, r.Range.Label)

	if len(r.Unreadable) > 0 {
		fmt.Fprintf(&b, "**The cells were not compared: %s could not be read in %s.**\n\n", SuitesFile, strings.Join(r.Unreadable, " and "))
		writeProblems(&b, r)
		_, err := io.WriteString(w, b.String())
		return err
	}
	if r.BeforeMissing {
		fmt.Fprintf(&b, "The corpus directory does not exist in %s, so every cell is new.\n\n", r.Range.Before)
	}

	counts := r.TransitionCounts()
	var parts []string
	for _, t := range Transitions {
		if n := counts[t]; n > 0 {
			part := fmt.Sprintf("%s: %d", t, n)
			if t.IsLoud() {
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

	writeProblems(&b, r)

	var expect []CaseInfo
	for _, k := range r.Cases {
		if k.ExpectBefore != "" {
			expect = append(expect, k)
		}
	}
	if len(expect) > 0 {
		b.WriteString("**#expect changed, which changes what the spec says:**\n\n")
		for _, k := range expect {
			fmt.Fprintf(&b, "- `%s`: was `%s`, now `%s` (spec: %s)\n", k.Name, k.ExpectBefore, k.Expect, k.Spec)
		}
		b.WriteString("\n")
	}

	var accepted []Cell
	for _, c := range r.Cells {
		if c.After == Known && slices.Contains(c.Transitions, TRegressed) {
			accepted = append(accepted, c)
		}
	}
	if len(accepted) > 0 {
		b.WriteString("**Regressions a new #known tag accepts:**\n\n")
		for _, c := range accepted {
			fmt.Fprintf(&b, "- `%s` × `%s`: #%d %s\n", c.Case, c.Suite, c.KnownAfter.Issue, c.KnownAfter.Reason)
		}
		b.WriteString("\n")
	}

	if slices.ContainsFunc(r.Cells, Cell.Failing) {
		fmt.Fprintf(&b, "**Failing cells in %s**, changed in this range or not:\n\n", r.Range.AfterLabel())
		for _, c := range r.Cells {
			if c.Failing() {
				fmt.Fprintf(&b, "- `%s` × `%s`: %s\n", c.Case, c.Suite, c.After)
			}
		}
		b.WriteString("\n")
	}

	if rows := markdownRows(r.Changed()); len(rows) > 0 {
		b.WriteString("| Case | Suite | Before → After | Change |\n|---|---|---|---|\n")
		for _, row := range rows {
			fmt.Fprintf(&b, "| %s | %s | %s → %s | %s |\n", row.cases, row.suites, row.before, row.after, row.change)
		}
	}
	if counts[TNoLongerApplies] > 0 {
		b.WriteString("\nA cell that no longer applies lost its section. The tests decide whether a suite applies, so only `go test` can say whether that is right.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeProblems(b *strings.Builder, r *Report) {
	if len(r.AfterProblems) == 0 {
		return
	}
	fmt.Fprintf(b, "**The corpus in %s breaks its format:**\n\n", r.Range.AfterLabel())
	for _, p := range r.AfterProblems {
		fmt.Fprintf(b, "- %s\n", p)
	}
	b.WriteString("\n")
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
			change: markdownTransitions(c.Transitions),
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

// markdownTransitions is ts for a Markdown row: the loud ones in bold.
func markdownTransitions(ts []Transition) string {
	s := make([]string, len(ts))
	for i, t := range ts {
		s[i] = string(t)
		if t.IsLoud() {
			s[i] = "**" + s[i] + "**"
		}
	}
	return strings.Join(s, ", ")
}
