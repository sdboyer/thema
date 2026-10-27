package diag

import "fmt"

// Severity is how seriously a Diagnostic is to be taken.
type Severity string

const (
	// SeverityError: the operation failed. A lineage with an error does not bind; data
	// with an error is not an instance.
	SeverityError Severity = "error"
	// SeverityWarning: the operation succeeded, but the author should look.
	SeverityWarning Severity = "warning"
	// SeverityNote: information with no judgment attached.
	SeverityNote Severity = "note"
)

// Origin says whose text a position points into.
type Origin string

const (
	// OriginUser: a file the author wrote, or data they supplied.
	OriginUser Origin = "user"
	// OriginThema: thema's own CUE. Never the author's line.
	OriginThema Origin = "thema"
	// OriginSynth: a position with no file behind it, such as a value built
	// in Go without a filename.
	OriginSynth Origin = "synth"
)

// Code identifies a diagnostic's meaning, stably: E0304 always means the same
// thing, and a retired code is never reused. Codes are grouped by hundreds;
// see [Codes].
type Code string

// MaxHeadline is the longest a headline may be. A longer statement is split: a
// shorter headline names the rule and the construct, and the statement becomes
// a note.
const MaxHeadline = 140

// Pos is a position in a file. Line and Col are 1-based; Col counts bytes, as
// CUE reports it.
type Pos struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Col    int    `json:"col"`
	Offset int    `json:"offset,omitempty"`
	Origin Origin `json:"origin"`
}

func (p Pos) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Span is a range in one file, from Start up to but not including End.
type Span struct {
	Start Pos `json:"start"`
	End   Pos `json:"end"`
}

// Note is a supporting statement, optionally anchored at a position.
type Note struct {
	Pos   *Pos   `json:"pos,omitempty"`
	Label string `json:"label"`
}

// Fix is a machine-applicable edit that resolves the diagnostic.
type Fix struct {
	Span        Span   `json:"span"`
	Replacement string `json:"replacement"`
}

// Diagnostic is one thing thema has to say about an input.
type Diagnostic struct {
	Code     Code     `json:"code"`
	Severity Severity `json:"severity"`
	// Headline names the rule and what violates it, in the author's vocabulary.
	Headline string `json:"headline"`
	// Path locates the subject in the author's vocabulary, rendered from
	// Address by a Vocabulary.
	Path []string `json:"path,omitempty"`
	// Address is the structured subject, in terms independent of how the
	// lineage was written.
	Address *Address `json:"address,omitempty"`
	// Primary is the author's position, or nil when none could be found.
	Primary *Pos `json:"primary,omitempty"`
	// Notes say why, and where else to look.
	Notes []Note `json:"notes,omitempty"`
	// Help says what to do.
	Help []string `json:"help,omitempty"`
	// Fix is an edit that resolves the diagnostic, when thema knows one.
	Fix *Fix `json:"fix,omitempty"`
	// Detail is supporting text that is not thema's own, such as CUE's
	// message verbatim.
	Detail string `json:"detail,omitempty"`
}

// Report is what a tool emits: the diagnostics of one operation.
type Report struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Summary     Summary      `json:"summary"`
}

// Summary counts a Report's diagnostics by severity.
type Summary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Notes    int `json:"notes"`
}

// NewReport builds a Report over ds.
func NewReport(ds []Diagnostic) Report {
	r := Report{Diagnostics: ds}
	if r.Diagnostics == nil {
		r.Diagnostics = []Diagnostic{}
	}
	for _, d := range ds {
		switch d.Severity {
		case SeverityError:
			r.Summary.Errors++
		case SeverityWarning:
			r.Summary.Warnings++
		case SeverityNote:
			r.Summary.Notes++
		}
	}
	return r
}

// HasErrors reports whether any of ds is an error.
func HasErrors(ds []Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}
