// The page against the real thema: start, load the default example, edit it.
// Needs go generate ./cmd/thema/playground; skipped without it.
import { cursorLineEnd, insertNewlineAndIndent } from "@codemirror/commands";
import { EditorView } from "@codemirror/view";
import { render } from "preact";
import { act } from "preact/test-utils";
import { describe, expect, it, vi } from "vitest";
import type { RegisterExamplesRequest } from "./api/types";
import { haveThema, realThema } from "./test/realThema";

// startThema runs thema in-process; the import waits for the call, since
// realThema itself imports this module. hostManifest is the examples.json the
// host serves, or null for none.
let hostManifest: RegisterExamplesRequest | null = null;
vi.mock("./api/client", async (orig) => {
  const mod = await orig<typeof import("./api/client")>();
  return {
    ...mod,
    startThema: async () => (await import("./test/realThema")).realThema(),
    fetchHostExamples: async () => hostManifest,
  };
});

async function waitFor(f: () => boolean, ms = 20000) {
  const t = Date.now();
  while (!f()) {
    if (Date.now() - t > ms) throw new Error("timed out");
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
  }
}

describe.skipIf(!haveThema())("App with thema", () => {
  it("loads the default example, binds it, and rebinds an edit to the tip typed in a burst", async () => {
    await realThema();
    const { App } = await import("./app");
    const root = document.createElement("div");
    document.body.append(root);
    await act(async () => {
      render(<App />, root);
    });
    await waitFor(() => root.querySelector(".bind-status")?.textContent?.startsWith("binds") ?? false);
    expect(root.querySelector(".bind-status")!.textContent).toBe("binds: 0.0, 1.0 · tip (clean at 1.0)");

    const view = EditorView.findFromDOM(root.querySelector<HTMLElement>(".lineage .cm-editor")!)!;
    expect(view.state.readOnly).toBe(false);
    const at = view.state.doc.toString().indexOf("secondfield: int") + "secondfield: int".length;
    // As a user would: End and Enter through the keymap's commands, then
    // type faster than the page's effects run, which wait for a frame.
    await act(async () => {
      view.dispatch({ selection: { anchor: at - 5 } });
      cursorLineEnd(view);
      insertNewlineAndIndent(view);
    });
    for (const ch of "bogus: int") {
      view.dispatch(view.state.replaceSelection(ch));
      await Promise.resolve();
      await Promise.resolve();
    }
    // A new required field: the tip is a major version, with no ForwardLens.
    await waitFor(() => root.querySelector(".bind-status")?.textContent?.includes("cuts as 2.0") ?? false);
    expect(view.state.doc.toString()).toContain("secondfield: int\n\tbogus: int");
    expect(root.querySelector(".bind-status")!.textContent).toBe("binds: 0.0, 1.0, tip (cuts as 2.0)");
    const panel = root.querySelector(".panel")!.textContent!;
    expect(panel).toContain("E1001");
    expect(panel).toContain("bogus");

    // A history file opens read-only, with the tip's version beside it.
    const tab = [...root.querySelectorAll<HTMLButtonElement>('.tab-group.frozen [role="tab"]')].find((b) => b.textContent === "history/1.0.thema")!;
    await act(async () => tab.click());
    const frozen = EditorView.findFromDOM(root.querySelector<HTMLElement>(".lineage .cm-editor")!)!;
    expect(frozen.state.doc.toString()).toContain("ForwardLens");
    expect(frozen.state.readOnly).toBe(true);
    expect(root.querySelector(".frozen-note")!.textContent).toContain("tip (cuts as 2.0)");
    render(null, root);
  }, 60000);

  it("offers the host's examples that bind as they declare, and says which do not", async () => {
    await realThema();
    const pet = 'name: "pet"\nschemas: {\n\t"0": {\n\t\t"0": {\n\t\t\tschema: {\n\t\t\t\tname: string\n\t\t\t}\n\t\t}\n\t}\n}\n';
    hostManifest = {
      examples: [
        {
          example: { id: "pets/pet", title: "pet", category: "Pets", summary: "A pet.", kind: "normal", binds: true },
          lineage: { normal: { path: "lineage.cue", text: pet } },
        },
        {
          example: { id: "pets/empty", title: "empty", category: "Pets", summary: "No schemas.", kind: "normal", binds: true },
          lineage: { normal: { path: "lineage.cue", text: 'name: "empty"\nschemas: {}\n' } },
        },
      ],
    };
    const { App } = await import("./app");
    const root = document.createElement("div");
    document.body.append(root);
    await act(async () => {
      render(<App />, root);
    });
    await waitFor(() => root.querySelector('header select optgroup[label="Pets"]') !== null);
    const group = root.querySelector('header select optgroup[label="Pets"]')!;
    expect([...group.querySelectorAll("option")].map((o) => o.getAttribute("value"))).toEqual(["pets/pet"]);
    expect(root.querySelector(".notice")!.textContent).toContain('host example "pets/empty" is declared to bind, but does not');
    render(null, root);
    hostManifest = null;
  }, 60000);
});
