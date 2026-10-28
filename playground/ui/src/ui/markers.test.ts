import { describe, expect, it } from "vitest";
import type { Diagnostic } from "../api/types";
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
    expect(ms).toEqual([
      { from: 11, to: 16, severity: "error", message: "E0360: count is not total\nhelp: bound the field", code: "E0360" },
      { from: 0, to: 6, severity: "info", message: "E0360: declared here", code: "E0360" },
    ]);
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
