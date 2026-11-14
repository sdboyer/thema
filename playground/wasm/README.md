# @sdboyer/thema-wasm

thema compiled to WebAssembly, and the scripts that run it in a browser, as the [thema playground](../ui) runs it. The package's version is the thema release it was built from.

- `thema.wasm`: thema, built from `cmd/thema-wasm` with `GOOS=js GOARCH=wasm`. `thema.wasm.gz` is the same, compressed.
- `wasm_exec.js`: Go's loader for it, from the Go release that built it.
- `thema-fs.js`: the filesystem thema needs in a browser, where there is none. Load it before `wasm_exec.js`.
- `thema-worker.js`: a classic Web Worker that runs thema off the page's thread. It loads the other three from beside itself, so serve them from one directory. Its message protocol is in its header comment.
- `MANIFEST.json`: the thema version and the digest of `thema.wasm`.

The requests and responses of thema's calls are typed by the wire types in [`@sdboyer/thema-ui`](../components) of the same version.

Each file is exported by its name (`@sdboyer/thema-wasm/thema-worker.js`). They are files to serve, not modules to import: copy them into the directory a page serves its static assets from.
