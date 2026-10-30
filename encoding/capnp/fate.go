package capnp

import (
	"fmt"
	"math/big"
	"sort"

	"cuelang.org/go/cue"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/encoding/capnp/compiled"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/lensir"
	"github.com/sdboyer/thema/lacuna"
)

// This file carries what translation reads around each step to decide the
// fate of the fields no program writes (docs/translation.md; docs/capnp.md,
// What the engine does around each step). Go's Instance.Translate defines
// that behavior (extras.go), and the artifact carries exactly what it reads
// before it sees data: per version, the structs each declares and whether
// each is open; per lens, what it reads, which result fields read each value
// and which structs it takes whole, read from the lens as Go's translation
// reads it (cuetil.LensSyntax), never derived again from the program; and the
// messages of the lacunas translation raises by itself.

// asideMessages are the templates of the lacunas translation raises by
// itself, with the text of Go's (extras.go asideMessage and lossyMessage;
// docs/reference/lacunas.md, Set-aside fields). TestAsideMessages holds them
// to what Go's translation raises.
var asideMessages = []compiled.AsideMessage{
	{Cause: compiled.AsideClosed, Form: compiled.OneField, Type: lacuna.DroppedField,
		Text: "{fields} set aside: {to} does not declare it in {struct}, which it does not leave open"},
	{Cause: compiled.AsideClosed, Form: compiled.SeveralFields, Type: lacuna.DroppedField,
		Text: "{fields} set aside: {to} does not declare them in {struct}, which it does not leave open"},
	{Cause: compiled.AsideNowhere, Form: compiled.OneField, Type: lacuna.DroppedField,
		Text: "{fields} set aside: translating from {from} to {to} produces no {struct} to carry it in"},
	{Cause: compiled.AsideNowhere, Form: compiled.SeveralFields, Type: lacuna.DroppedField,
		Text: "{fields} set aside: translating from {from} to {to} produces no {struct} to carry them in"},
	{Cause: compiled.AsideWritten, Form: compiled.OneField, Type: lacuna.DroppedField,
		Text: "{fields} set aside: the lens from {from} to {to} writes its own value for it"},
	{Cause: compiled.AsideWritten, Form: compiled.SeveralFields, Type: lacuna.DroppedField,
		Text: "{fields} set aside: the lens from {from} to {to} writes its own values for them"},
	{Cause: compiled.AsideUnwritten, Form: compiled.OneField, Type: lacuna.DroppedField,
		Text: "{fields} set aside: the lens from {from} to {to} neither maps nor writes it"},
	{Cause: compiled.AsideUnwritten, Form: compiled.SeveralFields, Type: lacuna.DroppedField,
		Text: "{fields} set aside: the lens from {from} to {to} neither maps nor writes them"},
	{Cause: compiled.AsideLost, Form: compiled.OneField, Type: lacuna.DroppedField,
		Text: "{fields} set aside: the lens from {from} to {to} reads it, but wrote nothing that carries it for this instance"},
	{Cause: compiled.AsideLost, Form: compiled.SeveralFields, Type: lacuna.DroppedField,
		Text: "{fields} set aside: the lens from {from} to {to} reads them, but wrote nothing that carries them for this instance"},
	{Cause: compiled.AsideLossy, Form: compiled.OneField, Type: lacuna.LossyFieldMapping,
		Text: "{field} not carried: its value is not an instance of what {to} declares for it"},
	{Cause: compiled.AsideLossy, Form: compiled.MovedField, Type: lacuna.LossyFieldMapping,
		Text: "{field} not carried: its value is not an instance of what {to} declares for {target}"},
}

// A shape is one struct a version declares, as the compiler walks it: its
// compiled StructShape, with its path and its value in the closed schema,
// from which a check is rendered.
type shape struct {
	compiled.StructShape
	path cue.Path
	val  cue.Value
	// alt identifies a tagged alternative of a disjunction by its tag
	// (label=value); "" for a struct that is not one.
	alt string
}

// A shapeWalk walks the structs of one version's schema.
type shapeWalk struct {
	cp *compiler
	// closed is the version's schema, closed as translation validates
	// against it.
	closed cue.Value
	out    []shape
}

// A probe builds an instance reaching a value of the schema: given what to
// put at the value, the instance that holds it there, with the tags that
// select each struct alternative on the way.
type probe func(at any) any

// shapes returns the structs version v's schema declares, root first, then
// depth-first in declaration order: through fields, list elements, map values
// and the struct alternatives of unions.
func (cp *compiler) shapes(v thema.SyntacticVersion) []shape {
	w := &shapeWalk{cp: cp, closed: cp.schemas[v]}
	w.strct(nil, cue.MakePath(), func(at any) any { return at }, cp.trees[v], cp.schemas[v], cp.c.Node(cp.gens.rootOf(v)))
	return w.out
}

// open reports whether the struct a probe reaches is open, as translation
// decides where it places a field (carry in extras.go): an instance holding
// a field the struct does not declare is still an instance of the closed
// schema. Validate without concreteness ignores what the probe leaves out,
// such as required fields. The schema's own closedness is read this way, not
// through Allows: a struct alternative of a disjunction, as compat.Branches
// returns it, reports every label allowed whether or not it is closed.
func (w *shapeWalk) open(pr probe, labels []string) bool {
	label := "probe"
	for i := 2; hasLabel(labels, label); i++ {
		label = fmt.Sprintf("probe%d", i)
	}
	inst := w.cp.ctx.Encode(pr(map[string]any{label: nil}))
	return w.closed.Unify(inst).Validate() == nil
}

func (w *shapeWalk) strct(p lensir.Path, cp2 cue.Path, pr probe, vs *vstruct, val cue.Value, node *compiled.Node) {
	w.altStruct(p, cp2, pr, "", nil, vs, val, node)
}

// altStruct records a struct, with tags holding the label and value that
// select it among the alternatives of a disjunction, if it is one, and alt
// naming it.
func (w *shapeWalk) altStruct(p lensir.Path, cp2 cue.Path, pr probe, alt string, tags map[string]any, vs *vstruct, val cue.Value, node *compiled.Node) {
	if vs == nil || node == nil || vs.role != compiled.SchemaStruct {
		return
	}
	at := func(m map[string]any) any {
		for k, v := range tags {
			m[k] = v
		}
		return pr(m)
	}
	s := shape{StructShape: compiled.StructShape{Path: p, Node: node.ID}, path: cp2, val: val, alt: alt}
	for _, f := range vs.fields {
		if f.fixed == "" {
			s.Labels = append(s.Labels, f.label)
		}
	}
	s.Open = w.open(func(v any) any { return at(v.(map[string]any)) }, s.Labels)
	w.out = append(w.out, s)
	for i := range vs.fields {
		f := &vs.fields[i]
		if f.fixed != "" {
			continue
		}
		cf := node.Field(f.label)
		if cf == nil {
			continue
		}
		label := f.label
		sel := cue.Str(label)
		w.typ(step(p, lensir.Field(label)), appendPath(cp2, sel), func(v any) any { return at(map[string]any{label: v}) },
			f.typ, lookup(val, cue.MakePath(sel)), cf.Type, cf.Union)
	}
}

// typ follows a field's type to the structs beneath it, as checkType does.
func (w *shapeWalk) typ(p lensir.Path, cp2 cue.Path, pr probe, vt *vtype, val cue.Value, ct *compiled.Type, union []*compiled.Member) {
	cp := w.cp
	if vt == nil {
		return
	}
	if vt.isUnion() {
		for i := range vt.union {
			m := &vt.union[i]
			if m.typ == nil {
				continue
			}
			for _, c := range union {
				if c.Name != m.name {
					continue
				}
				alt := cp.alternative(val, m)
				if m.role == compiled.TagMember && m.tag != nil && m.typ.strct != nil && c.Type != nil {
					w.altStruct(p, cp2, pr, m.tagLabel+"="+litKey(*m.tag), map[string]any{m.tagLabel: litValue(*m.tag)}, m.typ.strct, alt, cp.c.Node(c.Type.Node))
					continue
				}
				w.typ(p, cp2, pr, m.typ, alt, c.Type, nil)
			}
		}
		return
	}
	if ct == nil {
		for _, c := range union {
			if c.Role == compiled.Value {
				ct = c.Type
			}
		}
	}
	if ct == nil {
		return
	}
	switch vt.kind {
	case compiled.Struct:
		w.strct(p, cp2, pr, vt.strct, val, cp.c.Node(ct.Node))
	case compiled.List:
		if vt.elem == nil || ct.Elem == nil {
			return
		}
		ep := step(p, lensir.Any())
		elem := func(v any) any { return pr([]any{v}) }
		es := vt.elem.strct
		switch {
		case es != nil && es.role == compiled.MapEntry:
			entry := cp.c.Node(ct.Elem.Node)
			if entry == nil {
				return
			}
			ev := entry.Fields[1]
			w.typ(ep, appendPath(cp2, cue.AnyString), func(v any) any { return pr(map[string]any{"key": v}) },
				es.fields[1].typ, val.LookupPath(cue.MakePath(cue.AnyString)), ev.Type, ev.Union)
		case es != nil && es.role == compiled.UnionBox:
			box := cp.c.Node(ct.Elem.Node)
			if box == nil {
				return
			}
			w.typ(ep, appendPath(cp2, cue.AnyIndex), elem, es.fields[0].typ, val.LookupPath(cue.MakePath(cue.AnyIndex)), nil, box.Fields[0].Union)
		default:
			w.typ(ep, appendPath(cp2, cue.AnyIndex), elem, vt.elem, val.LookupPath(cue.MakePath(cue.AnyIndex)), ct.Elem, nil)
		}
	}
}

// litValue is a literal as Go data, for ctx.Encode.
func litValue(l lensir.Literal) any {
	switch l.Kind {
	case lensir.Bool:
		return l.Bool
	case lensir.Int:
		const decimal = 10
		n, ok := new(big.Int).SetString(l.Int, decimal)
		if !ok {
			return nil
		}
		return n
	case lensir.String:
		return l.Str
	}
	return nil
}

// step returns p with s appended, never sharing p's backing array.
func step(p lensir.Path, s lensir.Step) lensir.Path {
	return append(append(lensir.Path(nil), p...), s)
}

// fates sets each declared lens's edge's Fate, read from the lens as Go's
// translation reads it (stepLens in instance.go): what its result reads,
// which result fields read each value, and which structs it takes whole
// (cuetil.LensSyntax); and the source paths its lacunas name, which the
// program's lacunas carry as the lens lists them.
func (cp *compiler) fates() error {
	for _, e := range cp.c.Edges {
		if e.Synthesized {
			continue
		}
		f, err := cp.fate(e.Program)
		if err != nil {
			return diag.InternalErr(fmt.Errorf("capnp: lens %d.%d->%d.%d: %w", e.From[0], e.From[1], e.To[0], e.To[1], err))
		}
		e.Fate = f
	}
	return nil
}

func (cp *compiler) fate(p *lensir.Program) (*compiled.Fate, error) {
	from, to := p.Edge.From, p.Edge.To
	key := fmt.Sprintf("%d.%d->%d.%d", from[0], from[1], to[0], to[1])
	lens := cp.lin.Underlying().LookupPath(cue.MakePath(cue.Str("lenses"), cue.Str(key)))
	syn, err := cuetil.ParseLens(lens, cuetil.LensParam(from[0], from[1]), cuetil.LensParam(to[0], to[1]))
	if err != nil {
		return nil, err
	}
	f := &compiled.Fate{}
	for _, r := range syn.InputReads() {
		ip, err := irPath(r)
		if err != nil {
			return nil, err
		}
		f.Reads = append(f.Reads, ip)
	}
	for _, r := range syn.Readers() {
		pp, err := pathPair(r.Result, r.Input)
		if err != nil {
			return nil, err
		}
		f.Readers = append(f.Readers, pp)
	}
	for _, c := range syn.Copies() {
		pp, err := pathPair(c.Result, c.Input)
		if err != nil {
			return nil, err
		}
		f.Copies = append(f.Copies, pp)
	}
	// Go reads these from the lens's lacunas as evaluated for the instance,
	// where an entry exists only where its guard holds; the program carries
	// each entry with its guard. A path Go cannot parse names nothing, as
	// there.
	for i, l := range p.Lacunas {
		for j, fr := range l.SourceFields {
			cpth := cue.ParsePath(fr.Path)
			if cpth.Err() != nil {
				continue
			}
			ip, err := irPath(cpth)
			if err != nil {
				continue
			}
			f.Named = append(f.Named, compiled.Named{Path: ip, Lacuna: i, Field: j})
		}
	}
	return f, nil
}

func pathPair(result, input cue.Path) (compiled.PathPair, error) {
	r, err := irPath(result)
	if err != nil {
		return compiled.PathPair{}, err
	}
	in, err := irPath(input)
	if err != nil {
		return compiled.PathPair{}, err
	}
	return compiled.PathPair{Result: r, Input: in}, nil
}

// irPath converts a path within an instance: a list's open element is Any.
func irPath(p cue.Path) (lensir.Path, error) {
	var out lensir.Path
	for _, sel := range p.Selectors() {
		switch {
		case sel.Type().LabelType() == cue.IndexLabel && sel.ConstraintType() == cue.PatternConstraint:
			out = append(out, lensir.Any())
		case sel.Type().LabelType() == cue.IndexLabel:
			out = append(out, lensir.Index(sel.Index()))
		case sel.IsString():
			out = append(out, lensir.Field(sel.Unquoted()))
		default:
			return nil, fmt.Errorf("path %s has a selector an instance path does not: %s", p, sel)
		}
	}
	return out, nil
}

// fateChecks returns the acceptance checks a step can need beyond those of
// checks: where a struct of the step's source is open, an undeclared field
// in it goes, as Go's translation places it (stepLens.targets), to every
// copy of the deepest struct holding it that the lens takes whole, otherwise
// to its own path; there, a label the target declares and the source struct
// does not is one it may arrive under, and is promoted only if the check
// accepts it. Every adjacent pair of versions, both ways; have holds the
// checks already rendered, by version, node and label.
func (cp *compiler) fateChecks(shapes map[thema.SyntacticVersion][]shape, have map[string]bool) []*compiled.Check {
	copies := map[[2][2]uint][]compiled.PathPair{}
	for _, e := range cp.c.Edges {
		if e.Fate != nil {
			copies[[2][2]uint{e.From, e.To}] = e.Fate.Copies
		}
	}
	var out []*compiled.Check
	all := cp.lin.All()
	for i := 1; i < len(all); i++ {
		a, b := all[i-1].Version(), all[i].Version()
		for _, st := range [][2]thema.SyntacticVersion{{a, b}, {b, a}} {
			s, t := st[0], st[1]
			cps := copies[[2][2]uint{[2]uint(s), [2]uint(t)}]
			for _, ss := range shapes[s] {
				if !ss.Open {
					continue
				}
				for _, d := range destinations(ss.Path, cps) {
					for _, ts := range shapes[t] {
						// Undeclared data stays in the alternative its tag
						// selects: a label only another alternative declares
						// is placed by the openness of this one, with no check.
						if !samePath(ts.Path, d) || (ss.alt != "" && ts.alt != "" && ss.alt != ts.alt) {
							continue
						}
						for _, l := range ts.Labels {
							k := checkKey(t, ts.Node, l)
							if have[k] || hasLabel(ss.Labels, l) {
								continue
							}
							have[k] = true
							sel := cue.Str(l)
							if src, ok := cp.renderCheck(t, appendPath(ts.path, sel), lookup(ts.val, cue.MakePath(sel))); ok {
								out = append(out, &compiled.Check{Version: [2]uint(t), Node: ts.Node, Label: l, Source: src})
							}
						}
					}
				}
			}
		}
	}
	return out
}

func checkKey(v thema.SyntacticVersion, node uint64, label string) string {
	return fmt.Sprintf("%s %x %s", v, node, label)
}

func hasLabel(labels []string, l string) bool {
	for _, x := range labels {
		if x == l {
			return true
		}
	}
	return false
}

// destinations returns the paths in a step's result that the undeclared
// fields of the source struct at p go to: beneath each copy of the deepest
// value holding them that the lens takes whole, or p itself.
func destinations(p lensir.Path, copies []compiled.PathPair) []lensir.Path {
	best := -1
	var out []lensir.Path
	for _, c := range copies {
		if len(c.Input) > len(p) || len(c.Input) < best || !overlap(c.Input, p) {
			continue
		}
		if len(c.Input) > best {
			best, out = len(c.Input), nil
		}
		out = append(out, append(append(lensir.Path(nil), c.Result...), p[len(c.Input):]...))
	}
	if best < 0 {
		return []lensir.Path{p}
	}
	return out
}

// overlap reports whether one of a and b is a prefix of the other, Any
// matching every index.
func overlap(a, b lensir.Path) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if !sameStep(a[i], b[i]) {
			return false
		}
	}
	return true
}

func samePath(a, b lensir.Path) bool {
	return len(a) == len(b) && overlap(a, b)
}

func sameStep(a, b lensir.Step) bool {
	ai, bi := a.IsAny || a.IsIndex, b.IsAny || b.IsIndex
	switch {
	case ai && bi:
		return a.IsAny || b.IsAny || a.Index == b.Index
	case !ai && !bi:
		return a.Label == b.Label
	}
	return false
}

// sortChecks orders checks by version, keeping the order within each.
func sortChecks(cs []*compiled.Check) {
	sort.SliceStable(cs, func(i, j int) bool {
		return thema.SyntacticVersion(cs[i].Version).Less(thema.SyntacticVersion(cs[j].Version))
	})
}
