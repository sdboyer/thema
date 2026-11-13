# @sdboyer/thema-playground

The thema playground, built: a static site that binds, validates, translates and generates code from thema lineages, with thema running in the browser as WebAssembly. The package's version is the thema release it was built from.

`dist/` is the whole site. Serve it from any directory: every URL in it is relative to the page's, so the directory's URL needs its trailing slash. It holds the frontend, `thema.wasm` (and `thema.wasm.gz`) with the scripts that run it (as in [`@sdboyer/thema-wasm`](../wasm)), `types.ts`, the wire format's TypeScript declarations, and `MANIFEST.json`, the thema version and the digest of `thema.wasm`.

The examples the playground offers are its own. A host adds its own lineages by serving an `examples.json` beside `index.html`, as `thema playground examples <dir>` writes it.

`thema serve playground` serves the same site from the CLI.

## Developing

The source is the npm workspace at `playground/`, with the editor components in [`@sdboyer/thema-ui`](../components). See `cmd/thema/playground` for how the site is built.
