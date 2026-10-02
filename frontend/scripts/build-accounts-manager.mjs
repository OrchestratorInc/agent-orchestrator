import {
	copyFileSync,
	mkdirSync,
	readdirSync,
	readFileSync,
	rmSync,
	statSync,
	writeFileSync,
} from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import { meetsMinimumVersion, parseGoVersion, parseMinimumGoVersion } from "./go-version.mjs";
import { resolveAccountsManagerDependency, verifyAccountsManagerBinary } from "./accounts-manager-dependency.mjs";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptsDir, "..");
const repoRoot = resolve(frontendRoot, "..");
const runnerRoot = join(repoRoot, "accounts-manager", "runner");
const outDir = join(frontendRoot, "accounts-manager");
const binaryName = process.platform === "win32" ? "ao-accounts-manager.exe" : "ao-accounts-manager";
const outPath = join(outDir, binaryName);
const isWindowsDev = process.platform === "win32" && process.argv.includes("--dev");
const windowsDevOutDir = join(outDir, `dev-${Date.now()}-${process.pid}`);
const buildOutPath = isWindowsDev ? join(windowsDevOutDir, binaryName) : outPath;
const windowsDevManifestPath = join(outDir, "dev-accounts-manager.json");
const minimumGoVersion = parseMinimumGoVersion(readFileSync(join(runnerRoot, "go.mod"), "utf8"));
const packageVersion = JSON.parse(readFileSync(join(frontendRoot, "package.json"), "utf8")).version ?? "dev";

if (!minimumGoVersion) {
	console.error("Could not determine the required Go version from accounts-manager/runner/go.mod.");
	process.exit(1);
}

const versionResult = spawnSync("go", ["version"], { encoding: "utf8", windowsHide: true });
const actualGoVersion = versionResult.error ? null : parseGoVersion(versionResult.stdout);
if (versionResult.error || versionResult.status !== 0 || !actualGoVersion || !meetsMinimumVersion(actualGoVersion, minimumGoVersion)) {
	const found = actualGoVersion ? actualGoVersion.join(".") : versionResult.stdout?.trim() || "unknown";
	console.error(`Go ${minimumGoVersion.join(".")}+ required for Accounts Manager, found ${found}`);
	process.exit(1);
}

const dependency = resolveAccountsManagerDependency(repoRoot);

if (isWindowsDev) {
	mkdirSync(windowsDevOutDir, { recursive: true });
} else if (process.platform === "win32") {
	mkdirSync(outDir, { recursive: true });
	rmSync(outPath, { force: true });
	rmSync(windowsDevManifestPath, { force: true });
	cleanupOldWindowsDevBuilds(undefined);
} else {
	rmSync(outDir, { recursive: true, force: true });
	mkdirSync(outDir, { recursive: true });
}

const versionSymbol = "github.com/aoagents/agent-orchestrator/accounts-manager/runner/internal/runner.Version";
const result = spawnSync(
	"go",
	["build", "-mod=readonly", "-ldflags", `-X ${versionSymbol}=${packageVersion}`, "-o", buildOutPath, "./cmd/ao-accounts-manager"],
	{ cwd: runnerRoot, stdio: "inherit", windowsHide: true, env: { ...process.env, GOWORK: "off" } },
);
if (result.error) {
	console.error(`failed to start Accounts Manager build: ${result.error.message}`);
	process.exit(1);
}
if (result.status !== 0) process.exit(result.status ?? 1);

verifyAccountsManagerBinary(buildOutPath, repoRoot, dependency.pin);

const verify = spawnSync(buildOutPath, ["version"], { encoding: "utf8", windowsHide: true });
if (verify.status !== 0 || !verify.stdout.includes("ao-accounts-manager") || !verify.stdout.includes("CLIProxyAPI v7.3.8")) {
	console.error(`Accounts Manager version verification failed: ${verify.stderr || verify.stdout}`);
	process.exit(1);
}

copyFileSync(dependency.licensePath, join(outDir, "CLIProxyAPI-LICENSE"));
copyFileSync(join(repoRoot, "accounts-manager", "UPSTREAM.md"), join(outDir, "UPSTREAM.md"));
copyFileSync(dependency.recordPath, join(outDir, "dependency.json"));

if (isWindowsDev) {
	writeFileSync(windowsDevManifestPath, `${JSON.stringify({ path: buildOutPath }, null, 2)}\n`);
	cleanupOldWindowsDevBuilds(buildOutPath);
}

function cleanupOldWindowsDevBuilds(activePath) {
	const activeDir = activePath ? dirname(activePath) : "";
	let entries;
	try {
		entries = readdirSync(outDir, { withFileTypes: true })
			.filter((entry) => entry.isDirectory() && entry.name.startsWith("dev-"))
			.map((entry) => {
				const dir = join(outDir, entry.name);
				return { dir, mtimeMs: statSync(dir).mtimeMs };
			})
			.sort((a, b) => b.mtimeMs - a.mtimeMs);
	} catch {
		return;
	}
	for (const entry of entries.slice(activePath ? 5 : 0)) {
		if (entry.dir === activeDir) continue;
		try {
			rmSync(entry.dir, { recursive: true, force: true });
		} catch {
			// A running Windows dev runner may still own this executable.
		}
	}
}
