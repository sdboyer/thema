// The two kinds of file in a lineage's directory: tip.thema, the one an
// author edits, and the versions under history/, which are cut and never
// change. And how the tip's version reads wherever versions are shown.
import type { File, TipInfo } from "../api/types";

export const tipFile = "tip.thema";
export const historyDir = "history/";

export const isHistory = (path: string) => path.startsWith(historyDir);
export const isTip = (path: string) => path === tipFile;

// byKind orders files the tip first, then the rest, then history by version.
export function byKind(files: readonly File[]): File[] {
  const rank = (p: string) => (isTip(p) ? 0 : isHistory(p) ? 2 : 1);
  return [...files].sort((a, b) => rank(a.path) - rank(b.path) || compareVersions(a.path, b.path));
}

function compareVersions(a: string, b: string): number {
  const v = (p: string) => (/(\d+)\.(\d+)\.thema$/.exec(p) ?? []).slice(1).map(Number);
  const [x, y] = [v(a), v(b)];
  return (x[0] ?? 0) - (y[0] ?? 0) || (x[1] ?? 0) - (y[1] ?? 0) || a.localeCompare(b);
}

// versionLabel is how version v reads: the tip's label for the provisional
// version the tip binds as, v itself for every version cut.
export function versionLabel(v: string, tip?: TipInfo | null): string {
  return tip?.provisional && v === tip.version ? tip.label : v;
}

// bindSummary says what a lineage binds: its versions, with the tip's label
// for a provisional tip, or after them for a clean one.
export function bindSummary(versions: readonly string[], tip?: TipInfo | null): string {
  const s = `binds: ${versions.map((v) => versionLabel(v, tip)).join(", ")}`;
  return tip && !tip.provisional ? `${s} · ${tip.label}` : s;
}
