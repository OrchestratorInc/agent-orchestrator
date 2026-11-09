import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearCloudArtifactAccess, cloudArtifactAccess, cloudArtifactsBackend } from "./artifacts";
import type { CloudClient } from "./client";
import { CloudError } from "./errors";

const BASE = "https://cloud.example";
const inMinutes = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString();

function client(overrides: Partial<Record<keyof CloudClient, unknown>> = {}) {
	let token = 0;
	return {
		baseUrl: BASE,
		createArtifactAccess: vi.fn(async () => ({ filesPrefix: `/api/cloud/v1/artifact-files/t${++token}/`, expiresAt: inMinutes(10) })),
		...overrides,
	} as unknown as CloudClient & { createArtifactAccess: ReturnType<typeof vi.fn> };
}

beforeEach(() => clearCloudArtifactAccess());
afterEach(() => vi.unstubAllGlobals());

describe("a cloud session's artifact access", () => {
	it("asks once however many screens want it, and keeps it while it is good", async () => {
		const cloud = client();
		const [a, b] = await Promise.all([cloudArtifactAccess(cloud, "org", "s1"), cloudArtifactAccess(cloud, "org", "s1")]);
		expect(a.root).toBe(`${BASE}/api/cloud/v1/artifact-files/t1/`);
		expect(b).toBe(a);
		expect((await cloudArtifactAccess(cloud, "org", "s1")).root).toBe(a.root);
		expect(cloud.createArtifactAccess).toHaveBeenCalledTimes(1);
		expect(cloud.createArtifactAccess).toHaveBeenCalledWith("org", "s1");
	});

	it("asks again for one nearly spent, or when told to renew", async () => {
		const cloud = client({ createArtifactAccess: vi.fn().mockResolvedValueOnce({ filesPrefix: "/api/cloud/v1/artifact-files/old/", expiresAt: inMinutes(0.2) }).mockResolvedValue({ filesPrefix: "/api/cloud/v1/artifact-files/new/", expiresAt: inMinutes(10) }) });
		await cloudArtifactAccess(cloud, "org", "s1");
		expect((await cloudArtifactAccess(cloud, "org", "s1")).root).toContain("/new/");
		expect((await cloudArtifactAccess(cloud, "org", "s1", { renew: true })).root).toContain("/new/");
		expect(cloud.createArtifactAccess).toHaveBeenCalledTimes(3);
	});
});

describe("a cloud session's artifacts backend", () => {
	const ROOT = `${BASE}/api/cloud/v1/artifact-files/t0/`;
	const backend = (cloud: CloudClient, canChange = true) => cloudArtifactsBackend({ client: cloud, orgId: "org", sessionId: "s1", root: ROOT, canChange, renew: () => {} });

	// No header: the signed prefix is the credential, so a web view and an image load it as they are.
	it("loads files from the signed prefix with each segment escaped", () => {
		const files = backend(client());
		expect(files.webSource("s1", "report/Q3 summary.html")).toEqual({ uri: `${ROOT}report/Q3%20summary.html` });
		expect(files.imageSource("s1", "chart.png", "2026-11-08T10:00:00Z")).toEqual({ uri: `${ROOT}chart.png?v=2026-11-08T10%3A00%3A00Z` });
		expect(files.webSource("s1", "../escape.html")).toBeUndefined();
		expect(files.markdownImageSource("s1", "docs/readme.md", "img/a.png")).toEqual({ uri: `${ROOT}docs/img/a.png` });
		expect(files.markdownImageSource("s1", "docs/readme.md", "https://example.com/a.png")).toEqual({ uri: "https://example.com/a.png" });
	});

	it("reads a file's text, and tries once more with fresh access when the old one is refused", async () => {
		const cloud = client();
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(new Response("expired", { status: 401 }))
			.mockResolvedValueOnce(new Response("# Notes", { status: 200 }));
		vi.stubGlobal("fetch", fetchMock);
		expect(await backend(cloud).text("s1", "notes.md")).toBe("# Notes");
		expect(fetchMock.mock.calls[0]?.[0]).toBe(`${ROOT}notes.md`);
		expect(fetchMock.mock.calls[1]?.[0]).toBe(`${BASE}/api/cloud/v1/artifact-files/t1/notes.md`);
	});

	it("says a removed file is gone", async () => {
		vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 404 })));
		await expect(backend(client()).text("s1", "old.md")).rejects.toThrow("no longer in this session's artifacts");
	});

	it("lists the session's files newest first and reads their versions", async () => {
		const files = backend(client({
			listSessionArtifacts: vi.fn(async () => ({ artifacts: [
				{ path: "a.md", name: "a.md", kind: "markdown", size: 3, updatedAt: "2026-11-08T09:00:00Z" },
				{ path: "b.html", name: "b.html", kind: "html", size: 9, updatedAt: "2026-11-08T10:00:00Z" },
				{ path: "../bad", name: "bad", kind: "file", size: 1, updatedAt: "2026-11-08T11:00:00Z" },
			] })),
			listArtifactVersions: vi.fn(async () => ({ versions: [{ version: 2, createdAt: "2026-11-08T10:00:00Z", size: 9, source: "agent" }, { version: 1, createdAt: "2026-11-08T09:00:00Z", size: 4, source: "user" }] })),
			getArtifactVersion: vi.fn(async () => ({ version: 1, createdAt: "2026-11-08T09:00:00Z", size: 4, source: "user", path: "b.html", content: "<p>", truncated: false })),
		}));
		expect((await files.list("s1")).map((file) => file.path)).toEqual(["b.html", "a.md"]);
		expect((await files.versions("s1", "b.html")).map((version) => version.version)).toEqual([2, 1]);
		expect((await files.version("s1", "b.html", 1)).content).toBe("<p>");
		expect(files.syncVersions).toBeUndefined();
	});

	it("deletes and restores through the control plane, and says a refusal in its words", async () => {
		const deleteSessionArtifact = vi.fn(async () => undefined);
		const restoreArtifactVersion = vi.fn(async () => { throw new CloudError("Only the session's creator can change its artifacts.", { status: 403, code: "artifact_not_owner" }); });
		const files = backend(client({ deleteSessionArtifact, restoreArtifactVersion }), false);
		expect(files.canChange).toBe(false);
		await files.remove("s1", "a.md");
		expect(deleteSessionArtifact).toHaveBeenCalledWith("org", "s1", "a.md");
		const failure = await files.restore("s1", "a.md", 1).catch((error: unknown) => error);
		expect(failure).not.toHaveProperty("status");
		expect((failure as Error).message).toBeTruthy();
	});
});
