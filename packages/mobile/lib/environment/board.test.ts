import { describe, expect, it, vi } from "vitest";

import type { DashboardSession, ProjectInfo } from "../api";
import { cloudLifecycleStage } from "../cloud/lifecycle";
import type { SessionSource } from "./types";
import * as board from "./board";

const { loadSessionSourceBoard } = board;

const source = (overrides: Partial<SessionSource> = {}): SessionSource => ({
	kind: "local",
	listProjects: vi.fn().mockResolvedValue([]),
	listSessions: vi.fn().mockResolvedValue([]),
	createSession: vi.fn(),
	deleteSession: vi.fn(),
	getConversationPage: vi.fn(),
	sendMessage: vi.fn(),
	cancelTurn: vi.fn(),
	subscribeEvents: vi.fn(),
	resumeSession: vi.fn(),
	...overrides,
});

describe("loadSessionSourceBoard", () => {
	it("returns projects and sessions in one snapshot", async () => {
		const projects = [{ id: "p1", name: "web" }] as ProjectInfo[];
		const sessions = [{ id: "s1", projectId: "p1" }] as DashboardSession[];
		const sessionSource = source({
			listProjects: vi.fn().mockResolvedValue(projects),
			listSessions: vi.fn().mockResolvedValue(sessions),
		});

		expect(await loadSessionSourceBoard(sessionSource)).toEqual({ projects, sessions, orchestrators: [] });
	});

	it("separates Cloud orchestrators from workers and keeps the live project link", async () => {
		const projects = [{ id: "p1", name: "web" }] as ProjectInfo[];
		const worker = {
			id: "worker-1", projectId: "p1", kind: "worker", mode: "chat",
			status: "working", activity: "active", harness: "claude-code",
			branch: "ao/work", issueId: null, issueTitle: null, userPrompt: null,
			displayName: "Build", summary: null, createdAt: "2026-09-01T00:00:00Z",
			lastActivityAt: "2026-09-02T00:00:00Z", isTerminated: false,
		} satisfies DashboardSession;
		const orchestrator = {
			...worker,
			id: "orch-1",
			kind: "orchestrator" as const,
			displayName: "Orchestrator",
			runtimeConnected: true,
			cloud: { sandboxProvider: "ecs", desiredState: "running", observedState: "failed" },
		};
		const sessionSource = source({
			kind: "cloud",
			listProjects: vi.fn().mockResolvedValue(projects),
			listSessions: vi.fn().mockResolvedValue([worker, orchestrator]),
		});

		const result = await loadSessionSourceBoard(sessionSource);

		expect(result.sessions).toEqual([worker]);
		expect(result.orchestrators).toEqual([expect.objectContaining({
			id: "orch-1",
			projectId: "p1",
			projectName: "web",
			status: "working",
			runtimeConnected: true,
			hasRuntime: true,
			isTerminal: false,
		})]);
		expect(cloudLifecycleStage(result.orchestrators[0])).toBe("failed");
	});

	it("rejects when listing projects fails", async () => {
		const error = new Error("projects unavailable");
		const sessionSource = source({
			listProjects: vi.fn().mockRejectedValue(error),
		});

		await expect(loadSessionSourceBoard(sessionSource)).rejects.toBe(error);
	});

	it("rejects when listing sessions fails", async () => {
		const error = new Error("sessions unavailable");
		const sessionSource = source({
			listSessions: vi.fn().mockRejectedValue(error),
		});

		await expect(loadSessionSourceBoard(sessionSource)).rejects.toBe(error);
	});
});

describe("spawnSessionThroughSource", () => {
	it("creates through the active source, refreshes its board, and returns a routable identity", async () => {
		let received: unknown;
		let refreshes = 0;
		const sessionSource = source({
			kind: "cloud",
			createSession: async (options) => {
				received = options;
				return { id: "worker-new" };
			},
		});
		const spawn = (board as unknown as {
			spawnSessionThroughSource?: (
				source: SessionSource,
				options: { projectId?: string; prompt?: string; harness?: string },
				refresh: () => Promise<void>,
			) => Promise<{ id: string; projectId: string }>;
		}).spawnSessionThroughSource;

		const result = typeof spawn === "function"
			? await spawn(sessionSource, { projectId: "project-1", prompt: "Fix it", harness: "claude-code" }, async () => { refreshes += 1; })
			: undefined;

		expect(result).toEqual({ id: "worker-new", projectId: "project-1" });
		expect(received).toEqual({ projectId: "project-1", prompt: "Fix it", harness: "claude-code" });
		expect(refreshes).toBe(1);
	});
});
