# The authoring form

This is the reference for the authoring form: the files a lineage author writes, and the language lenses are written in. For a guided introduction to lineages, start with the [overview](overview.md) and the [authoring tutorial](authoring.md), which show the normal form that every thema tool consumes. The authoring form compiles to that normal form, and decompiles back from it, without loss. What translation then does with each field of an instance is defined in [translation](translation.md).

Every `thema` command that takes a lineage with `-l` accepts a lineage's directory in this form, and `thema lineage fmt -l <dir>` lays out its files in place. In Go, `load.AuthoringLineage` loads, compiles and binds a directory in one call, and the `authoring` package exposes the pieces: the files, the compiled normal form, the decompiler and the formatter.

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
- Only files ending in `.thema` are read; anything else in the directory is left alone. The playground uses this to keep an `example.cue` beside a lineage that it offers as an example (see `thema playground examples --help`).
- Whether a version is minor or major is not the author's call. thema checks each version against the one before it under the [backwards compatibility](invariants.md#backwards-compatibility) rules. A file named as a minor version must be compatible with its predecessor, and a file named as a major version must not be. A file named the wrong way is an error that says which version number it should have.

A version file holds at most three fields. A field is a name, a colon and a value; a block is a value written in braces, holding fields of its own.

```cue
Schema: {
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
		secondfield: _drop
	}
}
```

- `Schema` is the version's schema, written as a schema is in the normal form.
- `ForwardLens` maps instances of the previous version to this one. Only a major version has one: a minor version is backwards compatible, so translating to it needs no lens.
- `BackwardLens` maps instances of this version to the previous one. Every version after the first has one. It is derived from the forward lens unless written, as described under [the backward lens](#the-backward-lens).

Each of the three is written as a block in braces. The file has no package clause. It may import CUE's standard packages for its `Schema`'s constraints, such as `strings.MinRunes(1)` or `time.Format(time.RFC3339)`; it imports nothing else, and only what the `Schema` uses, since a lens calls the `strings` functions without an import. A standard constraint is written alone, `name: strings.MinRunes(1)`, since it already says the value is a string.

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

The body holds the target schema's fields. Each entry is a *mapping*: it names a field of the target and says where that field's value comes from. A field of the instance the lens takes is written by its path, `v0_0.before`, or `v0_0.meta.owner` for a field inside a field.

### Mappings

| Written as | Meaning |
|---|---|
| `after: v0_0.before` | **Move.** The field takes the value at a path in the source instance. |
| `kind: "v2"` | **Constant.** The field takes a fixed value: a number, a string, `true`, `false`, `null`, or a list of these. The value must be one the target field accepts. |
| `area: v0_0.width * v0_0.height` | **Computed.** The field takes a value computed from source fields: arithmetic (`+`, `-`, `*`, and whole-number division with `quo`, `rem`, `div`, `mod`), comparisons, `&&`, `||` and `!`, joining strings with `+` or `"\(...)"`, `len` for the length of a string or list, and the string functions `strings.ToUpper`, `ToLower`, `TrimSpace`, `Trim`, `TrimLeft`, `TrimRight`, `TrimPrefix`, `TrimSuffix`, `Replace`, `HasPrefix`, `HasSuffix` and `Contains`. These are exactly the computations thema can prove never fail; anything else is an error naming what is allowed. |
| `meta: {owner: v0_0.name}` | **Nested body.** A block is a body for the target field it names, holding mappings for the fields inside. To copy the rest of its fields from a different part of the source, name that part first: `{v0_0.m, x: 1}`. |
| `legacy: _drop` | **Drop.** The source field named by the key has no place in the target and is dropped. A field the target requires cannot be dropped. |
| `nick: _coalesce(v0_0.nick, v0_0.name)` | **Fallback.** The source field is optional; when the instance lacks it, the fallback is used. It is SQL's `COALESCE` with absence in place of null: a field the instance has is used whatever its value, `null` included. The first argument may also be a computed value, `_coalesce(v0_0.count * 2, 0)`, and the fallback is used when any field it reads is absent. The fallback is a constant, or a field the source always has. A `_coalesce` whose first argument reads only fields the source always has is an error. |
| `kind: _match(v0_0.kind, {"a": 1, [_]: 0})` | **Match.** The source value is matched against the arms. A value an arm matches becomes that arm's constant. A value no arm matches passes through unchanged where the target accepts it, and otherwise takes the wildcard arm `[_]`, which must declare a lacuna. Without a wildcard arm the arms must be exhaustive: every value the source may hold is matched or accepted. |
| `items: _each(v0_0.items, {name: _item.title})` | **Each.** A list is mapped element by element. The block is a lens body over `_item`, the element, and every mapping above is available in it, `_each` included: in `_each(v0_0.steps, {fields: _each(_item.fields, {...})})`, the inner `_item` is an element of the step's `fields`. Instead of a block, the body may copy the element, `_each(v0_0.tags, _item)`, or a field of it, `ids: _each(v0_0.users, _item.id)`; the copy is the whole target element, so the field must be one every element has, and the target's element must accept every value of it, as for any copy. Since the copy is the whole element, the rest of the element is lost: in either direction every other field is dropped, with a derived `DroppedField` lacuna naming each element that had it. `_each` applies to lists of any length (`[...T]`). |

A mapping's key is a plain field name, and its value is one of the forms above. Nothing else attaches to a mapping.

The operations, `_drop`, `_coalesce`, `_match` and `_each`, and `_item` inside an `_each` body, start with `_` so that they can never be mistaken for a field. In CUE, a name that starts with `_` is a hidden field, which is not data, and a lens body never declares one: a key there names a field of the target. A field whose name starts with `_` is mapped by quoting its key, `"_x": v0_0.y`.

### What is written and what is implied

What a lens does to an instance is defined in [translation](translation.md), field by field. The rules below are derivations: what the authoring form writes into the compiled lens when the author says nothing. Each one exists to produce a rule of translation, listed under [what each derivation must produce](#what-each-derivation-must-produce), and is correct exactly when it does. A case translation does not settle is settled there first, never here.

- **Copies are implicit.** A field both schemas declare, that no mapping mentions, is copied. The copy is made as high as the two schemas agree: a field whose contents the target declares whole is copied whole, and one whose contents differ is descended into. A copy, like a move, must fit: the target field has to accept every value the source field may hold, or the field must be mapped.
- **`...` declares nothing.** An open struct admits names it does not declare, and a lens treats a name it only admits as absent from the schema. A field one version declares and the other only admits is added or removed, exactly as between closed structs: it is never copied implicitly, the derived backward lens drops a field the new version added with a `DroppedField` lacuna, and a major version's forward lens must move or drop a field it removed. A lens reads, drops and writes only fields its schemas declare.
- **Optional sources are used only when present.** A copy, a move, a computed value or an `_each` that reads a field the source may omit is written only when the instance has it. Where the target requires the field, the author must say what to use when it is absent, with `_coalesce`; an `_each` has no fallback yet, so it maps an optional list only into a list the target does not require.
- **Translation keeps the form a struct had.** A struct the lens copies from an optional struct of the source is written only when the instance has it: absent stays absent, and empty stays empty. That holds for a struct copied whole, one the copy descends into, and a nested body whose every mapping reads from beneath the source's struct of the same name (`q: {b: v0_0.q.a}`, or `q: {y: _drop}`). A nested body that writes anything else, such as a constant, is written always, and so is a struct the target requires.
- **Removed fields must be accounted for.** In the forward direction, a field the source declares and the target does not must be moved somewhere or dropped. A forgotten rename is an error, not a silent loss. A struct the target lacks is one field: it is accounted for as a whole, unless a mapping reads something beneath it, in which case each of its other fields is. The one exception is an `_each` whose body copies a field of the element, `_each(v0_0.users, _item.id)`: the copy says what the element becomes, and the body has nowhere to write a drop, so the element's other fields are dropped by derivation, each with its `DroppedField` lacuna. An `_each` whose body is a block must account for them as any body does.
- **Required fields must be produced.** A field the target requires that no mapping and no copy produces is an error, naming the lens and the field.

### The backward lens

The derived backward lens is a derivation like those above, and must produce what [translation](translation.md) says for every field of the newer version.

The backward lens is derived from the forward lens:

- every move whose source is used once is inverted (`after: v0_0.before` forward derives `before: v1_0.after` backward);
- every field the new version added is dropped, with a derived `DroppedField` lacuna saying so; a struct the new version added is dropped whole, with one lacuna about the struct, reported where it is present and not empty, whose value holds the fields beneath it;
- fields both versions share are copied, as always.

What the author writes in `BackwardLens` overrides the derivation at the same key or beneath it. Writing `x: _drop` for a field the derivation would have moved suppresses the move. `_coalesce`, `_match` and `_each` have no derived inverse, so a forward lens that uses them usually needs a written backward lens.

### What each derivation must produce

| Derivation | The rule of translation it produces |
|---|---|
| Implicit copies: a field both schemas declare is copied | the field is mapped ([B1](translation.md#fields-the-source-declares)), so it never reaches [B6](translation.md#fields-the-source-declares), which would drop it with a `DroppedField` |
| Optional sources are used only when present: a mapping that reads an optional field is written under a presence test | nothing is written from an absent field ([B3](translation.md#fields-the-source-declares)), and an absent optional struct stays absent ([E1](translation.md#structs-and-presence)) |
| Required fields must be produced | a field the target requires with no default is written for every instance ([D3](translation.md#fields-only-the-target-declares)) |
| Removed fields must be accounted for | none: this is an authoring rule, which catches a forgotten rename, not a translation rule ([G3](translation.md#steps-and-direction)) |
| The backward lens inverts a move whose source is used once | the field is mapped ([B1](translation.md#fields-the-source-declares)) |
| The backward lens drops a field the newer version added, with a `DroppedField` | into a closed struct, the field is dropped with a lacuna ([B5](translation.md#fields-the-source-declares)), and a struct is dropped whole with one lacuna ([E2](translation.md#structs-and-presence)). Into an open struct, the field is left for translation to carry ([B4](translation.md#fields-the-source-declares)); main still drops it ([#196](https://github.com/sdboyer/thema/pull/196)) |
| A drop derives a `DroppedField` | [B2](translation.md#fields-the-source-declares) |
| The condition a lacuna's mapping implies, and `when` replacing it | [F1–F4](translation.md#lacunas-the-lens-lists) |

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
		count: _coalesce(v0_0.count, 0)
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
			type:    PrecisionLoss
			message: "counts above 1000 are rounded downstream"
			when:    v0_0.count > 1000
		}]
	}
}
```

A lacuna has a `type`, a `message`, and optionally a `when`. The types are `Placeholder`, `DroppedField`, `LossyFieldMapping`, `ChangedDefault`, `PrecisionLoss`, `AmbiguousMapping` and `Defaulted`.

A key must name a mapping the author wrote. A copy the author wants a lacuna on is written out as a move from the field to itself, as `name: v0_0.name` above.

**When a lacuna applies.** Without a `when`, a lacuna applies under the condition its mapping implies:

| Mapping | The lacuna applies when |
|---|---|
| constant; move or computed value reading only fields the source always has | always |
| move or computed value reading a field the source may omit | the fields it reads were present, so that it was written |
| `_coalesce` | the fallback was used |
| `_match` | the wildcard arm applied |
| `_drop` | the dropped field was present, and for a struct or a list, not empty |

A `when` replaces the implied condition. It is a comparison or combination of source fields, built from the usual operators (`==`, `!=`, `<`, `>`, `<=`, `>=`, `&&`, `||`, `!`, `+`, `-`, `*`), `len` for the length of a string or list, and whole-number division (`quo`, `rem`, `div`, `mod`). Three rules govern the fields it reads:

- **A `when` that reads a field the source may omit concerns only the instances that have it.** `when: v0_0.count > 1000` above applies when `count` is present and exceeds 1000, and never when it is absent. The author does not test for presence.
- **A field the source may omit, named bare, tests its presence.** `when: v0_0.flag` applies to the instances that have `flag`, whatever its value. To test a bare optional field's value, compare it: `when: v0_0.flag == true`.
- **A required true-or-false field named bare is its value.** `when: v0_0.deprecated` applies when `deprecated` is true. Any other field named bare is an error: compare it.

**The fields a lacuna reports.** A lacuna reports the source fields its mapping reads and the target field it writes, each only where the translation has it. A move or computed value that reads a field the source may omit writes its target field only where the instance has what it reads, so a lacuna on it reports the target field only there. Without a `when`, such a lacuna applies only where the mapping writes, so an instance that lacks what it reads raises none; a `when` replaces that condition, as it does any other.

**Drops.** A drop derives a `DroppedField` lacuna by itself. A `DroppedField` lacuna declared on a drop replaces the derived message. A lacuna of another type on a drop is added beside the derived one. A drop of a struct derives one lacuna, about the struct, not one for each field beneath it. An empty struct or list carries no information: an empty container and an absent one are not told apart as data, so dropping an empty one raises no lacuna.

**Matches.** A wildcard arm must have a lacuna: one on the match's key without a `when`, which applies when the arm does. A lacuna on an arm other than the wildcard is written with a `when` naming the value, `when: v0_0.kind == "a"`. A match without a wildcard arm has no implied condition, so every lacuna on it needs a `when`.

**Inside `_each`.** A lacuna on a mapping in an `_each` body is keyed through the list, `items: {legacy: [...]}`, and through each list in turn for an `_each` inside another, `steps: {fields: {legacy: [...]}}`. Only the `DroppedField` refinement of a drop in the body is supported today; it applies to the drop's derived lacuna, which is reported once for the whole list, naming every element that had the field (`steps[0].fields[1].legacy`).

## What is not accepted yet

Each of these is refused with an error naming what to write instead, rather than compiled approximately:

- **A reference to the whole source instance from inside an `_each` body.** The body reads only `_item`, the innermost element.
- **An `_each` over a list the source may omit, into a list the target requires** (`l!: [...T]`, or a list with elements it must have). `_each` has no fallback for an absent list.
- **An `_each` body other than a block or a copy** of the element or a field of it: a computed value, `_match`, `_coalesce` or a constant ([#168](https://github.com/sdboyer/thema/issues/168)).
- **Lacunas inside `each`** other than the `DroppedField` refinement ([#103](https://github.com/sdboyer/thema/issues/103)).
- **A match passthrough into a target whose type refers to other fields.** The passthrough tests the value against the target's type, which must stand on its own to be restated; match every value with an arm instead.
- **List-to-map and map-to-list mappings** ([#102](https://github.com/sdboyer/thema/issues/102)).

## What it compiles to

The compiler produces the normal form that the tutorial shows: a `thema.Lineage` with schemas keyed by version and lenses keyed by edge. A lens's fields keep their names, `v0_0` and `v1_0`, so a normal-form lens reads as its authoring form did. Implicit copies are written out, a copy of an optional field or struct is written under a presence test, `_coalesce` and `_match` become one branch per case, `_each` becomes a loop over the list, and each lacuna becomes an entry of the lens's `lacunas` list with its condition and the fields it reports.

Every part of the normal form is positioned in the authoring file it came from. An error from the evaluator, from binding or from translation points at the author's line, and the compiler's own errors are worded in these terms: the file, the lens block and the mapping.

Binding a lineage proves every lens [total](invariants.md#lens-totality). Every form the compiler emits is one the proof decides, so a lineage that compiles binds.

## Layout

The authoring form has one layout: every block keeps its braces, and every field has its own line. The formatter produces it, and decompiling a formatted file gives back its text exactly. A file in another layout compiles the same; formatting it is a layout change and nothing else.
