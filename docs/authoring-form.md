# The authoring form

This is the reference for the authoring form: the files a lineage author writes, and the language lenses are written in. For a guided introduction to lineages, start with the [overview](overview.md) and the [authoring tutorial](authoring.md), which show the normal form that every thema tool consumes. The authoring form compiles to that normal form, and decompiles back from it, without loss. What translation then does with each field of an instance is defined in [translation](translation.md).

Every `thema` command that takes a lineage with `-l` accepts a lineage's directory in this form, `thema lineage fmt -l <dir>` lays out its files in place, `thema lineage tip -l <dir>` reports the version its tip cuts as, and `thema lineage compile -l <dir>` writes its normal form to [`lineage.cue`](#lineagecue). In Go, `load.AuthoringLineage` loads, compiles and binds a directory in one call, `load.AuthoringCollection` does the same for a [collection](#collections) of them, and the `authoring` package exposes the pieces: the files, the compiled normal form, the tip, the decompiler and the formatter.

## Files

A lineage is a directory. The directory's base name is the lineage's name. It holds the lineage's history, one file per version that has been cut, and its tip, the version being worked on:

```
ship/
  tip.thema
  history/
    0.0.thema
    0.1.thema
    1.0.thema
```

- A version that has been cut is named `history/<major>.<minor>.thema`. Data has been written against it, so it never changes. A numbered file at the top of the directory is an error (`E0148`) that says to move it under `history/`.
- Versions are contiguous from `0.0`: each file is either the next minor version of the one before it, or the next major version with minor `0`.
- Only files ending in `.thema` are read; anything else in the directory is left alone. The playground uses this to keep an `example.cue` beside a lineage that it offers as an example (see `thema playground examples --help`). In a [collection](#collections), the directory also holds the generated [`lineage.cue`](#lineagecue).
- Whether a version is minor or major is not the author's call. thema checks each version against the one before it under the [backwards compatibility](invariants.md#backwards-compatibility) rules. A file named as a minor version must be compatible with its predecessor, and a file named as a major version must not be. A file named the wrong way is an error that says which version number it should have.

### The tip

`tip.thema` is the one file an author edits. The development path requires one, and decompiling writes one. It is written like any version file, and its version is never written down: thema derives it by checking the tip against the newest version under `history/`. If the tip is backwards compatible with that version, it is the next minor version, and otherwise the next major version. With no history at all, it is `0.0`. Tools show it as `tip (cuts as 2.1)`.

- The tip's lenses are its edge to the newest version in history. In them, the tip's instance is named `tip`, since it has no version number yet: `ForwardLens: {v1_0: _, tip: {...}}` and `BackwardLens: {tip: _, v1_0: {...}}`. `tip` names nothing in a file under `history/`, and a tip's lens that names its instance by a version number is an error (`E0111`). The other end names the tip's parent, the newest version under `history/`. A lens written against an older parent, as after a rebase onto a branch that cut a newer version, is an error (`E0150`) that names the parent to declare; its mappings then need review against that version's schema.
- A tip is *clean* when its `Schema` equals the newest version's, and it declares no lenses. Formatting, comments and field order do not count. A clean tip has nothing to cut, and tools show it as `tip (clean at 2.0)`. `thema lineage tip -l <dir> --check-clean` fails with `E1003` for a tip that is not clean, for CI to check that the tip was cut before merging; in Go, `authoring.CheckClean`.
- Cutting a tip writes it to `history/` at its derived version. It is not yet automated.

A lineage is read in one of two ways:

- **The history alone.** This is what is published and served: `load.AuthoringLineage`, `authoring.Compile`, and every CLI command by default. The tip is not read, and a directory without one loads.
- **With the tip, for development.** `load.DevelopmentLineage`, `authoring.CompileDevelopment` with `authoring.BindDevelopment`, the CLI's `--tip` flag, and the playground bind the tip as the newest version, at the version it cuts as. Unless the tip is clean, that version is *provisional*: it is the tip, and may still change. A directory with no `tip.thema` is an error (`E0149`). While the tip is being written, a lens its edge needs but does not write yet is not an error but a warning (`E1001`). The lineage still binds, and a translation that needs the missing lens fails with `E1002`. A lens the tip does write is checked as any other. Each of these errors unwraps to `thema/errors.ErrProvisional`. Human-readable output names the tip's version `tip (cuts as 2.1)`; JSON output names it `"tip"`, with the derived version in `cutsAs` ([translate](reference/operations.md#translate)).

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

## Collections

A collection is a directory of lineages, such as the set of objects one API or one service serves. Each of its immediate subdirectories is a lineage's directory, laid out as above, and named for its lineage:

```
fleet/
  ship/
    tip.thema
    history/
      0.0.thema
      1.0.thema
  boat/
    tip.thema
    history/
      0.0.thema
```

- **Flat.** A subdirectory is a lineage when it holds a version file, its `tip.thema` at its top or a frozen version under its `history/`. Any other subdirectory is not part of the collection, and nothing beneath it is read: a lineage two levels down is not in it.
- **Names are directories.** A lineage's name is its directory's, so two lineages of a collection never share one. The name must still be a valid lineage name (`E0137`).
- **Other files are left alone.** Files at the top of the collection, such as a README, are not read, and neither are files in a lineage's directory that are not version files, such as the playground's `example.cue`. A version file at the top of the collection makes the directory a lineage's, not a collection's (`E0624`).
- **Each lineage stands alone.** A lineage is read, compiled and bound on its own, and one that fails does not keep the others from binding. In Go, `load.AuthoringCollection` binds every lineage's history and `load.DevelopmentCollection` binds each with its tip. Both return a `load.Collection`: the lineages that bound, by name, and the error of each that did not, by name, positioned in that lineage's files. The call itself fails only when the directory is not a collection: it does not exist (`E0601`), is not a directory (`E0623`), or is a lineage's directory (`E0624`). `authoring.LoadCollection` reads a collection's files without compiling them.
- **No links.** A lineage's schemas cannot refer to another lineage of the collection, as they cannot refer to anything outside their own lineage. What a link between lineages would mean, and how it would version, is open ([#40](https://github.com/sdboyer/thema/issues/40)).

`thema playground examples <dir>` reads `<dir>` as a collection, and offers each lineage with an `example.cue` as an example.

### lineage.cue

Each lineage of a collection keeps its normal form beside its authoring files, in `lineage.cue`, the way a `.d.ts` sits beside a `.js`. The authoring files are for thema's tools; `lineage.cue` is for CUE, which can import a lineage by its directory's path and use its schemas as types, with no thema tooling:

```
fleet/
  ship/
    lineage.cue
    tip.thema
    history/
      0.0.thema
      1.0.thema
```

- **History only.** `lineage.cue` is every version under `history/`, compiled, and never the tip: what CUE imports is what was cut, which never changes.
- **A package named for the lineage.** It declares `package ship`, imports `github.com/sdboyer/thema` and the standard packages the history imports, and embeds `thema.Lineage`, so that the package's value is the lineage. A lineage named for a CUE keyword gets a trailing underscore, `package for_`, and is imported as `"<module>/fleet/for:for_"`. Nothing else in the directory is part of the package: the version files are not CUE files, and the playground's `example.cue` declares no package.
- **Generated and committed.** `thema lineage compile -l <dir>` writes it, for one lineage or for each of a collection's, and lists the files it changed. Its first line marks it generated; it is never edited by hand. In Go, `authoring.GenerateLineageFile` produces its text from `authoring.Compile`'s result, for a tool that cuts a version to rewrite it.
- **Checked.** `load.AuthoringCollection` fails a lineage whose `lineage.cue` is missing or is not exactly what its history compiles to (`E0151`), saying which versions each holds or the first line that differs. `thema lineage compile --check -l <dir>` runs the same check without writing, for CI. The development path, `load.DevelopmentCollection` and the playground, does not check it: they bind the tip, which `lineage.cue` never holds, and a tip being worked on is no reason to fail. A lineage loaded on its own, with `load.AuthoringLineage` or `-l`, needs no `lineage.cue`, since nothing imports it.

To be importable, the collection sits in a CUE module that depends on thema's, as any CUE that imports thema does ([CUE modules](cue-modules.md)):

```cue
// cue.mod/module.cue
module: "example.com/apis"
language: version: "v0.18.0"
deps: "github.com/sdboyer/thema@v0": v: "v0.1.0"
```

and CUE elsewhere in that module, or in a module that depends on it, imports a lineage by its path:

```cue
package harbor

import "example.com/apis/fleet/ship"

manifest: ship.schemas["1"]["0"].schema & {
	firstfield:  "cargo"
	secondfield: 3
}
```

The `lineage.cue` files are checked against `thema.Lineage` as the thema that generated them has it, so `deps` names that thema's version, the version of the `thema` CLI or Go module that wrote them; thema's Go library, loading the module, requires exactly its own version anyway. Regenerating after upgrading thema, and bumping `deps` with it, keeps the two together. A lineage seen through another package's import, such as `flagship: ship` above, is a CUE value and not something to bind: its lenses' references resolve in the ship package, which binding reads as an import (`E0143`). To bind it in Go, load the lineage's own package.

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
- **Removed fields must be accounted for.** In the forward direction, a field the source declares and the target does not must be moved somewhere or dropped. A forgotten rename is an error, not a silent loss. The one exception is an `_each` whose body copies a field of the element, `_each(v0_0.users, _item.id)`: the copy says what the element becomes, and the body has nowhere to write a drop, so the element's other fields are dropped by derivation, each with its `DroppedField` lacuna. An `_each` whose body is a block must account for them as any body does.
- **Required fields must be produced.** A field the target requires that no mapping and no copy produces is an error, naming the lens and the field.

### The backward lens

The derived backward lens is a derivation like those above, and must produce what [translation](translation.md) says for every field of the newer version.

The backward lens is derived from the forward lens:

- every move whose source is used once is inverted (`after: v0_0.before` forward derives `before: v1_0.after` backward);
- every field the new version added is dropped, with a derived `DroppedField` lacuna saying so;
- fields both versions share are copied, as always.

What the author writes in `BackwardLens` overrides the derivation at the same key or beneath it. Writing `x: _drop` for a field the derivation would have moved suppresses the move. `_coalesce`, `_match` and `_each` have no derived inverse, so a forward lens that uses them usually needs a written backward lens.

### What each derivation must produce

| Derivation | The rule of translation it produces |
|---|---|
| Implicit copies: a field both schemas declare is copied | the field is mapped ([B1](translation.md#fields-the-source-declares)), so it never reaches [B6](translation.md#fields-the-source-declares), which would drop it with a `DroppedField` |
| Optional sources are used only when present: a mapping that reads an optional field is written under a presence test | nothing is written from an absent field ([B3](translation.md#fields-the-source-declares)), and an absent optional struct stays absent ([E1](translation.md#structs-and-presence)), not yet on main ([#176](https://github.com/sdboyer/thema/pull/176)) |
| Required fields must be produced | a field the target requires with no default is written for every instance ([D3](translation.md#fields-only-the-target-declares)) |
| Removed fields must be accounted for | none: this is an authoring rule, which catches a forgotten rename, not a translation rule ([G3](translation.md#steps-and-direction)) |
| The backward lens inverts a move whose source is used once | the field is mapped ([B1](translation.md#fields-the-source-declares)) |
| The backward lens drops a field the newer version added, with a `DroppedField` | into a closed struct, the field is dropped with a lacuna ([B5](translation.md#fields-the-source-declares)), and a struct is dropped whole with one lacuna ([E2](translation.md#structs-and-presence), not yet on main: [#176](https://github.com/sdboyer/thema/pull/176)). Into an open struct, the field is left for translation to carry ([B4](translation.md#fields-the-source-declares)); main still drops it ([#196](https://github.com/sdboyer/thema/pull/196)) |
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
| move, constant | always |
| `_coalesce` | the fallback was used |
| `_match` | the wildcard arm applied |
| `_drop` | the dropped field was present |

A `when` replaces the implied condition. It is a comparison or combination of source fields, built from the usual operators (`==`, `!=`, `<`, `>`, `<=`, `>=`, `&&`, `||`, `!`, `+`, `-`, `*`), `len` for the length of a string or list, and whole-number division (`quo`, `rem`, `div`, `mod`). Three rules govern the fields it reads:

- **A `when` that reads a field the source may omit concerns only the instances that have it.** `when: v0_0.count > 1000` above applies when `count` is present and exceeds 1000, and never when it is absent. The author does not test for presence.
- **A field the source may omit, named bare, tests its presence.** `when: v0_0.flag` applies to the instances that have `flag`, whatever its value. To test a bare optional field's value, compare it: `when: v0_0.flag == true`.
- **A required true-or-false field named bare is its value.** `when: v0_0.deprecated` applies when `deprecated` is true. Any other field named bare is an error: compare it.

**Drops.** A drop derives a `DroppedField` lacuna by itself. A `DroppedField` lacuna declared on a drop replaces the derived message. A lacuna of another type on a drop is added beside the derived one.

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

The compiler produces the normal form that the tutorial shows: a `thema.Lineage` with schemas keyed by version and lenses keyed by edge. A lens's fields keep their names, `v0_0` and `v1_0`, so a normal-form lens reads as its authoring form did. Implicit copies are written out, a copy of an optional field is written under a presence test, `_coalesce` and `_match` become one branch per case, `_each` becomes a loop over the list, and each lacuna becomes an entry of the lens's `lacunas` list with its condition and the fields it reports.

Every part of the normal form is positioned in the authoring file it came from. An error from the evaluator, from binding or from translation points at the author's line, and the compiler's own errors are worded in these terms: the file, the lens block and the mapping.

Binding a lineage proves every lens [total](invariants.md#lens-totality). Every form the compiler emits is one the proof decides, so a lineage that compiles binds.

## Layout

The authoring form has one layout: every block keeps its braces, and every field has its own line. The formatter produces it, and decompiling a formatted file gives back its text exactly. A file in another layout compiles the same; formatting it is a layout change and nothing else.
