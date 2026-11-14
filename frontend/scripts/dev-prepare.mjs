// `predev`: builds what `electron-forge start` needs (dev daemon, AO Browser
// Bridge extension, macOS wake and dictation helpers, browser automation
// runtime, ACP runtime) by running the same build scripts as before, but skips
// the work a step already did for the same inputs.
//
// Each step is keyed by a hash of its real inputs (sources, lockfiles,
// toolchain versions, platform). Per step:
//   skip    - this checkout's output was built from the same key and is intact;
//   restore - another checkout built the same key: clone it from the cache;
//   build   - run the original build script, then store its output in the cache.
// The cache lives in ~/.ao/build-cache/outputs (AO_BUILD_CACHE_DIR overrides).
//
// AO_DEV_BUILD_CACHE=0 runs every build script in order, exactly as the old
// predev chain did. AO_DEV_DAEMON_REUSE=<path to an ao binary> copies that
// binary in instead of building the daemon (opt-in: only when you know the
// backend is unchanged). --dry-run prints the plan without changing anything.
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
	assertIgnoredOutputs,
	buildCacheRoot,
	cacheKey,
	cloneTree,
	gitSourceFiles,
	hashFiles,
	importClosure,
	log,
	lookupEntry,
	outputFingerprint,
	planStep,
	pruneEntries,
	readJSON,
	stampIsCurrent,
	storeEntry,
	writeJSON,
} from "./dev-cache.mjs";

const scriptsDir = path.dirname(fileURLToPath(import.meta.url));
const frontend = path.resolve(scriptsDir, "..");
const repoRoot = path.resolve(frontend, "..");
const stampFile = path.join(frontend, "node_modules", ".cache", "ao-dev-prepare.json");

// Go settings that change the compiled daemon. Paths are made repo-relative so
// two checkouts of the same source share a key.
const GO_ENV_KEYS = [
	"GOOS",
	"GOARCH",
	"GOFLAGS",
	"GOEXPERIMENT",
	"GOAMD64",
	"GOARM64",
	"CGO_ENABLED",
	"CC",
	"CXX",
	"CGO_CFLAGS",
	"CGO_CPPFLAGS",
	"CGO_CXXFLAGS",
	"CGO_LDFLAGS",
	"GOWORK",
];

export function normalizeGoEnv(goEnv, root) {
	const normalized = {};
	for (const key of GO_ENV_KEYS) {
		const value = goEnv[key] ?? "";
		normalized[key] = key === "GOWORK" && value && value !== "off" ? path.relative(root, value).split(path.sep).join("/") : value;
	}
	return normalized;
}

function capture(command, args, options = {}) {
	const result = spawnSync(command, args, { encoding: "utf8", windowsHide: true, ...options });
	if (result.error || result.status !== 0) return null;
	return `${result.stdout ?? ""}${result.stderr ?? ""}`.trim();
}

const probes = new Map();
function probe(name, fn) {
	if (!probes.has(name)) probes.set(name, fn());
	return probes.get(name);
}

function goToolchain() {
	return probe("go", () => {
		const backend = path.join(repoRoot, "backend");
		const version = capture("go", ["version"], { cwd: backend });
		const env = capture("go", ["env", "-json", ...GO_ENV_KEYS], { cwd: backend });
		if (!version || !env) return null;
		return { version, env: normalizeGoEnv(JSON.parse(env), repoRoot) };
	});
}

function swiftToolchain() {
	return probe("swift", () => capture("xcrun", ["swiftc", "--version"]));
}

function sha(text) {
	return createHash("sha256").update(text).digest("hex");
}

function viteEnv() {
	const entries = Object.entries(process.env)
		.filter(([name]) => name.startsWith("VITE_"))
		.sort(([a], [b]) => a.localeCompare(b));
	return sha(JSON.stringify(entries));
}

const frontendFile = (rel) => `frontend/${rel}`;

// Every predev step, in the order the old chain ran them. `inputs` returns the
// key material, or { uncacheable: reason } to always run the real build.
export function steps({ platform = process.platform, arch = process.arch } = {}) {
	const darwin = platform === "darwin";
	const win = platform === "win32";
	return [
		{
			name: "daemon",
			outDir: "daemon",
			sentinels: [win ? "ao.exe" : "ao"],
			command: ["scripts/build-daemon.mjs", "--dev"],
			inputs() {
				// Windows dev builds write a fresh dev-<time> folder plus a manifest
				// so a running daemon's locked exe is never overwritten.
				if (win) return { uncacheable: "Windows dev daemons use per-build folders" };
				if (existsSync(path.join(repoRoot, "backend", "vendor"))) return { uncacheable: "backend/vendor exists" };
				const files = gitSourceFiles(repoRoot, ["backend", "go.work", "go.work.sum"], {
					ignoredSuffixes: [".go", "/go.mod", "/go.sum"],
				});
				if (!files) return { uncacheable: "not a git checkout" };
				const go = goToolchain();
				if (!go) return { uncacheable: "go is unavailable" };
				const appVersion = JSON.parse(readFileSync(path.join(frontend, "package.json"), "utf8")).version;
				return {
					files: [...files, frontendFile("scripts/build-daemon.mjs"), frontendFile("scripts/go-version.mjs")],
					extra: { go, appVersion, workOSClientID: sha(process.env.VITE_WORKOS_CLIENT_ID?.trim() ?? "") },
				};
			},
		},
		{
			name: "browser-extension",
			outDir: "browser-extension",
			sentinels: ["manifest.json", "background.js", "content.js", "consent.js", "popup.js", "options.js"],
			command: ["scripts/build-browser-extension.mjs"],
			inputs() {
				const own = gitSourceFiles(repoRoot, ["frontend/src/browser-extension"]);
				if (!own) return { uncacheable: "not a git checkout" };
				const entries = own.filter((file) => /\.tsx?$/.test(file) && !/\.test\.tsx?$/.test(file));
				const closure = importClosure(repoRoot, entries);
				let files = [...own, ...closure.files];
				if (closure.unresolved.length > 0) {
					// Could not follow every import: key on all of src instead.
					log(`browser-extension: unresolved imports (${closure.unresolved.slice(0, 3).join(", ")}); keying on all of frontend/src`);
					files = gitSourceFiles(repoRoot, ["frontend/src"]) ?? files;
				}
				return {
					files: [
						...files,
						...["package-lock.json", "tsconfig.json", "scripts/build-browser-extension.mjs"].map(frontendFile),
						// Vite reads these from the frontend root even with configFile: false.
						...[".env", ".env.local", ".env.production", ".env.production.local"].map(frontendFile),
					],
					extra: { node: process.versions.node, viteEnv: viteEnv() },
				};
			},
		},
		darwin && {
			name: "wake-helper",
			outDir: "wake-helper",
			sentinels: ["ao-wake", "THIRD_PARTY_NOTICES.md"],
			command: ["scripts/build-wake-helper.mjs", "--if-supported"],
			inputs() {
				const files = gitSourceFiles(repoRoot, ["frontend/native/wake-helper"]);
				if (!files) return { uncacheable: "not a git checkout" };
				const swift = swiftToolchain();
				if (!swift) return { uncacheable: "swiftc is unavailable" };
				return { files: [...files, frontendFile("scripts/build-wake-helper.mjs")], extra: { swift, arch } };
			},
		},
		darwin && {
			name: "dictation-helper",
			outDir: "dictation-helper",
			sentinels: ["Orchestrator.app/Contents/MacOS/Orchestrator", "Orchestrator.app/Contents/Info.plist"],
			command: ["scripts/build-dictation-helper.mjs", "--if-supported"],
			inputs() {
				const files = gitSourceFiles(repoRoot, ["frontend/native/dictation-helper"]);
				if (!files) return { uncacheable: "not a git checkout" };
				const swift = swiftToolchain();
				if (!swift) return { uncacheable: "swiftc is unavailable" };
				return {
					files: [...files, frontendFile("assets/icon.icns"), frontendFile("scripts/build-dictation-helper.mjs")],
					extra: { swift, arch },
				};
			},
		},
		{
			name: "agent-browser",
			outDir: "agent-browser",
			sentinels: [win ? "agent-browser.exe" : "agent-browser", ".license-version"],
			command: ["scripts/ensure-browser-runtime.mjs"],
			inputs() {
				// The pinned version and checksums live in the script itself.
				return {
					files: ["scripts/prepare-agent-browser.mjs", "scripts/ensure-browser-runtime.mjs"].map(frontendFile),
					extra: {},
				};
			},
		},
		{
			name: "acp-runtime",
			outDir: "resources/acp-runtime",
			sentinels: [
				".ao-acp-runtime.json",
				win ? "node/node.exe" : "node/bin/node",
				"node_modules/@agentclientprotocol/claude-agent-acp/dist/index.js",
			],
			command: ["scripts/build-acp-runtime.mjs"],
			inputs() {
				// The same inputs build-acp-runtime.mjs signs its marker with.
				return {
					files: [
						"acp-runtime/package.json",
						"acp-runtime/package-lock.json",
						"scripts/build-acp-runtime.mjs",
						"scripts/build-acp-runtime-helpers.mjs",
					].map(frontendFile),
					extra: {},
				};
			},
		},
	].filter(Boolean);
}

function stepKey(step, platform, arch) {
	const inputs = step.inputs();
	if (inputs.uncacheable) return { key: null, reason: inputs.uncacheable };
	return {
		key: cacheKey({ step: step.name, platform, arch, files: hashFiles(repoRoot, inputs.files), extra: inputs.extra }),
		reason: "",
	};
}

// Runs a build script with its output prefixed by the step name, so parallel
// builds stay readable.
function runBuild(step) {
	return new Promise((resolve) => {
		const child = spawn(process.execPath, step.command, { cwd: frontend, stdio: ["inherit", "pipe", "pipe"], windowsHide: true });
		const prefix = (stream, target) => {
			let pending = "";
			stream.on("data", (chunk) => {
				pending += chunk;
				const lines = pending.split(/\r?\n/);
				pending = lines.pop() ?? "";
				for (const line of lines) target.write(`[${step.name}] ${line}\n`);
			});
			stream.on("end", () => pending && target.write(`[${step.name}] ${pending}\n`));
		};
		prefix(child.stdout, process.stdout);
		prefix(child.stderr, process.stderr);
		child.on("error", (error) => resolve({ ok: false, detail: error.message }));
		child.on("close", (code) => resolve({ ok: code === 0, detail: code === 0 ? "" : `exited ${code}` }));
	});
}

function runBuildInherited(step) {
	const result = spawnSync(process.execPath, step.command, { cwd: frontend, stdio: "inherit", windowsHide: true });
	return result.status === 0;
}

function reuseDaemon(binary, platform) {
	if (platform === "win32") throw new Error("AO_DEV_DAEMON_REUSE is not supported on Windows");
	const source = path.resolve(binary);
	if (!existsSync(source)) throw new Error(`AO_DEV_DAEMON_REUSE: ${source} does not exist`);
	const outDir = path.join(frontend, "daemon");
	assertIgnoredOutputs(repoRoot, ["frontend/daemon"]);
	rmSync(outDir, { recursive: true, force: true });
	mkdirSync(outDir, { recursive: true });
	copyFileSync(source, path.join(outDir, "ao"));
	chmodSync(path.join(outDir, "ao"), 0o755);
}

export async function prepare({ dryRun = false, env = process.env, platform = process.platform, arch = process.arch } = {}) {
	const started = Date.now();
	const all = steps({ platform, arch });

	if (env.AO_DEV_BUILD_CACHE === "0") {
		log("AO_DEV_BUILD_CACHE=0: running every build step without the cache");
		if (dryRun) return 0;
		for (const step of all) if (!runBuildInherited(step)) return 1;
		return 0;
	}

	const root = buildCacheRoot(env);
	const stamps = readJSON(stampFile, {});
	const results = [];
	const builds = [];
	for (const step of all) {
		const t0 = Date.now();
		const outDir = path.join(frontend, step.outDir);
		const result = { step: step.name, action: "", reason: "", seconds: 0 };
		results.push(result);

		if (step.name === "daemon" && env.AO_DEV_DAEMON_REUSE) {
			result.action = "reuse";
			result.reason = `copied ${env.AO_DEV_DAEMON_REUSE} (AO_DEV_DAEMON_REUSE); backend sources were NOT rebuilt`;
			if (!dryRun) {
				reuseDaemon(env.AO_DEV_DAEMON_REUSE, platform);
				delete stamps[step.name];
			}
			result.seconds = (Date.now() - t0) / 1000;
			continue;
		}

		let key = null;
		let reason = "";
		try {
			({ key, reason } = stepKey(step, platform, arch));
		} catch (error) {
			reason = `could not hash inputs: ${error.message}`;
		}
		const fingerprint = outputFingerprint(outDir, step.sentinels);
		const stampCurrent = stampIsCurrent(stamps[step.name], key, fingerprint);
		const cachedOutput = key && !stampCurrent ? lookupEntry(root, step.name, key) : null;
		const action = planStep({ key, stampCurrent, cachedOutput });
		result.action = action;
		result.reason =
			action === "skip"
				? `output already built from inputs ${key.slice(0, 12)}`
				: action === "restore"
					? `cloned cached output ${key.slice(0, 12)}`
					: key
						? `no cached output for inputs ${key.slice(0, 12)}`
						: `uncached: ${reason}`;
		if (dryRun) continue;

		if (action === "restore") {
			try {
				assertIgnoredOutputs(repoRoot, [path.posix.join("frontend", step.outDir)]);
				rmSync(outDir, { recursive: true, force: true });
				const method = cloneTree(cachedOutput, outDir);
				stamps[step.name] = { key, fingerprint: outputFingerprint(outDir, step.sentinels) };
				result.reason += ` (${method})`;
				result.seconds = (Date.now() - t0) / 1000;
				continue;
			} catch (error) {
				log(`${step.name}: restore failed (${error.message}); building instead`);
				rmSync(outDir, { recursive: true, force: true });
				result.action = "build";
				result.reason = `restore failed: ${error.message}`;
			}
		}
		if (result.action === "build") builds.push({ step, key, outDir, result, t0 });
	}

	if (dryRun) {
		printSummary(results, started, true);
		return 0;
	}

	// Misses build in parallel: they write disjoint output folders.
	const outcomes = await Promise.all(
		builds.map(async ({ step, key, outDir, result }) => {
			const t0 = Date.now();
			const { ok, detail } = await runBuild(step);
			result.seconds = (Date.now() - t0) / 1000;
			if (!ok) {
				result.action = "FAILED";
				result.reason = detail;
				delete stamps[step.name];
				return false;
			}
			const fingerprint = outputFingerprint(outDir, step.sentinels);
			if (key && fingerprint) {
				stamps[step.name] = { key, fingerprint };
				try {
					storeEntry(root, step.name, key, outDir, { outDir: step.outDir });
					pruneEntries(root, step.name);
					result.reason += "; cached for other checkouts";
				} catch (error) {
					log(`${step.name}: could not cache the output (${error.message}); the build itself succeeded`);
				}
			} else {
				delete stamps[step.name];
			}
			return true;
		}),
	);

	try {
		writeJSON(stampFile, stamps);
	} catch (error) {
		log(`could not record build stamps (${error.message}); the next run will re-check every step`);
	}
	printSummary(results, started, false);
	return outcomes.every(Boolean) ? 0 : 1;
}

function printSummary(results, started, dryRun) {
	const width = Math.max(...results.map((r) => r.step.length));
	log(dryRun ? "dry run, nothing changed:" : "summary:");
	for (const r of results) {
		const time = dryRun ? "" : ` ${r.seconds.toFixed(1).padStart(5)}s`;
		console.log(`  ${r.step.padEnd(width)} ${r.action.padEnd(7)}${time}  ${r.reason}`);
	}
	if (!dryRun) log(`predev took ${((Date.now() - started) / 1000).toFixed(1)}s`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
	const args = process.argv.slice(2);
	const unknown = args.filter((arg) => arg !== "--dry-run");
	if (unknown.length > 0) {
		console.error(`Usage: dev-prepare.mjs [--dry-run]  (unknown: ${unknown.join(" ")})`);
		process.exit(2);
	}
	process.exitCode = await prepare({ dryRun: args.includes("--dry-run") });
}
