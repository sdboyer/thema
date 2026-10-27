package diag

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
)

// B builds an [Error]. Every thema-internal site that reports a problem goes
// through it, so that the problem has a code, an author position and the
// structure a tool needs.
//
//	return diag.New(E0202).
//		Headlinef("schema %s does not follow %s", curr, prev).
//		AtValue(v).
//		Help(fmt.Sprintf("the next version is %s or %s", nextMinor, nextMajor)).
//		Err()
type B struct {
	e *Error
	// declared: the builder has noted the author's declaration of what the
	// diagnostic is about, so the positions CUE reports for the same
	// declaration, in thema's CUE or in the author's schema, add nothing and
	// are not noted. Positions in the data still are.
	declared bool
	// declareWith, when the builder could not find the declaration itself,
	// labels the first position CUE reports outside the data as the
	// declaration, after which declared applies.
	declareWith string
	// dropArms: a disjunction shape has said in its own words what each arm
	// said, so the arms' text and positions are not noted.
	dropArms bool
}

// New starts a diagnostic with code c. The code must be declared in [Codes];
// an undeclared code is a programming error.
func New(c Code) *B {
	info, ok := Lookup(c)
	if !ok {
		panic(fmt.Sprintf("diag: code %s is not declared in Codes", c))
	}
	return &B{e: &Error{d: Diagnostic{Code: c, Severity: info.Severity, Headline: info.Title}}}
}

// Headline sets the headline. Without it, the code's title is the headline.
func (b *B) Headline(s string) *B {
	b.e.d.Headline = s
	return b
}

// Headlinef is Headline with a formatted text.
func (b *B) Headlinef(format string, args ...any) *B {
	b.e.d.Headline = fmt.Sprintf(format, args...)
	return b
}

// At sets the primary position. An invalid position leaves the diagnostic
// without one.
func (b *B) At(p token.Pos) *B {
	b.e.primary = p
	b.e.d.Primary = PosOf(p)
	return b
}

// AtValue sets the primary position to the author's text for v, per
// [AuthorPos]. A value positioned only in thema's CUE gets that position as a
// note instead, and no primary.
func (b *B) AtValue(v cue.Value) *B {
	p, ok := AuthorPos(v)
	if ok {
		return b.At(p)
	}
	if p.IsValid() {
		return b.Note(p, "in thema's own CUE")
	}
	return b
}

// Addr sets the structured subject. Path is rendered from it by [Vocabulary]
// at the time the diagnostic is reported; until then it is in [NormalForm].
func (b *B) Addr(a Address) *B {
	b.e.d.Address = &a
	b.e.d.Path = NormalForm.Path(a)
	return b
}

// Note adds a note at p. An invalid p adds a positionless note.
func (b *B) Note(p token.Pos, label string) *B {
	b.e.d.Notes = append(b.e.d.Notes, Note{Pos: PosOf(p), Label: label})
	if p.IsValid() {
		b.e.notePos = append(b.e.notePos, p)
	}
	return b
}

// Notef is Note with a formatted label.
func (b *B) Notef(p token.Pos, format string, args ...any) *B {
	return b.Note(p, fmt.Sprintf(format, args...))
}

// Help adds a line saying what to do.
func (b *B) Help(s string) *B {
	b.e.d.Help = append(b.e.d.Help, s)
	return b
}

// Helpf is Help with a formatted line.
func (b *B) Helpf(format string, args ...any) *B {
	return b.Help(fmt.Sprintf(format, args...))
}

// Fix sets a machine-applicable edit.
func (b *B) Fix(span Span, replacement string) *B {
	b.e.d.Fix = &Fix{Span: span, Replacement: replacement}
	return b
}

// Detail sets supporting text that is not thema's own.
func (b *B) Detail(s string) *B {
	b.e.d.Detail = s
	return b
}

// Cause records the CUE error the diagnostic explains. Every entry's text
// goes into the detail, verbatim, and every distinct position becomes a note,
// so that nothing CUE said is lost while the headline stays thema's own.
func (b *B) Cause(err error) *B {
	if err == nil {
		return b
	}
	b.e.cause = err
	var texts []string
	for _, e := range cerrors.Errors(err) {
		texts = append(texts, stripClosed(e.Error()))
		ps := dedup(append([]token.Pos{e.Position()}, e.InputPositions()...))
		// Positions in thema's own CUE add nothing once the author's are there.
		user := false
		for _, p := range ps {
			user = user || OriginOf(p.Filename()) == OriginUser
		}
		first := true
		for _, p := range ps {
			if user && OriginOf(p.Filename()) == OriginThema {
				continue
			}
			if first {
				b.Note(p, hereLabel(p))
			} else {
				b.Note(p, alsoLabel(p))
			}
			first = false
		}
	}
	detail := strings.Join(texts, "\n")
	if b.e.d.Detail != "" {
		detail = b.e.d.Detail + "\n" + detail
	}
	b.e.d.Detail = detail
	return b
}

// stripClosed removes thema's own roots from the path CUE prefixes a message
// with: the label thema closes a value under before checking it
// (internal/cuetil.CloseRecursive), and the kernel's Lineage, which a lineage
// unified with thema.Lineage is rooted in. The author wrote neither.
func stripClosed(text string) string {
	for _, prefix := range []string{"#closed.", "#closed: ", "Lineage."} {
		if rest, ok := strings.CutPrefix(text, prefix); ok {
			return rest
		}
	}
	return text
}

// hereLabel labels the first position of one CUE error.
func hereLabel(p token.Pos) string {
	if OriginOf(p.Filename()) == OriginThema {
		return "in thema's own CUE"
	}
	return "here"
}

// alsoLabel labels a further position of one CUE error.
func alsoLabel(p token.Pos) string {
	if OriginOf(p.Filename()) == OriginThema {
		return "in thema's own CUE"
	}
	return "also here"
}

// Err returns the built error.
func (b *B) Err() *Error { return b.e }
