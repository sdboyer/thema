import { describe, expect, it } from "vitest";

// The highlight style colors each token kind through the page's --syn-*
// variables; ui/src/theme.test.ts checks the playground's stylesheet defines
// them readably.
describe("highlight", () => {
  it("gives every token kind its color, comments included", async () => {
    const { highlight } = await import("./theme");
    const { highlightTree } = await import("@lezer/highlight");
    const { cue } = await import("./cue-lang");
    const { EditorState } = await import("@codemirror/state");
    const { ensureSyntaxTree } = await import("@codemirror/language");
    const doc = 'Schema: {\n\t// a note\n\tname: string | *"x"\n\tn: 1\n}\n';
    const state = EditorState.create({ doc, extensions: [cue] });
    const tree = ensureSyntaxTree(state, doc.length, 5000)!;
    const classes = new Map<string, string>();
    highlightTree(tree, highlight, (from, to, cls) => classes.set(doc.slice(from, to), cls));
    // Each class is a rule of the highlight style's module; find its color.
    const rules = highlight.module!.getRules();
    const colorOf = (text: string) => {
      const cls = classes.get(text);
      expect(cls, `${text} is highlighted`).toBeDefined();
      return new RegExp(`\\.${cls!.split(" ")[0]} \\{([^}]*)\\}`).exec(rules)?.[1] ?? "";
    };
    expect(colorOf("// a note")).toContain("var(--syn-comment)");
    expect(colorOf("// a note")).toContain("italic");
    expect(colorOf('"x"')).toContain("var(--syn-string)");
    expect(colorOf("Schema")).toContain("var(--syn-keyword)");
    expect(colorOf("string")).toContain("var(--syn-type)");
    expect(colorOf("name")).toContain("var(--syn-property)");
  });
});
