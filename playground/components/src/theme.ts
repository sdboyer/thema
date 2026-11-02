// The editors' look. Colors come from the page's stylesheet, as CSS
// variables with a light and a dark value each, so the editors follow the
// system's color scheme with the rest of the page. CodeMirror's own
// light/dark rules (caret, search panel, bracket matching) key off whether
// the editor theme says it is dark, so the editor also needs telling.
import { HighlightStyle } from "@codemirror/language";
import { EditorView } from "@codemirror/view";
import { tags as t } from "@lezer/highlight";

// The token colors, each a --syn-* variable in style.css. A test holds every
// one to readable contrast against the editor's background in both schemes.
export const tokenColors = {
  keyword: [t.keyword, t.controlKeyword, t.moduleKeyword, t.operatorKeyword],
  string: [t.string, t.special(t.string), t.regexp, t.escape],
  literal: [t.number, t.bool, t.null, t.atom],
  type: [t.typeName, t.standard(t.typeName)],
  definition: [t.className, t.definition(t.typeName)],
  property: [t.propertyName, t.definition(t.propertyName)],
  comment: [t.comment, t.lineComment, t.blockComment],
  meta: [t.meta, t.annotation],
  punctuation: [t.punctuation, t.bracket, t.separator, t.operator],
} as const;

// One rule per tag: a later rule naming the same tag replaces an earlier one
// rather than adding to it, so a comment's italics ride with its color.
export const highlight = HighlightStyle.define([
  ...Object.entries(tokenColors).map(([name, tag]) => ({
    tag: [...tag],
    color: `var(--syn-${name})`,
    ...(name === "comment" ? { fontStyle: "italic" } : {}),
  })),
  { tag: t.invalid, color: "var(--error)" },
]);

const spec = {
  "&": { height: "100%", fontSize: "var(--code-size)", color: "var(--text)", backgroundColor: "var(--bg)" },
  ".cm-scroller": { fontFamily: "var(--mono)", lineHeight: "1.5" },
  ".cm-content": { caretColor: "var(--text)" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--text)" },
  ".cm-gutters": { background: "var(--panel)", color: "var(--muted)", border: "none" },
  ".cm-activeLine": { background: "var(--active-line)" },
  ".cm-activeLineGutter": { background: "var(--active-line)", color: "var(--text)" },
  ".cm-selectionBackground, &.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground": {
    background: "var(--selection)",
  },
  ".cm-tooltip": { background: "var(--panel)", color: "var(--text)", border: "1px solid var(--border)" },
  ".cm-tooltip-lint": { fontFamily: "var(--mono)", fontSize: "var(--code-size)" },
  ".cm-diagnostic": { whiteSpace: "pre-wrap" },
  ".cm-diagnosticSource": { color: "var(--muted)" },
  "&.cm-focused": { outline: "none" },
};

export const lightTheme = EditorView.theme(spec, { dark: false });
export const darkTheme = EditorView.theme(spec, { dark: true });

// darkScheme is the system's color-scheme preference, or null where there is
// no way to ask (jsdom).
export function darkScheme(): MediaQueryList | null {
  return typeof matchMedia === "function" ? matchMedia("(prefers-color-scheme: dark)") : null;
}
