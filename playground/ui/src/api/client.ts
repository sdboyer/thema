// The page's side of the thema worker: a typed call per operation.
import type {
  BindRequest,
  BindResponse,
  CodegenRequest,
  CodegenResponse,
  CompileRequest,
  CompileResponse,
  DecompileRequest,
  DecompileResponse,
  FormatRequest,
  FormatResponse,
  GetExampleRequest,
  GetExampleResponse,
  HydrateRequest,
  HydrateResponse,
  InitResponse,
  ListExamplesResponse,
  TranslateRequest,
  TranslateResponse,
  ValidateAnyRequest,
  ValidateAnyResponse,
  ValidateRequest,
  ValidateResponse,
} from "./types";

// Ops maps each operation to its request and response.
export interface Ops {
  bind: [BindRequest, BindResponse];
  validate: [ValidateRequest, ValidateResponse];
  "validate-any": [ValidateAnyRequest, ValidateAnyResponse];
  translate: [TranslateRequest, TranslateResponse];
  hydrate: [HydrateRequest, HydrateResponse];
  dehydrate: [HydrateRequest, HydrateResponse];
  openapi: [CodegenRequest, CodegenResponse];
  jsonschema: [CodegenRequest, CodegenResponse];
  typescript: [CodegenRequest, CodegenResponse];
  compile: [CompileRequest, CompileResponse];
  decompile: [DecompileRequest, DecompileResponse];
  format: [FormatRequest, FormatResponse];
  examples: [Record<string, never>, ListExamplesResponse];
  example: [GetExampleRequest, GetExampleResponse];
}

export type Op = keyof Ops;

export interface Thema {
  init: InitResponse;
  call<K extends Op>(op: K, req: Ops[K][0]): Promise<Ops[K][1]>;
}

// The shapes the worker posts; see public/thema-worker.js.
type FromWorker =
  | { type: "progress"; loaded: number }
  | { type: "ready"; init: InitResponse }
  | { type: "failed"; error: string }
  | { id: number; response?: string; error?: string };

// Worker is the part of a Web Worker the client uses, so tests can give it
// a fake.
export interface WorkerLike {
  postMessage(msg: unknown): void;
  onmessage: ((e: MessageEvent) => void) | null;
}

// connect starts thema in worker and resolves once it is ready. onProgress
// hears how much of thema.wasm has arrived.
export function connect(worker: WorkerLike, onProgress?: (loaded: number) => void): Promise<Thema> {
  let next = 0;
  const pending = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>();
  return new Promise((resolve, reject) => {
    worker.onmessage = (e: MessageEvent) => {
      const m = e.data as FromWorker;
      if ("type" in m) {
        switch (m.type) {
          case "progress":
            onProgress?.(m.loaded);
            return;
          case "failed":
            reject(new Error(m.error));
            return;
          case "ready":
            resolve({
              init: m.init,
              call<K extends Op>(op: K, req: Ops[K][0]): Promise<Ops[K][1]> {
                const id = ++next;
                return new Promise<Ops[K][1]>((res, rej) => {
                  pending.set(id, { resolve: res as (v: unknown) => void, reject: rej });
                  worker.postMessage({ id, op, request: JSON.stringify(req) });
                });
              },
            });
            return;
        }
      }
      const p = pending.get(m.id);
      if (!p) return;
      pending.delete(m.id);
      if (m.error !== undefined) {
        p.reject(new Error(m.error));
      } else {
        p.resolve(JSON.parse(m.response ?? "null"));
      }
    };
  });
}

// startThema starts the worker beside the page.
export function startThema(onProgress?: (loaded: number) => void): Promise<Thema> {
  return connect(new Worker(new URL("./thema-worker.js", document.baseURI)), onProgress);
}
