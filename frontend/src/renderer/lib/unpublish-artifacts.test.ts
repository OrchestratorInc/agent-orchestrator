import { describe, expect, it, vi } from "vitest";
import { STANDALONE_WORKSPACE_ID, type SessionArtifact, type WorkspaceSession } from "../types/workspace";
import {
	onArtifactLinksUnpublished,
	prepareUnpublish,
	sessionPublishedCount,
	sessionUnpublishTargets,
	type UnpublishBridge,
	unpublishTarget,
} from "./unpublish-artifacts";

const BASE = "https://cp.example";

function artifact(path: string, shareUrl?: string, id?: string): SessionArtifact {
	return { kind: "markdown", name: path, path, size: 1, updatedAt: "2026-11-01T00:00:00Z", ...(shareUrl ? { shareUrl } : {}), ...(id ? { id } : {}) };
}

function session(artifacts: SessionArtifact[], overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: "ao-1",
		title: "Session",
		workspaceId: "ao",
		workspaceName: "ao",
		provider: "claude-code",
		kind: "worker",
		status: "idle",
		updatedAt: "2026-11-01T00:00:00Z",
		prs: [],
		outputType: "artifact",
		artifactFiles: artifacts,
		...overrides,
	} as WorkspaceSession;
}

/** A bridge whose links are by path; `refuse` makes a lookup or unpublish fail. */
function bridge(links: Record<string, string>, refuse: { inspect?: string[]; unpublish?: string[] } = {}) {
	const unpublished: string[] = [];
	const fake: UnpublishBridge = {
		inspect: vi.fn(async (file) => {
			if (refuse.inspect?.includes(file.path)) throw new Error("signed out");
			const shareId = links[file.path];
			return { name: file.path, unsupported: false, fingerprint: "f", ...(shareId ? { shareId } : {}) };
		}),
		unpublish: vi.fn(async (_baseUrl, shareId) => {
			if (refuse.unpublish?.includes(shareId)) throw new Error("offline");
			unpublished.push(shareId);
		}),
	};
	return { fake, unpublished };
}

describe("sessionUnpublishTargets", () => {
	it("names only published files, by artifact ID where there is one", () => {
		const targets = sessionUnpublishTargets(session([artifact("a.md", "https://l/a", "id-a"), artifact("b.md"), artifact("c.md", "https://l/c")]));
		expect(targets).toEqual([
			{ sessionId: "ao-1", path: "a.md", projectId: "ao", artifactId: "id-a" },
			{ sessionId: "ao-1", path: "c.md", projectId: "ao" },
		]);
	});

	it("has none for a remote host's session, whose links are not recorded here", () => {
		const remote = session([artifact("a.md", "https://l/a")], { hostId: "host-1" });
		expect(sessionUnpublishTargets(remote)).toEqual([]);
		expect(sessionPublishedCount(remote)).toBe(0);
	});

	it("names a cloud session's file by its org, and a projectless one by the Scratchpad", () => {
		const cloud = session([], { cloud: { orgId: "org-1" } } as Partial<WorkspaceSession>);
		expect(unpublishTarget(cloud, { path: "x.md", id: "id-x" })).toEqual({ source: "cloud", orgId: "org-1", sessionId: "ao-1", path: "x.md", projectId: "", artifactId: "id-x" });
		expect(unpublishTarget({ id: "s-1", workspaceId: "" }, { path: "y.md" }).projectId).toBe(STANDALONE_WORKSPACE_ID);
	});
});

describe("prepareUnpublish", () => {
	const files = [
		{ sessionId: "ao-1", path: "a.md", projectId: "ao" },
		{ sessionId: "ao-1", path: "b.md", projectId: "ao" },
	];

	it("looks links up first and unpublishes them only when asked, after the delete", async () => {
		const { fake, unpublished } = bridge({ "a.md": "share-a", "b.md": "share-b" });
		const pending = await prepareUnpublish(BASE, files, fake);
		expect(fake.inspect).toHaveBeenCalledTimes(2);
		expect(unpublished).toEqual([]);
		const changed = vi.fn();
		const stop = onArtifactLinksUnpublished(changed);
		await expect(pending.unpublish()).resolves.toBe(0);
		stop();
		expect(unpublished.sort()).toEqual(["share-a", "share-b"]);
		expect(changed).toHaveBeenCalledTimes(1);
	});

	it("counts links it could not look up or unpublish as still live, without throwing", async () => {
		const { fake, unpublished } = bridge({ "a.md": "share-a", "b.md": "share-b" }, { inspect: ["a.md"], unpublish: ["share-b"] });
		const pending = await prepareUnpublish(BASE, files, fake);
		await expect(pending.unpublish()).resolves.toBe(2);
		expect(unpublished).toEqual([]);
	});

	it("counts a file with no recorded link as done", async () => {
		const { fake } = bridge({});
		await expect((await prepareUnpublish(BASE, files, fake)).unpublish()).resolves.toBe(0);
		expect(fake.unpublish).not.toHaveBeenCalled();
	});

	it("unpublishes a link two files share once", async () => {
		const { fake, unpublished } = bridge({ "a.md": "share-a", "b.md": "share-a" });
		await (await prepareUnpublish(BASE, files, fake)).unpublish();
		expect(unpublished).toEqual(["share-a"]);
	});

	it("leaves every link live when no control plane is configured", async () => {
		const { fake } = bridge({ "a.md": "share-a" });
		await expect((await prepareUnpublish("", files, fake)).unpublish()).resolves.toBe(2);
		expect(fake.inspect).not.toHaveBeenCalled();
	});

	it("does nothing for no files", async () => {
		const { fake } = bridge({});
		await expect((await prepareUnpublish(BASE, [], fake)).unpublish()).resolves.toBe(0);
		expect(fake.inspect).not.toHaveBeenCalled();
	});
});
