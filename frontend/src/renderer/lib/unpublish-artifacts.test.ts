import { describe, expect, it, vi } from "vitest";
import type { HostShareLink } from "../../shared/cloud-share";
import { toCloudWorkspaceSession } from "../hooks/useWorkspaceQuery";
import type { CloudCpSession } from "./cloud-cp/types";
import { STANDALONE_WORKSPACE_ID, type SessionArtifact, type WorkspaceSession } from "../types/workspace";
import {
	countPublished,
	deleteTargets,
	onArtifactLinksUnpublished,
	prepareUnpublish,
	publishedCheck,
	sessionDeleteTargets,
	sessionPublishedCheck,
	sessionUnpublishTargets,
	trackPublish,
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
function bridge(links: Record<string, string>, refuse: { inspect?: string[]; unpublish?: string[] } = {}, hostLinks: HostShareLink[] = []) {
	const unpublished: string[] = [];
	const fake: UnpublishBridge = {
		inspect: vi.fn(async (file) => {
			if (refuse.inspect?.includes(file.path)) throw new Error("signed out");
			const shareId = links[file.path];
			return { name: file.path, unsupported: false, fingerprint: "f", ...(shareId ? { shareId } : {}) };
		}),
		hostLinks: vi.fn(async () => hostLinks),
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

	it("names a remote host's files by the host, taking their links from this computer's host links", async () => {
		const remote = session([artifact("a.md", undefined, "id-a"), artifact("b.md"), artifact("c.md")], { hostId: "host-1" });
		const hostLinks: HostShareLink[] = [
			{ hostId: "host-1", sessionId: "ao-1", artifactId: "id-a", url: "https://l/a" },
			{ hostId: "host-1", sessionId: "ao-1", path: "c.md", url: "https://l/c" },
			{ hostId: "host-2", sessionId: "ao-1", path: "b.md", url: "https://l/b" },
		];
		const expected = [
			{ source: "host", hostId: "host-1", sessionId: "ao-1", path: "a.md", projectId: "ao", artifactId: "id-a" },
			{ source: "host", hostId: "host-1", sessionId: "ao-1", path: "c.md", projectId: "ao" },
		];
		expect(sessionUnpublishTargets(remote, hostLinks)).toEqual(expected);
		expect(sessionPublishedCheck(remote, hostLinks)).toEqual({ known: 2, lookup: [] });
		expect(sessionUnpublishTargets(remote)).toEqual([]);

		const { fake } = bridge({}, {}, hostLinks);
		await expect(sessionDeleteTargets(BASE, remote, fake)).resolves.toEqual(expected);
		// Host links that cannot be read leave every file to be looked up.
		fake.hostLinks = vi.fn(async () => { throw new Error("signed out"); });
		await expect(sessionDeleteTargets(BASE, remote, fake)).resolves.toHaveLength(3);
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
		const pending = await prepareUnpublish(BASE, files, { bridge: fake });
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
		const pending = await prepareUnpublish(BASE, files, { bridge: fake });
		await expect(pending.unpublish()).resolves.toBe(2);
		expect(unpublished).toEqual([]);
	});

	it("counts a file with no recorded link as done", async () => {
		const { fake } = bridge({});
		await expect((await prepareUnpublish(BASE, files, { bridge: fake })).unpublish()).resolves.toBe(0);
		expect(fake.unpublish).not.toHaveBeenCalled();
	});

	it("unpublishes a link two files share once", async () => {
		const { fake, unpublished } = bridge({ "a.md": "share-a", "b.md": "share-a" });
		await (await prepareUnpublish(BASE, files, { bridge: fake })).unpublish();
		expect(unpublished).toEqual(["share-a"]);
	});

	it("leaves every link live when no control plane is configured", async () => {
		const { fake } = bridge({ "a.md": "share-a" });
		await expect((await prepareUnpublish("", files, { bridge: fake })).unpublish()).resolves.toBe(2);
		expect(fake.inspect).not.toHaveBeenCalled();
	});

	it("does nothing for no files", async () => {
		const { fake } = bridge({});
		await expect((await prepareUnpublish(BASE, [], { bridge: fake })).unpublish()).resolves.toBe(0);
		expect(fake.inspect).not.toHaveBeenCalled();
	});
});

/** A terminated cloud session as the control plane lists it, through the app's real mapping. */
function mappedCloudSession(): WorkspaceSession {
	const listed: CloudCpSession = {
		id: "s-1", orgId: "org-1", projectId: "p-1", kind: "worker", harness: "claude-code", displayName: "Report",
		branch: "ao/s-1", mode: "trusted", interfaceMode: "tui", deniedCommands: [], activityState: "idle", status: "terminated",
		runtimeConnected: false, isTerminated: true, prs: [], outputType: "artifact", createdAt: "2026-10-16T00:00:00Z", updatedAt: "2026-10-16T00:00:00Z",
		artifactFiles: [
			{ id: "art-1", path: "report.html", name: "report.html", kind: "html", contentType: "text/html", size: 12, sha256: "a".repeat(64), updatedAt: "2026-10-16T01:00:00Z" },
			{ id: "art-2", path: "notes.md", name: "notes.md", kind: "markdown", contentType: "text/markdown", size: 3, sha256: "b".repeat(64), updatedAt: "2026-10-16T01:00:00Z" },
		],
	};
	return toCloudWorkspaceSession(listed, { id: "p-1", displayName: "Project" }, "org-1");
}

describe("a cloud session's links", () => {
	it("are looked up for every file, since its list carries no shareUrl", () => {
		const cloud = mappedCloudSession();
		expect(cloud.artifactFiles?.some((file) => file.shareUrl)).toBe(false);
		const targets = sessionUnpublishTargets(cloud);
		expect(targets).toHaveLength(2);
		expect(targets).toEqual(expect.arrayContaining([
			{ source: "cloud", orgId: "org-1", sessionId: "s-1", path: "notes.md", projectId: "", artifactId: "art-2", fingerprint: "b".repeat(64) },
			{ source: "cloud", orgId: "org-1", sessionId: "s-1", path: "report.html", projectId: "", artifactId: "art-1", fingerprint: "a".repeat(64) },
		]));
		expect(sessionPublishedCheck(cloud)).toEqual({ known: 0, lookup: targets });
	});

	it("are unpublished and counted by what this computer recorded", async () => {
		const cloud = mappedCloudSession();
		const { fake, unpublished } = bridge({ "report.html": "share-r" });
		await expect(countPublished(BASE, sessionPublishedCheck(cloud).lookup, fake)).resolves.toBe(1);
		await expect((await prepareUnpublish(BASE, sessionUnpublishTargets(cloud), { bridge: fake })).unpublish()).resolves.toBe(0);
		expect(unpublished).toEqual(["share-r"]);
	});
});

describe("publishedCheck", () => {
	it("counts a local session's published files without a lookup", () => {
		expect(publishedCheck(session([]), [artifact("a.md", "https://l/a"), artifact("b.md")])).toEqual({ known: 1, lookup: [] });
	});

	it("counts nothing that cannot be looked up", async () => {
		const { fake } = bridge({ "a.md": "share-a" }, { inspect: ["a.md"] });
		await expect(countPublished(BASE, [{ sessionId: "ao-1", path: "a.md", projectId: "ao" }], fake)).resolves.toBe(0);
		await expect(countPublished("", [{ sessionId: "ao-1", path: "a.md", projectId: "ao" }], fake)).resolves.toBe(0);
	});
});

describe("a publish still running when its file is deleted", () => {
	const file = { sessionId: "ao-1", path: "new.md", projectId: "ao" };

	it("is waited for, then its new link is unpublished", async () => {
		const links: Record<string, string> = {};
		const { fake, unpublished } = bridge(links);
		let finish!: () => void;
		trackPublish(BASE, file, new Promise<void>((resolve) => { finish = resolve; }));

		// Not marked published yet: the delete knows of no link.
		const preparing = prepareUnpublish(BASE, [], { deleting: [{ sessionId: "ao-1", path: "new.md" }], bridge: fake });
		await Promise.resolve();
		expect(fake.inspect).not.toHaveBeenCalled();
		links["new.md"] = "share-new";
		finish();

		await expect((await preparing).unpublish()).resolves.toBe(0);
		expect(unpublished).toEqual(["share-new"]);
	});

	it("is waited for by a whole session's delete, and not by another file's", async () => {
		const { fake, unpublished } = bridge({ "new.md": "share-new" });
		let finish!: () => void;
		const done = new Promise<void>((resolve) => { finish = resolve; });
		trackPublish(BASE, file, done);

		await expect((await prepareUnpublish(BASE, [], { deleting: [{ sessionId: "ao-1", path: "other.md" }], bridge: fake })).unpublish()).resolves.toBe(0);
		expect(fake.inspect).not.toHaveBeenCalled();

		const preparing = prepareUnpublish(BASE, [], { deleting: [{ sessionId: "ao-1" }], bridge: fake });
		finish();
		await (await preparing).unpublish();
		expect(unpublished).toEqual(["share-new"]);
		await done;
	});
});

describe("deleteTargets", () => {
	const remote = { id: "ao-1", hostId: "host-1", workspaceId: "ao" };
	const target = { source: "host", hostId: "host-1", sessionId: "ao-1", path: "a.md", projectId: "ao" };

	it("reads this computer's host links at the delete, not a list that may be empty", async () => {
		const { fake } = bridge({}, {}, [{ hostId: "host-1", sessionId: "ao-1", path: "a.md", url: "https://l/a" }]);
		await expect(deleteTargets(BASE, remote, [{ path: "a.md" }], fake)).resolves.toEqual([target]);
		await expect(deleteTargets(BASE, remote, [{ path: "b.md" }], fake)).resolves.toEqual([]);
	});

	it("looks a remote file up when the host links cannot be read", async () => {
		const { fake } = bridge({});
		fake.hostLinks = vi.fn(async () => { throw new Error("unavailable"); });
		await expect(deleteTargets(BASE, remote, [{ path: "a.md" }], fake)).resolves.toEqual([target]);
	});
});

describe("a running publish and a delete on another host", () => {
	it("are kept apart, though their session and path match", async () => {
		const { fake } = bridge({ "same.md": "share-host" });
		let finish!: () => void;
		const done = new Promise<void>((resolve) => { finish = resolve; });
		trackPublish(BASE, { source: "host", hostId: "host-1", sessionId: "ao-1", path: "same.md", projectId: "ao" }, done);

		// A delete of this computer's ao-1/same.md neither waits for nor unpublishes it.
		await expect((await prepareUnpublish(BASE, [], { deleting: [{ sessionId: "ao-1", path: "same.md" }], bridge: fake })).unpublish()).resolves.toBe(0);
		expect(fake.inspect).not.toHaveBeenCalled();

		const preparing = prepareUnpublish(BASE, [], { deleting: [{ hostId: "host-1", sessionId: "ao-1", path: "same.md" }], bridge: fake });
		finish();
		await (await preparing).unpublish();
		expect(fake.unpublish).toHaveBeenCalledWith(BASE, "share-host");
		await done;
	});
});

describe("a publish that finished before its file's list shows the link", () => {
	it("is still found by a delete of the file, and forgotten once it is deleted", async () => {
		const file = { sessionId: "ao-1", path: "fresh.md", projectId: "ao" };
		const { fake, unpublished } = bridge({ "fresh.md": "share-fresh" });
		trackPublish(BASE, file, Promise.resolve());
		await Promise.resolve();

		// The row still lacks shareUrl, so the delete names no link of its own.
		const pending = await prepareUnpublish(BASE, [], { deleting: [{ sessionId: "ao-1", path: "fresh.md" }], bridge: fake });
		await expect(pending.unpublish()).resolves.toBe(0);
		expect(unpublished).toEqual(["share-fresh"]);

		await (await prepareUnpublish(BASE, [], { deleting: [{ sessionId: "ao-1", path: "fresh.md" }], bridge: fake })).unpublish();
		expect(fake.inspect).toHaveBeenCalledTimes(1);
	});
});
