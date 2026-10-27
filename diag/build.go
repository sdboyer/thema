package diag

import (
	"fmt"

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

// Headlinef sets the headline. Without it, the code's title is the headline.
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

// Cause records the CUE error the diagnostic explains. Its text and positions
// become notes, each entry's verbatim message at its first position and every
// other distinct position as a further note, so that nothing CUE said is lost
// while the headline stays thema's own.
func (b *B) Cause(err error) *B {
	if err == nil {
		return b
	}
	b.e.cause = err
	for _, e := range cerrors.Errors(err) {
		ps := dedup(append([]token.Pos{e.Position()}, e.InputPositions()...))
		if len(ps) == 0 {
			b.Note(token.NoPos, e.Error())
			continue
		}
		b.Note(ps[0], e.Error())
		for _, p := range ps[1:] {
			b.Note(p, alsoLabel(p))
		}
	}
	return b
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
