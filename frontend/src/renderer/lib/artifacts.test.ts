import { describe, expect, it } from "vitest";
import {
	CLOUD_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type SessionArtifact,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { artifactChangeError, artifactCount, artifactSharing, artifactType, canChangeSessionArtifacts, filterArtifactGroups, groupArtifactsByProject, withShareAccess } from "./artifacts";
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

	it("hides archived and terminated sessions and drops projects left empty", () => {
		const groups = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [
				session("ao-1", [artifact("kept.md", "2026-10-09T10:00:00Z")]),
				session("ao-2", [artifact("archived.md", "2026-10-10T10:00:00Z")], { kanbanColumn: "archive" }),
				session("ao-3", [artifact("killed.md", "2026-10-10T10:00:00Z")], { isTerminated: true }),
			]),
			workspace("old", "old-project", [session("old-1", [artifact("gone.md", "2026-10-10T10:00:00Z")], { status: "terminated" })]),
			workspace("empty", "empty-project", [session("empty-1", [])]),
		]);

		expect(groups.map((group) => group.id)).toEqual(["ao"]);
		expect(groups[0].rows.map((row) => row.name)).toEqual(["kept.md"]);
	});

	it("labels the standalone workspace as the Scratchpad without a project id", () => {
		const [group] = groupArtifactsByProject([
			workspace(STANDALONE_WORKSPACE_ID, "Standalone", [session("standalone-1", [artifact("caching.md", "2026-10-08T10:00:00Z")])]),
		]);

		expect(group.isScratchpad).toBe(true);
		expect(group.rows[0].projectId).toBeUndefined();
	});

	it("skips remote hosts and PR-only output", () => {
		const groups = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [
				session("ao-1", [artifact("pr-only.md", "2026-10-09T10:00:00Z")], { outputType: "pr" }),
			]),
			workspace("remote", "remote-project", [session("r-1", [artifact("remote.md", "2026-10-09T10:00:00Z")])], { hostId: "host-2" }),
		]);

		expect(groups).toEqual([]);
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

	it("hides a replaced orchestrator's artifacts as it does a killed worker's", () => {
		const groups = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [
				session("ao-40", [artifact("old-report.md", "2026-10-09T10:00:00Z")], { kind: "orchestrator", isTerminated: true }),
			]),
		], "Orchestrator");

		expect(groups).toEqual([]);
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
