import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { forgetCloudAccountCaches } from "./accountCaches";
import { clearCloudArtifactAccess, cloudArtifactAccess, cloudArtifactsBackend, useCloudArtifactAccess } from "./artifacts";
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

	// The client is the same for every account, so the key can't tell accounts apart.
	it("forgets access when the Cloud account changes", async () => {
		const cloud = client();
		await cloudArtifactAccess(cloud, "org", "s1");
		forgetCloudAccountCaches();
		expect((await cloudArtifactAccess(cloud, "org", "s1")).root).toContain("/t2/");
		expect(cloud.createArtifactAccess).toHaveBeenCalledTimes(2);
	});

	it("keeps nothing that arrives after the account changed, and asks again under the new one", async () => {
		let answerOld!: (value: { filesPrefix: string; expiresAt: string }) => void;
		const cloud = client({ createArtifactAccess: vi.fn()
			.mockReturnValueOnce(new Promise((resolve) => { answerOld = resolve; }))
			.mockResolvedValue({ filesPrefix: "/api/cloud/v1/artifact-files/new-account/", expiresAt: inMinutes(10) }) });
		const asked = cloudArtifactAccess(cloud, "org", "s1");
		forgetCloudAccountCaches();
		answerOld({ filesPrefix: "/api/cloud/v1/artifact-files/old-account/", expiresAt: inMinutes(10) });
		expect((await asked).root).toContain("/new-account/");
		expect((await cloudArtifactAccess(cloud, "org", "s1")).root).toContain("/new-account/");
		expect(cloud.createArtifactAccess).toHaveBeenCalledTimes(2);
	});

	it("leaves a screen without access when its earlier request comes back after the organization changed", async () => {
		let answer!: (value: { filesPrefix: string; expiresAt: string }) => void;
		const cloud = client({ createArtifactAccess: vi.fn().mockReturnValueOnce(new Promise((resolve) => { answer = resolve; })) });
		let seen: ReturnType<typeof useCloudArtifactAccess> | undefined;
		function Probe({ orgId }: { orgId?: string }) {
			seen = useCloudArtifactAccess(cloud, orgId, "s1", true);
			return null;
		}
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(createElement(Probe, { orgId: "org" })); });
		// An account switch clears the organization until the next one resolves.
		act(() => renderer.update(createElement(Probe, {})));
		await act(async () => {
			answer({ filesPrefix: "/api/cloud/v1/artifact-files/old-account/", expiresAt: inMinutes(10) });
			await new Promise((resolve) => setTimeout(resolve, 0));
		});
		expect(seen?.root).toBeUndefined();
		act(() => renderer.unmount());
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
		// A native player or download reads the signed address with no header, versioned like an image.
		expect(files.streamSource("s1", "clip.mp4", "v1")).toEqual({ uri: `${ROOT}clip.mp4?v=v1` });
		expect(files.streamSource("s1", "../escape.mp4")).toBeUndefined();
		// Cloud tells no size or file start beforehand: the viewers fall back to the page and the download's own check.
		expect(files.size).toBeUndefined();
		expect(files.textWithin).toBeUndefined();
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

	// Cloud keeps no record of which file a link came from: a link names the
	// file's name and the committed version's sha256, as the desktop publishes it.
	it("shows the public link of a file its creator published, for the version on the link", async () => {
		const sha = (digit: string) => digit.repeat(64);
		const listArtifactShares = vi.fn(async () => ({ limit: 5, used: 3, shares: [
			{ id: "l1", name: "report.html", sha256: sha("a"), contentUrl: "https://l1.orchestratorusercontent.com/", viewerUrl: "https://orchestrator.inc/artifact/l1" },
			{ id: "l2", name: "notes.md", sha256: sha("b"), contentUrl: "https://l2.orchestratorusercontent.com/" },
			{ id: "l3", name: "old.md", sha256: sha("c"), contentUrl: "https://l3.orchestratorusercontent.com/", revokedAt: "2026-11-09T10:00:00Z" },
			{ id: "l4", name: "changed.md", sha256: sha("d"), contentUrl: "https://l4.orchestratorusercontent.com/" },
		] }));
		const listSessionArtifacts = vi.fn(async () => ({ artifacts: [
			{ path: "out/report.html", name: "report.html", kind: "html", size: 9, sha256: sha("a"), updatedAt: "2026-11-08T10:00:00Z" },
			{ path: "notes.md", name: "notes.md", kind: "markdown", size: 3, sha256: sha("b"), updatedAt: "2026-11-08T09:00:00Z" },
			{ path: "old.md", name: "old.md", kind: "markdown", size: 3, sha256: sha("c"), updatedAt: "2026-11-08T08:00:00Z" },
			// Committed again since it was published: the link still serves the older version.
			{ path: "changed.md", name: "changed.md", kind: "markdown", size: 3, sha256: sha("e"), updatedAt: "2026-11-08T07:00:00Z" },
		] }));
		const links = Object.fromEntries((await backend(client({ listSessionArtifacts, listArtifactShares })).list("s1")).map((file) => [file.path, file.shareUrl]));
		expect(links).toEqual({
			"out/report.html": "https://orchestrator.inc/artifact/l1",
			"notes.md": "https://l2.orchestratorusercontent.com/",
			"old.md": undefined,
			"changed.md": undefined,
		});
		// Only the session's creator publishes its files, so nobody else's list asks.
		listArtifactShares.mockClear();
		expect((await backend(client({ listSessionArtifacts, listArtifactShares }), false).list("s1")).every((file) => !file.shareUrl)).toBe(true);
		expect(listArtifactShares).not.toHaveBeenCalled();
	});

	it("still lists the files when the links can't be read (a control plane without share links answers 404)", async () => {
		const files = backend(client({
			listSessionArtifacts: vi.fn(async () => ({ artifacts: [{ path: "a.md", name: "a.md", kind: "markdown", size: 3, sha256: "a".repeat(64), updatedAt: "2026-11-08T09:00:00Z" }] })),
			listArtifactShares: vi.fn(async () => { throw new CloudError("Not found.", { status: 404, code: "not_found" }); }),
		}));
		expect(await files.list("s1")).toMatchObject([{ path: "a.md" }]);
		expect((await files.list("s1"))[0].shareUrl).toBeUndefined();
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
