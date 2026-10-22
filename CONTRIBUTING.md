# Contributing to Thema

## Development

- `go test ./...` runs the full suite. Lineage behavior is covered by golden
  txtar files under `testdata/`; set `THEMA_UPDATE_GOLDEN=1` to regenerate them,
  and review the resulting diff carefully.
- Changes touching the seams declared in `.thresh/thresh.cue` (the CUE kernel,
  the CUE dependency, `BindLineage`, published lineages) should be checked with
  `thresh check`.
