# @sdboyer/thema-ui

The pieces of the [thema playground](../ui)'s frontend that other browser tools build on. The playground imports everything it shares from this package, so the package's API is exactly what the playground uses.

- `cue`, `cueParser`: the CUE language mode for CodeMirror 6.
- `Editor`: a Preact CodeMirror editor for CUE, JSON and YAML, with markers.
- `Diagnostics`: thema's diagnostics as a list, each linking to its error page.
- `markersFor`, `filesOf`, `docsURL`: diagnostics as editor markers.
- `offsetOf`, `charIndexOfByte`, `spanAt`: CUE positions (1-based line, byte column) as editor offsets.
- `lightTheme`, `darkTheme`, `highlight`, `tokenColors`, `darkScheme`: the editor's theme.
- `docs`, `DocLink`, `themaDoc`, `lacunaTypeDoc`: links into thema's reference docs.
- The wire types of thema's diagnostics and the playground's session API (`Diagnostic`, `Pos`, `LineageInput`, …). They are generated from thema's Go types by `playground/tsapi` (`github.com/sdboyer/thema/playground/tsapi`), so they match the thema release they come from. A Go program whose own wire types embed thema's can generate them with `tsapi.Generate`, which imports thema's types from this package instead of declaring them again.

The components are Preact components. `preact` and the CodeMirror packages are peer dependencies, so the host application provides one copy of each, which CodeMirror requires.

The theme colors through CSS custom properties that the host page defines: `--bg`, `--panel`, `--text`, `--muted`, `--border`, `--selection`, `--active-line`, `--error`, `--mono`, `--code-size`, and the token colors `--syn-comment`, `--syn-keyword`, `--syn-property`, `--syn-string` and `--syn-type`. The playground's `ui/src/style.css` defines them for light and dark schemes.

## Developing

The package lives in the npm workspace at `playground/`. Run `npm ci` there, then `npm test` and `npm run build --workspace components`. The playground resolves the package to its source (the `source` export condition), so there is no build step between editing a component and seeing it in the playground.
