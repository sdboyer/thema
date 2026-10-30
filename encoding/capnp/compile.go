// Package capnp compiles a bound thema lineage to Cap'n Proto: a schema with
// one struct per major version, split into a new struct wherever a minor
// version is not capnp-append-compatible with the one before it, with
// ordinals and type IDs derived from the lineage's history so that appending
// a version never moves one; and, for each lens, its program (the lens IR)
// with every expression rendered to CEL and type-checked.
//
// The compiler consumes the normal form only: each version's schema, queried
// through the cue API ([cue.Value.Fields] with constraint types,
// [compat.FormOf], [compat.Branches], integer bounds through Expr), and the
// lens programs binding built (thema.LensPrograms). It never reads a lens's
// CUE and never decides anything from Source().
//
// Not every valid lineage compiles. The compiler is its own checker, in the
// house style of internal/compat: a structural walk, findings with stable
// rule IDs, and three-valued verdicts, where Rejected means capnp cannot carry
// the construct and Unknown means this compiler does not carry it yet; both
// fail. docs/capnp.md documents the rules and what an author can do about
// each; every rule has a golden in testdata.
package capnp

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"

	"github.com/sdboyer/thema"
	"github.com/sdboyer/thema/diag"
	"github.com/sdboyer/thema/encoding/capnp/compiled"
	"github.com/sdboyer/thema/internal/cuetil"
	"github.com/sdboyer/thema/internal/embedreg"
	"github.com/sdboyer/thema/internal/lensir"
	"github.com/sdboyer/thema/internal/util"
)

// Compiled is the result of compiling a lineage: see [compiled.Compiled].
type Compiled = compiled.Compiled

// An Option configures Compile.
type Option func(*config)

type config struct {
	namespace string
	tool      string
	noLayout  bool
	vocab     diag.Vocabulary
}

// Namespace sets the hash namespace type IDs are derived under, so that two
// lineages of the same name in different modules get different IDs. A bound
// lineage cannot report its CUE module path, so the caller supplies one, such
// as the module path; the default is the lineage's name alone. The namespace
// is recorded in the result.
func Namespace(ns string) Option {
	return func(c *config) { c.namespace = ns }
}

// Tool names the capnp executable that computes the layout; by default it
// is looked up on PATH as "capnp".
func Tool(path string) Option {
	return func(c *config) { c.tool = path }
}

// WithoutLayout skips running the capnp tool: the result's Layout is nil.
// For use where only the schema text is wanted, or the tool is unavailable.
func WithoutLayout() Option {
	return func(c *config) { c.noLayout = true }
}

// Verdict is the outcome of the type mapping, for one path or overall.
type Verdict int

const (
	// Compilable means every construct maps to a capnp type.
	Compilable Verdict = iota
	// Rejected means capnp cannot carry a construct of the lineage.
	Rejected
	// Unknown means the compiler does not carry a construct yet. It counts as
	// a failure; the rule says what the author can do about it.
	Unknown
)

func (v Verdict) String() string {
	switch v {
	case Compilable:
		return "compilable"
	case Rejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// Rule identifies the rule a finding comes from. Rules are stable identifiers
// documented in docs/capnp.md.
type Rule string

// Rules that reject a lineage: capnp cannot carry the construct.
const (
	// RuleIntUnbounded: an integer with no finite bounds, or bounds beyond 64
	// bits. capnp integers are sized.
	RuleIntUnbounded Rule = "int-unbounded"
	// RuleFloat: a float or number value. Floats are outside the compiled
	// target (#82).
	RuleFloat Rule = "float"
	// RuleTop: _, which has no type.
	RuleTop Rule = "top"
	// RuleDisjunctionUndiscriminated: a disjunction whose alternatives are
	// neither of pairwise distinct kinds nor structs sharing a tag label with
	// distinct constants, so no discriminant can be derived from the value.
	RuleDisjunctionUndiscriminated Rule = "disjunction-undiscriminated"
	// RuleLensNoProgram: a lens uses a construct the IR does not carry, so it
	// has no program. Without one the artifact has no way across its edge.
	RuleLensNoProgram Rule = "lens-no-program"
	// RuleCELUnrenderable: an expression of a lens program has no faithful
	// CEL rendering (a divergence between CUE and CEL, see docs/capnp.md), or
	// its rendering does not type-check.
	RuleCELUnrenderable Rule = "cel-unrenderable"
	// RuleExtrasCheckUnrenderable: a label a version newly declares has a
	// constraint the extras acceptance check cannot express in CEL.
	RuleExtrasCheckUnrenderable Rule = "extras-check-unrenderable"
	// RuleNameCollision: two labels, values or alternatives of one struct map
	// to the same Cap'n Proto name once underscores are dropped and other
	// separators camel-cased (a_b and ab). capnp names in one scope must
	// differ, and the compiler does not invent one.
	RuleNameCollision Rule = "name-collision"
)

// Rules that leave the verdict Unknown: forms this compiler does not carry.
const (
	// RuleUnknownPatternLabels: a struct with pattern constraints other than a
	// lone [string]: T on a struct declaring no other field.
	RuleUnknownPatternLabels Rule = "unknown-pattern-labels"
	// RuleUnknownListHeterogeneous: a closed list whose elements do not all
	// map to one type.
	RuleUnknownListHeterogeneous Rule = "unknown-list-heterogeneous"
	// RuleUnknownUnionWrite: a lens writes an alternative of a disjunction
	// (a copy into one, a struct literal of one alternative) at a path the
	// compiler cannot place among the members of the compiled unions, such
	// as beneath a union in the source.
	RuleUnknownUnionWrite Rule = "unknown-union-write"
)

// Finding is one verdict at one path of one schema version, or of one lens.
// Compilable paths produce no finding.
//
// Its text reads as a sentence about the value at Path in the version:
// "schema <version>: <path> <detail> (rule <rule>)"; for a lens, about the
// result path: "lens <edge>: <path> <detail> (rule <rule>)". Where Path is
// empty, the subject is the schema or the lens.
type Finding struct {
	// Version is the schema version the finding is about; for a lens, its
	// target version.
	Version thema.SyntacticVersion
	// Lens is the edge of the lens the finding is about, as "from->to"; ""
	// for a finding about a schema.
	Lens    string
	Path    cue.Path
	Verdict Verdict
	Rule    Rule
	Detail  string
	// Pos is the author's position for the finding: the value at Path, or
	// the lens.
	Pos token.Pos
}

func (f Finding) String() string {
	if f.Lens != "" {
		subject := "the lens"
		if len(f.Path.Selectors()) > 0 {
			subject = f.Path.String()
		}
		return fmt.Sprintf("lens %s: %s %s (rule %s)", f.Lens, subject, f.Detail, f.Rule)
	}
	subject := "the schema"
	if len(f.Path.Selectors()) > 0 {
		subject = f.Path.String()
	}
	return fmt.Sprintf("schema %s: %s %s (rule %s)", f.Version, subject, f.Detail, f.Rule)
}

// Result is the outcome of the checker: the findings at every path that is
// not Compilable. It is the error Compile returns where there are any, and
// unwraps to their diagnostics.
type Result struct {
	Findings []Finding
	// vocab names constructs in the diagnostics (see [Vocabulary]).
	vocab diag.Vocabulary
}

// Verdict is the overall verdict: Rejected if any path is, otherwise Unknown
// if any is, otherwise Compilable.
func (r Result) Verdict() Verdict {
	v := Compilable
	for _, f := range r.Findings {
		switch f.Verdict {
		case Rejected:
			return Rejected
		case Unknown:
			v = Unknown
		}
	}
	return v
}

// Error lists the findings, one per line.
func (r Result) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "lineage does not compile to Cap'n Proto (%s):", r.Verdict())
	for _, f := range r.Findings {
		b.WriteString("\n\t")
		b.WriteString(f.String())
		if f.Pos.IsValid() {
			fmt.Fprintf(&b, " @ %s", f.Pos)
		}
	}
	return b.String()
}

// compiler is one compilation's state.
type compiler struct {
	cfg      *config
	ctx      *cue.Context
	lin      thema.Lineage
	findings []Finding
	// trees and schemas are each version's type tree and closed schema
	// value, which the lens and check renderers read paths against.
	trees   map[thema.SyntacticVersion]*vstruct
	schemas map[thema.SyntacticVersion]cue.Value
	gens    *generations
	c       *compiled.Compiled
}

func (cp *compiler) add(v thema.SyntacticVersion, p cue.Path, verdict Verdict, rule Rule, detail string, pos token.Pos) {
	cp.findings = append(cp.findings, Finding{Version: v, Path: p, Verdict: verdict, Rule: rule, Detail: detail, Pos: pos})
}

// addLens records a finding about a lens: at its target version, with the
// result path the finding is about, positioned at the lens.
func (cp *compiler) addLens(edge lensir.Edge, to lensir.Path, verdict Verdict, rule Rule, detail string) {
	pos := authorPos(cp.lin.Underlying().LookupPath(cue.MakePath(cue.Str("lenses"), cue.Str(edge.String()))))
	cp.findings = append(cp.findings, Finding{
		Version: thema.SyntacticVersion(edge.To), Lens: edge.String(), Path: cuePathOf(to),
		Verdict: verdict, Rule: rule, Detail: detail, Pos: pos,
	})
}

// Compile compiles lin. Where the lineage has a construct capnp cannot carry,
// the error is a [Result] listing every finding.
func Compile(lin thema.Lineage, opts ...Option) (*Compiled, error) {
	cfg := &config{namespace: lin.Name(), tool: "capnp"}
	for _, opt := range opts {
		opt(cfg)
	}
	cp := &compiler{
		cfg:     cfg,
		ctx:     lin.Runtime().Context(),
		lin:     lin,
		trees:   map[thema.SyntacticVersion]*vstruct{},
		schemas: map[thema.SyntacticVersion]cue.Value{},
		gens:    newGenerations(cfg.namespace, lin.Name()),
	}

	for _, sch := range lin.All() {
		w := &walker{ctx: cp.ctx, version: sch.Version()}
		sv := cuetil.CloseRecursive(cp.ctx, schemaValue(sch))
		tree := w.schema(sv)
		cp.findings = append(cp.findings, w.findings...)
		if len(w.findings) > 0 {
			continue
		}
		cp.trees[sch.Version()] = tree
		cp.schemas[sch.Version()] = sv
		cp.gens.add(sch.Version(), tree)
	}
	cp.findings = append(cp.findings, cp.gens.findings...)
	if len(cp.findings) > 0 {
		return nil, cp.result()
	}

	cp.c = &Compiled{Namespace: cfg.namespace, Lineage: lin.Name()}
	for _, sch := range lin.All() {
		id, err := thema.VersionID(lin, sch.Version())
		if err != nil {
			return nil, err
		}
		v := sch.Version()
		cp.c.Versions = append(cp.c.Versions, compiled.Version{Major: v[0], Minor: v[1], ID: id, Root: cp.gens.rootOf(v)})
	}
	cp.c.Nodes = cp.gens.nodes()
	cp.c.Edges = cp.edges()
	if err := cp.fates(); err != nil {
		return nil, err
	}
	shapes := map[thema.SyntacticVersion][]shape{}
	for i, sch := range lin.All() {
		v := sch.Version()
		shapes[v] = cp.shapes(v)
		for _, s := range shapes[v] {
			cp.c.Versions[i].Structs = append(cp.c.Versions[i].Structs, s.StructShape)
		}
	}
	cp.c.Checks = cp.checks()
	have := map[string]bool{}
	for _, ck := range cp.c.Checks {
		have[checkKey(thema.SyntacticVersion(ck.Version), ck.Node, ck.Label)] = true
	}
	cp.c.Checks = append(cp.c.Checks, cp.fateChecks(shapes, have)...)
	sortChecks(cp.c.Checks)
	cp.c.Messages = asideMessages
	if len(cp.findings) > 0 {
		return nil, cp.result()
	}
	cp.c.Schema = emit(cp.c, cp.gens)
	if !cfg.noLayout {
		b, err := layout(cfg.tool, cp.c.Lineage, cp.c.Schema)
		if err != nil {
			return nil, err
		}
		cp.c.Layout = b
	}
	return cp.c, nil
}

// result sorts the findings, by version, then lens, then path, and returns
// them as the error.
func (cp *compiler) result() Result {
	res := Result{Findings: cp.findings, vocab: cp.cfg.vocab}
	sort.SliceStable(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.Version != b.Version {
			return a.Version.Less(b.Version)
		}
		if a.Lens != b.Lens {
			return a.Lens < b.Lens
		}
		return pathString(a.Path) < pathString(b.Path)
	})
	return res
}

// HaveTool reports whether the capnp tool is available at path (or on PATH,
// for "capnp"), for callers that want to compile without it otherwise.
func HaveTool(path string) bool {
	_, err := exec.LookPath(path)
	return err == nil
}

// pathSchDef is where a schema's definition, unified with the lineage's
// joinSchema, lies in a SchemaDef (lineage.cue).
var pathSchDef = cue.MakePath(cue.Hid("_schema", util.ThemaPackageID))

// schemaValue is the schema instances of sch are validated against: its
// definition joined with the lineage's joinSchema.
func schemaValue(sch thema.Schema) cue.Value {
	return sch.Underlying().LookupPath(pathSchDef)
}

func pathString(p cue.Path) string {
	if len(p.Selectors()) == 0 {
		return ""
	}
	return p.String()
}

// authorPos is the author's position of a value thema's CUE also writes, such
// as a lens or the lenses field: the position of the conjunct that does not
// come from thema's embedded module. A value's own position is that of
// whichever conjunct the evaluator put first, which for these is thema's.
func authorPos(v cue.Value) token.Pos {
	if op, args := v.Expr(); op == cue.AndOp {
		for _, a := range args {
			if p := a.Pos(); p.IsValid() && !strings.HasPrefix(p.Filename(), embedreg.Root()) {
				return p
			}
		}
	}
	return v.Pos()
}
