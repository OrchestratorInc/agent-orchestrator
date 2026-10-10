// @vitest-environment node
import { afterEach, describe, expect, it } from "vitest";
import * as fs from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { runInNewContext } from "node:vm";
import { meetsMinimumVersion, parseGoVersion, parseMinimumGoVersion } from "./go-version.mjs";

const source = fs.readFileSync(new URL("./build-daemon.mjs", import.meta.url), "utf8");
const roots = [];
afterEach(() => {
 for (const root of roots.splice(0)) fs.rmSync(root, { recursive: true, force: true });
});
function runBuild({ platform = "darwin", dev = false, version = "go version go1.27.1 darwin/arm64", failure = "", helperStatus = 1, existing = false } = {}) {
 const root = fs.mkdtempSync(join(tmpdir(), "ao-account-build-"));
 roots.push(root);
 const frontend = join(root, "frontend");
 const scripts = join(frontend, "scripts");
 const backend = join(root, "backend");
 const helper = join(root, "proxy-host");
 for (const dir of [scripts, backend, helper]) fs.mkdirSync(dir, { recursive: true });
 fs.writeFileSync(join(backend, "go.mod"), "module ao.test/backend\n\ngo 1.27.1\n");
 fs.writeFileSync(join(helper, "go.mod"), "module ao.test/helper\n\ngo 1.27.1\n");
 const license = "Upstream test license retained verbatim\n";
 fs.writeFileSync(join(helper, "CLIProxyAPI-LICENSE"), license);
 const out = join(frontend, "daemon");
 if (existing) {
  fs.mkdirSync(out, { recursive: true });
  fs.writeFileSync(join(out, "unrelated-stale-output"), "old");
 }
 const calls = [];
 const messages = [];
 let status = 0;
 const exitSignal = {};
 const runSource = source.replace(/^import .*;\n/gm, "").replaceAll("import.meta.url", JSON.stringify(pathToFileURL(join(scripts, "build-daemon.mjs")).href));
 const environment = { GOWORK: join(root, "go.work"), AO_DATA_DIR: join(root, "scratch-data") };
 try {
  runInNewContext(runSource, {
   ...fs,
   dirname, join, resolve, fileURLToPath,
   meetsMinimumVersion, parseGoVersion, parseMinimumGoVersion,
   console: { error: value => messages.push(String(value)) },
   process: { platform, pid: 4321, argv: dev ? ["node", "build-daemon.mjs", "--dev"] : ["node", "build-daemon.mjs"], env: environment, exit: code => { status = code; throw exitSignal; } },
   spawnSync: (command, args, options) => {
    calls.push({ command, args: Array.from(args), options });
    if (args[0] === "version") {
     if (failure === "version-start") return { error: new Error("go missing") };
     return { status: 0, stdout: version };
    }
    const isHelper = options.cwd === helper;
    if (failure === "backend-start" && !isHelper) return { error: new Error("backend could not start") };
    if (failure === "backend-status" && !isHelper) return { status: 2 };
    if (failure === "helper-start" && isHelper) return { error: new Error("helper could not start") };
    if (failure === "helper-status" && isHelper) return { status: helperStatus };
    const path = args[args.indexOf("-o") + 1];
    fs.writeFileSync(path, isHelper ? "helper fixture" : "daemon fixture");
    return { status: 0 };
   },
  }, { timeout: 2000, filename: "build-daemon.mjs" });
 } catch (error) {
  if (error !== exitSignal) throw error;
 }
 return { root, frontend, backend, helper, out, calls, messages, status, license, environment };
}

describe("desktop account helper packaging", () => {
 it.each(["darwin", "linux", "win32"])("bundles daemon, helper, and license together on %s", platform => {
  const result = runBuild({ platform });
  expect(result.status).toBe(0);
  const suffix = platform === "win32" ? ".exe" : "";
  expect(fs.readFileSync(join(result.out, `ao${suffix}`), "utf8")).toBe("daemon fixture");
  expect(fs.readFileSync(join(result.out, `ao-proxy-host${suffix}`), "utf8")).toBe("helper fixture");
  expect(fs.readFileSync(join(result.out, "CLIProxyAPI-LICENSE"), "utf8")).toBe(result.license);
  expect(result.calls.map(call => call.args[0])).toEqual(["version", "build", "build"]);
  expect(result.calls[1].options.cwd).toBe(result.backend);
  expect(result.calls[2].options.cwd).toBe(result.helper);
  expect(result.calls[1].args.at(-1)).toBe("./cmd/ao");
  expect(result.calls[2].args.at(-1)).toBe("./cmd/ao-proxy-host");
  expect(result.messages).toEqual([]);
 });
 it("builds the pinned SDK in its separate module with the workspace disabled", () => {
  const result = runBuild();
  const backend = result.calls[1];
  const helper = result.calls[2];
  expect(backend.options.env).toBeUndefined();
  expect(helper.options.env.GOWORK).toBe("off");
  expect(helper.options.env.AO_DATA_DIR).toBe(result.environment.AO_DATA_DIR);
  expect(result.environment.GOWORK).toBe(join(result.root, "go.work"));
  expect(helper.options.cwd).not.toBe(backend.options.cwd);
  expect(helper.args).not.toContain("github.com/router-for-me/CLIProxyAPI");
  expect(fs.existsSync(result.environment.AO_DATA_DIR)).toBe(false);
 });
 it("places both Windows dev executables in the same unique launch directory", () => {
  const result = runBuild({ platform: "win32", dev: true });
  const manifest = JSON.parse(fs.readFileSync(join(result.out, "dev-daemon.json"), "utf8"));
  const buildDirectory = dirname(manifest.path);
  expect(buildDirectory).not.toBe(result.out);
  expect(buildDirectory).toMatch(/dev-\d+-4321$/);
  expect(fs.readFileSync(manifest.path, "utf8")).toBe("daemon fixture");
  expect(fs.readFileSync(join(buildDirectory, "ao-proxy-host.exe"), "utf8")).toBe("helper fixture");
  expect(fs.readFileSync(join(buildDirectory, "CLIProxyAPI-LICENSE"), "utf8")).toBe(result.license);
  expect(result.calls[1].args[2]).toBe(manifest.path);
  expect(dirname(result.calls[2].args[2])).toBe(buildDirectory);
 });
 it("uses the regular sibling helper path for Unix development", () => {
  const result = runBuild({ dev: true });
  expect(fs.existsSync(join(result.out, "dev-daemon.json"))).toBe(false);
  expect(result.calls[1].args[2]).toBe(join(result.out, "ao"));
  expect(result.calls[2].args[2]).toBe(join(result.out, "ao-proxy-host"));
 });
 it("removes stale Unix output before producing the complete new pair", () => {
  const result = runBuild({ existing: true });
  expect(fs.existsSync(join(result.out, "unrelated-stale-output"))).toBe(false);
  expect(fs.readdirSync(result.out).sort()).toEqual(["CLIProxyAPI-LICENSE", "ao", "ao-proxy-host"]);
 });
 it.each(["version-start", "backend-start", "backend-status", "helper-start", "helper-status"])("fails the build when %s fails", failure => {
  const result = runBuild({ failure, platform: "win32", dev: true });
  expect(result.status).not.toBe(0);
  expect(fs.existsSync(join(result.out, "dev-daemon.json"))).toBe(false);
  if (failure === "version-start") expect(result.calls).toHaveLength(1);
  else if (failure.startsWith("backend")) expect(result.calls).toHaveLength(2);
  else expect(result.calls).toHaveLength(3);
  const licensePaths = fs.existsSync(result.out) ? fs.readdirSync(result.out).filter(name => name === "CLIProxyAPI-LICENSE") : [];
  expect(licensePaths).toEqual([]);
 });
 it.each(["go version go1.26.4 darwin/arm64", "go version go1.27rc1 darwin/arm64", "unparseable"])("rejects an unsupported Go runtime: %s", version => {
  const result = runBuild({ version });
  expect(result.status).toBe(1);
  expect(result.calls).toHaveLength(1);
  expect(result.messages.join(" ")).toContain("Go 1.27.1+ required");
  expect(fs.existsSync(result.out)).toBe(false);
 });
 it("preserves helper build exit status so packaging cannot report a partial success", () => {
  const result = runBuild({ failure: "helper-status", helperStatus: 7 });
  expect(result.status).toBe(7);
  expect(result.messages).toContain("account helper build failed");
  expect(fs.existsSync(join(result.out, "ao"))).toBe(true);
  expect(fs.existsSync(join(result.out, "ao-proxy-host"))).toBe(false);
  expect(fs.existsSync(join(result.out, "CLIProxyAPI-LICENSE"))).toBe(false);
 });
 it("uses hidden console mode for each Windows build command", () => {
  const result = runBuild({ platform: "win32" });
  expect(result.calls).toHaveLength(3);
  for (const call of result.calls) {
   expect(call.command).toBe("go");
   expect(call.options.windowsHide).toBe(true);
  }
  expect(result.calls[1].options.stdio).toBe("inherit");
  expect(result.calls[2].options.stdio).toBe("inherit");
 });
});
