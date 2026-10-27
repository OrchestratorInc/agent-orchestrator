import { describe, expect, it } from "vitest";
import {
	CLOUD_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type SessionArtifact,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { artifactCount, filterArtifactGroups, groupArtifactsByProject } from "./artifacts";

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
			generating: false,
		});
		expect(artifactCount(groups)).toBe(4);
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

	it("skips orchestrators, remote hosts, and PR-only output", () => {
		const groups = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [
				session("ao-orchestrator", [artifact("orch.md", "2026-10-09T10:00:00Z")], { kind: "orchestrator" }),
				session("ao-1", [artifact("pr-only.md", "2026-10-09T10:00:00Z")], { outputType: "pr" }),
			]),
			workspace("remote", "remote-project", [session("r-1", [artifact("remote.md", "2026-10-09T10:00:00Z")])], { hostId: "host-2" }),
		]);

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

	it("marks rows from a working session as generating", () => {
		const [group] = groupArtifactsByProject([
			workspace("ao", "agent-orchestrator", [
				session("ao-1", [artifact("draft.md", "2026-10-09T10:00:00Z")], { activity: { state: "active", lastActivityAt: "2026-10-09T10:00:00Z" } }),
			]),
		]);

		expect(group.rows[0].generating).toBe(true);
	});
});

describe("filterArtifactGroups", () => {
	const groups = groupArtifactsByProject([
		workspace("ao", "agent-orchestrator", [
			session("ao-1", [artifact("artifacts-user-flow.html", "2026-10-11T10:00:00Z", "html")], { title: "Design the Artifacts entry point" }),
			session("ao-2", [artifact("notes.md", "2026-10-10T10:00:00Z")], { title: "Review pricing" }),
		]),
		workspace("px0", "px0", [session("px0-1", [artifact("metrics.html", "2026-10-11T12:00:00Z", "html")])]),
	]);

	it("matches the file name or the session title, case-insensitively", () => {
		expect(filterArtifactGroups(groups, "USER-FLOW", "").flatMap((group) => group.rows.map((row) => row.name))).toEqual(["artifacts-user-flow.html"]);
		expect(filterArtifactGroups(groups, "pricing", "").flatMap((group) => group.rows.map((row) => row.name))).toEqual(["notes.md"]);
	});

	it("keeps only the chosen project's group", () => {
		expect(filterArtifactGroups(groups, "", "px0").map((group) => group.id)).toEqual(["px0"]);
	});

	it("returns nothing when no row matches", () => {
		expect(filterArtifactGroups(groups, "pricing", "px0")).toEqual([]);
	});
});
