# @sdboyer/thema-ui

The pieces of the [thema playground](../ui)'s frontend that other browser tools build on. The playground imports everything it shares from this package, so the package's API is exactly what the playground uses.

- `cue`, `cueParser`: the CUE language mode for CodeMirror 6.
- `Editor`: a Preact CodeMirror editor for CUE, JSON and YAML, with markers.
- `Diagnostics`: thema's diagnostics as a list, each linking to its error page.
- `OpBar`: the operations bar: a button per operation, the arguments the chosen one takes, and Run. The host holds the operation and its arguments (`OpArgs`) and passes `ops` to offer only some.
- `Output`: what an operation returned (an `Outcome`): its result in its own terms, lacunas included, then its diagnostics.
- `Tabs`: tabs over named texts, with adding, renaming, removing and a frozen group.
- `dataOps`, `codegenOps`, `argsOf`, `isCodegen` and the `UserOp` types: the operations a user runs and the arguments each takes.
- `markersFor`, `markerDOM`, `filesOf`, `docsURL`, `whereText`: diagnostics as editor markers. A marker says what the diagnostics list says, in its order: the headline, the notes, then the help, a line each. A note the file cannot mark, in another file or in thema's own CUE, is a line of the primary's marker, with where it is as `whereText` says it.
- `offsetOf`, `charIndexOfByte`, `spanAt`: CUE positions (1-based line, byte column) as editor offsets.
- `lightTheme`, `darkTheme`, `highlight`, `tokenColors`, `darkScheme`: the editor's theme.
- `docs`, `DocLink`, `themaDoc`, `lacunaTypeDoc`: links into thema's reference docs.
- The wire types of thema's diagnostics and the playground's session API (`Diagnostic`, `Pos`, `LineageInput`, …). They are generated from thema's Go types by `playground/tsapi` (`github.com/sdboyer/thema/playground/tsapi`), so they match the thema release they come from. A Go program whose own wire types embed thema's can generate them by running the `tsapi.Types` jenny on a `tsapi.Config` of its own. With the Config's `Import` set to `tsapi.UIModule`, the generated file imports thema's types from this package instead of declaring them again.

The components are Preact components. `preact` and the CodeMirror packages are peer dependencies, so the host application provides one copy of each, which CodeMirror requires.

The bar, tabs and output are styled by the host through their class names: `ops`, `bar`, `op-group`, `op`, `op-args`, `primary`; `tabs`, `tab`, `active`, `frozen`, `flag`, `add`, `close`, `tab-group`, `group-label`; `output`, `status`, `ok`, `failed`, `per-version`, `result`, `lacunas`. The diagnostics list and the editor's marker tooltips share theirs, so the host styles the two alike: `diagnostics`, `diagnostic`, `headline`, `code`, `where`, `note`, `help`, `label`, `detail`, and `of` in a tooltip. The playground's `ui/src/style.css` is a complete set.

The theme colors through CSS custom properties that the host page defines: `--bg`, `--panel`, `--text`, `--muted`, `--border`, `--selection`, `--active-line`, `--error`, `--warning`, `--mono`, `--code-size`, and the token colors `--syn-comment`, `--syn-keyword`, `--syn-property`, `--syn-string` and `--syn-type`. The playground's `ui/src/style.css` defines them for light and dark schemes.

## Developing

The package lives in the npm workspace at `playground/`. Run `npm ci` there, then `npm test` and `npm run build --workspace components`. The playground resolves the package to its source (the `source` export condition), so there is no build step between editing a component and seeing it in the playground.
