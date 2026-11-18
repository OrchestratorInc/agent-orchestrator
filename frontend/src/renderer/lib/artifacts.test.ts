import { describe, expect, it } from "vitest";
import {
	CLOUD_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type SessionArtifact,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { artifactChangeError, artifactContext, artifactCount, artifactKey, artifactLocationCounts, artifactOwner, NO_PROJECT_GROUP_ID, withKeptArtifacts, artifactLocation, artifactSharing, artifactType, canChangeSessionArtifacts, filterArtifactGroups, findHostShareLink, groupArtifactsByProject, hostArtifactLocation, withHostShareLinks, withShareAccess } from "./artifacts";
import { CloudCpError } from "./cloud-cp/errors";

function artifact(name: string, updatedAt: string, kind: SessionArtifact["kind"] = "markdown"): SessionArtifact {
	return { kind, name, path: name, size: 10, updatedAt, ...(kind === "html" ? { previewUrl: `http://preview/${name}` } : {}) };
}

function session(id: string, artifacts: SessionArtifact[], overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id,
		title: `Session ${id}`,
		workspaceId: "ws",
		workspaceName: "ws",
		provider: "claude-code",
		kind: "worker",
		status: "idle",
		updatedAt: "2026-10-01T00:00:00Z",
		prs: [],
		outputType: artifacts.length > 0 ? "artifact" : "none",
		artifactFiles: artifacts,
		...overrides,
	} as WorkspaceSession;
}

function workspace(id: string, name: string, sessions: WorkspaceSession[], overrides: Partial<WorkspaceSummary> = {}): WorkspaceSummary {
	return { id, name, path: `/code/${id}`, sessions, ...overrides };
}

describe("groupArtifactsByProject", () => {
	it("groups by project, newest row first and groups by their newest row", () => {
		const groups = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [
				session("ao-1", [artifact("plan.md", "2026-10-09T10:00:00Z"), artifact("flow.html", "2026-10-11T10:00:00Z", "html")]),
				session("ao-2", [artifact("notes.md", "2026-10-10T10:00:00Z")]),
			]),
			workspace("px0", "px0", [session("px0-1", [artifact("metrics.html", "2026-10-11T12:00:00Z", "html")])]),
		]);

		expect(groups.map((group) => group.id)).toEqual(["px0", "ao"]);
		expect(groups[1].rows.map((row) => row.name)).toEqual(["flow.html", "notes.md", "plan.md"]);
		expect(groups[1].rows[0]).toMatchObject({
			sessionId: "ao-1",
			sessionTitle: "Session ao-1",
			projectId: "ao",
			kind: "html",
			previewUrl: "http://preview/flow.html",
		});
		expect(artifactCount(groups)).toBe(4);
	});

	it("leaves out files the session did not register", () => {
		const registered = { ...artifact("brand.html", "2026-10-09T10:00:00Z", "html"), registered: true };
		const groups = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [session("ao-1", [registered, artifact("draft.md", "2026-10-11T10:00:00Z")])]),
		]);

		expect(groups[0].rows.map((row) => row.name)).toEqual(["brand.html"]);
		expect(artifactCount(groups)).toBe(1);
	});

	it("keeps archived and terminated sessions' artifacts, marked archived, and drops projects left empty", () => {
		const groups = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [
				session("ao-1", [artifact("kept.md", "2026-10-09T10:00:00Z")]),
				session("ao-2", [artifact("archived.md", "2026-10-11T10:00:00Z")], { kanbanColumn: "archive" }),
				session("ao-3", [artifact("killed.md", "2026-10-10T10:00:00Z")], { isTerminated: true }),
			]),
			workspace("old", "old-project", [session("old-1", [artifact("ended.md", "2026-10-08T10:00:00Z")], { status: "terminated" })]),
			workspace("empty", "empty-project", [session("empty-1", [])]),
		]);

		expect(groups.map((group) => group.id)).toEqual(["ao", "old"]);
		expect(groups[0].rows.map((row) => [row.name, row.archived ?? false])).toEqual([
			["archived.md", true],
			["killed.md", true],
			["kept.md", false],
		]);
		expect(groups[1].rows.map((row) => [row.name, row.archived])).toEqual([["ended.md", true]]);
	});

	it("labels the standalone workspace as the Scratchpad without a project id", () => {
		const [group] = groupArtifactsByProject([
			workspace(STANDALONE_WORKSPACE_ID, "Standalone", [session("standalone-1", [artifact("caching.md", "2026-10-08T10:00:00Z")])]),
		]);

		expect(group.isScratchpad).toBe(true);
		expect(group.rows[0].projectId).toBeUndefined();
	});

	it("skips PR-only output", () => {
		const groups = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [
				session("ao-1", [artifact("pr-only.md", "2026-10-09T10:00:00Z")], { outputType: "pr" }),
			]),
		]);

		expect(groups).toEqual([]);
	});

	it("lists a remote host's projects apart from this computer's project with the same ID", () => {
		const groups = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [session("ao-1", [artifact("local.md", "2026-10-09T10:00:00Z")])]),
			workspace("ao", "agent-orchestrator", [session("ao-1", [artifact("remote.md", "2026-10-09T11:00:00Z")])], { hostId: "host-2" }),
		]);

		expect(groups.map((group) => [group.id, group.hostId, group.rows.map((row) => [row.name, row.hostId, row.projectId])])).toEqual([
			["remote:host-2:ao", "host-2", [["remote.md", "host-2", "ao"]]],
			["ao", undefined, [["local.md", undefined, "ao"]]],
		]);
	});

	it("lists an orchestrator's artifacts beside its workers', titled as the orchestrator", () => {
		const groups = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [
				session("ao-48", [artifact("status-report.md", "2026-10-11T10:00:00Z")], { kind: "orchestrator", title: "ao-48" }),
				session("ao-1", [artifact("plan.md", "2026-10-10T10:00:00Z")]),
			]),
		], "Orchestrator");

		expect(groups[0].rows.map((row) => [row.name, row.sessionId, row.sessionTitle])).toEqual([
			["status-report.md", "ao-48", "Orchestrator"],
			["plan.md", "ao-1", "Session ao-1"],
		]);
		expect(artifactCount(groups)).toBe(2);
	});

	it("keeps an orchestrator to its registered deliverables, with bundles as one entry", () => {
		const page = { ...artifact("brief.html", "2026-10-11T10:00:00Z", "html"), registered: true };
		const asset = artifact("assets/chart.png", "2026-10-11T10:00:00Z", "file");
		const draft = artifact("draft.md", "2026-10-11T09:00:00Z");
		const icon = [
			{ ...artifact("brand.icon/icon.json", "2026-10-11T08:00:00Z", "file"), registered: true },
			{ ...artifact("brand.icon/Assets/glyph.svg", "2026-10-11T08:00:00Z", "file"), registered: true },
		];
		const [group] = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [
				session("ao-48", [page, asset, draft, ...icon], { kind: "orchestrator" }),
			]),
		], "Orchestrator");

		expect(group.rows.map((row) => row.name)).toEqual(["brief.html", "brand.icon"]);
		expect(group.rows[1].bundle).toEqual({ path: "brand.icon", fileCount: 2 });
	});

	it("keeps a replaced orchestrator's artifacts, marked archived, as it does a killed worker's", () => {
		const groups = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [
				session("ao-40", [artifact("old-report.md", "2026-10-09T10:00:00Z")], { kind: "orchestrator", isTerminated: true }),
			]),
		], "Orchestrator");

		expect(groups[0].rows).toEqual([expect.objectContaining({ name: "old-report.md", sessionTitle: "Orchestrator", archived: true })]);
	});

	it("includes cloud projects' artifacts", () => {
		const [group] = groupArtifactsByProject([
			workspace("cloud", "cloud-project", [
				session("c-1", [artifact("cloud.md", "2026-10-09T10:00:00Z")], { cloud: { orgId: "org-1" } }),
			], { kind: CLOUD_PROJECT_KIND, cloudOrgId: "org-1" }),
		]);

		expect(group).toMatchObject({ id: "cloud", isScratchpad: false });
		expect(group.rows[0]).toMatchObject({ name: "cloud.md", sessionId: "c-1", projectId: "cloud", cloud: { orgId: "org-1" } });
	});

});

describe("artifactOwner", () => {
	it("counts a cloud session started by someone else as shared, everything else as mine", () => {
		expect(artifactOwner({}, "me")).toBe("mine");
		expect(artifactOwner({ cloud: { orgId: "o", createdByUserId: "me" } }, "me")).toBe("mine");
		expect(artifactOwner({ cloud: { orgId: "o", createdByUserId: "maya" } }, "me")).toBe("shared");
		// Unknown until this person's ID is read, or with no creator on record.
		expect(artifactOwner({ cloud: { orgId: "o", createdByUserId: "maya" } }, undefined)).toBe("mine");
		expect(artifactOwner({ cloud: { orgId: "o" } }, "me")).toBe("mine");
	});
});

describe("filterArtifactGroups", () => {
	const groups = groupArtifactsByProject([
		workspace("ao", "agent-orchestrator", [
			session("ao-1", [artifact("artifacts-user-flow.html", "2026-10-11T10:00:00Z", "html")], { title: "Design the Artifacts entry point" }),
			session("ao-2", [artifact("notes.md", "2026-10-10T10:00:00Z")], { title: "Review pricing" }),
		]),
		workspace("px0", "px0", [session("px0-1", [artifact("metrics.html", "2026-10-11T12:00:00Z", "html")])]),
		workspace("cloud", "cloud-project", [
			session("c-1", [artifact("report.pdf", "2026-10-09T10:00:00Z", "file"), artifact("chart.png", "2026-10-09T09:00:00Z", "file"), artifact("demo.mp4", "2026-10-09T08:00:00Z", "file")], { cloud: { orgId: "org-1" } }),
		], { kind: CLOUD_PROJECT_KIND, cloudOrgId: "org-1" }),
	]);
	const names = (filtered: ReturnType<typeof filterArtifactGroups>) => filtered.flatMap((group) => group.rows.map((row) => row.name));

	it("matches the file name or the session title, case-insensitively", () => {
		expect(names(filterArtifactGroups(groups, { query: "USER-FLOW" }))).toEqual(["artifacts-user-flow.html"]);
		expect(names(filterArtifactGroups(groups, { query: "pricing" }))).toEqual(["notes.md"]);
	});

	it("keeps only the chosen project's group", () => {
		expect(filterArtifactGroups(groups, { query: "", projectIds: ["px0"] }).map((group) => group.id)).toEqual(["px0"]);
	});

	it("keeps only artifacts on this machine or only cloud ones", () => {
		expect(filterArtifactGroups(groups, { query: "", location: "local" }).map((group) => group.id)).toEqual(["px0", "ao"]);
		expect(names(filterArtifactGroups(groups, { query: "", location: "cloud" }))).toEqual(["report.pdf", "chart.png", "demo.mp4"]);
	});

	it("keeps only one remote host's artifacts", () => {
		const withHost = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [session("ao-1", [artifact("local.md", "2026-10-09T10:00:00Z")])]),
			workspace("ao", "agent-orchestrator", [session("ao-1", [artifact("vm.md", "2026-10-09T11:00:00Z")])], { hostId: "host-2" }),
			workspace("lab", "lab", [session("lab-1", [artifact("other-vm.md", "2026-10-09T12:00:00Z")])], { hostId: "host-3" }),
		]);
		expect(names(filterArtifactGroups(withHost, { query: "", location: hostArtifactLocation("host-2") }))).toEqual(["vm.md"]);
		expect(names(filterArtifactGroups(withHost, { query: "", location: "local" }))).toEqual(["local.md"]);
		expect(names(filterArtifactGroups(withHost, { query: "", projectIds: ["remote:host-2:ao"] }))).toEqual(["vm.md"]);
	});

	it("keeps only my artifacts or only ones shared with me, and only pinned ones", () => {
		const shared = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [session("ao-1", [artifact("local.md", "2026-10-09T10:00:00Z")])]),
			workspace("cloud", "cloud-project", [
				session("c-1", [artifact("mine.md", "2026-10-09T11:00:00Z")], { cloud: { orgId: "org-1", createdByUserId: "me" } }),
				session("c-2", [artifact("theirs.md", "2026-10-09T12:00:00Z")], { cloud: { orgId: "org-1", createdByUserId: "maya" } }),
			], { kind: CLOUD_PROJECT_KIND, cloudOrgId: "org-1" }),
		]);
		expect(names(filterArtifactGroups(shared, { query: "", owner: "shared", cloudUserId: "me" }))).toEqual(["theirs.md"]);
		expect(names(filterArtifactGroups(shared, { query: "", owner: "mine", cloudUserId: "me" })).sort()).toEqual(["local.md", "mine.md"]);
		const pinned = new Set([artifactKey({ sessionId: "ao-1", path: "local.md" })]);
		expect(names(filterArtifactGroups(shared, { query: "", onlyKeys: pinned }))).toEqual(["local.md"]);
	});

	it("counts each location under the other filters, ignoring the chosen location", () => {
		const counts = artifactLocationCounts(groups, { query: "", location: "local", types: ["document"] });
		expect(Object.fromEntries(counts)).toEqual({ all: 4, local: 3, cloud: 1 });
	});

	it("keeps only the chosen file type, together with the other filters", () => {
		expect(names(filterArtifactGroups(groups, { query: "", types: ["document"] }))).toEqual(["metrics.html", "artifacts-user-flow.html", "notes.md", "report.pdf"]);
		expect(names(filterArtifactGroups(groups, { query: "", projectIds: ["ao"], types: ["document"] }))).toEqual(["artifacts-user-flow.html", "notes.md"]);
		expect(names(filterArtifactGroups(groups, { query: "", types: ["image"] }))).toEqual(["chart.png"]);
		expect(names(filterArtifactGroups(groups, { query: "", location: "cloud", types: ["video"] }))).toEqual(["demo.mp4"]);
		expect(filterArtifactGroups(groups, { query: "", location: "local", types: ["video"] })).toEqual([]);
	});

	it("keeps only public artifacts, or only ones not shared", () => {
		const shared = groups.map((group) => ({ ...group, rows: group.rows.map((row) => row.name === "notes.md" || row.name === "chart.png" ? { ...row, shareUrl: `https://share.test/${row.name}`, shareAccess: "anyone" as const } : row) }));
		expect(names(filterArtifactGroups(shared, { query: "", sharings: ["public"] }))).toEqual(["notes.md", "chart.png"]);
		expect(names(filterArtifactGroups(shared, { query: "", sharings: ["private"] }))).toEqual(["metrics.html", "artifacts-user-flow.html"]);
		expect(names(filterArtifactGroups(shared, { query: "", projectIds: ["ao"], sharings: ["public"] }))).toEqual(["notes.md"]);
	});

	it("tells a private link apart from a public one, once the link list says who it opens for", () => {
		const shared = groups.map((group) => ({ ...group, rows: group.rows.map((row) => row.name === "notes.md" || row.name === "chart.png" ? { ...row, shareUrl: `https://share.test/${row.name}` } : row) }));
		const withAccess = withShareAccess(shared, new Map([["https://share.test/notes.md", "restricted" as const], ["https://share.test/chart.png", "anyone" as const]]));
		expect(names(filterArtifactGroups(withAccess, { query: "", sharings: ["shared"] }))).toEqual(["notes.md"]);
		expect(names(filterArtifactGroups(withAccess, { query: "", sharings: ["public"] }))).toEqual(["chart.png"]);
		expect(names(filterArtifactGroups(withAccess, { query: "", sharings: ["public", "shared"] }))).toEqual(["notes.md", "chart.png"]);
		// Before the list is read, who a published file's link opens for is not
		// known: it counts under neither Public nor Shared privately.
		expect(withShareAccess(shared, new Map())).toEqual(shared);
		expect(filterArtifactGroups(shared, { query: "", sharings: ["public", "shared"] })).toEqual([]);
		expect(artifactSharing({ shareUrl: "https://share.test/x" })).toBeUndefined();
		expect(artifactSharing({ shareUrl: "https://share.test/x", shareAccess: "anyone" })).toBe("public");
		expect(artifactSharing({ shareUrl: "https://share.test/x", shareAccess: "restricted" })).toBe("shared");
	});

	it("keeps a project's rows of the chosen type when the query names the project", () => {
		expect(names(filterArtifactGroups(groups, { query: "agent-orch", types: ["document"] }))).toEqual(["artifacts-user-flow.html", "notes.md"]);
	});

	it("keeps cloud artifacts, whose publishing is unknown, out of both Public and Not shared", () => {
		// The cloud file list carries no public link, so chart.png may well be published.
		expect(names(filterArtifactGroups(groups, { query: "", sharings: ["private"] }))).toEqual(["metrics.html", "artifacts-user-flow.html", "notes.md"]);
		expect(names(filterArtifactGroups(groups, { query: "", sharings: ["public", "private"] }))).toEqual(["metrics.html", "artifacts-user-flow.html", "notes.md"]);
		expect(names(filterArtifactGroups(groups, { query: "", location: "cloud" }))).toEqual(["report.pdf", "chart.png", "demo.mp4"]);
	});

	it("keeps rows matching any value chosen in a filter, and every filter together", () => {
		expect(filterArtifactGroups(groups, { query: "", projectIds: ["px0", "ao"] }).map((group) => group.id)).toEqual(["px0", "ao"]);
		expect(names(filterArtifactGroups(groups, { query: "", types: ["image", "video"] }))).toEqual(["chart.png", "demo.mp4"]);
		expect(names(filterArtifactGroups(groups, { query: "", projectIds: ["cloud", "px0"], types: ["image", "video"] }))).toEqual(["chart.png", "demo.mp4"]);
		expect(names(filterArtifactGroups(groups, { query: "", projectIds: ["px0"], types: ["image"] }))).toEqual([]);
	});

	it("returns nothing when no row matches", () => {
		expect(filterArtifactGroups(groups, { query: "pricing", projectIds: ["px0"] })).toEqual([]);
	});
});

describe("groupArtifactsByProject share access", () => {
	it("keeps the access recorded with a published file's link", () => {
		const [group] = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [session("ao-1", [
				{ ...artifact("a.html", "2026-10-11T10:00:00Z", "html"), shareUrl: "https://viewer.test/artifact/a", shareAccess: "restricted" },
				{ ...artifact("b.html", "2026-10-10T10:00:00Z", "html"), shareAccess: "anyone" },
			])]),
		]);
		expect(group.rows.map((row) => [row.name, row.shareAccess])).toEqual([["a.html", "restricted"], ["b.html", undefined]]);
		expect(artifactSharing(group.rows[0])).toBe("shared");
	});
});

describe("artifactType", () => {
	it("sorts images and videos by extension, and everything else into documents", () => {
		expect(artifactType({ path: "a.png" })).toBe("image");
		expect(artifactType({ path: "a.svg" })).toBe("image");
		expect(artifactType({ path: "a.MP4" })).toBe("video");
		expect(artifactType({ path: "a.webm" })).toBe("video");
		expect(artifactType({ path: "a.html" })).toBe("document");
		expect(artifactType({ path: "a.md" })).toBe("document");
		expect(artifactType({ path: "a.pdf" })).toBe("document");
		expect(artifactType({ path: "a.csv" })).toBe("document");
	});
});

describe("artifactChangeError", () => {
	it("reads the control plane's refusal as view only", () => {
		const refused = new CloudCpError("Only the person who started this session can change its files.", { status: 403, code: "artifact_not_owner" });
		expect((artifactChangeError(refused, "View only.") as Error).message).toBe("View only.");
	});

	it("leaves every other failure as it is", () => {
		const changed = new CloudCpError("The file changed since it was opened.", { status: 409, code: "artifact_changed" });
		const offline = new Error("network down");
		expect(artifactChangeError(changed, "View only.")).toBe(changed);
		expect(artifactChangeError(offline, "View only.")).toBe(offline);
	});
});

describe("canChangeSessionArtifacts", () => {
	it("gives a cloud session's files to the person who started it, and only them", () => {
		expect(canChangeSessionArtifacts({ orgId: "org-1", createdByUserId: "u-1" }, "u-1")).toBe(true);
		expect(canChangeSessionArtifacts({ orgId: "org-1", createdByUserId: "u-1" }, "u-2")).toBe(false);
		expect(canChangeSessionArtifacts({ orgId: "org-1", createdByUserId: "u-1" }, undefined)).toBe(false);
	});

	it("always gives a local session's files to this person", () => {
		expect(canChangeSessionArtifacts(undefined, undefined)).toBe(true);
	});

	it("leaves the decision to a control plane too old to name the creator", () => {
		expect(canChangeSessionArtifacts({ orgId: "org-1" }, "u-2")).toBe(true);
	});
});

describe("artifactKey", () => {
	it("keeps this computer's key as it was, so saved pins still match, and names a remote host's", () => {
		expect(artifactKey({ sessionId: "ao-1", path: "plan.md" })).toBe(JSON.stringify(["ao-1", "plan.md"]));
		expect(artifactKey({ sessionId: "ao-1", path: "plan.md", hostId: "host-2" })).not.toBe(artifactKey({ sessionId: "ao-1", path: "plan.md" }));
		expect(artifactKey({ sessionId: "ao-1", path: "plan.md", hostId: "host-2" })).not.toBe(artifactKey({ sessionId: "ao-1", path: "plan.md", hostId: "host-3" }));
	});
});

describe("artifactLocation", () => {
	it("is the row's remote host, this computer, or the cloud", () => {
		expect(artifactLocation({ hostId: "host-2" }, { cloud: false })).toBe("host:host-2");
		expect(artifactLocation({}, { cloud: false })).toBe("local");
		expect(artifactLocation({ cloud: { orgId: "org-1" } }, { cloud: false })).toBe("cloud");
	});
});

describe("withHostShareLinks", () => {
	const groups = groupArtifactsByProject([
		workspace("ao", "agent-orchestrator", [session("ao-1", [{ ...artifact("deck.html", "2026-10-09T10:00:00Z", "html"), id: "art-7" }, artifact("notes.md", "2026-10-09T09:00:00Z")])]),
		workspace("ao", "agent-orchestrator", [session("ao-1", [{ ...artifact("deck.html", "2026-10-09T10:00:00Z", "html"), id: "art-7" }, artifact("notes.md", "2026-10-09T09:00:00Z")])], { hostId: "host-2" }),
	]);
	const links = [
		{ hostId: "host-2", sessionId: "ao-1", artifactId: "art-7", url: "https://deck.share.test/", access: "restricted" as const },
		{ hostId: "host-2", sessionId: "ao-1", path: "notes.md", url: "https://notes.share.test/" },
		{ hostId: "host-3", sessionId: "ao-1", path: "notes.md", url: "https://elsewhere.share.test/" },
	];

	it("marks a remote host's files by artifact ID, else by path, and leaves this computer's alone", () => {
		const marked = withHostShareLinks(groups, links);
		const rows = (hostId?: string) => marked.find((group) => group.hostId === hostId)?.rows.map((row) => [row.name, row.shareUrl, row.shareAccess]);
		expect(rows("host-2")).toEqual([
			["deck.html", "https://deck.share.test/", "restricted"],
			["notes.md", "https://notes.share.test/", undefined],
		]);
		expect(rows(undefined)).toEqual([["deck.html", undefined, undefined], ["notes.md", undefined, undefined]]);
		expect(artifactSharing(marked.find((group) => group.hostId === "host-2")!.rows[0]!)).toBe("shared");
	});

	it("finds one file's link on its own host only", () => {
		expect(findHostShareLink(links, "host-3", "ao-1", { path: "notes.md" })?.url).toBe("https://elsewhere.share.test/");
		expect(findHostShareLink(links, "host-3", "ao-1", { id: "art-7", path: "deck.html" })).toBeUndefined();
	});
});

describe("withKeptArtifacts", () => {
	const projects = [
		workspace("ao", "agent-orchestrator", [session("ao-1", [artifact("live.md", "2026-10-02T00:00:00Z")])]),
		workspace("docs", "docs", []),
		workspace(STANDALONE_WORKSPACE_ID, "Sessions", []),
	];
	const groups = groupArtifactsByProject(projects);

	it("puts a kept set under its live project, or under one group with no project", () => {
		const out = withKeptArtifacts(groups, [
			{ sessionId: "ao-7", projectId: "ao", title: "Old report", sessionDeleted: true, artifactFiles: [{ ...artifact("report.md", "2026-10-05T00:00:00Z"), registered: true }, artifact("scratch.md", "2026-10-06T00:00:00Z")] },
			{ sessionId: "docs-2", projectId: "docs", title: "Guide", sessionDeleted: true, artifactFiles: [artifact("guide.md", "2026-10-01T00:00:00Z")] },
			{ sessionId: "gone-3", title: "Removed work", sessionDeleted: false, artifactFiles: [artifact("plan.md", "2026-10-04T00:00:00Z")] },
			{ sessionId: "ao-9", projectId: "unknown", title: "Lost", sessionDeleted: true, artifactFiles: [artifact("lost.md", "2026-10-03T00:00:00Z")] },
		], projects, "No project");

		expect(out.map((group) => [group.id, group.name, group.rows.map((row) => row.path)])).toEqual([
			// Registered files only, newest first among the project's rows.
			["ao", "agent-orchestrator", ["report.md", "live.md"]],
			[NO_PROJECT_GROUP_ID, "No project", ["plan.md", "lost.md"]],
			["docs", "docs", ["guide.md"]],
		]);
		const kept = out[0].rows[0];
		expect(kept).toMatchObject({ sessionId: "ao-7", sessionTitle: "Old report", projectId: "ao", archived: true, kept: "session-deleted" });
		expect(out[1].rows[0]).toMatchObject({ kept: "project-removed", archived: true });
		expect(out[1].rows[0].projectId).toBeUndefined();
		// The input groups are not changed.
		expect(groups[0].rows.map((row) => row.path)).toEqual(["live.md"]);
	});

	it("leaves the groups as they are with nothing kept", () => {
		expect(withKeptArtifacts(groups, undefined, projects, "No project")).toEqual(groups);
	});

	it("names the machine, whose it is, and why a row is view only", () => {
		const t = ((key: string) => ({ "artifacts.deletedSession": "Session deleted", "artifacts.removedProject": "Project removed", "artifacts.archivedSession": "Archived", "artifacts.owner.mine": "Yours", "artifacts.owner.shared": "Shared with you" })[key] ?? key) as never;
		expect(artifactContext(t, { archived: true, kept: "session-deleted" }, "On this machine", "mine")).toBe("On this machine · Yours · Session deleted");
		expect(artifactContext(t, { archived: true, kept: "project-removed" }, "On this machine", "mine")).toBe("On this machine · Yours · Project removed");
		expect(artifactContext(t, { archived: true }, "Cloud", "shared")).toBe("Cloud · Shared with you · Archived");
		expect(artifactContext(t, {}, "build-vm", "mine")).toBe("build-vm · Yours");
	});
});
