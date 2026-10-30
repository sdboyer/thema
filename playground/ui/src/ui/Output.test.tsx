import { render } from "preact";
import { describe, expect, it } from "vitest";
import { docsBase } from "./docs";
import { Output } from "./Output";

describe("Output", () => {
  it("links each lacuna type to its reference entry", () => {
    const root = document.createElement("div");
    render(
      <Output
        onOpen={() => {}}
        outcome={{
          op: "translate",
          resp: {
            ok: true,
            diagnostics: [],
            from: "1.0",
            to: "0.0",
            output: { firstfield: "hello" },
            lacunas: [{ from: [1, 0], to: [0, 0], lacunas: [{ type: "DroppedField", message: "gone", sourceFields: [{ path: "secondfield", value: 42 }] }] }],
          },
        }}
      />,
      root,
    );
    const cell = [...root.querySelectorAll(".lacunas td")].find((td) => td.textContent?.startsWith("DroppedField"))!;
    expect(cell.querySelector("a.doc-link")!.getAttribute("href")).toBe(`${docsBase}reference/lacunas.md#droppedfield`);
    expect(root.querySelector(".lacunas caption a.doc-link")!.getAttribute("href")).toBe(`${docsBase}reference/lacunas.md`);
  });

  it("reads versions through the label it is given", () => {
    const root = document.createElement("div");
    const label = (v: string) => (v === "1.1" || v === "tip" ? "tip (cuts as 1.1)" : v);
    render(
      <Output
        onOpen={() => {}}
        label={label}
        outcome={{
          op: "translate",
          resp: {
            ok: true,
            diagnostics: [],
            from: "tip",
            to: "1.0",
            cutsAs: "1.1",
            output: { firstfield: "hello" },
            lacunas: [{ from: [1, 1], to: [1, 0], lacunas: [{ type: "DroppedField", message: "gone", sourceFields: [{ path: "size", value: "large" }] }] }],
          },
        }}
      />,
      root,
    );
    expect(root.querySelector(".status")!.textContent).toBe("Translated from tip (cuts as 1.1) to 1.0, with 1 lacuna.");
    expect(root.querySelector(".lacunas td")!.textContent).toBe("tip (cuts as 1.1) → 1.0");
  });
});
