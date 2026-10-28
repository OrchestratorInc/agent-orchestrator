import { mkdtemp, readdir, readFile, rm, stat, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cloudArtifactFileName, registerCloudArtifactIpc, saveCloudArtifact } from "./cloud-artifact-ipc";

const FILE_URL = "https://cp.test/api/cloud/v1/artifact-files/token/report.pdf?v=1";

function session(response: Response) {
	return { fetch: vi.fn(async () => response) };
}

let dir: string;
beforeEach(async () => {
	dir = await mkdtemp(path.join(os.tmpdir(), "cloud-artifact-"));
});
afterEach(async () => {
	await rm(dir, { recursive: true, force: true });
});

describe("saveCloudArtifact", () => {
	it("writes the file's bytes to the chosen path", async () => {
		const source = session(new Response(new Uint8Array([37, 80, 68, 70]), { headers: { "content-type": "application/pdf" } }));
		const target = path.join(dir, "report.pdf");
		await saveCloudArtifact(source, FILE_URL, target);
		expect(new Uint8Array(await readFile(target))).toEqual(new Uint8Array([37, 80, 68, 70]));
		expect(source.fetch).toHaveBeenCalledWith(FILE_URL);
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

	it("keeps an existing file and removes the partial one when the stream breaks", async () => {
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
	function register(showSaveDialog: ReturnType<typeof vi.fn>) {
		const registered = new Map<string, (event: unknown, ...args: unknown[]) => unknown>();
		registerCloudArtifactIpc({
			ipcMain: { handle: vi.fn((channel: string, handler: (event: unknown, ...args: unknown[]) => unknown) => registered.set(channel, handler)) },
			dialog: { showSaveDialog } as never,
			downloadsDirectory: () => dir,
		});
		return registered.get("cloudArtifact:save")!;
	}

	it("asks where to save, then streams through the sender's session", async () => {
		const target = path.join(dir, "chosen.pdf");
		const showSaveDialog = vi.fn(async () => ({ canceled: false, filePath: target }));
		const save = register(showSaveDialog);
		const source = session(new Response(new Uint8Array([1])));
		await expect(save({ sender: { session: source } }, { url: FILE_URL, name: "../report.pdf" })).resolves.toBe(true);
		expect(showSaveDialog).toHaveBeenCalledWith({ defaultPath: path.join(dir, "report.pdf") });
		expect(new Uint8Array(await readFile(target))).toEqual(new Uint8Array([1]));
	});

	it("reads nothing when the user cancels", async () => {
		const save = register(vi.fn(async () => ({ canceled: true, filePath: "" })));
		const source = session(new Response("x"));
		await expect(save({ sender: { session: source } }, { url: FILE_URL, name: "report.pdf" })).resolves.toBe(false);
		expect(source.fetch).not.toHaveBeenCalled();
	});

	it("refuses a link that is not an artifact file before opening a dialog", async () => {
		const showSaveDialog = vi.fn();
		const save = register(showSaveDialog);
		await expect(save({ sender: { session: session(new Response("x")) } }, { url: "https://cp.test/api/cloud/v1/me", name: "x" })).rejects.toThrow("Not a cloud artifact URL");
		expect(showSaveDialog).not.toHaveBeenCalled();
	});
});
