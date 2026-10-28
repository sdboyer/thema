// Diagnostics as editor markers: each diagnostic's primary position marks the
// file it is in with the diagnostic's severity, and each note positioned in a
// file marks it as information. Positions in thema's own CUE mark nothing;
// the diagnostics list shows them.
import type { Diagnostic, Pos } from "../api/types";
import { themaDoc } from "./docs";
import { offsetOf, spanAt } from "./positions";

export interface Marker {
  from: number;
  to: number;
  severity: "error" | "warning" | "info";
  message: string;
  code: string;
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
    if (primary) {
      const extra = [...(d.help ?? []).map((h) => `help: ${h}`)];
      out.push({
        ...primary,
        severity: d.severity === "warning" ? "warning" : d.severity === "note" ? "info" : "error",
        message: [`${d.code}: ${d.headline}`, ...extra].join("\n"),
        code: d.code,
      });
    }
    for (const n of d.notes ?? []) {
      const span = at(n.pos);
      if (span) {
        out.push({ ...span, severity: "info", message: `${d.code}: ${n.label}`, code: d.code });
      }
    }
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
