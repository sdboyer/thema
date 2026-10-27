// Runs thema.wasm off the page's thread. A classic worker, not bundled: it
// loads thema's own scripts beside it, thema-fs.js before wasm_exec.js as
// thema requires, and they and thema.wasm are whatever thema build was put
// next to it (npm run sync-thema, or thema serve playground's own).
//
// Messages to the page:
//   {type: "progress", loaded}  bytes of thema.wasm received so far
//   {type: "ready", init}       thema's init response, parsed
//   {type: "failed", error}     thema could not start
//   {id, response}              the response to call id, as JSON text
// Messages from the page: {id, op, request}, request as JSON text.
importScripts("thema-fs.js", "wasm_exec.js");

const ready = new Promise((resolve) => {
  self.onThemaReady = resolve;
});

async function fetchWasm() {
  const resp = await fetch("thema.wasm");
  if (!resp.ok) {
    throw new Error(`fetching thema.wasm: ${resp.status} ${resp.statusText}`);
  }
  const reader = resp.body.getReader();
  const chunks = [];
  let loaded = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    chunks.push(value);
    loaded += value.length;
    self.postMessage({ type: "progress", loaded });
  }
  const bytes = new Uint8Array(loaded);
  let at = 0;
  for (const c of chunks) {
    bytes.set(c, at);
    at += c.length;
  }
  return bytes;
}

async function start() {
  const go = new Go();
  const { instance } = await WebAssembly.instantiate(await fetchWasm(), go.importObject);
  go.run(instance);
  await ready;
  self.postMessage({ type: "ready", init: JSON.parse(self.themaInit()) });
}

const started = start().catch((err) => {
  self.postMessage({ type: "failed", error: String(err && err.message ? err.message : err) });
  throw err;
});

self.onmessage = async (e) => {
  const { id, op, request } = e.data;
  try {
    await started;
    self.postMessage({ id, response: self.themaCall(op, request) });
  } catch (err) {
    self.postMessage({ id, error: String(err) });
  }
};
