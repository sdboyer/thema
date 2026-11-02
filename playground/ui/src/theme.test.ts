import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { tokenColors } from "@sdboyer/thema-ui";

// The page's color variables in each scheme, read from the stylesheet the
// page ships: the light ones in :root, the dark ones overriding them.
const css = readFileSync(resolve(__dirname, "style.css"), "utf8");
function vars(block: string): Record<string, string> {
  return Object.fromEntries([...block.matchAll(/(--[\w-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)].map((m) => [m[1]!, m[2]!.toLowerCase()]));
}
const lightBlock = /^:root\s*\{([^}]*)\}/m.exec(css)![1]!;
const darkBlock = /prefers-color-scheme:\s*dark\)\s*\{\s*:root\s*\{([^}]*)\}/.exec(css)![1]!;
const light = vars(lightBlock);
const schemes = { light, dark: { ...light, ...vars(darkBlock) } };

// WCAG 2 relative luminance and contrast ratio.
function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
}
function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi! + 0.05) / (lo! + 0.05);
}

describe("editor colors", () => {
  for (const [scheme, v] of Object.entries(schemes)) {
    it(`are readable on the editor's background in the ${scheme} scheme`, () => {
      const bg = v["--bg"]!;
      expect(bg).toBeDefined();
      for (const name of [...Object.keys(tokenColors).map((n) => `--syn-${n}`), "--text"]) {
        const color = v[name];
        expect(color, `${name} is defined for the ${scheme} scheme`).toBeDefined();
        // WCAG AA for body text.
        expect(contrast(color!, bg), `${name} ${color} on ${bg}`).toBeGreaterThanOrEqual(4.5);
      }
    });

    it(`keep the selection distinct from the background in the ${scheme} scheme`, () => {
      expect(contrast(v["--selection"]!, v["--bg"]!)).toBeGreaterThan(1.3);
      expect(contrast(v["--text"]!, v["--selection"]!)).toBeGreaterThanOrEqual(4.5);
    });
  }

  it("give the dark scheme its own token colors", () => {
    for (const name of Object.keys(tokenColors)) {
      expect(vars(darkBlock)[`--syn-${name}`], `--syn-${name} in the dark scheme`).toBeDefined();
    }
  });
});
