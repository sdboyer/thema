package thema

import (
	"fmt"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/lenscheck"
	"github.com/sdboyer/thema/internal/rulecodes"
	"github.com/sdboyer/thema/internal/util"
)

// This file is where thema's Go knows the normal form's layout for the sake of
// diagnostics: which construct a normal-form path names, where the author's
// value for it is, and which code each rule of the compatibility and totality
// checkers reports under. The checkers' rules are stable identifiers of their
// own; the maps here are kept in lockstep with them by tests.

// vocab is the vocabulary diagnostics about this lineage are rendered in.
func (ml *maybeLineage) vocab() diag.Vocabulary {
	if ml.cfg != nil && ml.cfg.vocab != nil {
		return ml.cfg.vocab
	}
	return diag.NormalForm
}

// term names a construct of the lineage in the author's vocabulary.
func (ml *maybeLineage) term(a diag.Address) string {
	return ml.vocab().Term(a)
}

// diag starts a diagnostic about this lineage.
func (ml *maybeLineage) diag(code diag.Code) *diag.B {
	return diag.New(code)
}

// bindContext is what diag needs to convert CUE's own errors about this
// lineage: the author's value at any normal-form path, and the construct the
// path names.
func (ml *maybeLineage) bindContext() diag.Context {
	return diag.Context{
		Op:         diag.OpBind,
		Base:       diag.Address{Kind: diag.AddrLineage},
		Vocabulary: ml.vocab(),
		// Errors from the lineage unified with thema.Lineage carry the
		// kernel's root; the author's paths start below it.
		PathPrefix: []string{"Lineage"},
		SourceValue: func(path []string) (cue.Value, bool) {
			v := ml.uni.LookupPath(normalFormPath(path))
			return v, v.Exists()
		},
		AddressOf: addressOfNormalForm,
	}
}

// convert turns an error from CUE about this lineage into diagnostics; an
// error no shape describes is reported under generic.
func (ml *maybeLineage) convert(err error, generic diag.Code) error {
	ctx := ml.bindContext()
	ctx.Generic = generic
	return diag.Convert(err, ctx)
}

// addrOf says which construct of the lineage v is, by its path relative to
// the lineage.
func (ml *maybeLineage) addrOf(v cue.Value) (diag.Address, bool) {
	p := v.Path()
	if root := ml.raw.Path(); cuetil.PathHasPrefix(p, root) {
		p = cuetil.TrimPathPrefix(p, root)
	}
	return addressOfNormalForm(pathSegments(p))
}

// relPath renders a path into the lineage relative to the lineage, whichever
// of its values (raw, or unified with thema.Lineage) the path is rooted in.
func (ml *maybeLineage) relPath(p cue.Path) string {
	for _, root := range []cue.Path{ml.uni.Path(), ml.raw.Path()} {
		if len(root.Selectors()) > 0 && cuetil.PathHasPrefix(p, root) {
			return cuetil.TrimPathPrefix(p, root).String()
		}
	}
	return p.String()
}

// at starts a diagnostic positioned at the author's text for v, and about the
// construct v is.
func (ml *maybeLineage) at(code diag.Code, v cue.Value) *diag.B {
	b := ml.diag(code).AtValue(v)
	if a, ok := ml.addrOf(v); ok {
		b.Addr(a)
	}
	return b
}

// normalFormPath is the CUE path of a normal-form error path: every segment is
// a string label, except an index under lacunas.
func normalFormPath(path []string) cue.Path {
	sels := make([]cue.Selector, 0, len(path))
	for i, s := range path {
		if i > 0 && path[i-1] == "lacunas" {
			if n, err := strconv.Atoi(s); err == nil {
				sels = append(sels, cue.Index(n))
				continue
			}
		}
		sels = append(sels, cue.Str(s))
	}
	return cue.MakePath(sels...)
}

// addressOfNormalForm says which construct a path into the normal form names.
// The path is relative to the lineage and unquoted; a leading "Lineage" from
// the kernel's unification is ignored.
func addressOfNormalForm(path []string) (diag.Address, bool) {
	if len(path) > 0 && path[0] == "Lineage" {
		path = path[1:]
	}
	if len(path) == 0 {
		return diag.Address{Kind: diag.AddrLineage}, true
	}
	switch path[0] {
	case "name":
		return diag.Address{Kind: diag.AddrName}, true
	case "joinSchema":
		return diag.Address{Kind: diag.AddrJoinSchema}, true
	case "schemas":
		if len(path) < 3 {
			return diag.Address{Kind: diag.AddrLineage}, true
		}
		maj, okM := parseVersionKey(path[1])
		min, okm := parseVersionKey(path[2])
		if !okM || !okm {
			return diag.Address{}, false
		}
		v := diag.Version{maj, min}
		rest := path[3:]
		if len(rest) == 0 {
			return diag.Address{Kind: diag.AddrSchema, Version: v}, true
		}
		switch rest[0] {
		case "schema":
			if len(rest) == 1 {
				return diag.Address{Kind: diag.AddrSchema, Version: v}, true
			}
			return diag.Address{Kind: diag.AddrSchemaField, Version: v, Field: rest[1:]}, true
		case "examples":
			name := ""
			if len(rest) > 1 {
				name = rest[1]
			}
			return diag.Address{Kind: diag.AddrExample, Version: v, Name: name, Field: rest[min2(2, len(rest)):]}, true
		}
		return diag.Address{Kind: diag.AddrSchema, Version: v}, true
	case "lenses":
		if len(path) < 2 {
			return diag.Address{Kind: diag.AddrLineage}, true
		}
		id, ok := parseLensEdge(path[1])
		if !ok {
			return diag.Address{}, false
		}
		a := diag.Address{Kind: diag.AddrLens, From: diag.Version(id.From), To: diag.Version(id.To)}
		rest := path[2:]
		if len(rest) == 0 {
			return a, true
		}
		switch rest[0] {
		case lensParam(id.From):
			a.Kind = diag.AddrLensInput
		case lensParam(id.To):
			a.Kind = diag.AddrLensResult
			if len(rest) > 1 {
				a.Kind, a.Field = diag.AddrLensResultField, rest[1:]
			}
		case "lacunas":
			a.Kind = diag.AddrLacuna
			if len(rest) > 1 {
				a.Index, _ = strconv.Atoi(rest[1])
			}
			if len(rest) > 2 {
				a.Kind, a.Name = diag.AddrLacunaField, rest[2]
			}
		}
		return a, true
	}
	return diag.Address{}, false
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// compatDiag describes one finding of the compatibility checker between prev
// and curr, which were declared as a minor step. The headline is the finding's
// own sentence about the field; the help says what makes it compatible, or
// that it is a major version.
func (ml *maybeLineage) compatDiag(f compat.Finding, prev, curr *schemaDef) *diag.Error {
	code := rulecodes.CompatCode(f.Rule)
	schema := schemaAddr(curr.v)
	prior := schemaAddr(prev.v)
	subject := ml.term(schema)
	addr := schema
	if sels := f.Path.Selectors(); len(sels) > 0 {
		addr = diag.Address{Kind: diag.AddrSchemaField, Version: diag.Version(curr.v), Field: pathSegments(f.Path)}
		subject = ml.term(addr)
	}
	b := ml.diag(code).Addr(addr)
	statement(b, subject+" "+diag.RewriteFor(ml.vocab(), addr, f.Detail), subject+" is not backward compatible with "+prev.v.String())
	if f.Pos.IsValid() {
		b.At(f.Pos)
	} else {
		// A finding about the whole struct has no field to point at.
		b.AtValue(curr.ref.LookupPath(pathSch))
	}
	b.Notef(token.NoPos, "%s is declared as a minor version of %s, so every instance of %s must be an instance of it (compatibility rule %s)", ml.term(schema), prev.v, ml.term(prior), f.Rule)
	if f.ViaResidual {
		b.Notef(token.NoPos, "%s does not declare %s by name, but accepts it through ... or a pattern, so an instance may already hold any value there", ml.term(prior), f.Path)
		b.Helpf("declare it as %s?: _ to accept what %s accepted, or make this version %d.0", f.Path, prev.v, prev.v[0]+1)
	} else if f.Verdict == compat.Unknown {
		b.Help("change the schema so the checker can decide, or make this version " + synv(prev.v[0]+1, 0).String())
	} else {
		b.Help("make the change compatible, or make this version " + synv(prev.v[0]+1, 0).String())
	}
	return b.Err()
}

// statement puts a checker's sentence in the headline when it fits, and
// otherwise makes it a note under a shorter headline.
func statement(b *diag.B, sentence, short string) {
	if len(sentence) <= diag.MaxHeadline {
		b.Headline(sentence)
		return
	}
	b.Headline(short).Note(token.NoPos, sentence)
}

// totalityDiag describes one finding of the lens totality checker for the lens
// from -> to.
func (ml *maybeLineage) totalityDiag(f lenscheck.Finding, from, to SyntacticVersion) *diag.Error {
	code := rulecodes.LenscheckCode(f.Rule)
	addr := lensAddress(f.Path, from, to)
	b := ml.diag(code).Addr(addr).At(f.Pos)
	statement(b, ml.term(addr)+" "+diag.RewriteFor(ml.vocab(), addr, f.Detail), ml.term(addr)+" is not total for some instance")
	b.Notef(token.NoPos, "a lens must be total: every instance of %s must translate to an instance of %s (totality rule %s)", ml.term(schemaAddr(from)), ml.term(schemaAddr(to)), f.Rule)
	if f.Verdict == lenscheck.Unknown {
		b.Help("write the mapping in a form the checker can follow, or add a lacuna where it cannot decide")
	}
	return b.Err()
}

// lensAddress parses a totality finding's path within a lens: "v1_0.a.b",
// "lacunas[0].condition", "v0_0".
func lensAddress(path string, from, to SyntacticVersion) diag.Address {
	a := diag.Address{Kind: diag.AddrLens, From: diag.Version(from), To: diag.Version(to)}
	head, rest, _ := strings.Cut(path, ".")
	switch {
	case head == lensParam(to):
		a.Kind = diag.AddrLensResult
		if rest != "" {
			a.Kind, a.Field = diag.AddrLensResultField, strings.Split(rest, ".")
		}
	case head == lensParam(from):
		a.Kind = diag.AddrLensInput
	case strings.HasPrefix(head, "lacunas["):
		a.Kind = diag.AddrLacuna
		idx := strings.TrimSuffix(strings.TrimPrefix(head, "lacunas["), "]")
		a.Index, _ = strconv.Atoi(idx)
		if rest != "" {
			a.Kind, a.Name = diag.AddrLacunaField, rest
		}
	}
	return a
}

// translateContext is what diag needs to describe a failure of thema's CUE
// Translate over out, its output: the error paths run through the kernel's
// accumulator, out.steps._accum[N].result.<p>, and the author's value for
// such a path is step N's lens result at <p>, which the lineage author wrote.
func (lin *baseLineage) translateContext(out cue.Value) diag.Context {
	steps := out.LookupPath(cue.MakePath(cue.Str("steps")))
	// When a step fails, steps itself is an error (list.Drop over the
	// accumulator fails), so the accumulator is read directly.
	accum := steps.LookupPath(cue.MakePath(cue.Hid("_accum", util.ThemaPackageID)))
	lensPath := cue.MakePath(cue.Hid("_lens", util.ThemaPackageID))

	// step returns the translation step an error path names, as the kernel
	// evaluated it, and the field path below its result: steps.N is step N,
	// and steps._accum.N is accumulator entry N, which is step N-1.
	step := func(path []string) (cue.Value, []string, bool) {
		for i, s := range path {
			if s != "steps" || i+1 >= len(path) {
				continue
			}
			var st cue.Value
			var rest []string
			if path[i+1] == "_accum" && i+2 < len(path) {
				n, err := strconv.Atoi(path[i+2])
				if err != nil || n < 1 {
					return cue.Value{}, nil, false
				}
				st, rest = accum.LookupPath(cue.MakePath(cue.Index(n))), path[i+3:]
			} else {
				n, err := strconv.Atoi(path[i+1])
				if err != nil {
					return cue.Value{}, nil, false
				}
				st, rest = steps.LookupPath(cue.MakePath(cue.Index(n))), path[i+2:]
			}
			if !st.Exists() {
				return cue.Value{}, nil, false
			}
			if len(rest) > 0 && rest[0] == "result" {
				rest = rest[1:]
			}
			return st, rest, true
		}
		return cue.Value{}, nil, false
	}
	edge := func(st cue.Value) (from, to SyntacticVersion, ok bool) {
		if st.LookupPath(cue.MakePath(cue.Str("from"))).Decode(&from) != nil {
			return from, to, false
		}
		if st.LookupPath(cue.MakePath(cue.Str("to"))).Decode(&to) != nil {
			return from, to, false
		}
		return from, to, true
	}

	return diag.Context{
		Op:           diag.OpTranslate,
		Vocabulary:   lin.vocab,
		Generic:      diag.E0308,
		PreferSource: true,
		SourceValue: func(path []string) (cue.Value, bool) {
			st, field, ok := step(path)
			if !ok {
				return cue.Value{}, false
			}
			lens := st.LookupPath(lensPath)
			if !lens.Exists() {
				// A minor step has no lens; its result is the prior one.
				return cue.Value{}, false
			}
			from, to, ok := edge(st)
			if !ok {
				return cue.Value{}, false
			}
			v := lens.LookupPath(cue.MakePath(cue.Str(lensParam(to)))).LookupPath(fieldPath(field))
			if !v.Exists() {
				return cue.Value{}, false
			}
			// The lens as thema evaluated it has the kernel's conjuncts; the
			// author's are on the lineage's own copy of the lens.
			own := lin.uni.LookupPath(cue.MakePath(cue.Str("lenses"), cue.Str(lid(from, to).String()), cue.Str(lensParam(to)))).LookupPath(fieldPath(field))
			if own.Exists() {
				return own, true
			}
			return v, true
		},
		AddressOf: func(path []string) (diag.Address, bool) {
			st, field, ok := step(path)
			if !ok {
				return diag.Address{}, false
			}
			from, to, ok := edge(st)
			if !ok {
				return diag.Address{}, false
			}
			a := diag.Address{Kind: diag.AddrLensResult, From: diag.Version(from), To: diag.Version(to)}
			if len(field) > 0 {
				a.Kind, a.Field = diag.AddrLensResultField, field
			}
			return a, true
		},
	}
}

// pathSegments renders a CUE path's selectors as segments: string labels
// unquoted, anything else (an index, a pattern) as CUE writes it.
func pathSegments(p cue.Path) []string {
	sels := p.Selectors()
	out := make([]string, len(sels))
	for i, s := range sels {
		if s.IsString() {
			out[i] = s.Unquoted()
		} else {
			out[i] = s.String()
		}
	}
	return out
}

// refCodes is the code of each reference rule (docs/invariants.md,
// References). Each rule has one code; TestRefCodes keeps the two in lockstep.
var refCodes = map[refRule]diag.Code{
	refSiblingSchema:  diag.E0140,
	refLineageMeta:    diag.E0141,
	refOutsideLineage: diag.E0142,
	refImport:         diag.E0143,
	refLensToSchema:   diag.E0144,
	refLensToLens:     diag.E0145,
	refUnresolved:     diag.E0146,
}

// refDiag describes a reference that breaks a rule, at the field holding it.
func (ml *maybeLineage) refDiag(o refOwner, f refFinding) error {
	code, ok := refCodes[f.rule]
	if !ok {
		return diag.InternalErr(fmt.Errorf("reference rule %s has no code", f.rule))
	}
	subject := ml.term(o.addr)
	if len(f.field) > 0 {
		subject = strings.Join(f.field, ".") + " in " + subject
	}
	b := ml.diag(code).At(refPos(o, f.field)).Addr(o.addr)
	sentence := fmt.Sprintf("%s refers to %s, %s", subject, f.expr, f.what(o))
	if len(sentence) <= diag.MaxHeadline {
		b.Headline(sentence)
	} else {
		b.Headlinef("%s refers to %s", subject, f.expr).Note(token.NoPos, sentence)
	}
	return b.Notef(token.NoPos, "reference rule %s", f.rule).Help(f.help(o)).Err()
}
