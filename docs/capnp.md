# Compiling to Cap'n Proto

`encoding/capnp` compiles a bound lineage to a Cap'n Proto schema: one struct
per major version, split into a further struct wherever a minor version is not
capnp-append-compatible with the one before it, with ordinals and type IDs
derived from the lineage's history so that appending a version never moves
one. The schema text is deterministic, and the `capnp` tool's layout of it
(the `CodeGeneratorRequest` of `capnp compile -o-`) is embedded in the result.

The compiler consumes the normal form only: each version's schema, read
through the CUE API the way the compatibility checker reads it (field forms
through `compat.FormOf`, alternatives through `compat.Branches`, integer bounds
through `Expr()`). It never decides anything from a value's source text.

Not every valid lineage compiles. The compiler is its own checker, in the
style of the compatibility and totality checkers: a structural walk, findings
with stable rule IDs, and three-valued verdicts. A lineage with a construct
capnp cannot carry is **rejected**; one with a construct this compiler does not
carry yet is **unknown**. Both fail compilation, and every finding names its
rule, so that it can be looked up below.

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

Each version records its root struct. A later part of this work (#89) adds
copy programs for the minor steps that cross a generation, so that a reader
of the earlier struct can be given the later data.

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

## Verdicts and rules

The walk gives each path of each version one of three verdicts: **compilable**
(no finding), **rejected** or **unknown**. A lineage's verdict is the worst of
its paths: rejected if any path is, else unknown if any is, else compilable.
Every finding reads

    schema <version>: <path> <detail> (rule <name>)

for example

    schema 0.0: anint is int, an integer with no finite bounds; capnp integers are sized (int32, uint8, >=0 & <=100) (rule int-unbounded)

| Rule | Rejected when | What to do |
|---|---|---|
| `int-unbounded` | an integer has no finite lower or upper bound, or bounds beyond 64 bits (`int`, `int & >=0`, `-1 \| int`) | bound it: `int32`, `uint8`, `int & >=0 & <=100` |
| `float` | a value may be a float (`number`, `float`, `float32`, `float64`), anywhere, including as an alternative or a default | floats are outside the compiled target (#82); use an integer, or a string |
| `top` | a value is `_`, including as a list element (`[...]`), a map value, or a disjunction alternative | declare the type |
| `disjunction-undiscriminated` | no value decides the alternative: several non-constant alternatives of one kind (`=~"^a" \| =~"^b"`, `int8 \| int16`, `[...string] \| [...int8]`), a constant beside the open type of its kind (`"a" \| string`), or struct alternatives with no label that holds a distinct constant in each (`{a: string} \| {b: string}`, `{kind: string, …} \| {kind: string, …}`) | give struct alternatives a tag field with a distinct constant in each, make the alternatives of distinct kinds, or make them all constants |
| `name-collision` | two labels, enum values or tagged alternatives of one struct sanitize to the same capnp name (`a_b` and `ab`; `"x_y" \| "xy"`; a field `kindKA` beside a field `kind` whose alternative tagged `"a"` is the struct `KindKA`) | rename one of them so the names differ once underscores are dropped |

| Rule | Unknown when | What to do |
|---|---|---|
| `unknown-pattern-labels` | a struct has pattern constraints other than a lone `[string]: T` beside no declared field: a regular expression, a pattern beside declared fields, several patterns, a narrower key type | declare the fields, or move the map to its own field |
| `unknown-list-heterogeneous` | a closed list's elements do not all map to one type (`[string, int8]`) | capnp has no tuple; use a struct |

Every rule has a recorded case in `encoding/capnp/testdata`, which also
records the schema each accepted shape compiles to, the generation each
transition produces, and the layout the capnp tool computes for it.

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
