# Issues to file against CUE

thema's bugs whose root cause is in [cue-lang/cue](https://github.com/cue-lang/cue),
drafted here to be reviewed and filed by hand: nothing is posted upstream from
this repo. Each entry follows CUE's issue template, so the part under its
heading can be pasted as the issue body; the lines above it are for review
here.

When an issue is filed, record its number on the entry and in the thema issue
it came from. When CUE fixes one, delete the entry, along with any workaround
thema carries for it.

Each repro was last run on 2026-11-06, against v0.17.1 (latest stable) and
v0.18.0-rc.3 (latest pre-release), with Go 1.27.3.

| | Issue | Status | From |
|---|---|---|---|
| 1 | [Expr and ReferencePath race on values with unresolved import references](#1-expr-and-referencepath-race-on-values-with-unresolved-import-references) | Ready | #292 |
| 2 | [A reference to a required field with a concrete value conflicts with that same value](#2-a-reference-to-a-required-field-with-a-concrete-value-conflicts-with-that-same-value) | Ready | #154 |
| 3 | [Subsume refuses a bound written with a builtin package's constant](#3-subsume-refuses-a-bound-written-with-a-builtin-packages-constant) | Ready | #290 |
| 4 | [ast.Clone traps under js/wasm on files of more than 298 fields](#4-astclone-traps-under-jswasm-on-files-of-more-than-298-fields) | Ready | #194 |
| 5 | [A long-lived Context retains every value compiled in it](#5-a-long-lived-context-retains-every-value-compiled-in-it) | Ready, as a question | #292 benchmarks |
| 6 | [OpenAPI encoder fixes from the grafana/cue fork](#6-openapi-encoder-fixes-from-the-grafanacue-fork) | Needs reproductions | #2 |

## 1. Expr and ReferencePath race on values with unresolved import references

Review: a follow-up to cue-lang/cue#4379, which fixed the same lazy
finalization for `Unify` but not for these methods. thema works around it by
keeping `BindLineage` serialized on a Runtime (#292); everything after binding
runs in parallel. CUE's `cue/concurrent_test.go` compiles every value from a
string, with no imports, which is why its suite misses this.

### Title

cue: Value.Expr and Value.ReferencePath are not safe for concurrent use on values with unresolved import references

### What version of CUE are you using?

v0.18.0-rc.3; also v0.17.1.

### Does this issue reproduce with the latest stable release?

Yes, v0.17.1.

### What did you do?

`go test -race -count=3 .` on:

```go
package repro

import (
	"sync"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

func TestReferencePathConcurrent(t *testing.T) {
	ov := map[string]load.Source{
		"/m/cue.mod/module.cue": load.FromString("module: \"example.com/m\"\nlanguage: version: \"v0.13.0\"\n"),
		"/m/lib/lib.cue": load.FromString(`package lib
Def: {name: string, items: [string]: {x: int, y: x + 1}, n: len(items)}
`),
		"/m/m.cue": load.FromString(`package m
import "example.com/m/lib"
a: lib.Def & {name: "a", items: p: x: 1}
b: lib.Def & {name: "b", items: q: x: 2}
c: lib.Def & {name: "c"}
d: lib.Def & {name: "d"}
`),
	}
	bi := load.Instances([]string{"."}, &load.Config{Dir: "/m", Overlay: ov})[0]
	if bi.Err != nil {
		t.Fatal(bi.Err)
	}
	root := cuecontext.New().BuildInstance(bi)
	var wg sync.WaitGroup
	for _, f := range []string{"a", "b", "c", "d"} {
		for range 2 {
			wg.Go(func() {
				_, args := root.LookupPath(cue.ParsePath(f)).Expr()
				for _, a := range args {
					a.ReferencePath()
				}
			})
		}
	}
	wg.Wait()
}
```

### What did you expect to see?

No races: the package docs say values are immutable and all methods can be
invoked concurrently.

### What did you see instead?

DATA RACE reports inside `internal/core/adt` (112 in three runs at v0.18.0-rc.3,
188 at v0.17.1), reached from `Value.ReferencePath` and `Value.Expr`. The same
test with `lib.Def` replaced by a field declared in package `m` reports none.

Without `-race`, the same pattern crashes our code: binding two values of one
loaded package from several goroutines, each calling `ReferencePath` and
`Expr`, panics with `invalid memory address or nil pointer dereference` within
200 runs.

`a.ReferencePath()` on a field whose value refers into the imported package
appears to finalize the shared vertex of `lib.Def` lazily, as #4379 describes
for `Unify`: every field of `m` that refers to `lib.Def` shares that vertex, so
two goroutines finalize it at once.

## 2. A reference to a required field with a concrete value conflicts with that same value

Review: drafted with #154, which bisected it. thema works around it in lens
checking (#180), so nothing in thema waits on the fix.

### Title

evaluator: a reference to a required field with a concrete value conflicts with that same value

### What version of CUE are you using?

v0.18.0-rc.3; also v0.18.0-rc.1, v0.18.0-rc.2, v0.17.1, v0.16.1, v0.15.4 and
master at c4a272fd2c0d (2026-10-28), through the Go API and `cue vet`.

Last good: v0.15.0-alpha.2. First bad: v0.15.0-alpha.3. Bisected to
a25fb94136c3 ("internal/core/adt: report error for missing required fields",
which fixed #3918); its parent, 166a851f1f63, is good.

### Does this issue reproduce with the latest stable release?

Yes (v0.17.1), and with the latest pre-release and master.

### What did you do?

```
exec cue vet -c=false x.cue

-- x.cue --
s: {k!: "U"}
y: s.k & "U"
```

### What did you expect to see?

No error. `y` is incomplete (`s.k` is a required field that is not present),
which `vet -c=false` accepts, as v0.15.0-alpha.2 and earlier do.

### What did you see instead?

```
y: conflicting values "U" and "U":
    ./x.cue:1:9
    ./x.cue:2:4
    ./x.cue:2:10
```

The value conflicts with itself. It happens only when the required field's
value is a concrete scalar and the reference is unified with an equal value:

| case | result |
|---|---|
| `s: {k!: "U"}, y: s.k & "U"` | conflicting values "U" and "U" |
| `s: {k!: 1}, y: s.k & 1` | conflicting values 1 and 1 |
| `s: {k!: true}, y: s.k & true` | conflicting values true and true |
| `s: {n: {k!: "U"}}, y: s.n.k & "U"` | conflicting values "U" and "U" |
| `s: {l: [{k!: "U"}]}, y: [for x in s.l {k: x.k}] & [{k!: "U"}]` | conflicting values "U" and "U" |
| `s: {k!: "U"}, z: s.k, y: z & "U"` | conflicting values "U" and "U" |
| `s: {k!: "U"}, y: {k: s.k} & s` | `y.k`: conflicting values "U" and "U" |
| `s: {k: "U"}, y: s.k & "U"` (regular) | ok |
| `s: {k?: "U"}, y: s.k & "U"` (optional) | ok (incomplete: cannot reference optional field) |
| `s: {k!: string}, y: s.k & "U"` | ok (incomplete: required field missing) |
| `s: {k!: "U" \| "V"}, y: s.k & "U"` | ok (incomplete) |
| `s: {k!: {a: "U"}}, y: s.k & {a: "U"}` | ok (incomplete) |
| `s: {n!: {k!: "U"}}, y: {n: k: s.n.k} & s` | ok |

a25fb94136c3 changed `Vertex.lookup` so that looking up an `ArcRequired` arc
adds an incomplete "required field missing" error to the context but still
returns the arc. For a non-concrete constraint (`string`) the result stays an
incomplete error, as intended. When the arc's value is a concrete scalar, the
incomplete error and the equal concrete value end up reported as a conflict
(an `EvalError`, not incomplete), so `Validate()` without `Concrete` rejects it.

`y: {k: s.k} & s` is how a schema-to-schema mapping is commonly checked: fill a
mapping's input with a schema, and unify its output with the target schema. Any
required single-value enum, such as `kind!: "Usage"` in schemas imported from
OpenAPI or Kubernetes CRDs, can't be carried through such a mapping unchanged.

## 3. Subsume refuses a bound written with a builtin package's constant

Review: thema's compatibility check and lens copy rule are both built on
`Subsume`, so widening `int & <=math.MaxInt8` to `int` is refused as narrowing
(#290). The same bound written `<=127` passes. thema has no workaround yet;
whether to resolve the constant before comparing is open on #290.

### Title

cue: Subsume refuses a bound written with a builtin package's constant

### What version of CUE are you using?

v0.18.0-rc.3; also v0.17.1.

### Does this issue reproduce with the latest stable release?

Yes, v0.17.1.

### What did you do?

```go
package main

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

func main() {
	v := cuecontext.New().CompileString(`
import "math"
a: int & <=math.MaxInt8
b: int & <=127
w: int
`)
	w := v.LookupPath(cue.ParsePath("w"))
	fmt.Println("a:", w.Subsume(v.LookupPath(cue.ParsePath("a"))))
	fmt.Println("a, Final:", w.Subsume(v.LookupPath(cue.ParsePath("a")), cue.Final()))
	fmt.Println("b:", w.Subsume(v.LookupPath(cue.ParsePath("b"))))
}
```

### What did you expect to see?

```
a: <nil>
a, Final: <nil>
b: <nil>
```

`int` subsumes `int & <=127`, whether the bound is written as a literal or as
`math.MaxInt8`, which is 127.

### What did you see instead?

```
a: value not an instance
a, Final: value not an instance
b: <nil>
```

## 4. ast.Clone traps under js/wasm on files of more than 298 fields

Review: breaks thema's browser playground for any lineage whose normal form is
over about 900 AST nodes (#194), because `format.Node` clones the file first
under the v2 formatter. Natively it is fine. thema has no workaround yet.

### Title

cue/ast: Clone traps with "memory access out of bounds" under GOOS=js GOARCH=wasm

### What version of CUE are you using?

v0.18.0-rc.3; also v0.18.0-rc.1 and rc.2. `ast.Clone` is new in v0.18, so
v0.17.1 has nothing to compare.

### Does this issue reproduce with the latest stable release?

No: v0.17.1 has no `ast.Clone`.

### What did you do?

```go
package main

import (
	"fmt"
	"os"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"
)

func main() {
	var b strings.Builder
	n := 299 // 298 is fine
	if len(os.Args) > 1 {
		fmt.Sscan(os.Args[1], &n)
	}
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "f%d: %d\n", i, i)
	}
	f, err := parser.ParseFile("x.cue", b.String())
	if err != nil {
		panic(err)
	}
	ast.Clone(f)
	fmt.Println("cloned", n)
}
```

```
GOOS=js GOARCH=wasm go build -o clone.wasm .
node "$(go env GOROOT)/lib/wasm/wasm_exec_node.js" clone.wasm 298
node "$(go env GOROOT)/lib/wasm/wasm_exec_node.js" clone.wasm 299
```

(Go 1.27.3, Node v26.12.1. Run node with a small environment, e.g. under
`env -i PATH=...`: wasm_exec refuses one over its size limit.)

### What did you expect to see?

```
cloned 298
cloned 299
```

### What did you see instead?

```
cloned 298
RuntimeError: memory access out of bounds
```

The trap is in `runtime.mapaccess2`, called from `ast.Clone`. The same program
built natively clones any size. 299 fields is about 900 nodes; every larger file
traps too.

## 5. A long-lived Context retains every value compiled in it

Review: a question rather than a bug report, since it may be by design.
cue-lang/cue#1424 (2022) blamed a global shared index that the evaluator has
since dropped; this measures a single Context. It matters to anything that
keeps one Context for a long-running server, as `thema.Runtime` does: compiling
each request's data in the Runtime's context grows the heap without bound. In
thema the benchmarks (`internal/bench`) compile data in a context of its own,
which costs about 120ns and keeps nothing, since unifying with a schema from
another context retains nothing in either.

### Title

cue: a long-lived Context retains every value compiled in it

### What version of CUE are you using?

v0.18.0-rc.3; also v0.17.1.

### Does this issue reproduce with the latest stable release?

Yes, v0.17.1.

### What did you do?

`go test -v .` on:

```go
package repro

import (
	"fmt"
	"runtime"
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

func live() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}

func TestContextRetention(t *testing.T) {
	for _, src := range []string{`{"a":"x"}`, `{"a":[1,2,3,4,5,6,7,8]}`, `{"a":{"b":{"c":1}}}`} {
		ctx := cuecontext.New()
		base := live()
		for range 20000 {
			_ = ctx.CompileString(src).Validate()
		}
		fmt.Printf("%6d bytes retained per compile of %s\n", (live()-base)/20000, src)
		runtime.KeepAlive(ctx)
	}
}
```

### What did you expect to see?

Close to zero bytes retained per compile: no value compiled in the loop is
reachable after its iteration.

### What did you see instead?

```
  2499 bytes retained per compile of {"a":"x"}
  5852 bytes retained per compile of {"a":[1,2,3,4,5,6,7,8]}
  3966 bytes retained per compile of {"a":{"b":{"c":1}}}
```

The memory is released when the Context itself becomes unreachable. Is a
Context meant to be short-lived, one per batch of work? If so, the cue and
cuecontext docs could say so: the natural design for a server is one Context
holding its schemas, and every request's data compiled in it.

## 6. OpenAPI encoder fixes from the grafana/cue fork

Review: not ready to file. #2 has the plan: build minimal reproductions against
v0.18 of the two patches thema's old grafana/cue fork carried, then propose
them upstream.

1. `SelectorOp` in object types: `build.go` handles
   `case cue.NoOp, cue.SelectorOp:`, so a selector expression doesn't hit
   "unsupported op for object type".
2. `T | null` format extraction: `extractFormat` treats a two-way `OrOp` whose
   second branch is `null` as its first branch, so `int64 | null` yields
   `format: int64` instead of bounds.

The fork's third patch, `Config.MaxCycleDepth`, was a hack to drop, not to
upstream.
