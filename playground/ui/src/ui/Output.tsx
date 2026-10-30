// What an operation returned: its result in its own terms, then its
// diagnostics.
import type {
  CodegenResponse,
  HydrateResponse,
  TranslateResponse,
  ValidateAnyResponse,
  ValidateResponse,
} from "../api/types";
import { Diagnostics } from "./Diagnostics";
import { DocLink, lacunaTypeDoc } from "./docs";
import { Editor } from "./Editor";

export type Outcome =
  | { op: "validate"; resp: ValidateResponse }
  | { op: "validate-any"; resp: ValidateAnyResponse }
  | { op: "translate"; resp: TranslateResponse }
  | { op: "hydrate" | "dehydrate"; resp: HydrateResponse }
  | { op: "openapi" | "jsonschema" | "typescript"; resp: CodegenResponse };

interface Props {
  outcome: Outcome;
  onOpen: (file: string) => void;
  // label is how a version reads: the tip's label for the tip's version.
  label?: (version: string) => string;
}

type Label = (version: string) => string;
const plain: Label = (v) => v;

export function Output({ outcome, onOpen, label = plain }: Props) {
  const { resp } = outcome;
  return (
    <div class="output">
      <Summary outcome={outcome} label={label} />
      <Body outcome={outcome} label={label} />
      {(resp.diagnostics.length > 0 || outcome.op !== "validate-any") && (
        <Diagnostics diagnostics={resp.diagnostics} onOpen={onOpen} empty="" />
      )}
    </div>
  );
}

function Summary({ outcome, label }: { outcome: Outcome; label: Label }) {
  const { op, resp } = outcome;
  if (!resp.ok) return <p class="status failed">{op} failed</p>;
  switch (op) {
    case "validate":
      return <p class="status ok">The data is an instance of schema {label(outcome.resp.version ?? "")}.</p>;
    case "validate-any":
      return <p class="status ok">The newest schema the data is an instance of is {label(outcome.resp.version ?? "")}.</p>;
    case "translate":
      return (
        <p class="status ok">
          Translated from {label(outcome.resp.from ?? "")} to {label(outcome.resp.to ?? "")}
          {lacunaCount(outcome.resp) === 0 ? ", with no lacunas" : lacunaCount(outcome.resp) === 1 ? ", with 1 lacuna" : `, with ${lacunaCount(outcome.resp)} lacunas`}.
        </p>
      );
    case "hydrate":
    case "dehydrate":
      return (
        <p class="status ok">
          {outcome.resp.changed ? `${capitalize(op)}d` : "Unchanged"}, against schema {label(outcome.resp.version ?? "")}.
        </p>
      );
    default:
      return <p class="status ok">Generated from schema {label(outcome.resp.version ?? "")}.</p>;
  }
}

function Body({ outcome, label }: { outcome: Outcome; label: Label }) {
  switch (outcome.op) {
    case "validate-any":
      return (
        <div class="per-version">
          {outcome.resp.perVersion.map((v) => (
            <details key={v.version} open={!outcome.resp.ok}>
              <summary>
                schema {label(v.version)}: {v.diagnostics.length === 0 ? "accepts the data" : `${v.diagnostics.length} diagnostics`}
              </summary>
              <Diagnostics diagnostics={v.diagnostics} empty="Accepts the data." />
            </details>
          ))}
        </div>
      );
    case "translate": {
      const r = outcome.resp;
      if (!r.ok) return null;
      return (
        <>
          <Result text={JSON.stringify(r.output, null, 2)} language="json" label="Translated instance" />
          <Lacunas resp={r} label={label} />
        </>
      );
    }
    case "hydrate":
    case "dehydrate":
      return outcome.resp.ok ? <Result text={JSON.stringify(outcome.resp.output, null, 2)} language="json" label="Result" /> : null;
    case "openapi":
    case "jsonschema":
    case "typescript":
      return outcome.resp.ok ? (
        <Result text={outcome.resp.output ?? ""} language={outcome.op === "typescript" ? "text" : "json"} label="Generated code" />
      ) : null;
    default:
      return null;
  }
}

function Result({ text, language, label }: { text: string; language: "json" | "text"; label: string }) {
  return (
    <div class="result">
      <Editor text={text} language={language} readOnly label={label} />
    </div>
  );
}

function Lacunas({ resp, label }: { resp: TranslateResponse; label: Label }) {
  const steps = (resp.lacunas ?? []).filter((s) => s.lacunas.length > 0);
  if (steps.length === 0) return null;
  return (
    <table class="lacunas">
      <caption>
        Lacunas <DocLink topic="lacunas" />
      </caption>
      <thead>
        <tr>
          <th>step</th>
          <th>type</th>
          <th>fields</th>
          <th>message</th>
        </tr>
      </thead>
      <tbody>
        {steps.flatMap((s) =>
          s.lacunas.map((l, i) => (
            <tr key={`${s.from}-${s.to}-${i}`}>
              <td>
                {label(s.from.join("."))} → {label(s.to.join("."))}
              </td>
              <td>
                {l.type}
                <DocLink doc={lacunaTypeDoc(l.type)} />
              </td>
              <td>{[...(l.sourceFields ?? []), ...(l.targetFields ?? [])].map((f) => f.path).join(", ")}</td>
              <td>{l.message}</td>
            </tr>
          )),
        )}
      </tbody>
    </table>
  );
}

function lacunaCount(r: TranslateResponse): number {
  return (r.lacunas ?? []).reduce((n, s) => n + s.lacunas.length, 0);
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}
