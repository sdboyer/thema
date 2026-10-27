// Puts thema's side of the playground in place, from a thema checkout:
//
//   public/thema.wasm   cmd/thema-wasm, built as thema's release builds it
//   public/wasm_exec.js Go's loader for it, from the Go that built it
//   public/thema-fs.js  the filesystem thema.wasm needs in a browser
//   src/api/types.ts    the wire format, generated in thema from its Go types
//
// usage: node scripts/sync-thema.mjs <thema-checkout> [--check]
//
// The checkout should be at the commit thema.version pins; CI checks it out
// there. With --check, nothing is written, and the script fails if
// src/api/types.ts differs from the checkout's: the committed types must be
// the ones the pinned wasm speaks.
import { execFileSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const args = process.argv.slice(2);
const check = args.includes("--check");
const thema = args.find((a) => !a.startsWith("--"));
if (!thema) {
  console.error("usage: node scripts/sync-thema.mjs <thema-checkout> [--check]");
  process.exit(2);
}
const src = resolve(thema);
for (const f of ["go.mod", "cmd/thema-wasm/main.go", "playground/types.ts"]) {
  if (!existsSync(join(src, f))) {
    console.error(`${src} is not a thema checkout with the playground: no ${f}`);
    process.exit(1);
  }
}

const types = readFileSync(join(src, "playground", "types.ts"), "utf8");
const typesDst = join(root, "src", "api", "types.ts");
if (check) {
  const have = existsSync(typesDst) ? readFileSync(typesDst, "utf8") : "";
  if (have !== types) {
    console.error("src/api/types.ts differs from the pinned thema's playground/types.ts; run npm run sync-thema");
    process.exit(1);
  }
  console.log("src/api/types.ts matches the pinned thema");
  process.exit(0);
}

const run = (cmd, argv, env = {}) =>
  execFileSync(cmd, argv, { cwd: src, env: { ...process.env, ...env }, stdio: ["ignore", "pipe", "inherit"] }).toString().trim();

const pub = join(root, "public");
mkdirSync(pub, { recursive: true });
mkdirSync(dirname(typesDst), { recursive: true });

console.log(`building thema.wasm from ${src}`);
run("go", ["build", "-trimpath", "-ldflags=-s -w", "-o", join(pub, "thema.wasm"), "./cmd/thema-wasm"], {
  GOOS: "js",
  GOARCH: "wasm",
});
const goroot = run("go", ["env", "GOROOT"]);
copyFileSync(join(goroot, "lib", "wasm", "wasm_exec.js"), join(pub, "wasm_exec.js"));
copyFileSync(join(src, "cmd", "thema-wasm", "thema-fs.js"), join(pub, "thema-fs.js"));
writeFileSync(typesDst, types);

let commit = "";
try {
  commit = run("git", ["rev-parse", "HEAD"]);
} catch {
  // Not a git checkout; the manifest just won't say.
}
writeFileSync(join(pub, "MANIFEST.json"), JSON.stringify({ themaCommit: commit }, null, 2) + "\n");
console.log(`synced thema${commit ? " " + commit.slice(0, 12) : ""}`);
