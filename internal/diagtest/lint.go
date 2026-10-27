// Package diagtest holds the checks every corpus of diagnostics is run
// through: the authoring principles in package diag's documentation, as code.
package diagtest

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/sdboyer/thema/diag"
)

// Form is the authoring form a corpus's input was written in. It decides which
// terms are the author's and which are not.
type Form int

const (
	// Normal: the lineage is written as thema.Lineage CUE. schemas, lenses,
	// input and result are the author's terms.
	Normal Form = iota
	// Authoring: the lineage is written as version files. The normal form's
	// terms are not the author's.
	Authoring
)

// Terms no headline, note or help may use, whatever the form: thema's kernel
// and pipeline.
var kernelTerms = []string{
	"_accum", "_schema", "_join", "_lens", "_version", "_ordered", "_counts", "#closed",
	"steps.", "Translate.out", "Lineage.", "SchemaDef", "/github.com/sdboyer/thema",
}

// Terms the authoring form does not use.
var normalFormTerms = []string{
	"result", "input", "lenses[", "lenses.", "schemas.", "schemas[",
}

// CUE's own phrasing. A headline that contains it was built from CUE's message
// rather than from thema's understanding of the problem.
var cuePhrases = []string{
	"conflicting values", "field not allowed", "mismatched types", "out of bound",
	"incomplete value", "empty disjunction", "cannot reference", "does not satisfy",
	"required but not present",
}

// Go's formatting of values a message should never show raw.
var goLeakage = []string{"map[", "&{", "%!", "[]string", "0x", "<nil>"}

var (
	positionInText = regexp.MustCompile(`\S+\.(cue|thema|json|yaml|yml):\d+:\d+`)
	bracketVersion = regexp.MustCompile(`\[\d+ \d+\]`)
)

// Lint checks ds against the mechanical authoring rules, failing t for each
// violation. It is called by every test whose output is diagnostics.
func Lint(t testing.TB, ds []diag.Diagnostic, form Form) {
	t.Helper()
	for i, d := range ds {
		where := fmt.Sprintf("diagnostic %d (%s)", i, d.Code)
		if _, ok := diag.Lookup(d.Code); !ok {
			t.Errorf("%s: code is not declared in diag.Codes", where)
		}
		lintText(t, where+" headline", d.Headline, form, true)
		for j, n := range d.Notes {
			// A note that carries CUE's text verbatim is allowed CUE's phrasing;
			// its purpose is to not drop what CUE said.
			lintText(t, fmt.Sprintf("%s note %d", where, j), n.Label, form, false)
		}
		for j, h := range d.Help {
			lintText(t, fmt.Sprintf("%s help %d", where, j), h, form, false)
		}
		if d.Primary != nil && d.Primary.Origin == diag.OriginThema {
			t.Errorf("%s: primary position is in thema's own CUE (%s); the primary is always the author's", where, d.Primary)
		}
		if d.Primary == nil {
			if info, ok := diag.Lookup(d.Code); ok && !info.Positionless {
				t.Errorf("%s: no primary position, and %s is not declared Positionless", where, d.Code)
			}
		}
	}
}

func lintText(t testing.TB, where, s string, form Form, headline bool) {
	t.Helper()
	if s == "" {
		t.Errorf("%s: empty", where)
		return
	}
	for _, term := range kernelTerms {
		if strings.Contains(s, term) {
			t.Errorf("%s: uses the kernel term %q: %q", where, term, s)
		}
	}
	if form == Authoring {
		for _, term := range normalFormTerms {
			if containsWord(s, term) {
				t.Errorf("%s: uses the normal form's term %q in a diagnostic about authoring files: %q", where, term, s)
			}
		}
	}
	for _, leak := range goLeakage {
		if strings.Contains(s, leak) {
			t.Errorf("%s: Go formatting leaked (%q): %q", where, leak, s)
		}
	}
	if bracketVersion.MatchString(s) {
		t.Errorf("%s: version written as [M m], not M.m: %q", where, s)
	}
	if !headline {
		return
	}
	for _, phrase := range cuePhrases {
		if strings.Contains(s, phrase) {
			t.Errorf("%s: contains CUE's phrasing %q; say it in thema's terms and keep CUE's text in a note: %q", where, phrase, s)
		}
	}
	if positionInText.MatchString(s) {
		t.Errorf("%s: contains a file:line:col; positions are structured: %q", where, s)
	}
	if strings.ContainsAny(s, "\n") {
		t.Errorf("%s: spans lines: %q", where, s)
	}
	if strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "...") {
		t.Errorf("%s: ends with a period: %q", where, s)
	}
	if len(s) > diag.MaxHeadline {
		t.Errorf("%s: longer than %d characters: %q", where, diag.MaxHeadline, s)
	}
	if r := []rune(s)[0]; unicode.IsUpper(r) && !startsWithIdentifier(s) {
		t.Errorf("%s: starts with a capital that is not an identifier: %q", where, s)
	}
}

// containsWord reports whether s contains term as a word, so that "result"
// is found but "resulting" is not.
func containsWord(s, term string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], term)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(term)
		before := start == 0 || !isWordByte(s[start-1])
		after := end == len(s) || !isWordByte(s[end])
		if before && after {
			return true
		}
		i = start + 1
	}
}

func isWordByte(b byte) bool {
	return b == '_' || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9')
}

// startsWithIdentifier allows a headline to begin with a capitalized name the
// author wrote: ForwardLens, BackwardLens, a lacuna type, a field.
func startsWithIdentifier(s string) bool {
	word, _, _ := strings.Cut(s, " ")
	word = strings.TrimRight(word, ":,'s")
	for _, r := range word {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == '#') {
			return false
		}
	}
	// A capitalized English word is not an identifier; an identifier has a
	// second capital, a digit, a dot or an underscore, or is a known name.
	rest := []rune(word)[1:]
	for _, r := range rest {
		if unicode.IsUpper(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == '#' {
			return true
		}
	}
	switch word {
	case "CUE", "JSON", "YAML",
		// A version file's schema field.
		"Schema":
		return true
	}
	return false
}
