// Loads thema.wasm the way a page's worker does, through Go's wasm_exec.js,
// and exercises the bridge: init, an example, bind, translate, and a call
// that must fail with a diagnostic rather than throw.
//
// It loads thema-fs.js first, as a page must: node's own fs is never handed
// to Go, so the module sees what a browser gives it.
//
// usage: node smoke.mjs <thema-fs.js> <wasm_exec.js> <thema.wasm>
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";

const [themaFS, wasmExec, wasmPath] = process.argv.slice(2);
const require = createRequire(import.meta.url);
require(themaFS);
require(wasmExec);

const ready = new Promise((resolve) => {
  globalThis.onThemaReady = resolve;
});
const go = new globalThis.Go();
const { instance } = await WebAssembly.instantiate(readFileSync(wasmPath), go.importObject);
go.run(instance);
await ready;

const call = (op, req) => JSON.parse(globalThis.themaCall(op, JSON.stringify(req)));
const fail = (msg, v) => {
  console.error(msg, JSON.stringify(v, null, 2));
  process.exit(1);
};

const init = JSON.parse(globalThis.themaInit());
if (!init.ok || !init.ops.includes("translate")) fail("init", init);

const ex = call("example", { id: "authoring/ship" });
if (!ex.ok) fail("example", ex);

const bound = call("bind", { lineage: ex.lineage });
if (!bound.ok || bound.versions.join(",") !== "0.0,1.0") fail("bind", bound);

const tr = call("translate", {
  lineage: ex.lineage,
  data: { filename: "data.json", text: '{"firstfield": "x"}' },
  from: "0.0",
  to: "1.0",
});
if (!tr.ok || tr.output.secondfield !== -1 || tr.lacunas.length !== 1) fail("translate", tr);

// A lineage that imports thema exercises CUE's module loader inside wasm.
const imp = call("bind", {
  lineage: {
    normal: {
      path: "lineage.cue",
      text: 'package p\n\nimport "github.com/sdboyer/thema"\n\nlin: thema.Lineage & {\n\tname: "p"\n\tschemas: "0": "0": schema: {a: string}\n}\n',
    },
    path: "lin",
  },
});
if (!imp.ok) fail("bind with import", imp);

const bad = call("translate", { lineage: ex.lineage, data: { filename: "d.json", text: "{}" }, to: "9.0" });
if (bad.ok || bad.diagnostics[0].code !== "E0240") fail("translate to a missing version", bad);

console.log(`ok thema ${globalThis.themaVersion()}`);
process.exit(0);
