// thema positions are CUE's: a 1-based line, and a 1-based column counting
// bytes of UTF-8. An editor counts UTF-16 code units from the document's
// start.
import type { Pos } from "../api/types";

// offsetOf is pos as an offset into text, or null when text has no such
// line. A column past the line's end is clamped to it.
export function offsetOf(text: string, pos: Pick<Pos, "line" | "col">): number | null {
  if (pos.line < 1) return null;
  let start = 0;
  for (let l = 1; l < pos.line; l++) {
    const nl = text.indexOf("\n", start);
    if (nl < 0) return null;
    start = nl + 1;
  }
  let end = text.indexOf("\n", start);
  if (end < 0) end = text.length;
  const line = text.slice(start, end);
  return start + charIndexOfByte(line, Math.max(0, pos.col - 1));
}

// charIndexOfByte is the index of the character that starts at byte b of
// s's UTF-8 encoding.
export function charIndexOfByte(s: string, b: number): number {
  let bytes = 0;
  let i = 0;
  while (i < s.length && bytes < b) {
    const cp = s.codePointAt(i)!;
    bytes += cp < 0x80 ? 1 : cp < 0x800 ? 2 : cp < 0x10000 ? 3 : 4;
    i += cp > 0xffff ? 2 : 1;
  }
  return i;
}

// spanAt is the range to underline for a position: the word or quoted string
// that starts there, or one character.
export function spanAt(text: string, from: number): { from: number; to: number } {
  if (from >= text.length) return { from: Math.max(0, text.length - 1), to: text.length };
  const rest = text.slice(from);
  const m = /^("(?:[^"\\\n]|\\.)*"|#?[\w$.]+)/.exec(rest);
  const len = m ? m[0].length : 1;
  return { from, to: from + Math.max(1, len) };
}
