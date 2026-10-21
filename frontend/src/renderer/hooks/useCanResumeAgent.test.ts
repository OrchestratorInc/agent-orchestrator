import { describe, expect, it, vi } from "vitest";
import { sessionInterfaceTransition } from "../test/interface-transition-fixtures";
import type { WorkspaceSession } from "../types/workspace";
import { canResumeAgent, isRetryableResumeError, resumeAgentErrorMessage, resumeAgentOnOpen } from "./useCanResumeAgent";

const post = vi.hoisted(() => vi.fn());
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ POST: post }) }));

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

describe("resumeAgentOnOpen", () => {
	it("preserves sign-in guidance alongside structured failure details", async () => {
		post.mockResolvedValueOnce({
			error: { code: "CHAT_AUTH_REQUIRED", message: "conflict", requestId: "auth-123" },
			response: new Response(null, { status: 409 }),
		});
		const error = await resumeAgentOnOpen("auth-guidance").catch((error: unknown) => error);
		expect(error).toMatchObject({ code: "CHAT_AUTH_REQUIRED", requestId: "auth-123", status: 409 });
		expect((error as Error).message).toContain("sign-in");
		expect(isRetryableResumeError(error)).toBe(false);
	});

	it("retains the transient error code and request ID for recovery", async () => {
		post.mockResolvedValueOnce({
			error: { code: "SERVICE_UNAVAILABLE", message: "Please retry", requestId: "resume-123" },
			response: new Response(null, { status: 503 }),
		});
		const error = await resumeAgentOnOpen("transient").catch((error: unknown) => error);
		expect(error).toMatchObject({ message: "Please retry", code: "SERVICE_UNAVAILABLE", requestId: "resume-123" });
		expect(isRetryableResumeError(error)).toBe(true);
	});

	it("treats a bodyless gateway timeout as a failed, retryable resume", async () => {
		post.mockResolvedValueOnce({ response: new Response(null, { status: 504 }) });
		const error = await resumeAgentOnOpen("gateway-timeout").catch((error: unknown) => error);
		expect(error).toBeInstanceOf(Error);
		expect(isRetryableResumeError(error)).toBe(true);
	});

	it.each(["CHAT_AUTH_REQUIRED", "CHAT_DRIVER_INCOMPATIBLE", "CHAT_RECOVERY_INCONCLUSIVE", "AGENT_RESUME_IN_PROGRESS"])(
		"does not retry %s", async (code) => {
			post.mockResolvedValueOnce({ error: { code, message: "User action required" }, response: new Response(null, { status: 409 }) });
			const error = await resumeAgentOnOpen(code).catch((error: unknown) => error);
			expect(isRetryableResumeError(error)).toBe(false);
		},
	);

	it("shares a resume request between hover, opening, and manual recovery", async () => {
		let finish!: (value: unknown) => void;
		post.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		const first = resumeAgentOnOpen("shared");
		const second = resumeAgentOnOpen("shared");
		expect(second).toBe(first);
		finish({ data: {}, response: new Response() });
		await first;
	});
});
