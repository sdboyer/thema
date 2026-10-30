import { describe, expect, it } from "vitest";
import type { TipInfo } from "../api/types";
import { bindSummary, byKind, isHistory, isTip, tipLabel, versionLabel } from "./tip";

const cutting: TipInfo = { version: "1.1", clean: false, provisional: true };
const clean: TipInfo = { version: "1.0", clean: true, provisional: false };

describe("the tip and history", () => {
  it("tells the two kinds of file apart", () => {
    expect(isTip("tip.thema")).toBe(true);
    expect(isTip("history/1.0.thema")).toBe(false);
    expect(isHistory("history/1.0.thema")).toBe(true);
    expect(isHistory("tip.thema")).toBe(false);
    expect(isHistory("1.0.thema")).toBe(false);
  });

  it("orders the tip first, then history by version", () => {
    const f = (path: string) => ({ path, text: "" });
    const files = [f("history/10.0.thema"), f("history/2.1.thema"), f("tip.thema"), f("history/2.0.thema")];
    expect(byKind(files).map((x) => x.path)).toEqual(["tip.thema", "history/2.0.thema", "history/2.1.thema", "history/10.0.thema"]);
  });

  it("labels the tip from its version and whether it is clean", () => {
    expect(tipLabel(cutting)).toBe("tip (cuts as 1.1)");
    expect(tipLabel(clean)).toBe("tip (clean at 1.0)");
  });

  it("labels the provisional version, and tip, as the tip, and no other", () => {
    expect(versionLabel("1.1", cutting)).toBe("tip (cuts as 1.1)");
    expect(versionLabel("tip", cutting)).toBe("tip (cuts as 1.1)");
    expect(versionLabel("tip", null)).toBe("tip");
    expect(versionLabel("1.0", cutting)).toBe("1.0");
    expect(versionLabel("1.0", clean)).toBe("1.0");
    expect(versionLabel("1.0", undefined)).toBe("1.0");
  });

  it("says what a lineage binds, with its tip", () => {
    expect(bindSummary(["0.0", "1.0", "1.1"], cutting)).toBe("binds: 0.0, 1.0, tip (cuts as 1.1)");
    expect(bindSummary(["0.0", "1.0"], clean)).toBe("binds: 0.0, 1.0 · tip (clean at 1.0)");
    expect(bindSummary(["0.0"], null)).toBe("binds: 0.0");
  });
});
