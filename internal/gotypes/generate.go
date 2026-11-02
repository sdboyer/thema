// Copyright 2024 CUE Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package gotypes generates Go type declarations from CUE values.
//
// It is derived from cuelang.org/go/internal/encoding/gotypes at
// cuelang.org/go v0.18.0-rc.1 (the code behind `cue exp gengotypes`), which is
// internal upstream and so is copied here. Upstream generates types for the
// exported definitions of a CUE package instance; this copy generates types
// for a set of named root values, which thema takes from a lineage's schemas.
// The other changes from upstream:
//
//   - nested structs, list elements and map values that are structs are
//     emitted as named types rather than inline anonymous structs;
//   - a disjunction of concrete strings is emitted as a named string type with
//     a typed constant for each value;
//   - `null | T` is emitted as a pointer to T (or T, if T is already nillable);
//   - optional fields are pointers unless their type is already nillable, and
//     carry `omitzero`;
//   - Go field names are camel-cased from the CUE name (`created_on` becomes
//     CreatedOn), and the json tag keeps the CUE name exactly;
//   - a scalar field's default is noted in its doc comment;
//   - a disjunction of structs is `any`, and `number` is float64;
//   - references to definitions in other CUE packages are not supported,
//     except time.Time and time.Duration from the standard library.
package gotypes

import (
	"bytes"
	"fmt"
	goast "go/ast"
	goformat "go/format"
	goparser "go/parser"
	goscanner "go/scanner"
	gotoken "go/token"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	goastutil "golang.org/x/tools/go/ast/astutil"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
)

// Root is a CUE value to be generated as a named Go type.
type Root struct {
	// Name is the Go name of the type.
	Name string
	// Doc is the type's doc comment, without comment markers. Lines are
	// separated by newlines.
	Doc string
	// Value is the CUE value. It must be struct-kinded.
	Value cue.Value
}

// Config governs [Generate].
type Config struct {
	// PackageName is the name of the generated Go package. It is required.
	PackageName string
	// Header is written verbatim before the package clause. It should end in a
	// newline, and typically holds a "Code generated" comment.
	Header string
}

// Generate produces a gofmt-formatted Go source file declaring a type for
// each root, along with the named types the roots need.
func Generate(cfg Config, roots ...Root) ([]byte, error) {
	if cfg.PackageName == "" {
		return nil, fmt.Errorf("cannot generate Go types without a package name")
	}
	g := &generator{
		byKey:   make(map[string]*generatedDef),
		names:   make(map[string]bool),
		imports: make(map[string]string),
	}
	for _, root := range roots {
		if g.names[root.Name] {
			return nil, fmt.Errorf("duplicate root type name %q", root.Name)
		}
		if k := root.Value.IncompleteKind(); k != cue.StructKind {
			return nil, fmt.Errorf("root %s must be a struct, is %s", root.Name, k)
		}
		g.root = root.Name
		def := g.newDef(root.Name)
		def.doc = root.Doc
		if err := g.inDef(def, func() (typeFacts, error) {
			return g.emitStruct(root.Value, root.Name, optionalNillable)
		}); err != nil {
			return nil, err
		}
	}

	var buf []byte
	printf := func(format string, args ...any) {
		buf = fmt.Appendf(buf, format, args...)
	}
	printf("%s", cfg.Header)
	printf("package %s\n\n", cfg.PackageName)
	importedGo := slices.Sorted(maps.Values(g.imports))
	importedGo = slices.Compact(importedGo)
	if len(importedGo) > 0 {
		printf("import (\n")
		for _, path := range importedGo {
			printf("\t%q\n", path)
		}
		printf(")\n")
	}
	for _, def := range g.defs {
		if def.inProgress {
			return nil, fmt.Errorf("unexpected in-progress type: %s", def.name)
		}
		if len(def.src) == 0 {
			return nil, fmt.Errorf("unexpected empty type: %s", def.name)
		}
		if def.doc != "" {
			for _, line := range strings.Split(strings.TrimRight(def.doc, "\n"), "\n") {
				printf("// %s\n", line)
			}
		}
		emitDocs(printf, def.name, def.docGroups)
		printf("type %s ", def.name)
		buf = append(buf, def.src...)
		if def.note != "" {
			printf(" // %s", def.note)
		}
		printf("\n\n")
		if len(def.consts) > 0 {
			printf("const (\n")
			for _, c := range def.consts {
				printf("\t%s %s = %s\n", c.name, def.name, c.value)
			}
			printf(")\n\n")
		}
	}

	formatted, err := goformat.Source(buf)
	if err != nil {
		// Showing the generated Go code helps debug where the syntax error is.
		// This should only occur if the generator is buggy.
		lines := bytes.Split(buf, []byte("\n"))
		var withLineNums []byte
		for i, line := range lines {
			withLineNums = fmt.Appendf(withLineNums, "% 4d: %s\n", i+1, line)
		}
		return nil, fmt.Errorf("generated Go is invalid: %w\n%s", err, withLineNums)
	}
	return formatted, nil
}

// generator holds the state for generating one Go file.
type generator struct {
	// defs records the named types to emit, in the order they were begun.
	defs []*generatedDef

	// byKey records named types generated for CUE definitions, keyed by the
	// definition's reference path, so a definition referenced more than once
	// is generated once.
	byKey map[string]*generatedDef

	// names records the Go names already taken by named types and constants.
	names map[string]bool

	// imports records which Go packages the generated types refer to.
	// This is collected as we emit types, so that omitted fields don't leave
	// unused imports. Keys and values are Go import paths.
	imports map[string]string

	// root is the name of the root currently being generated.
	root string

	// def is the named type currently being generated.
	def *generatedDef
}

// generatedDef holds information about a named Go type.
type generatedDef struct {
	name string

	// doc and docGroups are the type's doc comment.
	doc       string
	docGroups []*ast.CommentGroup

	// inProgress helps detect cyclic definitions and prevents emitting any Go source
	// before we are done analyzing and generating the relevant types.
	inProgress bool

	// facts records useful information about the generated type.
	// Note that this only records the facts about the top-level type generated for a definition;
	// the facts about its sub-types, such as the types of fields in a struct,
	// are computed by recursive calls to [generator.emitType] but are not recorded here.
	facts typeFacts

	// src is the generated Go type expression source.
	// We generate types as plaintext Go source rather than [goast.Expr]
	// as the latter makes it very hard to use empty lines and comment placement correctly.
	src []byte

	// consts are the typed constants declared for an enum type.
	consts []enumConst

	// note is the note recorded with notef for the type expression being
	// emitted.
	note string

	// bare records that the type was begun for a value that has not yet
	// been emitted, so a struct or enum for that value is emitted as this
	// type's body rather than as another named type.
	bare bool
}

type enumConst struct {
	name, value string
}

// typeFacts holds useful information about a generated type,
// such as how it was configured by the user, or qualities about the generated Go type.
type typeFacts struct {
	// isTypeOverride records whether the generated type came from a @go(,type=expr) expression.
	isTypeOverride bool

	// isNillable records whether the generated Go type can be compared to nil,
	// such that @go(,optional=nillable) can avoid wrapping it in a pointer.
	isNillable bool
}

// notef records a note about the type being emitted: what CUE type Go could
// not express. A struct field's note is emitted as a line comment after the
// field; a named type's, after its declaration.
func (g *generatedDef) notef(format string, args ...any) {
	if g.note == "" {
		g.note = fmt.Sprintf(format, args...)
	}
}

func (g *generatedDef) printf(format string, args ...any) {
	if !g.inProgress {
		// It only makes sense to append to src while we are building the Go type expression.
		// If we append bytes after we're done, it's pointless, and likely a bug.
		panic("generatedDef.printf called when inProgress is false")
	}
	g.src = fmt.Appendf(g.src, format, args...)
}

type optionalStrategy int

const (
	_ optionalStrategy = iota
	// optional=zero; emit the Go type as-is and rely on the zero value.
	optionalZero
	// optional=nillable (default); emit the Go type with a pointer unless it
	// can already be compared to nil.
	optionalNillable
)

// uniqueName returns name, or name with a numeric suffix if name is taken,
// and records the result as taken.
func (g *generator) uniqueName(name string) string {
	candidate := name
	for i := 2; g.names[candidate]; i++ {
		candidate = fmt.Sprintf("%s%d", name, i)
	}
	g.names[candidate] = true
	return candidate
}

// newDef begins a named type, reserving a unique name derived from name.
func (g *generator) newDef(name string) *generatedDef {
	def := &generatedDef{name: g.uniqueName(name), inProgress: true}
	g.defs = append(g.defs, def)
	return def
}

// inDef generates the source of def with emit, which writes to def.
//
// When generating a named type, we may recurse into this very method if a
// field needs a named type of its own. Store the current [generatedDef] in the
// stack so we don't lose what we have generated so far.
func (g *generator) inDef(def *generatedDef, emit func() (typeFacts, error)) error {
	parentDef := g.def
	g.def = def
	facts, err := emit()
	if err != nil {
		return err
	}
	def.facts = facts
	def.inProgress = false
	g.def = parentDef
	return nil
}

// emitNamed generates a named type with a name derived from name, writes a
// reference to it into the current type, and returns the named type's facts.
func (g *generator) emitNamed(name string, emit func() (typeFacts, error)) (typeFacts, error) {
	if g.def.bare && len(g.def.src) == 0 {
		g.def.bare = false
		return emit()
	}
	def := g.newDef(name)
	if err := g.inDef(def, emit); err != nil {
		return typeFacts{}, err
	}
	g.def.printf("%s", def.name)
	return def.facts, nil
}

// emitType generates a CUE value as a Go type.
// When possible, the Go type is emitted in the form of a reference.
// Otherwise, an inline Go type expression is used, or a named type is
// generated with a name derived from name.
func (g *generator) emitType(val cue.Value, name string, optionalStg optionalStrategy) (typeFacts, error) {
	var facts typeFacts
	goAttr := goValueAttr(val)
	// We prefer the form @go(Name,type=pkg.Baz) as it is explicit and extensible,
	// but we are also backwards compatible with @go(Name,pkg.Baz) as emitted by `cue get go`.
	// Make sure that we don't mistake @go(,foo=bar) for a type though.
	attrType, _, _ := goAttr.Lookup(1, "type")
	if attrType == "" {
		if s, _ := goAttr.String(1); !strings.Contains(s, "=") {
			attrType = s
		}
	}
	if attrType != "" {
		fset := gotoken.NewFileSet()
		expr, importedByName, err := parseTypeExpr(fset, attrType)
		if err != nil {
			return facts, fmt.Errorf("cannot parse @go type expression: %w", err)
		}
		for _, pkgPath := range importedByName {
			g.imports[pkgPath] = pkgPath
		}
		// Collect any remaining imports from selectors on unquoted single-element std packages
		// such as `@go(,type=io.Reader)`.
		expr = goastutil.Apply(expr, func(c *goastutil.Cursor) bool {
			if sel, _ := c.Node().(*goast.SelectorExpr); sel != nil {
				if imp, _ := sel.X.(*goast.Ident); imp != nil {
					if importedByName[imp.Name] != "" {
						// `@go(,type="go/constant".Kind)` ends up being parsed as the Go expression `constant.Kind`;
						// via importedByName we can tell that "constant" is already provided via "go/constant".
						return true
					}
					g.imports[imp.Name] = imp.Name
				}
			}
			return true
		}, nil).(goast.Expr)
		var buf bytes.Buffer
		// We emit in plaintext, so format the parsed Go expression and print it out.
		if err := goformat.Node(&buf, fset, expr); err != nil {
			return facts, err
		}
		g.def.printf("%s", buf.Bytes())
		facts.isTypeOverride = true
		return facts, nil
	}

	// Note that type references don't get optionalStg,
	// as @go(,optional=) only affects fields under the current type expression.
	if done, refFacts, err := g.emitTypeReference(val); err != nil {
		return typeFacts{}, err
	} else if done {
		return refFacts, nil
	}

	// Inline types are below.

	k := val.IncompleteKind()
	if k&cue.NullKind != 0 && k != cue.NullKind && k != cue.TopKind {
		return g.emitNullable(val, name, optionalStg)
	}

	if values, ok := stringEnum(val); ok {
		return g.emitEnum(name, values)
	}

	switch k {
	case cue.StructKind:
		// A struct with only a pattern constraint, like `[string]: int`, is a map.
		// A struct with regular fields as well is a struct, and the pattern is dropped.
		if elem := val.LookupPath(cue.MakePath(cue.AnyString)); elem.Err() == nil && !hasFields(val) {
			facts.isNillable = true // maps can be nil
			g.def.printf("map[string]")
			if _, err := g.emitType(elem, name+"Value", optionalStg); err != nil {
				return facts, err
			}
			break
		}
		// A disjunction of structs cannot be represented in Go, as it does not have sum types.
		// thema's assignability rules accept only `any` for one.
		//
		// TODO: consider alternatives, such as:
		// * For `#StructFoo | #StructBar`, generate named types for each disjunct,
		//   and use `any` here as a sum type between them.
		// * For a disjunction of closed structs, generate a flat struct with the superset
		//   of all fields, akin to a C union.
		if op, _ := val.Expr(); op == cue.OrOp {
			facts.isNillable = true // interfaces can be nil
			g.def.printf("any")
			g.def.notef("CUE disjunction of structs")
			break
		}
		return g.emitNamed(name, func() (typeFacts, error) {
			return g.emitStruct(val, name, optionalStg)
		})
	case cue.ListKind:
		// We mainly care about patterns like [...string].
		// Anything else can convert into []any as a fallback.
		facts.isNillable = true // slices can be nil
		g.def.printf("[]")
		elem := val.LookupPath(cue.MakePath(cue.AnyIndex))
		if !elem.Exists() {
			// TODO: perhaps mention the original type.
			g.def.printf("any")
			g.def.notef("CUE closed list")
		} else if _, err := g.emitType(elem, name+"Item", optionalStg); err != nil {
			return facts, err
		}

	case cue.NullKind:
		facts.isNillable = true // pointers can be nil
		g.def.printf("*struct{}")
		g.def.notef("CUE null")
	case cue.BoolKind:
		g.def.printf("bool")
	case cue.IntKind:
		g.def.printf("int64")
	case cue.FloatKind:
		g.def.printf("float64")
	case cue.StringKind:
		g.def.printf("string")
	case cue.BytesKind:
		facts.isNillable = true // slices can be nil
		g.def.printf("[]byte")

	case cue.NumberKind:
		// JSON numbers decode into float64 whether integral or not.
		g.def.printf("float64")

	case cue.TopKind:
		facts.isNillable = true // interfaces can be nil
		g.def.printf("any")

	// TODO: generate e.g. int8 where appropriate
	// TODO: uint64 would be marginally better than int64 for unsigned integer types

	default:
		// A disjunction of various kinds cannot be represented in Go, as it does not have sum types.
		// Also see the potential approaches in the TODO about disjunctions of structs.
		if op, _ := val.Expr(); op == cue.OrOp {
			facts.isNillable = true // interfaces can be nil
			g.def.printf("any")
			g.def.notef("CUE disjunction: %s", k)
			break
		}
		facts.isNillable = true // interfaces can be nil
		g.def.printf("any")
		g.def.notef("CUE kind: %s", k)
	}
	return facts, nil
}

// emitStruct generates the body of a Go struct type for a struct-kinded value.
func (g *generator) emitStruct(val cue.Value, typeName string, optionalStg optionalStrategy) (typeFacts, error) {
	var facts typeFacts
	g.def.printf("struct {\n")
	iter, err := val.Fields(cue.Definitions(true), cue.Optional(true))
	if err != nil {
		return facts, err
	}
	goNames := make(map[string]bool)
	prevDocumented := false
	for iter.Next() {
		sel := iter.Selector()
		val := iter.Value()
		if sel.IsDefinition() {
			// TODO: why does removing [cue.Definitions] above break the tests?
			continue
		}
		cueName := selectorName(sel)

		goName := goFieldName(cueName)
		goAttr := val.Attribute("go")
		if s, _ := goAttr.String(0); s != "" {
			if s == "-" {
				continue
			}
			goName = s
		}

		optional := sel.ConstraintType()&cue.OptionalConstraint != 0
		optionalStg := optionalStg // only for this field

		// TODO: much like @go(-), support @(,optional=) when embedded in a value,
		// or attached to an entire package or file, to set a default for an entire scope.
		switch s, ok, _ := goAttr.Lookup(1, "optional"); s {
		case "zero":
			optionalStg = optionalZero
		case "nillable":
			optionalStg = optionalNillable
		default:
			if ok {
				return facts, fmt.Errorf("unknown optional strategy %q", s)
			}
		}

		// Neither version of encoding/json can express names with double quotes,
		// commas, backticks, or newlines in a struct tag; supporting such fields
		// would require generating marshal methods. They are rare, so we skip them
		// with a clear note.
		if strings.ContainsAny(cueName, "\\\"`,\n") {
			g.def.printf("// CUE field %q: encoding/json does not support this field name\n\n", cueName)
			continue
		}

		// Two CUE names can camel-case to the same Go name.
		for i := 2; goNames[goName]; i++ {
			goName = fmt.Sprintf("%s%d", strings.TrimRight(goName, "0123456789"), i)
		}
		goNames[goName] = true

		// Fields are separated by a blank line only where one has a doc
		// comment, so that a struct of undocumented fields stays compact.
		d, hasDefault := scalarDefault(val)
		documented := len(val.Doc()) > 0 || hasDefault
		if (documented || prevDocumented) && len(goNames) > 1 {
			g.def.printf("\n")
		}
		prevDocumented = documented
		emitDocs(g.def.printf, cueName, val.Doc())
		if hasDefault {
			if len(val.Doc()) > 0 {
				g.def.printf("//\n")
			}
			g.def.printf("// Default: %v\n", d)
		}
		g.def.printf("%s ", goName)

		// Pointers in Go are a prefix in the syntax, but we won't find out the generated type facts
		// until we have emitted its Go source, which we do into the same buffer to avoid copies.
		// Luckily, since a pointer is always one byte, and we gofmt the result anyway for nice formatting,
		// we can add the pointer first and replace it with whitespace later if not wanted.
		ptrOffset := len(g.def.src)
		g.def.printf("*")

		structNote := g.def.note
		g.def.note = ""
		fieldFacts, err := g.emitType(val, typeName+goName, optionalStg)
		if err != nil {
			return fieldFacts, err
		}
		if !usePointer(fieldFacts, optional, optionalStg) {
			g.def.src[ptrOffset] = ' '
		}

		omitZero := ""
		if optional {
			omitZero = ",omitzero"
		}
		g.def.printf(" `json:\"%s%s\"`", cueName, omitZero)
		if g.def.note != "" {
			g.def.printf(" // %s", g.def.note)
		}
		g.def.note = structNote
		g.def.printf("\n")
	}
	g.def.printf("}")
	return facts, nil
}

// emitNullable generates a value whose kind includes null and at least one
// other kind, such as `null | string`, as a pointer to the type of its non-null
// disjuncts, or as that type if it is already nillable.
func (g *generator) emitNullable(val cue.Value, name string, optionalStg optionalStrategy) (typeFacts, error) {
	var nonNull []cue.Value
	if op, args := val.Expr(); op == cue.OrOp {
		for _, arg := range args {
			if arg.IncompleteKind() != cue.NullKind {
				nonNull = append(nonNull, arg)
			}
		}
	}
	k := val.IncompleteKind() &^ cue.NullKind

	ptrOffset := len(g.def.src)
	g.def.printf("*")
	var facts typeFacts
	var err error
	switch {
	case len(nonNull) == 1:
		facts, err = g.emitType(nonNull[0], name, optionalStg)
	case len(nonNull) > 1 && allConcreteStrings(nonNull):
		facts, err = g.emitEnum(name, nonNull)
	case k == cue.BoolKind || k == cue.IntKind || k == cue.FloatKind || k == cue.StringKind:
		// Several non-null disjuncts of one scalar kind, such as
		// `null | int | 1`, are that kind.
		facts, err = g.emitType(cuecontext.New().CompileString(k.String()), name, optionalStg)
	default:
		facts.isNillable = true // interfaces can be nil
		g.def.printf("any")
		g.def.notef("CUE disjunction: %s", val.IncompleteKind())
	}
	if err != nil {
		return facts, err
	}
	if facts.isNillable {
		g.def.src[ptrOffset] = ' '
	}
	facts.isNillable = true // pointers can be nil
	return facts, nil
}

// emitEnum generates a named string type for a disjunction of concrete
// strings, with a typed constant for each value.
func (g *generator) emitEnum(name string, values []cue.Value) (typeFacts, error) {
	return g.emitNamed(name, func() (typeFacts, error) {
		g.def.printf("string")
		def := g.def
		for _, v := range values {
			s, err := v.String()
			if err != nil {
				return typeFacts{}, err
			}
			def.consts = append(def.consts, enumConst{
				name:  g.uniqueName(def.name + enumValueName(s)),
				value: strconv.Quote(s),
			})
		}
		return typeFacts{}, nil
	})
}

// stringEnum reports whether val is a disjunction of two or more concrete
// strings, such as `*"a" | "b"`, and returns them.
func stringEnum(val cue.Value) ([]cue.Value, bool) {
	if val.IncompleteKind() != cue.StringKind {
		return nil, false
	}
	op, args := val.Expr()
	if op != cue.OrOp || len(args) < 2 || !allConcreteStrings(args) {
		return nil, false
	}
	return args, true
}

func allConcreteStrings(vals []cue.Value) bool {
	for _, v := range vals {
		if v.Kind() != cue.StringKind {
			return false
		}
	}
	return true
}

// scalarDefault returns the default of a field whose value is a scalar with a
// concrete default, such as `*"foo" | "bar"` or `*5 | int`.
//
// Lists and structs are excluded: every open list reports a default of [].
func scalarDefault(val cue.Value) (cue.Value, bool) {
	k := val.IncompleteKind() &^ cue.NullKind
	if k&(cue.ListKind|cue.StructKind) != 0 {
		return cue.Value{}, false
	}
	d, ok := val.Default()
	if !ok || !d.IsConcrete() {
		return cue.Value{}, false
	}
	return d, true
}

// hasFields reports whether a struct-kinded value has any regular, required
// or optional fields, as opposed to only pattern constraints.
func hasFields(val cue.Value) bool {
	iter, err := val.Fields(cue.Optional(true))
	return err == nil && iter.Next()
}

func usePointer(facts typeFacts, optional bool, strategy optionalStrategy) bool {
	if facts.isTypeOverride {
		// @(,type=) overrides any @(,optional=) setting
		return false
	}
	if !optional {
		// Regular and required fields never use pointers.
		return false
	}
	switch strategy {
	case optionalZero:
		return false
	case optionalNillable:
		// Only use a pointer when the type isn't already nillable.
		return !facts.isNillable
	default:
		panic("unreachable")
	}
}

// parseTypeExpr extends [goparser.ParseExpr] to allow selecting from full import paths.
// `[]go/constant.Kind` is not a valid Go expression, and `[]constant.Kind` is valid
// but doesn't specify a full import path, so it's ambiguous.
//
// Accept `[]"go/constant".Kind` with a pre-processing step to find quoted strings,
// record them as imports keyed by package name in the returned map,
// and rewrite the Go expression to be in terms of the imported package.
// Note that a pre-processing step is necessary as ParseExpr rejects this custom syntax.
func parseTypeExpr(fset *gotoken.FileSet, src string) (goast.Expr, map[string]string, error) {
	var goSrc strings.Builder
	importedByName := make(map[string]string)

	var scan goscanner.Scanner
	scan.Init(fset.AddFile("", fset.Base(), len(src)), []byte(src), nil, 0)
	lastStringLit := ""
	for {
		_, tok, lit := scan.Scan()
		if tok == gotoken.EOF {
			break
		}
		if lastStringLit != "" {
			if tok == gotoken.PERIOD {
				imp, err := strconv.Unquote(lastStringLit)
				if err != nil {
					panic(err) // should never happen
				}
				// We assume the package name is the last path component.
				// TODO: consider how we might support renaming imports,
				// so that importing both foo.com/x and bar.com/x is possible.
				_, impName, _ := cutLast(imp, "/")
				importedByName[impName] = imp
				goSrc.WriteString(impName)
			} else {
				goSrc.WriteString(lastStringLit)
			}
			lastStringLit = ""
		}
		switch tok {
		case gotoken.STRING:
			lastStringLit = lit
		case gotoken.IDENT, gotoken.INT, gotoken.FLOAT, gotoken.IMAG, gotoken.CHAR:
			goSrc.WriteString(lit)
		case gotoken.SEMICOLON:
			// TODO: How can we support multi-line types such as structs?
			// Note that EOF inserts a semicolon, which breaks goparser.ParseExpr.
			if lit == "\n" {
				break // inserted semicolon at EOF
			}
			fallthrough
		default:
			goSrc.WriteString(tok.String())
		}
	}
	expr, err := goparser.ParseExpr(goSrc.String())
	return expr, importedByName, err
}

func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return "", s, false
}

// selectorName returns the name of sel without the trailing "?" or "!"
// for optional or required, which is part of the name in a string label.
func selectorName(sel cue.Selector) string {
	if sel.IsString() {
		return sel.Unquoted()
	}
	return strings.TrimRight(sel.String(), "?!")
}

// camel transforms a CUE name, such as "created_on" or "foo-bar", into an
// exported Go identifier fragment, such as "CreatedOn" or "FooBar": runs of
// characters that may not appear in a Go identifier, and underscores, separate
// words, and each word's first letter is capitalized.
func camel(s string) string {
	var sb strings.Builder
	upper := true
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if upper {
				r = unicode.ToUpper(r)
				upper = false
			}
			sb.WriteRune(r)
		default:
			upper = true
		}
	}
	return sb.String()
}

// goFieldName returns the Go name for a struct field with the CUE name
// cueName. CUE allows quoted field names such as "foo-bar" or "123baz", none
// of which are valid Go identifiers per https://go.dev/ref/spec#Identifiers;
// a leading "F" is added if the name begins with a digit, and "F" is used if
// nothing of the name is usable.
func goFieldName(cueName string) string {
	name := camel(strings.TrimPrefix(cueName, "#"))
	if first, _ := utf8.DecodeRuneInString(name); name == "" || unicode.IsDigit(first) {
		name = "F" + name
	}
	return name
}

// enumValueName returns the suffix appended to an enum type's name to name
// the constant for the string value s.
func enumValueName(s string) string {
	if name := camel(s); name != "" {
		return name
	}
	return "Empty"
}

// goNameFromPath transforms a CUE path, such as "#foo.#bar",
// into a suitable name for a generated Go type, such as "FooBar".
// All path elements must be definitions, or "" is returned.
func goNameFromPath(path cue.Path) string {
	var sb strings.Builder
	for _, sel := range path.Selectors() {
		if !sel.IsDefinition() {
			return ""
		}
		sb.WriteString(camel(strings.TrimPrefix(selectorName(sel), "#")))
	}
	return sb.String()
}

// goValueAttr is like [cue.Value.Attribute] with the string parameter "go",
// but it supports [cue.DeclAttr] attributes as well and not just [cue.FieldAttr].
//
// TODO: surely this is a shortcoming of the method above?
func goValueAttr(val cue.Value) cue.Attribute {
	attrs := val.Attributes(cue.ValueAttr)
	for _, attr := range attrs {
		if attr.Name() == "go" {
			return attr
		}
	}
	return cue.Attribute{}
}

// emitTypeReference attempts to generate a CUE value as a Go type via a
// reference to a definition, generating a named type for the definition the
// first time it is referenced.
//
// The type is named for the root being generated followed by the trailing
// definition selectors of the reference path, so `#Base` declared inside the
// schema of root Foo gives FooBase.
func (g *generator) emitTypeReference(val cue.Value) (bool, typeFacts, error) {
	var root cue.Value
	var path cue.Path
	for _, candidate := range refCandidates(val) {
		root, path = candidate.ReferencePath()
		if root.Exists() {
			break
		}
	}
	if !root.Exists() {
		return false, typeFacts{}, nil
	}

	// As a special case, some CUE standard library types are allowed as references
	// even though they aren't definitions.
	if inst := root.BuildInstance(); inst != nil {
		switch fmt.Sprintf("%s.%s", ast.ParseImportPath(inst.ImportPath).Unqualified(), path) {
		case "time.Duration":
			// Note that CUE represents durations as strings, but Go as int64.
			// TODO: can we do better here, such as a custom duration type?
			g.def.printf("string")
			g.def.notef("CUE time.Duration")
			return true, typeFacts{}, nil
		case "time.Time":
			g.imports["time"] = "time"
			g.def.printf("time.Time")
			return true, typeFacts{}, nil
		}
	}

	// Name the type after the trailing run of definition selectors.
	sels := path.Selectors()
	i := len(sels)
	for i > 0 && sels[i-1].IsDefinition() {
		i--
	}
	name := goNameFromPath(cue.MakePath(sels[i:]...))
	if name == "" {
		return false, typeFacts{}, nil // Not a definition.
	}
	name = g.root + name

	key := fmt.Sprintf("%p %s", root.BuildInstance(), path)
	def, ok := g.byKey[key]
	if !ok {
		def = g.newDef(name)
		def.bare = true
		g.byKey[key] = def
		defVal := root.LookupPath(path)
		def.docGroups = defVal.Doc()
		if err := g.inDef(def, func() (typeFacts, error) {
			return g.emitType(defVal, def.name, optionalNillable)
		}); err != nil {
			return false, typeFacts{}, err
		}
	}
	facts := def.facts
	// We generate types depth-first; if the type referenced here is still in progress,
	// it means that we are in a cyclic type, so be nillable to avoid a Go type of infinite size.
	// Note that sometimes we're in a complex type which is already nillable, such as:
	//
	//    #GraphNode: {edges?: [...#GraphNode]}
	//
	// So we could generate the Go field as `[]GraphNode` rather than `[]*GraphNode`,
	// given that Go slices are already nillable, but we currently do use a pointer.
	if def.inProgress && !facts.isNillable {
		g.def.printf("*")
		facts.isNillable = true // pointers can be nil
	}
	g.def.printf("%s", def.name)
	return true, facts, nil
}

// refCandidates returns the values to try when looking for a type reference.
// For a conjunction like `#Def & {extra constraints}`, the unified value has no
// ReferencePath, so we return the operands that subsume the full value,
// meaning the operand's type fully covers the conjunction's fields
// and the extra constraints only narrow without adding new fields.
func refCandidates(val cue.Value) []cue.Value {
	op, args := val.Expr()
	if op != cue.AndOp {
		return []cue.Value{val}
	}
	var candidates []cue.Value
	for _, arg := range args {
		if arg.Subsume(val) == nil {
			candidates = append(candidates, arg)
		}
	}
	return candidates
}

// emitDocs generates the documentation comments attached to the following declaration.
// It takes a printf function as we emit docs directly in the generated Go code
// when emitting the top-level Go type definitions.
func emitDocs(printf func(string, ...any), _ string, groups []*ast.CommentGroup) {
	// TODO: place the comment group starting with `// $name ...` first.
	// TODO: ensure that the Go name is used in the godoc.
	for i, group := range groups {
		if i > 0 {
			printf("//\n")
		}
		for _, line := range group.List {
			printf("%s\n", line.Text)
		}
	}
}
