import { expect, it } from "vitest";
import { buildSidebarSessionOrder, groupSidebarSessions } from "./sidebar-order";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

const session = (id: string, extra: Partial<WorkspaceSession> = {}): WorkspaceSession => ({
	id, title: id, workspaceId: "project", workspaceName: "Project", kind: "worker",
	provider: "codex", status: "working", updatedAt: "2026-11-11T00:00:00Z", prs: [], ...extra,
});

it("按真实归属分两层，普通会话和无法解析父级的 worker 保持可见，导航跟随分组顺序", () => {
	const primary = session("primary", { kind: "orchestrator" });
	const independent = session("independent", { kind: "orchestrator", independentOrchestrator: true });
	const child = session("child", { parentOrchestratorId: primary.id });
	const secondChild = session("second-child", { parentOrchestratorId: independent.id });
	const ordinary = session("ordinary");
	const missing = session("missing", { parentOrchestratorId: "archived" });
	const wrongHost = session("wrong-host", { parentOrchestratorId: primary.id, hostId: "remote" });
	const wrongProject = session("wrong-project", { parentOrchestratorId: primary.id, workspaceId: "other" });
	const sessions = [child, ordinary, independent, secondChild, primary, missing, wrongHost, wrongProject];
	const { roots, childrenByParent } = groupSidebarSessions(sessions);
	expect(roots.map((item) => item.id)).toEqual(["ordinary", "independent", "primary", "missing", "wrong-host", "wrong-project"]);
	expect([...childrenByParent].map(([id, children]) => [id, children.map((item) => item.id)]))
		.toEqual([["primary", ["child"]], ["independent", ["second-child"]]]);
	const workspace: WorkspaceSummary = { id: "project", name: "Project", path: "/project", sessions };
	expect(buildSidebarSessionOrder({ workspaces: [workspace], sessionOrders: { project: sessions.map((item) => item.id) } }).map((item) => item.sessionId))
		.toEqual(["ordinary", "independent", "second-child", "primary", "child", "missing", "wrong-host", "wrong-project"]);
	expect(groupSidebarSessions([child, ordinary]).roots).toEqual([child, ordinary]);
	expect(groupSidebarSessions([child, { ...primary, isTerminated: true }]).childrenByParent.size).toBe(0);
});
