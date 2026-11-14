import { describe, expect, it } from "vitest";
import { argsOf } from "./ops.js";

describe("argsOf", () => {
  it("offers what each operation takes", () => {
    expect(argsOf("translate")).toEqual(["from", "to"]);
    expect(argsOf("validate-any")).toEqual([]);
    expect(argsOf("jsonschema")).toEqual(["version", "format"]);
  });
});
