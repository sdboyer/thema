import { describe, expect, it } from "vitest";
import { connect, type WorkerLike } from "./client";

// fakeWorker answers like public/thema-worker.js: progress, ready, then one
// response per call.
function fakeWorker(answer: (op: string, req: unknown) => unknown): WorkerLike & { start(): void; fail(e: string): void } {
  const w: WorkerLike & { start(): void; fail(e: string): void } = {
    onmessage: null,
    postMessage(msg: unknown) {
      const { id, op, request } = msg as { id: number; op: string; request: string };
      queueMicrotask(() => {
        try {
          w.onmessage?.({ data: { id, response: JSON.stringify(answer(op, JSON.parse(request))) } } as MessageEvent);
        } catch (e) {
          w.onmessage?.({ data: { id, error: String(e) } } as MessageEvent);
        }
      });
    },
    start() {
      w.onmessage?.({ data: { type: "progress", loaded: 10 } } as MessageEvent);
      w.onmessage?.({ data: { type: "ready", init: { ok: true, diagnostics: [], themaVersion: "v0.1.0", cueVersion: "v0", ops: ["bind"] } } } as MessageEvent);
    },
    fail(error: string) {
      w.onmessage?.({ data: { type: "failed", error } } as MessageEvent);
    },
  };
  return w;
}

describe("connect", () => {
  it("resolves when thema is ready, and reports progress", async () => {
    const w = fakeWorker(() => ({}));
    const progress: number[] = [];
    const p = connect(w, (n) => progress.push(n));
    w.start();
    const thema = await p;
    expect(thema.init.themaVersion).toBe("v0.1.0");
    expect(progress).toEqual([10]);
  });

  it("matches responses to calls", async () => {
    const w = fakeWorker((op, req) => ({ ok: true, diagnostics: [], echo: { op, req } }));
    const p = connect(w);
    w.start();
    const thema = await p;
    const [a, b] = await Promise.all([
      thema.call("bind", { lineage: { name: "a" } }),
      thema.call("format", { file: { path: "0.0.thema", text: "x" } }),
    ]);
    expect((a as unknown as { echo: { op: string } }).echo.op).toBe("bind");
    expect((b as unknown as { echo: { op: string } }).echo.op).toBe("format");
  });

  it("rejects a call the worker could not answer", async () => {
    const w = fakeWorker(() => {
      throw new Error("boom");
    });
    const p = connect(w);
    w.start();
    const thema = await p;
    await expect(thema.call("bind", { lineage: {} })).rejects.toThrow("boom");
  });

  it("rejects when thema cannot start", async () => {
    const w = fakeWorker(() => ({}));
    const p = connect(w);
    w.fail("fetching thema.wasm: 404");
    await expect(p).rejects.toThrow("404");
  });
});
