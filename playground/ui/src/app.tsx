import { useCallback, useEffect, useMemo, useRef, useState } from "preact/hooks";
import { type Thema, startThema } from "./api/client";
import type { BindResponse, Diagnostic, ExampleInfo, GetExampleResponse } from "./api/types";
import { argsOf, isCodegen, requestFor } from "./state/requests";
import {
  type Session,
  type UserOp,
  codegenOps,
  dataOps,
  decodeShare,
  emptySession,
  encodeShare,
  lineageInput,
  shareWarnBytes,
} from "./state/session";
import { Diagnostics } from "./ui/Diagnostics";
import { Editor, type Language } from "./ui/Editor";
import { filesOf, markersFor } from "./ui/markers";
import { type Outcome, Output } from "./ui/Output";
import { Tabs } from "./ui/Tabs";

// bindDelay is how long the page waits after an edit before binding.
const bindDelay = 300;
const defaultExample = "authoring/ship";

export function App() {
  const [thema, setThema] = useState<Thema | null>(null);
  const [loaded, setLoaded] = useState(0);
  const [startError, setStartError] = useState<string | null>(null);
  const [session, setSession] = useState<Session>(emptySession);
  const [examples, setExamples] = useState<ExampleInfo[]>([]);
  const [example, setExample] = useState("");
  const [bound, setBound] = useState<BindResponse | null>(null);
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [running, setRunning] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [panel, setPanel] = useState<"diagnostics" | "result" | "normal">("diagnostics");

  // Start thema, then restore a shared session or load the default example.
  useEffect(() => {
    startThema(setLoaded)
      .then(async (t) => {
        setThema(t);
        const list = await t.call("examples", {});
        setExamples(list.examples);
        let shared: Session | null = null;
        try {
          shared = await decodeShare(location.hash);
        } catch {
          setNotice("The link's session could not be read; showing an example instead.");
        }
        if (shared) {
          setSession(shared);
        } else {
          await loadExample(t, defaultExample);
        }
      })
      .catch((e: Error) => setStartError(e.message));
  }, []);

  const loadExample = useCallback(async (t: Thema, id: string) => {
    const ex = await t.call("example", { id });
    if (!ex.ok) {
      setNotice(ex.diagnostics.map((d) => d.headline).join("; "));
      return;
    }
    setExample(id);
    setSession((s) => fromExample(s, ex));
    setOutcome(null);
    setPanel("diagnostics");
  }, []);

  // Bind on every edit to the lineage, once typing pauses. Only the latest
  // bind's answer is kept.
  const lineageKey = JSON.stringify(lineageInput(session));
  const bindSeq = useRef(0);
  useEffect(() => {
    if (!thema) return;
    const seq = ++bindSeq.current;
    const timer = setTimeout(async () => {
      const r = await thema.call("bind", { lineage: lineageInput(session) });
      if (seq === bindSeq.current) setBound(r);
    }, bindDelay);
    return () => clearTimeout(timer);
  }, [thema, lineageKey]);

  const update = (f: (s: Session) => Session) => setSession((s) => f({ ...s }));

  const run = async () => {
    if (!thema) return;
    const r = requestFor(session);
    if (typeof r === "string") {
      setNotice(r);
      return;
    }
    setRunning(true);
    try {
      // The union keeps each op with its own request; the call is typed per op.
      const resp = await (thema.call as (op: string, req: unknown) => Promise<unknown>)(r.op, r.req);
      setOutcome({ op: r.op, resp } as Outcome);
      setPanel("result");
    } finally {
      setRunning(false);
    }
  };

  const switchForm = async () => {
    if (!thema) return;
    if (session.form === "authoring") {
      const r = await thema.call("compile", { name: session.name, files: session.files });
      if (!r.ok) return showFailure("The version files do not compile, so there is no normal form to show.", r.diagnostics);
      update((s) => ({ ...s, form: "normal", normal: { path: "lineage.cue", text: r.normal ?? "" }, path: "" }));
    } else {
      const r = await thema.call("decompile", { normal: session.normal, path: session.path || undefined });
      if (!r.ok) return showFailure("This lineage cannot be written as version files.", r.diagnostics);
      update((s) => ({ ...s, form: "authoring", name: r.name ?? s.name, files: r.files ?? [], activeFile: 0 }));
    }
    setNotice(null);
  };

  const [formFailure, setFormFailure] = useState<{ message: string; diagnostics: Diagnostic[] } | null>(null);
  const showFailure = (message: string, diagnostics: Diagnostic[]) => {
    setFormFailure({ message, diagnostics });
    setPanel("diagnostics");
  };
  useEffect(() => setFormFailure(null), [lineageKey]);

  const formatFile = async () => {
    if (!thema || session.form !== "authoring") return;
    const f = session.files[session.activeFile];
    if (!f) return;
    const r = await thema.call("format", { file: f });
    if (!r.ok) return showFailure(`${f.path} cannot be formatted.`, r.diagnostics);
    update((s) => {
      s.files = s.files.map((x, i) => (i === s.activeFile ? { ...x, text: r.text ?? x.text } : x));
      return s;
    });
  };

  const share = async () => {
    const link = await encodeShare(session);
    history.replaceState(null, "", "#" + link);
    const url = location.href;
    try {
      await navigator.clipboard.writeText(url);
      setNotice(link.length > shareWarnBytes ? `Link copied; at ${Math.round(link.length / 1024)} KB it may be too long for some places.` : "Link copied.");
    } catch {
      setNotice("The link is in the address bar.");
    }
  };

  const bindDiagnostics = bound?.diagnostics ?? [];
  const flagged = useMemo(() => filesOf(bindDiagnostics), [bindDiagnostics]);
  const versions = bound?.versions ?? [];
  const lineageOK = !!bound?.ok;

  // The lineage editor's current file.
  const lin =
    session.form === "authoring"
      ? session.files[session.activeFile] ?? { path: "", text: "" }
      : session.normal;
  const setLinText = (text: string) =>
    update((s) => {
      if (s.form === "authoring") {
        s.files = s.files.map((f, i) => (i === s.activeFile ? { ...f, text } : f));
      } else {
        s.normal = { ...s.normal, text };
      }
      return s;
    });

  const doc = session.data[session.activeData];
  const outcomeDiagnostics = outcome?.resp.diagnostics ?? [];
  const openFile = (file: string) => {
    if (session.form === "authoring") {
      const i = session.files.findIndex((f) => f.path === file);
      if (i >= 0) update((s) => ({ ...s, activeFile: i }));
    }
    const d = session.data.findIndex((x) => x.filename === file);
    if (d >= 0) update((s) => ({ ...s, activeData: d }));
  };

  if (startError) {
    return (
      <main class="start">
        <h1>thema playground</h1>
        <p class="status failed">thema could not start: {startError}</p>
      </main>
    );
  }
  if (!thema) {
    return (
      <main class="start">
        <h1>thema playground</h1>
        <p>Loading thema… {loaded > 0 && `${(loaded / 1e6).toFixed(1)} MB`}</p>
        <progress />
      </main>
    );
  }

  return (
    <div class="app">
      <header>
        <h1>thema playground</h1>
        <label>
          Example{" "}
          <select
            value={example}
            onChange={(e) => {
              const id = (e.target as HTMLSelectElement).value;
              if (id) loadExample(thema, id);
            }}
          >
            <option value="">(your own)</option>
            {groupBy(examples, (e) => e.category).map(([cat, list]) => (
              <optgroup key={cat} label={cat}>
                {list.map((e) => (
                  <option key={e.id} value={e.id} title={e.summary}>
                    {e.title}
                  </option>
                ))}
              </optgroup>
            ))}
          </select>
        </label>
        <button onClick={switchForm} title="Convert the lineage to the other form">
          Show as {session.form === "authoring" ? "normal form" : "version files"}
        </button>
        <button onClick={share}>Share</button>
        <span class="version">
          thema {thema.init.themaVersion} · CUE {thema.init.cueVersion}
        </span>
      </header>
      {notice && (
        <div class="notice" role="status">
          {notice}{" "}
          <button class="link" onClick={() => setNotice(null)}>
            dismiss
          </button>
        </div>
      )}
      <main>
        <section class="lineage" aria-label="Lineage">
          <div class="bar">
            {session.form === "authoring" ? (
              <>
                <label>
                  Lineage{" "}
                  <input value={session.name} size={12} onInput={(e) => update((s) => ({ ...s, name: (e.target as HTMLInputElement).value }))} />
                </label>
                <button onClick={formatFile} title="Lay out this file in the authoring form's layout">
                  Format
                </button>
              </>
            ) : (
              <label>
                Path{" "}
                <input
                  value={session.path}
                  size={12}
                  placeholder="(the file's root)"
                  onInput={(e) => update((s) => ({ ...s, path: (e.target as HTMLInputElement).value }))}
                />
              </label>
            )}
            <span class={`bind-status ${lineageOK ? "ok" : "failed"}`}>
              {bound === null ? "binding…" : lineageOK ? `binds: ${versions.join(", ")}` : `${bindDiagnostics.length} diagnostics`}
            </span>
          </div>
          {session.form === "authoring" ? (
            <Tabs
              label="version files"
              names={session.files.map((f) => f.path)}
              active={session.activeFile}
              flagged={flagged}
              onSelect={(i) => update((s) => ({ ...s, activeFile: i }))}
              onAdd={() =>
                update((s) => {
                  const path = nextVersionFile(s.files.map((f) => f.path));
                  s.files = [...s.files, { path, text: "Schema: {\n}\n" }];
                  s.activeFile = s.files.length - 1;
                  return s;
                })
              }
              onRename={(i, path) => update((s) => ({ ...s, files: s.files.map((f, j) => (j === i ? { ...f, path } : f)) }))}
              onRemove={(i) =>
                update((s) => ({ ...s, files: s.files.filter((_, j) => j !== i), activeFile: Math.max(0, Math.min(s.activeFile, s.files.length - 2)) }))
              }
            />
          ) : (
            <Tabs label="normal form" names={[session.normal.path]} active={0} flagged={flagged} onSelect={() => {}} />
          )}
          <Editor text={lin.text} language="cue" onChange={setLinText} markers={markersFor(bindDiagnostics, lin.path, lin.text)} label={`Lineage file ${lin.path}`} />
        </section>

        <section class="work" aria-label="Data and operations">
          <Tabs
            label="data documents"
            names={session.data.map((d) => d.filename)}
            active={session.activeData}
            flagged={filesOf(outcomeDiagnostics)}
            onSelect={(i) => update((s) => ({ ...s, activeData: i }))}
            onAdd={() =>
              update((s) => {
                s.data = [...s.data, { filename: nextDataFile(s.data.map((d) => d.filename)), text: "{}\n" }];
                s.activeData = s.data.length - 1;
                return s;
              })
            }
            onRename={(i, filename) => update((s) => ({ ...s, data: s.data.map((d, j) => (j === i ? { ...d, filename } : d)) }))}
            onRemove={(i) =>
              update((s) => ({ ...s, data: s.data.filter((_, j) => j !== i), activeData: Math.max(0, Math.min(s.activeData, s.data.length - 2)) }))
            }
          />
          <div class="data-editor">
            {doc ? (
              <Editor
                text={doc.text}
                language={dataLanguage(doc.filename)}
                onChange={(text) => update((s) => ({ ...s, data: s.data.map((d, i) => (i === s.activeData ? { ...d, text } : d)) }))}
                markers={markersFor(outcomeDiagnostics, doc.filename, doc.text)}
                label={`Data document ${doc.filename}`}
              />
            ) : (
              <p class="muted">No data documents. Add one with +.</p>
            )}
          </div>

          <OpBar session={session} versions={versions} update={update} onRun={run} running={running} disabled={!lineageOK} />

          <Tabs
            label="output"
            names={session.form === "authoring" ? ["diagnostics", "result", "normal form"] : ["diagnostics", "result"]}
            active={["diagnostics", "result", "normal"].indexOf(panel)}
            onSelect={(i) => setPanel((["diagnostics", "result", "normal"] as const)[i]!)}
          />
          <div class="panel">
            {panel === "diagnostics" &&
              (formFailure ? (
                <>
                  <p class="status failed">{formFailure.message}</p>
                  <Diagnostics diagnostics={formFailure.diagnostics} onOpen={openFile} />
                </>
              ) : (
                <Diagnostics diagnostics={bindDiagnostics} onOpen={openFile} empty={bound ? "The lineage binds." : "Binding…"} />
              ))}
            {panel === "result" && (outcome ? <Output outcome={outcome} onOpen={openFile} /> : <p class="muted">Run an operation to see its result.</p>)}
            {panel === "normal" && (
              <div class="result">
                <Editor text={bound?.normal ?? ""} language="cue" readOnly label="The lineage in the normal form" />
              </div>
            )}
          </div>
        </section>
      </main>
    </div>
  );
}

interface OpBarProps {
  session: Session;
  versions: string[];
  update: (f: (s: Session) => Session) => void;
  onRun: () => void;
  running: boolean;
  disabled: boolean;
}

function OpBar({ session, versions, update, onRun, running, disabled }: OpBarProps) {
  const setArg = (k: keyof Session["args"], v: string) => update((s) => ({ ...s, args: { ...s.args, [k]: v } }));
  const versionSelect = (k: "version" | "from" | "to", none: string) => (
    <select value={session.args[k]} onChange={(e) => setArg(k, (e.target as HTMLSelectElement).value)} aria-label={k}>
      <option value="">{none}</option>
      {versions.map((v) => (
        <option key={v} value={v}>
          {v}
        </option>
      ))}
    </select>
  );
  const args = argsOf(session.op);
  return (
    <div class="bar ops">
      <select value={session.op} onChange={(e) => update((s) => ({ ...s, op: (e.target as HTMLSelectElement).value as UserOp }))} aria-label="operation">
        <optgroup label="Data">
          {dataOps.map((o) => (
            <option key={o} value={o}>
              {o}
            </option>
          ))}
        </optgroup>
        <optgroup label="Code">
          {codegenOps.map((o) => (
            <option key={o} value={o}>
              {o}
            </option>
          ))}
        </optgroup>
      </select>
      {args.includes("version") && (
        <label>schema {versionSelect("version", isCodegen(session.op) || session.op === "validate" ? "latest" : "any that accepts it")}</label>
      )}
      {args.includes("from") && <label>from {versionSelect("from", "the data's")}</label>}
      {args.includes("to") && <label>to {versionSelect("to", "choose")}</label>}
      {args.includes("format") && (
        <label>
          as{" "}
          <select value={session.args.format} onChange={(e) => setArg("format", (e.target as HTMLSelectElement).value)} aria-label="format">
            <option value="">{session.op === "openapi" ? "YAML" : "JSON"}</option>
            <option value="json">JSON</option>
            <option value="yaml">YAML</option>
          </select>
        </label>
      )}
      <button class="primary" onClick={onRun} disabled={disabled || running} title={disabled ? "The lineage must bind first" : undefined}>
        {running ? "Running…" : "Run"}
      </button>
    </div>
  );
}

// fromExample is s with the example's lineage and data in place.
function fromExample(s: Session, ex: GetExampleResponse): Session {
  const l = ex.lineage;
  const next: Session = { ...s, data: ex.data.length > 0 ? ex.data : [{ filename: "data.json", text: "{}\n" }], activeData: 0, activeFile: 0 };
  if (l.files && l.files.length > 0) {
    // The newest version file first: it is the one an author edits.
    const files = [...l.files].sort((a, b) => (a.path.startsWith("history/") ? 1 : 0) - (b.path.startsWith("history/") ? 1 : 0));
    return { ...next, form: "authoring", name: l.name ?? "lineage", files };
  }
  return { ...next, form: "normal", normal: l.normal ?? s.normal, path: l.path ?? "" };
}

function dataLanguage(filename: string): Language {
  return /\.ya?ml$/.test(filename) ? "yaml" : "json";
}

// nextVersionFile names a new version file after the latest one: the next
// minor version.
function nextVersionFile(paths: string[]): string {
  let major = 0;
  let minor = -1;
  for (const p of paths) {
    const m = /(\d+)\.(\d+)\.thema$/.exec(p);
    if (!m) continue;
    const [a, b] = [Number(m[1]), Number(m[2])];
    if (a > major || (a === major && b > minor)) [major, minor] = [a, b];
  }
  return `${major}.${minor + 1}.thema`;
}

function nextDataFile(names: string[]): string {
  for (let i = names.length + 1; ; i++) {
    const n = `data${i}.json`;
    if (!names.includes(n)) return n;
  }
}

function groupBy<T>(xs: T[], key: (x: T) => string): [string, T[]][] {
  const m = new Map<string, T[]>();
  for (const x of xs) {
    const k = key(x);
    m.set(k, [...(m.get(k) ?? []), x]);
  }
  return [...m.entries()];
}
