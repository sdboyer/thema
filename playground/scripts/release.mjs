// Stages and publishes thema's npm packages, for the release workflow and
// for CI's rehearsal of it. Each package is staged in a directory of its
// own, holding only what it publishes, with a manifest derived from the one
// in this tree:
//
//   @sdboyer/thema-ui          components/: the built library (npm run build
//                              --workspace components), and thema's error
//                              pages, docs/errors/*.md, under errors/
//   @sdboyer/thema-wasm        wasm/: thema.wasm and the scripts that run it,
//                              from the playground bundle
//   @sdboyer/thema-playground  ui/: the playground bundle, whole, under dist/
//
// The bundle is what go generate ./cmd/thema/playground writes to
// cmd/thema/playground/dist, the same files the GitHub release attaches. All
// three take the release's version, and publish under the dist-tag it
// implies: latest for a release, next for a prerelease.
//
//   node scripts/release.mjs stage <version> <bundle> <out>
//   node scripts/release.mjs publish <out> [--dry-run]
//
// publish skips a package whose version is already published, so a release
// whose publishing failed part way is finished by running it again.
import { execFileSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// packages are the published packages, in the order publish publishes them,
// each by the directory in playground/ holding its manifest and README.
// bundled says its dependencies are compiled into what it ships, so the
// published manifest declares none.
export const packages = [
  { dir: "components", staged: "thema-ui", bundled: false },
  { dir: "wasm", staged: "thema-wasm", bundled: false },
  { dir: "ui", staged: "thema-playground", bundled: true },
];

const semver = /^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/;

// distTag is the dist-tag a version publishes under.
export function distTag(version) {
  if (!semver.test(version)) {
    throw new Error(`${version} is not a version npm publishes: want X.Y.Z or X.Y.Z-pre, without a leading v`);
  }
  return version.includes("-") ? "next" : "latest";
}

// manifest is the published manifest made from src, a package.json in this
// tree: at version, publishable, without the scripts and devDependencies
// that only build it here, and, for a bundled package, without dependencies.
// thema-ui's source export condition goes too: it names src/, which the
// package does not ship. Its files name NOTICE, which npm leaves out of a
// package unless named, and which the license asks a redistribution to
// carry.
export function manifest(src, version, { bundled }) {
  const m = { ...src, version, files: [...src.files, "NOTICE"] };
  delete m.private;
  delete m.scripts;
  delete m.devDependencies;
  if (bundled) {
    delete m.dependencies;
  }
  if (m.exports?.["."]?.source) {
    const { source: _, ...rest } = m.exports["."];
    m.exports = { ...m.exports, ".": rest };
  }
  return m;
}

// stage writes each package to out/<staged>, replacing out, from the
// repository at root and the playground bundle at bundle. It fails if
// anything a package ships is missing.
export function stage({ version, root, bundle, out }) {
  distTag(version);
  const pg = join(root, "playground");
  rmSync(out, { recursive: true, force: true });
  for (const p of packages) {
    const src = JSON.parse(readFileSync(join(pg, p.dir, "package.json"), "utf8"));
    const dst = join(out, p.staged);
    mkdirSync(dst, { recursive: true });
    writeFileSync(join(dst, "package.json"), JSON.stringify(manifest(src, version, p), null, 2) + "\n");
    copy(join(pg, p.dir, "README.md"), join(dst, "README.md"));
    for (const f of ["LICENSE", "NOTICE"]) {
      copy(join(root, f), join(dst, f));
    }
  }

  const ui = join(out, "thema-ui");
  copy(join(pg, "components", "dist"), join(ui, "dist"), "run npm run build --workspace components first");
  const errors = join(root, "docs", "errors");
  mkdirSync(join(ui, "errors"));
  for (const f of readdirSync(errors).filter((f) => f.endsWith(".md"))) {
    copy(join(errors, f), join(ui, "errors", f));
  }

  const wasm = join(out, "thema-wasm");
  for (const f of JSON.parse(readFileSync(join(pg, "wasm", "package.json"), "utf8")).files) {
    copy(join(bundle, f), join(wasm, f), "is it a playground bundle?");
  }

  copy(bundle, join(out, "thema-playground", "dist"));
  return packages.map((p) => join(out, p.staged));
}

function copy(src, dst, hint) {
  if (!existsSync(src)) {
    throw new Error(`${src} does not exist${hint ? `; ${hint}` : ""}`);
  }
  cpSync(src, dst, { recursive: true });
}

// publish publishes each package staged in out, in the order of packages,
// skipping one whose version is already published. npm runs the npm CLI
// with an argv and returns its stdout, throwing when it fails.
export function publish({ out, dryRun = false, npm = runNpm }) {
  for (const p of packages) {
    const dir = join(out, p.staged);
    const { name, version } = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));
    if (published(npm, name, version)) {
      console.log(`${name}@${version} is already published`);
      continue;
    }
    const argv = ["publish", dir, "--access", "public", "--tag", distTag(version)];
    if (dryRun) {
      argv.push("--dry-run");
    }
    console.log(`npm ${argv.join(" ")}`);
    npm(argv);
  }
}

// published reports whether name@version is on the registry. npm view
// fails, E404, for a version or a package that does not exist. Any failure
// counts as not published: publishing a version that is fails rather than
// replaces it, and the job is run again.
function published(npm, name, version) {
  try {
    return npm(["view", `${name}@${version}`, "version"]).trim() === version;
  } catch {
    return false;
  }
}

// runNpm runs npm, its stderr shown except for npm view's: a version not yet
// published is what view finds most often, and npm reports it as an error.
function runNpm(argv) {
  const stderr = argv[0] === "view" ? "ignore" : "inherit";
  return execFileSync("npm", argv, { stdio: ["ignore", "pipe", stderr] }).toString();
}

function main(argv) {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
  const [cmd, ...args] = argv;
  if (cmd === "stage" && args.length === 3) {
    const [version, bundle, out] = args;
    for (const dir of stage({ version, root, bundle: resolve(bundle), out: resolve(out) })) {
      console.log(`staged ${dir}`);
    }
  } else if (cmd === "publish" && (args.length === 1 || (args.length === 2 && args[1] === "--dry-run"))) {
    publish({ out: resolve(args[0]), dryRun: args.length === 2 });
  } else {
    console.error("usage: release.mjs stage <version> <bundle> <out>\n       release.mjs publish <out> [--dry-run]");
    process.exit(2);
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2));
}
