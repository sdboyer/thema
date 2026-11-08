// The diagnostics of one operation, as thema says them and in the order the
// text renderer says them: code and headline, where, the notes with a
// position and then those without, help, and CUE's own text when there is
// any. Each is on its own line under its own label, so that what is wrong and
// what to do are each in one place.
import type { Diagnostic, Pos } from "./types";
import { docsURL, whereText } from "./markers";

interface Props {
  diagnostics: readonly Diagnostic[];
  // onOpen opens a file of the session, by the name a position gives it.
  onOpen?: (file: string) => void;
  empty?: string;
}

const samePos = (a: Pos, b: Pos | undefined) => !!b && a.file === b.file && a.line === b.line && a.col === b.col;

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
          {[...(d.notes ?? []).filter((n) => n.pos), ...(d.notes ?? []).filter((n) => !n.pos)].map((n, j) => (
            <div key={j} class="note">
              <span class="label">note:</span> {n.label} {n.pos && !samePos(n.pos, d.primary) && <Where pos={n.pos} onOpen={onOpen} inline />}
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
  const body =
    pos.origin === "thema" || !onOpen ? (
      <span>{whereText(pos)}</span>
    ) : (
      <button class="link" onClick={() => onOpen(pos.file)}>
        {whereText(pos)}
      </button>
    );
  return inline ? <span class="where">({body})</span> : <div class="where">--&gt; {body}</div>;
}
