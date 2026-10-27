// Runs the synced thema.wasm in the test's own process, the way
// public/thema-worker.js runs it in a worker, for tests that need thema's
// real answers. Returns null when npm run sync-thema has not been run.
import { existsSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { resolve } from "node:path";
import { type Thema, type WorkerLike, connect } from "../api/client";

const pub = resolve(__dirname, "../../public");

export function haveThema(): boolean {
  return ["thema.wasm", "wasm_exec.js", "thema-fs.js"].every((f) => existsSync(resolve(pub, f)));
}

let started: Promise<Thema> | null = null;

export function realThema(): Promise<Thema> {
  if (started) return started;
  const g = globalThis as unknown as Record<string, unknown>;
  // thema-fs.js installs globalThis.fs only when there is none.
  delete g.fs;
  const require = createRequire(import.meta.url);
  require(resolve(pub, "thema-fs.js"));
  require(resolve(pub, "wasm_exec.js"));

  const worker: WorkerLike & { send(data: unknown): void } = {
    onmessage: null,
    send(data) {
      this.onmessage?.({ data } as MessageEvent);
    },
    postMessage(msg) {
      const { id, op, request } = msg as { id: number; op: string; request: string };
      const call = g.themaCall as (op: string, req: string) => string;
      setTimeout(() => worker.send({ id, response: call(op, request) }), 0);
    },
  };
  started = connect(worker);
  const ready = new Promise<void>((r) => {
    g.onThemaReady = r;
  });
  const Go = g.Go as new () => { importObject: WebAssembly.Imports; run(i: WebAssembly.Instance): Promise<void> };
  const go = new Go();
  void WebAssembly.instantiate(readFileSync(resolve(pub, "thema.wasm")), go.importObject).then(async ({ instance }) => {
    void go.run(instance);
    await ready;
    const init = JSON.parse((g.themaInit as () => string)());
    worker.send({ type: "ready", init });
  });
  return started;
}
