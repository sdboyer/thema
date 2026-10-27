import { EditorView } from "@codemirror/view";
import { render } from "preact";
import { useState } from "preact/hooks";
import { act } from "preact/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Editor } from "./Editor";

// Typing faster than effects run after paint: each keystroke re-renders the
// parent with the new text before the previous render's effects have run.
// The editor must neither lose keystrokes nor fight its parent over the text.
describe("Editor", () => {
  it("keeps up with keystrokes that arrive before effects run", async () => {
    let changes = 0;
    let text = "";
    function Host() {
      const [t, setT] = useState("a: 1\n");
      text = t;
      return (
        <Editor
          text={t}
          language="cue"
          label="test"
          onChange={(next) => {
            // A runaway exchange would never end; stop feeding it.
            if (++changes < 200) setT(next);
          }}
        />
      );
    }
    const root = document.createElement("div");
    document.body.append(root);
    await act(async () => {
      render(<Host />, root);
    });
    const view = EditorView.findFromDOM(root.querySelector<HTMLElement>(".cm-editor")!)!;
    view.dispatch({ selection: { anchor: view.state.doc.length } });

    const typed = "bogus: 1";
    for (const ch of typed) {
      view.dispatch(view.state.replaceSelection(ch));
      // Lets the parent re-render, as it does between two key events, but
      // not run effects, which wait for a frame.
      await Promise.resolve();
      await Promise.resolve();
    }
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });

    expect(view.state.doc.toString()).toBe("a: 1\n" + typed);
    expect(text).toBe("a: 1\n" + typed);
    expect(changes).toBe(typed.length);
    render(null, root);
  });

  it("takes a new text from its parent", async () => {
    let set: (t: string) => void = () => {};
    function Host() {
      const [t, setT] = useState("one");
      set = setT;
      return <Editor text={t} language="text" label="test" onChange={setT} />;
    }
    const root = document.createElement("div");
    document.body.append(root);
    await act(async () => {
      render(<Host />, root);
    });
    const view = EditorView.findFromDOM(root.querySelector<HTMLElement>(".cm-editor")!)!;
    await act(async () => set("two"));
    expect(view.state.doc.toString()).toBe("two");
    render(null, root);
  });

  describe("color scheme", () => {
    // A stand-in for the system's preference that tests can flip.
    function fakeScheme(dark: boolean) {
      const listeners = new Set<(e: MediaQueryListEvent) => void>();
      const mq = {
        matches: dark,
        addEventListener: (_: string, f: (e: MediaQueryListEvent) => void) => listeners.add(f),
        removeEventListener: (_: string, f: (e: MediaQueryListEvent) => void) => listeners.delete(f),
      };
      vi.stubGlobal("matchMedia", () => mq);
      return {
        flip(to: boolean) {
          mq.matches = to;
          for (const f of listeners) f({ matches: to } as MediaQueryListEvent);
        },
        listeners,
      };
    }
    afterEach(() => vi.unstubAllGlobals());

    async function mount() {
      const root = document.createElement("div");
      document.body.append(root);
      await act(async () => {
        render(<Editor text="a: 1" language="cue" label="test" />, root);
      });
      const view = EditorView.findFromDOM(root.querySelector<HTMLElement>(".cm-editor")!)!;
      return { root, view };
    }

    it("starts in the system's scheme and follows it when it changes", async () => {
      const sys = fakeScheme(true);
      const { root, view } = await mount();
      expect(view.state.facet(EditorView.darkTheme)).toBe(true);
      sys.flip(false);
      expect(view.state.facet(EditorView.darkTheme)).toBe(false);
      sys.flip(true);
      expect(view.state.facet(EditorView.darkTheme)).toBe(true);
      render(null, root);
      expect(sys.listeners.size).toBe(0);
    });

    it("is light where the scheme cannot be asked", async () => {
      const { root, view } = await mount();
      expect(view.state.facet(EditorView.darkTheme)).toBe(false);
      render(null, root);
    });
  });
});
