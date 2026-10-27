## #83 spike findings

Branch: [`spike/capnp`](https://github.com/sdboyer/thema/tree/spike/capnp) (never merged). Probes and their reports are under `internal/spike/`: `q1_lens_ir_test.go` and `q1b_expr_test.go` (lens structure), `q3_cel_test.go` (cel-go, CUE/CEL divergence), `q2_capnp_test.go` with `schema/forms.capnp` (go-capnp, defaults fidelity), and the Q6 test in `q1_lens_ir_test.go`. Each writes a `*_report.txt` that is checked in.

### 1. Lens structure from existing tools

**Verdict: `cueview.Tree` has what the IR needs; `LensField` as it stands does not, and `Expr()` fills in exactly the part syntax can't.**

What the Tree gives, over every bindable lens in `exemplars/`, `testdata/cases/lens-*`, `testdata/known/lens-*` and `testdata/lineage/*`:

- **Copy source paths.** A copied conjunct's `Expr` is the `input.a.b` selector chain the Tree could not follow further (into `_`); the path is read off it. This works through `let`s (`lens-let-copy`), aliases, nested results (`toObj.init <- input.renamed`), and whole-input copies (`result: input`, path `""`).
- **Guards.** Comprehension clauses are in the exported syntax. Presence (`x != _|_`), equality (`x == "bar"`), and type tests (`(x & string) != _|_`) all classify, with negation.
- **Computed operands.** The `Expr` of a computed conjunct is the operator AST: `BinaryExpr`, `Interpolation`, `CallExpr` with `strings.ToUpper` resolved, `IndexExpr` over a table.

Gaps, each needing a change under the `lens-result-fields` seam:

1. **`LensField` is the wrong shape for the IR.** It records only `Origin` and hides its children; no source path, no guards, no operand expression. Its top-level `Result().Origin` is always `computed` (thema's own `struct.MinFields` conjunct). Either build the IR from `cueview.Tree` directly, sharing `ParseLens`'s input resolution, or extend `LensField` with exported children, source path, guards and expression. I recommend the first; `ParseLens` was built for one question.
2. **References into comprehension bodies lose their guards.** `value: result.properbool` (narrowing's lacuna) resolves to both guarded arms, returned as two unguarded conjuncts. `cueview.Conjunct` needs to carry the clauses a reference passed through.
3. **Export reorders operands.** `(input.boolish & string)` comes back `(string & input.boolish)`. Guard and operand classifiers must be order-insensitive.
4. **Kernel conjuncts leak in.** A lacuna written as `thema.Lacuna & {...}` brings thema's constraint struct (with its `error()` pattern constraint) along as a conjunct. The lacuna walk should read only the five declared fields.
5. **Dynamic table index.** `TABLE[input.t]` can't be followed, but `TABLE` resolves to its struct literal, so a value-map node is buildable from the literal plus the index path.

`Expr()` on the lens with `input` filled by the closed source schema gives, for every computed leaf, an operator tree whose leaf references carry `ReferencePath` (`input.n`) and dereference to their constraints: `int32` becomes `int & >=-2147483648 & <=2147483647`, so interval analysis has its operand bounds. But fields that only comprehensions produce **do not exist** in that value (the conditions are undecidable on a schema), and `[if c {x}, y][0]` shows as an index over an opaque list. Branches, presence and head-with-fallback must come from syntax; arithmetic bounds from `Expr()`.

**Corpus coverage is thin.** Apart from narrowing's `input.boolish == "true"`, every bindable lens is copies, constants and guards. No arithmetic, interpolation, or `strings.*` appears anywhere. #87 should add one fixture per op in the op set, or #86's rules will ship untested.

### 2. go-capnp v3 mechanics

Pinned `capnproto.org/go/capnp/v3 v3.1.0-alpha.2` (latest). Dependency graph: `zeropool` only, beyond protobuf and x/.

- **Layout parsing.** `schema.ReadRootCodeGeneratorRequest` reads `capnp compile -o-` output directly. `Node.structNode` gives `dataWordCount`, `pointerCount`, `discriminantCount`, `discriminantOffset`; `Field.slot` gives `offset` (in units of the type's size, or pointer index), `type`, `defaultValue`, `hadExplicitDefault`; `Field.discriminantValue`. Unions are groups: separate nodes sharing the parent's storage.
- **Raw access.** `capnp.Struct.Uint16/Uint32/Uint64`, `Ptr`, `SetPtr`, `SetNewText` at those offsets read and write everything. No generated code needed. **Raw accessors do not apply defaults**: the engine must XOR data defaults and substitute pointer defaults itself, from the layout, as generated code does.
- **Reads past the writer's size** return zero or null, which is what makes append-compatible reads work.
- **Deep copy.** `Struct.CopyFrom` works across layouts: it grows with zero fill and shrinks by truncation; identical layouts copy exactly. `SetPtr` of a struct pointer carries the source's layout with the data, and the reader applies the target's defaults. So a type-compatible copy into a struct with a different layout is correct iff the target layout is append-compatible with the source's; otherwise the engine must convert field-wise. The compiler can decide that per copy edge, statically.
- **Union discriminants are assigned by ordinal, not declaration order.** See 5.

### 3. cel-go

- **Version.** The module is now `cel.dev/cel-go` (v0.32+); `github.com/google/cel-go` fails to resolve. Pin `cel.dev/cel-go v0.33.0`. It pulls `antlr4-go`, `cel.dev/expr`, `genproto/googleapis/api/expr`. The old monolithic `genproto` already in thema's graph made that import ambiguous; `go get` of the split `genproto/googleapis/api` module resolved it.
- **One variable per input path** (`input_n :int`, `input_s :string`) with `cel.Variable` and a map or `cel.Activation` works, no type provider.
- **Overflow is an error, never a wrap**: `+`, `-`, `*`, unary `-` on int64 bounds all return `integer overflow` at eval. Literals beyond int64 fail at compile.
- **Cost.** Compile+Program ≈ 31µs, Eval ≈ 135ns (≈230x). Compile once at `Load`.
- `ast.OutputType()` gives the static result type; type errors fail at compile. `ext.Strings()` is needed for `upperAscii` and friends.

### 4. CUE vs CEL divergence

CUE v0.18 no longer has infix `quo div mod rem`; they are builtin functions. Measured:

| Op | CUE | CEL | Render |
|---|---|---|---|
| `/` on ints | `7 / 2 = 3.5` (float) | `3` | **reject**: not int-valued in CUE |
| `quo(a, b)` | `quo(-7,2) = -3` | `-7 / 2 = -3` | `a / b` |
| `rem(a, b)` | `rem(-7,2) = -1` | `-7 % 2 = -1` | `a % b` |
| `div(a, b)` | `div(-7,2) = -4` | `-3` | `(a - ((a % b + b) % b)) / b` verified |
| `mod(a, b)` | `mod(-7,2) = 1` | `-1` | `((a % b) + b) % b` verified |
| `x / 0` | error | error | needs the div-by-zero proof either way |
| `len(s)` | bytes (`"héllo"` = 6) | `size(s)` code points (5) | `size(bytes(s))` = 6, verified |
| `len(strings.Runes(s))` | 5 | `size(s)` = 5 | `size(s)` |
| `strings.Index` | byte index | `indexOf` code points | **reject** |
| `strings.SliceRunes(s,i,j)` | `"él"` | `substring(i,j)` = `"él"` | `substring` |
| `strings.ToUpper("héllo")` | `"HÉLLO"` | `upperAscii` = `"HéLLO"` | **reject** unless input is proven ASCII |
| `Contains/HasPrefix/Replace/TrimSpace/Join/Split` | same | same | direct |
| `"\(42)"`, `"\(-42)"`, `"\(true)"` | same | `string(x)` | direct |
| `"\(1.0)"` | `"1.0"` | `string(1.0)` = `"1"` | floats excluded, as #82 says |
| int64 overflow | big integer | error | the overflow proof makes both unreachable |
| table miss | error | error | the proof covers the key set |
| `strconv.Atoi("4x")` | error | `int("4x")` error | both error; needs a totality rule |

### 5. Defaults fidelity

Encoded `Old{a: int32, t: Text}` with `a ∈ {3, -1}` and read the bytes through each successor shape (`schema/forms.capnp`). Against the transitions in `docs/invariants.md`:

| Transition | capnp shape | Old bytes read as | CUE minor step | OK? |
|---|---|---|---|---|
| new optional field | union, `unset` at the lower ordinal | `unset` | absent | yes |
| new defaulted field, data | `d @2 :Int64 = 7` | `7` | `7` | yes |
| new defaulted field, pointer | `s @3 :Text = "foo"` | `"foo"` | `"foo"` | yes |
| required → optional | move field into a new union; it keeps ordinal 0 | `value`, `3` / `-1` | present | yes |
| required → defaulted, data | `a @0 :Int32 = 7` | `3 → 4`, `-1 → -8` | `3`, `-1` | **no**: XOR corrupts data |
| required → defaulted, pointer | `t @1 :Text = "foo"` | `"x"` | `"x"` | yes |
| optional → defaulted | union can't become a field | | | new struct, as #82 says |
| int32 → int64 | retyped | `-1 → 4294967295` | `-1` | **no**: new struct, as #82 says |

Two corrections to #82's type-mapping text:

- **"`unset` first" is about ordinals, not declaration order.** The compiler assigns discriminants by ordinal; I wrote `unset @2` before `value @0` and `value` still got discriminant 0. For a field optional from birth, `unset` must take the lower of the two new ordinals. For required → optional, the existing field keeps its ordinal and is automatically discriminant 0, which is exactly the reading old bytes need. The rule should say: *a field that becomes optional moves into a union with a new `unset` member; a new optional field gets `unset` at the lower ordinal.*
- **Required → defaulted on a data field forces a new struct**, or the compiler must never declare capnp defaults for fields that were once required. Pointer fields are fine either way. This is a compatible minor change in CUE that capnp cannot read for free.

The wire records the writer's struct size, and reads past it are zero or null, so none of the "yes" rows needs a copy.

### 6. Type-ID namespace

`cue.Value.BuildInstance()` is non-nil only when the lineage value *is* the root of a build instance (the `testdata/lineage` style). It is nil for a lineage at a path (`lin: thema.Lineage & {...}`, exemplars' `x.l`), and always nil for `lin.Underlying()` after unification. `Pos().Filename()` shows the loader's conventions (`/m/in.cue`, or even a sibling file for a unified value), not the module. **A bound lineage cannot report its CUE module path.**

Decision: the hash namespace is an explicit option on `Compile` (e.g. `capnp.Namespace("github.com/acme/schemas")`), defaulting to the lineage `name` alone, and recorded in the artifact next to each version's #71 content hash. Type IDs: capnp's usual scheme, a 64-bit hash with the high bit set over `(namespace, lineage name, major, struct generation)`; field ordinals by first appearance in lineage history.

### What changes for the sub-issues

- **#86**: the op-set rendering table above decides which computed forms are provable *and* renderable; CEL's overflow and division-by-zero errors mean those proofs are load-bearing for the engine, not just for CUE. `strconv.Atoi`-style partial functions need a rule.
- **#87**: add fixtures for every op in the set (arithmetic, interpolation, `strings.*`, table) and for each field-form transition; the corpus has none.
- **#88**: build the IR from `cueview.Tree` (not `LensField`); extend `cueview.Conjunct` with the comprehension clauses passed through; classifiers order-insensitive; a lacuna walk that reads only the declared fields.
- **#89**: reword the optional-field rule per section 5; required → defaulted data fields start a new struct; rendering table from section 4 (`quo`→`/`, `rem`→`%`, `div`/`mod` rewritten, `len(string)`→`size(bytes(s))`, reject `/` on ints, `strings.Index`, non-ASCII case ops); `Namespace` option; pin `cel.dev/cel-go v0.33.0` and `capnproto.org/go/capnp/v3 v3.1.0-alpha.2`.
- **#90**: the engine applies defaults itself from the layout; compiles CEL at `Load`; uses `CopyFrom` when the target layout is append-compatible with the source's and converts field-wise otherwise.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
