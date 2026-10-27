# Tutorial: Writing a Lineage

The most fundamental task in thema is writing a lineage. Writing a lineage is similar to other schema-writing systems: you're defining a specification for what data is supposed to look like, with two key differences:

1. CUE is the only language supported for schemas
2. Schemas are written _within_ a larger, well-defined structure - the lineage - which groups those schema together and enforces certain requirements.

This tutorial focuses on the second key difference: expressing collections of schemas as a valid lineage.

**Note:** A primer on writing CUE is available using the [official CUE tutorials](https://cuelang.org/docs/tutorials/) and [CUE Playground](https://cuelang.org/play/).

## Before you begin

Read the [Thema overview](overview.md) before beginning this tutorial.

Lineages are written in a CUE module that depends on thema's CUE module, `github.com/sdboyer/thema@v0`:

```bash
cue mod init github.com/example/ships
cue mod get github.com/sdboyer/thema@v0
```

When lineages are loaded through thema's Go library, thema serves its own CUE module from the copy built into the library, and the version in your `cue.mod/module.cue` must match it exactly. See [CUE modules](cue-modules.md).

## Scaffolding

A lineage is a `thema.Lineage` with two required fields:

1. `name`: the identifier for the thing schematized by the lineage. Use a simple name, not a fully-qualified name. For this tutorial, we'll use [`"Ship"`](https://github.com/sdboyer/thema/blob/main/FAQ.md#where-does-the-name-thema-come-from).
2. `schemas`: every schema the lineage has ever had, keyed by major version, then minor version.

```cue
import "github.com/sdboyer/thema"

lin: thema.Lineage & {
	name: "Ship"
	schemas: {}
}
```

A valid lineage requires at least one schema in `schemas`, so this isn't actually a valid lineage yet - it's the minimum structure to start from. Tooling refuses to work with invalid lineages, similar to a failed type check: [loading this lineage in Go](https://pkg.go.dev/github.com/sdboyer/thema#BindLineage) fails because `schemas` declares no schema.

thema checks the fields of a lineage, its schema entries and its lenses: a misspelled `schemass` or `lense` is an error, both in plain CUE and in Go. Hidden fields (`_helper`) and definitions (`#Helper`) are left alone, so you can keep your own helpers alongside a lineage. When you need regular fields next to a lineage, put the lineage in its own field, as above (`lin: thema.Lineage & {...}`), rather than embedding `thema.Lineage` at the top of a file.

## Defining a schema

Each entry in `schemas` declares one schema, keyed by its version: `"0": "1": {...}` is version 0.1. Keys are non-negative integers, written as strings. An entry holds the `schema` itself, and optionally named `examples` of it; the version is never written inside the entry. Schemas are CUE structs, from a single field to highly complex, deeply nested objects. Complex CUE constraints may also be used, though they may limit translatability to other languages.

Lineages should be sealed structures, as thema's invariants only hold in the context of immutable schema declarations. A schema should refer only to itself and the lineage's `joinSchema`; values from elsewhere, especially from other CUE modules, may change when their source changes.

Let's define our first schema as an object containing a single field named `firstfield`, which must be of type `string`.

```cue
import "github.com/sdboyer/thema"

lin: thema.Lineage & {
	name: "Ship"
	schemas: {
		"0": "0": {
			schema: {
				firstfield: string
			}
		}
	}
}
```

And that's it! Congratulations, we now have a valid thema lineage, containing a single schema.

Because versions are keys, each one is declared once. Writing the same key twice declares one entry, whose two bodies unify like any CUE field's.

## Adding more schemas

The schema we wrote isn't terribly exciting. But we can add to it.

When writing real thema, whether to define a new schema or make additions directly to the existing one is determined by whether the latest existing schema has been published, as published schema must be immutable. That makes the definition of "published" quite important.

For this tutorial, we'll sidestep the issue by assuming that publication has happened. Because publication has happened - making the schema immutable - making changes requires creating a new schema.

A schema's version is a pair, major and minor. Versions are contiguous: the first schema is `0.0`, and each schema's version must be exactly the next minor version of its predecessor, or the first version (`.0`) of the next major version. Which of the two is correct isn't the author's choice: it is determined by whether the change is backward compatible. A schema that is backward compatible with its predecessor - every instance of the old schema is also an instance of the new one - is the next minor version. A schema that isn't is the next major version.

Let's add one more field, `secondfield`, which must be an `int`, as version `0.1`.

```cue
import "github.com/sdboyer/thema"

lin: thema.Lineage & {
	name: "Ship"
	schemas: {
		"0": "0": {
			schema: {
				firstfield: string
			}
		}
		"0": "1": {
			schema: {
				firstfield:  string
				secondfield: int
			}
		}
	}
}
```

This isn't a valid lineage, though. `secondfield` is required and lacks a default, which means that valid instances of `0.0` will be invalid with respect to `0.1`. Our change is backward incompatible, so it can't be a minor version.

There are three approaches to fixing this:
- make `secondfield` optional
- give `secondfield` a default[^defaultcompat]
- make the new schema a new major version

## Optional fields

The simplest fix involves just one character: `?`. This indicates that `secondfield` is optional.

```cue
import "github.com/sdboyer/thema"

lin: thema.Lineage & {
	name: "Ship"
	schemas: {
		"0": "0": {
			schema: {
				firstfield: string
			}
		}
		"0": "1": {
			schema: {
				firstfield:   string
				secondfield?: int
			}
		}
	}
	lenses: {
		"0.1->0.0": {
			v0_1: _
			v0_0: {
				firstfield: v0_1.firstfield
			}
		}
	}
}
```

Every schema needs a lens back to its predecessor; for a minor version, that lens is simple, as it only needs to leave out what the newer schema added. Lenses are covered [below](#defining-lenses).

Marking added fields as optional is often a good option, but it's not without implications. By making `secondfield` optional, we have nontrivially expanded the set of valid values to which consumers of `Ship` must assign semantics/program behavior. Before, it was "all valid values of `int`". Now, it's that, plus "an absence of a value."

If the behavior of a program accepting `Ship` behaves in a meaningfully different way absent this value rather than any valid `int`, this outcome may be acceptable. But if the program treats the _absence_ of a `Ship.secondfield` as equivalent to the _presence_ of some particular `int` value, a default is the more natural fit.

## Setting defaults

CUE allows [specifying default values](https://cuelang.org/docs/tour/types/defaults/) for fields: `secondfield: int | *42` gives `secondfield` a default of `42`. Every instance of `0.0` is still an instance of a schema that adds a defaulted field, so this should be backward compatible.

However, thema does not yet treat adding a field with a default as a backward compatible change[^defaultcompat], so for now, prefer optional fields.

Choosing between optional and default values for added fields is a subtle, surprisingly hard problem that lacks a single, universally correct answer. (In most schema-writing cases, using both at once is discouraged, as it just muddles the issue further.) In general, prefer:

* Optional fields when the absence of said field most naturally corresponds to an absence of a class of behavior in the consuming program
* Default values when the absence of said field most naturally corresponds to the behavior when a particular value is present

Other considerations:

* Optional values may not translate well into the language you want to work in. TypeScript has a direct symbolic equivalent, but in Go, the only sane representation is to represent the field as a pointer type and treat `nil` as absence.
* Once a default value is published, changing it is always considered a breaking change, requiring a new major version.

## Safe breaking changes

The third option for making our lineage valid is to rely on thema's foundational feature: making breaking changes safely. We choose this path over the others for some reason that's important to the semantics of a newer version of our program - the field is indeed required for all future correct behavior, and relies on information - say, some user input - that simply wasn't a part of the initial schema. Requirements evolve, and programs naturally evolve them.

In this approach, the new schema is a new major version: `1.0`.

```cue
import "github.com/sdboyer/thema"

lin: thema.Lineage & {
	name: "Ship"
	schemas: {
		"0": "0": {
			schema: {
				firstfield: string
			}
		}
		"1": "0": {
			schema: {
				firstfield:  string
				secondfield: int
			}
		}
	}
}
```

Whereas a minor version must be backward compatible with its predecessor, a major version must be backward _in_compatible. (The logical inverse holds.) As a result, making `secondfield` here either optional or giving it a default will make this lineage invalid.

But this lineage is already invalid, because crossing a major version also requires the author to define lenses.

## Defining lenses

Lenses define mappings between schemas. They're the magical connectivity that provide thema's foundational guarantee - translatability of a valid instance of a schema to other versions of that schema.

But - as in all software - where there be magic, [there be dragons](https://en.wikipedia.org/wiki/Here_be_dragons). Checking syntactic, type-level backward compatibility is trivial thanks to CUE, but _semantics_, best thought of as the intended behavior of the programs consuming schema instances, are the only reasonable motivation for making a breaking change. Because [no general algorithm can exist for specifying semantic correctness](https://en.wikipedia.org/wiki/Rice%27s_theorem), it's the responsibility of lineage authors to ensure that their lenses capture semantics correctly.

A lineage must have exactly these lenses:

- one from every schema back to its predecessor
- one forward across every major version boundary, from the last schema of one major version to the first of the next

Moving forward within a major version needs no lens, as backward compatibility already guarantees an old instance is a new instance.

Each lens is keyed by the versions it maps between, written `"<from>-><to>"`: `"1.0->0.0"` maps instances of `1.0` to `0.0`. A lens has an `input` that stands for an instance of the `from` schema, and a `result`: the instance of the `to` schema, built from `input`. A lens's result may contain only fields its `to` schema declares. thema checks lenses when the lineage is loaded, treating `input` as any instance of its schema: a result that couldn't be an instance of the `to` schema, or that refers to a field `input` can't have, is an error before any data is ever translated.

The change to the `Ship` schema is trivial, but presents an interesting challenge - because we specifically don't want to make `secondfield` optional or give it a default value, how can we define a forward lens that still produces a valid instance of `Ship@1.0`?

The only real answer is to add a placeholder value - here, `-1`.

```cue
import "github.com/sdboyer/thema"

lin: thema.Lineage & {
	name: "Ship"
	schemas: {
		"0": "0": {
			schema: {
				firstfield: string
			}
		}
		"1": "0": {
			schema: {
				firstfield:  string
				secondfield: int
			}
		}
	}
	lenses: {
		"1.0->0.0": {
			v1_0: _
			v0_0: {
				// Map the first field back
				firstfield: v1_0.firstfield
			}
		}
		"0.0->1.0": {
			v0_0: _
			v1_0: {
				// Direct mapping of the first field
				firstfield: v0_0.firstfield
				// Just some placeholder int, so we have a valid instance of schema 1.0
				secondfield: -1
			}
		}
	}
}
```

Translating `{"firstfield": "foobar"}` from `0.0` to `1.0` produces `{"firstfield": "foobar", "secondfield": -1}`.

The output is valid, but less than ideal. Are we just going to have `-1` values littered all over our instances of `Ship.secondfield`? When would those get cleaned up? Does choosing `-1` as a placeholder grant special semantics to that particular value in perpetuity?

These questions bring us to the last part of thema: Lacunas.

## Emitting a lacuna

Thema's professed guarantee - all prior valid instances of schema will be translatable to all future valid instances of schema - sounds lovely. But the `secondfield` case shows it to be a pressure vessel, fit to burst when requirements evolve in just _slightly_ the wrong way. Other schema systems are similar - they "burst" when folks make breaking changes to attain the semantics they want. And it's nice that thema pushes this out further with the ability to encode translations in lenses. But eventually, it'll still burst, and folks will pick their desired semantics over thema's rules - just like they do today.

To prevent this outcome, what we really need is a pressure release valve. Which is where lacunas come in.

Lacunas represent a gap or flaw in a lens translation. As a lineage author, you add a lacuna to your lens when the translation results in a message that, while syntactically valid (it conforms to schema), has problematic semantics. Lacunas are accumulated during translation, and returned alongside the translated instance itself.

A lens lists every lacuna it can emit in its `lacunas` field. Each lacuna has:

- `type`: the kind of gap, one of `thema.LacunaTypes`
- `message`: a human-readable description of the gap
- `sourceFields` and `targetFields`: the fields, in the `from` and `to` schemas, that the gap concerns, as a CUE path (`"a.b"`) and the field's value
- `condition`: whether the gap applies to the instance being translated (default `true`)

Lacunas are always listed unconditionally, with `condition` deciding whether each applies - not wrapped in an `if`. That way, every lacuna a lens can emit is known when the lineage is loaded, and thema checks that its field paths exist in the schemas they refer to.

The lacuna types are:

| Type | The gap |
|---|---|
| `Placeholder` | A target field holds a lens-invented value standing in for data the source didn't have, meant to be replaced |
| `DroppedField` | Source data was discarded |
| `LossyFieldMapping` | Some source values have no clear mapping to a target value (e.g. a coercion only some values survive) |
| `ChangedDefault` | The source value was the source schema's default, and was changed to the target schema's differing default |
| `PrecisionLoss` | Every value maps, but with reduced fidelity (e.g. seconds to whole milliseconds) |
| `AmbiguousMapping` | The lens chose one of several equally valid target values |
| `Defaulted` | A target field holds the target schema's default, because the source had no corresponding data |

For our case, the forward lens should emit a `Placeholder` lacuna, and the backward lens a `DroppedField` - which gives us the complete `Ship` lineage, also available as [`ship.cue`](ship.cue):

```cue
import "github.com/sdboyer/thema"

lin: thema.Lineage & {
	name: "Ship"
	schemas: {
		"0": "0": {
			schema: {
				firstfield: string
			}
		}
		"1": "0": {
			schema: {
				firstfield:  string
				secondfield: int
			}
		}
	}
	lenses: {
		"1.0->0.0": {
			v1_0: _
			v0_0: {
				firstfield: v1_0.firstfield
			}
			lacunas: [thema.Lacuna & {
				sourceFields: [{
					path:  "secondfield"
					value: v1_0.secondfield
				}]
				message: "secondfield has no equivalent in 0.0 and was dropped"
				type:    thema.LacunaTypes.DroppedField
			}]
		}
		"0.0->1.0": {
			v0_0: _
			v1_0: {
				firstfield:  v0_0.firstfield
				secondfield: -1
			}
			lacunas: [thema.Lacuna & {
				targetFields: [{
					path:  "secondfield"
					value: v1_0.secondfield
				}]
				message: "-1 used as a placeholder value - replace with a real value before persisting!"
				type:    thema.LacunaTypes.Placeholder
			}]
		}
	}
}
```

Translating `{"firstfield": "foobar"}` from `0.0` to `1.0` now yields, alongside the instance, one translation step with its lacunas:

```json
[
    {
        "from": [0, 0],
        "to": [1, 0],
        "lacunas": [
            {
                "targetFields": [
                    {
                        "path": "secondfield",
                        "value": -1
                    }
                ],
                "type": "Placeholder",
                "message": "-1 used as a placeholder value - replace with a real value before persisting!"
            }
        ]
    }
]
```

A translation across several versions reports the lacunas of each step separately, in that step's terms.

Encapsulating translation flaws as lacunas relieves pressure on the schemas and translation. Schemas need not carry extraneous, legacy fields to reflect translation flaws, and lacunas can disambiguate for the calling program between translations with flaws, and those without. In this case, we can imagine `secondfield` is actually a serial identifier/foreign key, and the calling program can be constructed to look for a `Placeholder` lacuna on `secondfield`, then replace that `-1` with a correct value derived from another source.

Knowing when to emit a lacuna, and which type to emit, is nontrivial. The [exemplars directory](https://github.com/sdboyer/thema/tree/main/exemplars) contains further examples, including conditional lacunas.

## Advanced: schema openness

thema takes no position on whether a schema is open or closed: a schema means exactly what its author wrote. Operations that need closed semantics apply them where they're used:

- checking backward compatibility compares schemas as if closed
- validating data closes the schema by default, so data may contain only fields the schema defines (the Go library can instead validate against a schema exactly as written)
- a lens's result may contain only fields its target schema declares, even where the schema is open

## Advanced: `joinSchema`

A lineage's `joinSchema` is unified into every one of its schemas: its fields, constraints and defaults become part of every version. It must be a struct, and like a lineage's name, it must never change as the lineage evolves.

## Wrap up

You've now seen all the component parts of thema and how they work together, which provides a sense of how to express schemas using thema. In the [next tutorial](go-mapping.md), we'll map the `Ship` lineage created here to a general-purpose language (Go), which is prerequisite to writing useful programs around lineages.

[^defaultcompat]:
    Whether adding a field with a default should be backward compatible is an [open question](https://github.com/sdboyer/thema/issues/18). Every old instance remains valid, but thema's compatibility check currently rejects it.
