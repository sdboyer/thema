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
	// SourceValue returns the author's value at a normal-form error path, such
	// as [lenses "0.0->1.0" result count], so that an error CUE positioned only
	// in thema's CUE can be given the author's position. The path's segments
	// are unquoted.
	SourceValue func(path []string) (cue.Value, bool)
	// IsData reports whether a filename is the data document being validated
	// or translated. Its positions come before the schema's.
	IsData func(file string) bool
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

// From converts err into diagnostics. Every [Error] in err contributes its
// diagnostic; every other cue/errors entry is matched against the shapes thema
// recognizes, and what matches nothing becomes a generic diagnostic carrying
// CUE's text verbatim. Nothing in err is dropped.
func From(err error, ctx Context) []Diagnostic {
	if err == nil {
		return nil
	}
	entries := cerrors.Errors(err)
	var out []Diagnostic
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if de, ok := e.(*Error); ok {
			out = append(out, ctx.finish(de.d))
			continue
		}
		if n, ok := disjunctionHeader(e); ok && i+n < len(entries) {
			out = append(out, ctx.disjunction(e, entries[i+1:i+1+n]))
			i += n
			continue
		}
		if d, ok := ctx.recognize(e); ok {
			out = append(out, ctx.finish(d))
			continue
		}
		out = append(out, ctx.finish(ctx.generic(e)))
	}
	return out
}

// finish renders the Path from the Address in the operation's vocabulary.
func (ctx Context) finish(d Diagnostic) Diagnostic {
	if d.Address != nil {
		d.Path = ctx.vocab().Path(*d.Address)
	}
	return d
}

// generic builds the diagnostic for an entry no shape describes.
func (ctx Context) generic(e cerrors.Error) Diagnostic {
	code := E0902
	if isPromoted(e) {
		code = E0903
	}
	d := Diagnostic{Code: code, Severity: SeverityError, Detail: e.Error()}
	d.Headline, _ = title(code)
	if ctx.AddressOf != nil {
		if a, ok := ctx.AddressOf(unquotePath(e.Path())); ok {
			d.Address = &a
		}
	}
	ctx.place(&d, e.Path(), positionsOf(e), "")
	return d
}

// disjunction folds the positionless "K errors in empty disjunction" header
// and its K arms into one diagnostic: the arms are notes, each at its own
// position with its own text.
func (ctx Context) disjunction(header cerrors.Error, arms []cerrors.Error) Diagnostic {
	d := Diagnostic{Code: E0902, Severity: SeverityError, Detail: header.Error()}
	d.Headline, _ = title(E0902)
	if ctx.AddressOf != nil {
		if a, ok := ctx.AddressOf(unquotePath(header.Path())); ok {
			d.Address = &a
		}
	}
	var ps []token.Pos
	for _, arm := range arms {
		armps := positionsOf(arm)
		if len(armps) == 0 {
			d.Notes = append(d.Notes, Note{Label: arm.Error()})
			continue
		}
		d.Notes = append(d.Notes, Note{Pos: PosOf(armps[0]), Label: arm.Error()})
		ps = append(ps, armps...)
	}
	ctx.place(&d, header.Path(), ps, "also here")
	return ctx.finish(d)
}

// place chooses d's primary position from ps, per the operation: the data's
// position first when validating or translating, else the first position in
// the author's files; failing both, the author's value along path. Every
// other position becomes a note. Positions already noted are not noted again.
func (ctx Context) place(d *Diagnostic, path []string, ps []token.Pos, label string) {
	ps = dedup(ps)
	noted := make(map[token.Pos]bool)
	for _, n := range d.Notes {
		if n.Pos != nil {
			for _, p := range ps {
				if PosOf(p).String() == n.Pos.String() {
					noted[p] = true
				}
			}
		}
	}
	primary := token.NoPos
	if ctx.IsData != nil {
		for _, p := range ps {
			if OriginOf(p.Filename()) == OriginUser && ctx.IsData(p.Filename()) {
				primary = p
				break
			}
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
		if p, depth, ok := ctx.authorPosAlong(unquotePath(path)); ok {
			primary = p
			if depth < len(path) {
				d.Notes = append(d.Notes, Note{Pos: PosOf(p), Label: "reported at the enclosing value"})
				noted[p] = true
			}
		}
	}
	if primary.IsValid() {
		d.Primary = PosOf(primary)
	}
	for _, p := range ps {
		if p == primary || noted[p] {
			continue
		}
		l := label
		if l == "" {
			l = alsoLabel(p)
		} else if OriginOf(p.Filename()) == OriginThema {
			l = alsoLabel(p)
		}
		d.Notes = append(d.Notes, Note{Pos: PosOf(p), Label: l})
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
	if !strings.Contains(format, "errors in empty disjunction") || len(args) != 1 {
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

func title(c Code) (string, bool) {
	info, ok := Lookup(c)
	return info.Title, ok
}
