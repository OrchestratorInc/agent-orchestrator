import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, rmSync } from "node:fs";
import { homedir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

/** Where the built helper's executable lives, inside its app bundle. */
export const DICTATION_HELPER_EXECUTABLE = path.join("dictation-helper", "Orchestrator.app", "Contents", "MacOS", "Orchestrator");

// An app bundle, not a bare binary, so macOS names it Orchestrator and shows
// the app icon when it asks for microphone and speech recognition access.
export function buildDictationHelper({ arch = process.arch } = {}) {
  if (process.platform !== "darwin") throw new Error("The dictation helper must be built on macOS");
  const cpu = arch === "arm64" ? "arm64" : arch === "x64" ? "x86_64" : undefined;
  if (!cpu) throw new Error(`Unsupported macOS dictation helper architecture: ${arch}`);
  const source = path.join(frontend, "native/dictation-helper");
  const output = path.join(frontend, "dictation-helper");
  const bundle = path.join(output, "Orchestrator.app");
  const cache = path.join(homedir(), ".ao/build-cache/dictation-helper", arch);
  rmSync(output, { recursive: true, force: true });
  mkdirSync(path.join(bundle, "Contents", "MacOS"), { recursive: true });
  mkdirSync(path.join(bundle, "Contents", "Resources"), { recursive: true });
  mkdirSync(cache, { recursive: true });
  copyFileSync(path.join(source, "Info.plist"), path.join(bundle, "Contents", "Info.plist"));
  copyFileSync(path.join(frontend, "assets", "icon.icns"), path.join(bundle, "Contents", "Resources", "icon.icns"));
  const compile = spawnSync("xcrun", [
    "swiftc", "-target", `${cpu}-apple-macosx11.0`, "-O", path.join(source, "main.swift"),
    "-module-cache-path", cache, "-o", path.join(frontend, DICTATION_HELPER_EXECUTABLE),
  ], { stdio: "inherit" });
  if (compile.error) throw compile.error;
  if (compile.status !== 0) throw new Error(`macOS dictation helper compilation failed (${compile.status})`);
  // Ad-hoc seal the bundle so its Info.plist and icon are part of its identity;
  // release signing re-signs it with the app.
  const seal = spawnSync("codesign", ["--force", "--sign", "-", bundle], { stdio: "inherit" });
  if (seal.error) throw seal.error;
  if (seal.status !== 0) throw new Error(`macOS dictation helper signing failed (${seal.status})`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  if (args.length === 1 && args[0] === "--if-supported") {
    if (process.platform === "darwin") buildDictationHelper();
    process.exit(0);
  }
  if (args.length !== 0 && (args.length !== 2 || args[0] !== "--arch")) throw new Error("Usage: build-dictation-helper.mjs [--arch arm64|x64]");
  buildDictationHelper({ arch: args[1] ?? process.arch });
}
