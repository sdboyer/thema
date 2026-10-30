# Operations

These are the operations that combine data with a lineage: `validate`, `validate-any`, `translate`, `hydrate` and `dehydrate`. Each is available in the Go API, as a `thema data <op>` command, and in the playground, whose requests and responses are defined in `playground/types.go`; where the three differ for one operation, its section says so. Before any operation runs, the lineage is bound and the data is parsed: a lineage that does not bind fails with its own codes, and data that does not parse fails with [E0507](../errors/E0507.md), or [E0611](../errors/E0611.md) when it is neither JSON nor YAML. A version is written `<major>.<minor>`, such as `1.0`. Why thema has these operations is explained in the [overview](../overview.md#about-thema-operations); a guided walkthrough in Go is in [using thema from Go](../go-usage.md).

## validate

Checks that the data is an instance of one schema. The schema is closed for validation, keeping the author's `...`: a field the schema neither declares nor admits is an error. A struct the author wrote with `...` admits any field, and a pattern constraint admits the keys it matches; any other struct, including one written without `...`, is closed ([what an instance may hold](../translation.md#what-an-instance-may-hold)). Every field the schema declares that is not optional must have a concrete value, from the data or from the schema's default.

**Takes**

- A lineage and one data document.
- `version`: the schema to validate against. Empty means the latest.

**Version selection**

- The given version, or `Lineage.Latest`, the largest minor version of the largest major version. No other schema is tried.

**Returns**

- The version validated against.
- In Go, an `*Instance` whose `Underlying` value is the data as given. Validation does not fill defaults into it; [hydrate](#hydrate) does.

**Fails when**

- The data lacks a field the schema requires ([E0501](../errors/E0501.md)).
- The data has a field the schema neither declares nor admits ([E0502](../errors/E0502.md)).
- A value is of a kind the schema does not accept ([E0503](../errors/E0503.md)), is outside the bounds it allows ([E0504](../errors/E0504.md)), or matches none of its alternatives ([E0505](../errors/E0505.md)).
- The data is not concrete ([E0506](../errors/E0506.md)). Only data built in CUE can be; parsed JSON and YAML are always concrete.
- The version is not a version: [E0803](../errors/E0803.md) in the playground, [E0620](../errors/E0620.md) in the CLI.
- The lineage has no schema with the version ([E0240](../errors/E0240.md)).

**CLI**

```
thema data validate -l <lineage> [-p <cue-path>] [-v <version>] [-e json|yaml] [-q] [<data-file>]
```

- `-l`, `--lineage`: the lineage's `.cue` file or directory. Required.
- `-p`, `--path`: the CUE path of the lineage within the file, when it is not the file's root.
- `-v`, `--version`: the schema version. Defaults to the latest.
- `-e`, `--format`: `json` or `yaml`. Detected by default, JSON first. A format that disagrees with the file's extension is [E0612](../errors/E0612.md); any other format is [E0614](../errors/E0614.md).
- `-q`, `--quiet`: print nothing on failure; report by exit status only.
- `<data-file>`: the data. Without it, the data is read from stdin ([E0613](../errors/E0613.md) when there is none).
- `--diag-format`: `text` on stderr (default) or `json` on stdout, for every command.

Success prints nothing and exits 0. Failure prints the diagnostics and exits 1.

**Go**

- `thema.Schema.Validate(data cue.Value) (*thema.Instance, error)`, on a schema from `thema.Lineage.Schema(v)`, `thema.Lineage.Latest` or `thema.Lineage.First`.
- `thema.TypedSchema.ValidateTyped`, the same, returning a `*thema.TypedInstance`.
- Playground: `playground.Session.Validate`, operation name `validate`.

## validate-any

Searches the lineage for a schema the data is an instance of. Each schema is tried as [validate](#validate) tries it.

**Takes**

- A lineage and one data document.
- Playground: no version.
- CLI: an optional `-v`, described under version selection.

**Version selection**

The three differ:

- **Go**, `Lineage.ValidateAny`: tries the schemas from `0.0` upward and returns the first that accepts the data, the oldest.
- **Playground**: tries every schema, newest first, and reports the newest that accepts the data. Every schema is tried even after one accepts.
- **CLI**: tries the schema named by `-v`, or the latest when `-v` is not given, first. If it accepts, that is the result. Otherwise it searches as `Lineage.ValidateAny` does and reports the oldest schema that accepts.

**Returns**

- The version of the accepting schema.
- In Go, an `*Instance` of that schema, or `nil` when none accepts.
- Playground: `perVersion`, one entry per schema in the lineage, newest first, each with its `version` and the `diagnostics` that schema gave. An accepting schema's list is empty. These are returned on failure too.

**Fails when**

- No schema in the lineage accepts the data. The playground reports [E0508](../errors/E0508.md), with each schema's reasons in `perVersion`. The CLI reports the diagnostics of the schema it tried first, the `-v` schema or the latest, rather than E0508. In Go, `ValidateAny` returns `nil` and no error.
- CLI: the `-v` version is not a version ([E0620](../errors/E0620.md)) or not in the lineage ([E0240](../errors/E0240.md)).

**CLI**

```
thema data validate-any -l <lineage> [-p <cue-path>] [-v <version>] [-e json|yaml] [-q] [<data-file>]
```

- `-v`, `--version`: the schema to try first. Defaults to the latest.
- `-l`, `-p`, `-e`, `-q`, `<data-file>` and `--diag-format` are as for [validate](#validate).

Success prints the accepting version and exits 0. Failure exits 1, printing diagnostics unless `-q` is given.

**Go**

- `thema.Lineage.ValidateAny(data cue.Value) *thema.Instance`. The version is `inst.Schema().Version()`.
- Playground: `playground.Session.ValidateAny`, operation name `validate-any`.

## translate

Transforms an instance of one schema into an instance of another schema in the same lineage, and returns the [lacunas](lacunas.md) each step emitted. What a step does to each field of the instance, and which lacunas it raises, is defined in [translation](../translation.md). This section describes the operation as main implements it today; the rules of translation main does not implement yet, such as carrying the fields an open struct admits without declaring, are marked there.

A translation passes through every version between the two, one step per pair of adjacent versions:

- **Forward**, to a newer version. A step to the next minor version applies no lens: the instance is already an instance of the successor, and the step emits no lacunas. A step to the next major version applies that version's forward lens.
- **Backward**, to an older version. Every step, minor or major, applies the backward lens of that pair of versions.
- **Same version.** No steps; the result is the instance and there are no lacunas.

Each step's result is closed against its target schema and is the next step's input. Before the first step, the data is unified with its own schema, so lenses see the schema's defaults for fields the data omits. As a result, the output holds the defaults of non-optional fields the data omitted, as [hydrate](#hydrate) would fill them. A step's lacunas are those of its lens whose condition held for the instance that lens took, as described under [emission](lacunas.md#emission).

**Takes**

- A lineage and one data document.
- `to`: the version to translate to. Required.
- `from`: the version the data is an instance of. Playground and Go only. Empty means the data's: searched for in the lineage.

**Version selection**

- The target is `to`. No default.
- The source, in the playground, is `from` if given, against which the data is validated; otherwise `Lineage.ValidateAny` picks the oldest schema that accepts the data. This can differ from the version [validate-any](#validate-any) reports for the same data in the playground, which is the newest.
- The source, in the CLI, is always the oldest schema that accepts the data, by `Lineage.ValidateAny`. The CLI has no way to name it.
- The source, in Go, is the schema of the `*Instance`, fixed when it was validated.

**Returns**

- `from` and `to`: the source and target versions. On the [development path](../authoring-form.md#the-tip), which the playground uses for the authoring form and the CLI with `--tip`, the tip's provisional version is `"tip"`, and `cutsAs` gives the version the tip cuts as: `{"from": "1.0", "to": "tip", "cutsAs": "2.1", ...}`. `cutsAs` is omitted when neither end is the tip.
- The translated instance: playground `output`, CLI `result`, and in Go an `*Instance` of the target schema.
- `lacunas`: one entry per step, in order, each with `from` and `to` as `[major, minor]` and the `lacunas` that step emitted, `[]` when none. In Go this is `lacuna.TranslationLacunas`; `AsList` flattens it. The playground omits `lacunas` when there were no steps. See [lacunas](lacunas.md) for each lacuna's fields.

**Fails when**

- No `to` is given: [E0803](../errors/E0803.md) in the playground, [E0620](../errors/E0620.md) in the CLI.
- `to` or `from` is not a version (playground [E0803](../errors/E0803.md), CLI [E0620](../errors/E0620.md)), or not in the lineage ([E0240](../errors/E0240.md)).
- `from` is given and the data is not an instance of it: that schema's validation diagnostics, as for [validate](#validate).
- `from` is not given and no schema accepts the data ([E0508](../errors/E0508.md)).
- A lens fails for this instance. Binding proves every lens total and checks every field it writes, so these mean a bug in thema, not in the lineage or the data:
  - a mapping produces a value its target rejects ([E0308](../errors/E0308.md));
  - the result is not concrete ([E0307](../errors/E0307.md));
  - a lens writes a field its target schema does not declare ([E0305](../errors/E0305.md)).

**CLI**

```
thema data translate -l <lineage> [-p <cue-path>] --to <version> [-e json|yaml] [<data-file>]
```

- `-v`, `--to`: the target version. Required. With `--tip`, `tip` names the tip's version.
- `--tip`: bind a lineage in the authoring form with its tip, on the development path.
- `-l`, `-p`, `-e`, `<data-file>` and `--diag-format` are as for [validate](#validate). There is no `-q`.

Success prints a JSON object with `from`, `to`, `cutsAs` when the tip is involved, `result` and `lacunas`, and exits 0. Failure prints the diagnostics and exits 1.

**Go**

- `thema.Instance.Translate(to thema.SyntacticVersion) (*thema.Instance, lacuna.TranslationLacunas, error)`. It panics when the lineage has no schema `to`; check with `thema.Lineage.Schema` first.
- `thema.Instance.AsSuccessor` and `thema.Instance.AsPredecessor` translate one step, to the next or previous version. `AsSuccessor` to the next minor version validates the instance against the successor and returns one step with no lacunas.
- Playground: `playground.Session.Translate`, operation name `translate`.

## hydrate

Fills the data with the defaults its schema gives. The schema is walked field by field:

- A field the data lacks is filled when the schema declares it as a regular field, neither optional (`?`) nor required (`!`). It is filled with the value the schema gives it: its default, or its only value.
- A field the data lacks that the schema declares optional is not filled, even when the schema gives it a default.
- A field the data has keeps its value. When the value is a struct, its own fields are hydrated by the same rules. When it is a list, each element is hydrated against the schema's element type; for an element type with several alternatives, against the first alternative the element is an instance of.

**Takes**

- A lineage and one data document.
- `version`: the schema to hydrate against. Its meaning when empty differs; see version selection.

**Version selection**

- **Playground**: the given version, against which the data is validated. Empty means the data's: `Lineage.ValidateAny` picks the oldest schema that accepts the data. This can differ from the version [validate-any](#validate-any) reports for the same data in the playground, which is the newest.
- **CLI**: the `-v` version, or the latest when `-v` is not given. The data is validated against that one schema; the lineage is not searched.
- **Go**: the schema of the `*Instance`.

**Returns**

- The version hydrated against.
- The hydrated instance: playground `output`, CLI the printed JSON, and in Go a new `*Instance` of the same schema.
- Playground: `changed`, whether the output differs from the input as JSON values, ignoring field order.
- In Go, an internal failure while hydrating returns the instance unchanged, with no error.

**Fails when**

- A version is given and the data is not an instance of it: that schema's validation diagnostics, as for [validate](#validate). In the CLI this includes the case where no `-v` is given and the latest schema rejects the data.
- Playground, no version given: no schema accepts the data ([E0508](../errors/E0508.md)).
- The version is not a version (playground [E0803](../errors/E0803.md), CLI [E0620](../errors/E0620.md)), or not in the lineage ([E0240](../errors/E0240.md)).

**CLI**

```
thema data hydrate -l <lineage> [-p <cue-path>] [-v <version>] [-e json|yaml] [<data-file>]
```

- `-v`, `--version`: the schema version. Defaults to the latest.
- `-l`, `-p`, `-e`, `<data-file>` and `--diag-format` are as for [validate](#validate). There is no `-q`.

Success prints the hydrated instance as JSON and exits 0. Neither the input's formatting nor its field order is kept. Failure prints the diagnostics and exits 1.

**Go**

- `thema.Instance.Hydrate() *thema.Instance`.
- Playground: `playground.Session.Hydrate`, operation name `hydrate`.

## dehydrate

Removes from the data the values its schema's defaults imply. The schema is walked field by field:

- A field whose value equals the default the schema gives it is removed. Equality is mutual subsumption between the value and the default. A field without a default is kept.
- A field the schema declares optional (`?`) is kept, even when its value equals the default the schema gives it. When its value is a struct, the struct's fields are dehydrated by these rules.
- A struct is kept even when every field in it is removed; it remains as `{}`. Its own fields are dehydrated by these rules.
- A non-empty list that equals its default is removed. An empty list is kept, even when the schema's default is the empty list. A list that is kept has each element dehydrated against the schema's element type.

**Takes**

- A lineage and one data document.
- `version`: the schema to dehydrate against. Its meaning when empty differs; see version selection.

**Version selection**

- The same as [hydrate](#hydrate): in the playground, the given version or else the oldest schema that accepts the data; in the CLI, the `-v` version or else the latest; in Go, the schema of the `*Instance`.

**Returns**

- The version dehydrated against.
- The dehydrated instance: playground `output`, CLI the printed JSON, and in Go a new `*Instance` of the same schema.
- Playground: `changed`, whether the output differs from the input as JSON values, ignoring field order.
- In Go, an internal failure while dehydrating returns the instance unchanged, with no error.

**Fails when**

- The same as [hydrate](#hydrate).

**CLI**

```
thema data dehydrate -l <lineage> [-p <cue-path>] [-v <version>] [-e json|yaml] [<data-file>]
```

- `-v`, `--version`: the schema version. Defaults to the latest.
- `-l`, `-p`, `-e`, `<data-file>` and `--diag-format` are as for [validate](#validate). There is no `-q`.

Success prints the dehydrated instance as JSON and exits 0. Neither the input's formatting nor its field order is kept. Failure prints the diagnostics and exits 1.

**Go**

- `thema.Instance.Dehydrate() *thema.Instance`.
- Playground: `playground.Session.Dehydrate`, operation name `dehydrate`.
