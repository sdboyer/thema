# Translation

This page defines what translation does to each field of an instance, one step at a time, and when it raises a lacuna. It is the contract that consumers of translated data rely on, that every implementation of translation must meet (Go's `Instance.Translate`, and the Cap'n Proto engine of [#89](https://github.com/sdboyer/thema/issues/89) and [#90](https://github.com/sdboyer/thema/issues/90)), and that the authoring form's derivations must produce.

thema's behaviour is described in three layers:

1. **Translation**, this page: what happens to each field of an instance in a step, and whether it raises a lacuna.
2. **The lens language**: what a lens can say. Copies, moves, constants and computed values, `_drop`, `_coalesce`, `_match`, `_each`, and lacunas with `when`. See [the authoring form](authoring-form.md#lenses), and [lens totality](invariants.md#lens-totality) for what binding proves of a lens.
3. **Authoring derivations**: what the authoring form writes into a lens when the author says nothing. Implicit copies, the derived backward lens, implied presence tests, implied lacuna conditions and derived `DroppedField` lacunas. See [the authoring form](authoring-form.md#what-is-written-and-what-is-implied). A derivation is correct exactly when it produces what this page says.

A case this page does not settle is settled here first, before any derivation or lens rule changes to handle it.

**This page states the contract, not what main does today.** Each rule's **Main** column says whether thema's main branch implements it. Where it doesn't, the column names the issue or pull request that tracks the work, and the list under the table says what main does instead.

## Terms

- **Step.** One translation from an instance of a *source* version to an instance of the adjacent *target* version, forward or backward. A translation across several versions is a sequence of steps, each taking the previous step's result. A step either applies the lens for that pair of versions, or applies no lens ([G1](#steps-and-direction)).
- **Source field, target field.** A field of the step's source instance, and a field of the instance it produces. A field is named by its path from the instance's root: `meta.owner`.
- **Declared.** A schema declares a field when it names it: `a: T`, `a?: T` or `a!: T`, inside a struct the schema declares. Being admitted is not being declared:
  - a name that `...` admits is not declared ([#174](https://github.com/sdboyer/thema/pull/174));
  - a key that matches a pattern constraint, as in `labels: [string]: string`, is not declared ([A4](#what-an-instance-may-hold)). Only a field with a predictable name is.
  - Hidden fields, definitions and `let`s are not data, and are never part of an instance.
- **Undeclared data.** Fields an instance holds that its schema does not declare: those admitted by `...` or matched by a pattern constraint.
- **Open and closed.** A struct the author wrote with `...` is open. Every other struct is closed: thema reads a plain struct as closed, as it reads one written with `close()` or as a definition.
- **Present.** A field is present when the instance has it. A field the lens *writes* is one it gives a value in the result it produces for this instance.
- **Carried, promoted, set aside.** What translation does with undeclared data around a step ([C](#undeclared-data)). A carried field keeps its path and stays undeclared. A promoted field lands in a field of the same name that the target declares. A field set aside is dropped, with a lacuna.

Three principles hold for every rule below:

- **Translation never fails** ([#57](https://github.com/sdboyer/thema/issues/57)). Every instance of the source version produces an instance of the target version. Binding proves this for every lens ([lens totality](invariants.md#lens-totality)), and translation handles undeclared data so that it cannot make a step fail. On main, a minor step can still fail on undeclared data ([G1](#steps-and-direction)).
- **Dropped data always raises a lacuna.** No value that an instance held disappears from a step without a lacuna saying so. Dropping an empty struct or list is not dropping data ([E4](#structs-and-presence)).
- **Direction doesn't matter.** Every rule applies the same way forward and backward ([G3](#steps-and-direction)).

One guideline, which settles most cases the rules leave open: **a refusal at bind follows from the target schema; a lacuna follows from the source schema.** A lens is refused when the target requires something it can't produce for some instance (D3, E4). Data the source holds that a step can't deliver to the target is never a reason to refuse; it raises a lacuna (B5, B6, C3 to C5, E3a). It is a guideline, not a rule: where a row below says otherwise, the row holds.

## What an instance may hold

Validation decides what can reach a translation at all. It closes the schema, but keeps the author's `...`.

| Rule | Case | Outcome | Main |
|---|---|---|---|
| A1 | A field the schema doesn't declare, under the author's `...`, at any depth | Valid. The instance carries it into translation, as undeclared data ([C](#undeclared-data)). | yes |
| A2 | An undeclared field in a struct written without `...` | Invalid: thema reads a plain struct as closed. | yes |
| A3 | An undeclared field under `close()` or a definition | Invalid. | yes |
| A4 | A field matching a pattern constraint (`[string]: T`) | Valid when its value is an instance of `T`. It is undeclared data, carried like data under `...` ([C](#undeclared-data)). A lens never reads or writes a specific key under a pattern: it copies the map only whole (`labels: v0_0.labels`, since `labels` is named). To use a key, the author first declares an optional field for carried values to land in ([C2](#undeclared-data)), then maps from that field in a later version. | no: [#197](https://github.com/sdboyer/thema/issues/197) |

Today on main:

- **A4.** A matching pattern counts as declaring the key. Lenses translate such keys like named fields, and may read or write them by name ([#197](https://github.com/sdboyer/thema/issues/197)).

## Fields the source declares

Grouped by what the step's lens does with the field. A lens *maps* a field when a field of its result takes its value from the field or anything beneath it: copies it, or computes from it. A field the lens reads only in a comprehension's condition (`if v0_0.kind == "x" {...}`, or a list filter's `if`) is not mapped: the condition decides which fields exist, and none of its data is in them. A lens *drops* a field when a `DroppedField` lacuna it lists names the field. Steps without a lens are under [G1](#steps-and-direction).

One rule underlies the table: **a present source value whose data has no effect on any target value the lens writes for the instance is dropped.** It raises a `DroppedField` unless (i) a target field written for this instance copies it or computes from it, through the comprehension arm that actually fired; (ii) it is carried as undeclared data (B4); or (iii) the lens lists a `DroppedField` of its own for it (B2), which is raised instead. A field the lens maps only in an arm that didn't fire for this instance is not mapped for it, even where another arm wrote the target field ([lacunas translation raises](#lacunas-translation-raises-by-itself)).

The rules apply field by field, at every depth: a struct the lens reads in part is mapped in that part, and each declared field beneath it that nothing reads is a case of its own. A struct that nothing reads, and that the target lacks, is dropped whole ([E2](#structs-and-presence)).

| Rule | Case | Outcome | Lacuna | Main |
|---|---|---|---|---|
| B1 | The lens maps it: copy, move, computed value, `_match`, `_each` | The target gets the mapped value. | Only those the lens lists, under the conditions in [F](#when-lacunas-fire). | yes |
| B2 | The lens drops it (`_drop`) | Dropped. | `DroppedField`, whenever the field was present, unless it was an empty struct or list ([E4](#structs-and-presence)). | yes |
| B3 | Optional in the source, absent from the instance | Nothing is written from it. A target that requires the field takes a fallback the lens supplies (`_coalesce`). | None from the mapping ([F2](#when-lacunas-fire)). | yes, except `_match` ([F5](#when-lacunas-fire), [#187](https://github.com/sdboyer/thema/issues/187)) |
| B4 | The lens doesn't map it; the target doesn't declare it; the target's struct at the same path is open | Carried, as undeclared data of the target, at its own path. On the way back it is promoted into the field the source declares ([C2](#undeclared-data)). | None. | yes |
| B5 | The lens doesn't map it; the target doesn't declare it; the target's struct is closed, or the target has no struct at that path | Dropped. | `DroppedField`, raised by translation itself, whenever the field was present: one per struct per cause, naming each field and its value, as for C5. A `DroppedField` the lens lists for the field supersedes this one: it is raised instead, not as well. | yes |
| B6 | The lens doesn't map it; the target also declares it | Not copied. The target field gets the value the lens writes there without reading this one (a constant, or one computed from other fields), or is absent, takes its default ([D2](#fields-only-the-target-declares)), or, where the target requires it with no default, the lens is refused at bind ([D3](#fields-only-the-target-declares)). The source value is dropped. | `DroppedField`, as for B5. Where the lens writes the target field, none is raised if the value it writes equals the source value. | yes |

A field read only in a condition, such as a discriminator, is a B4, B5 or B6 case: its value is carried where the target's struct is open, and otherwise dropped with a `DroppedField`, even where the lens writes something that encodes it (`if v0_0.kind == "a" {isA: true}`). A lens that writes the same value back into a field the target declares (`if v0_0.kind == "a" {kind: "a"}`) loses nothing, and raises none.

Leaving a field unmapped is not refused at bind for that reason alone. A lens must turn every instance of its source into a valid instance of its target; it need not account for how. The authoring form never reaches B6: it copies a field both versions declare implicitly. It reaches B5 only through its derived backward lens, which drops a field the older version lacks with a `DroppedField` of its own; going forward, an unmapped removed field is an error ([E0347](errors/E0347.md)).

Carrying into a field the target declares, promotion, is for undeclared data only ([C2](#undeclared-data)). A declared source field the lens leaves alone is never copied into a target field by translation.

## Undeclared data

Fields an instance holds that its schema doesn't declare, under `...` ([A1](#what-an-instance-may-hold)) or a pattern constraint ([A4](#what-an-instance-may-hold)). Lenses never read or write them. Translation handles them around each step, with or without a lens, and the same way for a declared source field the lens doesn't map whose target struct is open ([B4](#fields-the-source-declares)).

| Rule | Case | Outcome | Lacuna | Main |
|---|---|---|---|---|
| C1 | The target's struct at the same path is open and doesn't declare it | Carried at its own path. | None. | yes |
| C2 | The target declares a field of that name, the lens wrote nothing there for this instance, and the value is an instance of the target field | Promoted into the declared field. A value that fits is promoted over the target's default. | None. | yes |
| C3 | The target declares it, the lens wrote nothing there, and the value is not an instance of the target field | Set aside. The target field is absent, or takes its default. This arises only where the target field is optional or has a default: the lens writes every field the target requires without a default ([D3](#fields-only-the-target-declares)), so C4 applies there. | `LossyFieldMapping`. Its `sourceFields` hold the carried field and its value, and its `targetFields` the declared target field and what it ended up as: absent, or its default. | yes |
| C4 | The target declares it, and the lens wrote that field | The lens's value wins. The carried value is discarded. | `DroppedField` for the discarded value. Any lacuna the lens lists on the field still fires. | yes |
| C5 | The target's struct is closed, and doesn't declare it | Set aside. | `DroppedField`: one per struct per cause, naming each field and its value. | yes |
| C6 | A target struct's contents come from one source struct as a whole, at another path: a move (`info: v0_0.meta`), an embed (`info: {v0_0.meta, x: 1}`), or a move with mappings beneath it | It follows the struct: carried to the target struct, where C1–C5 apply. Openness belongs to a struct, and taking a whole struct brings its undeclared data along. A struct assembled from parts of several source structs takes none: undeclared data keeps matching by path. | As C1–C5 at the new path. | yes |
| C7 | Inside list elements | Carried element by element, by index. Where the lens changes the list's length, undeclared fields on the indices that survive are kept, and only those on elements past the new length are set aside. | `DroppedField` for what is set aside. | yes |

Wherever undeclared data has no place in the target, because the target has no struct at its parent path, it is set aside with a `DroppedField`. Undeclared fields inside a struct that is dropped whole are part of the struct's value, which its own `DroppedField` reports ([E2](#structs-and-presence)).

C4 has a remedy for an author who wants to adopt a field that consumers already put in an open struct informally. Declare it optional first, so that carried values that fit are promoted (C2) and the rest are reported (C3), then make it required in a later version. Writing the new field with a lens value in the same step that introduces it discards what instances carried, since the field had no meaning its author defined until then.

For example, with 0.0 as `{a!: string, ...}` and 1.0 as `{a!: string, b?: bool, ...}`, and lenses that map only `a`:

| Step | Instance | Result | Lacuna |
|---|---|---|---|
| 0.0 → 1.0 | `{a: "x", b: true, z: 1}` | `{a: "x", b: true, z: 1}`: `b` promoted, `z` carried | none |
| 0.0 → 1.0 | `{a: "x", b: "s"}` | `{a: "x"}` | `LossyFieldMapping` on `b` (C3) |
| 1.0 → 0.0 | `{a: "x", b: false}` | `{a: "x", b: false}`: `b` carried as undeclared data (B4) | none |

## Fields only the target declares

Nothing in the source corresponds to them.

| Rule | Case | Outcome | Lacuna | Main |
|---|---|---|---|---|
| D1 | The lens writes a constant | That value. | Only one the lens lists. `Placeholder` is never raised by itself. | yes |
| D2 | The target has a default, and nothing writes the field | The default fills it, unless undeclared data is promoted over it ([C2](#undeclared-data)). | Only one the lens lists. `Defaulted` is never raised by itself. | yes |
| D3 | The target requires it, has no default, and nothing writes it | Refused at bind. | n/a | yes |
| D4 | Optional in the target, and nothing writes it | Absent. | None. | yes |

## Structs and presence

When a struct exists in the result, and how dropping one is reported.

| Rule | Case | Outcome | Lacuna | Main |
|---|---|---|---|---|
| E1 | An optional struct absent from the source | Absent from the target, never `{}`. A struct or list keeps the form the input had: absent stays absent, and empty stays empty. | None. | yes |
| E2 | A struct the target lacks, present in the source | Dropped whole. | One `DroppedField`, on the struct, whose value includes its contents. It replaces lacunas for the fields beneath it. An empty struct raises none (E4). | yes |
| E3 | A target struct filled from inside and outside an optional source struct | Present when any of its content is written, under E3a. | As E3a. | yes |
| E3a | An optional target struct assembled from several sources | Written when at least one of its contents is written, *and* every field it requires is satisfied, written or filled by its default. Otherwise it is not written. | Where some content is available but a required field isn't satisfied: a `DroppedField` for each value that would have gone into the struct. | yes |
| E4 | The target requires a struct that the source has as optional | `{}` is written when the source is absent, with whatever the struct's own defaults fill. An empty container and an absent one are not distinct information. Where that can't produce the struct for some instance, because it has a required field of its own that nothing fills when the source is absent, the lens is refused at bind, as for any required field it doesn't produce ([D3](#fields-only-the-target-declares)). The remedy is `_coalesce` with a struct fallback. | None. Dropping an empty struct or list never raises a lacuna. | yes |

**E3a**, a target struct exists when there is enough data for it to exist. For example, with 0.0 as `q?: {a!: int, b?: int}`, and 1.0 as `q?: {a!: int}` and `b?: int` (`b` moved out of `q`), the step back from 1.0 to 0.0 gives:

| 1.0 instance | 0.0 result | Lacuna |
|---|---|---|
| `{b: 2}` | no `q`: nothing supplies the required `q.a` | `DroppedField` on `b` |
| `{q: {a: 1}, b: 2}` | `q: {a: 1, b: 2}` | none |
| `{q: {a: 1}}` | `q: {a: 1}` | none |
| `{}` | no `q` | none |

The lacuna's condition depends on more than one field: some content source is present, and not every required source is. Here that is `b` present and `q` absent, since 1.0's `q` being present implies its `a`. In general it is

    (any content source present) && !(every required field's source present, or defaulted)

E3a applies to every optional target struct assembled from several sources, not only to a struct a field was moved out of. A lens can express the guarded struct and its lacuna with `when`; a derived lens must produce them, and a written one must write them, or the lens is not total.

## When lacunas fire

### Lacunas the lens lists

| Rule | Case | Fires | Main |
|---|---|---|---|
| F1 | A move, computed value or constant from a required source, with no `when` | Always. | yes |
| F2 | A move or computed value from an optional source, with no `when` | Only when the source is present, so that the mapping wrote its field. | yes |
| F3 | `_coalesce`; `_match`; `_drop` | When the fallback was used; when the wildcard arm applied; when the dropped field was present and not an empty container ([E4](#structs-and-presence)). | yes, except F5 |
| F4 | A written `when` | The `when` replaces the implied condition entirely, so the lacuna can fire where the mapping wrote nothing. It then names no target field. | yes |
| F5 | `_match` on an absent optional source | Writes nothing, and raises no lacuna (B3, F2). | no: bug [#187](https://github.com/sdboyer/thema/issues/187) |

### Lacunas translation raises by itself

These are not listed by any lens. Translation raises them whatever lens the step applies, or with no lens.

| Lacuna | Raised for | Rule |
|---|---|---|
| `DroppedField` | a declared source field the lens doesn't map and translation can't carry, when present, unless the lens lists a `DroppedField` for it. A field read only in a comprehension's condition is not mapped. Nor is a field both versions declare that the lens writes without reading it, unless the value it writes equals the source value | [B5, B6](#fields-the-source-declares) |
| `LossyFieldMapping` | undeclared data that a declared target field doesn't accept | [C3](#undeclared-data) |
| `DroppedField` | undeclared data discarded for the value the lens wrote | [C4](#undeclared-data) |
| `DroppedField` | undeclared data set aside: a closed target struct, no struct to carry it in, or a list element past the new length | [C5, C7](#undeclared-data) |
| `DroppedField` | a present source value that the lens maps, where no target field that copies it or computes from it was written for this instance by the arm that maps it, and that translation can't carry. For example the values that would have gone into an optional target struct that isn't written; or `b`, where `if v0_0.m == "copy" {x: v0_0.b}` didn't fire and another arm wrote `x`. Unless the lens lists a `DroppedField` for it, as the authoring form's derived lens does | [E3a](#structs-and-presence), [B](#fields-the-source-declares) |

Together these say: a present source value whose data affects no target value the lens writes for the instance, and that isn't carried as undeclared data, always raises a lacuna; one per struct per cause, with the lens's own `DroppedField` for the field raised in its place, and none for an empty struct or list.

Go's `Translate` raises all of these ([G4](#which-implementation-defines-translation)). The authoring form also derives the E3a lacuna into the lens itself ([#200](https://github.com/sdboyer/thema/pull/200)).

Today on main:

- **F5.** `{}` translates to the wildcard arm's constant, and raises the wildcard's lacuna.

## Steps and direction

| Rule | Case | Outcome | Main |
|---|---|---|---|
| G1 | A step forward to the next minor version | No lens: the instance is already an instance of the successor. Undeclared data follows [C](#undeclared-data), so a closed struct of the successor sets aside what it can't hold. | yes |
| G2 | Defaults in the input | Before the first step, the data is unified with its own schema, so non-optional fields with defaults are filled, and they appear in the output. | yes |
| G3 | Direction | The same rules apply forward and backward. Nothing depends on which way a step goes. | yes, for each rule main implements |

Forward and backward lenses differ only in what the authoring form derives for them. Its rule that a forward lens must account for every field the newer version removes ([E0347](errors/E0347.md)) guards against a forgotten rename. It is an authoring rule, not a translation rule.

## Which implementation defines translation

| Rule | Case | Outcome | Main |
|---|---|---|---|
| G4 | Which implementation defines this page | Go's `Instance.Translate`. The CUE `thema.Translate` pseudofunction in `translate.cue` translates declared fields only; bringing it to parity is a subordinate goal. | the gap is tracked in [#198](https://github.com/sdboyer/thema/issues/198) |

Go's `Translate` evaluates the CUE pseudofunction for the lenses, then carries, promotes and sets aside undeclared data around each step, and raises the automatic lacunas; the pseudofunction does neither.
