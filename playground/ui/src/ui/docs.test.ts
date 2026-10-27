// @vitest-environment node
// Every link into thema's docs names a file, and a heading if it has one, in
// the docs of this tree: the docs the links will reach once this tree is on
// main.
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { docs, docsBase, lacunaTypeDoc } from "./docs";

const root = resolve(__dirname, "../../../..");
const docsDir = resolve(root, "docs");

// anchor is GitHub's id for a Markdown heading.
function anchor(heading: string): string {
  return heading
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N} _-]/gu, "")
    .replace(/ /g, "-");
}

describe("docs", () => {
  it("point at thema's docs or its Go API", () => {
    for (const [topic, d] of Object.entries(docs)) {
      expect(d.href.startsWith(docsBase) || d.href.startsWith("https://pkg.go.dev/github.com/sdboyer/thema"), topic).toBe(true);
    }
  });

  it("name files and headings this tree's docs have", () => {
    for (const [topic, d] of Object.entries(docs)) {
      if (!d.href.startsWith(docsBase)) continue;
      const [file, hash] = d.href.slice(docsBase.length).split("#");
      const path = resolve(docsDir, file!);
      expect(existsSync(path), `${topic}: ${file}`).toBe(true);
      if (hash) {
        const ids = [...readFileSync(path, "utf8").matchAll(/^#+ (.*)$/gm)].map((m) => anchor(m[1]!));
        expect(ids, topic).toContain(hash);
      }
    }
  });

  it("have an entry for every lacuna type thema declares", () => {
    const cue = readFileSync(resolve(root, "lacuna.cue"), "utf8");
    const block = cue.slice(cue.indexOf("LacunaTypes: {"));
    const types = [...block.slice(0, block.indexOf("\n}")).matchAll(/^\t(\w+):\s*"\1"/gm)].map((m) => m[1]!);
    expect(types).toContain("Placeholder");
    const page = readFileSync(resolve(docsDir, "reference/lacunas.md"), "utf8");
    const ids = [...page.matchAll(/^#+ (.*)$/gm)].map((m) => anchor(m[1]!));
    for (const t of types) {
      expect(ids, t).toContain(lacunaTypeDoc(t).href.split("#")[1]);
    }
  });
});
