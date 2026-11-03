### Corpus `testdata/refs`: `before..after`

Changes: **regressed: 2**, **xpass: 1**, **no longer applies: 1**, status changed: 1, **known verdict changed: 1**, **#expect changed: 2**, fixed: 1, now applies: 1, #known changed: 1, rendering changed: 3, removed case: 1, removed suite: 1, new case: 5, new suite: 2.

Cells in after: regressed: 1, xpass: 1, known: 5, pass: 11, not-applicable: 34.

**#expect changed, which changes what the spec says:**

- `expect`: was `ok`, now `E0141` (spec: docs/invariants.md:140-145)
- `respec`: was `E0141`, now `ok` (spec: docs/invariants.md:140-145)

**Regressions a new #known tag accepts:**

- `accepted` × `s1`: #264 the lineage is at the top of a root

**Failing cells in after**, changed in this range or not:

- `regress` × `s1`: regressed
- `xpass` × `s1`: xpass

| Case | Suite | Before → After | Change |
|---|---|---|---|
| `accepted` | `s1` | pass → known | **regressed** |
| `regress` | `s1` | pass → regressed | **regressed** |
| `xpass` | `s1` | known → xpass | **xpass** |
| `applies` | `s1` | pass → not-applicable | **no longer applies** |
| `respec` | `s1` | known → pass | status changed, **#expect changed** |
| `moved` | `s1` | known → known | **known verdict changed**, rendering changed |
| `expect` | `s1` | pass → pass | **#expect changed**, rendering changed |
| `fixed` | `s1` | known → pass | fixed |
| `applies` | `s2` | not-applicable → pass | now applies |
| `retag` | `s1` | known → known | #known changed |
| `render` | `s1` | pass → pass | rendering changed |
| `removed` | `s1` | pass → absent | removed case |
| `steady` | `old` | pass → absent | removed suite |
| `added` | `s1` | absent → pass | new case |
| `added` | `s2` | absent → known | new case |
| `wide` | 3 suites | absent → pass | new case |
| `steady` | `s4` | absent → pass | new suite |

A cell that no longer applies lost its section. The tests decide whether a suite applies, so only `go test` can say whether that is right.
