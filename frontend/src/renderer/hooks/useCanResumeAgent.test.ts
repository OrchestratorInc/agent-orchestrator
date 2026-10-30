import { describe, expect, it } from "vitest";
import { sessionInterfaceTransition } from "../test/interface-transition-fixtures";
import type { WorkspaceSession } from "../types/workspace";
import { canResumeAgent } from "./useCanResumeAgent";

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

// A Qwen chat imported read-only: shown but never resumable from AO's worktree.
const historyOnlyImport: WorkspaceSession = {
	id: "session-2",
	workspaceId: "project-1",
	workspaceName: "Project One",
	title: "Imported Qwen chat",
	provider: "qwen",
	kind: "worker",
	status: "idle",
	needsResume: true,
	activity: { state: "idle", lastActivityAt: "2026-09-21T00:00:00Z" },
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

	it("never resumes a history-only import, so it does not auto-resume, prefetch or show a Resume control", () => {
		expect(canResumeAgent(historyOnlyImport)).toBe(false);
		// A pending import of a resumable harness still resumes on open.
		expect(canResumeAgent({ ...historyOnlyImport, provider: "claude-code" })).toBe(true);
	});
});
