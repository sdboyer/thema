import { StringStream } from "@codemirror/language";
import { describe, expect, it } from "vitest";
import { cueParser } from "./cue-lang";

// tokens runs the tokenizer over text, as CodeMirror does line by line, and
// fails rather than hang if a call does not advance.
function tokens(text: string): [string, string | null][] {
  const state = cueParser.startState!(2);
  const out: [string, string | null][] = [];
  for (const line of text.split("\n")) {
    const s = new StringStream(line, 2, 2);
    if (line === "") {
      cueParser.blankLine?.(state, 2);
      continue;
    }
    let guard = 0;
    while (!s.eol()) {
      const start = s.pos;
      const style = cueParser.token(s, state);
      if (s.pos === start) throw new Error(`token did not advance at ${JSON.stringify(line)}:${start}`);
      if (++guard > 1000) throw new Error("runaway");
      out.push([line.slice(start, s.pos), style]);
      s.start = s.pos;
    }
  }
  return out;
}

describe("the CUE tokenizer", () => {
  it("advances on every line of a version file", () => {
    const text = 'Schema: {\n\tname: string // the name\n\tn?: int & >0 | *1\n}\n\nForwardLens: {\n\tv0_0: _\n\tv1_0: {\n\t\t\n\t\tbogus: 1\n\t\ts: "a \\" b"\n\t\tt: """\n\t\t\tmulti\n\t\t\t"""\n\t\t#D: [...string]\n\t\t@attr(x)\n\t}\n}\n';
    expect(() => tokens(text)).not.toThrow();
  });

  it("names what it sees", () => {
    const t = tokens('a: "x" // c').filter(([, s]) => s !== null);
    expect(t).toEqual([
      ["a", "propertyName"],
      [":", "punctuation"],
      ['"x"', "string"],
      ["// c", "comment"],
    ]);
  });
});
