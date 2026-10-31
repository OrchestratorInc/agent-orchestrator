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

	it("copies the files under ~/.ao and replaces an earlier copy", async () => {
		const { mkdtemp, readFile, readdir } = await import("node:fs/promises");
		const { tmpdir } = await import("node:os");
		const { join } = await import("node:path");
		const { copyCloudArtifactsToFolder } = await import("./cloud-artifact-ipc");
		const dataDir = await mkdtemp(join(tmpdir(), "ao-cloud-artifacts-"));
		const env = { AO_DATA_DIR: dataDir };
		const bodies: Record<string, string> = { [`${FILE_URL}&a`]: "# a", [`${FILE_URL}&b`]: "b" };
		const source = { fetch: vi.fn(async (url: string) => new Response(bodies[url])) };
		const first = await copyCloudArtifactsToFolder(source, { sessionId: SESSION, files: [{ path: "a.md", url: `${FILE_URL}&a` }, { path: "old.txt", url: `${FILE_URL}&b` }] }, env);
		expect(first).toBe(join(dataDir, "cloud-artifacts", SESSION));
		const revealed = await copyCloudArtifactsToFolder(source, { sessionId: SESSION, files: [{ path: "dir/a.md", url: `${FILE_URL}&a` }], reveal: "dir/a.md" }, env);
		expect(revealed).toBe(join(first, "dir", "a.md"));
		expect(await readFile(revealed, "utf8")).toBe("# a");
		expect(await readdir(first)).toEqual(["dir"]);
	});

	it("refuses a path outside the folder and an invalid session", async () => {
		const { copyCloudArtifactsToFolder } = await import("./cloud-artifact-ipc");
		const source = { fetch: vi.fn(async () => new Response("x")) };
		await expect(copyCloudArtifactsToFolder(source, { sessionId: SESSION, files: [{ path: "../escape", url: FILE_URL }] }, { AO_DATA_DIR: "/tmp/ao-x" })).rejects.toThrow("Invalid artifact path");
		await expect(copyCloudArtifactsToFolder(source, { sessionId: "../x", files: [] })).rejects.toThrow("Invalid session");
		expect(source.fetch).not.toHaveBeenCalled();
	});
});
