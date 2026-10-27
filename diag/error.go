package diag

import (
	"fmt"
	"strings"

	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
)

// Error is a Diagnostic as a Go error. It is one diagnostic, so that
// cue/errors.Errors over a list of them yields one Error each, and it
// satisfies cue/errors.Error so that cue/errors.Append, Sanitize and Details
// keep working over a mix of thema's errors and CUE's.
//
// Error unwraps to the sentinels of its Code (see [CodeInfo.Sentinels]), so
// that errors.Is against thema/errors keeps working. It does not unwrap to the
// CUE error it was built from, because cue/errors.Errors would then find that
// error's list in place of this one; the cause is available from [Error.Cause].
type Error struct {
	d     Diagnostic
	cause error
	// notePos is the CUE position of each note, in order, for InputPositions.
	notePos []token.Pos
	primary token.Pos
}

var _ cerrors.Error = (*Error)(nil)

// Diagnostic returns the diagnostic.
func (e *Error) Diagnostic() Diagnostic { return e.d }

// Cause returns the CUE error this diagnostic was built from, if any.
func (e *Error) Cause() error { return e.cause }

// Error renders the headline and the primary position, compactly. Tools
// render the full form with [Render].
func (e *Error) Error() string {
	if e.d.Primary == nil {
		return e.d.Headline
	}
	return fmt.Sprintf("%s\n    %s", e.d.Headline, e.d.Primary)
}

// Unwrap returns the sentinels of the diagnostic's code.
func (e *Error) Unwrap() []error {
	info, ok := Lookup(e.d.Code)
	if !ok {
		return nil
	}
	return info.Sentinels
}

// Position implements cue/errors.Error: the primary position.
func (e *Error) Position() token.Pos { return e.primary }

// InputPositions implements cue/errors.Error: the notes' positions.
func (e *Error) InputPositions() []token.Pos { return e.notePos }

// Path implements cue/errors.Error.
func (e *Error) Path() []string { return e.d.Path }

// Msg implements cue/errors.Error.
func (e *Error) Msg() (string, []any) { return "%s", []any{e.d.Headline} }

// Join combines errors into one, as a cue/errors list. It returns nil when
// every argument is nil.
func Join(errs ...error) error {
	var out cerrors.Error
	for _, err := range errs {
		if err == nil {
			continue
		}
		for _, e := range cerrors.Errors(err) {
			out = cerrors.Append(out, e)
		}
	}
	if out == nil {
		return nil
	}
	return out
}

// Diagnostics returns the diagnostics carried by err: those of every [Error]
// in it, in order. Errors that are not an [Error] are not converted; use
// [From] for that.
func Diagnostics(err error) []Diagnostic {
	var out []Diagnostic
	for _, e := range cerrors.Errors(err) {
		if de, ok := e.(*Error); ok {
			out = append(out, de.d)
		}
	}
	return out
}

// firstLine returns s up to its first newline.
func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
