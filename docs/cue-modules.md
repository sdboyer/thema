# CUE modules

Thema's CUE is a CUE module, `github.com/sdboyer/thema@v0`, published to the [Central Registry](https://registry.cue.works). Lineages import it as `github.com/sdboyer/thema`, from a CUE module that declares it as a dependency:

```bash
cue mod init github.com/example/ships
cue mod get github.com/sdboyer/thema@v0
```

That's all plain `cue` needs to evaluate lineages.

## Loading lineages in Go

Thema's Go library and its CUE depend on each other's exact shape, so the Go library always evaluates lineages against the copy of thema's CUE built into it, never one fetched from a registry. When the Go library loads a CUE module that depends on thema:

- thema's CUE module is served from that built-in copy, so loading never needs the network for thema itself. Other dependencies are fetched from the configured registry as usual.
- The version of `github.com/sdboyer/thema@v0` in the module's `cue.mod/module.cue` is a minimum, as every CUE dependency's is. A module that requires the library's built-in version, or an older one, is loaded against the built-in copy. A newer version is an error (E0607) that names the newest version the library can load.

Thema's Go and CUE modules are released together under the same version, so a module written against a thema release loads with that release of the Go module or any later one, without changing its `cue.mod/module.cue`. When a dependency of the module requires a newer thema than the module does, the newest requirement is the one loaded, and the error names it.

Loaded by a later release, a module is evaluated against that release's CUE, not the one it was written against. Before v1, thema's CUE and its authoring form can change between minor versions, so such a module can fail where it loaded before. The failure is reported as what the newer release refuses, not as a version mismatch, and the release notes say what changed.

Plain `cue` fetches the version the module requires, so it can evaluate a module against older thema CUE than the Go library does. When the two disagree, thema's Go library decides: its checks are what make a lineage valid.

To load CUE that imports thema with your own `load.Config`, pass it through [`load.Configure`](https://pkg.go.dev/github.com/sdboyer/thema/load#Configure), which sets up the built-in module. `load.InstanceWithThema` does this for you.

## Error positions

Errors that point into thema's own CUE show it at a path naming the module and its version, such as `github.com/sdboyer/thema@v0.1.0/lineage.cue:42:3`, the same shape as a path in CUE's module cache.
