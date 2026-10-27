import { describe, expect, it } from "vitest";
import { charIndexOfByte, offsetOf, spanAt } from "./positions";

describe("offsetOf", () => {
  const text = "a: 1\nbb: \"x\"\n\tccc: 3\n";

  it("maps a line and byte column to an offset", () => {
    expect(offsetOf(text, { line: 1, col: 1 })).toBe(0);
    expect(offsetOf(text, { line: 2, col: 5 })).toBe(9);
    expect(text[offsetOf(text, { line: 3, col: 2 })!]).toBe("c");
  });

  it("clamps a column past the line", () => {
    expect(offsetOf(text, { line: 1, col: 99 })).toBe(4);
  });

  it("refuses a line the text lacks", () => {
    expect(offsetOf(text, { line: 9, col: 1 })).toBeNull();
    expect(offsetOf(text, { line: 0, col: 1 })).toBeNull();
  });

  it("counts columns in bytes", () => {
    // é is two bytes, 🚀 four (and two UTF-16 units).
    const t = 'é: "🚀", n: 1';
    expect(t.slice(offsetOf(t, { line: 1, col: 5 })!)).toBe('"🚀", n: 1');
    expect(t.slice(offsetOf(t, { line: 1, col: 13 })!)).toBe("n: 1");
  });
});

describe("charIndexOfByte", () => {
  it("is the identity for ASCII", () => {
    expect(charIndexOfByte("abc", 2)).toBe(2);
  });
});

describe("spanAt", () => {
  it("covers a word, a quoted string, or one character", () => {
    const t = 'count: "a b" & {';
    expect(spanAt(t, 0)).toEqual({ from: 0, to: 5 });
    expect(spanAt(t, 7)).toEqual({ from: 7, to: 12 });
    expect(spanAt(t, 15)).toEqual({ from: 15, to: 16 });
  });
});
