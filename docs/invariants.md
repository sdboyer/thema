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

**Stability note: before thema v1, verdicts are unstable.** A verdict may
change between thema releases, including to fix a bug where a breaking change
was accepted as minor. Bind re-checks every step, so a changed verdict surfaces
as a bind error after upgrading, never as a silent reclassification; the author
then renumbers. Version numbers are provisional until v1: consumers that need a
durable identity for a schema before then should store its content hash. At v1
the rules freeze, and later rule changes apply only to steps published after
them.

A schema version *new* is backwards compatible with its predecessor *old* iff

1. every instance valid under *old* is valid under *new*, and
2. for every valid *old* instance, every path where *old* fills a default gets
   the same value under *new*.

Compatibility is judged on concrete data, as a consumer outside CUE sees it.
Hidden fields, definitions and `let`s are not data. A reference means the value
it refers to. Both versions are closed before comparison, keeping only the
openness the author wrote (`...`).

### Field forms

Data can observe three things about a field: whether it may be absent, and if
so, what the consumer sees.

| Form | Absent in data | Class |
|---|---|---|
| `a!: T` | rejected | required |
| `a: T` | rejected (incomplete) | required |
| `a?: T` | stays absent | optional |
| `a: *d \| T` | fills with `d` | defaulted |
| `a: c` (concrete) | fills with `c` | defaulted |
| `a: {...}` whose fields all fill | fills with the minimal struct | defaulted |
| `a: {...}` with a required field inside | rejected | required |
| `a: [...T]` | fills with `[]` | defaulted |

A default on an optional or required field never applies to data.

### Rules

The checker walks both versions and gives each path a verdict. A step's verdict
is **incompatible** if any path is, else **unknown** if any path is, else
**compatible**. For versioning, unknown counts as incompatible: a minor step
must be shown compatible, and the finding says what to change. Each finding
names its rule.

| Rule | Incompatible when |
|---|---|
| `field-added-required` | *new* requires a field *old* does not declare |
| `field-removed` | *old* declares a field *new* neither declares nor accepts through a pattern or `...` |
| `field-now-required` | an optional or defaulted field became required |
| `default-removed` | a defaulted field became optional |
| `default-changed` | a defaulted field fills with a different value |
| `value-narrowed` | *new* does not accept every value *old* does at a path: a kind, bound, alternative or constant changed |
| `length-narrowed` | a list's length bounds narrowed |
| `labels-narrowed` | *old* accepts labels (through `...` or a pattern) that *new* rejects |
| `validator-added` | *new* adds, or tightens, a builtin validator the checker knows (see below) |

Compatible changes include adding an optional or defaulted field, making a
required field optional or defaulted, making an optional field defaulted,
widening a value, dropping a validator, and adding hidden fields, definitions or
`let`s.

| Rule | Unknown when | What to do |
|---|---|---|
| `unknown-crossfield` | *new* constrains a field by another field's value (`b?: a`, `c: >a`), differently from *old* | keep such constraints identical across minor versions |
| `unknown-comprehension` | a comprehension over data (`if kind == "x" {...}`) is pending in either version, and the two differ | keep the comprehension identical across minor versions |
| `unknown-validator` | *new* adds or changes a validator the checker has no rule for (`matchIf`, `matchN`, regexp functions, ...) | keep it identical across minor versions, or express the constraint with bounds |
| `unknown-disjunction` | no single alternative of *new* covers an alternative of *old*, though together they might (`int` → `>=0 \| <0`) | write *new*'s alternatives so that each *old* alternative fits one |
| `unknown-patterns` | pattern constraints differ in a way the checker compares only for identity: several patterns on one side, or a narrower pattern replacing a wider one | keep patterns identical across minor versions |

Known validators: `strings.MinRunes`, `strings.MaxRunes`, `list.MinItems`,
`list.MaxItems`, `list.UniqueItems`, `struct.MinFields`, `struct.MaxFields`,
`time.Time`, `time.Format`, `time.Duration`, `net.IP`, `net.IPv4`, `net.IPv6`.
A lower bound may be lowered and an upper bound raised; the others must stay
identical.

Every rule has a recorded case in `internal/compat/testdata`.
