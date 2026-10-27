package diag

import (
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
)

// Op is the thema operation an error came from. It decides which error shapes
// are expected and whose position comes first.
type Op string

const (
	OpBind      Op = "bind"
	OpValidate  Op = "validate"
	OpTranslate Op = "translate"
	OpCompile   Op = "compile"
	OpLoad      Op = "load"
	OpDecompile Op = "decompile"
	OpFormat    Op = "format"
)

// Context is what [From] knows about the operation whose error it converts.
// Every field is optional.
type Context struct {
	Op Op
	// Base is the construct the operation is about: the data being validated
	// against a schema version, the lineage being bound. Shapes refine it with
	// each error's path.
	Base Address
	// PathPrefix is the leading segments every error path carries because of
	// how the operation evaluated, not because of anything the author wrote:
	// the definition validation closes a schema in. They are stripped before a
	// path is used.
	PathPrefix []string
	// SourceValue returns the author's value at an error path: for validation,
	// the schema's value at the data path; for binding, the lineage's value at
	// the normal-form path. It gives an error CUE positioned only in thema's
	// CUE the author's position, and lets shapes describe what the author
	// declared. The path's segments are unquoted.
	SourceValue func(path []string) (cue.Value, bool)
	// IsData reports whether a filename is the data document being validated
	// or translated. Its positions come before the schema's.
	IsData func(file string) bool
	// DataPos is the position of the data document itself. It is the primary
	// when the data has no position for the field at fault, as when the field
	// is absent.
	DataPos token.Pos
	// Vocabulary renders each diagnostic's Address into the author's terms.
	// nil means [NormalForm].
	Vocabulary Vocabulary
	// AddressOf maps a normal-form error path to the construct it is in.
	AddressOf func(path []string) (Address, bool)
}

func (ctx Context) vocab() Vocabulary {
	if ctx.Vocabulary == nil {
		return NormalForm
	}
	return ctx.Vocabulary
}

// path returns an error's path as the author sees it: unquoted, without the
// operation's PathPrefix.
func (ctx Context) path(e cerrors.Error) []string {
	p := unquotePath(e.Path())
	if len(ctx.PathPrefix) > 0 && len(p) >= len(ctx.PathPrefix) {
		match := true
		for i, s := range ctx.PathPrefix {
			if p[i] != s {
				match = false
				break
			}
		}
		if match {
			p = p[len(ctx.PathPrefix):]
		}
	}
	return p
}

// From converts err into diagnostics. Every [Error] in err contributes its
// diagnostic; every other cue/errors entry is matched against the shapes thema
// recognizes, and what matches nothing becomes a generic diagnostic carrying
// CUE's text verbatim. Nothing in err is dropped.
func From(err error, ctx Context) []Diagnostic {
	errs := convert(err, ctx)
	if len(errs) == 0 {
		return nil
	}
	out := make([]Diagnostic, len(errs))
	for i, e := range errs {
		out[i] = e.d
	}
	return out
}

// Convert is [From] as an error: a cue/errors list of one [Error] per
// diagnostic, or nil when err is nil. Operations return it so that callers
// get diagnostics through [Diagnostics] and sentinels through errors.Is.
func Convert(err error, ctx Context) error {
	errs := convert(err, ctx)
	if len(errs) == 0 {
		return nil
	}
	var out cerrors.Error
	for _, e := range errs {
		out = cerrors.Append(out, e)
	}
	return out
}

func convert(err error, ctx Context) []*Error {
	if err == nil {
		return nil
	}
	entries := cerrors.Errors(err)
	var out []*Error
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if de, ok := e.(*Error); ok {
			out = append(out, ctx.finish(de))
			continue
		}
		if n, ok := disjunctionHeader(e); ok && i+n < len(entries) {
			out = append(out, ctx.finish(ctx.disjunction(e, entries[i+1:i+1+n])))
			i += n
			continue
		}
		if b, ok := ctx.recognize(e); ok {
			out = append(out, ctx.finish(b.Err()))
			continue
		}
		out = append(out, ctx.finish(ctx.generic(e).Err()))
	}
	return out
}

// finish renders the Path from the Address in the operation's vocabulary.
func (ctx Context) finish(e *Error) *Error {
	if e.d.Address != nil {
		e.d.Path = ctx.vocab().Path(*e.d.Address)
	}
	return e
}

// generic builds the diagnostic for an entry no shape describes.
func (ctx Context) generic(e cerrors.Error) *B {
	code := E0902
	if isPromoted(e) {
		code = E0903
	}
	b := New(code).Detail(ctx.cueText(e))
	if ctx.AddressOf != nil {
		if a, ok := ctx.AddressOf(ctx.path(e)); ok {
			b.Addr(a)
		}
	}
	ctx.place(b, ctx.path(e), positionsOf(e), "")
	return b
}

// disjunction folds the positionless "K errors in empty disjunction" header
// and its K arms into one diagnostic: the arms are notes, each at its own
// position with its own text. A shape for the operation may describe it;
// otherwise it is generic.
func (ctx Context) disjunction(header cerrors.Error, arms []cerrors.Error) *Error {
	b, ok := ctx.recognizeDisjunction(header, arms)
	if !ok {
		b = New(E0902).Detail(ctx.cueText(header))
		if ctx.AddressOf != nil {
			if a, ok := ctx.AddressOf(ctx.path(header)); ok {
				b.Addr(a)
			}
		}
	}
	var ps []token.Pos
	for _, arm := range arms {
		armps := positionsOf(arm)
		if len(armps) == 0 {
			b.Note(token.NoPos, ctx.cueText(arm))
			continue
		}
		b.Note(armps[0], ctx.cueText(arm))
		ps = append(ps, armps...)
	}
	ctx.place(b, ctx.path(header), ps, "also here")
	return b.Err()
}

// place chooses b's primary position from ps, per the operation: the data's
// position first when validating or translating, else the first position in
// the author's files; failing both, the author's value along path. Every
// other position becomes a note, unless already noted. A builder that found
// the author's position for what thema's CUE says (hideKernel) drops thema's
// own positions instead of noting them.
func (ctx Context) place(b *B, path []string, ps []token.Pos, label string) {
	ps = dedup(ps)
	noted := make(map[token.Pos]bool)
	for _, p := range b.e.notePos {
		noted[p] = true
	}
	primary := token.NoPos
	if b.e.primary.IsValid() {
		primary = b.e.primary
	}
	if !primary.IsValid() && ctx.IsData != nil {
		for _, p := range ps {
			if OriginOf(p.Filename()) == OriginUser && ctx.IsData(p.Filename()) {
				primary = p
				break
			}
		}
		if !primary.IsValid() && ctx.DataPos.IsValid() {
			primary = ctx.DataPos
		}
	}
	if !primary.IsValid() {
		for _, p := range ps {
			if OriginOf(p.Filename()) == OriginUser {
				primary = p
				break
			}
		}
	}
	if !primary.IsValid() && ctx.SourceValue != nil {
		if p, depth, ok := ctx.authorPosAlong(path); ok {
			primary = p
			if depth < len(path) {
				b.Note(p, "reported at the enclosing value")
				noted[p] = true
			}
		}
	}
	if primary.IsValid() {
		b.At(primary)
	}
	for _, p := range ps {
		if p == primary || noted[p] {
			continue
		}
		kernel := OriginOf(p.Filename()) == OriginThema
		data := ctx.IsData != nil && ctx.IsData(p.Filename())
		if b.declareWith != "" && kernel {
			// The shape knows what thema's CUE says here; its position adds nothing.
			continue
		}
		if !b.declared && b.declareWith != "" && !data {
			b.Note(p, b.declareWith)
			b.declared = true
			continue
		}
		if b.declared && !data {
			continue
		}
		l := label
		if l == "" || kernel {
			l = alsoLabel(p)
		}
		b.Note(p, l)
	}
}

// authorPosAlong walks path from its full length down, returning the author's
// position of the first value that has one, and the depth it was found at.
func (ctx Context) authorPosAlong(path []string) (token.Pos, int, bool) {
	for i := len(path); i >= 0; i-- {
		v, ok := ctx.SourceValue(path[:i])
		if !ok {
			continue
		}
		if p, ok := AuthorPos(v); ok {
			return p, i, true
		}
	}
	return token.NoPos, 0, false
}

// positionsOf returns an entry's positions, its own first, without
// duplicates or invalid positions.
func positionsOf(e cerrors.Error) []token.Pos {
	return dedup(append([]token.Pos{e.Position()}, e.InputPositions()...))
}

// disjunctionHeader recognizes "K errors in empty disjunction:" and returns K.
// The header has no position; its arms follow it as separate entries.
func disjunctionHeader(e cerrors.Error) (int, bool) {
	format, args := e.Msg()
	if format != msgEmptyDisjunction || len(args) != 1 {
		return 0, false
	}
	switch n := args[0].(type) {
	case int:
		return n, n > 0
	case string:
		k, err := strconv.Atoi(n)
		return k, err == nil && k > 0
	}
	return 0, false
}

// isPromoted recognizes an entry cue/errors.Promote made from a plain Go
// error: a positionless message with the empty format argument.
func isPromoted(e cerrors.Error) bool {
	if e.Position().IsValid() || len(e.InputPositions()) > 0 || len(e.Path()) > 0 {
		return false
	}
	format, args := e.Msg()
	return format == "%s" && len(args) == 1 && args[0] == ""
}

// unquotePath strips the quotes cue/errors puts around string selectors.
func unquotePath(path []string) []string {
	out := make([]string, len(path))
	for i, s := range path {
		if u, err := strconv.Unquote(s); err == nil {
			s = u
		}
		out[i] = s
	}
	return out
}

// joinPath renders an unquoted path for a message.
func joinPath(path []string) string {
	return strings.Join(unquotePath(path), ".")
}

// cueText is e's message verbatim, except that the path CUE prefixes it with
// is written as the author sees it: unquoted, and without the operation's
// PathPrefix, which is not CUE's wording but thema's evaluation showing.
func (ctx Context) cueText(e cerrors.Error) string {
	text := e.Error()
	if len(e.Path()) == 0 {
		return text
	}
	raw := strings.Join(e.Path(), ".") + ": "
	rest, ok := strings.CutPrefix(text, raw)
	if !ok {
		return text
	}
	if p := ctx.path(e); len(p) > 0 {
		return joinPath(p) + ": " + rest
	}
	return rest
}
