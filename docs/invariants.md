# Invariants

TODO TODO TODO

this doc will contain two tables:

* One describing the invariants that define lineage validity, and are enforced by different parts of the toolchain
* Another describing publish-time constraints, which are largely about immutability

At least the first table will indicate the maturity of the enforcement mechanism, as well as where enforcement currently resides (native, portable CUE vs. language-specific helper libs)

Also, this is where the maths discussion should probably live that explains Thema is all a category theoretic construct

What are the axioms of the category Lineage?

## Go Assignability

**Stability note: this definition is rudimentary, and may change.**

Thema defines a relationship between its schemas and Go types based on the notion of "assignability": for all valid instances of a schema, will it be possible to accurately represent that instance in a given Go type? If so, that schema is considered to be "assignable" to that Go type.

Go Assignability is a case-specific definition of the more general [CUE subsumption relation](https://cuelang.org/docs/references/spec/#values-1), amounting to mutual subsumption (`cue ⊑ go` and `go ⊑ cue`), with certain rules relaxed when necessitated by differences in the type systems. Whether the assignable relation holds for a given Thema schema and Go type can be checked with the [`AssignableTo()` Go function](https://pkg.go.dev/github.com/sdboyer/thema#AssignableTo).

### Struct rules

* CUE struct types must correspond to Go struct types, named or unnamed.
* Excess fields must not be present on either side. ([Closed struct semantics](https://cuelang.org/docs/references/spec/#closed-structs) are always applied.)
* If a CUE struct field is optional (`?`), there must exist a corresponding Go struct field.

### List rules

* CUE closed list types must correspond to Go fixed array types.
* CUE open list types must correspond to Go slice types.
* CUE list types must contain at most one type constraint; Go cannot express multi-typed lists.

### Basic type rules

* Go `interface{}` or `any` (Go 1.18+) types are escape hatches, allowing any CUE type.
* CUE values having more than one basic kind (e.g. `(string|int)` may only correspond to `interface{}`/`any`.
* CUE `string` kinded-values must have corresponding Go `string` types.
* CUE `bool` kinded-values must have corresponding Go `bool` types.
* CUE `int` kinded-values must have a corresponding Go integer type must allow at least all the integral values allowed by the CUE type.
  * A CUE `int` is much larger than a Go `int`. Its corresponding Go type is `math/big.Int`.
  * `int32` and `uint32` are recommended for use in CUE schemas where use of Go's ergonomic, arch-dependent `int` and `uint` are desirable in the corresponding Go type.
* CUE `float` kinded-values must have corresponding Go `float64` types.
* CUE `number` kinded-values are not permitted. (Use `int` or `float`.)
* CUE `null` kinded-values are not permitted. (Represent optionality with `?`)

### Other rules

* Go channel, complex, and function types are not permitted.
* At most one level of pointer indirection is permitted on any otherwise valid Go type.

TODO Go pointers, uints, runes, smaller number sizes, CUE & Go embeds, CUE references, improve optionality, nullability, disjunctions
## Schema constructs

A schema is a declaration of what instances look like. Two CUE constructs make
that declaration depend on something other than what the author wrote, and
thema refuses them in schemas and in the `joinSchema`, at bind, with the error
positioned at the construct. Both are refused from the conservative side: CUE
evaluates them without complaint, but the checks that follow (compatibility,
lens results, lacunas) describe schemas by walking their fields and paths, and
neither construct has fields or paths they can describe. Admitting either
later would refuse nothing that binds today.

**No comprehensions.** An `if` or `for` clause anywhere in a schema is
refused, even over constant data that the evaluator would expand (`for k in
["a", "b"] {(k): int}`, which has a plain spelling). A comprehension over an
instance's own fields (`if kind == "x" {extra?: int}`) would make the
schema's fields a function of the data.

**No reference cycles.** A chain of references within a schema that returns to
a value on the chain is refused: a definition that refers to itself
(`#N: {v: int, next?: #N}`), two that refer to each other, or regular fields
that do (`a: b, b: a`), which CUE resolves but no schema needs. A reference to
a value outside the schema that contains the schema (`lin.schemas`) is a cycle
too. Other references that leave the schema are not followed. Non-circular
references are fine: `d: #T`, `b?: a`, `x: y.z`. Trees and JSON-like values
(`#Value: string | [...#Value] | {[string]: #Value}`) are the cost; a lens
over one would have to be recursive, a lacuna's path unbounded, and a lens
compiled to a copy-only target impossible.

## References

A schema declares what instances look like, and a lens declares how an
instance of one schema becomes an instance of another. thema's invariants hold
only while those declarations are sealed: a published schema must mean
tomorrow what it means today, and what a lens returns must be determined by
the instance it takes. Both are true only when whatever a schema or lens
refers to is as fixed as the schema or lens itself. CUE resolves a reference to anything in lexical
scope, so a sibling schema, the lineage's `name`, a helper declared beside the
lineage or a definition in another package all evaluate without complaint, and
each of them can change without the referring schema's own text changing.

This section states what a schema and a lens may refer to, how thema decides
it, and what is left to admit later.

### Rules

**A schema refers only to its own body and CUE's builtin packages.** Its body
is everything declared inside it, at any depth: regular and hidden fields,
definitions, `let`s and aliases. The builtin packages (`strings`, `list`,
`time`, ...) are part of the CUE version thema pins, not something an author
edits. The `joinSchema` is a schema for this purpose: it refers only to its
own body. A schema does not refer to the `joinSchema`; thema unifies it into
every schema itself.

**A lens refers only to the instance it takes, its own body and CUE's builtin
packages.** The instance it takes is the field named after its from version,
`v1_0` for a lens from 1.0, declared `_` (the Source rule under Lens totality
says why), and its body is the instance it returns (`v0_0`), `lacunas` and
anything else declared inside the lens, such as a hidden lookup table or a
`let`. thema's own package is part of the substrate, so
`thema.Lacuna` and `thema.LacunaTypes` are fine.

Everything else is refused when the lineage binds, with the error positioned
at the referring field. The refused thing is spelled where it is used: a
definition two schemas share is declared in each of them, and a table a lens
looks up moves inside the lens. Two versions that share a declaration are
not sealed separately, which is the point: editing the shared declaration
edits the published version too.

The rules start from the conservative side. Referring to another lineage, or
to a package of the author's own module, is a goal, not an error in
principle: a disciplined account of what such a reference means for a
published schema when its referent moves, and of how thema would pin and
check it, is future work. Admitting a form later refuses nothing that binds
today.

### Verdicts and rules

Each reference in a schema or lens gets one of three verdicts.

- **allowed**: the reference stays within what the rules name.
- **refused**: the reference leaves it. Binding fails, and the finding names
  the rule and what the reference denotes.
- **unknown**: thema cannot tell from the schema's or lens's text what the
  reference denotes. Binding treats unknown as a failure.

Every finding is positioned at the referring field and reads

    <owner>: <path> refers to <reference>, <what it denotes>; <the rule> (rule <name>)

for example

    schema 0.0: color refers to #Color, which is declared outside the lineage; a schema may refer only to its own body and CUE's builtin packages (rule ref-outside-lineage)

| Rule | Refused when |
|---|---|
| `ref-sibling-schema` | a schema refers to another schema of the lineage through `schemas`, such as a field of the version before it |
| `ref-lineage-meta` | a schema or lens refers to something declared in the lineage outside itself: `name`, `#LatestVersion`, the `joinSchema`, a helper field or definition declared in the lineage, or `schemas` or `lenses` as a whole |
| `ref-outside-lineage` | a schema or lens refers to something declared outside the lineage, such as a definition at the top of the file |
| `ref-import` | a schema or lens refers to a package other than CUE's builtins, including another package of the author's module |
| `ref-lens-to-schema` | a lens refers to a schema of the lineage |
| `ref-lens-to-lens` | a lens refers to another lens of the lineage |

| Rule | Unknown when | What to do |
|---|---|---|
| `ref-unresolved` | a reference selects into the lineage, or beyond it, by a label only an instance decides (`lin[a]`, with `a` a field of the schema; a label the evaluator can fold, such as `lin["na"+"me"]`, resolves), or through a construct thema does not read | write the reference with constant labels, or declare what it needs inside the schema or lens |

Every rule has a recorded case in the repository's case corpus
(`testdata/cases/ref-*`), alongside a case for each allowed form.

### Stability of verdicts

A lineage that binds satisfies these rules, and every other check in this
document relies on it. A rule here is only ever loosened, by admitting a
reference form, so a lineage that binds under one release binds under every
later one, before v1 as well as after it.

## Backwards compatibility

A lineage may take a minor version step only when the new schema is backwards
compatible with the one before it, and a major step only when it is not. This
section defines what compatible means, how thema decides it, and what its
verdicts are worth across thema releases.

### Definition

A schema *new* is backwards compatible with the schema before it, *prior*, when

1. every instance valid under *prior* is valid under *new*, and
2. for every valid *prior* instance, every path where *prior* fills in a
   default gets the same value under *new*.

The first condition is the familiar one: nothing that was accepted is now
rejected. The second exists because a consumer sees defaults as data. A program
reading an instance that omits a defaulted field receives the default, so
changing or removing that default changes what the program receives, even
though the instance is still accepted.

Compatibility is judged on concrete data, as a consumer outside CUE sees it.
Hidden fields, definitions and `let`s are not data, so changing them is
compatible unless the change reaches data through a reference; a reference
means the value it refers to. Both schemas are closed before comparison, so an
undeclared field is rejected unless the author wrote `...`.

A disjunction is compared alternative by alternative wherever it stands,
beneath a field or as the whole of a schema. Each *prior* alternative must be
accepted by one *new* alternative on its own. Where none does, a value of the
*prior* alternative that *new* rejects is a counterexample, and the step is
incompatible. The checker looks for one in three places: the alternative
itself when it is concrete, any of its values when no *new* alternative shares
its kind, and, for each scalar kind it admits, the first value of a fixed
sequence that equals no concrete *new* alternative (`""` for `string` →
`"a" | "b"`, `2` for `int` → `0 | 1`). Otherwise the `unknown-disjunction` row below applies. A *new* alternative no
prior alternative needs is a widening. A value that is not a disjunction is
one alternative, so `"a"` → `"a" | "b"` is a widening, at a required or
optional field as at a regular one. A conjunction with a disjunctive operand,
`(A | B) & C` or `#Union & {d?: int}`, is the disjunction of that operand's
alternatives, each unified with the rest.

### Field forms

Most verdicts come from fields, and what matters about a field is what an
instance can observe: whether the field may be absent, and if it is absent,
whether the consumer sees nothing or a value the schema supplies. CUE's field
syntax decides this, and the checker sorts every field into one of three forms.

- **required**: an instance must supply it.
- **optional**: an instance may omit it, and it then stays absent.
- **defaulted**: an instance may omit it, and the consumer then sees the value
  the schema fills in.

| Written as | When an instance omits it | Form |
|---|---|---|
| `a!: T` | rejected | required |
| `a: T`, `T` not concrete | rejected: the instance is incomplete | required |
| `a?: T` | stays absent | optional |
| `a: *d \| T` | fills with `d` | defaulted |
| `a: c`, `c` concrete | fills with `c` | defaulted |
| `a: [...T]` | fills with `[]` | defaulted |
| `a: {...}` whose fields all fill | fills with the struct they produce | defaulted |
| `a: {...}` with a required field inside | rejected | required |

A bare struct field takes its form from its contents: an instance may omit
`a: {b: *1 | int}` and sees `a: {b: 1}`, but may not omit `a: {b!: int}`. A
default on an optional or required field, as in `a?: *d | T`, never reaches an
instance: an absent optional field is simply absent, and a required one must be
supplied.

Changing a field's form is compatible exactly when every prior instance still
means the same thing:

| From | To | Verdict |
|---|---|---|
| required | optional or defaulted | compatible: no prior instance omits it |
| optional | defaulted | compatible: *prior* fills nothing there, so there is nothing to preserve |
| optional or defaulted | required | incompatible, `field-now-required` |
| defaulted | optional | incompatible, `default-removed` |
| defaulted with `d` | defaulted with another value | incompatible, `default-changed` |

### Verdicts and rules

The checker walks both schemas together, path by path, and gives each path one
of three verdicts.

- **compatible**: every prior instance is accepted and means the same thing at
  this path.
- **incompatible**: a change at this path rejects some prior instance or
  changes what it means.
- **unknown**: the schemas differ at this path in a way the checker cannot
  decide, because deciding it would need the instance (a constraint between
  fields, a comprehension) or an analysis the checker does not attempt
  (regular expression containment, combinations of disjunction alternatives).

A step's verdict is the worst of its paths: incompatible if any path is, else
unknown if any is, else compatible. Binding a lineage treats unknown as
incompatible: a minor step must be *shown* compatible, so the author either
rewrites the schema so that the checker can decide, or makes it a major step.

Every finding names the rule that produced it, so that it can be looked up in
the tables below, and reads

    <path> <what changed> (rule <name>)

for example

    added is required in the new schema but not declared in the prior one (rule field-added-required)

| Rule | Incompatible when |
|---|---|
| `field-added-required` | *new* requires a field *prior* does not declare |
| `field-removed` | *prior* declares a field *new* neither declares nor accepts through a pattern or `...` |
| `field-now-required` | an optional or defaulted field became required |
| `default-removed` | a defaulted field became optional |
| `default-changed` | a defaulted field fills with a different value |
| `value-narrowed` | *new* does not accept every value *prior* does at a path: a kind, bound, alternative or constant changed |
| `length-narrowed` | a list's length bounds narrowed |
| `labels-narrowed` | *prior* accepts field names (through `...` or a pattern) that *new* rejects |
| `validator-added` | *new* adds, or tightens, a builtin validator the checker knows (see below) |

Compatible changes include adding an optional or defaulted field, making a
required field optional or defaulted, making an optional field defaulted,
widening a value, dropping a validator, and adding hidden fields, definitions or
`let`s. Removing an optional field from a struct whose `...` still accepts it
is compatible too, whatever its value, a struct or list included: `...` accepts
the label with any value. The converse, declaring a type narrower than `_` for
a label `...` accepted, narrows what *prior* admitted there, even as an
optional field.

| Rule | Unknown when | What to do |
|---|---|---|
| `unknown-crossfield` | either schema constrains a field by another field's value (`b?: a`, `c: >a`, a reference to a defaulted field or an open list), and the two differ | keep such constraints identical across minor versions |
| `unknown-comprehension` | a comprehension over data (`if kind == "x" {...}`) is pending in either schema, and the two differ | bind refuses comprehensions in schemas before the checker runs (see Schema constructs); the rule keeps the checker sound on its own |
| `unknown-validator` | *new* adds or changes a validator the checker has no rule for (`matchIf`, `matchN`, `regexp.Match`, `path.Match`, `net.InCIDR`, ...) | keep it identical across minor versions, or express the constraint with bounds |
| `unknown-disjunction` | no single alternative of *new* covers an alternative of *prior*, though together they might (`int` → `>=0 \| <0`) | write *new*'s alternatives so that each *prior* alternative fits one |
| `unknown-patterns` | pattern constraints differ in a way the checker compares only for identity: several patterns on one side, or a narrower pattern replacing a wider one | keep patterns identical across minor versions |

The validators the checker has rules for, and how each may change between
compatible versions:

- Bounds: `strings.MinRunes`, `list.MinItems` and `struct.MinFields` may be
  lowered; `strings.MaxRunes`, `list.MaxItems` and `struct.MaxFields` may be
  raised.
- `math.MultipleOf(n)`: n may change to a divisor of the prior n.
- `strings.HasPrefix`, `strings.HasSuffix` and `strings.Contains`: the
  argument may change to a prefix, suffix or substring of the prior one.
- `strings.ContainsAny`: the argument may gain characters, not lose them.
- Identical only: `list.UniqueItems`, `list.IsSortedStrings`, `list.Contains`,
  `list.IsSorted`, `math.Signbit`, `strconv.IsPrint`, `strconv.IsGraphic`,
  `regexp.Valid`, `encoding/json.Valid`, `time.Time`, `time.Format`,
  `time.Duration`, `net.IP`, `net.IPv4`, `net.IPv6`, `net.IPCIDR`, `net.URL`,
  `net.AbsURL`, `net.FQDN`, `net.GlobalUnicastIP`,
  `net.InterfaceLocalMulticastIP`, `net.LinkLocalMulticastIP`,
  `net.LinkLocalUnicastIP`, `net.LoopbackIP`, `net.MulticastIP` and
  `net.UnspecifiedIP`. A different argument to `list.Contains` or
  `list.IsSorted` is a different requirement in both directions.

Any other validator is unknown unless it is identical in both schemas:
`regexp.Match`, `path.Match` and `net.InCIDR` stay unknown because a changed
argument can widen or narrow, and the checker cannot tell which. This set changes
only under the stability policy below. When CUE adds a validator to its
standard library, no schema written against an earlier CUE can have used it,
which would in principle let the checker learn a rule for it without
disturbing any existing verdict; but a lineage is checked by whichever thema
its consumer runs, and an older thema would report the validator as unknown
where a newer one decides it, so the two would disagree about the lineage.

Every rule has a recorded case in `internal/compat/testdata`.

### Stability of verdicts

Every verdict is part of thema's versioning contract: a lineage's version
numbers mean something only if the verdicts behind them hold.

**Before thema v1, verdicts are unstable.** A verdict may change between thema
releases, including to fix a bug where a breaking change was accepted as minor.
Bind re-checks every step, so a changed verdict surfaces as a bind error after
upgrading, never as a silent reclassification; the author then renumbers.
Version numbers are therefore provisional until v1: consumers that need a
durable identity for a schema before then should store its content hash.

**From thema v1, a verdict never changes.** Every consumer's version numbers,
translations and generated code rest on the verdicts a lineage was bound with,
so a lineage that binds under one v1 release binds, with the same verdicts,
under every later one. A rule may still be added or corrected after v1, but it
applies only to schemas published after it; schemas already published keep the
verdicts they were given.

## Lens totality

Every lens in a lineage is total: for every instance of its source schema, the
lens produces a complete instance of its target schema, and never an error.
thema proves this when it binds a lineage, and a lineage with a lens it cannot
prove total does not bind. Binding is how thema loads a lineage and checks every
invariant in this document, and nothing in thema uses a lineage that has not
bound. Because every lens is total, translating an instance from one version to
another cannot fail. What a step does with each field of an instance, including
fields no lens maps and the fields an open struct admits without declaring,
and which lacunas it raises, is defined in [translation](translation.md); this
section is about the lens.

This section defines totality, states the rules a lens must satisfy for thema
to prove it, and says what the verdicts are worth across thema releases.

### Definition

A lens from schema *S* to schema *T* is total when, for every instance *i* of
*S*:

1. evaluating the lens with *i* as the instance it takes gives an instance
   of *T*, closed, with every field *T* requires, and
2. every lacuna's condition and reported field values evaluate to concrete
   values.

Instances are what translation sees: *i* unified with *S*, so defaults are
filled in and optional fields may be absent. The result is closed against *T*,
so a field *T* does not declare is an error. Both schemas are closed for the
proof, keeping an author's `...`. Hidden fields, definitions and `let`s in a
lens are not data. A result field that refers to one is judged by what it
refers to. A hidden field in the result is evaluated all the same, so it must
evaluate on every instance, though it need not be anything the target knows.

### Rules for a lens

thema proves totality from the lens's text, one result field at a time, asking
the schemas about every path the lens reads or writes. A lens is proven total
when every field satisfies the rule for what it is built from, and the lens as
a whole satisfies the exhaustiveness and single-write rules.

**Source.** The field the lens takes (`v0_0`, for a lens from 0.0) is `_`.
Any other constraint on it, a type, a field or a default, says what the lens
receives where the source schema alone must say it, and the proof is over the
source schema's instances.

**Copies.** A field that copies an input path is total when the source
declares the path, the path is present in every instance where the copy is
evaluated (an optional field must be under a presence pattern), and every value the source
allows there is accepted by the target at the field. The last condition is the
first clause of the backwards-compatibility definition above, applied to the
two paths, defaults included: an instance that omits a field with a default
is not filled with the default alone but with the whole defaulted field
(`*1 | int`), so copying it into a field with another default gives two
defaults and no single value. An optional field an instance omits stays
absent, and copies nothing. Where the target at the field is a disjunction,
each alternative the source may hold at the path, once the patterns above the
copy have refined them, must be accepted by one of the target's alternatives
on its own; the compatibility rules decide which. A source that is not a
disjunction is one alternative, so `{t!: "a"}` copies into `{t!: "a" | "b"}`.

**Literals.** A literal must be an instance of the target at its field. A
struct or list literal must have the target's kind, and each of its fields or
elements is proven in turn at its own path. A struct literal written where the
target is a disjunction must be an instance of one alternative, and is proven
against that alternative alone, closed as translation closes the result. The
alternative is the one that declares every label the literal writes and
accepts every scalar literal it writes directly; a literal that fits no
alternative is not total, and one that fits several is not proven. A struct
literal made only of arms (`if`) at such a target is proven arm by arm, each
arm's struct against its own alternative; the arms must give the field an
alternative on every instance, and may not write two alternatives for one
instance. A struct literal mixing plain fields with arms or embeddings, and a
list literal beneath a disjunction, are not proven.

**Computed values.** A computed field uses only the operations listed below,
and every value it can take must fit the target at the field.

**Patterns and arms.** A field may be written under an `if`, which makes the
field an arm of a match on the input. The condition must be a pattern over
input paths: a presence test (`p != _|_`), an equality with a literal
(`p == c`), a type test (`(p & T) != _|_`), the negation of one of them, or a
conjunction (`&&`) of them. A boolean field on its own, `p`, is the equality
`p == true`. Within an arm, the proof knows what its pattern establishes: that
`p` is present, holds `c`, or is a `T`. A pattern on a path beneath a
disjunction also narrows every path above it to the alternatives in which the
pattern can hold: under `v0_1.rootfield.branch == 1`, where `rootfield` is a
disjunction of structs each with a constant `branch`, a copy of `rootfield`
carries only the alternative whose `branch` is `1`. A type test against a literal,
`(p & c) != _|_`, is the equality `p == c`, since input is data; unlike
`p == c` it is `false` rather than an error where `p` is absent, so it may
test an optional field, and where it holds `p` is present. An equality on a
field that may be a float is not a pattern: CUE compares numbers by value
(`5.0 == 5`), so holding `c` does not fix the kind. A conjunction does not
short-circuit: `p != _|_ && p == c` still compares an absent `p`. Unlike
`match` in Rust, every arm whose pattern holds applies, and the values unify,
so arms must be exhaustive *and* agree where they overlap.

**Lists mapped element by element.** A list may be written as one
comprehension over a source list, `[for item in v0_0.items {...}]`. The body
is proven as a lens from the source list's element to the target list's, with
`item` standing for the element, and every rule above applies to it. A body
that embeds one reference, `[for item in v0_0.users {item.id}]`, copies it as
the whole element, under the rule for copies: the element must always have it
(`source-absent` otherwise), and the target's element must accept every value
of it. The list
has one element per element of the source, so the target list's length bounds
must accept the source list's. A list's length bounds are those its shape sets
(`[_, ...T]`) and those `list.MinItems` and `list.MaxItems` set; a list literal
must fall within the target's the same way. Any other validator on the target
list (`list.UniqueItems`, `list.IsSorted`, …) constrains it beyond its length,
which the proof does not compare. Nor does it read any other validator on the
source list, which may bound its length further: where the bounds it reads do
not fit the target's, the mapping is Unknown. A comprehension that filters
(`if`), nests `for` clauses, or sits beside other elements is not proven; a
comprehension written in the body of another, mapping a list within the
element, is proven as any list in the body is. A list the lens builds this way
and names through a `let` may be measured with `len`, within the source list's
bounds, and its elements reported by a lacuna under the comprehension's own
patterns. Such a list may also be built by nested `for` clauses (`[for s in
v0_0.steps for f in s.fields {...}]`), and measures up to the product of the
lengths of the lists they iterate. A pattern tested before a `for` clause guards
the iteration (`[if v0_0.items != _|_ for item in v0_0.items {...}]`): where it
fails the list is empty, and the source list is read under it, so that a list
the source may omit can be measured.

**Exhaustiveness.** Every field the target requires must be written for every
instance. That holds when the field is written unconditionally, or when the
patterns it is written under together cover every instance:

- a pattern and its negation,
- type tests that together cover every alternative of the tested field,
- equalities with every alternative of the tested field, when each alternative
  is a constant, or
- equalities with `true` and with `false`, for a boolean field.

A field written as arms at a disjunctive target must be given an alternative
the same way.

A field that reads another result field (`v1_0.a`, `self.a`, or through an
alias) reads it only where it is written. Where `a` is written under patterns,
they must hold wherever the reading field is written: the reader is written
under the same patterns, or the patterns `a` is written under cover every
instance, as above. Otherwise the read finds `a` absent, which is an error.
A hidden field reading one is exempt, since an absent field leaves it
incomplete rather than failing.

**One value per path.** No path may be written twice unless the two values
cannot disagree. Two writes to one path unify, so the second can reject the
first's value. At a disjunctive target, two writes that may produce different
alternatives for one instance conflict even where they share no field: the
structs unify into an instance of neither. A hidden field the lens declares is
a path like any other: it is evaluated, so two values that may disagree fail
the lens whether or not a result field reads it.

**Lacunas.** A lacuna's condition is a pattern, or a guard: a boolean
expression that the operations below prove to evaluate for every instance.
Every input path the lacuna reports must be present whenever the condition
holds, and every result field it reports must be written for every instance,
or under patterns the condition implies. A guard establishes nothing about the
source, so under a guard the reported paths must be present, and the reported
fields written, for every instance. A lacuna's message follows the rules for a
result field whose target is any string, each value it reports follows the
rules for a computed value, and its type is a literal naming one of thema's
lacuna types.

**Operations.** The computed operations thema proves, over integers, strings
and booleans. Integer results are proven under an `int64` arithmetic model,
which the compiled engine requires; CUE's unbounded integers are not relied
upon.

- Scalar remaps: `==` and `!=` as values. A table `{k: v, ...}[v0_0.p]`, when
  every alternative of `v0_0.p` is a string constant with an entry. The head
  of a list, `[if c {x}, y][0]`, when the last element is unconditional.
- Integer arithmetic: `+`, `-`, `*`, unary `-`, `quo`, `rem`, `div`, `mod` and
  `len`. Each operand needs finite bounds, from a sized type (`int32`), a
  bounds expression (`>=0 & <=100`) or constant alternatives. The result must
  fit `int64` and the target's bounds, and a divisor's bounds must exclude
  zero. `/` yields a float in CUE and is not proven. Each occurrence of an
  operand is bounded on its own, so `v0_0.n - v0_0.n` is bounded like any
  difference and is never refuted. `len` is bounded by the list's declared
  length, and for a string not at all: a validator such as
  `strings.MaxRunes` is not read.
- Strings: concatenation and interpolation of strings and integers, and
  `strings.ToUpper`, `ToLower`, `TrimSpace`, `Trim`, `TrimLeft`, `TrimRight`,
  `TrimPrefix`, `TrimSuffix` and `Replace` with an integer count.
  `strings.HasPrefix`, `HasSuffix` and `Contains` as booleans. A string result
  is proven only into a target that accepts any string.
- Floats are outside the set.

### Verdicts and rules

Each place in the lens gets one of three verdicts.

- **total**: the rule holds for every instance.
- **not total**: some instance of *S* has no result, or a result *T* rejects.
- **unknown**: thema cannot decide. Deciding would need the instance itself (a
  `for` clause, a computed label), an analysis thema does not attempt (regular
  expression containment, string contents), or a form outside the rules above
  (a condition that is not a presence test, an equality or a type test).

A lens's verdict is the worst of its places: not total if any place is, else
unknown if any is, else total. Binding treats unknown as a failure. A lens must
be shown total, so the author rewrites it into a form thema decides.

Every finding names the rule that produced it, and is positioned at the
author's construct: the result field, the comprehension it is declared in, or
the lacuna field. It reads

    <path> <what is wrong> (rule <name>)

for example

    v1_0.a copies v0_0.c, which may be absent: v0_0.c is optional in the source schema; write the field under if v0_0.c != _|_ (rule source-absent)

| Rule | Not total when |
|---|---|
| `source-constrained` | the lens constrains the field it takes with anything but `_`: a type, a field or a default |
| `copy-narrowed` | a copied value has an instance the target rejects: a kind, bound, alternative, constant or field the target lacks, a field it requires that the source does not declare, or a default differing from the source's. The finding names the compatibility rule |
| `source-absent` | a path the lens reads may be absent where it is read: an optional field, an element past a list's minimum length, or a field beneath `_`, with no presence or type test establishing it |
| `source-undeclared` | the lens reads a path the source schema does not declare |
| `target-undeclared` | the lens writes a path the target schema does not declare |
| `pattern-absent` | an equality tests a path that may be absent. CUE makes comparing an absent field an error, not `false` |
| `not-a-value` | a result field is a constraint (`int`, `_`), an error (`_\|_`), or a disjunction, where an instance needs one concrete value; or a lacuna's type is not a literal |
| `literal-not-instance` | a literal, struct or list is not an instance of the target at its path: a wrong kind, a value outside the target, a list of the wrong length, a struct no alternative of a disjunctive target accepts |
| `computed-narrowed` | a computed value has a possible result the target rejects: a kind the target lacks, a boolean it does not accept, or an integer outside its bounds |
| `required-not-written` | a field the target requires is never written on some path through the lens |
| `write-conflict` | a path, a hidden field's included, is written twice with values that may disagree, or as instances of two alternatives of a disjunctive target |
| `arith-overflow` | an integer operation's result may exceed `int64` |
| `division-by-zero` | a divisor is zero for some instance |
| `table-key-missing` | a table is indexed by a value with no entry |

| Rule | Unknown when | What to do |
|---|---|---|
| `unknown-copy` | the compatibility rules cannot decide whether the copied value fits the target: a constraint between fields, a pending comprehension, a validator, or a disjunction they cannot match; or a list is mapped element by element from a source list whose validator may bound its length where the target's bounds need it | copy a value whose schemas the compatibility rules decide (see their Unknown table) |
| `unknown-pattern` | an arm's condition is not a pattern (a presence test, an equality or a type test on an input field, or a conjunction of these); a lacuna's condition is neither a pattern nor a boolean the computed rules prove; or an equality tests a field that may be a float | write the condition as a pattern, testing one input field at a time; test a number's type before comparing it |
| `unknown-exhaustive` | a required field is written only under patterns that cannot be shown to cover every instance; or a field reads a result field written only under patterns that cannot be shown to hold wherever the reader is written | write it unconditionally, under a condition and its negation, or under type tests covering every alternative of the tested field; write a reader under the patterns of the field it reads |
| `unknown-op` | a computation uses an operation outside the proven set: `/`, a float, a builtin not listed above, or `&` or `\|` within an expression; or a call constrains the result | use the listed operations, with `quo` or `div` for integer division; write the result as a struct |
| `unknown-dynamic` | fields are produced by a `for` clause or a computed label, or list elements by a comprehension that filters, nests `for` clauses, or sits beside other elements | write each field by name; map a list with one comprehension over one source list, every element |
| `unknown-reference` | the lens reads something that cannot be followed to the input or to a constant: a computed index or label, or a selection into a comprehension's output | select fields by name |
| `unknown-bounds` | an integer operand has no finite bounds, so the result cannot be shown to fit `int64` or the target, or an operand occurs more than once. Bounds that contain the target's refute nothing when the result need not take them, as with `len` | bound the fields the computation reads (`int32`, `>=0 & <=100`) |
| `unknown-divisor` | a divisor's bounds include zero, though zero may not be an instance | bound the divisor away from zero (`>0` or `<0`) |
| `unknown-target-constraint` | the target constrains a computed value in a way the proof does not compare: a regular expression, a validator, or alternatives with gaps; a list literal or a mapped list is written to a target list with a validator other than `list.MinItems` and `list.MaxItems`; a struct written to a disjunctive target fits several of its alternatives, or mixes plain fields with arms or embeddings; or a list literal, a mapped list's elements, or a computed value is written beneath a disjunction | express the constraint with `>=`, `<=` or a sized type, or make a string target accept any string; at a disjunctive target write one struct per alternative, its fields unconditional, with a constant field telling the alternatives apart, each in its own arm |
| `unknown-table-key` | a table is indexed by a value that is not a finite set of string constants | index by an input field whose alternatives are string constants |
| `unknown-head` | the first element of a list built by a comprehension is taken with no unconditional fallback, or with one that is itself conditional | add an unconditional fallback element: `[if c {x}, y][0]` |

Every rule has a recorded case in the repository's lens corpus
(`internal/lenscheck/testdata`).

### Stability of verdicts

A lens that binds is total, and translation relies on it. The verdicts follow
the policy for compatibility verdicts. Before thema v1 they are unstable: a
verdict may change between releases, and the change surfaces as a bind error
after upgrading. From v1 they are fixed: a lens that binds under one release
binds, with the same verdicts, under every later one, and a rule added or
corrected applies only to lenses published after it. A copy's verdict is a
compatibility verdict, and moves with it.

### Lens programs

The proof of a lens leaves behind the lens's program (`internal/lensir`): a
target-neutral account of what the lens writes into its result and from what,
built node by node at the places in the walk that proved them, so that no
node exists without its proof. A lens that is not proven total has no program;
`thema.LensPrograms` returns a lineage's programs or says why there are none.
Backends such as `encoding/capnp` render the programs; they never consult
the lens's CUE. The nodes are the proven forms: copies, constants, struct and
list literals, lists mapped element by element (`each`), branches on patterns,
heads, tables, computed expressions over the operations above, and the
declared lacunas, with an entry reported under its own presence test carrying
that test. Where a copy's target is a disjunction of structs or lists, the
copy names, for each alternative of the source it may carry, the target
alternative that accepts it, and a struct literal proven against an
alternative names it; a backend lowering the disjunction to a tagged union
reads the member from these. Three proven forms are not yet carried, and a lens using them binds
without a program: a lacuna reporting one entry per element of a source list,
`len` of a list the lens builds through a `let`, and a list mapped element by
element inside the body of another over a list outside that one's element,
where a path into an open element would not say which item it reads. Each corpus lineage's
programs are recorded beside it (`out/lensir` in `testdata/lineage`,
`exemplars/testdata/lensir`), and each proven form's in the lens corpus
(`internal/lenscheck/testdata`, `ir` sections).
