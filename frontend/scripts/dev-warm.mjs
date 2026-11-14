// `npm run dev:warm`: make a fresh checkout ready for `npm run dev` in seconds.
//
// 1. frontend/node_modules: if a previous checkout ran a real `npm ci` with a
//    byte-identical package-lock.json (same Node, npm, platform), clone that
//    install (APFS clonefile on macOS, reflink on Linux, plain copy elsewhere).
//    Otherwise run a real `npm ci` here and store a clone of the result in
//    ~/.ao/warm (AO_WARM_DIR overrides) for the next checkout. A stored install
//    is never modified afterwards, and is never written from a launched app:
//    it is captured straight after `npm ci`, before Vite or Electron touch it.
// 2. Runs scripts/dev-prepare.mjs (the predev steps, content-keyed and cached).
//
// Only git-ignored paths are written; dev:warm aborts if a target is tracked
// and verifies `git status` is unchanged at the end. Any clone failure falls
// back to the normal slow path (`npm ci`, real builds).
//
// Usage: node scripts/dev-warm.mjs [--worktree <path>] [--dry-run]
//          [--skip-prepare] [--replace] [--reuse-daemon <path to ao>]
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readFileSync, realpathSync, rmSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
	absoluteSymlinks,
	assertIgnoredOutputs,
	cacheKey,
	cloneStrategies,
	cloneTree,
	fileIsIdentical,
	isDirectory,
	log,
	lookupEntry,
	planDeps,
	pruneEntries,
	readJSON,
	runGit,
	storeEntry,
	warmStoreRoot,
	writeJSON,
} from "./dev-cache.mjs";

const STORE_NAME = "node_modules";
const INSTALL_STAMP = ".ao-warm.json";
// Per-checkout caches inside node_modules that must never travel between
// checkouts (Vite's optimizer cache resolves React by absolute path).
const NEVER_SHARED = [".vite", ".cache", ".vite-temp"];
const DEPENDENCY_FIELDS = ["dependencies", "devDependencies", "optionalDependencies", "peerDependencies", "overrides", "bundleDependencies"];

export function parseArgs(argv) {
	const options = { worktree: null, dryRun: false, skipPrepare: false, replace: false, reuseDaemon: null };
	for (let i = 0; i < argv.length; i += 1) {
		const arg = argv[i];
		if (arg === "--dry-run") options.dryRun = true;
		else if (arg === "--skip-prepare") options.skipPrepare = true;
		else if (arg === "--replace") options.replace = true;
		else if (arg === "--worktree" || arg === "--reuse-daemon") {
			const value = argv[i + 1];
			if (!value || value.startsWith("--")) throw new Error(`${arg} needs a path`);
			options[arg === "--worktree" ? "worktree" : "reuseDaemon"] = value;
			i += 1;
		} else throw new Error(`unknown argument ${arg}`);
	}
	return options;
}

// The part of package.json that decides what `npm ci` installs.
export function dependencyFields(packageJSON) {
	const picked = {};
	for (const field of DEPENDENCY_FIELDS) if (packageJSON[field] !== undefined) picked[field] = packageJSON[field];
	return picked;
}

function sha(buffer) {
	return createHash("sha256").update(buffer).digest("hex");
}

function npmCommand() {
	return process.platform === "win32" ? "npm.cmd" : "npm";
}

function capture(command, args, options = {}) {
	const result = spawnSync(command, args, { encoding: "utf8", windowsHide: true, shell: process.platform === "win32", ...options });
	return result.error || result.status !== 0 ? null : result.stdout.trim();
}

// Key for an install: the lockfile, the dependency fields of package.json and
// of each `file:` package it links, and the Node/npm/platform that ran npm ci.
export function depsKeyParts({ lockfile, packageJSON, linkedPackageJSONs, node, npm, platform, arch }) {
	return {
		store: STORE_NAME,
		lockfile: sha(lockfile),
		packageJSON: dependencyFields(packageJSON),
		linked: Object.fromEntries(Object.entries(linkedPackageJSONs).map(([rel, json]) => [rel, dependencyFields(json)])),
		node,
		npm,
		platform,
		arch,
	};
}

function linkedPackages(frontend, packageJSON) {
	const linked = {};
	for (const field of ["dependencies", "devDependencies", "optionalDependencies"]) {
		for (const spec of Object.values(packageJSON[field] ?? {})) {
			if (typeof spec !== "string" || !spec.startsWith("file:")) continue;
			const rel = spec.slice("file:".length);
			linked[rel] = readJSON(path.join(frontend, rel, "package.json"), {});
		}
	}
	return linked;
}

function seconds(t0) {
	return `${((Date.now() - t0) / 1000).toFixed(1)}s`;
}

function gitStatus(root) {
	return runGit(root, ["status", "--porcelain=v1", "--untracked-files=all"]);
}

function resolveWorktree(requested) {
	const start = requested ? path.resolve(requested) : path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
	const top = runGit(start, ["rev-parse", "--show-toplevel"])?.trim();
	if (!top) throw new Error(`${start} is not inside a git checkout`);
	const root = realpathSync(top);
	const frontend = path.join(root, "frontend");
	for (const file of ["package.json", "package-lock.json"]) {
		if (!existsSync(path.join(frontend, file))) throw new Error(`${frontend} has no ${file}`);
	}
	return { root, frontend };
}

function npmCi(frontend) {
	log("running npm ci (real install)");
	const result = spawnSync(npmCommand(), ["ci"], { cwd: frontend, stdio: "inherit", windowsHide: true, shell: process.platform === "win32" });
	if (result.error || result.status !== 0) throw new Error(`npm ci failed (${result.error?.message ?? `exit ${result.status}`})`);
}

function seedStore(store, key, frontend, nodeModules) {
	const links = absoluteSymlinks(nodeModules);
	if (links.length > 0) {
		log(`not caching this install: it has absolute symlinks (${links.slice(0, 3).join(", ")})`);
		return;
	}
	const t0 = Date.now();
	storeEntry(store, STORE_NAME, key, nodeModules, { lockfileSHA256: sha(readFileSync(path.join(frontend, "package-lock.json"))) }, {
		omit: [...NEVER_SHARED, INSTALL_STAMP],
		files: { "package-lock.json": path.join(frontend, "package-lock.json") },
	});
	const pruned = pruneEntries(store, STORE_NAME);
	log(`cached this install for other checkouts in ${seconds(t0)}${pruned.length ? ` (pruned ${pruned.length} older)` : ""}`);
}

async function warmDeps({ root, frontend }, options) {
	const nodeModules = path.join(frontend, "node_modules");
	const packageJSON = JSON.parse(readFileSync(path.join(frontend, "package.json"), "utf8"));
	const lockfile = readFileSync(path.join(frontend, "package-lock.json"));
	const npm = capture(npmCommand(), ["--version"]) ?? "unknown";
	const key = cacheKey(
		depsKeyParts({
			lockfile,
			packageJSON,
			linkedPackageJSONs: linkedPackages(frontend, packageJSON),
			node: process.version,
			npm,
			platform: process.platform,
			arch: process.arch,
		}),
	);
	const store = warmStoreRoot();
	const hasNodeModules = isDirectory(nodeModules);
	const installedKey = hasNodeModules ? (readJSON(path.join(nodeModules, INSTALL_STAMP), null)?.key ?? null) : null;
	const entry = lookupEntry(store, STORE_NAME, key);
	const cachedLock = entry ? path.join(path.dirname(entry), "package-lock.json") : null;
	const plan = planDeps({
		hasNodeModules: hasNodeModules && !(options.replace && !installedKey),
		installedKey,
		key,
		storeHit: Boolean(entry),
		lockfilesIdentical: Boolean(cachedLock) && fileIsIdentical(cachedLock, path.join(frontend, "package-lock.json")),
		// npm links `file:` packages with absolute junctions on Windows.
		cloneSupported: process.platform !== "win32" && cloneStrategies(process.platform).length > 1,
	});
	log(`node_modules: ${plan.action} - ${plan.reason} (key ${key.slice(0, 12)}, store ${store})`);
	if (options.dryRun || plan.action === "keep" || plan.action === "foreign") return;

	assertIgnoredOutputs(root, ["frontend/node_modules"]);
	if (hasNodeModules) {
		log(installedKey ? "removing the previous dev:warm install (its inputs changed)" : "removing the existing node_modules (--replace)");
		rmSync(nodeModules, { recursive: true, force: true });
	}
	const t0 = Date.now();
	if (plan.action === "clone") {
		try {
			const method = cloneTree(entry, nodeModules);
			writeJSON(path.join(nodeModules, INSTALL_STAMP), { key, from: path.dirname(entry), method });
			log(`node_modules cloned in ${seconds(t0)} via ${method}`);
			return;
		} catch (error) {
			log(`clone failed (${error.message}); falling back to npm ci`);
			rmSync(nodeModules, { recursive: true, force: true });
		}
	}
	npmCi(frontend);
	log(`npm ci took ${seconds(t0)}`);
	try {
		seedStore(store, key, frontend, nodeModules);
	} catch (error) {
		log(`could not cache this install (${error.message}); npm ci itself succeeded`);
	}
	writeJSON(path.join(nodeModules, INSTALL_STAMP), { key, from: "npm ci" });
}

function prepare({ frontend }, options) {
	const script = path.join(frontend, "scripts", "dev-prepare.mjs");
	const env = { ...process.env };
	if (options.reuseDaemon) env.AO_DEV_DAEMON_REUSE = path.resolve(options.reuseDaemon);
	const [command, args] = existsSync(script)
		? [process.execPath, [script, ...(options.dryRun ? ["--dry-run"] : [])]]
		: [npmCommand(), ["run", "predev"]]; // an older checkout without dev-prepare
	if (options.dryRun && command !== process.execPath) {
		log("predev: would run `npm run predev` (this checkout predates dev-prepare.mjs)");
		return;
	}
	const result = spawnSync(command, args, { cwd: frontend, env, stdio: "inherit", windowsHide: true, shell: command !== process.execPath && process.platform === "win32" });
	if (result.error || result.status !== 0) throw new Error(`predev steps failed (${result.error?.message ?? `exit ${result.status}`})`);
}

export async function main(argv) {
	const options = parseArgs(argv);
	const started = Date.now();
	const checkout = resolveWorktree(options.worktree);
	log(`${options.dryRun ? "dry run for" : "warming"} ${checkout.root}`);
	const statusBefore = gitStatus(checkout.root);

	await warmDeps(checkout, options);
	if (!options.skipPrepare) {
		if (!options.dryRun && !isDirectory(path.join(checkout.frontend, "node_modules"))) throw new Error("node_modules is missing; cannot run the predev steps");
		prepare(checkout, options);
	}

	const statusAfter = gitStatus(checkout.root);
	if (statusBefore !== statusAfter) {
		throw new Error(`git status changed while warming ${checkout.root}; dev:warm only writes ignored paths, so inspect \`git status\` now`);
	}
	log(`${options.dryRun ? "dry run finished" : "ready for `npm run dev`"} in ${seconds(started)}`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
	try {
		await main(process.argv.slice(2));
	} catch (error) {
		console.error(`[dev-warm] FAILED: ${error.message}`);
		console.error("[dev-warm] The slow path still works: cd frontend && npm ci && npm run dev");
		process.exitCode = 1;
	}
}
