import { mkdtemp, readdir, readFile, rm, stat, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { abortCloudArtifactSaves, cloudArtifactFileName, registerCloudArtifactIpc, saveCloudArtifact } from "./cloud-artifact-ipc";

const FILE_URL = "https://cp.test/api/cloud/v1/artifact-files/token/report.pdf?v=1";

function session(response: Response | (() => Response)) {
	return { fetch: vi.fn(async () => (typeof response === "function" ? response() : response)) };
}

// A body that sends `first`, then nothing until the reader cancels it.
function hangingBody(first = new Uint8Array([1, 2, 3])) {
	const cancel = vi.fn();
	const body = new ReadableStream<Uint8Array>({
		start(controller) {
			controller.enqueue(first);
		},
		cancel,
	});
	return { body, cancel };
}

async function until(condition: () => Promise<boolean>) {
	for (let i = 0; i < 200; i++) {
		if (await condition()) return;
		await new Promise((resolve) => setTimeout(resolve, 10));
	}
	throw new Error("condition never held");
}

let dir: string;
beforeEach(async () => {
	dir = await mkdtemp(path.join(os.tmpdir(), "cloud-artifact-"));
});
afterEach(async () => {
	abortCloudArtifactSaves();
	await rm(dir, { recursive: true, force: true });
});

describe("saveCloudArtifact", () => {
	it("writes the file's bytes to the chosen path and leaves nothing else", async () => {
		const source = session(new Response(new Uint8Array([37, 80, 68, 70]), { headers: { "content-type": "application/pdf" } }));
		const target = path.join(dir, "report.pdf");
		await saveCloudArtifact(source, FILE_URL, target);
		expect(new Uint8Array(await readFile(target))).toEqual(new Uint8Array([37, 80, 68, 70]));
		expect(await readdir(dir)).toEqual(["report.pdf"]);
	});

	it("streams a file larger than the old 100 MiB cap without holding it in memory", async () => {
		const chunk = new Uint8Array(1024 * 1024).fill(7);
		const total = 600;
		let sent = 0;
		const body = new ReadableStream<Uint8Array>({
			pull(controller) {
				if (sent === total) return controller.close();
				sent += 1;
				controller.enqueue(chunk);
			},
		});
		const target = path.join(dir, "big.bin");
		await saveCloudArtifact(session(new Response(body)), FILE_URL, target);
		expect((await stat(target)).size).toBe(total * chunk.byteLength);
	});

	it("reads only the control plane's artifact file route", async () => {
		const ok = session(new Response("x"));
		const target = path.join(dir, "a");
		await expect(saveCloudArtifact(ok, "not a url", target)).rejects.toThrow("Invalid artifact URL");
		await expect(saveCloudArtifact(ok, "file:///api/cloud/v1/artifact-files/t/a", target)).rejects.toThrow("Not a cloud artifact URL");
		await expect(saveCloudArtifact(ok, "https://cp.test/api/cloud/v1/orgs/o/sessions", target)).rejects.toThrow("Not a cloud artifact URL");
		expect(ok.fetch).not.toHaveBeenCalled();
	});

	it("leaves no file behind when the request fails", async () => {
		const target = path.join(dir, "report.pdf");
		await expect(saveCloudArtifact(session(new Response("", { status: 401 })), FILE_URL, target)).rejects.toThrow("401");
		expect(await readdir(dir)).toEqual([]);
	});

	it("keeps an existing file and removes its partial file when the stream breaks", async () => {
		const target = path.join(dir, "report.pdf");
		await writeFile(target, "previous");
		const body = new ReadableStream<Uint8Array>({
			start(controller) {
				controller.enqueue(new Uint8Array([1, 2, 3]));
				controller.error(new Error("connection reset"));
			},
		});
		await expect(saveCloudArtifact(session(new Response(body)), FILE_URL, target)).rejects.toThrow("connection reset");
		expect(await readFile(target, "utf8")).toBe("previous");
		expect(await readdir(dir)).toEqual(["report.pdf"]);
	});

	it("never touches an unrelated file that happens to be named <name>.part", async () => {
		const target = path.join(dir, "report.pdf");
		await writeFile(`${target}.part`, "someone else's");
		await saveCloudArtifact(session(new Response("new")), FILE_URL, target);
		await expect(saveCloudArtifact(session(new Response("", { status: 500 })), FILE_URL, target)).rejects.toThrow("500");
		expect(await readFile(`${target}.part`, "utf8")).toBe("someone else's");
		expect(await readFile(target, "utf8")).toBe("new");
	});

	it("gives concurrent saves to the same path their own partial files", async () => {
		const target = path.join(dir, "report.pdf");
		const a = new Uint8Array(4 * 1024 * 1024).fill(1);
		const b = new Uint8Array(4 * 1024 * 1024).fill(2);
		await Promise.all([saveCloudArtifact(session(new Response(a)), FILE_URL, target), saveCloudArtifact(session(new Response(b)), FILE_URL, target)]);
		const saved = new Uint8Array(await readFile(target));
		expect(saved.byteLength).toBe(a.byteLength);
		expect(saved.every((byte) => byte === saved[0])).toBe(true);
		expect(await readdir(dir)).toEqual(["report.pdf"]);
	});

	it("fails a stalled download instead of hanging, and cleans up", async () => {
		const { body, cancel } = hangingBody();
		const target = path.join(dir, "report.pdf");
		await expect(saveCloudArtifact(session(new Response(body)), FILE_URL, target, 50)).rejects.toThrow("stalled");
		expect(cancel).toHaveBeenCalled();
		expect(await readdir(dir)).toEqual([]);
	});
});

describe("cloudArtifactFileName", () => {
	it("keeps only a safe base name", () => {
		expect(cloudArtifactFileName("reports/q3.pdf")).toBe("q3.pdf");
		expect(cloudArtifactFileName("..\\..\\evil.sh")).toBe("evil.sh");
		expect(cloudArtifactFileName("..")).toBe("artifact");
		expect(cloudArtifactFileName("")).toBe("artifact");
	});
});

describe("registerCloudArtifactIpc", () => {
	function register(showSaveDialog: ReturnType<typeof vi.fn>, idleTimeoutMs?: number) {
		const registered = new Map<string, (event: unknown, ...args: unknown[]) => unknown>();
		registerCloudArtifactIpc({
			ipcMain: { handle: vi.fn((channel: string, handler: (event: unknown, ...args: unknown[]) => unknown) => registered.set(channel, handler)) },
			dialog: { showSaveDialog } as never,
			downloadsDirectory: () => dir,
			idleTimeoutMs,
		});
		return { save: registered.get("cloudArtifact:save")!, cancelSave: registered.get("cloudArtifact:cancelSave")! };
	}

	it("checks the response, asks where to save, then streams through the sender's session", async () => {
		const target = path.join(dir, "chosen.pdf");
		const showSaveDialog = vi.fn(async () => ({ canceled: false, filePath: target }));
		const { save } = register(showSaveDialog);
		const source = session(new Response(new Uint8Array([1])));
		await expect(save({ sender: { session: source } }, { url: FILE_URL, name: "../report.pdf", id: "a" })).resolves.toBe(true);
		expect(source.fetch).toHaveBeenCalledBefore(showSaveDialog);
		expect(showSaveDialog).toHaveBeenCalledWith({ defaultPath: path.join(dir, "report.pdf") });
		expect(new Uint8Array(await readFile(target))).toEqual(new Uint8Array([1]));
	});

	it("reports an expired link or a missing file before opening a dialog", async () => {
		const showSaveDialog = vi.fn();
		const { save } = register(showSaveDialog);
		await expect(save({ sender: { session: session(new Response("", { status: 401 })) } }, { url: FILE_URL, name: "x" })).rejects.toThrow("401");
		await expect(save({ sender: { session: session(new Response("", { status: 404 })) } }, { url: FILE_URL, name: "x" })).rejects.toThrow("404");
		await expect(save({ sender: { session: session(new Response("x")) } }, { url: "https://cp.test/api/cloud/v1/me", name: "x" })).rejects.toThrow("Not a cloud artifact URL");
		expect(showSaveDialog).not.toHaveBeenCalled();
	});

	it("drops the unread response when the user cancels the dialog", async () => {
		const { body, cancel } = hangingBody();
		const { save } = register(vi.fn(async () => ({ canceled: true, filePath: "" })));
		await expect(save({ sender: { session: session(new Response(body)) } }, { url: FILE_URL, name: "report.pdf" })).resolves.toBe(false);
		expect(cancel).toHaveBeenCalled();
		expect(await readdir(dir)).toEqual([]);
	});

	it("stops a download in progress on cancelSave and leaves no file", async () => {
		const { body } = hangingBody();
		const target = path.join(dir, "report.pdf");
		const { save, cancelSave } = register(vi.fn(async () => ({ canceled: false, filePath: target })));
		const pending = save({ sender: { session: session(new Response(body)) } }, { url: FILE_URL, name: "report.pdf", id: "dl-1" });
		await until(async () => (await readdir(dir)).some((name) => name.endsWith(".part")));
		await cancelSave({}, "dl-1");
		await expect(pending).resolves.toBe(false);
		expect(await readdir(dir)).toEqual([]);
	});

	it("removes partial files when the app quits mid-download", async () => {
		const { body } = hangingBody();
		const target = path.join(dir, "report.pdf");
		const { save } = register(vi.fn(async () => ({ canceled: false, filePath: target })));
		const pending = save({ sender: { session: session(new Response(body)) } }, { url: FILE_URL, name: "report.pdf", id: "dl-2" });
		await until(async () => (await readdir(dir)).some((name) => name.endsWith(".part")));
		abortCloudArtifactSaves();
		expect(await readdir(dir)).toEqual([]);
		await expect(pending).resolves.toBe(false);
	});
});
