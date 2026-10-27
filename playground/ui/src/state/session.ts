// The whole of what a user has in the playground, as one serializable value:
// what a share link carries, and what the page renders.
import type { DataInput, File, LineageInput } from "../api/types";

export type Form = "authoring" | "normal";

// The operations a user runs on data or on the lineage. bind runs on its own
// on every edit; compile, decompile and format are the form controls.
export type DataOp = "validate" | "validate-any" | "translate" | "hydrate" | "dehydrate";
export type CodegenOp = "openapi" | "jsonschema" | "typescript";
export type UserOp = DataOp | CodegenOp;

export const dataOps: readonly DataOp[] = ["validate", "validate-any", "translate", "hydrate", "dehydrate"];
export const codegenOps: readonly CodegenOp[] = ["openapi", "jsonschema", "typescript"];

export interface Args {
  // version is the schema for validate, hydrate, dehydrate and code
  // generation; empty means the latest, or for hydrate the one that accepts
  // the data.
  version: string;
  from: string;
  to: string;
  format: string;
}

export interface Session {
  form: Form;
  // The authoring form: the lineage's name (its directory) and its files.
  name: string;
  files: File[];
  // The normal form: one file, and the lineage's path within it.
  normal: File;
  path: string;
  data: DataInput[];
  op: UserOp;
  args: Args;
  // Which file and data document are open.
  activeFile: number;
  activeData: number;
}

export function emptySession(): Session {
  return {
    form: "authoring",
    name: "ship",
    files: [{ path: "0.0.thema", text: "Schema: {\n\tname: string\n}\n" }],
    normal: { path: "lineage.cue", text: 'name: "ship"\nschemas: {\n\t"0": {\n\t\t"0": {\n\t\t\tschema: {\n\t\t\t\tname: string\n\t\t\t}\n\t\t}\n\t}\n}\n' },
    path: "",
    data: [{ filename: "data.json", text: '{\n  "name": "Argo"\n}\n' }],
    op: "validate",
    args: { version: "", from: "", to: "", format: "" },
    activeFile: 0,
    activeData: 0,
  };
}

// lineageInput is the session's lineage as thema takes it.
export function lineageInput(s: Session): LineageInput {
  if (s.form === "authoring") {
    return { name: s.name, files: s.files };
  }
  return { normal: s.normal, path: s.path || undefined };
}

// The share link's fragment: #s= and the session, as deflated JSON in
// base64url. The URL never reaches a server.
const prefix = "s=";

// shareWarnBytes is where a link starts to be unwieldy to paste; past it the
// page says so, but still makes the link.
export const shareWarnBytes = 30 * 1024;

export async function encodeShare(s: Session): Promise<string> {
  const json = new TextEncoder().encode(JSON.stringify(s));
  const deflated = await pipe(json, new CompressionStream("deflate-raw"));
  return prefix + base64url(deflated);
}

// decodeShare reads a fragment, with or without its #. It returns null for a
// fragment that is not a share link, and throws for one that is but cannot be
// read.
export async function decodeShare(fragment: string): Promise<Session | null> {
  const f = fragment.startsWith("#") ? fragment.slice(1) : fragment;
  if (!f.startsWith(prefix)) return null;
  const inflated = await pipe(unbase64url(f.slice(prefix.length)), new DecompressionStream("deflate-raw"));
  return normalize(JSON.parse(new TextDecoder().decode(inflated)));
}

// normalize fills what a link from an older page lacks, so a link keeps
// working as the session grows fields.
export function normalize(v: Partial<Session>): Session {
  const d = emptySession();
  const s: Session = {
    ...d,
    ...v,
    args: { ...d.args, ...(v.args ?? {}) },
  };
  if (!Array.isArray(s.files) || s.files.length === 0) s.files = d.files;
  if (!Array.isArray(s.data)) s.data = [];
  s.activeFile = clamp(s.activeFile, s.files.length);
  s.activeData = clamp(s.activeData, s.data.length);
  return s;
}

function clamp(i: number, n: number): number {
  return Number.isInteger(i) && i >= 0 && i < n ? i : 0;
}

async function pipe(bytes: Uint8Array, t: CompressionStream | DecompressionStream): Promise<Uint8Array> {
  const source = new ReadableStream<Uint8Array>({
    start(c) {
      c.enqueue(bytes);
      c.close();
    },
  });
  const stream = source.pipeThrough(t);
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

function base64url(b: Uint8Array): string {
  let s = "";
  for (const byte of b) s += String.fromCharCode(byte);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function unbase64url(s: string): Uint8Array {
  const b64 = s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4);
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// dataFor is the data document of an operation's request, or undefined when
// the session has none.
export function dataFor(s: Session): DataInput | undefined {
  return s.data[s.activeData];
}
