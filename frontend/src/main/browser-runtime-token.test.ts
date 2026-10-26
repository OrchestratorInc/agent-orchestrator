import { spawn } from "node:child_process";
import {
	chmodSync,
	closeSync,
	existsSync,
	fchmodSync,
	mkdirSync,
	mkdtempSync,
	openSync,
	readdirSync,
	readFileSync,
	rmSync,
	statSync,
	symlinkSync,
	writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { afterEach, describe, expect, it, vi } from "vitest";
import { browserRuntimeKeyPath, resolveBrowserRuntimeToken, type BrowserRuntimeKeyFs } from "./browser-runtime-token";

const dirs: string[] = [];
const children: ReturnType<typeof spawn>[] = [];
// The child process loads the real module from disk; a vitest module URL would
// point at the dev server, which node cannot import.
const MODULE_URL = pathToFileURL(
	path.join(process.cwd(), "src", "main", "browser-runtime-token.ts"),
).href;

const nodeIo: BrowserRuntimeKeyFs = {
	closeSync: (fd) => closeSync(fd),
	fchmodSync: (fd, mode) => fchmodSync(fd, mode),
	mkdirSync: (target, options) => mkdirSync(target, options),
	openSync: (target, flags, mode) => openSync(target, flags, mode),
	readFileSync: (target, encoding) => readFileSync(target, encoding),
	writeFileSync: (target, data) => writeFileSync(target, data),
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
			let value = "";
			let failure: unknown;
			try {
				value = readFileSync(target, encoding);
			} catch (error) {
				failure = error;
			}
			if (!fired) {
				fired = true;
				onKeyRead();
			}
			if (failure) throw failure;
			return value;
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
function resolveInChildProcess(dataDir: string): Promise<{ token: string | null; error: string | null }> {
	const script = `import(${JSON.stringify(MODULE_URL)}).then((m) => {
		process.stdout.write(m.resolveBrowserRuntimeToken(${JSON.stringify(dataDir)}));
	}).catch((error) => {
		process.stderr.write(String(error && error.message ? error.message : error));
		process.exitCode = 1;
	});`;
	return runChild(["-e", script]).result;
}

function runChild(args: string[]) {
	const child = spawn(process.execPath, ["--no-warnings", "--experimental-strip-types", ...args]);
	children.push(child);
	let output = "";
	let error = "";
	let done = false;
		child.stdout.on("data", (data) => {
			output += data;
		});
		child.stderr.on("data", (data) => {
			error += data;
		});
	const result = new Promise<{ token: string | null; error: string | null }>((resolve, reject) => {
		child.on("error", reject);
		child.on("close", (code) => {
			done = true;
			resolve(code === 0 ? { token: output.trim(), error: null } : { token: null, error: error.trim() || `exit ${code}` });
		});
	});
	return { result, done: () => done };
}

afterEach(() => {
	children.splice(0).forEach((child) => child.kill());
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
		expect(readFileSync(browserRuntimeKeyPath(dataDir), "utf8")).toMatch(/(?:^|\n)[A-Za-z0-9_-]{43}\n/);
	});

	it("adopts a valid key another writer appends before its own candidate", () => {
		const outsiderDir = tempDataDir();
		resolveBrowserRuntimeToken(outsiderDir);
		const outsiderKey = readFileSync(browserRuntimeKeyPath(outsiderDir), "utf8").trim();
		const outsiderToken = resolveBrowserRuntimeToken(outsiderDir);

		const dataDir = tempDataDir();
		resolveBrowserRuntimeToken(dataDir);
		writeFileSync(browserRuntimeKeyPath(dataDir), "not-a-key\n", { mode: 0o600 });

		let injected = false;
		const io: BrowserRuntimeKeyFs = {
			...nodeIo,
			writeFileSync: (target, data) => {
				if (!injected) {
					injected = true;
					writeFileSync(target, `\n${outsiderKey}\n`);
				}
				writeFileSync(target, data);
			},
		};

		expect(resolveBrowserRuntimeToken(dataDir, io)).toBe(outsiderToken);
		expect(resolveBrowserRuntimeToken(dataDir)).toBe(outsiderToken);
		expect(readFileSync(browserRuntimeKeyPath(dataDir), "utf8")).toContain(`\n${outsiderKey}\n`);
	});

	it("ignores a valid-looking unfinished record before a writer continues it", () => {
		const originalDir = tempDataDir();
		const unfinishedToken = resolveBrowserRuntimeToken(originalDir);
		const unfinishedKey = readFileSync(browserRuntimeKeyPath(originalDir), "utf8").trim();
		const dataDir = tempDataDir();
		mkdirSync(dataDir, { recursive: true });
		writeFileSync(browserRuntimeKeyPath(dataDir), `\n${unfinishedKey}`, { mode: 0o600 });
		let continued = false;
		const io: BrowserRuntimeKeyFs = {
			...nodeIo,
			writeFileSync: (target, data) => {
				if (!continued) {
					continued = true;
					writeFileSync(target, "x\n");
				}
				writeFileSync(target, data);
			},
		};

		const token = resolveBrowserRuntimeToken(dataDir, io);
		expect(continued).toBe(true);
		expect(token).not.toBe(unfinishedToken);
		expect(resolveBrowserRuntimeToken(dataDir)).toBe(token);
	});

	it("retries when interleaved short writes leave no complete key", () => {
		const dataDir = tempDataDir();
		let interleaved = false;
		const io: BrowserRuntimeKeyFs = {
			...nodeIo,
			writeFileSync: (target, data) => {
				if (!interleaved) {
					interleaved = true;
					writeFileSync(target, `${data.slice(0, 10)}\nother-fragment\n${data.slice(10)}`);
					return;
				}
				writeFileSync(target, data);
			},
		};

		const token = resolveBrowserRuntimeToken(dataDir, io);
		expect(interleaved).toBe(true);
		expect(resolveBrowserRuntimeToken(dataDir)).toBe(token);
	});

	it.skipIf(process.platform === "win32")("does not append repairs through a corrupt symlink", () => {
		const dataDir = tempDataDir();
		mkdirSync(dataDir, { recursive: true });
		const target = path.join(path.dirname(dataDir), "unrelated-file");
		writeFileSync(target, "unrelated contents\n");
		symlinkSync(target, browserRuntimeKeyPath(dataDir));

		expect(() => resolveBrowserRuntimeToken(dataDir)).toThrow();
		expect(readFileSync(target, "utf8")).toBe("unrelated contents\n");
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
		expect(readFileSync(browserRuntimeKeyPath(dataDir), "utf8")).toMatch(/(?:^|\n)[A-Za-z0-9_-]{43}\n/);
		expect(statSync(dataRoot).isDirectory()).toBe(true);
	});

	it("gives every concurrent launch the one persisted key", async () => {
		const dataDir = tempDataDir();
		const tokens = await Promise.all(Array.from({ length: 8 }, () => resolveInChildProcess(dataDir)));

		const failures = tokens.filter((result) => result.error);
		expect(failures.map((failure) => failure.error)).toEqual([]);
		const resolved = tokens.map((result) => result.token as string);
		const inProcess = resolveBrowserRuntimeToken(dataDir);

		// One key on disk, one token everywhere: a launch that disagreed with the
		// persisted key would authenticate against no daemon.
		expect(new Set(resolved)).toEqual(new Set([inProcess]));
		expect(readFileSync(browserRuntimeKeyPath(dataDir), "utf8")).toMatch(/(?:^|\n)[A-Za-z0-9_-]{43}\n/);
	});

	it("gives every concurrent launch the same repaired key", async () => {
		const dataDir = tempDataDir();
		resolveBrowserRuntimeToken(dataDir);
		// A damaged key is the case the reviewer measured: many launches repairing
		// at once is where a stale reader used to strand the others.
		writeFileSync(browserRuntimeKeyPath(dataDir), "not-a-key\n", { mode: 0o600 });

		const tokens = await Promise.all(Array.from({ length: 8 }, () => resolveInChildProcess(dataDir)));

		expect(tokens.filter((result) => result.error).map((failure) => failure.error)).toEqual([]);
		const resolved = tokens.map((result) => result.token as string);

		expect(new Set(resolved).size).toBe(1);
		expect(resolveBrowserRuntimeToken(dataDir)).toBe(resolved[0]);
		expect(readFileSync(browserRuntimeKeyPath(dataDir), "utf8")).toMatch(/(?:^|\n)[A-Za-z0-9_-]{43}\n/);
	});

	it("keeps both repairers on the persisted key when stale-lock cleanup races a new owner", async () => {
		const dataDir = tempDataDir();
		mkdirSync(dataDir, { recursive: true });
		const keyPath = browserRuntimeKeyPath(dataDir);
		writeFileSync(keyPath, "corrupt\n", { mode: 0o600 });
		writeFileSync(`${keyPath}.lock`, `${Date.now() - 31_000}\n`, { mode: 0o600 });
		const barriers = path.join(path.dirname(dataDir), "barriers");
		mkdirSync(barriers);
		const worker = path.join(process.cwd(), "src/main/test-fixtures/browser-runtime-token-worker.mjs");
		const first = runChild([worker, "stale-reader", dataDir, barriers]);
		await vi.waitFor(() => {
			expect(existsSync(path.join(barriers, "stale-reader-observed-lock")) || first.done()).toBe(true);
		}, { timeout: 10_000 });
		const second = runChild([worker, "new-owner", dataDir, barriers]);
		const results = await Promise.all([first.result, second.result]);

		expect(results.map((result) => result.error)).toEqual([null, null]);
		expect(results[0].token === results[1].token).toBe(true);
		expect(resolveBrowserRuntimeToken(dataDir) === results[0].token).toBe(true);
	}, 15_000);
});
