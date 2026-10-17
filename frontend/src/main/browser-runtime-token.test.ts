import { spawnSync } from "node:child_process";
import {
	chmodSync,
	linkSync,
	mkdirSync,
	mkdtempSync,
	readdirSync,
	readFileSync,
	renameSync,
	rmSync,
	statSync,
	writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import { browserRuntimeKeyPath, resolveBrowserRuntimeToken, type BrowserRuntimeKeyFs } from "./browser-runtime-token";

const dirs: string[] = [];
// The child process loads the real module from disk; a vitest module URL would
// point at the dev server, which node cannot import.
const MODULE_URL = pathToFileURL(
	path.join(process.cwd(), "src", "main", "browser-runtime-token.ts"),
).href;

const nodeIo: BrowserRuntimeKeyFs = {
	chmodSync: (target, mode) => chmodSync(target, mode),
	linkSync: (target, destination) => linkSync(target, destination),
	mkdirSync: (target, options) => mkdirSync(target, options),
	readFileSync: (target, encoding) => readFileSync(target, encoding),
	renameSync: (oldPath, newPath) => renameSync(oldPath, newPath),
	rmSync: (target, options) => rmSync(target, options),
	writeFileSync: (target, data, options) => writeFileSync(target, data, options),
};

/**
 * The real filesystem, with a hook that runs once — immediately after this launch
 * reads the key path and before it can act on what it read. That is the window a
 * competing launch slips into: it reads, stalls, then finds the key already
 * repaired and published. Node builtins cannot be module-mocked in this setup,
 * so the injection seam is the filesystem.
 */
function ioWithStaleReadHook(onKeyRead: () => void): BrowserRuntimeKeyFs {
	let fired = false;
	return {
		...nodeIo,
		readFileSync: (target, encoding) => {
			if (!fired) {
				fired = true;
				onKeyRead();
			}
			return readFileSync(target, encoding);
		},
	};
}

function tempDataDir(): string {
	const dir = mkdtempSync(path.join(os.tmpdir(), "ao-runtime-token-"));
	dirs.push(dir);
	return path.join(dir, "data");
}

function readKeySafely(dataDir: string): string | null {
	try {
		return readFileSync(browserRuntimeKeyPath(dataDir), "utf8");
	} catch {
		return null;
	}
}

/** Resolve the token in a separate process, the way a second app launch does. */
function resolveInChildProcess(dataDir: string): { token: string | null; error: string | null } {
	const script = `import(${JSON.stringify(MODULE_URL)}).then((m) => {
		process.stdout.write(m.resolveBrowserRuntimeToken(${JSON.stringify(dataDir)}));
	}).catch((error) => {
		process.stderr.write(String(error && error.message ? error.message : error));
		process.exitCode = 1;
	});`;
	const result = spawnSync(process.execPath, ["--no-warnings", "--experimental-strip-types", "-e", script], {
		encoding: "utf8",
	});
	if (result.status !== 0) return { token: null, error: result.stderr.trim() || `exit ${result.status}` };
	return { token: result.stdout.trim(), error: null };
}

afterEach(() => {
	dirs.splice(0).forEach((dir) => rmSync(dir, { recursive: true, force: true }));
});

describe("resolveBrowserRuntimeToken", () => {
	it("returns the same token for every launch on one data dir", () => {
		const dataDir = tempDataDir();

		// Each call stands in for one app launch: the key persists, so a daemon
		// kept alive across restarts still accepts this app's hello.
		expect(resolveBrowserRuntimeToken(dataDir)).toBe(resolveBrowserRuntimeToken(dataDir));
	});

	it("keeps tokens distinct per data dir", () => {
		expect(resolveBrowserRuntimeToken(tempDataDir())).not.toBe(resolveBrowserRuntimeToken(tempDataDir()));
	});

	it("stores an owner-only key that is not the token itself", () => {
		const dataDir = tempDataDir();
		const token = resolveBrowserRuntimeToken(dataDir);
		const key = readFileSync(browserRuntimeKeyPath(dataDir), "utf8").trim();

		expect(key).not.toBe(token);
		expect(token.length).toBeGreaterThanOrEqual(43);
		if (process.platform !== "win32") {
			expect(statSync(browserRuntimeKeyPath(dataDir)).mode & 0o777).toBe(0o600);
		}
	});

	// The reported interleave: this launch reads a damaged key, another launch
	// repairs and publishes a valid one while this one is stalled, and this launch
	// must not move that valid key aside on its way to publishing its own.
	it("adopts a key repaired by another launch after a stale read", () => {
		const dataDir = tempDataDir();
		resolveBrowserRuntimeToken(dataDir);
		writeFileSync(browserRuntimeKeyPath(dataDir), "not-a-key\n", { mode: 0o600 });
		chmodSync(browserRuntimeKeyPath(dataDir), 0o600);

		let repairer = "";
		const staleReader = resolveBrowserRuntimeToken(
			dataDir,
			ioWithStaleReadHook(() => {
				// The competitor repairs the damaged key and returns its token
				// before this launch acts on what it read.
				repairer = resolveBrowserRuntimeToken(dataDir);
			}),
		);

		expect(repairer).not.toBe("");
		expect(staleReader).toBe(repairer);
		expect(resolveBrowserRuntimeToken(dataDir)).toBe(repairer);
		expect(readFileSync(browserRuntimeKeyPath(dataDir), "utf8").trim()).toMatch(/^[A-Za-z0-9_-]{43}$/);
	});

	it("does not displace a valid key an outside writer published mid-repair", () => {
		// A writer that does not take this build's lock (an older app build, say)
		// publishes a valid key between the re-read under the lock and the rename.
		// Displacing it would strand that writer on a token nothing on disk backs.
		const outsiderDir = tempDataDir();
		resolveBrowserRuntimeToken(outsiderDir);
		const outsiderKey = readFileSync(browserRuntimeKeyPath(outsiderDir), "utf8").trim();
		const outsiderToken = resolveBrowserRuntimeToken(outsiderDir);

		const dataDir = tempDataDir();
		resolveBrowserRuntimeToken(dataDir);
		writeFileSync(browserRuntimeKeyPath(dataDir), "not-a-key\n", { mode: 0o600 });

		// Reads of the key path, in order: the fast path, the re-read under the
		// lock, and the one guarding the rename. Publish on the third.
		let reads = 0;
		let injected = false;
		const io: BrowserRuntimeKeyFs = {
			...nodeIo,
			readFileSync: (target, encoding) => {
				reads += 1;
				if (reads === 3 && !injected) {
					// Publish before this read is served, so the reader sees a key
					// that appeared after the previous one.
					injected = true;
					writeFileSync(browserRuntimeKeyPath(dataDir), `${outsiderKey}\n`, { mode: 0o600 });
				}
				return readFileSync(target, encoding);
			},
		};

		expect(resolveBrowserRuntimeToken(dataDir, io)).toBe(outsiderToken);
		expect(readFileSync(browserRuntimeKeyPath(dataDir), "utf8").trim()).toBe(outsiderKey);
	});

	it("adopts the key a competing launch published first", () => {
		const dataDir = tempDataDir();
		let competitor = "";
		const io = ioWithStaleReadHook(() => {
			competitor = resolveBrowserRuntimeToken(dataDir);
		});
		const creator = resolveBrowserRuntimeToken(dataDir, io);

		expect(competitor).not.toBe("");
		expect(creator).toBe(competitor);
		expect(resolveBrowserRuntimeToken(dataDir)).toBe(creator);
	});

	it("never exposes a partially written key to a concurrent reader", () => {
		const dataDir = tempDataDir();
		const observed: Array<string | null> = [];
		const io = ioWithStaleReadHook(() => {
			// Read the key path the moment this launch observes it. Until the key
			// is published it must be absent, never truncated.
			observed.push(readKeySafely(dataDir));
		});
		const first = resolveBrowserRuntimeToken(dataDir, io);
		const second = resolveBrowserRuntimeToken(dataDir);

		expect(observed).toEqual([null]);
		expect(first).toBe(second);
	});

	it("leaves no lock or temporary files behind", () => {
		const dataDir = tempDataDir();
		resolveBrowserRuntimeToken(dataDir, ioWithStaleReadHook(() => resolveBrowserRuntimeToken(dataDir)));

		expect(readdirSync(dataDir).filter((entry) => entry.startsWith("browser-runtime.key."))).toEqual([]);
	});

	it("replaces a corrupt key instead of propagating it", () => {
		const dataDir = tempDataDir();
		const dataRoot = path.dirname(browserRuntimeKeyPath(dataDir));
		// Simulate a truncated file: the previous token is unrecoverable either way.
		resolveBrowserRuntimeToken(dataDir);
		writeFileSync(browserRuntimeKeyPath(dataDir), "not-a-key\n", { mode: 0o600 });
		chmodSync(browserRuntimeKeyPath(dataDir), 0o600);

		const repaired = resolveBrowserRuntimeToken(dataDir);
		expect(resolveBrowserRuntimeToken(dataDir)).toBe(repaired);
		expect(readFileSync(browserRuntimeKeyPath(dataDir), "utf8").trim()).toMatch(/^[A-Za-z0-9_-]{43}$/);
		expect(statSync(dataRoot).isDirectory()).toBe(true);
	});

	it("gives every concurrent launch the one persisted key", () => {
		const dataDir = tempDataDir();
		const tokens = Array.from({ length: 8 }, () => resolveInChildProcess(dataDir));

		const failures = tokens.filter((result) => result.error);
		expect(failures.map((failure) => failure.error)).toEqual([]);
		const resolved = tokens.map((result) => result.token as string);
		const inProcess = resolveBrowserRuntimeToken(dataDir);

		// One key on disk, one token everywhere: a launch that disagreed with the
		// persisted key would authenticate against no daemon.
		expect(new Set(resolved)).toEqual(new Set([inProcess]));
		expect(readFileSync(browserRuntimeKeyPath(dataDir), "utf8").trim()).toMatch(/^[A-Za-z0-9_-]{43}$/);
	});

	it("gives every concurrent launch the same repaired key", () => {
		const dataDir = tempDataDir();
		resolveBrowserRuntimeToken(dataDir);
		// A damaged key is the case the reviewer measured: many launches repairing
		// at once is where a stale reader used to strand the others.
		writeFileSync(browserRuntimeKeyPath(dataDir), "not-a-key\n", { mode: 0o600 });

		const tokens = Array.from({ length: 8 }, () => resolveInChildProcess(dataDir));

		expect(tokens.filter((result) => result.error).map((failure) => failure.error)).toEqual([]);
		const resolved = tokens.map((result) => result.token as string);

		expect(new Set(resolved).size).toBe(1);
		expect(resolveBrowserRuntimeToken(dataDir)).toBe(resolved[0]);
		expect(readFileSync(browserRuntimeKeyPath(dataDir), "utf8").trim()).toMatch(/^[A-Za-z0-9_-]{43}$/);
	});
});