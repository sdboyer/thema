import { EditorView } from "@codemirror/view";
import { render } from "preact";
import { act } from "preact/test-utils";
import { describe, expect, it, vi } from "vitest";
import type { Thema } from "./api/client";
import { docsBase } from "./ui/docs";

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

async function mount(): Promise<HTMLElement> {
  const root = document.createElement("div");
  document.body.append(root);
  await act(async () => {
    render(<App />, root);
  });
  await settle();
  return root;
}

function opButton(root: HTMLElement, op: string): HTMLButtonElement {
  const b = [...root.querySelectorAll<HTMLButtonElement>(".op button")].find((x) => x.textContent === op);
  if (!b) throw new Error(`no button for ${op}`);
  return b;
}

describe("App", () => {
  it("survives an edit to the lineage", async () => {
    const root = await mount();
    const dom = root.querySelector<HTMLElement>(".lineage .cm-editor");
    expect(dom).not.toBeNull();
    const view = EditorView.findFromDOM(dom!)!;
    await act(async () => {
      view.dispatch({ changes: { from: view.state.doc.length, insert: "\nx: 1" } });
    });
    await settle();
    expect(view.state.doc.toString()).toContain("x: 1");
  }, 10000);

  it("leads the work column with a button for every operation", async () => {
    const root = await mount();
    const work = root.querySelector(".work")!;
    expect(work.firstElementChild!.classList.contains("ops")).toBe(true);
    const ops = [...work.querySelectorAll(".op button")].map((b) => b.textContent);
    expect(ops).toEqual(["validate", "validate-any", "translate", "hydrate", "dehydrate", "openapi", "jsonschema", "typescript"]);
    expect(opButton(root, "validate").getAttribute("aria-pressed")).toBe("true");
    render(null, root);
  });

  it("shows the data only for operations that take it", async () => {
    const root = await mount();
    const dataEditor = () => root.querySelector(".work .data-editor");
    expect(dataEditor()).not.toBeNull();
    await act(async () => opButton(root, "typescript").click());
    expect(opButton(root, "typescript").getAttribute("aria-pressed")).toBe("true");
    expect(opButton(root, "validate").getAttribute("aria-pressed")).toBe("false");
    expect(dataEditor()).toBeNull();
    expect(root.querySelector(".work .panel")).not.toBeNull();
    await act(async () => opButton(root, "translate").click());
    expect(dataEditor()).not.toBeNull();
    render(null, root);
  });

  it("links every operation to its reference entry", async () => {
    const root = await mount();
    const link = (op: string) => opButton(root, op).parentElement!.querySelector<HTMLAnchorElement>("a.doc-link");
    for (const op of ["validate", "validate-any", "translate", "hydrate", "dehydrate"]) {
      expect(link(op)!.href, op).toBe(`${docsBase}reference/operations.md#${op}`);
    }
    expect(link("jsonschema")!.href).toBe(`${docsBase}reference/codegen.md#json-schema`);
    expect(link("translate")!.target).toBe("_blank");
    render(null, root);
  });

  it("links the normal form tab, and no other output tab", async () => {
    const root = await mount();
    const tabs = [...root.querySelectorAll('[aria-label="output"] .tab')];
    expect(tabs.map((t) => t.querySelector("a.doc-link")?.getAttribute("href") ?? null)).toEqual([null, null, `${docsBase}reference/lineage.md#forms`]);
    render(null, root);
  });

  it("opens the tour beside the work, and closes it", async () => {
    const root = await mount();
    const tourButton = [...root.querySelectorAll<HTMLButtonElement>("header button")].find((b) => b.textContent === "Tour")!;
    expect(root.querySelector(".tour")).toBeNull();
    await act(async () => tourButton.click());
    expect(root.querySelector("main > .tour")).not.toBeNull();
    expect(root.querySelector("main")!.classList.contains("touring")).toBe(true);
    await act(async () => root.querySelector<HTMLButtonElement>(".tour .close")!.click());
    expect(root.querySelector(".tour")).toBeNull();
    render(null, root);
  });
});
