# Compiling to Cap'n Proto

`encoding/capnp` compiles a bound lineage to a Cap'n Proto schema: one struct
per major version, split into a further struct wherever a minor version is not
capnp-append-compatible with the one before it, with ordinals and type IDs
derived from the lineage's history so that appending a version never moves
one. The schema text is deterministic, and the `capnp` tool's layout of it
(the `CodeGeneratorRequest` of `capnp compile -o-`) is embedded in the result.
Beside the schema, the result carries every lens as its program (the lens IR,
`internal/lensir`) with each expression rendered to CEL, and an acceptance
check for every label a version newly declares (#48); the whole is serialized
as a Cap'n Proto message of its own, the artifact, for an engine that never
links CUE (#90). `thema lineage gen capnp` prints the schema or the artifact.

The compiler consumes the normal form only: each version's schema, read
through the CUE API the way the compatibility checker reads it (field forms
through `compat.FormOf`, alternatives through `compat.Branches`, integer bounds
through `Expr()`). It never decides anything from a value's source text.

Not every valid lineage compiles. The compiler is its own checker, in the
style of the compatibility and totality checkers: a structural walk, findings
with stable rule IDs, and three-valued verdicts. A lineage with a construct
capnp cannot carry is **rejected**; one with a construct this compiler does not
carry yet is **unknown**. Both fail compilation, and every finding names its
rule, so that it can be looked up below. Each finding is reported as a
diagnostic with the rule's code, at the author's construct; the
[code generation reference](reference/codegen.md#capn-proto) summarizes the
compiler's interface.

## Type mapping

Each schema is closed before it is walked, so the only openness the compiler
sees is what the author wrote. Every struct of the schema becomes a capnp
struct whose field `extras @0 :Data` is reserved for an opaque side-channel
(fields a newer version declares that this generation does not); every other
field follows.

| CUE | Cap'n Proto |
|---|---|
| `{...}` with named fields | `struct`, with `extras @0 :Data` then one field per label, in order of first appearance across versions |
| `[string]: T` on a struct declaring no other field | `List(Entry)`, where `Entry` is a struct of `key @0 :Text` and `value @1 :T` |
| `[...T]`, and a closed list whose elements all map to one type | `List(T)` |
| `[]` | `List(Void)` |
| `bool`, `string`, `bytes`, `null` | `Bool`, `Text`, `Data`, `Void` |
| an integer with finite bounds (`int8`…`uint64`, `int & >=0 & <=100`, a constant) | the smallest of `Int8`…`Int64`, `UInt8`…`UInt64` holding the bounds; unsigned at equal size where the lower bound is not negative |
| integer constants `1 \| 2 \| 300` | the integer holding their hull |
| string constants `"a" \| "b"`, and a single string constant | an `enum`, one enumerant per value in declaration order, with the value recorded where its name had to be sanitized |
| alternatives of pairwise distinct kinds `string \| int32 \| null` | a `union` with one member per kind, `asString`, `asInt`, `asNull :Void`, … |
| struct alternatives sharing a label that holds a distinct constant in each `{kind: "a", …} \| {kind: "b", …}` | a `union` with one member per alternative named after the tag (`kindA`, `kindB`), each a nested struct |
| a disjunction as a list element | the element is a struct holding only an anonymous `union` |
| a disjunction as a map value | the entry's `value` is a `union` |

Field forms follow `compat.FormOf`:

| Form | Cap'n Proto |
|---|---|
| required `a: T` | `a @n :T` |
| optional `a?: T` | `a :union { unset @n :Void; value @n+1 :T }`; a disjunction's members replace `value` |
| defaulted `a: *x \| T`, and a concrete value `a: 5` | `a @n :T = x` where capnp can declare the default (`Bool`, integers, `Text`, `Data`, enums); a defaulted struct or list declares none and reads a null pointer as the default; a defaulted union declares none |
| optional with a default `a?: *x \| T` | optional |

Nested nodes are named after the field (`inner: {…}` → `Inner`), with `Item`
for a list's element (`ElemsItem`, `ElemsItemItem`) and `Entry` for a map's
(`MappedEntry`); a tagged alternative's struct after the field and the tag
(`TaggedKindCircle`); a boxed list element's alternatives after the member
alone (`KindA`). Labels are sanitized to capnp names: underscores are dropped
(`ship_v2` → `shipv2`), other characters outside ASCII letters and digits
separate words joined in camel case (`foo-bar` → `fooBar`), lowercase first for
fields and enumerants, uppercase first for types, `v` before a leading digit,
`empty` for an empty label. Two labels, values or alternatives of one struct
that sanitize to the same name (`a_b` and `ab`) are a `name-collision`, never
renamed; only a label that sanitizes to the compiler's own `extras` field is
renamed (`extras2`).

## Generations

A major version is at least one struct. Within a major, a minor version
extends the previous struct in place when every change is one capnp readers
of the earlier data tolerate, and otherwise starts a new struct (a
*generation*), named `<Lineage>V<major>` for the first and
`<Lineage>V<major>M<minor>` for each later one. Extending in place:

- a new field, optional or with a declarable default (a reader of earlier data
  sees `unset`, or the default);
- a required field becoming optional: the value keeps its ordinal as the
  union's `value` member, and `unset` takes the next free ordinal, so earlier
  data (discriminant 0) reads as the value;
- a required `Text`, `Data`, list or struct field becoming defaulted: the
  default is declared, and earlier data is unchanged, a null pointer reading
  as the default;
- a new enumerant, or a new union member.

Starting a new generation:

- a required `Bool`, integer or enum field becoming defaulted: capnp stores
  data fields XORed with their default, so a default declared after data was
  written would misread it;
- an optional field becoming defaulted: a union cannot become a plain field;
- a new defaulted union;
- an integer widening to a larger capnp size (`int8` → `int64`); widening
  within a size does not;
- any of these inside a nested struct, a list element or a map value.

Each version records its root struct. A forward minor step whose versions are
read through different structs gets a synthesized edge whose program is the
identity copy (`out <- in`), so that a reader of the earlier struct can be
given the later data; a forward minor step within one struct has no edge, its
data being read in place.

## Identity

Type IDs are 64-bit hashes with the high bit set, as capnp requires, derived
from the lineage's history and nothing else:

- a generation's root: SHA-256 over the namespace, the lineage name, the major
  and the first minor of the generation;
- a nested node: SHA-256 over the parent's ID and the node's name;
- the file: SHA-256 over the namespace and the lineage name.

Ordinals are assigned in order of first appearance across the generation's
versions and never reassigned. Appending a version therefore leaves every
earlier ID, ordinal and enumerant where it was; `TestStability` in
`encoding/capnp` pins this. The namespace is `Compile`'s `Namespace` option
and defaults to the lineage's name; a bound lineage cannot report its module
path, so a caller that compiles lineages from several modules supplies one.
Each version also carries its content hash (`thema.VersionID`), independent of
the namespace.

## Lens programs and CEL

Every lens the lineage declares becomes an edge of the result, carrying the
program binding built for it (`thema.LensPrograms`): the nodes that write the
result (copies, constants, struct and list literals, `for` over a list,
conditional branches, lookup tables, computed expressions) and the lacunas it
declares. The compiler never reads a lens's CUE; a lens using a construct the
IR does not carry has no program, and the lineage is rejected
(`lens-no-program`).

What a backend without an expression language cannot run, the expressions,
is rendered to [CEL](https://cel.dev) and type-checked with cel-go against
the type of the field it writes: each computed node's expression, each
lacuna's condition and message, and the value of each field a lacuna reports.
An expression reads variables named for the input or result path it reads
(`in_a`, `out_neg`), each declared with its role and CEL type: the value at
the path, whether the path is present (`in_a_present`, for optional fields),
which union member it holds (`in_kind_which`), or the result at the path. CUE
and CEL disagree on some operations; the renderer rewrites what has a faithful
rendering and refuses the rest (`cel-unrenderable`):

| CUE | CEL | Why |
|---|---|---|
| `quo(a, b)`, `rem(a, b)` | `a / b`, `a % b` | CEL's integer division truncates, as these do |
| `div(a, b)`, `mod(a, b)` | `(a - ((a % b + b) % b)) / b`, `(a % b + b) % b` | floored, written in terms of truncation |
| `a / b` on integers | refused | a float in CUE; use `quo` or `div` |
| `len(s)` on a string | `size(bytes(s))` | CUE counts bytes, CEL's `size` counts code points |
| `len(l)`, `len(b)` on a list or bytes | `size(x)` | |
| `"a=\(x)"` | `"a=" + string(x)` | |
| `strings.Contains/HasPrefix/HasSuffix/TrimSpace/Join/Split` | `contains/startsWith/endsWith/trim/join/split` | agree on every input |
| `strings.SliceRunes(s, i, j)` | `s.substring(i, j)` | both count code points |
| `strings.Replace(s, a, b, -1)`, `(s, a, b, n)` | `s.replace(a, b)`, `s.replace(a, b, n)` | |
| `strings.ToUpper/ToLower/ToTitle` | refused | CUE maps case per Unicode, CEL per ASCII |
| `strings.Index/LastIndex/IndexByte` | refused | CUE counts bytes, CEL code points |
| `(p & T) != _|_` | the test of `T` on the input's compiled type: bounds for an integer, equality or membership for constants, the member for a union | |
| a float literal | refused | floats are outside the compiled target |

## Extras checks

Data validated open may carry fields its version did not declare, which
travel in each struct's `extras`. When a later version declares one of those
labels at a struct the version before also had, the extra is promoted into
the field only if it is an instance of it. For each such label the result
carries a check: a CEL predicate over `x`, the extra's value typed `dyn`,
rendered from the field's constraint (a type test with its bounds or regular
expressions, an equality or membership for constants, element-wise for a
list, field-wise and closed for a struct, `||` across alternatives). A
constraint the renderer cannot express, such as a validator call
(`strings.MinRunes(1)`), rejects the lineage (`extras-check-unrenderable`).

## Artifact

`compiled.Encode` serializes the result as the message
`encoding/capnp/compiled/wire/compiled.capnp` declares (an unpacked,
single-segment Cap'n Proto message; `compiled.Decode` reads it back): the
namespace and lineage name, the schema text and the tool's layout, the
versions with their content hashes and root structs, every node with how its
fields map back to the lineage's labels and values, every edge with its
program and CEL, and the checks. Nothing in it is specific to Go, and the Go
model it round-trips through (`encoding/capnp/compiled`) does not depend on
CUE. The meta-schema is a published language (`compiled-artifact` in
`.thresh/thresh.cue`): a field may be appended, never renumbered, retyped or
removed. The goldens record each case's artifact digest, without the layout,
whose bytes carry the tool's version.

## Verdicts and rules

The walk gives each path of each version one of three verdicts: **compilable**
(no finding), **rejected** or **unknown**. A lineage's verdict is the worst of
its paths: rejected if any path is, else unknown if any is, else compilable.
Every finding reads

    schema <version>: <path> <detail> (rule <name>)
    lens <edge>: <path> <detail> (rule <name>)

for example

    schema 0.0: anint is int, an integer with no finite bounds; capnp integers are sized (int32, uint8, >=0 & <=100) (rule int-unbounded)
    lens 0.0->1.0: up calls strings.ToUpper, whose CEL counterpart changes ASCII only (rule cel-unrenderable)

| Rule | Code | Rejected when | What to do |
|---|---|---|---|
| `int-unbounded` | [E0710](errors/E0710.md) | an integer has no finite lower or upper bound, or bounds beyond 64 bits (`int`, `int & >=0`, `-1 \| int`) | bound it: `int32`, `uint8`, `int & >=0 & <=100` |
| `float` | [E0711](errors/E0711.md) | a value may be a float (`number`, `float`, `float32`, `float64`), anywhere, including as an alternative or a default | floats are outside the compiled target (#82); use an integer, or a string |
| `top` | [E0712](errors/E0712.md) | a value is `_`, including as a list element (`[...]`), a map value, or a disjunction alternative | declare the type |
| `disjunction-undiscriminated` | [E0713](errors/E0713.md) | no value decides the alternative: several non-constant alternatives of one kind (`=~"^a" \| =~"^b"`, `int8 \| int16`, `[...string] \| [...int8]`), a constant beside the open type of its kind (`"a" \| string`), or struct alternatives with no label that holds a distinct constant in each (`{a: string} \| {b: string}`, `{kind: string, …} \| {kind: string, …}`) | give struct alternatives a tag field with a distinct constant in each, make the alternatives of distinct kinds, or make them all constants |
| `name-collision` | [E0714](errors/E0714.md) | two labels, enum values or tagged alternatives of one struct sanitize to the same capnp name (`a_b` and `ab`; `"x_y" \| "xy"`; a field `kindKA` beside a field `kind` whose alternative tagged `"a"` is the struct `KindKA`) | rename one of them so the names differ once underscores are dropped |
| `lens-no-program` | [E0715](errors/E0715.md) | a lens uses a construct the IR does not carry, so it has no program (a list literal inside an expression) | write the lens in the forms the lens language proves |
| `cel-unrenderable` | [E0716](errors/E0716.md) | an expression of a lens has no faithful CEL rendering (the refused rows of the table above), or its rendering does not type-check | use the rewritable operation (`quo` for `/`, `strings.Contains` for `strings.Index`) |
| `extras-check-unrenderable` | [E0717](errors/E0717.md) | a label a version newly declares has a constraint the acceptance check cannot express: a validator call, a pattern constraint, `_` | constrain the field with types, bounds, regular expressions and constants |

| Rule | Code | Unknown when | What to do |
|---|---|---|---|
| `unknown-pattern-labels` | [E0730](errors/E0730.md) | a struct has pattern constraints other than a lone `[string]: T` beside no declared field: a regular expression, a pattern beside declared fields, several patterns, a narrower key type | declare the fields, or move the map to its own field |
| `unknown-list-heterogeneous` | [E0731](errors/E0731.md) | a closed list's elements do not all map to one type (`[string, int8]`) | capnp has no tuple; use a struct |

Every rule has a recorded case in `encoding/capnp/testdata`, which also
records the schema each accepted shape compiles to, the generation each
transition produces, each edge's program and CEL, the checks, the artifact's
digest, and the layout the capnp tool computes for it.

## Stability of verdicts, ordinals and IDs

Every verdict, ordinal and type ID is part of what a compiled artifact relies
on: a verdict that moves stops a lineage compiling or admits a shape no reader
handles, and an ordinal or ID that moves misreads every message already
written. The stability policy is the one in `docs/invariants.md`: before thema
v1 these may change between releases, and from v1 a lineage that compiles
under one release compiles identically under every later one. The goldens in
`encoding/capnp/testdata` are declared as a kernel (`capnp-verdicts` in
`.thresh/thresh.cue`) so that a change to them is loud.

## Layout

`Compile` runs `capnp compile -o-` on the schema text and embeds the
`CodeGeneratorRequest` it produces, which carries the authoritative layout of
every struct: data and pointer section sizes, each field's offset, each
union's discriminant. The request's bytes carry the tool's version, so the
goldens record a decoded rendering of it instead. `WithoutLayout()` skips the
tool; `Tool(path)` names it; tests skip the layout goldens where the tool
is not on `PATH`, and CI installs it.
