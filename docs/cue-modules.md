# CUE modules

Thema's CUE is a CUE module, `github.com/sdboyer/thema@v0`, published to the [Central Registry](https://registry.cue.works). Lineages import it as `github.com/sdboyer/thema`, from a CUE module that declares it as a dependency:

```bash
cue mod init github.com/example/ships
cue mod get github.com/sdboyer/thema@v0
```

That's all plain `cue` needs to evaluate lineages.

A [collection](authoring-form.md#lineagecue) of lineages in the authoring form keeps each lineage's normal form in a generated `lineage.cue`, a package that CUE imports by the lineage's directory's path, from the collection's module or from one that depends on it.

## Loading lineages in Go

Thema's Go library and its CUE depend on each other's exact shape, so the Go library always evaluates lineages against the copy of thema's CUE built into it, never one fetched from a registry. When the Go library loads a CUE module that depends on thema:

- thema's CUE module is served from that built-in copy, so loading never needs the network for thema itself. Other dependencies are fetched from the configured registry as usual.
- The version of `github.com/sdboyer/thema@v0` in the module's `cue.mod/module.cue` must match the library's built-in version exactly. Any other version is an error that names the version to use.

Thema's Go and CUE modules are released together under the same version, so the CUE dependency version to use is the version of the Go module.

To load CUE that imports thema with your own `load.Config`, pass it through [`load.Configure`](https://pkg.go.dev/github.com/sdboyer/thema/load#Configure), which sets up the built-in module. `load.InstanceWithThema` does this for you.

## Error positions

Errors that point into thema's own CUE show it at a path naming the module and its version, such as `github.com/sdboyer/thema@v0.1.0/lineage.cue:42:3`, the same shape as a path in CUE's module cache.
