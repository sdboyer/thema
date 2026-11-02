import preact from "@preact/preset-vite";
import { defineConfig } from "vitest/config";

// The bundle is served from wherever it is unpacked: GitHub Pages under the
// repository's path, or the root of `thema serve playground`. Every URL is
// relative.
export default defineConfig({
  base: "./",
  plugins: [preact()],
  // The editor components come from the workspace's own source, so the
  // playground always runs exactly what the package ships.
  resolve: {
    conditions: ["source"],
  },
  build: {
    target: "es2022",
  },
  test: {
    environment: "jsdom",
    setupFiles: ["../components/test/setup.ts"],
  },
});
