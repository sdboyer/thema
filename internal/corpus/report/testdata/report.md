### Corpus `testdata/refs`: `before..after`

Changes: **regressed: 2**, **xpass: 1**, no longer applies: 1, #expect changed: 1, fixed: 1, now applies: 1, #known changed: 1, rendering changed: 2, removed case: 1, removed suite: 1, new case: 5, new suite: 2.

Cells in after: regressed: 1, xpass: 1, known: 4, pass: 10, not-applicable: 28.

| Case | Suite | Before → After | Change |
|---|---|---|---|
| `accepted` | `s1` | pass → known | regressed |
| `regress` | `s1` | pass → regressed | regressed |
| `xpass` | `s1` | known → xpass | xpass |
| `applies` | `s1` | pass → not-applicable | no longer applies |
| `expect` | `s1` | pass → pass | #expect changed, rendering changed |
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
