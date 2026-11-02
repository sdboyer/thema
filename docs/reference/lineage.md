# Lineages

A lineage is a named, ordered set of schema versions, with lenses that translate instances between adjacent versions, held in one CUE value. This page describes each part of a lineage and what thema checks of it. For an explanation of why lineages are built this way, see the [overview](../overview.md); for a guided start, the [authoring tutorial](../authoring.md); for the files lineage authors write, [the authoring form](../authoring-form.md).

In the normal form, a lineage is a `thema.Lineage` with four fields:

```cue
import "github.com/sdboyer/thema"

lin: thema.Lineage & {
	name: "Ship"
	schemas: {
		"0": "0": schema: {firstfield: string}
		"1": "0": schema: {firstfield: string, secondfield: int}
	}
	lenses: {
		"1.0->0.0": {
			v1_0: _
			v0_0: {firstfield: v1_0.firstfield}
		}
		"0.0->1.0": {
			v0_0: _
			v1_0: {firstfield: v0_0.firstfield, secondfield: -1}
		}
	}
}
```

- `name` is required. See [Name](#name).
- `schemas` is required, and holds at least one schema. See [Versions](#versions) and [Schemas](#schemas).
- `lenses` is required once the lineage has more than one version. See [Lenses](#lenses).
- `joinSchema` is optional. See [Schemas](#schemas).

Any other regular field is an error (`E0123`). Hidden fields (`_helper`) and definitions (`#Helper`) are not checked, and are free for the author's own helpers; the same holds inside schema entries and lenses. A lineage that needs regular fields beside it is declared in a field of its own, as `lin` above.

## Name

`name` names the kind of object the lineage's schemas describe.

- It is a string that starts with an ASCII letter and continues with ASCII letters and digits only: `^[A-Za-z][A-Za-z0-9]*$`. Any other name is an error at bind (`E0137`). Code generators derive identifiers, package names and file names from it without sanitizing.
- It is concrete (`E0121`).
- It does not change as the lineage evolves. Binding sees one revision of a lineage, and does not check this.
- In the authoring form, the name is the base name of the lineage's directory.
- In Go, `Lineage.Name()` returns it.

## Versions

Every schema has a syntactic version, a pair of non-negative integers written `major.minor`: `0.0`, `0.1`, `1.0`.

**Ordering.** Versions are ordered by major version, then by minor version: `0.9` precedes `1.0`, and `1.0` precedes `1.1`.

**Contiguity.** Versions are contiguous from `0.0`:

- The first version is `0.0` (`E0201`).
- Each later version is either the next minor version of its predecessor (`0.1` after `0.0`) or the first version of the next major version (`1.0` after `0.1`) (`E0202`).
- Every major version declares at least one schema (`E0203`).

**Minor and major.** Whether a version is minor or major is decided by [backwards compatibility](../invariants.md#backwards-compatibility), not by the author. thema checks each version against its predecessor:

- A minor version (`x.y`, `y > 0`) must be shown backwards compatible with its predecessor. An unknown verdict counts as incompatible. Each incompatibility is reported under its rule (`E0210`–`E0218`, `E0230`–`E0234`).
- A major version (`x.0`, `x > 0`) must not be backwards compatible with its predecessor (`E0208`). A step whose verdict is incompatible or unknown may be a major version.

The error for a version numbered the wrong way says which version it should be.

**Writing a version.** A version is never declared inside a schema; it is where the schema is declared.

- In the normal form, a schema is keyed by major version, then by minor version: `schemas["1"]["0"]` is the schema of version `1.0`. Keys are non-negative decimal integers written as strings, without leading zeros (`E0124`). Since versions are keys, a version cannot be declared twice; two bodies under one key unify like any CUE field's.
- In the authoring form, each version is a file named `<major>.<minor>.thema` (see [Files](../authoring-form.md#files)).
- In CUE, a version is a `thema.SyntacticVersion`, `[major, minor]`. In Go it is a `thema.SyntacticVersion`; `thema.ParseSyntacticVersion` reads `"1.0"`, and `String()` writes it.

**Latest.** The latest version is the largest: the last minor version of the last major version.

- In Go, `Lineage.Latest()` returns its schema, `Lineage.First()` the schema of `0.0`, and `Lineage.All()` every schema in version order. `Schema.LatestInMajor()` returns the last schema of a schema's major version.
- In CUE, `#LatestVersion` of a lineage holds its version. A schema or lens may not refer to it ([References](../invariants.md#references)).
- In the authoring form, the latest version's file is at the top of the lineage's directory.
- CLI commands and playground operations that take an optional version use the latest when none is given.

**Published versions.** A published schema is never changed or deleted. `thema.IsAppendOnly` compares two revisions of a lineage, and reports a deleted schema (`E0242`) or a changed one (`E0243`). Binding does not perform this check.

## Schemas

In the normal form, each entry of `schemas` holds a schema and, optionally, examples of it:

```cue
"0": "1": {
	schema: {
		firstfield:   string
		secondfield?: int
	}
	examples: {
		minimal: {firstfield: "a"}
	}
}
```

An entry has only `schema` and `examples` (`E0123`). `schema` is required.

**What a schema is.**

- A schema is a CUE value of struct kind (`E0129`).
- Each field is required, optional or defaulted, as its CUE syntax says ([Field forms](../invariants.md#field-forms)).
- A schema may declare definitions, hidden fields and `let`s inside itself, and use CUE's builtin packages.
- A schema may not use a comprehension, or contain a reference cycle (`E0132`–`E0135`, [Schema constructs](../invariants.md#schema-constructs)).
- A schema refers only to its own body and CUE's builtin packages: not to another schema, the lineage's other fields, or anything outside the lineage (`E0140`–`E0143`, `E0146`, [References](../invariants.md#references)).
- In the authoring form, a schema is the `Schema` field of a version file. A version file imports nothing, so a schema there cannot use the builtin packages ([What is not accepted yet](../authoring-form.md#what-is-not-accepted-yet)).

**Openness.** A schema is exactly as open or closed as its author wrote it. The operations that need closed semantics close it where they use it: validating data, checking backwards compatibility, and checking what a lens returns.

**Defaults.** A field written `a: *d | T`, or with a concrete value, is defaulted: an instance that omits it is read with the default filled in. Translation reads an instance as an instance of its schema, so a lens sees the defaults of its source schema. Adding a defaulted field is backwards compatible; changing or removing a default is not ([Backwards compatibility](../invariants.md#backwards-compatibility)).

**Examples.** `examples` maps a name to an instance of the schema.

- Each example is checked at bind against the schema, closed, and must be concrete (`E0131`).
- In Go, `Schema.Examples()` returns them as instances, keyed by name.
- The playground loads an example lineage's examples as data documents, one per example, named `<version>-<name>.json`.
- The authoring form has no field for examples.

**`joinSchema`.** A lineage's `joinSchema` is unified into every one of its schemas: its fields, constraints and defaults are part of every version.

- It is a struct, or `_`, the default, which joins nothing (`E0130`).
- It follows the rules for a schema's constructs and references.
- It does not change as the lineage evolves.
- The authoring form has no field for it.

## Lenses

A lens maps instances of one version to instances of an adjacent version, and declares the lacunas the mapping can produce.

**Keys.** In the normal form, each lens is keyed by its edge, `"<from>-><to>"`: `lenses["1.0->0.1"]` maps instances of `1.0` to `0.1` (`E0125`). The lens does not declare its versions.

**Which lenses exist.** A lineage declares exactly these lenses:

- a backward lens from every version after `0.0` to its predecessor;
- a forward lens from the last version of each major version to the first version of the next.

A forward lens within a major version is implicit and may not be declared: an instance of a version is an instance of its minor successor. For versions `0.0`, `0.1`, `1.0`, `2.0` and `2.1`, the lenses are (implicit lenses in parentheses):

```
 "0.1->0.0"
("0.0->0.1")
 "1.0->0.1"
 "0.1->1.0"
 "2.0->1.0"
 "1.0->2.0"
 "2.1->2.0"
("2.0->2.1")
```

A missing lens is an error (`E0301`). So is any other lens: a forward lens within a major version, a lens from a version to itself, a lens between non-adjacent versions, or a lens naming a version the lineage does not have (`E0302`).

**Fields.** A lens's fields are named after its edge's versions, `v<major>_<minor>`:

```cue
"0.0->1.0": {
	v0_0: _
	v1_0: {
		firstfield:  v0_0.firstfield
		secondfield: -1
	}
	lacunas: [thema.Lacuna & {
		targetFields: [{path: "secondfield", value: v1_0.secondfield}]
		message: "-1 is a placeholder"
		type:    thema.LacunaTypes.Placeholder
	}]
}
```

- The field named after the source version (`v0_0`) is the instance the lens takes. It is declared, and declared `_` (`E0310`, `E0303`).
- The field named after the target version (`v1_0`) is the instance the lens returns, built by referring to the instance it takes. It is declared (`E0311`), it is an instance of the target schema (`E0304`), and it writes only fields the target schema declares, however open that schema is written (`E0305`).
- `lacunas` lists every lacuna the lens can emit, unconditionally; each lacuna's `condition` decides whether it applies to an instance. It defaults to `[]`. See [Lacunas](lacunas.md).

A lens has no other regular fields (`E0123`). It may declare hidden fields, definitions and `let`s. It refers only to the instance it takes, its own body, CUE's builtin packages and thema's package ([References](../invariants.md#references)).

**Totality.** Every lens is total: for every instance of its source schema, it returns an instance of its target schema, and every lacuna's condition and reported values evaluate. thema proves this at bind, and a lens it cannot prove total is an error (`E0360`–`E0390`, [Lens totality](../invariants.md#lens-totality)).

**Translation.** Translating an instance from one version to another steps through every version between them, one edge at a time. A step across a declared lens evaluates the lens; a step forward within a major version unifies the instance with the next schema. Each step's result is closed against its target schema, and each step reports its own lacunas.

**In the authoring form.** A version file holds the lenses between its version and its predecessor: `ForwardLens`, for a major version only, and `BackwardLens`, which is derived from the forward lens unless written. The lens language is described under [Lenses](../authoring-form.md#lenses); a lens's fields keep their names, `v0_0` and `v1_0`, in both forms. In the tip's lenses, the tip's instance is named `tip`, and compiles to the name of the version the tip cuts as ([The tip](../authoring-form.md#the-tip)).

## Forms

A lineage is written in one of two forms.

**The normal form** is one CUE value: a `thema.Lineage` with `name`, `schemas`, `lenses` and optionally `joinSchema`, as on this page. Every thema tool consumes the normal form, and binding reads only it.

- It is written as `thema.Lineage & {...}`, or as a plain struct, which binding unifies with `thema.Lineage`.
- It may sit at any path of a CUE file or package. The CLI's `-p` flag, and the playground's lineage path, name that path; without one, the lineage is the whole value.
- It is loaded as CUE: a file that imports thema is loaded in a CUE module that depends on thema's module ([CUE modules](../cue-modules.md)).

**The authoring form** is a directory of version files, one per version, described in [the authoring form](../authoring-form.md).

- The directory's base name is the lineage's name. Every version that has been cut is a file under `history/`. `tip.thema`, at the top, is the version being worked on, whose version number is derived ([The tip](../authoring-form.md#the-tip)). Published lineages are read from `history/` alone; the development path reads the tip too.
- A version file holds `Schema`, and `ForwardLens` and `BackwardLens` where its version has them.
- It compiles to the normal form. Decompiling a normal form the compiler produced gives back its files, in the authoring form's layout. A normal-form lens outside what the authoring form expresses does not decompile (`E0356`).
- It has no field for `joinSchema` or `examples`. A lineage that uses them is written in the normal form.

Converting between the forms:

- In Go, the `authoring` package compiles (`authoring.Compile`, and `authoring.CompileDevelopment` with the tip), decompiles (`authoring.Decompile`) and formats (`authoring.Format`). `load.AuthoringLineage` loads, compiles and binds a directory in one call; `load.DevelopmentLineage` does the same with the tip.
- `thema lineage fmt -l <dir>` lays out a directory's version files in place, in the authoring form's [layout](../authoring-form.md#layout). It does not bind the lineage.
- Every CLI command that takes `-l` accepts a `.cue` file, a directory holding a CUE package, or a directory in the authoring form.
- In the playground, "Show as normal form" compiles the version files, with the tip, to the normal form, and "Show as version files" decompiles the normal form to version files and a clean tip. Decompiling requires the lineage to bind.

## Binding

Binding takes a lineage in the normal form, checks that it satisfies every invariant, and produces a bound `Lineage`. Nothing in thema operates on a lineage that has not bound.

**Entry points.**

- `thema.BindLineage(v cue.Value, rt *thema.Runtime, opts ...thema.BindOption) (thema.Lineage, error)` binds a normal-form value. It is the only way to obtain a non-nil `thema.Lineage`.
- `load.AuthoringLineage(fsys fs.FS, dir string, rt *thema.Runtime, opts ...thema.BindOption) (thema.Lineage, error)` compiles a directory in the authoring form and binds the result, with diagnostics positioned in, and worded in terms of, the version files.
- `load.DevelopmentLineage(fsys fs.FS, dir string, rt *thema.Runtime, opts ...thema.BindOption) (*load.Development, error)` does the same with the tip, as a provisional newest version; `authoring.BindDevelopment` binds what `authoring.CompileDevelopment` compiled. A lens the tip's edge lacks is a warning (`E1001`) rather than an error, and translating across it fails (`E1002`). The development path is for writing and testing a lineage, never for serving it.
- The CLI binds the lineage whenever a command loads one with `-l`.
- The playground binds the lineage, in either form, before validating or translating data against it or generating code from it. It binds the authoring form with its tip, on the development path.

**Checks.** Binding runs these steps in order. A step that fails ends binding, and reports every diagnostic it found; later steps do not run.

| Step | What is checked | Codes |
|---|---|---|
| 1. Existence | there is a value to bind | `E0128` |
| 2. Fields and keys | the lineage, each schema entry and each lens have only the fields thema defines; every `schemas` key is a version, every `lenses` key an edge | `E0123`, `E0124`, `E0125` |
| 3. Shape | `name` and `schemas` are declared and concrete, `schemas` holds a schema, `name` matches its pattern, and the value is an instance of `thema.Lineage` | `E0120`, `E0121`, `E0122`, `E0137`, `E0126` |
| 4. CUE errors | the value, with and without `thema.Lineage`, has no CUE error | `E0126` |
| 5. Versions | every major version declares a schema; the `joinSchema` and, in version order, each schema use no comprehension or reference cycle; versions are contiguous from `0.0`; each minor version is shown backwards compatible with its predecessor, and no major version is | `E0203`, `E0132`–`E0135`, `E0201`, `E0202`, `E0208`, `E0210`–`E0234` |
| 6. Schema shapes | the `joinSchema` is a struct or `_`; each schema is a struct; each example is a concrete instance of its schema | `E0130`, `E0129`, `E0131` |
| 7. Lens edges | the lineage declares exactly the lenses its versions require | `E0301`, `E0302` |
| 8. Lens results | each lens declares the instance it takes and the instance it returns; the instance it takes accepts every instance of the source schema; what it returns is an instance of the target schema, writing only fields the target declares | `E0310`, `E0311`, `E0303`, `E0304`, `E0305` |
| 9. Lacunas | each lens lists its lacunas unconditionally; each lacuna has a message and a type; each field path it reports is a path the corresponding schema declares | `E0401`–`E0405` |
| 10. References | each schema and the `joinSchema` refer only to what they may; when they all do, each lens is checked in turn | `E0140`–`E0146` |
| 11. Lens totality | each lens is proven total | `E0360`–`E0390` |

Where step 8 finds a result that is not an instance of its target, and the totality proof can name the rule broken, the totality finding is reported in place of `E0304`. The rules behind steps 5, 10 and 11 are stated in [invariants](../invariants.md): [Schema constructs](../invariants.md#schema-constructs), [Backwards compatibility](../invariants.md#backwards-compatibility), [References](../invariants.md#references) and [Lens totality](../invariants.md#lens-totality). Every code is listed in [Error codes](../errors/README.md).

A lineage in the authoring form is checked by the compiler before it is bound: the files, their version numbering, whether each version is minor or major, and each lens's mappings and lacunas (`E0101`–`E0112`, `E0148`–`E0150`, `E0204`–`E0206`, `E0330`–`E0357`, `E0410`–`E0423`). Binding then runs every step above on the compiled normal form. On the development path, the tip's missing lenses and its cleanness are reported as `E1001`–`E1003`.

**Options.**

- `thema.WithVocabulary` makes binding's diagnostics describe the lineage in the terms of the form it was written in. `load.AuthoringLineage` passes it.

**What it produces.** A bound `thema.Lineage` gives access to the lineage's schemas and to the operations on them:

- `Name()`, `First()`, `Latest()`, `All()`, and `Schema(v)`, which returns the schema of a version or an error naming the versions there are (`E0240`);
- `ValidateAny(data)`, which returns an instance of the oldest schema the data is valid against;
- `Underlying()`, the lineage's CUE value, unified with `thema.Lineage`.

Each lens proven total leaves a lens program, a target-neutral account of what the lens writes; `thema.LensPrograms(lin)` returns them ([Lens programs](../invariants.md#lens-programs)).
