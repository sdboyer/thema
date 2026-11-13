import { describe, expect, it } from "vitest";
import type { Diagnostic } from "./types";
import { docsBase } from "./docs";
import { docsURL, filesOf, markersFor } from "./markers";

const text = "Schema: {\n\tcount: int\n}\n";

const d: Diagnostic = {
  code: "E0360",
  severity: "error",
  headline: "count is not total",
  primary: { file: "1.0.thema", line: 2, col: 2, origin: "user" },
  notes: [
    { pos: { file: "1.0.thema", line: 1, col: 1, origin: "user" }, label: "declared here" },
    { pos: { file: "thema/lineage.cue", line: 9, col: 1, origin: "thema" }, label: "in thema's own CUE" },
    { label: "a note with no position" },
  ],
  help: ["bound the field"],
};

describe("markersFor", () => {
  it("marks the primary position and positioned notes in the file", () => {
    const ms = markersFor([d], "1.0.thema", text);
    expect(ms.map(({ lines: _, ...m }) => m)).toEqual([
      { from: 11, to: 16, severity: "error", message: "E0360: count is not total\nnote: in thema's own CUE (thema's own CUE, thema/lineage.cue:9:1)\nnote: a note with no position\nhelp: bound the field", code: "E0360" },
      { from: 0, to: 6, severity: "info", message: "note: declared here\nof E0360: count is not total", code: "E0360" },
    ]);
  });

  // A note this file cannot mark, in another file or in thema's own CUE, is
  // said in the primary's marker, with where it is, rather than dropped.
  it("says a note in another file in the primary's marker, with where it is", () => {
    const at = { file: "tip.thema", line: 2, col: 2, origin: "user" } as const;
    const across: Diagnostic = {
      code: "E0358",
      severity: "error",
      headline: "the ForwardLens in tip.thema would copy v0_0.count into count",
      primary: at,
      notes: [
        { pos: { file: "history/0.0.thema", line: 2, col: 2, origin: "user" }, label: "0.0 declares count as string" },
        { pos: at, label: "tip declares it as int" },
      ],
    };
    const ms = markersFor([across], "tip.thema", text);
    expect(ms).toHaveLength(1);
    expect(ms[0]!.lines).toEqual([
      { kind: "headline", text: "the ForwardLens in tip.thema would copy v0_0.count into count" },
      { kind: "note", text: "0.0 declares count as string", where: "history/0.0.thema:2:2" },
      { kind: "note", text: "tip declares it as int" },
    ]);
    expect(ms[0]!.message.split("\n")[1]).toBe("note: 0.0 declares count as string (history/0.0.thema:2:2)");
  });

  // The primary's marker says what the diagnostics list says, in its order:
  // the headline, the notes (those where the primary is, then those with no
  // position), then the help, each a line of its own kind.
  it("lays the primary's marker out as headline, notes, then help", () => {
    const at = { file: "1.0.thema", line: 1, col: 1, origin: "user" } as const;
    const lens: Diagnostic = {
      code: "E1001",
      severity: "warning",
      headline: "tip.thema has no ForwardLens, and one cannot be derived",
      primary: at,
      notes: [
        { label: "translating from 0.0 to tip needs it" },
        { pos: at, label: "0.0 declares b and tip does not, and nothing maps it (E0347)" },
      ],
      help: ["write ForwardLens: {v0_0: _, tip: {...}} in tip.thema", "write <field>: v0_0.b to move it, or b: _drop"],
    };
    const ms = markersFor([lens], "1.0.thema", text);
    expect(ms).toHaveLength(1);
    expect(ms[0]!.lines).toEqual([
      { kind: "headline", text: "tip.thema has no ForwardLens, and one cannot be derived" },
      { kind: "note", text: "0.0 declares b and tip does not, and nothing maps it (E0347)" },
      { kind: "note", text: "translating from 0.0 to tip needs it" },
      { kind: "help", text: "write ForwardLens: {v0_0: _, tip: {...}} in tip.thema" },
      { kind: "help", text: "write <field>: v0_0.b to move it, or b: _drop" },
    ]);
    expect(ms[0]!.message.split("\n").map((l) => l.split(":")[0])).toEqual(["E1001", "note", "note", "help", "help"]);
  });

  it("marks nothing in another file", () => {
    expect(markersFor([d], "history/0.0.thema", text)).toEqual([]);
  });

  it("marks nothing for a position beyond the text", () => {
    const far = { ...d, primary: { file: "1.0.thema", line: 40, col: 1, origin: "user" }, notes: [] };
    expect(markersFor([far], "1.0.thema", text)).toEqual([]);
  });
});

describe("filesOf", () => {
  it("lists the author's files with a primary position", () => {
    const k = { ...d, primary: { file: "thema/lineage.cue", line: 1, col: 1, origin: "thema" } };
    expect([...filesOf([d, k])]).toEqual(["1.0.thema"]);
  });
});

describe("docsURL", () => {
  it("links the code's page", () => {
    expect(docsURL("E0360")).toBe(`${docsBase}errors/E0360.md`);
  });
});
