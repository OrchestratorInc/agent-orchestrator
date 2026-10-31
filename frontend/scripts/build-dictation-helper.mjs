import { spawnSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { homedir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

export function buildDictationHelper({ arch = process.arch } = {}) {
  if (process.platform !== "darwin") throw new Error("The dictation helper must be built on macOS");
  const cpu = arch === "arm64" ? "arm64" : arch === "x64" ? "x86_64" : undefined;
  if (!cpu) throw new Error(`Unsupported macOS dictation helper architecture: ${arch}`);
  const source = path.join(frontend, "native/dictation-helper");
  const output = path.join(frontend, "dictation-helper", "ao-dictate");
  const cache = path.join(homedir(), ".ao/build-cache/dictation-helper", arch);
  mkdirSync(path.dirname(output), { recursive: true });
  mkdirSync(cache, { recursive: true });
  const result = spawnSync("xcrun", [
    "swiftc", "-target", `${cpu}-apple-macosx11.0`, "-O", path.join(source, "main.swift"),
    // Speech and microphone access read their usage strings from the embedded plist.
    "-Xlinker", "-sectcreate", "-Xlinker", "__TEXT", "-Xlinker", "__info_plist", "-Xlinker", path.join(source, "Info.plist"),
    "-module-cache-path", cache, "-o", output,
  ], { stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`macOS dictation helper compilation failed (${result.status})`);
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
