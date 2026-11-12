// Shared helpers for fast dev launches (scripts/dev-prepare.mjs and
// scripts/dev-warm.mjs): content-keyed caches of build outputs, restored into
// a checkout with copy-on-write clones. Node built-ins only: dev-warm runs
// before node_modules exists.
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
	constants,
	cpSync,
	existsSync,
	lstatSync,
	mkdirSync,
	readdirSync,
	readFileSync,
	readlinkSync,
	renameSync,
	rmSync,
	statSync,
	utimesSync,
	writeFileSync,
} from "node:fs";
import { homedir } from "node:os";
import path from "node:path";

// Bump to invalidate every cached output when the cache layout or keying changes.
export const CACHE_SCHEMA = 1;
export const KEEP_ENTRIES = 3;

export function buildCacheRoot(env = process.env) {
	return env.AO_BUILD_CACHE_DIR || path.join(homedir(), ".ao", "build-cache", "outputs");
}

export function warmStoreRoot(env = process.env) {
	return env.AO_WARM_DIR || path.join(homedir(), ".ao", "warm");
}

export function log(message) {
	console.log(`[dev-cache] ${message}`);
}

// JSON with sorted object keys, so equal inputs always hash equally.
export function stableStringify(value) {
	if (Array.isArray(value)) return `[${value.map(stableStringify).join(",")}]`;
	if (value && typeof value === "object") {
		return `{${Object.keys(value)
			.sort()
			.filter((key) => value[key] !== undefined)
			.map((key) => `${JSON.stringify(key)}:${stableStringify(value[key])}`)
			.join(",")}}`;
	}
	return JSON.stringify(value ?? null);
}

// Digest of the named files' paths and contents. A missing file hashes as
// "missing", so deleting an input changes the key instead of being ignored.
export function hashFiles(root, relPaths, readFile = readFileSync) {
	const hash = createHash("sha256");
	for (const rel of [...new Set(relPaths.map(toPosix))].sort()) {
		let digest;
		try {
			digest = createHash("sha256").update(readFile(path.join(root, rel))).digest("hex");
		} catch (error) {
			if (error?.code !== "ENOENT" && error?.code !== "EISDIR" && error?.code !== "ENOTDIR") throw error;
			digest = "missing";
		}
		hash.update(`${rel}\0${digest}\n`);
	}
	return hash.digest("hex");
}

export function cacheKey(parts) {
	return createHash("sha256")
		.update(stableStringify({ schema: CACHE_SCHEMA, ...parts }))
		.digest("hex")
		.slice(0, 32);
}

function toPosix(rel) {
	return rel.split(path.sep).join("/");
}

function splitNul(text) {
	return text.split("\0").filter(Boolean);
}

export function runGit(repoRoot, args, run = spawnSync) {
	const result = run("git", ["-C", repoRoot, ...args], { encoding: "utf8", maxBuffer: 64 * 1024 * 1024, windowsHide: true });
	if (result.error || result.status !== 0) return null;
	return result.stdout;
}

// Files git would consider part of the source: tracked plus untracked files
// that are not ignored, relative to repoRoot. Ignored files are added back only
// when their name ends with one of `ignoredSuffixes` (for example an ignored
// generated .go file still compiles). Returns null outside a git checkout.
export function gitSourceFiles(repoRoot, pathspecs, { ignoredSuffixes = [], run = spawnSync } = {}) {
	const listed = runGit(repoRoot, ["ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", ...pathspecs], run);
	if (listed == null) return null;
	const files = splitNul(listed);
	if (ignoredSuffixes.length > 0) {
		const ignored = runGit(
			repoRoot,
			["ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--", ...pathspecs],
			run,
		);
		if (ignored == null) return null;
		files.push(...splitNul(ignored).filter((file) => ignoredSuffixes.some((suffix) => file.endsWith(suffix))));
	}
	return [...new Set(files)].sort();
}

const IMPORT_PATTERN =
	/(?:^|[\s;])(?:import|export)\s[^'"`;]*?\bfrom\s*["'](\.{1,2}\/[^"']+)["']|\bimport\s*\(\s*["'](\.{1,2}\/[^"']+)["']\s*\)|(?:^|[\s;])import\s*["'](\.{1,2}\/[^"']+)["']/gm;
const RESOLVE_SUFFIXES = ["", ".ts", ".tsx", ".mts", ".js", ".mjs", "/index.ts", "/index.tsx", "/index.js"];

export function relativeImports(source) {
	const specifiers = [];
	for (const match of source.matchAll(IMPORT_PATTERN)) {
		specifiers.push((match[1] ?? match[2] ?? match[3]).replace(/[?#].*$/, ""));
	}
	return specifiers;
}

// Every file reachable from `entries` through relative imports (paths relative
// to root). Package imports are covered by the lockfile instead. `unresolved`
// lists specifiers that matched no file: callers must then widen the key, since
// the closure may be incomplete.
export function importClosure(root, entries, { isFile = isRegularFile, read = (file) => readFileSync(file, "utf8") } = {}) {
	const seen = new Set();
	const unresolved = [];
	const queue = entries.map(toPosix);
	while (queue.length > 0) {
		const rel = queue.shift();
		if (seen.has(rel)) continue;
		seen.add(rel);
		if (!/\.(?:[cm]?[jt]sx?)$/.test(rel)) continue;
		for (const specifier of relativeImports(read(path.join(root, rel)))) {
			const base = path.posix.normalize(path.posix.join(path.posix.dirname(rel), specifier));
			const candidates = RESOLVE_SUFFIXES.map((suffix) => base + suffix);
			if (/\.m?js$/.test(base)) candidates.push(base.replace(/\.(m?)js$/, ".$1ts"), base.replace(/\.js$/, ".tsx"));
			const hit = candidates.find((candidate) => !candidate.startsWith("../") && isFile(path.join(root, candidate)));
			if (hit) queue.push(hit);
			else unresolved.push(`${rel} -> ${specifier}`);
		}
	}
	return { files: [...seen].sort(), unresolved };
}

function isRegularFile(file) {
	try {
		return statSync(file).isFile();
	} catch {
		return false;
	}
}

// --- Cloning -----------------------------------------------------------------

// Order of copy strategies per platform. Every list ends with a plain copy, so
// cloning never fails just because copy-on-write is unavailable.
export function cloneStrategies(platform) {
	if (platform === "darwin") return ["clonefile", "cp-clone", "copy"];
	if (platform === "linux") return ["cp-reflink", "copy"];
	return ["copy"];
}

// clonefile(2) clones a whole directory tree in one call on APFS (about 1 s for
// a 70k-file node_modules, versus about 9 s for per-file `cp -c`). Called by
// symbol through ctypes rather than a raw syscall number.
const CLONEFILE_PY = [
	"import ctypes, os, sys",
	"libc = ctypes.CDLL(None, use_errno=True)",
	"libc.clonefile.argtypes = [ctypes.c_char_p, ctypes.c_char_p, ctypes.c_uint32]",
	"if libc.clonefile(os.fsencode(sys.argv[1]), os.fsencode(sys.argv[2]), 0) != 0:",
	"    sys.exit('clonefile: ' + os.strerror(ctypes.get_errno()))",
].join("\n");

let commandLineToolsPresent;
function hasCommandLineTools(run) {
	// /usr/bin/python3 is a stub that pops an installer dialog without them.
	if (commandLineToolsPresent === undefined) {
		const result = run("xcode-select", ["-p"], { encoding: "utf8" });
		commandLineToolsPresent = !result.error && result.status === 0;
	}
	return commandLineToolsPresent;
}

// Copies the directory `src` to `dst`, which must not exist yet. Returns the
// strategy that worked. A failed attempt's partial output is removed before
// the next strategy runs.
export function cloneTree(src, dst, { platform = process.platform, run = spawnSync, copy = cpSync } = {}) {
	if (existsSync(dst)) throw new Error(`refusing to clone over existing ${dst}`);
	mkdirSync(path.dirname(dst), { recursive: true });
	const failures = [];
	for (const strategy of cloneStrategies(platform)) {
		try {
			if (strategy === "copy") {
				copy(src, dst, { recursive: true, verbatimSymlinks: true, mode: constants.COPYFILE_FICLONE });
				return strategy;
			}
			if (strategy === "clonefile" && !hasCommandLineTools(run)) {
				failures.push("clonefile: Command Line Tools unavailable");
				continue;
			}
			const [command, args] =
				strategy === "clonefile"
					? ["/usr/bin/python3", ["-I", "-c", CLONEFILE_PY, src, dst]]
					: strategy === "cp-clone"
						? ["cp", ["-c", "-R", src, dst]]
						: ["cp", ["-R", "--reflink=auto", src, dst]];
			const result = run(command, args, { encoding: "utf8", windowsHide: true });
			if (!result.error && result.status === 0) return strategy;
			failures.push(`${strategy}: ${(result.error?.message ?? result.stderr ?? "").trim() || `exit ${result.status}`}`);
		} catch (error) {
			failures.push(`${strategy}: ${error.message}`);
		}
		rmSync(dst, { recursive: true, force: true });
	}
	throw new Error(`could not copy ${src} to ${dst} (${failures.join("; ")})`);
}

// Symlinks under `dir` whose target is absolute. A clone keeps link targets
// verbatim, so an absolute link would point back into the source tree.
export function absoluteSymlinks(dir) {
	const found = [];
	const pending = [dir];
	while (pending.length > 0) {
		const current = pending.pop();
		for (const entry of readdirSync(current, { withFileTypes: true })) {
			const full = path.join(current, entry.name);
			if (entry.isSymbolicLink()) {
				if (path.isAbsolute(readlinkSync(full))) found.push(full);
			} else if (entry.isDirectory()) {
				pending.push(full);
			}
		}
	}
	return found;
}

// --- Cache entries -----------------------------------------------------------
// <root>/<name>/<key>/{output/, meta.json}. Entries are written once through a
// temporary directory and an atomic rename, then only read.

export function entryDir(root, name, key) {
	return path.join(root, name, key);
}

export function lookupEntry(root, name, key) {
	const dir = entryDir(root, name, key);
	if (!existsSync(path.join(dir, "meta.json")) || !existsSync(path.join(dir, "output"))) return null;
	const now = new Date();
	try {
		utimesSync(dir, now, now); // recency for pruning
	} catch {
		// Read-only cache: still usable.
	}
	return path.join(dir, "output");
}

// `omit` lists output-relative paths dropped from the stored copy (per-checkout
// caches); `files` maps extra entry file names to source paths copied beside it.
export function storeEntry(root, name, key, sourceDir, meta, { clone = cloneTree, omit = [], files = {} } = {}) {
	const final = entryDir(root, name, key);
	if (existsSync(path.join(final, "meta.json"))) return final;
	const temp = path.join(root, name, `.tmp-${key}-${process.pid}-${Date.now()}`);
	try {
		clone(sourceDir, path.join(temp, "output"));
		for (const rel of omit) rmSync(path.join(temp, "output", rel), { recursive: true, force: true });
		for (const [fileName, source] of Object.entries(files)) cpSync(source, path.join(temp, fileName));
		writeFileSync(path.join(temp, "meta.json"), `${JSON.stringify({ name, key, createdAt: new Date().toISOString(), ...meta }, null, 2)}\n`);
		if (existsSync(final)) rmSync(final, { recursive: true, force: true }); // incomplete leftover without meta.json
		renameSync(temp, final);
	} catch (error) {
		rmSync(temp, { recursive: true, force: true });
		// Another checkout stored the same key first: theirs is equivalent.
		if (existsSync(path.join(final, "meta.json"))) return final;
		throw error;
	}
	return final;
}

// Which entries to delete: everything but the `keep` most recently used, plus
// temporary directories older than an hour (a crashed store).
export function entriesToPrune(entries, keep = KEEP_ENTRIES, now = Date.now()) {
	const complete = entries.filter((entry) => !entry.name.startsWith("."));
	const stale = entries.filter((entry) => entry.name.startsWith(".tmp-") && now - entry.mtimeMs > 60 * 60 * 1000);
	const old = complete.sort((a, b) => b.mtimeMs - a.mtimeMs).slice(keep);
	return [...old, ...stale].map((entry) => entry.name);
}

export function pruneEntries(root, name, keep = KEEP_ENTRIES) {
	const dir = path.join(root, name);
	let entries;
	try {
		entries = readdirSync(dir, { withFileTypes: true })
			.filter((entry) => entry.isDirectory())
			.map((entry) => ({ name: entry.name, mtimeMs: statSync(path.join(dir, entry.name)).mtimeMs }));
	} catch {
		return [];
	}
	const removed = entriesToPrune(entries, keep);
	for (const entryName of removed) rmSync(path.join(dir, entryName), { recursive: true, force: true });
	return removed;
}

// --- Stamps --------------------------------------------------------------------
// A stamp records which key produced a checkout's current output, plus the size
// and mtime of a few sentinel files so a replaced or deleted output is noticed.

export function outputFingerprint(outDir, sentinels) {
	const fingerprint = [];
	for (const rel of sentinels) {
		try {
			const stat = statSync(path.join(outDir, rel));
			if (!stat.isFile()) return null;
			fingerprint.push([toPosix(rel), stat.size, Math.trunc(stat.mtimeMs)]);
		} catch {
			return null;
		}
	}
	return fingerprint;
}

export function stampIsCurrent(stamp, key, fingerprint) {
	return Boolean(stamp && fingerprint && stamp.key === key && stableStringify(stamp.fingerprint) === stableStringify(fingerprint));
}

export function readJSON(file, fallback) {
	try {
		return JSON.parse(readFileSync(file, "utf8"));
	} catch {
		return fallback;
	}
}

export function writeJSON(file, value) {
	mkdirSync(path.dirname(file), { recursive: true });
	writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
}

// --- Decisions -----------------------------------------------------------------

// What to do for one build step. `key` is null when the step cannot be keyed
// (cache disabled, no git, missing toolchain), which always means a real build.
export function planStep({ key, stampCurrent, cachedOutput }) {
	if (!key) return "build";
	if (stampCurrent) return "skip";
	if (cachedOutput) return "restore";
	return "build";
}

// How to provide frontend/node_modules.
// - keep: already installed from this exact key.
// - foreign: an install dev-warm did not make; leave it alone.
// - clone: identical lockfile in the store; clone it.
// - install: run a real `npm ci` (and seed the store afterwards).
export function planDeps({ hasNodeModules, installedKey, key, storeHit, lockfilesIdentical, cloneSupported }) {
	if (hasNodeModules && installedKey === key) return { action: "keep", reason: "node_modules already matches the lockfile" };
	if (hasNodeModules && !installedKey) {
		return { action: "foreign", reason: "node_modules was not installed by dev:warm; leaving it untouched (pass --replace to swap it)" };
	}
	if (!cloneSupported) return { action: "install", reason: "cloning node_modules is not supported on this platform" };
	if (!storeHit) return { action: "install", reason: "no cached install for this lockfile yet" };
	if (!lockfilesIdentical) return { action: "install", reason: "cached lockfile differs byte-for-byte from this checkout's" };
	return { action: "clone", reason: "cached install has an identical lockfile" };
}

// Ignored paths dev-warm may write, relative to the repository root. Nothing
// outside this list is ever cloned into a checkout.
export function assertIgnoredOutputs(repoRoot, relDirs, run = spawnSync) {
	for (const rel of relDirs) {
		const dir = `${toPosix(rel).replace(/\/$/, "")}/`;
		const ignored = run("git", ["-C", repoRoot, "check-ignore", "-q", dir], { windowsHide: true });
		if (ignored.error || ignored.status !== 0) {
			throw new Error(`${dir} is not ignored by git; refusing to write cached output there`);
		}
		const tracked = runGit(repoRoot, ["ls-files", "-z", "--", dir], run);
		if (tracked == null || tracked.length > 0) {
			throw new Error(`${dir} contains tracked files; refusing to write cached output there`);
		}
	}
}

export function fileIsIdentical(a, b) {
	try {
		const left = readFileSync(a);
		const right = readFileSync(b);
		return left.equals(right);
	} catch {
		return false;
	}
}

export function isDirectory(dir) {
	try {
		return lstatSync(dir).isDirectory();
	} catch {
		return false;
	}
}
