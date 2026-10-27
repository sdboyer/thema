// @vitest-environment node
// Every link into thema's docs names a file and a heading that exist at the
// pinned commit. Needs a thema checkout that has the commit (THEMA, .thema
// as CI checks it out, or ../thema); skipped without one.
import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { docs, themaCommit } from "./docs";

const prefix = `https://github.com/sdboyer/thema/blob/${themaCommit}/docs/`;

function checkout(): string | undefined {
  for (const dir of [process.env.THEMA, ".thema", "../thema"]) {
    if (!dir || !existsSync(resolve(dir, ".git"))) continue;
    try {
      execFileSync("git", ["-C", dir, "cat-file", "-e", `${themaCommit}^{commit}`], { stdio: "ignore" });
      return dir;
    } catch {
      // This checkout lacks the commit.
    }
  }
  return undefined;
}

// anchor is GitHub's id for a Markdown heading.
function anchor(heading: string): string {
  return heading
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N} _-]/gu, "")
    .replace(/ /g, "-");
}

const thema = checkout();

describe("docs", () => {
  it("point at the pinned commit", () => {
    expect(themaCommit).toMatch(/^[0-9a-f]{40}$/);
    for (const [topic, d] of Object.entries(docs)) {
      expect(d.href.startsWith(prefix) || d.href.startsWith("https://pkg.go.dev/github.com/sdboyer/thema"), topic).toBe(true);
    }
  });

  it.skipIf(!thema)("name files and headings thema's docs have", () => {
    for (const [topic, d] of Object.entries(docs)) {
      if (!d.href.startsWith(prefix)) continue;
      const [file, hash] = d.href.slice(prefix.length).split("#");
      const text = execFileSync("git", ["-C", thema!, "show", `${themaCommit}:docs/${file}`]).toString();
      if (hash) {
        const ids = [...text.matchAll(/^#+ (.*)$/gm)].map((m) => anchor(m[1]!));
        expect(ids, topic).toContain(hash);
      }
    }
  });
});
