// The tour's claims about thema, checked against the real thema: each step's
// data, the answers it says the reader will see, and the codes it names.
// Needs npm run sync-thema; skipped without it.
import { beforeAll, describe, expect, it } from "vitest";
import type { Thema } from "../api/client";
import type { DataInput, File, LineageInput } from "../api/types";
import { haveThema, realThema } from "../test/realThema";
import { type Step, data, lensLine, minorDefault, minorFile, steps } from "./steps";

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
  const withMinor = () => [...ship, { path: "1.1.thema", text: minorFile }];

  beforeAll(async () => {
    t = await realThema();
    // The first step loads the example by its title.
    const list = await t.call("examples", {});
    const ex = list.examples.find((e) => e.category === "Authoring form" && e.title === "ship");
    expect(ex).toBeDefined();
    ship = (await t.call("example", { id: ex!.id })).lineage.files!;
  }, 60000);

  it("starts from a lineage that binds 0.0 and 1.0", async () => {
    const b = await t.call("bind", { lineage: lineage(ship) });
    expect(b.versions).toEqual(["0.0", "1.0"]);
    expect(step("lineage").see).toContain("binds: 0.0, 1.0");
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
    expect(r.output).toEqual(JSON.parse(step("translate-forward").result!));
    expect(r.lacunas!.flatMap((s) => s.lacunas.map((l) => l.type))).toEqual(["Placeholder"]);
  });

  it("translates back, dropping a field", async () => {
    const r = await t.call("translate", { lineage: lineage(ship), data: doc(data.v1), from: "1.0", to: "0.0" });
    expect(r.output).toEqual(JSON.parse(step("translate-back").result!));
    expect(r.lacunas!.flatMap((s) => s.lacunas.map((l) => l.type))).toEqual(["DroppedField"]);
  });

  it("refuses the lens without its line", async () => {
    const files = edit(ship, "1.0.thema", (s) => remove(s, `${lensLine}\n`));
    const b = await t.call("bind", { lineage: lineage(files) });
    expect(b.ok).toBe(false);
    expect(codes(b)[0]).toBe(named(step("lens-check"))[0]);
  });

  it("binds the minor version", async () => {
    const b = await t.call("bind", { lineage: lineage(withMinor()) });
    expect(b.versions).toEqual(["0.0", "1.0", "1.1"]);
    expect(step("minor").see).toContain(`binds: ${b.versions!.join(", ")}`);
  });

  it("refuses the minor version without its default", async () => {
    const files = edit(withMinor(), "1.1.thema", (s) => remove(s, minorDefault));
    const b = await t.call("bind", { lineage: lineage(files) });
    expect(codes(b)).toEqual(named(step("compat-check")));
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
    expect(r.lacunas!.map((s) => s.lacunas.map((l) => `${l.type} ${(l.sourceFields ?? []).map((f) => f.path)}`))).toEqual([
      ["DroppedField size"],
      ["DroppedField secondfield"],
    ]);
  });

  it("generates the TypeScript shown", async () => {
    const r = await t.call("typescript", { lineage: lineage(withMinor()) });
    expect(r.output).toBe(step("codegen").result);
  });

  it("compiles to a normal form with the implied lenses written out", async () => {
    const r = await t.call("compile", { name: "ship", files: withMinor() });
    expect(r.normal).toContain('"1.1->1.0"');
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
