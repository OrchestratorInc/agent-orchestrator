import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
// Node-API is ABI-stable, so the headers of the Node running this build work for
// Electron's embedded Node too.
const nodeInclude = path.resolve(process.execPath, "../../include/node");
export function scrollPhaseBuildOptions(arch, root = frontend, include = nodeInclude) {
  const cpu = arch === "arm64" ? "arm64" : arch === "x64" ? "x86_64" : undefined;
  if (!cpu) throw new Error(`Unsupported macOS scroll phase addon architecture: ${arch}`);
  return {
    output: path.join(root, "scroll-phase", "ao-scroll-phase.node"),
    args: ["clang++", "-target", `${cpu}-apple-macosx11.0`, "-std=c++17", "-fobjc-arc", "-O2",
      "-bundle", "-undefined", "dynamic_lookup", "-framework", "AppKit", "-I", include,
      path.join(root, "native/scroll-phase/scroll_phase.mm")],
  };
}
export function buildScrollPhase({ arch = process.arch, platform = process.platform, run = spawnSync, ensureDirectory = mkdirSync, include = nodeInclude } = {}) {
  if (platform !== "darwin") throw new Error("The scroll phase addon must be built on macOS");
  if (!existsSync(path.join(include, "node_api.h"))) throw new Error(`Node-API headers not found in ${include}`);
  const { output, args } = scrollPhaseBuildOptions(arch, frontend, include);
  ensureDirectory(path.dirname(output), { recursive: true });
  const result = run("xcrun", [...args, "-o", output], { stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`macOS scroll phase addon compilation failed (${result.status})`);
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  if (args.length === 1 && args[0] === "--if-supported") {
    // Swipe navigation falls back to wheel timing without the addon, so a dev
    // machine whose Node lacks headers should still start.
    if (process.platform === "darwin") {
      try {
        buildScrollPhase();
      } catch (error) {
        console.warn(`Skipping scroll phase addon: ${error.message}`);
      }
    }
    process.exit(0);
  }
  if (args.length !== 0 && (args.length !== 2 || args[0] !== "--arch")) throw new Error("Usage: build-scroll-phase.mjs [--arch arm64|x64]");
  buildScrollPhase({ arch: args[1] ?? process.arch });
}
