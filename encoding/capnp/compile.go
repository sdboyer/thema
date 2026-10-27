// Package capnp compiles a bound thema lineage to Cap'n Proto: a schema with
// one struct per major version, split into a new struct wherever a minor
// version is not capnp-append-compatible with the one before it, with
// ordinals and type IDs derived from the lineage's history so that appending
// a version never moves one.
//
// The compiler consumes the normal form only: each version's schema, queried
// through the cue API ([cue.Value.Fields] with constraint types,
// [compat.FormOf], [compat.Branches], integer bounds through Expr). It never
// reads a lens's CUE and never decides anything from Source().
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
	"github.com/sdboyer/thema/encoding/capnp/compiled"
	"github.com/sdboyer/thema/internal/cuetil"
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
)

// Rules that leave the verdict Unknown: forms this compiler does not carry.
const (
	// RuleUnknownPatternLabels: a struct with pattern constraints other than a
	// lone [string]: T on a struct declaring no other field.
	RuleUnknownPatternLabels Rule = "unknown-pattern-labels"
	// RuleUnknownListHeterogeneous: a closed list whose elements do not all
	// map to one type.
	RuleUnknownListHeterogeneous Rule = "unknown-list-heterogeneous"
)

// Finding is one verdict at one path of one schema version. Compilable paths
// produce no finding.
//
// Its text reads as a sentence about the value at Path in the version:
// "schema <version>: <path> <detail> (rule <rule>)".
type Finding struct {
	Version thema.SyntacticVersion
	Path    cue.Path
	Verdict Verdict
	Rule    Rule
	Detail  string
	// Pos is the author's position for the finding: the value at Path.
	Pos token.Pos
}

func (f Finding) String() string {
	subject := "the schema"
	if len(f.Path.Selectors()) > 0 {
		subject = f.Path.String()
	}
	return fmt.Sprintf("schema %s: %s %s (rule %s)", f.Version, subject, f.Detail, f.Rule)
}

// Result is the outcome of the checker: the findings at every path that is
// not Compilable. It is the error Compile returns where there are any.
type Result struct {
	Findings []Finding
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

// Compile compiles lin. Where the lineage has a construct capnp cannot carry,
// the error is a [Result] listing every finding.
func Compile(lin thema.Lineage, opts ...Option) (*Compiled, error) {
	cfg := &config{namespace: lin.Name(), tool: "capnp"}
	for _, opt := range opts {
		opt(cfg)
	}
	ctx := lin.Runtime().Context()

	var res Result
	gens := newGenerations(cfg.namespace, lin.Name())
	for _, sch := range lin.All() {
		w := &walker{ctx: ctx, version: sch.Version()}
		tree := w.schema(cuetil.CloseRecursive(ctx, schemaValue(sch)))
		res.Findings = append(res.Findings, w.findings...)
		if len(w.findings) > 0 {
			continue
		}
		gens.add(sch.Version(), tree)
	}
	if len(res.Findings) > 0 {
		sort.SliceStable(res.Findings, func(i, j int) bool {
			a, b := res.Findings[i], res.Findings[j]
			if a.Version != b.Version {
				return a.Version.Less(b.Version)
			}
			return pathString(a.Path) < pathString(b.Path)
		})
		return nil, res
	}

	c := &Compiled{Namespace: cfg.namespace, Lineage: lin.Name()}
	for _, sch := range lin.All() {
		id, err := thema.VersionID(lin, sch.Version())
		if err != nil {
			return nil, err
		}
		v := sch.Version()
		c.Versions = append(c.Versions, compiled.Version{Major: v[0], Minor: v[1], ID: id, Root: gens.rootOf(v)})
	}
	c.Nodes = gens.nodes()
	c.Schema = emit(c, gens)
	if !cfg.noLayout {
		b, err := layout(cfg.tool, c.Lineage, c.Schema)
		if err != nil {
			return nil, err
		}
		c.Layout = b
	}
	return c, nil
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
