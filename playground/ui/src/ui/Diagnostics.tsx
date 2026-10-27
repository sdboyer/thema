// The diagnostics of one operation, as thema says them: code and headline,
// where, the notes, help, and CUE's own text when there is any.
import type { Diagnostic, Pos } from "../api/types";
import { docsURL } from "./markers";

interface Props {
  diagnostics: readonly Diagnostic[];
  // onOpen opens a file of the session, by the name a position gives it.
  onOpen?: (file: string) => void;
  empty?: string;
}

export function Diagnostics({ diagnostics, onOpen, empty }: Props) {
  if (diagnostics.length === 0) {
    return <p class="muted">{empty ?? "Nothing to report."}</p>;
  }
  return (
    <ul class="diagnostics">
      {diagnostics.map((d, i) => (
        <li key={i} class={`diagnostic ${d.severity}`}>
          <div class="headline">
            <a class="code" href={docsURL(d.code)} target="_blank" rel="noreferrer" title="What this code means">
              {d.severity}[{d.code}]
            </a>{" "}
            {d.headline}
          </div>
          {d.primary && <Where pos={d.primary} onOpen={onOpen} />}
          {(d.notes ?? []).map((n, j) => (
            <div key={j} class="note">
              <span class="label">note:</span> {n.label} {n.pos && <Where pos={n.pos} onOpen={onOpen} inline />}
            </div>
          ))}
          {(d.help ?? []).map((h, j) => (
            <div key={j} class="help">
              <span class="label">help:</span> {h}
            </div>
          ))}
          {d.detail && (
            <details class="detail">
              <summary>CUE says</summary>
              <pre>{d.detail}</pre>
            </details>
          )}
        </li>
      ))}
    </ul>
  );
}

function Where({ pos, onOpen, inline }: { pos: Pos; onOpen?: (file: string) => void; inline?: boolean }) {
  const text = `${pos.file}:${pos.line}:${pos.col}`;
  const own = pos.origin === "thema";
  const body =
    own || !onOpen ? (
      <span>{own ? `thema's own CUE, ${text}` : text}</span>
    ) : (
      <button class="link" onClick={() => onOpen(pos.file)}>
        {text}
      </button>
    );
  return inline ? <span class="where">({body})</span> : <div class="where">--&gt; {body}</div>;
}
