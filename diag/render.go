package diag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sdboyer/thema/internal/embedreg"
)

// RenderOptions configures [Render].
type RenderOptions struct {
	// Source returns a file's text, for the excerpt under each position. nil,
	// or a false return, renders the position without an excerpt.
	Source func(file string) ([]byte, bool)
	// Rel is the directory the author's files are shown relative to. Empty
	// shows them as they are.
	Rel string
	// Display overrides how a filename is shown. nil shows thema's own CUE as
	// thema/<file> and the author's files relative to Rel.
	Display func(file string) string
}

func (o RenderOptions) display(file string) string {
	if o.Display != nil {
		return o.Display(file)
	}
	if rel, ok := embedreg.KernelPath(file); ok {
		return "thema/" + rel
	}
	if o.Rel != "" && filepath.IsAbs(file) {
		if rel, err := filepath.Rel(o.Rel, file); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return file
}

// Render writes ds as text, one block per diagnostic, in the layout of rustc:
// the severity and code, the headline, an excerpt of each file involved with
// the primary position underlined by ^ and each note's position by -, then
// positionless notes, detail and help.
func Render(w io.Writer, ds []Diagnostic, o RenderOptions) {
	for i, d := range ds {
		if i > 0 {
			fmt.Fprintln(w)
		}
		renderOne(w, d, o)
	}
}

// RenderJSON writes ds as a JSON [Report].
func RenderJSON(w io.Writer, ds []Diagnostic) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(NewReport(ds))
}

// An anchor is a position to mark in an excerpt.
type anchor struct {
	pos     Pos
	primary bool
	label   string
}

// layout is the gutter geometry of one diagnostic: every line hangs off the
// width of the widest line number shown.
type layout struct{ width int }

func (l layout) gutter(n string) string { return fmt.Sprintf("%*s |", l.width, n) }
func (l layout) arrow(secondary bool) string {
	if secondary {
		return strings.Repeat(" ", l.width) + "::: "
	}
	return strings.Repeat(" ", l.width) + "--> "
}
func (l layout) eq() string { return strings.Repeat(" ", l.width+1) + "= " }

func renderOne(w io.Writer, d Diagnostic, o RenderOptions) {
	fmt.Fprintf(w, "%s[%s]: %s\n", d.Severity, d.Code, d.Headline)

	// Group anchors by file, the primary's file first, then by first use.
	var files []string
	byFile := map[string][]anchor{}
	add := func(a anchor) {
		if _, ok := byFile[a.pos.File]; !ok {
			files = append(files, a.pos.File)
		}
		byFile[a.pos.File] = append(byFile[a.pos.File], a)
	}
	if d.Primary != nil {
		add(anchor{pos: *d.Primary, primary: true})
	}
	var loose []Note
	for _, n := range d.Notes {
		if n.Pos == nil {
			loose = append(loose, n)
			continue
		}
		add(anchor{pos: *n.Pos, label: n.Label})
	}

	l := layout{width: 1}
	for _, as := range byFile {
		for _, a := range as {
			l.width = max(l.width, len(fmt.Sprint(a.pos.Line)))
		}
	}
	shown := 0
	for _, f := range files {
		if renderFile(w, l, f, byFile[f], o) {
			shown++
		}
	}
	if shown == 0 && (len(loose) > 0 || d.Detail != "" || len(d.Help) > 0 || len(files) > 0) {
		fmt.Fprintln(w, l.gutter(""))
	}
	for _, f := range files {
		// Notes in files with no source: the label, then where.
		if o.Source != nil {
			if _, ok := o.Source(f); ok {
				continue
			}
		}
		for _, a := range byFile[f] {
			if a.primary {
				continue
			}
			renderKV(w, l, "note", a.label)
			fmt.Fprintf(w, "%s  --> %s:%d:%d\n", strings.Repeat(" ", l.width), o.display(f), a.pos.Line, a.pos.Col)
		}
	}
	for _, n := range loose {
		renderKV(w, l, "note", n.Label)
	}
	if d.Detail != "" {
		renderKV(w, l, "detail", d.Detail)
	}
	for _, h := range d.Help {
		renderKV(w, l, "help", h)
	}
	if d.Fix != nil {
		renderKV(w, l, "fix", fmt.Sprintf("at %s:%d:%d, write %s", o.display(d.Fix.Span.Start.File), d.Fix.Span.Start.Line, d.Fix.Span.Start.Col, d.Fix.Replacement))
	}
}

// renderKV writes "  = key: text", continuing a multi-line text aligned
// under its first line.
func renderKV(w io.Writer, l layout, key, text string) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	fmt.Fprintf(w, "%s%s: %s\n", l.eq(), key, lines[0])
	pad := strings.Repeat(" ", len(l.eq())+len(key)+2)
	for _, line := range lines[1:] {
		fmt.Fprintln(w, strings.TrimRight(pad+line, " "))
	}
}

// renderFile writes the excerpt for one file: the arrow line, then each
// anchored line with its marker line, with ... between lines that are not
// adjacent. It reports false when the file's source is unavailable, in which
// case it writes only the arrow for the primary, if it is in this file.
func renderFile(w io.Writer, l layout, file string, anchors []anchor, o RenderOptions) bool {
	sort.SliceStable(anchors, func(i, j int) bool {
		if anchors[i].pos.Line != anchors[j].pos.Line {
			return anchors[i].pos.Line < anchors[j].pos.Line
		}
		return anchors[i].pos.Col < anchors[j].pos.Col
	})
	first, hasPrimary := anchors[0], false
	for _, a := range anchors {
		if a.primary {
			first, hasPrimary = a, true
			break
		}
	}
	var src []byte
	ok := false
	if o.Source != nil {
		src, ok = o.Source(file)
	}
	if !ok {
		if hasPrimary {
			fmt.Fprintf(w, "%s%s:%d:%d\n", l.arrow(false), o.display(file), first.pos.Line, first.pos.Col)
		}
		return false
	}
	fmt.Fprintf(w, "%s%s:%d:%d\n", l.arrow(!hasPrimary), o.display(file), first.pos.Line, first.pos.Col)

	lines := bytes.Split(src, []byte("\n"))
	gutter := l.gutter
	fmt.Fprintln(w, gutter(""))
	prev := 0
	for i := 0; i < len(anchors); {
		a := anchors[i]
		if prev != 0 && a.pos.Line > prev+1 {
			fmt.Fprintln(w, "...")
		}
		prev = a.pos.Line
		text := ""
		if a.pos.Line >= 1 && a.pos.Line <= len(lines) {
			text = string(lines[a.pos.Line-1])
		}
		fmt.Fprintf(w, "%s %s\n", gutter(fmt.Sprint(a.pos.Line)), expandTabs(text))
		// Every anchor on this line gets its own marker line.
		for ; i < len(anchors) && anchors[i].pos.Line == a.pos.Line; i++ {
			b := anchors[i]
			col, n := markerSpan(text, b.pos.Col)
			mark := "^"
			if !b.primary {
				mark = "-"
			}
			line := gutter("") + " " + strings.Repeat(" ", col) + strings.Repeat(mark, n)
			if b.label != "" {
				line += " " + b.label
			}
			fmt.Fprintln(w, line)
		}
	}
	fmt.Fprintln(w, gutter(""))
	return true
}

const tabWidth = 4

func expandTabs(s string) string {
	return strings.ReplaceAll(s, "\t", strings.Repeat(" ", tabWidth))
}

// markerSpan returns the display column (in runes, tabs expanded) at which to
// start a marker for byte column col of text, and how many marks to draw: the
// token at col, or one mark where no token starts there.
func markerSpan(text string, col int) (int, int) {
	if col < 1 {
		col = 1
	}
	if col-1 > len(text) {
		return displayWidth(text), 1
	}
	prefix := text[:col-1]
	rest := text[col-1:]
	return displayWidth(prefix), max(1, tokenLen(rest))
}

func displayWidth(s string) int {
	return utf8.RuneCountInString(expandTabs(s))
}

// tokenLen measures the token at the start of s: a quoted string, an
// identifier (including # and _ and $), a run of digits, ..., or nothing.
func tokenLen(s string) int {
	if s == "" {
		return 0
	}
	r, size := utf8.DecodeRuneInString(s)
	switch {
	case r == '"':
		for i := size; i < len(s); {
			c, n := utf8.DecodeRuneInString(s[i:])
			i += n
			if c == '\\' {
				_, n := utf8.DecodeRuneInString(s[i:])
				i += n
				continue
			}
			if c == '"' {
				return utf8.RuneCountInString(s[:i])
			}
		}
		return utf8.RuneCountInString(s)
	case strings.HasPrefix(s, "..."):
		return 3
	case isIdent(r):
		n := 0
		for _, c := range s {
			if !isIdent(c) && !unicode.IsDigit(c) {
				break
			}
			n++
		}
		return n
	case unicode.IsDigit(r):
		n := 0
		for _, c := range s {
			if !unicode.IsDigit(c) && c != '.' {
				break
			}
			n++
		}
		return n
	}
	return 1
}

func isIdent(r rune) bool {
	return unicode.IsLetter(r) || r == '_' || r == '#' || r == '$'
}
