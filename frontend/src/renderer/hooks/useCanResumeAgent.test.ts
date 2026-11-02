import { describe, expect, it } from "vitest";
import { sessionInterfaceTransition } from "../test/interface-transition-fixtures";
import type { WorkspaceSession } from "../types/workspace";
import { canResumeAgent, resumeAgentErrorMessage } from "./useCanResumeAgent";

const exitedSession: WorkspaceSession = {
	id: "session-1",
	workspaceId: "project-1",
	workspaceName: "Project One",
	title: "Exited agent",
	provider: "codex",
	kind: "worker",
	branch: "ao/session-1",
	status: "exited",
	activity: { state: "exited", lastActivityAt: "2026-09-21T00:00:00Z" },
	updatedAt: "2026-09-21T00:00:00Z",
	prs: [],
};

describe("canResumeAgent", () => {
	it("blocks resume while an interface transition is active", () => {
		expect(canResumeAgent(exitedSession, sessionInterfaceTransition(exitedSession.id))).toBe(false);
	});

	it("allows resume after an interface transition completes", () => {
		expect(canResumeAgent(exitedSession, sessionInterfaceTransition(exitedSession.id, "completed"))).toBe(true);
	});
});

describe("resumeAgentErrorMessage", () => {
	it.each([
		["AGENT_NOT_EXITED", "already running"],
		["AGENT_RESUME_IN_PROGRESS", "already resuming"],
		["CHAT_RESUME_FAILED", "saved conversation"],
		["CHAT_AUTH_REQUIRED", "sign-in"],
	])("explains %s with a next step", (code, fragment) => {
		expect(resumeAgentErrorMessage({ code, message: "conflict" }, 409)).toContain(fragment);
	});
	it("preserves an unknown server message", () => {
		expect(resumeAgentErrorMessage({ code: "OTHER_CONFLICT", message: "Another operation owns the session" }, 409)).toBe("Another operation owns the session");
	});
});
