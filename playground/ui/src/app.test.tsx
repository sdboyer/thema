import { EditorView } from "@codemirror/view";
import { render } from "preact";
import { act } from "preact/test-utils";
import { describe, expect, it, vi } from "vitest";
import type { Thema } from "./api/client";

// A thema that answers like the real one for what the page asks at start
// and on edits.
const fakeThema: Thema = {
  init: { ok: true, diagnostics: [], themaVersion: "v0.1.0", cueVersion: "v0.18.0", ops: [] },
  call: (async (op: string) => {
    switch (op) {
      case "examples":
        return { ok: true, diagnostics: [], examples: [] };
      case "example":
        return {
          ok: true,
          diagnostics: [],
          example: { id: "authoring/ship", title: "ship", category: "c", summary: "", kind: "authoring", binds: true },
          lineage: { name: "ship", files: [{ path: "0.0.thema", text: "Schema: {\n\tname: string\n}\n" }] },
          data: [],
        };
      case "bind":
        return { ok: true, diagnostics: [], name: "ship", versions: ["0.0"], normal: "" };
      default:
        return { ok: true, diagnostics: [] };
    }
  }) as Thema["call"],
};

vi.mock("./api/client", () => ({ startThema: () => Promise.resolve(fakeThema) }));

const { App } = await import("./app");

async function settle() {
  for (let i = 0; i < 5; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

describe("App", () => {
  it("survives an edit to the lineage", async () => {
    const root = document.createElement("div");
    document.body.append(root);
    await act(async () => {
      render(<App />, root);
    });
    await settle();
    const dom = root.querySelector<HTMLElement>(".lineage .cm-editor");
    expect(dom).not.toBeNull();
    const view = EditorView.findFromDOM(dom!)!;
    await act(async () => {
      view.dispatch({ changes: { from: view.state.doc.length, insert: "\nx: 1" } });
    });
    await settle();
    expect(view.state.doc.toString()).toContain("x: 1");
  }, 10000);
});
