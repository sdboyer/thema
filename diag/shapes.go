package diag

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
)

// The message formats of the CUE evaluator thema recognizes, as the menagerie
// (diag/testdata/menagerie.txtar) records them. A CUE upgrade that changes
// one fails the menagerie's digest before it can silently stop matching here.
const (
	msgConflictKinds    = "conflicting values %s and %s (mismatched types %s and %s)"
	msgConflict         = "conflicting values %s and %s"
	msgNotAllowed       = "field not allowed"
	msgRequiredAbsent   = "field is required but not present"
	msgIncomplete       = "incomplete value %v"
	msgOutOfBound       = "invalid value %v (out of bound %s)"
	msgNotSatisfied     = "invalid value %s (does not satisfy %s)"
	msgEmptyDisjunction = "%d errors in empty disjunction:"
)

// A shape recognizes one kind of error the CUE evaluator produces, by the
// format string of its message and the operation it arose in, and says how
// thema describes it.
type shape struct {
	// name identifies the shape in tests and in docs/cue-errors.md.
	name string
	// msg is the format string of the CUE message this shape matches.
	msg string
	// kernel marks a message thema's own CUE produces with error(), rather
	// than one of the evaluator's. The menagerie does not record these; the
	// lockstep test against thema's CUE files does.
	kernel bool
	// ops are the operations the shape applies to.
	ops []Op
	// build describes e. It sets the code, headline, address and whatever
	// notes and help it can; [Context.place] then chooses the positions.
	build func(e cerrors.Error, ctx Context) *B
}

// shapes is the table, in matching order. An entry no shape matches is
// reported with [E0902].
var shapes = []shape{
	{name: "validate-required-absent", msg: msgRequiredAbsent, ops: []Op{OpValidate}, build: validateAbsent},
	{name: "validate-incomplete", msg: msgIncomplete, ops: []Op{OpValidate}, build: validateIncomplete},
	{name: "validate-not-allowed", msg: msgNotAllowed, ops: []Op{OpValidate}, build: validateNotAllowed},
	{name: "validate-kind", msg: msgConflictKinds, ops: []Op{OpValidate}, build: validateKind},
	{name: "validate-conflict", msg: msgConflict, ops: []Op{OpValidate}, build: validateConflict},
	{name: "validate-bound", msg: msgOutOfBound, ops: []Op{OpValidate}, build: validateBound},
	{name: "validate-constraint", msg: msgNotSatisfied, ops: []Op{OpValidate}, build: validateConstraint},
}

// disjunctionShapes describe a folded empty disjunction, by operation.
var disjunctionShapes = map[Op]func(header cerrors.Error, arms []cerrors.Error, ctx Context) *B{
	OpValidate: validateDisjunction,
}

func (s shape) matches(e cerrors.Error, ctx Context) bool {
	format, _ := e.Msg()
	if format != s.msg {
		return false
	}
	for _, op := range s.ops {
		if op == ctx.Op {
			return true
		}
	}
	return false
}

// recognize matches e against the shapes.
func (ctx Context) recognize(e cerrors.Error) (*B, bool) {
	for _, s := range shapes {
		if s.matches(e, ctx) {
			b := s.build(e, ctx)
			if b == nil {
				// The shape looked closer and declined.
				continue
			}
			if b.e.d.Detail == "" {
				b.Detail(ctx.cueText(e))
			}
			ctx.place(b, ctx.path(e), positionsOf(e), "")
			return b, true
		}
	}
	return nil, false
}

func (ctx Context) recognizeDisjunction(header cerrors.Error, arms []cerrors.Error) (*B, bool) {
	build, ok := disjunctionShapes[ctx.Op]
	if !ok {
		return nil, false
	}
	b := build(header, arms, ctx)
	if b == nil {
		return nil, false
	}
	if b.e.d.Detail == "" {
		b.Detail(ctx.cueText(header))
	}
	return b, true
}

// Helpers shapes share.

// argText renders a CUE message argument as CUE would.
func argText(a any) string {
	switch x := a.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(a)
}

// dataSide says which of a two-value conflict's arguments is the data's: the
// one that is the data's value at the error's path. CUE orders the arguments
// by their positions, so the order says nothing about which is the data: data
// compiled from a string or built in Go sorts differently from data read from
// a file. In a conflict of kinds, CUE reports the values' kinds after them,
// and the data's is the argument of its kind; in a conflict of values of one
// kind, it is the argument equal to it, compared as values, since CUE's text
// for a value can differ from the data's. ok is false when the data does not
// settle it.
func dataSide(e cerrors.Error, ctx Context) (side int, ok bool) {
	if ctx.DataValue == nil {
		return 0, false
	}
	dv, ok := ctx.DataValue(ctx.path(e))
	_, args := e.Msg()
	if !ok || len(args) < 2 {
		return 0, false
	}
	values, kinds := args[:2], args[2:]
	is := func(i int) bool {
		if len(kinds) > i {
			k, ok := kinds[i].(cue.Kind)
			return ok && k == dv.IncompleteKind()
		}
		v := cuecontext.New().CompileString(argText(values[i]))
		return v.Err() == nil && v.Equals(dv)
	}
	switch first, second := is(0), is(1); {
	case first && !second:
		return 0, true
	case second && !first:
		return 1, true
	}
	return 0, false
}

// schemaNote adds a note at the author's declaration of path in the schema,
// found through ctx.SourceValue, and tells the builder thema's own positions
// for the same declaration are not worth showing. When the schema has no field
// at path, as under a pattern constraint, the first position CUE reports
// outside the data stands for the declaration instead. It reports whether the
// declaration was found directly.
func schemaNote(b *B, ctx Context, path []string, label string) bool {
	b.declareWith = label
	if ctx.SourceValue == nil {
		return false
	}
	v, ok := ctx.SourceValue(unquotePath(path))
	if !ok {
		return false
	}
	p, ok := AuthorPos(v)
	if !ok {
		return false
	}
	b.Note(p, label)
	b.declared = true
	return true
}

// declaredFields lists the field names the schema declares at the parent of
// path, for suggesting the one the author meant.
func declaredFields(ctx Context, path []string) []string {
	if ctx.SourceValue == nil || len(path) == 0 {
		return nil
	}
	v, ok := ctx.SourceValue(unquotePath(path[:len(path)-1]))
	if !ok {
		return nil
	}
	iter, err := v.Fields(cue.Optional(true))
	if err != nil {
		return nil
	}
	var names []string
	for iter.Next() {
		names = append(names, iter.Selector().Unquoted())
	}
	return names
}

// Suggest returns the name in candidates closest to name, if one is close
// enough to be a likely typo.
func Suggest(name string, candidates []string) (string, bool) {
	best, dist := "", len(name)/2+1
	for _, c := range candidates {
		if c == name {
			continue
		}
		if d := editDistance(strings.ToLower(name), strings.ToLower(c)); d < dist {
			best, dist = c, d
		}
	}
	return best, best != ""
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// dataAddr is the Address of a path in the data, against the schema the
// operation's Base names.
func dataAddr(ctx Context, path []string) Address {
	return Address{Kind: AddrData, Version: ctx.Base.Version, Field: unquotePath(path)}
}

// oneLine shortens a CUE value's text to fit a headline.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}

var _ = token.NoPos
