// The tour's claims about thema, checked against the real thema: each step's
// data, the answers it says the reader will see, and the codes it names.
// Needs npm run sync-thema; skipped without it.
import { beforeAll, describe, expect, it } from "vitest";
import type { Thema } from "../api/client";
import type { DataInput, File, LineageInput } from "../api/types";
import { haveThema, realThema } from "../test/realThema";
import { bindSummary } from "../state/tip";
import { type Step, data, lensFix, majorLens, minorDefault, minorFile, steps } from "./steps";

const step = (id: string): Step => {
  const s = steps.find((x) => x.id === id);
  if (!s) throw new Error(`no step ${id}`);
  return s;
};
const doc = (text: string): DataInput => ({ filename: "data.json", text });
const codes = (r: { diagnostics: { code: string }[] }) => r.diagnostics.map((d) => d.code);
// Every code a step names, in `backticks`.
const named = (s: Step) => [...s.see.matchAll(/`(E\d{4})`/g)].map((m) => m[1]);

describe.skipIf(!haveThema())("the tour, against thema", () => {
  let t: Thema;
  let ship: File[];
  const lineage = (files: File[]): LineageInput => ({ name: "ship", files });
  const edit = (files: File[], path: string, f: (text: string) => string) => {
    expect(files.some((x) => x.path === path), path).toBe(true);
    return files.map((x) => (x.path === path ? { ...x, text: f(x.text) } : x));
  };
  const remove = (text: string, what: string) => {
    expect(text).toContain(what);
    return text.replace(what, "");
  };
  const withMinor = () => edit(ship, "tip.thema", () => minorFile);
  const withMajor = () => edit(withMinor(), "tip.thema", (s) => remove(s, minorDefault));
  const withLens = (lens: string) => edit(withMajor(), "tip.thema", (s) => s + lens);
  const summary = (b: { versions?: string[]; tip?: Parameters<typeof bindSummary>[1] }) => bindSummary(b.versions ?? [], b.tip);

  beforeAll(async () => {
    t = await realThema();
    // The first step loads the example by its title.
    const list = await t.call("examples", {});
    const ex = list.examples.find((e) => e.category === "Authoring form" && e.title === "ship");
    expect(ex).toBeDefined();
    ship = (await t.call("example", { id: ex!.id })).lineage.files!;
  }, 60000);

  it("starts from a lineage that binds 0.0 and 1.0, with a clean tip", async () => {
    expect(ship.map((f) => f.path).sort()).toEqual(["history/0.0.thema", "history/1.0.thema", "tip.thema"]);
    const b = await t.call("bind", { lineage: lineage(ship) });
    expect(b.versions).toEqual(["0.0", "1.0"]);
    expect(b.tip!.clean).toBe(true);
    expect(step("lineage").see).toContain(summary(b));
  });

  it("validates", async () => {
    expect((await t.call("validate", { lineage: lineage(ship), data: doc(data.v1) })).version).toBe("1.0");
    const r = await t.call("validate", { lineage: lineage(ship), data: doc(data.v0) });
    expect(r.ok).toBe(false);
    expect(codes(r)).toEqual(named(step("validate")));
  });

  it("finds the data's version", async () => {
    const r = await t.call("validate-any", { lineage: lineage(ship), data: doc(data.v0) });
    expect(r.version).toBe("0.0");
  });

  it("translates forward, with a placeholder", async () => {
    const r = await t.call("translate", { lineage: lineage(ship), data: doc(data.v0), to: "1.0" });
    expect([r.from, r.to, r.cutsAs]).toEqual(["0.0", "1.0", undefined]);
    expect(r.output).toEqual(JSON.parse(step("translate-forward").result!));
    expect(r.lacunas!.flatMap((s) => s.lacunas.map((l) => l.type))).toEqual(["Placeholder"]);
  });

  it("translates back, dropping a field", async () => {
    const r = await t.call("translate", { lineage: lineage(ship), data: doc(data.v1), from: "1.0", to: "0.0" });
    expect(r.output).toEqual(JSON.parse(step("translate-back").result!));
    expect(r.lacunas!.flatMap((s) => s.lacunas.map((l) => l.type))).toEqual(["DroppedField"]);
  });

  it("binds the tip as a minor version", async () => {
    const b = await t.call("bind", { lineage: lineage(withMinor()) });
    expect(b.ok).toBe(true);
    expect(b.versions).toEqual(["0.0", "1.0", "1.1"]);
    expect(b.tip!.provisional).toBe(true);
    expect(step("minor").see).toContain(summary(b));
  });

  it("hydrates and dehydrates", async () => {
    const h = await t.call("hydrate", { lineage: lineage(withMinor()), data: doc(data.v11Bare), version: "1.1" });
    expect(h.output).toEqual(JSON.parse(step("hydrate").result!));
    const d = await t.call("dehydrate", { lineage: lineage(withMinor()), data: doc(data.v11Default), version: "1.1" });
    expect(d.output).toEqual(JSON.parse(data.v11Bare));
  });

  it("chains translation through 1.0", async () => {
    const r = await t.call("translate", { lineage: lineage(withMinor()), data: doc(data.v11Large), from: "1.1", to: "0.0" });
    expect(r.output).toEqual(JSON.parse(step("translate-chain").result!));
    expect([r.from, r.to, r.cutsAs]).toEqual(["tip", "0.0", "1.1"]);
    expect(r.lacunas!.map((s) => s.lacunas.map((l) => `${l.type} ${(l.sourceFields ?? []).map((f) => f.path)}`))).toEqual([
      ["DroppedField size"],
      ["DroppedField secondfield"],
    ]);
  });

  it("generates the TypeScript shown", async () => {
    const r = await t.call("typescript", { lineage: lineage(withMinor()) });
    expect(r.output).toBe(step("codegen").result);
  });

  it("binds the tip without its default as a major version, warning of its missing lens", async () => {
    const b = await t.call("bind", { lineage: lineage(withMajor()) });
    expect(b.ok).toBe(true);
    expect(step("major").see).toContain(summary(b));
    expect(codes(b)).toEqual(named(step("major")));
    expect(b.diagnostics.map((d) => d.severity)).toEqual(["warning"]);
    const r = await t.call("translate", { lineage: lineage(withMajor()), data: doc(data.v1), from: "1.0", to: "2.0" });
    expect(r.ok).toBe(false);
  });

  it("checks the tip's lens, and binds it once it writes size", async () => {
    const empty = await t.call("bind", { lineage: lineage(withLens(majorLens)) });
    expect(empty.ok).toBe(false);
    expect(codes(empty)[0]).toBe(named(step("lens-check"))[0]);
    const fixed = await t.call("bind", { lineage: lineage(withLens(majorLens.replace("tip: {}", `tip: {${lensFix}}`))) });
    expect(fixed.diagnostics).toEqual([]);
    expect(summary(fixed)).toBe("binds: 0.0, 1.0, tip (cuts as 2.0)");
  });

  it("compiles to a normal form with the implied lenses written out", async () => {
    const r = await t.call("compile", { name: "ship", files: withLens(majorLens.replace("tip: {}", `tip: {${lensFix}}`)) });
    expect(r.normal).toContain('"2.0->1.0"');
    expect(r.normal).toContain("@thema(derived)");
  });

  it("names the code the error example reports", async () => {
    const list = await t.call("examples", {});
    const ex = list.examples.find((e) => e.category === "Errors" && e.title === "optional-to-required");
    expect(ex).toBeDefined();
    const b = await t.call("bind", { lineage: (await t.call("example", { id: ex!.id })).lineage });
    expect(codes(b)).toEqual(named(step("errors")));
  });
});
