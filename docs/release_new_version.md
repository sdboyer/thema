# Release a new version

A thema release is one tag, published in five places:

- the Go module `github.com/sdboyer/thema`, by the tag itself;
- the CUE module `github.com/sdboyer/thema@v0`, on the
  [CUE Central Registry](https://registry.cue.works);
- a GitHub release, with the `thema` CLI for each platform and the
  playground's wasm assets;
- on npm, at the tag's version without its `v`:
  - `@sdboyer/thema-ui`, the editor components and wire types, with thema's
    error pages under `errors/`;
  - `@sdboyer/thema-wasm`, `thema.wasm` and the scripts that run it in a
    browser;
  - `@sdboyer/thema-playground`, the playground, built, as a static site.

The Go module embeds the CUE module's version as `ThemaModuleVersion` in
`internal/util/modfile.go`, and every `cue.mod/module.cue` thema synthesizes
depends on that version, so the constant and the tag must agree.

None of it can be undone. Once a tag is pushed, the first `go get` of it
records it in the Go checksum database for good, and npm and the Central
Registry never take a version twice. So as much as can be checked is checked
before the tag exists. CI's `release` job rehearses, on every pull request and
on `main`, what can be rehearsed without a tag: GoReleaser's build, as a
snapshot; the npm packages, staged and published with `--dry-run`; and the
CUE module, published with `--dry-run`. `make release` tags only a commit that
passed it. What needs a tag (GoReleaser's choice of tag and its draft
release, the registries' trust in the workflow, the `release` environment) is
exercised only by a release: thema has no release candidates.

## Releasing

1. In a pull request, set `ThemaModuleVersion` to the new version, with its
   `v` (`v0.2.0`). Merge it.
2. On `main`, up to date with `origin`, run `make release tag=v0.2.0`. It
   refuses, before anything is pushed:
   - a tag that is not `vX.Y.Z`, or that is not `ThemaModuleVersion`;
   - a working tree with changes, or a `HEAD` that is not `origin/main`;
   - a tag that exists, or a version older than one tagged already;
   - a commit whose CI has not passed.

   Then it creates the tag, signed, and pushes it.
3. The [release workflow](../.github/workflows/release.yml) publishes it. In
   order, each step after the one before succeeds:
   1. a draft GitHub release, built by [GoReleaser](https://goreleaser.com);
   2. the CUE module;
   3. the npm packages, staged by `playground/scripts/release.mjs` from the
      same playground bundle the GitHub release attaches;
   4. the GitHub release, published.

   It refuses a tag `make release-check` refuses, or one on a commit that is
   not on `main`.

If a step fails, fix what failed outside the repository (a registry outage, a
missing permission) and re-run the workflow's failed jobs. Every step skips
what is already published, so a re-run finishes the release. Never move or
re-push a tag: the Go module is published already. A release that is wrong
is fixed by the next one.

## One-time setup

Every registry trusts the release workflow by GitHub Actions OIDC, so no
registry token is stored in the repository.

- **The CUE Central Registry.** At
  <https://registry.cue.works/account/oidc>, add a trust entry for the
  `sdboyer/thema` repository, limited to the `.github/workflows/release.yml`
  workflow and `refs/tags/v*` refs, acting as an account that can publish
  `github.com/sdboyer/thema`. The CUE module declares `source: kind: "git"`,
  so only files tracked by git at the tagged commit are published.
- **The `release` environment.** The npm job runs in the repository's
  `release` environment, which must deploy only from `v*` tags. npm checks
  the workflow's file name, not the ref it runs from, so without this a branch
  that edits `release.yml` could publish. A job naming an environment that
  does not exist creates it unrestricted, so create it before the first tag:

  ```
  gh api -X PUT repos/sdboyer/thema/environments/release \
    -F 'deployment_branch_policy[protected_branches]=false' \
    -F 'deployment_branch_policy[custom_branch_policies]=true'
  gh api -X POST repos/sdboyer/thema/environments/release/deployment-branch-policies \
    -f name='v*' -f type=tag
  ```
- **npm.** Each package must exist before it can trust a workflow, so each
  was first published by hand, as `0.0.0-bootstrap.0`; npm's processing of
  that publish also left a `0.0.0-stage` version. Then, on each package's
  settings page at npmjs.com, add a trusted publisher: GitHub Actions,
  owner `sdboyer`, repository `thema`, workflow `release.yml`, environment
  `release`, and, under Allowed actions, **npm publish**. npm allows a new
  trusted publisher only `npm stage publish` unless told otherwise, and the
  workflow publishes directly; without it, the npm job fails with
  `E403 OIDC permission denied`. A trusted publisher can't be edited, only
  deleted and added again. A trusted publisher that publishes nothing within
  two days expires, so add them when a tag is ready to follow. Then set each
  package's publishing access to require two-factor authentication and
  disallow tokens, so that only the workflow publishes. The placeholder
  versions are deprecated.
