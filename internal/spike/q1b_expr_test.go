package spike

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/util"
)

const synth = `package m
import (
	"strings"
	"github.com/sdboyer/thema"
)
_table: {a: 1, b: 2}
lin: thema.Lineage & {
	name: "synth"
	schemas: {
		"0": "0": schema: {
			n:    int & >=0 & <=10
			s:    string
			t:    "a" | "b"
			list: [...int]
			m: [string]: int
			bounded: int32
			opt?: string
		}
		"1": "0": schema: {
			n2:   int & >=-1 & <=9
			s2:   string
			up:   string
			tt:   1 | 2
			slen: int & >=0
			list: [...int]
			m: [string]: int
			bounded: int32
			first?: int
			opt?: string
			tern: string
		}
	}
	lenses: {
		"0.0->1.0": {
			input: _
			result: {
				n2:   input.n - 1
				s2:   "\(input.s)-\(input.n)"
				up:   strings.ToUpper(input.s)
				tt:   _table[input.t]
				slen: len(input.s)
				list: input.list
				m:    input.m
				bounded: input.bounded
				if len(input.list) > 0 {
					first: input.list[0]
				}
				if input.opt != _|_ {
					opt: input.opt
				}
				tern: [if input.n > 5 {"big"}, "small"][0]
			}
			lacunas: []
		}
		"1.0->0.0": {
			input: _
			result: {
				n: input.n2 + 1
				s: input.s2
				if input.tt == 1 {t: "a"}
				if input.tt == 2 {t: "b"}
				list: input.list
				m:    input.m
				bounded: input.bounded
				if input.opt != _|_ {
					opt: input.opt
				}
			}
			lacunas: []
		}
	}
}
`

func bindSrc(t *testing.T, in string) (thema.Lineage, cue.Value) {
	t.Helper()
	ctx := cuecontext.New()
	rt := thema.NewRuntime(ctx)
	cfg := &load.Config{Dir: "/m", Overlay: map[string]load.Source{
		"/m/cue.mod/module.cue": load.FromString(util.ModuleFile("example.com/m")),
		"/m/in.cue":             load.FromString(in),
	}}
	if err := embedreg.Configure(cfg, thema.CueJointFS); err != nil {
		t.Fatal(err)
	}
	bi := load.Instances([]string{"."}, cfg)[0]
	if bi.Err != nil {
		t.Fatal(bi.Err)
	}
	v := ctx.BuildInstance(bi).LookupPath(cue.ParsePath("lin"))
	lin, err := thema.BindLineage(v, rt)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	return lin, v
}

// exprTree prints v.Expr() recursively, the way an interval analysis would read
// operand bounds.
func exprTree(v cue.Value, depth int) string {
	ind := strings.Repeat("  ", depth)
	op, args := v.Expr()
	_, rp := v.ReferencePath()
	ref := ""
	if len(rp.Selectors()) > 0 {
		ref = " ref=" + rp.String()
	}
	kind := v.IncompleteKind().String()
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s%s kind=%s%s", ind, op, kind, ref)
	if op == cue.NoOp || (op == cue.SelectorOp) {
		if v.IsConcrete() && v.Kind() != cue.StructKind && v.Kind() != cue.ListKind {
			fmt.Fprintf(&sb, " = %v", v)
		}
		fmt.Fprintln(&sb)
		if ref != "" && depth < 4 {
			// a reference: show what it refers to (its bounds, for intervals)
			d := cue.Dereference(v)
			if d != v {
				sb.WriteString(exprTree(d, depth+1))
			}
		}
		return sb.String()
	}
	fmt.Fprintln(&sb)
	for _, a := range args {
		sb.WriteString(exprTree(a, depth+1))
	}
	return sb.String()
}

func TestQ1bExprOnComputedLeaves(t *testing.T) {
	var rep strings.Builder
	lin, _ := bindSrc(t, synth)
	ctx := lin.Runtime().Context()
	lenses := lin.Underlying().LookupPath(cue.ParsePath("lenses"))
	for _, edge := range []string{"0.0->1.0", "1.0->0.0"} {
		lens := lenses.LookupPath(cue.MakePath(cue.Str(edge)))
		from := strings.Split(edge, "->")[0]
		var fv thema.SyntacticVersion
		fmt.Sscanf(from, "%d.%d", &fv[0], &fv[1])
		sch, _ := lin.Schema(fv)
		filled := lens.FillPath(cue.ParsePath("input"), cuetil.CloseRecursive(ctx, sch.Underlying().LookupPath(cue.MakePath(cue.Hid("_schema", util.ThemaPackageID)))))
		result := filled.LookupPath(cue.ParsePath("result"))
		fmt.Fprintf(&rep, "\n==== lens %s: result after filling input with closed source\n", edge)
		fmt.Fprintf(&rep, "result.Validate(): %v\n", result.Validate())
		iter, _ := result.Fields(cue.Optional(true), cue.Hidden(true))
		for iter.Next() {
			f := iter.Value()
			fmt.Fprintf(&rep, "-- %s (exists=%v, err=%v)\n%s", iter.Selector(), f.Exists(), errStr(f.Err()), exprTree(f, 1))
		}
		// Fields only comprehensions produce:
		for _, name := range []string{"first", "opt", "t", "tern"} {
			f := result.LookupPath(cue.ParsePath(name))
			fmt.Fprintf(&rep, "-- lookup %s: exists=%v err=%v\n", name, f.Exists(), errStr(f.Err()))
		}
		// Unfilled lens: what does Expr() show for a computed leaf when input is _?
		raw := lens.LookupPath(cue.ParsePath("result"))
		for _, name := range []string{"n2", "s2", "up", "tt", "slen", "n"} {
			f := raw.LookupPath(cue.ParsePath(name))
			if f.Exists() {
				fmt.Fprintf(&rep, "-- UNFILLED %s:\n%s", name, exprTree(f, 1))
			}
		}
	}
	os.WriteFile("q1b_report.txt", []byte(rep.String()), 0o644)
	t.Log("wrote q1b_report.txt")
}

func errStr(err error) string {
	if err == nil {
		return "<nil>"
	}
	return firstLine(err.Error())
}
