import assert from "node:assert/strict";
import { cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { after, test } from "node:test";
import { fileURLToPath } from "node:url";

import { distTag, packages, publish, stage } from "./release.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");

// bundleFiles is what go generate ./cmd/thema/playground writes, as far as
// these tests need.
const bundleFiles = [
  "index.html",
  "assets/index.js",
  "thema-worker.js",
  "thema.wasm",
  "thema.wasm.gz",
  "wasm_exec.js",
  "thema-fs.js",
  "types.ts",
  "MANIFEST.json",
];

// fixture is a copy of what stage reads from this tree, its manifests and
// READMEs as they are here, with a built components package and a bundle
// standing in for the real ones.
const temps = [];
after(() => {
  for (const d of temps) rmSync(d, { recursive: true, force: true });
});

function fixture() {
  const dir = mkdtempSync(join(tmpdir(), "thema-release-"));
  temps.push(dir);
  const fake = join(dir, "repo");
  for (const f of ["LICENSE", "NOTICE"]) {
    cpSync(join(root, f), join(fake, f));
  }
  for (const p of packages) {
    for (const f of ["package.json", "README.md"]) {
      cpSync(join(root, "playground", p.dir, f), join(fake, "playground", p.dir, f));
    }
  }
  write(join(fake, "playground", "components", "dist", "index.js"), "export {};\n");
  write(join(fake, "docs", "errors", "E0101.md"), "# E0101\n");
  write(join(fake, "docs", "errors", "README.md"), "# Diagnostic codes\n");
  write(join(fake, "docs", "errors", "notes.txt"), "not a page\n");
  const bundle = join(dir, "bundle");
  for (const f of bundleFiles) {
    write(join(bundle, f), f);
  }
  return { root: fake, bundle, out: join(dir, "out") };
}

function write(path, text) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, text);
}

function files(dir, prefix = "") {
  return readdirSync(dir, { withFileTypes: true })
    .flatMap((e) => (e.isDirectory() ? files(join(dir, e.name), `${prefix}${e.name}/`) : [`${prefix}${e.name}`]))
    .sort();
}

function manifestOf(out, staged) {
  return JSON.parse(readFileSync(join(out, staged, "package.json"), "utf8"));
}

test("a release publishes as latest, and any prerelease as next", () => {
  assert.equal(distTag("0.1.0"), "latest");
  assert.equal(distTag("0.1.0-rc.1"), "next");
  assert.equal(distTag("0.0.0-rehearsal"), "next");
  for (const v of ["v0.1.0", "0.1", "0.1.0-", ""]) {
    assert.throws(() => distTag(v), /not a version npm publishes/, v);
  }
});

test("each package stages exactly what it ships", () => {
  const f = fixture();
  stage({ version: "0.1.0-rc.1", ...f });
  const common = ["LICENSE", "NOTICE", "README.md", "package.json"];
  assert.deepEqual(files(join(f.out, "thema-ui")), [...common, "dist/index.js", "errors/E0101.md", "errors/README.md"].sort());
  assert.deepEqual(
    files(join(f.out, "thema-wasm")),
    [...common, "MANIFEST.json", "thema-fs.js", "thema-worker.js", "thema.wasm", "thema.wasm.gz", "wasm_exec.js"].sort(),
  );
  assert.deepEqual(files(join(f.out, "thema-playground")), [...common, ...bundleFiles.map((b) => `dist/${b}`)].sort());
});

test("each manifest is publishable, at the version, from this repository", () => {
  const f = fixture();
  stage({ version: "0.1.0", ...f });
  const names = [];
  for (const p of packages) {
    const m = manifestOf(f.out, p.staged);
    names.push(m.name);
    assert.equal(m.version, "0.1.0", p.staged);
    assert.equal(m.private, undefined, p.staged);
    assert.equal(m.scripts, undefined, p.staged);
    assert.equal(m.devDependencies, undefined, p.staged);
    // Trusted publishing requires the repository to be this one, exactly.
    assert.equal(m.repository?.url, "git+https://github.com/sdboyer/thema.git", p.staged);
    assert.equal(m.repository?.directory, `playground/${p.dir}`, p.staged);
    assert.equal(m.license, "Apache-2.0", p.staged);
    // npm packs LICENSE whatever files says, and NOTICE only if named.
    assert.ok(m.files.includes("NOTICE"), p.staged);
  }
  assert.deepEqual(names, ["@sdboyer/thema-ui", "@sdboyer/thema-wasm", "@sdboyer/thema-playground"]);
});

test("the playground declares no dependencies: they are bundled", () => {
  const f = fixture();
  stage({ version: "0.1.0", ...f });
  assert.equal(manifestOf(f.out, "thema-playground").dependencies, undefined);
  assert.ok(manifestOf(f.out, "thema-ui").peerDependencies?.preact);
});

test("thema-ui's exports name only what it ships", () => {
  const f = fixture();
  stage({ version: "0.1.0", ...f });
  const m = manifestOf(f.out, "thema-ui");
  assert.deepEqual(m.exports, {
    ".": { types: "./dist/index.d.ts", default: "./dist/index.js" },
    "./errors/*": "./errors/*",
  });
});

test("staging fails when the library is not built", () => {
  const f = fixture();
  rmSync(join(f.root, "playground", "components", "dist"), { recursive: true });
  assert.throws(() => stage({ version: "0.1.0", ...f }), /npm run build --workspace components/);
});

test("staging fails when the bundle lacks a file a package ships", () => {
  const f = fixture();
  rmSync(join(f.bundle, "thema-worker.js"));
  assert.throws(() => stage({ version: "0.1.0", ...f }), /thema-worker\.js does not exist/);
});

test("staging refuses a tag where a version belongs", () => {
  const f = fixture();
  assert.throws(() => stage({ version: "v0.1.0", ...f }), /without a leading v/);
  assert.equal(existsSync(f.out), false);
});

// fakeNpm answers npm view from the versions in registry, and records every
// call.
function fakeNpm(registry) {
  const calls = [];
  const npm = (argv) => {
    calls.push(argv);
    if (argv[0] === "view") {
      const [, spec] = argv;
      const at = spec.lastIndexOf("@");
      const name = spec.slice(0, at);
      if (!(name in registry)) throw new Error("E404");
      return registry[name].includes(spec.slice(at + 1)) ? spec.slice(at + 1) + "\n" : "";
    }
    return "";
  };
  return { npm, calls };
}

function publishes(calls) {
  return calls.filter((c) => c[0] === "publish").map((c) => [c[1].split("/").pop(), ...c.slice(2)]);
}

test("publish publishes every package, in order, under the version's dist-tag", () => {
  const f = fixture();
  stage({ version: "0.1.0-rc.1", ...f });
  const { npm, calls } = fakeNpm({ "@sdboyer/thema-ui": ["0.0.0-bootstrap.0"] });
  publish({ out: f.out, npm });
  assert.deepEqual(publishes(calls), [
    ["thema-ui", "--access", "public", "--tag", "next"],
    ["thema-wasm", "--access", "public", "--tag", "next"],
    ["thema-playground", "--access", "public", "--tag", "next"],
  ]);
});

test("publish skips what is already published, so a failed release is finished by running it again", () => {
  const f = fixture();
  stage({ version: "0.1.0", ...f });
  const { npm, calls } = fakeNpm({ "@sdboyer/thema-ui": ["0.1.0"], "@sdboyer/thema-wasm": ["0.1.0-rc.1"] });
  publish({ out: f.out, npm });
  assert.deepEqual(publishes(calls), [
    ["thema-wasm", "--access", "public", "--tag", "latest"],
    ["thema-playground", "--access", "public", "--tag", "latest"],
  ]);
});

test("a dry run publishes nothing", () => {
  const f = fixture();
  stage({ version: "0.1.0", ...f });
  const { npm, calls } = fakeNpm({});
  publish({ out: f.out, npm, dryRun: true });
  const pubs = publishes(calls);
  assert.equal(pubs.length, 3);
  for (const p of pubs) {
    assert.equal(p.at(-1), "--dry-run");
  }
});
