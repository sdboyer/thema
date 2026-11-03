package capnp

import (
	"fmt"
	"strconv"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/ext"
	"cuelang.org/go/cue"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/encoding/capnp/compiled"
	"github.com/sdboyer/thema/internal/compat"
	"github.com/sdboyer/thema/internal/lenscheck"
	"github.com/sdboyer/thema/internal/lensir"
)

// renderProgram renders every expression of a program to CEL: each Computed
// node's expression, and each lacuna's condition, message and field values.
// Each rendering is type-checked with cel-go against the variables it reads,
// and a Computed node's against the type of the result field it writes. An
// expression with no faithful rendering (the divergences between CUE and CEL
// in docs/capnp.md) is a finding under cel-unrenderable; its edge still has
// the IR, but the lineage does not compile.
func (cp *compiler) renderProgram(p *lensir.Program) []compiled.CEL {
	var out []compiled.CEL
	idx := 0
	var walk func(nodes []lensir.Node)
	walk = func(nodes []lensir.Node) {
		for _, n := range nodes {
			i := idx
			idx++
			switch n := n.(type) {
			case *lensir.Wrap:
				walk(n.Children)
			case *lensir.Each:
				walk(n.Body)
			case *lensir.Branch:
				walk(n.Then)
			case *lensir.Head:
				walk(n.Then)
				walk(n.Else)
			case *lensir.Table:
				for _, e := range n.Entries {
					walk(e.Value)
				}
			case *lensir.Computed:
				r := cp.newRenderer(p.Edge, n.To)
				src := r.expr(n.Expr)
				r.check(src, r.targetType(n.To))
				out = append(out, compiled.CEL{Role: compiled.ComputedValue, Node: i, Source: src, Vars: r.vars})
			}
		}
	}
	walk(p.Result)
	for li, l := range p.Lacunas {
		if l.When != nil {
			r := cp.newRenderer(p.Edge, nil)
			src := r.expr(l.When)
			r.check(src, cel.BoolType)
			out = append(out, compiled.CEL{Role: compiled.LacunaWhen, Lacuna: li, Source: src, Vars: r.vars})
		}
		if l.Message != nil {
			r := cp.newRenderer(p.Edge, nil)
			src := r.expr(l.Message)
			r.check(src, cel.StringType)
			out = append(out, compiled.CEL{Role: compiled.LacunaMessage, Lacuna: li, Source: src, Vars: r.vars})
		}
		for _, fs := range []struct {
			role compiled.CELRole
			refs []lensir.FieldRef
		}{{compiled.LacunaSourceField, l.SourceFields}, {compiled.LacunaTargetField, l.TargetFields}} {
			for fi, f := range fs.refs {
				if f.Value == nil {
					continue
				}
				r := cp.newRenderer(p.Edge, nil)
				src := r.expr(f.Value)
				r.check(src, nil)
				out = append(out, compiled.CEL{Role: fs.role, Lacuna: li, Field: fi, Source: src, Vars: r.vars})
			}
		}
	}
	return out
}

// A renderer renders one expression of a program to CEL, collecting the
// variables it reads. Input paths are read against the source version's
// schema, result paths against the target's.
type renderer struct {
	cp   *compiler
	edge lensir.Edge
	// to is the result path the expression writes, for findings; nil for a
	// lacuna's expressions.
	to     lensir.Path
	src    *vstruct
	dst    *vstruct
	srcVal cue.Value
	vars   []compiled.Var
	byKey  map[string]string
	names  map[string]bool
	failed bool
}

func (cp *compiler) newRenderer(edge lensir.Edge, to lensir.Path) *renderer {
	from, target := thema.SyntacticVersion(edge.From), thema.SyntacticVersion(edge.To)
	return &renderer{
		cp:     cp,
		edge:   edge,
		to:     to,
		src:    cp.trees[from],
		dst:    cp.trees[target],
		srcVal: cp.schemas[from],
		byKey:  map[string]string{},
		names:  map[string]bool{},
	}
}

// fail records the finding for an expression with no rendering. One finding
// per expression: the first failure explains it.
func (r *renderer) fail(detail string) string {
	if !r.failed {
		r.failed = true
		r.cp.addLens(r.edge, r.to, Rejected, RuleCELUnrenderable, detail)
	}
	return "/* unrenderable */"
}

// check type-checks the rendering with cel-go against the variables it reads
// and, where want is given, the type the result field expects.
func (r *renderer) check(src string, want *cel.Type) {
	if r.failed {
		return
	}
	opts := []cel.EnvOption{ext.Strings()}
	for _, v := range r.vars {
		opts = append(opts, cel.Variable(v.Name, celTypeNamed(v.Type)))
	}
	env, err := cel.NewEnv(opts...)
	if err != nil {
		r.fail(fmt.Sprintf("renders to %s, whose variables cel-go rejects: %v", src, err))
		return
	}
	ast, iss := env.Compile(src)
	if iss.Err() != nil {
		r.fail(fmt.Sprintf("renders to %s, which cel-go rejects: %v", src, iss.Err()))
		return
	}
	got := ast.OutputType()
	if want == nil || isDyn(got) || isDyn(want) {
		return
	}
	if !got.IsEquivalentType(want) {
		r.fail(fmt.Sprintf("renders to %s, of CEL type %s where %s is wanted", src, got, want))
	}
}

func isDyn(t *cel.Type) bool { return t.IsEquivalentType(cel.DynType) }

// variable returns the name of the variable for a path in a role, declaring
// it on first use.
func (r *renderer) variable(p lensir.Path, role compiled.VarRole) string {
	key := fmt.Sprintf("%d:%s", role, p)
	if name, ok := r.byKey[key]; ok {
		return name
	}
	prefix := "in"
	if role == compiled.ResultVar {
		prefix = "out"
	}
	base := varName(prefix, p, role)
	name := base
	for i := 2; r.names[name]; i++ {
		name = base + "_" + strconv.Itoa(i)
	}
	r.names[name] = true
	r.byKey[key] = name
	var t *cel.Type
	switch role {
	case compiled.PresentVar:
		t = cel.BoolType
	case compiled.WhichVar:
		t = cel.StringType
	case compiled.ResultVar:
		vt, _, _ := r.dst.at(p)
		t = celType(vt)
	default:
		vt, _, _ := r.src.at(p)
		t = celType(vt)
	}
	r.vars = append(r.vars, compiled.Var{Name: name, Path: p, Role: role, Type: t.String()})
	return name
}

// varName spells a variable: the prefix, then each step of the path with
// characters outside [A-Za-z0-9_] replaced, the open element as each, and
// the role's suffix.
func varName(prefix string, p lensir.Path, role compiled.VarRole) string {
	var b strings.Builder
	b.WriteString(prefix)
	for _, st := range p {
		b.WriteByte('_')
		switch {
		case st.IsAny:
			b.WriteString("each")
		case st.IsIndex:
			b.WriteString(strconv.Itoa(st.Index))
		default:
			for _, c := range st.Label {
				if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
					b.WriteRune(c)
				} else {
					b.WriteByte('_')
				}
			}
		}
	}
	switch role {
	case compiled.PresentVar:
		b.WriteString("_present")
	case compiled.WhichVar:
		b.WriteString("_which")
	}
	return b.String()
}

// celType is the CEL type values of a compiled type have: integers are int
// (uint for UInt64), enums are their string value, structs are maps of dyn,
// a union is dyn.
func celType(vt *vtype) *cel.Type {
	if vt == nil || vt.isUnion() {
		return cel.DynType
	}
	switch vt.kind {
	case compiled.Void:
		return cel.NullType
	case compiled.Bool:
		return cel.BoolType
	case compiled.UInt64:
		return cel.UintType
	case compiled.Int8, compiled.Int16, compiled.Int32, compiled.Int64, compiled.UInt8, compiled.UInt16, compiled.UInt32:
		return cel.IntType
	case compiled.Text, compiled.Enum:
		return cel.StringType
	case compiled.Data:
		return cel.BytesType
	case compiled.List:
		if vt.elem != nil && vt.elem.strct != nil && vt.elem.strct.role == compiled.UnionBox {
			return cel.ListType(cel.DynType)
		}
		return cel.ListType(celType(vt.elem))
	case compiled.Struct:
		return cel.MapType(cel.StringType, cel.DynType)
	}
	return cel.DynType
}

// celTypeNamed parses a type back from its name, for declaring variables:
// the names celType produces.
func celTypeNamed(name string) *cel.Type {
	switch {
	case name == "null_type":
		return cel.NullType
	case name == "bool":
		return cel.BoolType
	case name == "int":
		return cel.IntType
	case name == "uint":
		return cel.UintType
	case name == kindString:
		return cel.StringType
	case name == kindBytes:
		return cel.BytesType
	case strings.HasPrefix(name, "list(") && strings.HasSuffix(name, ")"):
		return cel.ListType(celTypeNamed(name[len("list(") : len(name)-1]))
	case strings.HasPrefix(name, "map(string, ") && strings.HasSuffix(name, ")"):
		return cel.MapType(cel.StringType, celTypeNamed(name[len("map(string, "):len(name)-1]))
	}
	return cel.DynType
}

// targetType is the CEL type the result field at p expects; nil where the
// target is a union or unknown, which the check then does not compare.
func (r *renderer) targetType(p lensir.Path) *cel.Type {
	vt, _, ok := r.dst.at(p)
	if !ok || vt == nil || vt.isUnion() {
		return nil
	}
	return celType(vt)
}

// at finds the type and form of the field at p beneath the struct: fields by
// label, list elements by index or the open element, a map's values by any
// label. ok is false where the path leaves what the struct declares (a union
// in the middle of it).
func (s *vstruct) at(p lensir.Path) (*vtype, compiled.Form, bool) {
	cur := &vtype{kind: compiled.Struct, strct: s}
	form := compiled.Required
	for _, st := range p {
		if cur == nil || cur.isUnion() {
			return nil, 0, false
		}
		form = compiled.Required
		switch {
		case st.IsAny || st.IsIndex:
			if cur.kind != compiled.List {
				return nil, 0, false
			}
			cur = cur.elem
		case cur.kind == compiled.List && cur.elem != nil && cur.elem.strct != nil && cur.elem.strct.role == compiled.MapEntry:
			cur = cur.elem.strct.fields[1].typ
		case cur.kind == compiled.Struct && cur.strct != nil:
			var found *vfield
			for i := range cur.strct.fields {
				if f := &cur.strct.fields[i]; f.fixed == "" && f.label == st.Label {
					found = f
				}
			}
			if found == nil {
				return nil, 0, false
			}
			cur, form = found.typ, found.form
		default:
			return nil, 0, false
		}
	}
	return cur, form, true
}

// expr renders an expression.
func (r *renderer) expr(e lensir.Expr) string {
	switch e := e.(type) {
	case lensir.Literal:
		return r.literal(e)
	case *lensir.Ref:
		return r.variable(e.Path, compiled.ValueVar)
	case *lensir.ResultRef:
		return r.variable(e.Path, compiled.ResultVar)
	case *lensir.Unary:
		switch e.Op {
		case "-":
			if lit, ok := negatedInt(e); ok {
				return r.literal(lit)
			}
			return "-(" + r.expr(e.X) + ")"
		case "!":
			return "!(" + r.expr(e.X) + ")"
		}
		return r.fail(fmt.Sprintf("uses the operator %s, which has no CEL rendering", e.Op))
	case *lensir.Binary:
		switch e.Op {
		case "+", "-", "*", "==", "!=", "<", ">", "<=", ">=", "&&", "||":
			return "(" + r.expr(e.X) + " " + e.Op + " " + r.expr(e.Y) + ")"
		case "/":
			return r.fail("divides with /, which in CUE is a float even on integers; use quo or div")
		}
		return r.fail(fmt.Sprintf("uses the operator %s, which has no CEL rendering", e.Op))
	case *lensir.Interp:
		parts := make([]string, 0, len(e.Parts))
		for _, p := range e.Parts {
			if lit, ok := p.(lensir.Literal); ok && lit.Kind == lensir.String && lit.Str == "" {
				continue
			}
			s := r.expr(p)
			if r.kindOf(p) != kindString {
				s = "string(" + s + ")"
			}
			parts = append(parts, s)
		}
		if len(parts) == 0 {
			return `""`
		}
		return "(" + strings.Join(parts, " + ") + ")"
	case *lensir.Call:
		return r.call(e)
	case *lensir.Match:
		return r.tests(e.Tests)
	case *lensir.Cond:
		return "(" + r.tests(e.When) + " ? " + r.expr(e.Then) + " : " + r.expr(e.Else) + ")"
	}
	return r.fail(fmt.Sprintf("has an expression of kind %T, which has no CEL rendering", e))
}

// negatedInt reads -N, which the IR carries as a unary minus over the
// literal N, as the literal -N.
func negatedInt(u *lensir.Unary) (lensir.Literal, bool) {
	lit, ok := u.X.(lensir.Literal)
	if !ok || lit.Kind != lensir.Int {
		return lensir.Literal{}, false
	}
	if strings.HasPrefix(lit.Int, "-") {
		lit.Int = lit.Int[1:]
	} else {
		lit.Int = "-" + lit.Int
	}
	return lit, true
}

// intArg reads an integer literal argument, negated or not.
func intArg(e lensir.Expr) (string, bool) {
	switch e := e.(type) {
	case lensir.Literal:
		if e.Kind == lensir.Int {
			return e.Int, true
		}
	case *lensir.Unary:
		if e.Op == "-" {
			if lit, ok := negatedInt(e); ok {
				return lit.Int, true
			}
		}
	}
	return "", false
}

// kindOf is what an expression evaluates to, as far as the renderer needs to
// know it: string, bytes, list, or "" for anything else.
func (r *renderer) kindOf(e lensir.Expr) string {
	switch e := e.(type) {
	case lensir.Literal:
		switch e.Kind {
		case lensir.String:
			return kindString
		case lensir.Bytes:
			return kindBytes
		}
	case *lensir.Ref:
		return kindName(r.src, e.Path)
	case *lensir.ResultRef:
		return kindName(r.dst, e.Path)
	case *lensir.Interp:
		return kindString
	case *lensir.Call:
		return builtins[e.Fn].kind
	case *lensir.Binary:
		if e.Op == "+" && (r.kindOf(e.X) == kindString || r.kindOf(e.Y) == kindString) {
			return kindString
		}
	case *lensir.Cond:
		return r.kindOf(e.Then)
	}
	return ""
}

func kindName(s *vstruct, p lensir.Path) string {
	vt, _, ok := s.at(p)
	if !ok || vt == nil {
		return ""
	}
	switch vt.kind {
	case compiled.Text, compiled.Enum:
		return kindString
	case compiled.Data:
		return kindBytes
	case compiled.List:
		return kindList
	}
	return ""
}

// The names the renderer gives kinds and CEL's boolean literals.
const (
	kindString = "string"
	kindBytes  = "bytes"
	kindList   = "list"
	kindNull   = "null"
	celTrue    = "true"
	celFalse   = "false"
)

// Argument counts.
const (
	oneArg    = 1
	twoArgs   = 2
	threeArgs = 3
	fourArgs  = 4
)

// A builtin is what the renderer knows of a CUE builtin it renders: how many
// arguments it takes, and the kind of its result as far as the renderer
// tells kinds apart (string, bytes, list, or "" for anything else).
type builtin struct {
	arity int
	kind  string
}

// builtins are the CUE builtins with a CEL rendering.
var builtins = map[string]builtin{
	"len": {oneArg, ""}, "quo": {twoArgs, ""}, "rem": {twoArgs, ""}, "div": {twoArgs, ""}, "mod": {twoArgs, ""},
	"strings.Contains":   {twoArgs, ""},
	"strings.HasPrefix":  {twoArgs, ""},
	"strings.HasSuffix":  {twoArgs, ""},
	"strings.TrimSpace":  {oneArg, kindString},
	"strings.Join":       {twoArgs, kindString},
	"strings.Split":      {twoArgs, kindList},
	"strings.SliceRunes": {threeArgs, kindString},
	"strings.Replace":    {fourArgs, kindString},
}

// call renders a builtin call by the divergence table: CUE's integer
// division and remainder functions to CEL's operators, len to a byte count,
// and the strings functions whose CEL counterparts agree on every input.
func (r *renderer) call(c *lensir.Call) string {
	args := make([]string, len(c.Args))
	for i, a := range c.Args {
		args[i] = r.expr(a)
	}
	if b, ok := builtins[c.Fn]; ok && len(args) != b.arity {
		return r.fail(fmt.Sprintf("calls %s with %d arguments", c.Fn, len(args)))
	}
	switch c.Fn {
	case "len":
		switch r.kindOf(c.Args[0]) {
		case kindString:
			// CUE's len counts bytes; CEL's size counts code points.
			return "size(bytes(" + args[0] + "))"
		case kindBytes, kindList:
			return "size(" + args[0] + ")"
		}
		return r.fail("takes len of a value whose kind the renderer cannot tell")
	case "quo":
		return "(" + args[0] + " / " + args[1] + ")"
	case "rem":
		return "(" + args[0] + " % " + args[1] + ")"
	case "div":
		// Floored division from CEL's truncated one: a - mod(a, b), over b.
		return "((" + args[0] + " - ((" + args[0] + " % " + args[1] + " + " + args[1] + ") % " + args[1] + ")) / " + args[1] + ")"
	case "mod":
		return "((" + args[0] + " % " + args[1] + " + " + args[1] + ") % " + args[1] + ")"
	case "strings.Contains":
		return args[0] + ".contains(" + args[1] + ")"
	case "strings.HasPrefix":
		return args[0] + ".startsWith(" + args[1] + ")"
	case "strings.HasSuffix":
		return args[0] + ".endsWith(" + args[1] + ")"
	case "strings.TrimSpace":
		return args[0] + ".trim()"
	case "strings.Join":
		return args[0] + ".join(" + args[1] + ")"
	case "strings.Split":
		return args[0] + ".split(" + args[1] + ")"
	case "strings.SliceRunes":
		return args[0] + ".substring(" + args[1] + ", " + args[2] + ")"
	case "strings.Replace":
		if n, ok := intArg(c.Args[3]); ok && n == "-1" {
			return args[0] + ".replace(" + args[1] + ", " + args[2] + ")"
		}
		return args[0] + ".replace(" + args[1] + ", " + args[2] + ", " + args[3] + ")"
	case "strings.ToUpper", "strings.ToLower", "strings.ToTitle":
		return r.fail(fmt.Sprintf("calls %s, whose CEL counterpart changes ASCII only", c.Fn))
	case "strings.Index", "strings.LastIndex", "strings.IndexByte":
		return r.fail(fmt.Sprintf("calls %s, which counts bytes where CEL counts code points", c.Fn))
	}
	return r.fail(fmt.Sprintf("calls %s, which has no CEL rendering", c.Fn))
}

// literal renders a literal: null, a bool, an integer, a string or bytes.
// Floats are outside the compiled target.
func (r *renderer) literal(l lensir.Literal) string {
	switch l.Kind {
	case lensir.Null:
		return kindNull
	case lensir.Bool:
		return strconv.FormatBool(l.Bool)
	case lensir.Int:
		return l.Int
	case lensir.String:
		return strconv.Quote(l.Str)
	case lensir.Bytes:
		return "b" + strconv.Quote(l.Str)
	}
	return r.fail("has a float literal; floats are outside the compiled target")
}

// tests renders a conjunction of tests.
func (r *renderer) tests(ts []lensir.Test) string {
	if len(ts) == 0 {
		return celTrue
	}
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		parts = append(parts, r.test(t))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " && ") + ")"
}

// test renders one test. A presence test reads the path's presence
// variable. An equality compares the value, under presence where the lens
// wrote it safely over an optional field. A type test on a union field asks
// which member the input holds, with the value's constraint where the type
// is narrower than the member; on a plain field it is the constraint alone.
func (r *renderer) test(t lensir.Test) string {
	var s string
	switch t.Kind {
	case lensir.Presence:
		s = r.variable(t.Path, compiled.PresentVar)
	case lensir.Equality:
		s = "(" + r.variable(t.Path, compiled.ValueVar) + " == " + r.literal(t.Value) + ")"
		if _, form, ok := r.src.at(t.Path); t.Safe && ok && form == compiled.Optional {
			s = "(" + r.variable(t.Path, compiled.PresentVar) + " && " + s + ")"
		}
	case lensir.TypeTest:
		s = r.typeTest(t)
	default:
		return r.fail(fmt.Sprintf("tests the input with a test of kind %d", t.Kind))
	}
	if t.Neg {
		return "!" + s
	}
	return s
}

// typeTest renders (p & T) != _|_.
func (r *renderer) typeTest(t lensir.Test) string {
	// The type is compiled as a field's value, not alone: a bare sized type
	// (int8) compiled alone is a reference Expr does not follow to its
	// bounds, while as a field value it is the conjunction of them.
	tv := r.cp.ctx.CompileString("x: " + t.Type).LookupPath(cue.MakePath(cue.Str("x")))
	if tv.Err() != nil {
		return r.fail(fmt.Sprintf("tests %s against %s, which does not compile: %v", t.Path, t.Type, tv.Err()))
	}
	vt, form, ok := r.src.at(t.Path)
	if !ok || vt == nil {
		return r.fail(fmt.Sprintf("tests %s, a path the source schema does not declare", t.Path))
	}
	var s string
	if vt.isUnion() {
		s = r.unionTest(t.Path, vt, tv)
	} else {
		s = r.constraintTest(r.variable(t.Path, compiled.ValueVar), vt, tv)
	}
	if form == compiled.Optional {
		s = "(" + r.variable(t.Path, compiled.PresentVar) + " && " + s + ")"
	}
	return s
}

// unionTest renders a type test on a union field: the members whose
// alternatives unify with the type, and the type's constraint on the value
// where it is narrower than a whole member.
func (r *renderer) unionTest(p lensir.Path, vt *vtype, tv cue.Value) string {
	fv := lookup(r.srcVal, cuePathOf(p))
	if !fv.Exists() {
		return r.fail(fmt.Sprintf("tests %s, a path the source schema does not declare", p))
	}
	which := r.variable(p, compiled.WhichVar)
	var alts []string
	for _, b := range compat.Branches(fv) {
		if b.Unify(tv).Err() != nil {
			continue
		}
		m := memberOf(vt, b)
		if m == nil {
			return r.fail(fmt.Sprintf("tests %s against %s, an alternative the union has no member for", p, fmtv(tv)))
		}
		alt := "(" + which + " == " + strconv.Quote(m.name) + ")"
		if m.typ != nil && !m.typ.isUnion() {
			if c := r.constraintTest(r.variable(p, compiled.ValueVar), m.typ, tv); c != celTrue {
				alt = "(" + alt + " && " + c + ")"
			}
		}
		alts = append(alts, alt)
	}
	switch len(alts) {
	case 0:
		return celFalse
	case 1:
		return alts[0]
	}
	return "(" + strings.Join(alts, " || ") + ")"
}

// memberOf finds the union member holding an alternative: by kind for a
// kind member, by the tag's constant for a tag member.
func memberOf(vt *vtype, b cue.Value) *vmember {
	for i := range vt.union {
		m := &vt.union[i]
		switch m.role {
		case compiled.KindMember:
			if kindNames[b.IncompleteKind()] == m.kind {
				return m
			}
		case compiled.TagMember:
			if lit, ok := literalOf(b.LookupPath(cue.MakePath(cue.Str(m.tagLabel)))); ok && m.tag != nil && sameLiteral(&lit, m.tag) {
				return m
			}
		}
	}
	return nil
}

// constraintTest renders whether the value in variable v, of the compiled
// type vt, satisfies the CUE type tv: a constant is an equality, constants a
// membership, an integer type its bounds, a kind the kind itself (true where
// it is the field's, false where it is not), and anything else is not
// decided.
func (r *renderer) constraintTest(v string, vt *vtype, tv cue.Value) string {
	brs := compat.Branches(tv)
	if len(brs) > 1 {
		allConst := true
		lits := make([]string, 0, len(brs))
		for _, b := range brs {
			lit, ok := literalOf(b)
			if !ok {
				allConst = false
				break
			}
			lits = append(lits, r.literal(lit))
		}
		if allConst {
			return "(" + v + " in [" + strings.Join(lits, ", ") + "])"
		}
		parts := make([]string, 0, len(brs))
		for _, b := range brs {
			parts = append(parts, r.constraintTest(v, vt, b))
		}
		return "(" + strings.Join(parts, " || ") + ")"
	}
	if lit, ok := literalOf(tv); ok {
		if vt.kind == compiled.Enum || literalKindMatches(lit, vt.kind) {
			return "(" + v + " == " + r.literal(lit) + ")"
		}
		return celFalse
	}
	k := tv.IncompleteKind()
	switch k {
	case cue.IntKind:
		if !isIntKind(vt.kind) {
			return celFalse
		}
		lo, hi, ok := lenscheck.IntegerBounds(tv)
		if !ok {
			return r.fail(fmt.Sprintf("tests against %s, whose bounds the renderer cannot read", fmtv(tv)))
		}
		var parts []string
		if lo != nil {
			parts = append(parts, "("+v+" >= "+lo.String()+")")
		}
		if hi != nil {
			parts = append(parts, "("+v+" <= "+hi.String()+")")
		}
		if len(parts) == 0 {
			return celTrue
		}
		if lo != nil && hi != nil && lo.Cmp(hi) == 0 {
			return "(" + v + " == " + lo.String() + ")"
		}
		return "(" + strings.Join(parts, " && ") + ")"
	case cue.StringKind, cue.BoolKind, cue.BytesKind, cue.NullKind, cue.StructKind, cue.ListKind:
		if op, _ := tv.Expr(); op != cue.NoOp {
			return r.fail(fmt.Sprintf("tests against %s, a constraint the renderer does not decide", fmtv(tv)))
		}
		if vtKind(vt) == k {
			return celTrue
		}
		return celFalse
	}
	return r.fail(fmt.Sprintf("tests against %s, a constraint the renderer does not decide", fmtv(tv)))
}

func isIntKind(k compiled.TypeKind) bool {
	switch k {
	case compiled.Int8, compiled.Int16, compiled.Int32, compiled.Int64, compiled.UInt8, compiled.UInt16, compiled.UInt32, compiled.UInt64:
		return true
	}
	return false
}

// vtKind is the CUE kind of values of a compiled type.
func vtKind(vt *vtype) cue.Kind {
	switch {
	case vt == nil || vt.isUnion():
		return cue.TopKind
	case isIntKind(vt.kind):
		return cue.IntKind
	}
	switch vt.kind {
	case compiled.Void:
		return cue.NullKind
	case compiled.Bool:
		return cue.BoolKind
	case compiled.Text, compiled.Enum:
		return cue.StringKind
	case compiled.Data:
		return cue.BytesKind
	case compiled.List:
		return cue.ListKind
	case compiled.Struct:
		return cue.StructKind
	}
	return cue.TopKind
}

func literalKindMatches(l lensir.Literal, k compiled.TypeKind) bool {
	switch l.Kind {
	case lensir.Int:
		return isIntKind(k)
	case lensir.String:
		return k == compiled.Text
	case lensir.Bool:
		return k == compiled.Bool
	case lensir.Bytes:
		return k == compiled.Data
	case lensir.Null:
		return k == compiled.Void
	}
	return false
}

// cuePathOf converts a lensir path to a cue path: labels, indices and the
// open element.
func cuePathOf(p lensir.Path) cue.Path {
	sels := make([]cue.Selector, 0, len(p))
	for _, st := range p {
		switch {
		case st.IsAny:
			sels = append(sels, cue.AnyIndex)
		case st.IsIndex:
			sels = append(sels, cue.Index(st.Index))
		default:
			sels = append(sels, cue.Str(st.Label))
		}
	}
	return cue.MakePath(sels...)
}
