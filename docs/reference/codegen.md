# Code generation

This page is the reference for generating code from a lineage. Each generator except Go reads one schema of the lineage at a time. The Go generator reads the whole lineage, and binds one schema to a Go type only when asked to. The `thema lineage gen` command generates OpenAPI, JSON Schema and Go bindings. The `encoding/*` Go packages generate all four, and [Cap'n Proto](#capn-proto) from the whole lineage. The browser playground generates OpenAPI, JSON Schema and TypeScript. Every generator accepts a lineage in either the [authoring form](../authoring-form.md) or the normal form, except where its **Limits** say otherwise.

The examples use the `ship` lineage from the playground's tour, with a minor version `1.1` added:

```cue
// history/0.0.thema
Schema: {
	firstfield: string
}

// history/1.0.thema (lenses omitted)
Schema: {
	firstfield:  string
	secondfield: int
}

// 1.1.thema
Schema: {
	firstfield:  string
	secondfield: int
	size:        "small" | "medium" | "large" | *"medium"
}
```

## OpenAPI

Generates an OpenAPI 3.0 document whose `components.schemas` holds the schema as one object schema, plus one sibling schema for each definition the schema declares.

**Takes**

- A schema version, written `<major>.<minor>`. The default is the latest version.
- An output format, `json` or `yaml`. The playground's default is `yaml`. The CLI's help text says its default is `yaml` too, but the CLI currently prints JSON unless `-f yaml` is given.

**Output**

- The top level has four fields: `openapi: "3.0.0"`, `info` (`title` is the lineage's name, `version` is the schema's version), `paths: {}`, and `components.schemas`.
- The schema is named after the lineage, exactly as the lineage's name is written: lineage `ship` gives `components.schemas.ship`.
- A definition declared in the schema, `#Point`, becomes its own entry, `components.schemas.Point`. A field whose value is the definition holds `$ref: "#/components/schemas/Point"` instead of the definition's contents.
- `required` lists every field that is not optional, in declaration order. This includes fields that have a default.
- An optional field, `name?: T`, appears under `properties` and not under `required`.
- A default, `*v`, becomes `default: v`.
- A disjunction of concrete values, `"a" | "b"`, becomes `enum`. The default value is listed first. A single concrete value becomes an `enum` with one entry.
- A disjunction of types, `string | int`, becomes `oneOf`. A disjunction of definitions, `#A | #B`, becomes `type: object` with a `oneOf` of `$ref`s.
- `T | null` becomes `T` with `nullable: true`.
- Closed and open structs are generated the same way: no `additionalProperties`, whether or not the struct has `...`.
- A pattern constraint, `[string]: T`, becomes `type: object` with `additionalProperties: T`.
- `[...T]` becomes `type: array` with `items: T`.
- Bounds become `minimum`, `maximum` and `exclusiveMinimum`/`exclusiveMaximum`. `=~"re"` becomes `pattern`. `int32` and similar types become `type: integer` with a matching `format`. `bytes` becomes `type: string`, `format: binary`. `_` becomes `{}`.
- A doc comment on a field becomes its `description`.

The `1.1` schema, as YAML:

```yaml
openapi: 3.0.0
info:
  title: ship
  version: "1.1"
paths: {}
components:
  schemas:
    ship:
      type: object
      required:
        - firstfield
        - secondfield
        - size
      properties:
        firstfield:
          type: string
        secondfield:
          type: integer
        size:
          type: string
          enum:
            - medium
            - small
            - large
          default: medium
```

**CLI**

```
thema lineage gen openapi -l <path> [-p <cue-path>] [-v <version>] [-f json|yaml]
```

- `-l`, `--lineage`: the lineage's directory or `.cue` file. The default is `.`.
- `-p`, `--path`: the CUE path of the lineage within the file, if it is not the file's root.
- `-v`, `--version`: the schema version. The default is the latest version.
- `-f`, `--format`: `json`, `yaml` or `yml`.

The command prints the document to stdout, with no trailing newline. An unrecognized format prints a message to stderr, prints nothing to stdout, and exits 0.

**Go**

- Package `github.com/sdboyer/thema/encoding/openapi`.
- `openapi.GenerateSchema(sch thema.Schema, cfg *openapi.Config) (*ast.File, error)`. The result is CUE syntax (`cuelang.org/go/cue/ast`). Build it with `rt.Context().BuildFile(f)`, then marshal the value with `MarshalJSON`, or with `yaml.Marshal` from `cuelang.org/go/pkg/encoding/yaml`.
- `cfg` may be nil. `openapi.Config` embeds CUE's `*openapi.Config` (`cuelang.org/go/encoding/openapi`), whose `NameFunc`, `DescriptionFunc` and `ExpandReferences` apply, and adds:
  - `RootName`: the name of the schema's entry in `components.schemas`. The default is the lineage's name.
  - `Subpath`: a path within the schema to generate instead of the whole schema. The entry is named after the path's last element.
  - `Group`: generate each top-level field of the schema as its own entry, and no entry for the schema itself.
  - `SplitSchema`: generate the schema and the lineage's join schema as separate entries.

**Limits**

- OpenAPI 3.0 only.
- A schema that refers to a definition it declares fails to generate, with `Lineage: field not found: schemas`, when the lineage is in the authoring form or in a normal-form file that does not import `github.com/sdboyer/thema`. A normal-form file that imports thema and embeds `thema.Lineage` generates as described above.
- Closed structs are not marked closed. Generated object schemas accept fields the CUE schema rejects.
- A fixed-length list, `[A, B]`, becomes `items` holding a list of schemas. OpenAPI 3.0 requires `items` to be a single schema.
- `null` alone, or `null` in a disjunction of more than one other type, becomes `nullable: true` on a schema that has no `type`. OpenAPI 3.0 ignores `nullable` without `type`.
- A construct CUE's OpenAPI encoder cannot express fails the generation. The playground reports it as `E0701`.

## JSON Schema

Generates a JSON Schema (draft 2020-12) of the schema, unified with the lineage's `joinSchema`. The schema is converted by CUE's JSON Schema generator, [`cuelang.org/go/encoding/jsonschema`](https://pkg.go.dev/cuelang.org/go/encoding/jsonschema#Generate), and thema adds the defaults that generator leaves out.

**Takes**

- A schema version, written `<major>.<minor>`. The default is the latest version.
- An output format, `json` or `yaml`. The default is `json` in both the playground and the CLI.

**Output**

- The document is the schema itself: `"$schema": "https://json-schema.org/draft/2020-12/schema"`, then the schema's keywords at the top level.
- A definition the schema refers to, `#Point`, is under `$defs`, as `$defs.Point`, and each use of it is `{"$ref": "#/$defs/Point"}`. A hidden field the schema refers to, `_aMap`, is under `$defs` by its own name, `_aMap`.
- `required` lists every field that is neither optional nor defaulted, sorted by name. A defaulted field may be omitted from an instance, and is listed under `properties` only. In CUE, an open list, `[...T]`, has the default `[]`; a struct whose every field has a default, or that has only pattern constraints, also needs no data, and is not required.
- A field, list element or pattern value whose CUE value has a concrete default has a `default` keyword: `size: *"medium" | ...` gives `"default": "medium"`, and `[...T]` gives `"default": []`.
- A disjunction of concrete values, `"a" | "b"`, becomes `enum`, in declaration order, with no `type`. A single concrete value becomes `const`.
- A disjunction of types, `string | int`, becomes `anyOf`. `T | null` becomes `anyOf` with a `{"type": "null"}` branch.
- Openness is as `thema data validate` reads it: a struct rejects fields it does not declare, `additionalProperties: false`, unless its author wrote `...` in it, `additionalProperties: true`. This holds for the schema itself, nested structs, list elements, pattern values and definitions alike. The JSON Schema accepts exactly the data validation accepts.
- A pattern constraint, `[string]: T`, becomes `additionalProperties: T`.
- `[...T]` becomes `type: array` with `items: T`. A fixed-length list, `[A, B]`, becomes `prefixItems` with `minItems` and `maxItems`.
- Bounds become `minimum`, `maximum`, `exclusiveMinimum` and `exclusiveMaximum`; `int32` and the other sized types become their bounds. `strings.MinRunes(n)` and similar validators become `minLength` and their counterparts. `bytes` becomes `type: string` with `contentEncoding: base64`. `_` becomes `true`.
- A doc comment on a field becomes its `description`.

The `1.1` schema, as JSON:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "firstfield": {
      "type": "string"
    },
    "secondfield": {
      "type": "integer"
    },
    "size": {
      "enum": [
        "small",
        "medium",
        "large"
      ],
      "default": "medium"
    }
  },
  "required": [
    "firstfield",
    "secondfield"
  ]
}
```

**CLI**

```
thema lineage gen jsonschema -l <path> [-p <cue-path>] [-v <version>] [-f json|yaml]
```

- `-l`, `-p` and `-v` are as for [OpenAPI](#openapi).
- `-f`, `--format`: `json`, `yaml` or `yml`. The default is `json`.

Output, trailing newline and unrecognized formats behave as for OpenAPI.

**Go**

- Package `github.com/sdboyer/thema/encoding/jsonschema`.
- `jsonschema.GenerateSchema(sch thema.Schema) (*ast.File, error)`. It takes no configuration. The result is CUE syntax, marshaled as for OpenAPI.

**Limits**

- Draft 2020-12 only.
- CUE marks its JSON Schema generator experimental: its output may change when thema upgrades CUE.
- A struct with a regular-expression pattern constraint, `[=~"^i"]: int`, becomes `{"type": "object"}`: its fields and patterns are dropped, and it accepts any object.
- A default within a branch of a disjunction, `{a: int | *1} | {b: string}`, has no `default` keyword.

## TypeScript

Generates a TypeScript interface for the schema, and a constant holding the defaults of its top-level fields.

**Takes**

- A schema version, written `<major>.<minor>`. The default is the latest version.
- No output format. The playground ignores a format given for TypeScript.

**Output**

- `export interface <Name>`, where `<Name>` is the lineage's name with its first letter in upper case: lineage `ship` gives `Ship`.
- `export const default<Name>: Partial<<Name>>`, holding the default of each top-level field that has one. It is omitted when no top-level field has a default. A `[...T]` field has the default `[]`. Defaults of nested fields are not included.
- The interface's fields are sorted by name. Fields of a nested struct are written inline, as an object type, in declaration order.
- An optional field, `name?: T`, becomes `name?: T`.
- A disjunction of concrete strings becomes a parenthesized union of string literals, in declaration order. The default is not marked in the type; it appears in `default<Name>`.
- A disjunction of types becomes a parenthesized union: `string | int` becomes `(string | number)`, and `T | null` becomes `(T | null)`.
- `int`, `float`, `number` and their sized variants become `number`. `bytes` becomes `string`. `_` becomes `unknown`. `[...T]` becomes `Array<T>`. `[A, B]` becomes `[A, B]`. `[string]: T` becomes `Record<string, T>`.
- A definition the schema refers to is written inline wherever it is used. It becomes its own exported type only if it carries a `@cuetsy(kind="interface")`, `@cuetsy(kind="type")` or `@cuetsy(kind="enum")` attribute.
- Closed and open structs are generated the same way; `...` adds nothing.
- A doc comment on a field becomes a `/** ... */` comment above it.

The `1.1` schema:

```typescript
export interface Ship {
  firstfield: string;
  secondfield: number;
  size: ('small' | 'medium' | 'large');
}

export const defaultShip: Partial<Ship> = {
  size: 'medium',
};
```

**CLI**

None. `thema lineage gen` has no TypeScript subcommand.

**Go**

- Package `github.com/sdboyer/thema/encoding/typescript`.
- `typescript.GenerateTypes(sch thema.Schema, cfg *typescript.TypeConfig) (*ast.File, error)`. The result's type is in thema's internal `cuetsy/ts/ast` package; its `String()` method returns the TypeScript source.
- `cfg` may be nil. `typescript.TypeConfig` has:
  - `RootName`: the interface's name. The default is the lineage's name with its first letter in upper case.
  - `RootAsType`: generate the schema as a type alias, `export type <Name>`, instead of an interface.
  - `Group`: generate no type for the schema itself, only types for its fields that carry `@cuetsy` attributes.
  - `CuetsyConfig`: passed to the underlying generator. Its type is in an internal package, so callers outside thema leave it nil.

**Limits**

- No CLI.
- Bounds, patterns and other constraints are dropped: `int & >=0 & <10` becomes `number`, `=~"^[a-z]+$"` becomes `string`.
- Defaults of nested fields are dropped.
- Closed structs are not distinguished from open ones.
- A construct the generator cannot express fails the generation. The playground reports it as `E0701`.

## Go

Generates Go bindings for the lineage: a Go file with a lineage factory, a function that loads and binds the lineage. It does not generate Go types for schemas.

**Takes**

- The whole lineage.
- A schema version, only with `--bind-type`: the schema the Go type is bound to. The default is the latest version.
- No output format.

**Output**

- A Go file in package `<name>`, the lineage's name in lower case, or the name given with `--pkgname`.
- The CLI writes it to `<name>_bindings_gen.go` beside the lineage, where `<name>` is the lineage's name in lower case, and prints the file's path. With `--stdout` it prints the file instead.
- The CLI begins the file with a `// THIS FILE IS GENERATED. EDITING IS FUTILE.` header. When it writes the file, the header names the embedded path.
- `func Lineage(rt *thema.Runtime, opts ...thema.BindOption) (thema.Lineage, error)`, a `thema.LineageFactory`. It loads the lineage's CUE with `loadInstanceFor<Name>()`, looks up the `-p` path, and calls `thema.BindLineage`. `<Name>` is the lineage's name as written.
- With `--suffix`, the factory is `Lineage<Title>`, where `<Title>` is the lineage's name with its first letter in upper case. With `--private`, it is prefixed with `do`.
- With `--bind-type T`, `Lineage` returns `thema.ConvergentLineage[T]`: it calls a base factory, `baseLineage`, then `thema.BindType` with the schema chosen by `-v`. `T` itself is not generated.
- When a `go.mod` or `cue.mod` is found in the lineage's directory or above it, the file also has `var themaFSFor<Name> embed.FS`, embedding the lineage's file (or `*.cue` for a directory); `getThemaFSFor<Name>()`, which wraps it with `load.AsModFS`; and `loadInstanceFor<Name>()`, which calls `load.InstanceWithThema`. Otherwise, and always with `--stdout`, `loadInstanceFor<Name>()` must be written by hand in another file of the package.

Generated from the normal form of `ship` (`internal/examples/ship.cue`, lineage name `Ship`, at path `lin`), inside a Go module, abridged:

```go
func Lineage(rt *thema.Runtime, opts ...thema.BindOption) (thema.Lineage, error) {
	var inst *build.Instance
	var err error

	inst, err = loadInstanceForShip()
	if err != nil {
		return nil, err
	}

	raw := rt.Context().BuildInstance(inst)
	raw = raw.LookupPath(cue.ParsePath("lin"))

	return thema.BindLineage(raw, rt, opts...)
}

var _ thema.LineageFactory = Lineage

//go:embed ship.cue
var themaFSForShip embed.FS

func getThemaFSForShip() fs.FS {
	return load.AsModFS(themaFSForShip, "example.com/gm")
}

func loadInstanceForShip() (*build.Instance, error) {
	return load.InstanceWithThema(getThemaFSForShip(), ".")
}
```

**CLI**

```
thema lineage gen gobindings -l <path> [-p <cue-path>] [--bind-type <T>] [-v <version>]
    [--pkgname <name>] [--suffix] [--private] [--no-embed] [--no-fs-func] [--stdout] [-q]
```

- `-l` and `-p` are as for [OpenAPI](#openapi).
- `--bind-type`: generate a `ConvergentLineage` bound to this Go type.
- `-v`, `--version`: with `--bind-type`, the schema version to bind. The default is the latest version.
- `--pkgname`: the Go package name. The default is the lineage's name in lower case.
- `--suffix`: name the factory `Lineage<Title>` instead of `Lineage`.
- `--private`: make the factory unexported.
- `--no-embed`: omit the `embed.FS` variable and `getThemaFSFor<Name>()`. `loadInstanceFor<Name>()` still calls `getThemaFSFor<Name>()`, which must then be written by hand.
- `--no-fs-func`: omit `getThemaFSFor<Name>()` and `loadInstanceFor<Name>()`.
- `--stdout`: print the file instead of writing it.
- `-q`, `--quiet`: do not print the generated file's path.

**Go**

- Package `github.com/sdboyer/thema/encoding/gocode`.
- `gocode.GenerateLineageBinding(lin thema.Lineage, cfg *gocode.BindingConfig) ([]byte, error)`. The result is the formatted Go source, without the header comment.
- `gocode.BindingConfig` fields correspond to the CLI's flags: `EmbedPath`, `LoadDir`, `CueModName`, `CuePath`, `FactoryNameSuffix`, `TitleName`, `PrivateFactory`, `Assignee` with `TargetSchemaVersion`, and `PackageName`. `ApplyFuncs` rewrites the generated file before it is formatted. `IgnoreDiscoveredImports` allows `goimports` to add imports the template did not declare; by default that is an error.

**Limits**

- No Go types are generated for schemas.
- The generated loader reads `.cue` files with `load.InstanceWithThema`. A lineage in the authoring form, a directory of `.thema` files, is not loaded by it: for a directory the embed pattern is `*.cue`, which matches none of its files.
- With `--stdout`, or with no `go.mod` or `cue.mod` above the lineage, the generated file does not compile until `loadInstanceFor<Name>()` is written.

## Cap'n Proto

Compiles the whole lineage to a Cap'n Proto schema: one struct per major version, split wherever a minor version is not append-compatible in Cap'n Proto, with ordinals and type IDs derived from the lineage's history so that appending a version never moves one. [Compiling to Cap'n Proto](../capnp.md) describes the type mapping, the generations, the names and every rule in full.

**Takes**

- The whole lineage, in either form.
- A namespace for the type IDs; the default is the lineage's name.

**Output**

- The schema text, deterministic for a lineage.
- The `capnp` tool's layout of it, the `CodeGeneratorRequest` of `capnp compile -o-`, unless asked not to compute it.

**Go**

- Package `github.com/sdboyer/thema/encoding/capnp`.
- `capnp.Compile(lin thema.Lineage, opts ...capnp.Option) (*capnp.Compiled, error)`. `capnp.Namespace` sets the namespace, `capnp.Tool` the `capnp` executable, `capnp.WithoutLayout` skips it, and `capnp.Vocabulary` names constructs in diagnostics in the terms of the form the lineage was written in.
- The error of a lineage that does not compile is a `capnp.Result` listing every finding, which unwraps to one diagnostic per finding.

**Limits**

- A construct Cap'n Proto cannot carry is rejected: an unbounded integer ([E0710](../errors/E0710.md)), a float ([E0711](../errors/E0711.md)), `_` ([E0712](../errors/E0712.md)), a disjunction no value discriminates ([E0713](../errors/E0713.md)), and two names in one struct that are one Cap'n Proto name ([E0714](../errors/E0714.md)).
- A construct the compiler does not carry yet fails too: pattern constraints other than a lone `[string]: T` ([E0730](../errors/E0730.md)), and a closed list of mixed types ([E0731](../errors/E0731.md)).
