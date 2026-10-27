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

A lens maps instances of one schema to instances of another. Binding a lineage
proves every CUE lens *total*: for every instance of its source schema, the lens
evaluates without error to a complete instance of its target schema. A lens
that cannot be proven total does not bind. This section defines the proof, the
rules it is made of, and what its verdicts are worth across thema releases.

### Definition

A lens from schema *S* to schema *T* is total when, for every instance *i* of
*S*, evaluating the lens with `input: i` gives a `result` that is an instance of
*T* closed, with every field *T* requires, and every lacuna's `condition` and
field values evaluate to concrete values.

Totality is judged on instances, as `Translate` sees them. `Translate` fills
`input` with the instance unified with *S*, so defaults are present and
optional fields may be absent; it closes the result against *T*, so a field *T*
does not declare is an error. Both schemas are closed before the proof (an
author's `...` survives closing). Hidden fields, definitions and `let`s in the
lens are not data: a result field that refers to one is judged by what it
refers to.

### Obligations

The checker reads the lens's syntax (`cuetil.ParseLens`, `cueview.Tree`),
which depends only on what the lens is, and queries the schemas through the
`cue` API. Each field the result writes carries an obligation, decided by what
the field is built from.

| The field is | Proven by |
|---|---|
| a **copy** of input path *p* | *p* is declared by *S*, and present in every instance under the guards enclosing the field; and every instance of *S* at *p* is an instance of *T* at the field, by the compatibility checker's first clause (`compat.Check`, with the default rules set aside) |
| a **literal** | the literal is an instance of *T* at the field |
| a **struct** or **list** literal | its kind matches *T* at the field; each child is proven in turn, at its path |
| a **computed** expression | it is built from the provable operations below, and its result, as the checker bounds it, fits *T* at the field |
| **guarded** by a comprehension | the guard is a presence test (`p != _\|_`), an equality (`p == c`) or a type test (`(p & T) != _\|_`) on an input path, or a conjunction (`&&`) of these, possibly negated; each arm is proven under the guard, with the source refined by it |

Across the result, every field *T* requires must be written on every path
through the lens: unconditionally, or under guards that together cover every
instance (a guard and its negation; type tests that together subsume every
alternative of the tested field; equalities with every alternative, each a
constant). No path may be written twice with values that could disagree: two
writes unify, so one may reject the other's value.

Lacunas follow the same rules: a `condition` is a guard; a `sourceFields` or
`targetFields` value that reads the input must read a path present when the
condition holds, and one that refers to the result must refer to a field
written under guards the condition implies.

The computed operations the checker proves, over integers, strings and bools:

- **Scalar remaps**: `==` and `!=` as values; a table `{k: v, ...}[input.p]`
  where every alternative of `input.p` is a string constant with an entry; the
  head of a list `[if c {x}, y][0]` with an unconditional fallback.
- **Integer arithmetic**: `+`, `-`, `*`, unary `-`, `quo`, `rem`, `div`, `mod`
  and `len`. Operand bounds are read through `Expr()` (`int32`, `>=0 & <=100`,
  alternatives of constants) and combined as intervals; the result must fit
  `int64` and the target's bounds, and a divisor's interval must exclude zero.
  `/` yields a float in CUE and is not provable. Each occurrence of an operand
  is bounded independently, so `input.n - input.n` has the interval of a
  difference, not `0`; the checker never refutes on such an interval.
- **Strings**: concatenation and interpolation of strings and integers, and
  `strings.ToUpper`, `ToLower`, `TrimSpace`, `Trim`, `TrimLeft`, `TrimRight`,
  `TrimPrefix`, `TrimSuffix` and `Replace` (with an integer count);
  `strings.HasPrefix`, `HasSuffix` and `Contains` as bools. A string result is
  proven only into a target that accepts any string.
- Floats are outside the set.

### Verdicts and rules

Each place in the lens gets one of three verdicts.

- **total**: the obligation holds for every instance.
- **not total**: some instance of *S* has no result, or a result *T* rejects.
- **unknown**: the checker cannot decide, because deciding would need the
  instance (a `for` clause, a computed label), an analysis the checker does not
  attempt (regular expression containment, string contents), or a form it
  does not classify (a guard that is not a presence, equality or type test).

A lens's verdict is the worst of its places: not total if any is, else unknown
if any is, else total. Binding treats unknown as a failure: a lens must be
*shown* total, so the author rewrites it into a form the checker decides.

Every finding names the rule that produced it, and is positioned at the
author's construct: the result field, the comprehension it is declared in, or
the lacuna field. It reads

    <path> <what is wrong> (rule <name>)

for example

    result.a copies input.c, which may be absent: input.c is optional in the source schema; guard the field with if input.c != _|_ (rule source-absent)

| Rule | Not total when |
|---|---|
| `copy-narrowed` | a copied value has an instance the target rejects: a kind, bound, alternative, constant or field the target lacks, or a field it requires that the source does not declare (the compat rule is named in the finding) |
| `source-absent` | a path the lens reads may be absent where it is read: an optional field, an element past a list's minimum length, or a field beneath `_`, with no presence or type test establishing it |
| `source-undeclared` | the lens reads a path the source schema does not declare |
| `target-undeclared` | the lens writes a path the target schema does not declare |
| `guard-absent` | an equality tests a path that may be absent; CUE makes comparing an absent field an error, not `false` |
| `not-a-value` | a result field is a constraint (`int`, `_`), an error (`_\|_`), or a disjunction, where an instance needs one concrete value |
| `literal-not-instance` | a literal, struct or list is not an instance of the target at its path (a wrong kind, a value outside the target, a list of the wrong length) |
| `computed-narrowed` | a computed value has a possible result the target rejects: a kind the target lacks, a bool it does not accept, or an integer outside its bounds |
| `required-not-written` | a field the target requires is never written on some path through the lens |
| `write-conflict` | a path is written twice with values that may disagree |
| `arith-overflow` | an integer operation's result may exceed `int64` |
| `division-by-zero` | a divisor is zero for some instance |
| `table-key-missing` | a table is indexed by a value with no entry |

| Rule | Unknown when | What to do |
|---|---|---|
| `unknown-copy` | the compatibility checker cannot decide whether the copied value fits the target (a constraint between fields, a pending comprehension, a validator, a disjunction it cannot match) | copy a value whose schemas the compatibility checker decides; see its Unknown table |
| `unknown-guard` | a condition is not a presence test, an equality or a type test on an input field, or a conjunction of these | write the condition in one of those forms; test one input field at a time |
| `unknown-coverage` | a required field is written only under conditions the checker cannot show cover every instance | write it unconditionally, under a condition and its negation, or under type tests covering every alternative of the tested field |
| `unknown-op` | a computation uses an operation outside the provable set: `/`, a float, a builtin not listed above, `&` or `\|` within an expression | use the listed operations; `quo` or `div` for integer division |
| `unknown-dynamic` | fields or elements are produced by a `for` clause, a computed label or a list comprehension | write each field by name |
| `unknown-reference` | the lens reads something the checker cannot follow to the input or to a constant: a computed index or label, a selection into a comprehension's output | select fields by name |
| `unknown-bounds` | an integer operand has no finite bounds, so the result cannot be shown within `int64` or the target; or an operand occurs more than once, so the interval cannot refute | bound the fields the computation reads (`int32`, `>=0 & <=100`) |
| `unknown-divisor` | a divisor's bounds include zero, though zero may not be an instance | bound the divisor away from zero (`>0` or `<0`) |
| `unknown-target-constraint` | the target constrains a computed value beyond what the checker compares: a regular expression, a validator, alternatives with gaps, or a disjunction above the written path | express the constraint with `>=`, `<=` or a sized type; make a string target accept any string; write a disjunctive target as one value |
| `unknown-table-key` | a table is indexed by a value that is not a finite set of string constants | index by an input field whose alternatives are string constants |
| `unknown-head` | the first element of a list built by a comprehension is taken with no unconditional fallback | add a fallback element: `[if c {x}, y][0]` |

Every rule has a recorded case in `internal/lenscheck/testdata`.

### Stability of verdicts

A lens that binds is total; `Translate` relies on it. The verdicts follow the
same policy as compatibility verdicts: unstable before thema v1, when a verdict
may change between releases and surfaces as a bind error after upgrading; fixed
from v1, when a lens that binds under one release binds, with the same
verdicts, under every later one, and a rule added or corrected applies only to
lenses published after it. A copy's verdict is a compatibility verdict, and
moves with it.
