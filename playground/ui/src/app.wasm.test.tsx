// The page against the real thema: start, load the default example, edit it.
// Needs go generate ./cmd/thema/playground; skipped without it.
import { cursorLineEnd, insertNewlineAndIndent } from "@codemirror/commands";
import { EditorView } from "@codemirror/view";
import { render } from "preact";
import { act } from "preact/test-utils";
import { describe, expect, it, vi } from "vitest";
import { haveThema, realThema } from "./test/realThema";

// startThema runs thema in-process; the import waits for the call, since
// realThema itself imports this module.
vi.mock("./api/client", async (orig) => {
  const mod = await orig<typeof import("./api/client")>();
  return { ...mod, startThema: async () => (await import("./test/realThema")).realThema() };
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
  it("loads the default example, binds it, and rebinds an edit typed in a burst", async () => {
    await realThema();
    const { App } = await import("./app");
    const root = document.createElement("div");
    document.body.append(root);
    await act(async () => {
      render(<App />, root);
    });
    await waitFor(() => root.querySelector(".bind-status")?.textContent?.startsWith("binds") ?? false);
    expect(root.querySelector(".bind-status")!.textContent).toBe("binds: 0.0, 1.0");

    const view = EditorView.findFromDOM(root.querySelector<HTMLElement>(".lineage .cm-editor")!)!;
    const at = view.state.doc.toString().indexOf("secondfield: -1") + "secondfield: -1".length;
    // As a user would: End and Enter through the keymap's commands, then
    // type faster than the page's effects run, which wait for a frame.
    await act(async () => {
      view.dispatch({ selection: { anchor: at - 5 } });
      cursorLineEnd(view);
      insertNewlineAndIndent(view);
    });
    for (const ch of "bogus: 1") {
      view.dispatch(view.state.replaceSelection(ch));
      await Promise.resolve();
      await Promise.resolve();
    }
    await waitFor(() => root.querySelector(".bind-status")?.textContent?.includes("diagnostics") ?? false);
    expect(view.state.doc.toString()).toContain("secondfield: -1\n\t\tbogus: 1");
    const panel = root.querySelector(".panel")!.textContent!;
    expect(panel).toContain("E0341");
    expect(panel).toContain("bogus");
    render(null, root);
  }, 60000);
});
