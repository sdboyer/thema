// A CodeMirror editor for one text. The text is the parent's; the editor
// reports edits and shows markers.
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { json } from "@codemirror/lang-json";
import { bracketMatching, defaultHighlightStyle, indentOnInput, syntaxHighlighting } from "@codemirror/language";
import { lintGutter, setDiagnostics } from "@codemirror/lint";
import { highlightSelectionMatches, searchKeymap } from "@codemirror/search";
import { Compartment, EditorState } from "@codemirror/state";
import { EditorView, drawSelection, highlightActiveLine, keymap, lineNumbers } from "@codemirror/view";
import { useEffect, useRef } from "preact/hooks";
import { cue } from "./cue-lang";
import type { Marker } from "./markers";

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
  cue: () => [cue],
  json: () => [json()],
  yaml: () => [],
  text: () => [],
};

const theme = EditorView.theme({
  "&": { height: "100%", fontSize: "13px" },
  ".cm-scroller": { fontFamily: "var(--mono)", lineHeight: "1.5" },
  ".cm-gutters": { background: "var(--panel)", color: "var(--muted)", border: "none" },
  ".cm-activeLine": { background: "var(--active-line)" },
  "&.cm-focused": { outline: "none" },
});

export function Editor({ text, language, onChange, markers, readOnly, label }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const lang = useRef(new Compartment());
  const ro = useRef(new Compartment());

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
          syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
          lintGutter(),
          keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap, indentWithTab]),
          lang.current.of(languages[language]()),
          ro.current.of(EditorState.readOnly.of(!!readOnly)),
          EditorView.contentAttributes.of({ "aria-label": label }),
          theme,
          EditorView.updateListener.of((u) => {
            if (u.docChanged) onChangeRef.current?.(u.state.doc.toString());
          }),
        ],
      }),
    });
    view.current = v;
    return () => v.destroy();
  }, []);

  // A new text from outside (another file opened, an example loaded)
  // replaces the document; an edit from this editor is already in it.
  useEffect(() => {
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
      .map((m) => ({ from: m.from, to: Math.min(m.to, len), severity: m.severity, message: m.message, source: m.code }));
    v.dispatch(setDiagnostics(v.state, ds));
  }, [markers, text]);

  return <div class="editor" ref={host} />;
}
