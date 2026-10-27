# The authoring form

This is the reference for the authoring form: the files a lineage author writes, and the language lenses are written in. For a guided introduction to lineages, start with the [overview](overview.md) and the [authoring tutorial](authoring.md), which show the normal form that every thema tool consumes. The authoring form compiles to that normal form, and decompiles back from it, without loss.

The compiler is part of thema's Go library. A command-line entry point and an importable package are in progress ([#101](https://github.com/sdboyer/thema/issues/101)).

## Files

A lineage is a directory. The directory's base name is the lineage's name, and it holds one file per schema version:

```
ship/
  1.0.thema
  history/
    0.0.thema
    0.1.thema
```

- A version file is named `<major>.<minor>.thema`. The current version sits at the top of the directory; every earlier version sits under `history/`.
- Versions are contiguous from `0.0`: each file is either the next minor version of the one before it, or the next major version with minor `0`.
- Whether a version is minor or major is not the author's call. thema checks each version against the one before it under the [backwards compatibility](invariants.md#backwards-compatibility) rules. A file named as a minor version must be compatible with its predecessor, and a file named as a major version must not be. A file named the wrong way is an error that says which version number it should have.

A version file is CUE syntax holding at most three fields, with no package clause and no imports:

```cue
schema: {
	firstfield:  string
	secondfield: int
}

ForwardLens: {
	v0_0: _
	v1_0: {
		secondfield: -1
	}
	lacunas: {
		secondfield: [{
			type:    Placeholder
			message: "-1 is a placeholder; replace it before persisting"
		}]
	}
}

BackwardLens: {
	v1_0: _
	v0_0: {
		secondfield: drop
	}
}
```

- `schema` is the version's schema, as in the normal form.
- `ForwardLens` maps instances of the previous version to this one. Only a major version has one: a minor version is backwards compatible, so translating to it needs no lens.
- `BackwardLens` maps instances of this version to the previous one. Every version after the first has one. It is derived from the forward lens unless written, as described under [the backward lens](#the-backward-lens).

Every struct is written with its braces. CUE's shorthand `a: b: c` is refused wherever it appears in a version file, so that a lens body, a `lacunas` entry and a schema all read the same way.

## Lenses

A lens reads like a function from one version to the next:

```cue
ForwardLens: {
	v0_0: _
	v1_0: {
		after: v0_0.before
	}
}
```

The field whose value is `_` names the instance the lens takes, after its version: `v0_0` is an instance of version `0.0`. The other version field is the body, and produces the instance the lens returns, shaped like that version's schema. An optional `lacunas` field declares what the mappings lose, described under [lacunas](#lacunas).

The body is a struct whose fields are the target schema's fields. Each entry is a *mapping*: it names a field of the target and says where that field's value comes from.

### Mappings

| Written as | Meaning |
|---|---|
| `after: v0_0.before` | **Move.** The field takes the value at a path in the source instance. |
| `kind: "v2"` | **Constant.** The field takes a literal: a number, a string, a boolean, `null`, or a list of literals. The literal must be an instance of the target field. |
| `meta: {owner: v0_0.name}` | **Nested body.** A struct is a body for the target's struct field. It may embed a reference, `{v0_0.m, x: 1}`, to copy the rest of its fields from a different source struct. |
| `legacy: drop` | **Drop.** The source field named by the key has no place in the target and is dropped. A field the target requires cannot be dropped. |
| `nick: orelse(v0_0.nick, v0_0.name)` | **Fallback.** The source field is optional; when the instance lacks it, the fallback is used. The fallback is a constant or a reference to a field the source always has. An `orelse` over a field the source always has is an error. |
| `kind: match(v0_0.kind, {"a": 1, [_]: 0})` | **Match.** The source value is matched against the arms. A value an arm matches becomes that arm's constant. A value no arm matches passes through unchanged where the target accepts it, and otherwise takes the wildcard arm `[_]`, which must declare a lacuna. Without a wildcard arm the arms must be exhaustive: every value the source may hold is matched or accepted. |
| `items: each(v0_0.items, {name: item.title})` | **Each.** A list is mapped element by element. The body is a lens body over `item`, the element, and every mapping above is available in it. `each` applies to open lists (`[...T]`) only. |

A mapping's key is a plain field name. Nothing else attaches to a mapping: no attributes, no comprehensions, no `let`.

### What is written and what is implied

- **Copies are implicit.** A field both schemas declare, that no mapping mentions, is copied. The copy is made at the shallowest path the two schemas share: a struct the target declares whole is copied whole, and one whose fields differ is descended into.
- **Optional sources are copied only when present.** A copy or a move from an optional source field is written under a presence test. Where the target requires the field, the author must say what to use when it is absent, with `orelse`.
- **Removed fields must be accounted for.** In the forward direction, a field the source declares and the target does not must be moved somewhere or dropped. A forgotten rename is an error, not a silent loss.
- **Required fields must be produced.** A field the target requires that no mapping and no copy produces is an error, naming the lens and the field.

### The backward lens

The backward lens is derived from the forward lens:

- every move whose source is used once is inverted (`after: v0_0.before` forward derives `before: v1_0.after` backward);
- every field the new version added is dropped, with a derived `DroppedField` lacuna saying so;
- fields both versions share are copied, as always.

What the author writes in `BackwardLens` overrides the derivation at the same key or beneath it. Writing `x: drop` for a field the derivation would have moved suppresses the move. `orelse`, `match` and `each` have no derived inverse, so a forward lens that uses them usually needs a written backward lens.

## Lacunas

A lacuna records something a translation loses or guesses. The `lacunas` field of a lens is keyed exactly like the body, and each key lists the lacunas of that mapping:

```cue
ForwardLens: {
	v0_0: _
	v1_0: {
		name: v0_0.name
		meta: {
			team: "unassigned"
		}
		count: orelse(v0_0.count, 0)
	}
	lacunas: {
		name: [{
			type:    LossyFieldMapping
			message: "names longer than 64 characters are truncated downstream"
			when:    len(v0_0.name) > 64
		}]
		meta: {
			team: [{
				type:    Placeholder
				message: "0.0 has no team; \"unassigned\" is a placeholder"
			}]
		}
		count: [{
			type:    Placeholder
			message: "count was absent; 0 is a placeholder"
		}, {
			type:    AmbiguousMapping
			message: "legacy records never recorded a count, so 0 may be real"
			when:    v0_0.name == "legacy"
		}]
	}
}
```

A lacuna has a `type`, a `message`, and optionally a `when`. The types are `Placeholder`, `DroppedField`, `LossyFieldMapping`, `ChangedDefault`, `PrecisionLoss`, `AmbiguousMapping` and `Defaulted`.

A key must name a mapping the author wrote. A copy the author wants a lacuna on is written out as a move from the field to itself, as `name: v0_0.name` above.

**When a lacuna applies.** Without a `when`, a lacuna applies under the condition its mapping implies:

| Mapping | The lacuna applies when |
|---|---|
| move, constant | always |
| `orelse` | the fallback was used |
| `match` | the wildcard arm applied |
| `drop` | the dropped field was present |

A `when` replaces the implied condition. It may read fields the source always has, test any source field's presence (`v0_0.x != _|_`), and use CUE's builtins and operators. It may not read an optional field, since nothing yet proves such a condition evaluates when the field is absent; test presence instead.

**Drops.** A drop derives a `DroppedField` lacuna by itself. A `DroppedField` lacuna declared on a drop replaces the derived message. A lacuna of another type on a drop is added beside the derived one.

**Matches.** A wildcard arm must have a lacuna: one on the match's key without a `when`, which applies when the arm does. A lacuna on an arm other than the wildcard is written with a `when` naming the value, `when: v0_0.kind == "a"`. A match without a wildcard arm has no implied condition, so every lacuna on it needs a `when`.

**Inside `each`.** A lacuna on a mapping in an `each` body is keyed through the list, `items: {legacy: [...]}`. Only the `DroppedField` refinement of a drop in the body is supported today; it applies to the drop's derived lacuna, which is aggregated over the list's elements.

## What is not accepted yet

Each of these is refused with an error naming what to write instead, rather than compiled approximately:

- **Computed expressions.** A mapping's value is a reference, a constant, a body or one of the four operations. Arithmetic, string functions and `orelse` over a computed value are the next slice of the language.
- **Imports.** A version file imports nothing, so schema constraints from CUE's builtin packages (`strings.MinRunes`, `time.Format`) cannot be written yet ([#104](https://github.com/sdboyer/thema/issues/104)).
- **Nested `each`**, and references to the whole source instance from inside an `each` body.
- **Lacunas inside `each`** other than the `DroppedField` refinement ([#103](https://github.com/sdboyer/thema/issues/103)).
- **A match passthrough into a target whose type is not self-contained.** The passthrough tests the value against the target's type, so a type that refers to other fields cannot be restated; match every value with an arm instead.
- **List-to-map and map-to-list mappings** ([#102](https://github.com/sdboyer/thema/issues/102)).

## What it compiles to

The compiler produces the normal form that the tutorial shows: a `thema.Lineage` with schemas keyed by version and lenses keyed by edge. A lens's fields keep their names, `v0_0` and `v1_0`, so a normal-form lens reads as its authoring form did. Implicit copies are written out, a presence test becomes an `if`, `orelse` becomes two arms, `match` becomes an arm per value plus the passthrough and wildcard arms, `each` becomes a list comprehension, and each lacuna becomes an entry of the lens's `lacunas` list with its condition and the fields it reports.

Every node of the normal form is positioned in the authoring file it came from. An error from the evaluator, from binding or from translation points at the author's line, and the compiler's own errors are worded in these terms: the file, the lens block and the mapping.

Binding a lineage proves every lens [total](invariants.md#lens-totality). Two forms the compiler emits are not yet proven: the list comprehension `each` compiles to ([#98](https://github.com/sdboyer/thema/issues/98)), and a `when` outside the pattern forms the proof understands, such as `len(v0_0.name) > 64` ([#99](https://github.com/sdboyer/thema/issues/99)). A lineage using either compiles but does not bind until those land.

## Layout

The authoring form has one layout: every struct keeps its braces, and every field has its own line. The formatter produces it, and decompiling a formatted file gives back its text exactly. A file in another layout compiles the same; formatting it is a layout change and nothing else.
