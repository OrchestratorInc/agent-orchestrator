import { describe, expect, it } from "vitest";
import { buildCommands } from "./command-palette";
import { buildSidebarSessionOrder } from "./sidebar-order";
import { archivedStandaloneSessions } from "./standalone-archive";
import { rememberStandaloneAgent, standaloneAgentProjectId } from "./cloud-standalone-agents";
import {
	isScratchpadWorkspace,
	ownScratchpadWorkspaces,
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";

function session(id: string, workspaceId: string, overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id,
		workspaceId,
		workspaceName: workspaceId,
		title: id,
		provider: "claude-code",
		kind: "worker",
		status: "working",
		updatedAt: "2026-11-10T00:00:00Z",
		prs: [],
		...overrides,
	};
}

const cloud = { orgId: "org-1" };

// A local project, the local scratchpad, the person's own Cloud standalone
// agent, and one someone shared with them.
function workspaces(): WorkspaceSummary[] {
	return [
		{ id: "proj-1", name: "app", path: "/repos/app", sessions: [session("w-1", "proj-1")] },
		{ id: STANDALONE_WORKSPACE_ID, name: "Scratchpad", kind: STANDALONE_PROJECT_KIND, path: "", sessions: [session("local-1", STANDALONE_WORKSPACE_ID)] },
		{
			id: "agent-1", name: "Fix the flaky test", kind: "cloud", cloudOrgId: "org-1", standaloneAgent: true, path: "",
			sessions: [session("cloud-1", "agent-1", { cloud })],
		},
		{
			id: "agent-shared", name: "Someone's agent", kind: "cloud", cloudOrgId: "org-1", standaloneAgent: true, path: "",
			shared: { source: "link" },
			sessions: [session("cloud-shared", "agent-shared", { cloud: { ...cloud, shared: true } })],
		},
	];
}

describe("Cloud standalone agents in the Scratchpad", () => {
	it("are the Scratchpad's, not projects, and only the person's own", () => {
		const all = workspaces();
		expect(all.map(isScratchpadWorkspace)).toEqual([false, true, true, true]);
		expect(ownScratchpadWorkspaces(all).map((workspace) => workspace.id)).toEqual([STANDALONE_WORKSPACE_ID, "agent-1"]);
	});

	it("follow the Scratchpad's order in keyboard navigation and route by their own project", () => {
		const order = buildSidebarSessionOrder({
			workspaces: workspaces(),
			sessionOrders: { [STANDALONE_WORKSPACE_ID]: ["cloud-1", "local-1"] },
		});
		// Projects first (a shared agent stays among them), then the Scratchpad in its own order.
		expect(order.map((entry) => [entry.sessionId, entry.projectId])).toEqual([
			["w-1", "proj-1"],
			["cloud-shared", "agent-shared"],
			["cloud-1", "agent-1"],
			["local-1", STANDALONE_WORKSPACE_ID],
		]);
	});

	it("are archived with the Scratchpad's sessions", () => {
		const all = workspaces().map((workspace) => ({
			...workspace,
			sessions: workspace.sessions.map((candidate) => ({ ...candidate, isTerminated: true })),
		}));
		expect(archivedStandaloneSessions(all).map((candidate) => candidate.id).sort()).toEqual(["cloud-1", "local-1"]);
	});

	it("are not listed as projects in the command palette and offer no orchestrator", () => {
		const items = buildCommands({ workspaces: workspaces(), currentProjectId: "agent-1", currentSessionId: "cloud-1" });
		const projects = items.filter((item) => item.group === "projects").map((item) => item.id);
		expect(projects).toEqual(["project:proj-1"]);
		expect(items.some((item) => item.id === "current-open-orchestrator")).toBe(false);
	});

	it("remember the project a new agent's session opens in", () => {
		expect(standaloneAgentProjectId("s-new")).toBeUndefined();
		rememberStandaloneAgent("s-new", "agent-new");
		expect(standaloneAgentProjectId("s-new")).toBe("agent-new");
	});
});
