// A CodeMirror editor for one text. The text is the parent's; the editor
// reports edits and shows markers.
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { json } from "@codemirror/lang-json";
import { bracketMatching, indentOnInput, indentUnit, syntaxHighlighting } from "@codemirror/language";
import { lintGutter, setDiagnostics } from "@codemirror/lint";
import { highlightSelectionMatches, searchKeymap } from "@codemirror/search";
import { Compartment, EditorState } from "@codemirror/state";
import { EditorView, drawSelection, highlightActiveLine, keymap, lineNumbers } from "@codemirror/view";
import { useEffect, useLayoutEffect, useRef } from "preact/hooks";
import { cue } from "./cue-lang";
import type { Marker } from "./markers";
import { darkScheme, darkTheme, highlight, lightTheme } from "./theme";

export type Language = "cue" | "json" | "yaml" | "text";

interface Props {
  text: string;
  language: Language;
  onChange?: (text: string) => void;
  markers?: Marker[];
  readOnly?: boolean;
  label: string;
}

const languages = {
  // CUE's formatter indents with tabs, and so does every file it formats.
  cue: () => [cue, indentUnit.of("\t")],
  json: () => [json()],
  yaml: () => [],
  text: () => [],
};

export function Editor({ text, language, onChange, markers, readOnly, label }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const lang = useRef(new Compartment());
  const ro = useRef(new Compartment());
  const scheme = useRef(new Compartment());

  useEffect(() => {
    const v = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: text,
        extensions: [
          lineNumbers(),
          history(),
          drawSelection(),
          indentOnInput(),
          bracketMatching(),
          highlightActiveLine(),
          highlightSelectionMatches(),
          syntaxHighlighting(highlight),
          lintGutter(),
          keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap, indentWithTab]),
          lang.current.of(languages[language]()),
          ro.current.of(EditorState.readOnly.of(!!readOnly)),
          EditorView.contentAttributes.of({ "aria-label": label }),
          scheme.current.of(darkScheme()?.matches ? darkTheme : lightTheme),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) onChangeRef.current?.(u.state.doc.toString());
          }),
        ],
      }),
    });
    view.current = v;
    // Follow the system when it switches between light and dark.
    const mq = darkScheme();
    const follow = (e: MediaQueryListEvent) => v.dispatch({ effects: scheme.current.reconfigure(e.matches ? darkTheme : lightTheme) });
    mq?.addEventListener("change", follow);
    return () => {
      mq?.removeEventListener("change", follow);
      v.destroy();
    };
  }, []);

  // A new text from outside (another file opened, an example loaded)
  // replaces the document; an edit from this editor is already in it. This
  // must be a layout effect: a plain effect waits for a frame, and a keystroke
  // arriving before it would leave text one edit behind the document, so the
  // effect would put the old text back, report it as an edit, and the parent
  // and the editor would trade the two texts forever.
  useLayoutEffect(() => {
    const v = view.current;
    if (v && v.state.doc.toString() !== text) {
      v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: text } });
    }
  }, [text]);

  useEffect(() => {
    view.current?.dispatch({ effects: lang.current.reconfigure(languages[language]()) });
  }, [language]);

  useEffect(() => {
    view.current?.dispatch({ effects: ro.current.reconfigure(EditorState.readOnly.of(!!readOnly)) });
  }, [readOnly]);

  useEffect(() => {
    const v = view.current;
    if (!v) return;
    const len = v.state.doc.length;
    const ds = (markers ?? [])
      .filter((m) => m.from <= len)
      .map((m) => ({ from: m.from, to: Math.min(m.to, len), severity: m.severity, message: m.message, renderMessage: () => markerDOM(m) }));
    v.dispatch(setDiagnostics(v.state, ds));
  }, [markers, text]);

  return <div class="editor" ref={host} />;
}

// markerDOM lays a marker out as the diagnostics list lays out a diagnostic,
// with the same class names, so that the host styles the two alike: the code
// and the headline, then each note and help on its own line under its label,
// a note this file cannot mark followed by where it is.
export function markerDOM(m: Pick<Marker, "code" | "lines">): HTMLElement {
  const el = (cls: string, ...kids: (string | Node)[]) => {
    const e = document.createElement(cls === "code" || cls === "label" || cls === "where" ? "span" : "div");
    e.className = cls;
    e.append(...kids);
    return e;
  };
  const root = document.createElement("div");
  for (const l of m.lines) {
    if (l.kind === "headline") root.append(el("headline", el("code", m.code), " ", l.text));
    else if (l.kind === "of") root.append(el("of", `of ${m.code}: ${l.text}`));
    else if (l.where) root.append(el(l.kind, el("label", `${l.kind}:`), " ", l.text, " ", el("where", `(${l.where})`)));
    else root.append(el(l.kind, el("label", `${l.kind}:`), " ", l.text));
  }
  return root;
}
