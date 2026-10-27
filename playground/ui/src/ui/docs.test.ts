// @vitest-environment node
// Every link into thema's docs names a file, and a heading if it has one, in
// the docs of this tree: the docs the links will reach once this tree is on
// main.
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { docs, docsBase } from "./docs";

const docsDir = resolve(__dirname, "../../../../docs");

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
});
