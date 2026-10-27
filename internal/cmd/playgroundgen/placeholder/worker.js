// Runs thema.wasm off the page's thread: thema-fs.js before wasm_exec.js,
// as cmd/thema-wasm requires, then one message per call.
importScripts("thema-fs.js", "wasm_exec.js");

const ready = new Promise((resolve) => {
  self.onThemaReady = resolve;
});
const go = new Go();
WebAssembly.instantiateStreaming(fetch("thema.wasm"), go.importObject).then(({ instance }) => {
  go.run(instance);
});

ready.then(() => {
  self.postMessage({ ready: JSON.parse(self.themaInit()) });
});

self.onmessage = async (e) => {
  await ready;
  const { id, op, request } = e.data;
  try {
    self.postMessage({ id, response: self.themaCall(op, request) });
  } catch (err) {
    self.postMessage({ id, error: String(err) });
  }
};
