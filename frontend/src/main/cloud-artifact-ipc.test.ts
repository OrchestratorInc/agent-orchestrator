import { describe, expect, it, vi } from "vitest";
import { fetchCloudArtifact, MAX_CLOUD_ARTIFACT_BYTES, registerCloudArtifactIpc } from "./cloud-artifact-ipc";

const FILE_URL = "https://cp.test/api/cloud/v1/artifact-files/token/report.pdf?v=1";

function session(response: Response) {
	return { fetch: vi.fn(async () => response) };
}

describe("fetchCloudArtifact", () => {
	it("returns the file's bytes", async () => {
		const source = session(new Response(new Uint8Array([37, 80, 68, 70]), { headers: { "content-type": "application/pdf" } }));
		await expect(fetchCloudArtifact(source, FILE_URL)).resolves.toEqual(new Uint8Array([37, 80, 68, 70]));
		expect(source.fetch).toHaveBeenCalledWith(FILE_URL);
	});

	it("reads only the control plane's artifact file route", async () => {
		const ok = session(new Response("x"));
		await expect(fetchCloudArtifact(ok, "not a url")).rejects.toThrow("Invalid artifact URL");
		await expect(fetchCloudArtifact(ok, "file:///api/cloud/v1/artifact-files/t/a")).rejects.toThrow("Not a cloud artifact URL");
		await expect(fetchCloudArtifact(ok, "https://cp.test/api/cloud/v1/orgs/o/sessions")).rejects.toThrow("Not a cloud artifact URL");
		expect(ok.fetch).not.toHaveBeenCalled();
	});

	it("refuses failures and oversized files", async () => {
		await expect(fetchCloudArtifact(session(new Response("", { status: 401 })), FILE_URL)).rejects.toThrow("401");
		const huge = new Response("x", { headers: { "content-length": String(MAX_CLOUD_ARTIFACT_BYTES + 1) } });
		await expect(fetchCloudArtifact(session(huge), FILE_URL)).rejects.toThrow("too large");
	});

	it("reads through the sender's session", async () => {
		const registered = new Map<string, (event: unknown, ...args: unknown[]) => unknown>();
		registerCloudArtifactIpc({ ipcMain: { handle: vi.fn((channel: string, handler: (event: unknown, ...args: unknown[]) => unknown) => registered.set(channel, handler)) } });
		const source = session(new Response(new Uint8Array([1])));
		await expect(registered.get("cloudArtifact:fetch")?.({ sender: { session: source } }, FILE_URL)).resolves.toEqual(new Uint8Array([1]));
	});
});

describe("copyCloudArtifactsToFolder", () => {
	const SESSION = "11111111-2222-3333-4444-555555555555";

	async function dataDir() {
		const { mkdtemp } = await import("node:fs/promises");
		const { tmpdir } = await import("node:os");
		const { join } = await import("node:path");
		return mkdtemp(join(tmpdir(), "ao-cloud-artifacts-"));
	}

	/** A response whose body can only be streamed, never buffered whole. */
	function streamedResponse(text: string, headers?: HeadersInit): Response {
		const response = new Response(text, { headers });
		const buffered = () => Promise.reject(new Error("buffered the whole file"));
		return Object.assign(response, { arrayBuffer: buffered, blob: buffered, text: buffered, bytes: buffered });
	}

	it("copies the files under ~/.ao and replaces an earlier copy", async () => {
		const { readFile, readdir } = await import("node:fs/promises");
		const { join } = await import("node:path");
		const { copyCloudArtifactsToFolder } = await import("./cloud-artifact-ipc");
		const dir = await dataDir();
		const env = { AO_DATA_DIR: dir };
		const bodies: Record<string, string> = { [`${FILE_URL}&a`]: "# a", [`${FILE_URL}&b`]: "b" };
		const source = { fetch: vi.fn(async (url: string) => streamedResponse(bodies[url])) };
		const first = await copyCloudArtifactsToFolder(source, { sessionId: SESSION, files: [{ path: "a.md", url: `${FILE_URL}&a` }, { path: "old.txt", url: `${FILE_URL}&b` }] }, env);
		expect(first).toBe(join(dir, "cloud-artifacts", SESSION));
		const revealed = await copyCloudArtifactsToFolder(source, { sessionId: SESSION, files: [{ path: "dir/a.md", url: `${FILE_URL}&a` }], reveal: "dir/a.md" }, env);
		expect(revealed).toBe(join(first, "dir", "a.md"));
		expect(await readFile(revealed, "utf8")).toBe("# a");
		expect(await readdir(first)).toEqual(["dir"]);
		// No staging folder is left beside the session's folder.
		expect(await readdir(join(dir, "cloud-artifacts"))).toEqual([SESSION]);
	});

	it("downloads at most a few files at once", async () => {
		const { readdir } = await import("node:fs/promises");
		const { CLOUD_ARTIFACT_COPY_CONCURRENCY, cloudArtifactsFolder, copyCloudArtifactsToFolder } = await import("./cloud-artifact-ipc");
		const env = { AO_DATA_DIR: await dataDir() };
		let active = 0;
		let peak = 0;
		const source = {
			fetch: vi.fn(async () => {
				active += 1;
				peak = Math.max(peak, active);
				await new Promise((resolve) => setTimeout(resolve, 5));
				active -= 1;
				return streamedResponse("x");
			}),
		};
		const files = Array.from({ length: 10 }, (_, index) => ({ path: `f${index}.txt`, url: `${FILE_URL}&${index}` }));
		await copyCloudArtifactsToFolder(source, { sessionId: SESSION, files }, env);
		expect(source.fetch).toHaveBeenCalledTimes(10);
		expect(peak).toBe(CLOUD_ARTIFACT_COPY_CONCURRENCY);
		expect((await readdir(cloudArtifactsFolder(SESSION, env))).sort()).toEqual(files.map((file) => file.path).sort());
	});

	it("keeps the earlier copy when a file fails or grows past the limit", async () => {
		const { readFile, readdir } = await import("node:fs/promises");
		const { join } = await import("node:path");
		const { copyCloudArtifactsToFolder } = await import("./cloud-artifact-ipc");
		const dir = await dataDir();
		const env = { AO_DATA_DIR: dir };
		const folder = await copyCloudArtifactsToFolder({ fetch: vi.fn(async () => streamedResponse("kept")) }, { sessionId: SESSION, files: [{ path: "a.md", url: FILE_URL }] }, env);
		const failing = { fetch: vi.fn(async () => new Response("", { status: 403 })) };
		await expect(copyCloudArtifactsToFolder(failing, { sessionId: SESSION, files: [{ path: "b.md", url: FILE_URL }] }, env)).rejects.toThrow("403");
		// A body with no declared length is cut off once it passes the limit.
		const oversized = { fetch: vi.fn(async () => new Response(new Uint8Array(MAX_CLOUD_ARTIFACT_BYTES + 1))) };
		await expect(copyCloudArtifactsToFolder(oversized, { sessionId: SESSION, files: [{ path: "big.bin", url: FILE_URL }] }, env)).rejects.toThrow("too large");
		expect(await readFile(join(folder, "a.md"), "utf8")).toBe("kept");
		expect(await readdir(join(dir, "cloud-artifacts"))).toEqual([SESSION]);
	});

	it("lets two copies of one session finish one after the other", async () => {
		const { readdir } = await import("node:fs/promises");
		const { copyCloudArtifactsToFolder } = await import("./cloud-artifact-ipc");
		const env = { AO_DATA_DIR: await dataDir() };
		const source = { fetch: vi.fn(async () => streamedResponse("x")) };
		const [first, second] = await Promise.all([
			copyCloudArtifactsToFolder(source, { sessionId: SESSION, files: [{ path: "a.md", url: FILE_URL }] }, env),
			copyCloudArtifactsToFolder(source, { sessionId: SESSION, files: [{ path: "b.md", url: FILE_URL }] }, env),
		]);
		expect(first).toBe(second);
		expect(await readdir(first)).toEqual(["b.md"]);
	});

	it("refuses a path outside the folder and an invalid session", async () => {
		const { copyCloudArtifactsToFolder } = await import("./cloud-artifact-ipc");
		const source = { fetch: vi.fn(async () => new Response("x")) };
		await expect(copyCloudArtifactsToFolder(source, { sessionId: SESSION, files: [{ path: "../escape", url: FILE_URL }] }, { AO_DATA_DIR: "/tmp/ao-x" })).rejects.toThrow("Invalid artifact path");
		await expect(copyCloudArtifactsToFolder(source, { sessionId: "../x", files: [] })).rejects.toThrow("Invalid session");
		expect(source.fetch).not.toHaveBeenCalled();
	});
});

describe("pruneCloudArtifactFolders", () => {
	it("removes session copies older than a few days and abandoned staging folders only", async () => {
		const { mkdir, mkdtemp, readdir, utimes, writeFile } = await import("node:fs/promises");
		const { tmpdir } = await import("node:os");
		const { join } = await import("node:path");
		const { CLOUD_ARTIFACT_COPY_MAX_AGE_MS, pruneCloudArtifactFolders } = await import("./cloud-artifact-ipc");
		const dir = await mkdtemp(join(tmpdir(), "ao-cloud-artifacts-prune-"));
		const root = join(dir, "cloud-artifacts");
		const now = Date.now();
		const age = async (name: string, ms: number, file = false) => {
			const target = join(root, name);
			if (file) await writeFile(target, "x");
			else await mkdir(target, { recursive: true });
			const when = new Date(now - ms);
			await utimes(target, when, when);
		};
		const stale = "11111111-2222-3333-4444-555555555555";
		const fresh = "22222222-2222-3333-4444-555555555555";
		await age(stale, CLOUD_ARTIFACT_COPY_MAX_AGE_MS + 60_000);
		await age(fresh, 60_000);
		await age(`.staging-${fresh}-abc`, 2 * 60 * 60 * 1000);
		await age(`.staging-${fresh}-new`, 1_000);
		await age("not-ours", CLOUD_ARTIFACT_COPY_MAX_AGE_MS * 2);
		await age("aaaaaaaa-2222-3333-4444-555555555555", CLOUD_ARTIFACT_COPY_MAX_AGE_MS * 2, true);

		const removed = await pruneCloudArtifactFolders({ AO_DATA_DIR: dir }, now);
		expect(removed).toEqual([`.staging-${fresh}-abc`, stale]);
		expect((await readdir(root)).sort()).toEqual([`.staging-${fresh}-new`, fresh, "aaaaaaaa-2222-3333-4444-555555555555", "not-ours"].sort());
	});

	it("does nothing when no copy was ever made", async () => {
		const { mkdtemp } = await import("node:fs/promises");
		const { tmpdir } = await import("node:os");
		const { join } = await import("node:path");
		const { pruneCloudArtifactFolders } = await import("./cloud-artifact-ipc");
		await expect(pruneCloudArtifactFolders({ AO_DATA_DIR: await mkdtemp(join(tmpdir(), "ao-empty-")) })).resolves.toEqual([]);
	});
});

describe("cloudArtifact:openFolder in the browser", () => {
	const SESSION = "11111111-2222-3333-4444-555555555555";

	async function register() {
		const { mkdtemp } = await import("node:fs/promises");
		const { tmpdir } = await import("node:os");
		const { join } = await import("node:path");
		vi.stubEnv("AO_DATA_DIR", await mkdtemp(join(tmpdir(), "ao-cloud-browser-")));
		const registered = new Map<string, (event: unknown, ...args: unknown[]) => unknown>();
		const openInBrowser = vi.fn();
		const openInFileManager = vi.fn();
		registerCloudArtifactIpc({
			ipcMain: { handle: vi.fn((channel: string, handler: (event: unknown, ...args: unknown[]) => unknown) => registered.set(channel, handler)) },
			openInBrowser,
			openInFileManager,
		});
		const source = { fetch: vi.fn(async (_url: string) => new Response("<p>page</p>")) };
		const open = (input: unknown) => registered.get("cloudArtifact:openFolder")?.({ sender: { session: source } }, input);
		return { open, openInBrowser, openInFileManager, source };
	}

	it("opens the copied page, with the session's other files beside it", async () => {
		const { join } = await import("node:path");
		const { cloudArtifactsFolder } = await import("./cloud-artifact-ipc");
		const { open, openInBrowser, openInFileManager } = await register();
		await open({ sessionId: SESSION, files: [{ path: "report.html", url: `${FILE_URL}&a` }, { path: "chart.png", url: `${FILE_URL}&b` }], reveal: "report.html", open: "browser" });
		// Its own copy, apart from the folder "Show in" replaces.
		expect(openInBrowser).toHaveBeenCalledExactlyOnceWith(join(cloudArtifactsFolder(SESSION, process.env, true), "report.html"));
		expect(openInFileManager).not.toHaveBeenCalled();
		vi.unstubAllEnvs();
	});

	it("opens the page when a sibling file fails to copy, and names the page when it fails itself", async () => {
		const { readdir } = await import("node:fs/promises");
		const { cloudArtifactsFolder } = await import("./cloud-artifact-ipc");
		const { open, openInBrowser, source } = await register();
		source.fetch.mockImplementation(async (url: string) => url.endsWith("&big") ? new Response("", { status: 500 }) : new Response("<p>page</p>"));
		await open({ sessionId: SESSION, files: [{ path: "report.html", url: `${FILE_URL}&a` }, { path: "huge.mov", url: `${FILE_URL}&big` }], reveal: "report.html", open: "browser" });
		expect(openInBrowser).toHaveBeenCalledOnce();
		expect(await readdir(cloudArtifactsFolder(SESSION, process.env, true))).toEqual(["report.html"]);
		await expect(open({ sessionId: SESSION, files: [{ path: "report.html", url: `${FILE_URL}&big` }], reveal: "report.html", open: "browser" })).rejects.toThrow("Couldn't copy report.html");
		vi.unstubAllEnvs();
	});

	it("refuses a file a browser does not render, before copying anything", async () => {
		const { open, openInBrowser, source } = await register();
		await expect(open({ sessionId: SESSION, files: [{ path: "run.sh", url: `${FILE_URL}&a` }], reveal: "run.sh", open: "browser" })).rejects.toThrow("does not open in a browser");
		await expect(open({ sessionId: SESSION, files: [{ path: "a.html", url: `${FILE_URL}&a` }], open: "browser" })).rejects.toThrow("does not open in a browser");
		expect(source.fetch).not.toHaveBeenCalled();
		expect(openInBrowser).not.toHaveBeenCalled();
		vi.unstubAllEnvs();
	});
});
