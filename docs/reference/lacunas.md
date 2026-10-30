# Lacunas

A lacuna is a lens's report of a gap in translating an instance from one version to another: something the translation lost, invented or guessed. A lens declares every lacuna it can emit, and translating an instance returns the ones that applied to it. How lacunas are written in the authoring form is in [the authoring form](../authoring-form.md#lacunas); a guided example is in [the tutorial](../authoring.md#emitting-a-lacuna); why thema has them is in the [overview](../overview.md). What a translation step does to each field, and which lacunas it raises, including those translation raises by itself rather than a lens, is defined in [translation](../translation.md).

## Fields

A lacuna is `thema.Lacuna` in a lineage, defined in [`lacuna.cue`](../../lacuna.cue), and `lacuna.Lacuna` in Go. It has these fields and no others ([E0407](../errors/E0407.md)):

| Field | Type | In the result | Meaning |
|---|---|---|---|
| `type` | string, required | yes | The kind of gap: one of the [types](#types), as its name, `"Placeholder"`. `thema.LacunaTypes.Placeholder` is the same string. |
| `message` | string, required | yes | A human-readable description of the gap. |
| `condition` | `bool`, default `true` | no | Whether the lacuna applies to the instance being translated. |
| `sourceFields` | list of field references | yes, omitted when empty | The fields of the instance the lens takes that the gap concerns. |
| `targetFields` | list of field references | yes, omitted when empty | The fields of the instance the lens returns that the gap concerns. |

A field reference is `thema.LacunaFieldRef`, and `lacuna.FieldRef` in Go:

| Field | Type | Meaning |
|---|---|---|
| `path` | string, required | The field, as a CUE path from the instance's root: `"a.b"`, `"list[0]"`. |
| `value` | any | The field's value at the time of translation. |

- `lacuna.cue` states that at least one of `sourceFields` and `targetFields` must be non-empty. thema does not check it.
- `value` is written as a reference into the lens, `v0_0.a.b` or `v1_0.a.b`, and resolves when the lens runs. A source value comes from the instance the lens takes, which holds its schema's defaults for fields the data omits. A target value comes from the instance the lens returns.
- `type` and `message` are emitted as written. `condition` is evaluated and not emitted: an emitted lacuna has only `type`, `message`, `sourceFields` and `targetFields`.

Binding a lineage checks every lens's lacunas:

- The `lacunas` list is written unconditionally, never built under an `if`; each lacuna's `condition` decides whether it applies ([E0401](../errors/E0401.md)).
- Every lacuna has a `message` ([E0402](../errors/E0402.md)) and a `type` ([E0403](../errors/E0403.md)), and the type is one thema defines ([E0406](../errors/E0406.md)).
- Every `path` is a CUE path ([E0405](../errors/E0405.md)). A `sourceFields` path is declared by the lens's `from` schema, and a `targetFields` path by its `to` schema ([E0404](../errors/E0404.md)).

In the authoring form, a lacuna is written with `type`, `message` and an optional `when`. The compiler fills in the rest from the mapping the lacuna is keyed by:

- `condition` is the `when`, or without one the condition the mapping implies (see [the authoring form](../authoring-form.md#lacunas)).
- For a `_drop`, `sourceFields` is the dropped field, and there are no `targetFields`.
- For any other mapping, `sourceFields` lists each source field the mapping reads, and `targetFields` is the mapping's target field. A source field the source schema has as optional is listed only when the instance has it. A constant reads no source field, so its lacunas have only `targetFields`.

## Types

The types, in the order `lacuna.cue` declares them. The Go constants in package `lacuna` mirror them, and `lacuna.Types()` lists them.

### Placeholder

- **Gap:** a target field holds a value the lens invented to meet the target schema, standing in for data the source does not have. It is not a schema default: it is a lens-defined value that exists to be replaced by the calling program. The usual case is a new required field with no default.
- **Fields:** `targetFields`, the field holding the placeholder. A lacuna on a `_coalesce` in the authoring form also lists the source field when the instance has it; when the fallback applied, it does not.
- **Derived:** never. Emitted only when declared ([D1](../translation.md#fields-only-the-target-declares)).

### DroppedField

- **Gap:** one or more source fields were dropped in a way that may lose what they meant. Fields dropped for one cause belong in one `DroppedField`, such as every instance field dropped from an open struct because the schema lacks it.
- **Fields:** `sourceFields`, the dropped fields. No `targetFields`.
- **Derived:** yes. thema derives one for every drop in a compiled lens, described under [emission](#emission). An author may also declare one.

### LossyFieldMapping

- **Gap:** no clear mapping exists from the source value to the meaning of any valid target value, such as a coercion that only some source values survive: a string field becoming an integer, where `"12"` parses and `"twelve"` does not. At least one valid source value has a clear, lossless mapping to a target value.
- **Fields:** both. `sourceFields`, the field whose value did not map; `targetFields`, the field it mapped to.
- **Derived:** never by a lens. Translation raises one by itself for undeclared data, carried under a name the target declares, whose value is not an instance of the target field ([C3](../translation.md#undeclared-data)). That is not on main yet ([#196](https://github.com/sdboyer/thema/pull/196)); today no translation raises a `LossyFieldMapping` that a lens does not declare.

### ChangedDefault

- **Gap:** the source value equals the source schema's default for the field, the target schema's default differs, and the value was changed to the target's default. Whether the source value was written or filled in from the default is not considered.
- **Fields:** both. `sourceFields`, the field with the source default; `targetFields`, the field with the target default.
- **Derived:** never. thema does not compare defaults across versions; the lens maps the value, for instance with `_match(v0_0.kind, {"foo": "bar"})`, and declares the lacuna with a `when` on the old default.

### PrecisionLoss

- **Gap:** every source value maps to a target value, but with reduced fidelity, such as seconds becoming whole milliseconds or timestamps being truncated.
- **Fields:** both. `sourceFields`, the full-precision field; `targetFields`, the reduced one.
- **Derived:** never. Emitted only when declared.

### AmbiguousMapping

- **Gap:** the lens chose one of several equally valid target values for the source value, such as splitting a full name into first and last names. Nothing is lost, but the choice may be wrong.
- **Fields:** both. `sourceFields`, the field interpreted; `targetFields`, the fields that hold the choice.
- **Derived:** never. Emitted only when declared.

### Defaulted

- **Gap:** a target field holds the target schema's default because the source instance had no corresponding data. The value is a legitimate one chosen by the schema, not a placeholder, but it was inferred rather than stated.
- **Fields:** `targetFields`, the defaulted field.
- **Derived:** never. A target schema's default that fills a field during translation emits no lacuna unless the lens declares one ([D2](../translation.md#fields-only-the-target-declares)).

## Emission

This section describes how main emits lacunas today. [Translation](../translation.md#when-lacunas-fire) defines which lacunas a step raises. Main does not yet raise any of the [lacunas translation raises by itself](../translation.md#lacunas-translation-raises-by-itself), so where this section says a step emits only its lens's lacunas, or that a minor step emits none, it describes main, not the contract. Likewise, where the target version's struct is open, the derived drop of a field the newer version added is main's behaviour; translation carries such a field instead ([B4](../translation.md#fields-the-source-declares)).

A translation passes through every version between its `from` and `to` versions, one step per pair of adjacent versions.

- A forward step to the next minor version applies no lens: the instance is already an instance of the successor. It emits no lacunas.
- A forward step to the next major version, and every backward step, minor or major, applies the lens for that pair of versions.

A step that applies a lens evaluates the `condition` of each lacuna in the lens's `lacunas` list, against the instance that lens takes. A lacuna whose condition is `true` is emitted. Lacunas are emitted in list order.

Translation returns the lacunas grouped by step, `lacuna.TranslationLacunas` in Go:

```json
[
    {"from": [2, 0], "to": [1, 1], "lacunas": []},
    {
        "from": [1, 1],
        "to": [1, 0],
        "lacunas": [
            {
                "sourceFields": [{"path": "withDefault", "value": "bing"}],
                "targetFields": [{"path": "withDefault", "value": "bar"}],
                "type": "LossyFieldMapping",
                "message": "withDefault was \"bing\", which 1.0 does not have; \"bar\", its default, was substituted"
            }
        ]
    }
]
```

- Each step has `from` and `to`, each a version as `[sequence, schema]`, and the `lacunas` it emitted. A step that emitted none has `lacunas: []`.
- Each step's lacunas are in that step's terms: their paths refer to that step's source and target schemas, and are not re-mapped through later steps.
- `AsList` flattens the steps into one list in order, losing which step emitted each lacuna.
- The playground's lacunas table shows one row per emitted lacuna: its step, its type, the paths of its `sourceFields` then its `targetFields`, and its message.

**Derived lacunas.** thema derives `DroppedField` lacunas, and no other type. The authoring-form compiler derives one for each drop:

- each `_drop` the author writes;
- in a backward lens, each field the source version declares and the target version does not, that no mapping uses, including every field the newer version added. Such a field is dropped, and the drop derives the lacuna. In a forward lens, such a field is an error instead ([E0347](../errors/E0347.md)).

A derived lacuna has:

- `sourceFields`: the dropped field. No `targetFields`.
- `message`: `<path> has no equivalent in <version> and was dropped`, where `<version>` is the lens's target version: `meta.team has no equivalent in 0.0 and was dropped`.
- `condition`: that the field is present, when the source schema has it as optional. Otherwise none, so it always applies.

A drop inside an `_each` body derives one lacuna for the whole list. Its message is `<list>[].<path> has no equivalent in <version> and was dropped`, its `sourceFields` name each element that had the field, as `<list>[<i>].<path>`, and its condition is that at least one element had it.

A `DroppedField` lacuna the author declares on a `_drop` refines the derived one: its message replaces the derived message, and its `when` the derived condition ([E0416](../errors/E0416.md) for more than one). A lacuna of another type on a `_drop` is emitted beside the derived one. In a compiled lens, the declared lacunas come first, in authored order, with a refined `DroppedField` in its declared place; derived lacunas that nothing refines follow.

In the normal form, a derived lacuna is marked with the attribute `@thema(derived)`:

```cue
lacunas: [{
	@thema(derived)
	sourceFields: [{
		path:  "meta.team"
		value: v1_0.meta.team
	}]
	message: "meta.team has no equivalent in 0.0 and was dropped"
	type:    "DroppedField"
}]
```

- The attribute is not data. An emitted derived lacuna is indistinguishable from a declared one.
- The decompiler reads a derived lacuna back as a `_drop`. Where derivation would produce that drop again, as for a field the newer version added, nothing is written. Where the message or condition differs from the derived one, the `_drop` gets a declared `DroppedField` lacuna.
- A lacuna marked derived must be a `DroppedField` over exactly one source field and no target fields; any other is outside the authoring form, and decompiling it is an error.
