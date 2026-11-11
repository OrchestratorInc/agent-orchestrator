import { describe, expect, it } from "vitest";
import { newestActiveOrchestrator, sortedProjectSessions, workerSessions, type WorkspaceSession } from "./workspace";
import { buildSidebarSessionOrder } from "../lib/sidebar-order";

const primary: WorkspaceSession = {
	id: "p-1", workspaceId: "p", workspaceName: "Project", title: "Orchestrator", provider: "codex",
	kind: "orchestrator", status: "idle", updatedAt: "2026-11-10T00:00:00Z", prs: [],
};
const independent: WorkspaceSession = { ...primary, id: "p-2", independentOrchestrator: true, updatedAt: "2026-11-10T01:00:00Z" };

describe("独立编排器", () => {
	it("主编排器与独立编排器均列为会话，保留主入口与普通任务分类", () => {
		const worker: WorkspaceSession = { ...primary, id: "p-3", kind: "worker" };
		const sessions = [primary, independent, worker];
		expect(sortedProjectSessions(sessions).map((s) => s.id)).toEqual(["p-2", "p-3", "p-1"]);
		expect(workerSessions(sessions)).toEqual([worker]);
		expect(newestActiveOrchestrator(sessions)).toBe(primary);
		const pinned = { ...independent, isPinned: true };
		const order = buildSidebarSessionOrder({ workspaces: [{ id: "p", name: "Project", path: "/repo", sessions: [primary, worker, pinned] }] });
		expect(order.map((entry) => entry.sessionId)).toEqual(["p-2", "p-3", "p-1"]);
	});

});
