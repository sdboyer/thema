package util

import "fmt"

// LanguageVersion is the CUE language version declared by thema's own
// cue.mod/module.cue. Every module.cue that thema synthesizes declares the same
// version, so that thema's CUE and the CUE it loads alongside it are evaluated
// under the same language semantics.
const LanguageVersion = "v0.17.0"

// ModuleFile returns the contents of a cue.mod/module.cue file declaring the
// provided module path, [LanguageVersion], and a dependency on thema's CUE module
// at [ThemaModuleVersion].
func ModuleFile(modpath string) string {
	return fmt.Sprintf("module: %q\nlanguage: version: %q\ndeps: %q: v: %q\n",
		modpath, LanguageVersion, ThemaModulePath, ThemaModuleVersion)
}

// ThemaModulePath is the path of thema's CUE module, as declared in its
// cue.mod/module.cue.
const ThemaModulePath = "github.com/sdboyer/thema@v0"

// ThemaModuleVersion is the version of thema's CUE module embedded in this
// version of the thema Go module. It is bumped as part of each release, so that
// the Go module and the published CUE module carry the same version.
const ThemaModuleVersion = "v0.1.0"

// ThemaPackageID is the canonical CUE import path of thema's CUE package, which
// lives at the root of [ThemaModulePath]. Thema's hidden fields (e.g. _schema)
// are scoped to this package identity, so it is the qualifier for any [cue.Hid]
// selector targeting them.
const ThemaPackageID = ThemaModulePath
