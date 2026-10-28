import { describe, expect, it } from "vitest";
import { argsOf, requestFor } from "./requests";
import { emptySession, type Session } from "./session";

const with_ = (patch: Partial<Session>): Session => ({ ...emptySession(), ...patch });

describe("requestFor", () => {
  it("validates the open document against the chosen version", () => {
    const s = with_({ op: "validate", args: { version: "1.0", from: "", to: "", format: "" } });
    expect(requestFor(s)).toEqual({
      op: "validate",
      req: { lineage: { name: "ship", files: s.files }, data: s.data[0], version: "1.0" },
    });
  });

  it("leaves empty arguments out, so thema chooses", () => {
    const r = requestFor(with_({ op: "hydrate" }));
    expect(r).toMatchObject({ op: "hydrate", req: { version: undefined } });
  });

  it("says what translate lacks", () => {
    expect(requestFor(with_({ op: "translate" }))).toBe("choose the version to translate to");
    expect(requestFor(with_({ op: "translate", args: { version: "", from: "", to: "1.0", format: "" } }))).toMatchObject({
      op: "translate",
      req: { to: "1.0", from: undefined },
    });
  });

  it("needs a data document for a data operation", () => {
    expect(requestFor(with_({ op: "validate-any", data: [] }))).toBe("add a data document to run this");
  });

  it("generates code with no data, and no format for TypeScript", () => {
    const s = with_({ op: "typescript", data: [], args: { version: "", from: "", to: "", format: "yaml" } });
    expect(requestFor(s)).toMatchObject({ op: "typescript", req: { format: undefined } });
    expect(requestFor({ ...s, op: "openapi" })).toMatchObject({ op: "openapi", req: { format: "yaml" } });
  });
});

describe("argsOf", () => {
  it("offers what each operation takes", () => {
    expect(argsOf("translate")).toEqual(["from", "to"]);
    expect(argsOf("validate-any")).toEqual([]);
    expect(argsOf("jsonschema")).toEqual(["version", "format"]);
  });
});
