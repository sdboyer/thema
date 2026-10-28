package lensir

import (
	"fmt"
	"strconv"
	"strings"
)

// Print renders the program as text, one node per line, nested nodes
// indented beneath the branch, arm or entry they belong to. Input paths are
// written in.p and result paths out.p. The text is for people, and for the
// goldens that record what each lens compiles to; it is not parsed back.
func Print(p *Program) string {
	pr := &printer{}
	for _, n := range p.Result {
		pr.node(n)
	}
	for _, l := range p.Lacunas {
		pr.lacuna(l)
	}
	return pr.b.String()
}

type printer struct {
	b      strings.Builder
	indent int
}

func (pr *printer) linef(format string, args ...any) {
	pr.b.WriteString(strings.Repeat("  ", pr.indent))
	fmt.Fprintf(&pr.b, format, args...)
	pr.b.WriteByte('\n')
}

// block prints the nodes indented, between an opening line and a closing
// brace.
func (pr *printer) block(open string, nodes []Node) {
	pr.linef("%s {", open)
	pr.indent++
	for _, n := range nodes {
		pr.node(n)
	}
	pr.indent--
	pr.linef("}")
}

func (pr *printer) node(n Node) {
	switch n := n.(type) {
	case *Copy:
		pr.linef("%s <- %s", out(n.To), in(n.From))
	case *Constant:
		pr.linef("%s = %s", out(n.To), literal(n.Value))
	case *Wrap:
		switch n.Kind {
		case ListWrap:
			pr.block(fmt.Sprintf("%s: [%d]", out(n.To), n.Len), n.Children)
		default:
			pr.block(out(n.To)+":", n.Children)
		}
	case *Each:
		pr.block(fmt.Sprintf("%s = each %s", out(n.To), in(n.Source)), n.Body)
	case *Branch:
		pr.block("if "+tests(n.When), n.Then)
	case *Head:
		pr.linef("%s = head if %s {", out(n.To), tests(n.When))
		pr.indent++
		for _, c := range n.Then {
			pr.node(c)
		}
		pr.indent--
		pr.linef("} else {")
		pr.indent++
		for _, c := range n.Else {
			pr.node(c)
		}
		pr.indent--
		pr.linef("}")
	case *Table:
		pr.linef("%s = table %s {", out(n.To), in(n.Key))
		pr.indent++
		for _, e := range n.Entries {
			pr.block(strconv.Quote(e.Key), e.Value)
		}
		pr.indent--
		pr.linef("}")
	case *Computed:
		pr.linef("%s = %s", out(n.To), expr(n.Expr))
	default:
		pr.linef("<%T>", n)
	}
}

func (pr *printer) lacuna(l Lacuna) {
	pr.linef("lacuna %s {", l.Type)
	pr.indent++
	if l.When != nil {
		pr.linef("when %s", expr(l.When))
	}
	for _, f := range l.SourceFields {
		pr.fieldRef("source", f)
	}
	for _, f := range l.TargetFields {
		pr.fieldRef("target", f)
	}
	if l.Message != nil {
		pr.linef("message %s", expr(l.Message))
	}
	pr.indent--
	pr.linef("}")
}

// fieldRef prints a field a lacuna reports, under its own condition if it
// has one.
func (pr *printer) fieldRef(list string, f FieldRef) {
	if len(f.When) > 0 {
		pr.linef("%s if %s { %s = %s }", list, tests(f.When), f.Path, expr(f.Value))
		return
	}
	pr.linef("%s %s = %s", list, f.Path, expr(f.Value))
}

// in and out name an input and a result path.
func in(p Path) string  { return rooted("in", p) }
func out(p Path) string { return rooted("out", p) }

func rooted(root string, p Path) string {
	if len(p) == 0 {
		return root
	}
	s := p.String()
	if strings.HasPrefix(s, "[") {
		return root + s
	}
	return root + "." + s
}

func literal(l Literal) string {
	switch l.Kind {
	case Null:
		return "null"
	case Bool:
		return strconv.FormatBool(l.Bool)
	case Int:
		return l.Int
	case Float:
		return l.Float
	case String:
		return strconv.Quote(l.Str)
	case Bytes:
		return "'" + strings.Trim(strconv.Quote(l.Str), `"`) + "'"
	}
	return fmt.Sprintf("<literal kind %d>", l.Kind)
}

func tests(ts []Test) string {
	ss := make([]string, 0, len(ts))
	for _, t := range ts {
		ss = append(ss, test(t))
	}
	return strings.Join(ss, " && ")
}

func test(t Test) string {
	p := in(t.Path)
	bottom := "!= _|_"
	if t.Neg {
		bottom = "== _|_"
	}
	switch t.Kind {
	case Presence:
		return p + " " + bottom
	case Equality:
		if t.Safe {
			return "(" + p + " & " + literal(t.Value) + ") " + bottom
		}
		if t.Neg {
			return p + " != " + literal(t.Value)
		}
		return p + " == " + literal(t.Value)
	case TypeTest:
		typ := t.Type
		if strings.ContainsAny(typ, " |&") {
			typ = "(" + typ + ")"
		}
		return "(" + p + " & " + typ + ") " + bottom
	}
	return fmt.Sprintf("<test kind %d>", t.Kind)
}

func expr(e Expr) string {
	switch e := e.(type) {
	case Literal:
		return literal(e)
	case *Ref:
		return in(e.Path)
	case *ResultRef:
		return out(e.Path)
	case *Unary:
		return e.Op + operand(e.X)
	case *Binary:
		return operand(e.X) + " " + e.Op + " " + operand(e.Y)
	case *Interp:
		var b strings.Builder
		b.WriteByte('"')
		for _, p := range e.Parts {
			if l, ok := p.(Literal); ok && l.Kind == String {
				q := strconv.Quote(l.Str)
				b.WriteString(q[1 : len(q)-1])
				continue
			}
			b.WriteString(`\(` + expr(p) + `)`)
		}
		b.WriteByte('"')
		return b.String()
	case *Call:
		args := make([]string, 0, len(e.Args))
		for _, a := range e.Args {
			args = append(args, expr(a))
		}
		return e.Fn + "(" + strings.Join(args, ", ") + ")"
	case *Match:
		return tests(e.Tests)
	case *Cond:
		return "(if " + tests(e.When) + " then " + expr(e.Then) + " else " + expr(e.Else) + ")"
	}
	return fmt.Sprintf("<%T>", e)
}

// operand renders an operand of an operator, parenthesized where it is an
// operator itself.
func operand(e Expr) string {
	switch e.(type) {
	case *Binary, *Match:
		return "(" + expr(e) + ")"
	}
	return expr(e)
}
