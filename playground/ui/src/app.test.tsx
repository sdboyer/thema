import { EditorView } from "@codemirror/view";
import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Thema } from "./api/client";
import type { HostExample, RegisterExamplesRequest, RegisterExamplesResponse } from "./api/types";
import { docsBase } from "./ui/docs";

// A thema that answers like the real one for what the page asks at start
// and on edits. calls records the operations asked of it; registered is what
// register-examples answers, and what examples lists.
const calls: string[] = [];
let registered: RegisterExamplesResponse = { ok: true, diagnostics: [], registered: [] };
const fakeThema: Thema = {
  init: { ok: true, diagnostics: [], themaVersion: "v0.1.0", cueVersion: "v0.18.0", ops: [] },
  call: (async (op: string, req: unknown) => {
    calls.push(op);
    switch (op) {
      case "register-examples":
        hostManifestSent = req as RegisterExamplesRequest;
        return registered;
      case "examples":
        return {
          ok: true,
          diagnostics: [],
          examples: (hostManifestSent?.examples ?? []).filter((h) => registered.registered.includes(h.example.id)).map((h) => h.example),
        };
      case "example":
        return {
          ok: true,
          diagnostics: [],
          example: { id: "authoring/ship", title: "ship", category: "c", summary: "", kind: "authoring", binds: true },
          lineage: {
            name: "ship",
            files: [
              { path: "history/0.0.thema", text: "Schema: {\n\tname: string\n}\n" },
              { path: "tip.thema", text: "Schema: {\n\tname: string\n\tcrew: *1 | int\n}\n" },
            ],
          },
          data: [],
        };
      case "bind":
        return {
          ok: true,
          diagnostics: [],
          name: "ship",
          versions: ["0.0", "0.1"],
          normal: "",
          tip: { version: "0.1", clean: false, provisional: true },
        };
      default:
        return { ok: true, diagnostics: [] };
    }
  }) as Thema["call"],
};

// hostManifest is what fetchHostExamples finds: the host's examples.json, or
// null for none. hostManifestSent is what the page registered.
let hostManifest: RegisterExamplesRequest | Error | null = null;
let hostManifestSent: RegisterExamplesRequest | null = null;

vi.mock("./api/client", () => ({
  startThema: () => Promise.resolve(fakeThema),
  fetchHostExamples: async () => {
    if (hostManifest instanceof Error) throw hostManifest;
    return hostManifest;
  },
}));

const { App } = await import("./app");

async function settle() {
  for (let i = 0; i < 5; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

async function mount(): Promise<HTMLElement> {
  calls.length = 0;
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

  describe("with a tip and history", () => {
    const fileTabs = (root: HTMLElement) => root.querySelector<HTMLElement>('[aria-label="lineage files"]')!;
    const tabNamed = (root: HTMLElement, name: string) =>
      [...fileTabs(root).querySelectorAll<HTMLButtonElement>('[role="tab"]')].find((b) => b.textContent === name)!;
    const lineageView = (root: HTMLElement) => EditorView.findFromDOM(root.querySelector<HTMLElement>(".lineage .cm-editor")!)!;
    const waitBound = async (root: HTMLElement) => {
      for (let i = 0; i < 40 && !root.querySelector(".bind-status")!.textContent!.startsWith("binds"); i++) {
        await act(async () => {
          await new Promise((r) => setTimeout(r, 25));
        });
      }
    };

    it("opens the tip, and groups history apart from it, frozen", async () => {
      const root = await mount();
      const tabs = fileTabs(root);
      expect(tabs.querySelector('.tab.active [role="tab"]')!.textContent).toBe("tip.thema");
      const group = tabs.querySelector(".tab-group.frozen")!;
      expect(group.getAttribute("aria-label")).toBe("history (frozen)");
      expect([...group.querySelectorAll('[role="tab"]')].map((b) => b.textContent)).toEqual(["history/0.0.thema"]);
      expect(group.contains(tabNamed(root, "tip.thema"))).toBe(false);
      render(null, root);
    });

    it("offers no way to add, rename or remove a file", async () => {
      const root = await mount();
      const prompt = vi.spyOn(window, "prompt").mockReturnValue("history/9.9.thema");
      for (const name of ["tip.thema", "history/0.0.thema"]) {
        await act(async () => {
          tabNamed(root, name).dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
        });
      }
      expect(prompt).not.toHaveBeenCalled();
      prompt.mockRestore();
      expect(fileTabs(root).querySelector(".close, .add")).toBeNull();
      expect([...fileTabs(root).querySelectorAll('[role="tab"]')].map((b) => b.textContent)).toEqual(["tip.thema", "history/0.0.thema"]);
      render(null, root);
    });

    it("shows a history file read-only, and the tip editable", async () => {
      const root = await mount();
      const format = [...root.querySelectorAll<HTMLButtonElement>(".lineage button")].find((b) => b.textContent === "Format")!;
      expect(lineageView(root).state.readOnly).toBe(false);
      expect(root.querySelector(".frozen-note")).toBeNull();
      expect(format.disabled).toBe(false);

      await act(async () => tabNamed(root, "history/0.0.thema").click());
      await waitBound(root);
      expect(lineageView(root).state.doc.toString()).not.toContain("crew");
      expect(lineageView(root).state.readOnly).toBe(true);
      expect(root.querySelector(".lineage-editor.frozen")).not.toBeNull();
      expect(root.querySelector(".frozen-note")!.textContent).toContain("history/0.0.thema is frozen");
      expect(root.querySelector(".frozen-note")!.textContent).toContain("tip (cuts as 0.1)");
      expect(format.disabled).toBe(true);

      await act(async () => tabNamed(root, "tip.thema").click());
      expect(lineageView(root).state.readOnly).toBe(false);
      expect(root.querySelector(".frozen-note")).toBeNull();
      render(null, root);
    });

    it("shows the tip's version as the tip wherever versions are shown", async () => {
      const root = await mount();
      await waitBound(root);
      expect(root.querySelector(".bind-status")!.textContent).toBe("binds: 0.0, tip (cuts as 0.1)");
      const options = [...root.querySelectorAll<HTMLOptionElement>('select[aria-label="version"] option')];
      expect(options.map((o) => [o.value, o.textContent])).toEqual([
        ["", "latest"],
        ["0.0", "0.0"],
        ["0.1", "tip (cuts as 0.1)"],
      ]);
      render(null, root);
    });
  });

  describe("with a host's examples", () => {
    const pet: HostExample = {
      example: { id: "pets/pet", title: "pet", category: "Pets", summary: "A pet.", kind: "normal", binds: true },
      lineage: { normal: { path: "lineage.cue", text: 'name: "pet"\n' } },
    };
    const empty: HostExample = { ...pet, example: { ...pet.example, id: "pets/empty", title: "empty" } };
    afterEach(() => {
      hostManifest = null;
      hostManifestSent = null;
      registered = { ok: true, diagnostics: [], registered: [] };
    });

    it("registers them before listing the examples, and offers them under their category", async () => {
      hostManifest = { examples: [pet] };
      registered = { ok: true, diagnostics: [], registered: ["pets/pet"] };
      const root = await mount();
      expect(hostManifestSent).toEqual(hostManifest);
      expect(calls.indexOf("register-examples")).toBeLessThan(calls.indexOf("examples"));
      const group = root.querySelector<HTMLOptGroupElement>('header select optgroup[label="Pets"]');
      expect([...group!.querySelectorAll("option")].map((o) => o.value)).toEqual(["pets/pet"]);
      expect(root.querySelector(".notice")).toBeNull();
      render(null, root);
    });

    it("says which were left out", async () => {
      hostManifest = { examples: [pet, empty] };
      registered = {
        ok: false,
        diagnostics: [{ code: "E0805", severity: "error", headline: 'host example "pets/empty" is declared to bind, but does not' }],
        registered: ["pets/pet"],
      };
      const root = await mount();
      expect(root.querySelector(".notice")!.textContent).toContain('host example "pets/empty" is declared to bind, but does not');
      expect(root.querySelector('header select option[value="pets/pet"]')).not.toBeNull();
      render(null, root);
    });

    it("says when they cannot be read, and goes on without them", async () => {
      hostManifest = new Error("fetching examples.json: 500 Internal Server Error");
      const root = await mount();
      expect(calls).not.toContain("register-examples");
      expect(root.querySelector(".notice")!.textContent).toContain("500");
      expect(root.querySelector(".lineage .cm-editor")).not.toBeNull();
      render(null, root);
    });

    it("registers nothing when the host serves none", async () => {
      const root = await mount();
      expect(calls).not.toContain("register-examples");
      expect(calls).toContain("examples");
      expect(root.querySelector(".notice")).toBeNull();
      render(null, root);
    });
  });
});
