# Release a new version

Thema is two modules released under one tag: the Go module
`github.com/sdboyer/thema` and the CUE module `github.com/sdboyer/thema@v0`,
published to the [CUE Central Registry](https://registry.cue.works). The Go
module embeds the CUE module's version as `ThemaModuleVersion` in
`internal/util/modfile.go`, and every `cue.mod/module.cue` thema synthesizes
depends on that version, so the constant and the tag must agree.

1. In a commit on `main`, set `ThemaModuleVersion` in
   `internal/util/modfile.go` to the new version (with the `v` prefix, e.g.
   `v0.2.0`). Merge it.
2. From that commit, run `make gen-release tag=v0.2.0`. This refuses a tag that
   differs from `ThemaModuleVersion`, then creates the tag and pushes it to
   `origin`. Tags must have the form `vX.Y.Z`; the release workflow does not
   trigger on anything else.
3. The [release workflow](../.github/workflows/release.yml) does the rest:
   - fails the release if `ThemaModuleVersion` does not equal the tag;
   - runs [GoReleaser](https://goreleaser.com) to build the `thema` CLI and
     create the GitHub release;
   - publishes the CUE module to the Central Registry with
     `cue mod publish <tag>`.

## One-time setup

The `CUE_REGISTRY_TOKEN` repository secret must hold a Central Registry token
with permission to publish `github.com/sdboyer/thema`. Create one at
<https://registry.cue.works> (account settings, tokens). The CUE module
declares `source: kind: "git"`, so only files tracked by git at the tagged
commit are published.
