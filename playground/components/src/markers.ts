// Diagnostics as editor markers: each diagnostic's primary position marks the
// file it is in with the diagnostic's severity, and each note positioned
// elsewhere in the file marks it as information. Positions in thema's own CUE
// mark nothing; the diagnostics list shows them.
//
// A marker says what the diagnostics list says, in the same order: the
// headline, then the notes, then the help, each on its own line, so that the
// problem and what to do about it are each in one place. A note positioned
// where the primary is says why in the primary's marker rather than in one of
// its own.
import type { Diagnostic, Pos } from "./types";
import { themaDoc } from "./docs";
import { offsetOf, spanAt } from "./positions";

export interface Marker {
  from: number;
  to: number;
  severity: "error" | "warning" | "info";
  // message is the marker as text, one line per entry of lines: "E0360:
  // <headline>", "note: <label>", "help: <help>", or, for a note marked on its
  // own, "of E0360: <headline>".
  message: string;
  code: string;
  // lines are the marker's parts, for an editor that lays them out.
  lines: MarkerLine[];
}

// A MarkerLine is one part of a marker: its headline, a note, a help, or, for
// a note marked on its own, the diagnostic it belongs to.
export interface MarkerLine {
  kind: "headline" | "note" | "help" | "of";
  text: string;
}

function textOf(code: string, lines: readonly MarkerLine[]): string {
  return lines
    .map((l) => (l.kind === "headline" ? `${code}: ${l.text}` : l.kind === "of" ? `of ${code}: ${l.text}` : `${l.kind}: ${l.text}`))
    .join("\n");
}

export function markersFor(ds: readonly Diagnostic[], file: string, text: string): Marker[] {
  const out: Marker[] = [];
  const at = (p: Pos | undefined) => {
    if (!p || p.file !== file || p.origin === "thema") return null;
    const off = offsetOf(text, p);
    return off === null ? null : spanAt(text, off);
  };
  for (const d of ds) {
    const primary = at(d.primary);
    const here: MarkerLine[] = [];
    const loose: MarkerLine[] = [];
    const elsewhere: Marker[] = [];
    for (const n of d.notes ?? []) {
      const span = at(n.pos);
      if (!n.pos) {
        loose.push({ kind: "note", text: n.label });
      } else if (span && primary && span.from === primary.from && span.to === primary.to) {
        here.push({ kind: "note", text: n.label });
      } else if (span) {
        const lines: MarkerLine[] = [
          { kind: "note", text: n.label },
          { kind: "of", text: d.headline },
        ];
        elsewhere.push({ ...span, severity: "info", message: textOf(d.code, lines), code: d.code, lines });
      }
    }
    if (primary) {
      const lines: MarkerLine[] = [
        { kind: "headline", text: d.headline },
        ...here,
        ...loose,
        ...(d.help ?? []).map((h): MarkerLine => ({ kind: "help", text: h })),
      ];
      out.push({
        ...primary,
        severity: d.severity === "warning" ? "warning" : d.severity === "note" ? "info" : "error",
        message: textOf(d.code, lines),
        code: d.code,
        lines,
      });
    }
    out.push(...elsewhere);
  }
  return out;
}

// docsURL is where a code is explained, in the docs of the thema the page
// runs.
export function docsURL(code: string): string {
  return themaDoc(`errors/${encodeURIComponent(code)}.md`);
}

// filesOf lists the files diagnostics point into, so a file tab can say it
// has problems without being open.
export function filesOf(ds: readonly Diagnostic[]): Set<string> {
  const s = new Set<string>();
  for (const d of ds) {
    if (d.primary && d.primary.origin !== "thema") s.add(d.primary.file);
  }
  return s;
}
