import { describe, expect, it, vi } from "vitest";

import type { DashboardSession, ProjectInfo } from "../api";
import type { SessionSource } from "./types";
import { loadSessionSourceBoard } from "./board";

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

		expect(await loadSessionSourceBoard(sessionSource)).toEqual({ projects, sessions });
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
