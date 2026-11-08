import { render } from "preact";
import { describe, expect, it } from "vitest";
import { Diagnostics } from "./Diagnostics";
import { markerDOM } from "./Editor";
import { type Marker, markersFor } from "./markers";
import type { Diagnostic } from "./types";

const at = { file: "tip.thema", line: 1, col: 1, origin: "user" } as const;
const lens: Diagnostic = {
  code: "E1001",
  severity: "warning",
  headline: "tip.thema has no ForwardLens, and one cannot be derived",
  primary: at,
  notes: [
    { label: "translating from 0.0 to tip needs it" },
    { pos: at, label: "0.0 declares b and tip does not, and nothing maps it (E0347)" },
    { pos: { file: "tip.thema", line: 3, col: 2, origin: "user" }, label: "size is optional in 0.0 and required in tip (E0343)" },
  ],
  help: ["write ForwardLens: {v0_0: _, tip: {...}} in tip.thema"],
};

// Each row of a diagnostic as the reader meets it: its class, then its text.
const rows = (el: Element) => [...el.children].map((c) => `${c.className}: ${c.textContent}`);

describe("Diagnostics", () => {
  // The list says a diagnostic in the text renderer's order, a note where the
  // primary is without repeating where, so the eye finds the problem first,
  // then why, then what to do.
  it("lists notes with a position first, then those without, then help", () => {
    const root = document.createElement("div");
    render(<Diagnostics diagnostics={[lens]} />, root);
    expect(rows(root.querySelector(".diagnostic")!)).toEqual([
      "headline: warning[E1001] tip.thema has no ForwardLens, and one cannot be derived",
      "where: --> tip.thema:1:1",
      "note: note: 0.0 declares b and tip does not, and nothing maps it (E0347) ",
      "note: note: size is optional in 0.0 and required in tip (E0343) (tip.thema:3:2)",
      "note: note: translating from 0.0 to tip needs it ",
      "help: help: write ForwardLens: {v0_0: _, tip: {...}} in tip.thema",
    ]);
  });
});

describe("markerDOM", () => {
  // A marker's tooltip carries the list's class names, in the list's order,
  // so the host styles the two alike.
  it("lays a marker out with the diagnostics list's class names", () => {
    const text = "Schema: {\n\tuid!: int\n\tsize!: int\n}\n";
    const [primary, elsewhere] = markersFor([lens], "tip.thema", text) as [Marker, Marker];
    expect(rows(markerDOM(primary))).toEqual([
      "headline: E1001 tip.thema has no ForwardLens, and one cannot be derived",
      "note: note: 0.0 declares b and tip does not, and nothing maps it (E0347)",
      "note: note: translating from 0.0 to tip needs it",
      "help: help: write ForwardLens: {v0_0: _, tip: {...}} in tip.thema",
    ]);
    expect(markerDOM(primary).querySelector(".headline .code")!.textContent).toBe("E1001");
    expect(markerDOM(primary).querySelector(".help .label")!.textContent).toBe("help:");
    expect(rows(markerDOM(elsewhere))).toEqual([
      "note: note: size is optional in 0.0 and required in tip (E0343)",
      "of: of E1001: tip.thema has no ForwardLens, and one cannot be derived",
    ]);
  });

  // A note the file cannot mark says where it is as the list does, in the
  // list's class.
  it("says where a note in another file is, as the list does", () => {
    const m: Pick<Marker, "code" | "lines"> = {
      code: "E0358",
      lines: [{ kind: "note", text: "0.0 declares count as string", where: "history/0.0.thema:2:2" }],
    };
    expect(rows(markerDOM(m))).toEqual(["note: note: 0.0 declares count as string (history/0.0.thema:2:2)"]);
    expect(markerDOM(m).querySelector(".note .where")!.textContent).toBe("(history/0.0.thema:2:2)");
  });
});
