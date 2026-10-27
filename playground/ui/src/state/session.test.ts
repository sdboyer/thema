import { describe, expect, it } from "vitest";
import { decodeShare, emptySession, encodeShare, lineageInput, normalize } from "./session";

describe("share links", () => {
  it("round-trip a session", async () => {
    const s = emptySession();
    s.files.push({ path: "history/0.0.thema", text: "Schema: {\n\tname: string\n}\n" });
    s.data.push({ filename: "two.yaml", text: "name: Argo\n", format: "yaml" });
    s.op = "translate";
    s.args.to = "1.0";
    const link = await encodeShare(s);
    expect(link.startsWith("s=")).toBe(true);
    expect(link).toMatch(/^s=[A-Za-z0-9_-]+$/);
    expect(await decodeShare("#" + link)).toEqual(s);
  });

  it("ignore a fragment that is not a share link", async () => {
    expect(await decodeShare("#section")).toBeNull();
    expect(await decodeShare("")).toBeNull();
  });

  it("reject a share link that cannot be read", async () => {
    await expect(decodeShare("#s=not-deflate")).rejects.toThrow();
  });

  it("compress", async () => {
    const s = emptySession();
    s.normal.text = "x: 1\n".repeat(2000);
    const link = await encodeShare(s);
    expect(link.length).toBeLessThan(s.normal.text.length / 10);
  });
});

describe("normalize", () => {
  it("fills what an older link lacks", () => {
    const s = normalize({ form: "normal", path: "lin" });
    expect(s.form).toBe("normal");
    expect(s.path).toBe("lin");
    expect(s.args).toEqual(emptySession().args);
    expect(s.files.length).toBeGreaterThan(0);
  });

  it("clamps the open file and document", () => {
    const s = normalize({ activeFile: 9, activeData: -1 });
    expect(s.activeFile).toBe(0);
    expect(s.activeData).toBe(0);
  });
});

describe("lineageInput", () => {
  it("sends the authoring form's name and files", () => {
    const s = emptySession();
    expect(lineageInput(s)).toEqual({ name: "ship", files: s.files });
  });

  it("sends one normal-form file and its path", () => {
    const s = { ...emptySession(), form: "normal" as const, path: "lin" };
    expect(lineageInput(s)).toEqual({ normal: s.normal, path: "lin" });
    expect(lineageInput({ ...s, path: "" })).toEqual({ normal: s.normal, path: undefined });
  });
});
