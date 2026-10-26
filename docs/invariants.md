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
`let`s.

| Rule | Unknown when | What to do |
|---|---|---|
| `unknown-crossfield` | either schema constrains a field by another field's value (`b?: a`, `c: >a`, a reference to a defaulted field or an open list), and the two differ | keep such constraints identical across minor versions |
| `unknown-comprehension` | a comprehension over data (`if kind == "x" {...}`) is pending in either schema, and the two differ | keep the comprehension identical across minor versions |
| `unknown-validator` | *new* adds or changes a validator the checker has no rule for (`matchIf`, `matchN`, regexp functions, ...) | keep it identical across minor versions, or express the constraint with bounds |
| `unknown-disjunction` | no single alternative of *new* covers an alternative of *prior*, though together they might (`int` → `>=0 \| <0`) | write *new*'s alternatives so that each *prior* alternative fits one |
| `unknown-patterns` | pattern constraints differ in a way the checker compares only for identity: several patterns on one side, or a narrower pattern replacing a wider one | keep patterns identical across minor versions |

The validators the checker has rules for are `strings.MinRunes`,
`strings.MaxRunes`, `list.MinItems`, `list.MaxItems`, `list.UniqueItems`,
`struct.MinFields`, `struct.MaxFields`, `time.Time`, `time.Format`,
`time.Duration`, `net.IP`, `net.IPv4` and `net.IPv6`. A lower bound may be
lowered and an upper bound raised; the others must stay identical. Any other
validator is unknown unless it is identical in both schemas. This set changes
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
