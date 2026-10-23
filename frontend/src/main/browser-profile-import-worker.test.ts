// @vitest-environment node
import { fork, type ChildProcess } from "node:child_process";
import { createCipheriv, createHash, pbkdf2Sync } from "node:crypto";
import { once } from "node:events";
import { mkdir, mkdtemp, readdir, readFile, realpath, rm, stat, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import Database from "better-sqlite3";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BrowserHistoryStore } from "./browser-history-store";
import {
	answerSourceReadRequest,
	BrowserProfileImportService,
	processSourceReader,
	readSourceInProcess,
	type SourceReader,
	type SourceReadProcess,
	type SourceReadRequest,
} from "./browser-profile-import";
import { BrowserProfileStore } from "./browser-profile-store";

const temporaryDirectories: string[] = [];

afterEach(async () => {
	vi.restoreAllMocks();
	await Promise.all(temporaryDirectories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

async function fixtureRoot(): Promise<string> {
	// Canonical, like the profile roots discovery hands to readProfileData.
	const directory = await realpath(await mkdtemp(path.join(os.tmpdir(), "ao-browser-import-worker-")));
	temporaryDirectories.push(directory);
	return directory;
}

function chromiumMicros(iso: string): number {
	return Math.round((Date.parse(iso) / 1_000 + 11_644_473_600) * 1_000_000);
}

// Chromium on macOS: v10 + AES-128-CBC(PBKDF2(password)), with the v24
// SHA-256(host_key) prefix on the plaintext.
function macEncryptedCookie(password: string, host: string, value: string): Buffer {
	const key = pbkdf2Sync(password, "saltysalt", 1_003, 16, "sha1");
	const cipher = createCipheriv("aes-128-cbc", key, Buffer.alloc(16, 0x20));
	const plaintext = Buffer.concat([createHash("sha256").update(host).digest(), Buffer.from(value)]);
	return Buffer.concat([Buffer.from("v10"), cipher.update(plaintext), cipher.final()]);
}

async function createChromeProfile(userData: string, password = "lab-password"): Promise<string> {
	const profileRoot = path.join(userData, "Default");
	await mkdir(path.join(profileRoot, "Network"), { recursive: true });
	await writeFile(path.join(userData, "Local State"), JSON.stringify({ profile: { info_cache: { Default: { name: "Personal" } } } }));
	const future = chromiumMicros("2030-01-01T00:00:00.000Z");
	const cookies = new Database(path.join(profileRoot, "Network", "Cookies"));
	cookies.exec(`
		CREATE TABLE cookies (
			host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, path TEXT,
			expires_utc INTEGER, is_secure INTEGER, is_httponly INTEGER, samesite INTEGER,
			top_frame_site_key TEXT, last_access_utc INTEGER, creation_utc INTEGER
		)
	`);
	const insert = cookies.prepare("INSERT INTO cookies VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)");
	insert.run(".github.com", "plain", "visible", Buffer.alloc(0), "/", future, 1, 1, 1, "", future, future);
	insert.run(".github.com", "sealed", "", macEncryptedCookie(password, ".github.com", "decrypted"), "/", future, 1, 1, 1, "", future - 1, future - 1);
	cookies.close();
	const history = new Database(path.join(profileRoot, "History"));
	history.exec("CREATE TABLE urls (url TEXT, title TEXT, visit_count INTEGER, last_visit_time INTEGER)");
	history.prepare("INSERT INTO urls VALUES (?, ?, ?, ?)").run("https://github.com/openai", "OpenAI", 4, chromiumMicros("2026-01-01T00:00:00.000Z"));
	history.close();
	return profileRoot;
}

async function sourceDigest(profileRoot: string): Promise<string[]> {
	return Promise.all(["History", path.join("Network", "Cookies")].map(async (file) =>
		createHash("sha256").update(await readFile(path.join(profileRoot, file))).digest("hex")));
}

function profileReadRequest(profileRoot: string, staging: string, overrides: Record<string, unknown> = {}): SourceReadRequest<"readProfileData"> {
	return {
		name: "readProfileData",
		args: [{
			family: "chromium",
			browserName: "Google Chrome",
			profile: { id: "a".repeat(32), name: "Personal", default: true, root: profileRoot },
			includeCookies: true,
			includeHistory: true,
			staging,
			budgetUsed: 0,
			decryptor: { platform: "darwin", key: Buffer.from("lab-password") },
			now: new Date("2026-06-01T00:00:00.000Z"),
			...overrides,
		}],
	};
}

// Stand-in for an Electron utility process: a real child process whose IPC
// uses structured clone, so the reader is exercised against real spawn,
// serialization, exit and kill. Like UtilityProcess.kill(), kill() does
// nothing until the process has spawned.
async function standInProcess(body: string): Promise<{ spawn: () => SourceReadProcess; children: ChildProcess[] }> {
	const script = path.join(await fixtureRoot(), "stand-in.cjs");
	await writeFile(script, body);
	const children: ChildProcess[] = [];
	return {
		children,
		spawn: () => {
			const child = fork(script, [], { serialization: "advanced", silent: true });
			children.push(child);
			let spawned = false;
			child.once("spawn", () => {
				spawned = true;
			});
			return {
				postMessage: (message) => void child.send(message as never),
				kill: () => spawned && child.kill(),
				once: (event: string, listener: (...args: unknown[]) => void) => child.once(event, listener),
			} as SourceReadProcess;
		},
	};
}

function exited(child: ChildProcess): boolean {
	return child.exitCode !== null || child.signalCode !== null;
}

describe("browser import worker requests", () => {
	it("reads a Chromium profile from a structured-cloned request without changing the source", async () => {
		const root = await fixtureRoot();
		const profileRoot = await createChromeProfile(path.join(root, "chrome"));
		const before = await sourceDigest(profileRoot);
		const staging = path.join(root, "ao-state", "staging");

		// The worker receives a clone: the decryptor key arrives as a plain
		// Uint8Array and the Date stays a Date.
		const response = await answerSourceReadRequest(structuredClone(profileReadRequest(profileRoot, staging)));

		expect(response).toMatchObject({ ok: true });
		if (!response.ok) throw new Error(response.message);
		expect(structuredClone(response)).toEqual(response);
		const value = response.value as { cookies: Array<{ name: string; value: string }>; history: unknown[]; budgetUsed: number };
		expect(value.cookies.map(({ name, value: cookieValue }) => [name, cookieValue]).sort()).toEqual([
			["plain", "visible"],
			["sealed", "decrypted"],
		]);
		expect(value.history).toEqual([expect.objectContaining({ url: "https://github.com/openai", title: "OpenAI", visitCount: 4 })]);
		expect(value.budgetUsed).toBeGreaterThan(0);
		expect(await sourceDigest(profileRoot)).toEqual(before);
	});

	it("carries the import size budget across profile reads", async () => {
		const root = await fixtureRoot();
		const profileRoot = await createChromeProfile(path.join(root, "chrome"));
		const staging = path.join(root, "ao-state", "staging");

		const response = await answerSourceReadRequest(profileReadRequest(profileRoot, staging, { budgetUsed: 512 * 1024 * 1024 - 1 }));

		expect(response).toEqual({ ok: false, message: "Selected browser data exceeds the import size limit." });
	});

	it("returns a failed read's message and error code", async () => {
		const root = await fixtureRoot();
		const profileRoot = await createChromeProfile(path.join(root, "chrome"));
		const blocker = path.join(root, "not-a-directory");
		await writeFile(blocker, "");

		const response = await answerSourceReadRequest(profileReadRequest(profileRoot, path.join(blocker, "staging")));

		expect(response).toMatchObject({ ok: false, code: "ENOTDIR", message: expect.stringContaining("ENOTDIR") });
	});

	it("rejects an unknown read", async () => {
		const response = await answerSourceReadRequest({ name: "readPasswords", args: [] } as unknown as SourceReadRequest);

		expect(response).toMatchObject({ ok: false });
	});
});

describe("processSourceReader", () => {
	it("resolves the process's value and stops the process", async () => {
		const { spawn, children } = await standInProcess(`process.once("message", (request) => process.send({ ok: true, value: request }));`);
		const request = profileReadRequest("/source/profile", "/state/staging");

		const value = await processSourceReader(spawn)(request) as unknown as SourceReadRequest<"readProfileData">;

		expect(value.args[0].now).toEqual(request.args[0].now);
		expect([...value.args[0].decryptor!.key!]).toEqual([...Buffer.from("lab-password")]);
		expect(children).toHaveLength(1);
		expect(exited(children[0]!)).toBe(true);
	});

	it("rejects with the message and code of a failed read", async () => {
		const { spawn } = await standInProcess(
			`process.once("message", () => process.send({ ok: false, message: "database is locked", code: "SQLITE_BUSY" }));`,
		);

		await expect(processSourceReader(spawn)(profileReadRequest("/source", "/staging"))).rejects.toMatchObject({ message: "database is locked", code: "SQLITE_BUSY" });
	});

	it.each([
		["crashes", `process.once("message", () => { throw new Error("reader exploded"); });`],
		["exits without answering", `process.once("message", () => process.exit(3));`],
	])("rejects when the process %s", async (_case, body) => {
		const { spawn, children } = await standInProcess(body);

		await expect(processSourceReader(spawn)(profileReadRequest("/source", "/staging"))).rejects.toThrow("stopped before it finished");
		expect(exited(children[0]!)).toBe(true);
	});

	it("stops a running read when the import is canceled", async () => {
		const { spawn, children } = await standInProcess(`process.once("message", () => { process.stdout.write("reading"); setInterval(() => undefined, 1_000); });`);
		const controller = new AbortController();
		const reading = processSourceReader(spawn)(profileReadRequest("/source", "/staging"), controller.signal);
		await vi.waitFor(() => expect(children).toHaveLength(1));
		await once(children[0]!.stdout!, "data");

		controller.abort();

		await expect(reading).rejects.toThrow("app is closing");
		expect(exited(children[0]!)).toBe(true);
	});

	it("stops a process that is canceled before it has spawned", async () => {
		const { spawn, children } = await standInProcess(`process.once("message", () => setInterval(() => undefined, 1_000));`);
		const controller = new AbortController();
		const reading = processSourceReader(() => {
			const child = spawn();
			controller.abort();
			return child;
		})(profileReadRequest("/source", "/staging"), controller.signal);

		await expect(reading).rejects.toThrow("app is closing");
		expect(exited(children[0]!)).toBe(true);
	});

	it("does not start a process for a canceled import", async () => {
		const spawn = vi.fn<() => SourceReadProcess>();
		const controller = new AbortController();
		controller.abort();

		await expect(processSourceReader(spawn)(profileReadRequest("/source", "/staging"), controller.signal)).rejects.toThrow("app is closing");
		expect(spawn).not.toHaveBeenCalled();
	});
});

describe("BrowserProfileImportService source reads", () => {
	it("sends only cloneable reads with the import signal and imports the cloned results", async () => {
		const root = await fixtureRoot();
		await createChromeProfile(path.join(root, "config", "google-chrome"));
		const stateDir = path.join(root, "ao-state");
		const profileStore = new BrowserProfileStore({ stateDir });
		await profileStore.load();
		const calls: Array<{ request: SourceReadRequest; signal?: AbortSignal }> = [];
		// Stand-in for the worker boundary: requests and results are cloned.
		const readSource: SourceReader = async (request, signal) => {
			calls.push({ request: structuredClone(request), signal });
			return structuredClone(await readSourceInProcess(structuredClone(request)));
		};
		const setCookie = vi.fn(async () => undefined);
		const service = new BrowserProfileImportService({
			stateDir,
			profileStore,
			historyStore: new BrowserHistoryStore({ stateDir }),
			platform: "linux",
			homeDir: root,
			env: { XDG_CONFIG_HOME: path.join(root, "config") },
			fromPartition: () => ({ cookies: { set: setCookie }, clearStorageData: async () => undefined, clearCache: async () => undefined }),
			readSource,
		});
		const source = (await service.discover()).sources.find((candidate) => candidate.family === "chromium")!;

		const result = await service.import({
			requestId: "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1",
			sourceId: source.id,
			profileIds: [source.profiles[0]!.id],
			includeCookies: true,
			includeHistory: true,
			destination: { mode: "merge", name: "From worker" },
		}, vi.fn());

		expect(calls.map(({ request }) => request.name)).toEqual(["readProfileData"]);
		expect(calls[0]!.signal).toBeInstanceOf(AbortSignal);
		expect(result.entries[0]).toMatchObject({ importedCookies: 1, importedHistoryEntries: 1 });
		expect(setCookie).toHaveBeenCalledWith(expect.objectContaining({ name: "plain", value: "visible" }));
		expect(await readdir(path.join(stateDir, "browser-import-staging"))).toEqual([]);
	});

	it("rolls back and cleans staging when the import is canceled during a read", async () => {
		const root = await fixtureRoot();
		await createChromeProfile(path.join(root, "local", "Google", "Chrome", "User Data"));
		const stateDir = path.join(root, "ao-state");
		const profileStore = new BrowserProfileStore({ stateDir });
		await profileStore.load();
		let reading!: () => void;
		const readStarted = new Promise<void>((resolve) => {
			reading = resolve;
		});
		const readSource: SourceReader = (_request, signal) => new Promise((_resolve, reject) => {
			reading();
			signal?.addEventListener("abort", () => reject(new Error("Browser import was canceled because the app is closing.")));
		});
		const service = new BrowserProfileImportService({
			stateDir,
			profileStore,
			historyStore: new BrowserHistoryStore({ stateDir }),
			platform: "win32",
			homeDir: root,
			env: { LOCALAPPDATA: path.join(root, "local") },
			fromPartition: vi.fn(),
			readSource,
		});
		const source = (await service.discover()).sources[0]!;
		const importing = service.import({
			requestId: "b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2",
			sourceId: source.id,
			profileIds: [source.profiles[0]!.id],
			includeCookies: false,
			includeHistory: true,
			destination: { mode: "merge", name: "Interrupted read" },
		}, vi.fn());
		await readStarted;

		const disposing = service.dispose();

		await expect(importing).rejects.toThrow("app is closing");
		await disposing;
		expect(profileStore.profiles).toEqual([]);
		await expect(stat(path.join(stateDir, "browser-import-staging"))).rejects.toMatchObject({ code: "ENOENT" });
	});

	it("reads Safari profile names through the reader and keeps defaults when that read fails", async () => {
		const root = await fixtureRoot();
		const library = path.join(root, "Library");
		await mkdir(path.join(library, "Safari"), { recursive: true });
		const history = new Database(path.join(library, "Safari", "History.db"));
		history.exec("CREATE TABLE history_items (id INTEGER PRIMARY KEY, url TEXT, visit_count INTEGER)");
		history.close();
		const stateDir = path.join(root, "ao-state");
		const readSource = vi.fn<SourceReader>(async () => {
			throw new Error("worker unavailable");
		});
		const service = new BrowserProfileImportService({
			stateDir,
			profileStore: new BrowserProfileStore({ stateDir }),
			historyStore: new BrowserHistoryStore({ stateDir }),
			platform: "darwin",
			homeDir: root,
			env: {},
			fromPartition: vi.fn(),
			readSource,
		});
		const deferred = (await service.discover()).sources.find((candidate) => candidate.family === "safari")!;

		const discovery = await service.discover({ sourceId: deferred.id });

		expect(readSource).toHaveBeenCalledWith(expect.objectContaining({ name: "readSafariProfileNames" }));
		expect(discovery.sources[0]?.profiles).toEqual([{ id: expect.stringMatching(/^[a-f0-9]{32}$/), name: "Personal", default: true }]);
		expect(JSON.stringify(discovery)).not.toContain(root);
	});
});
