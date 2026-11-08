import { describe, expect, it, vi } from "vitest";
import { MoveError, moveSession, type MoveDeps } from "./move-session";

type Reply = { data?: unknown; error?: unknown };

// fakeDaemon answers POSTs by route and records each call in order.
function fakeDaemon(replies: Record<string, Reply>, calls: string[]) {
	return {
		POST: vi.fn(async (route: string, init?: { body?: unknown }) => {
			calls.push(route);
			const reply = replies[route];
			if (!reply) throw new Error(`unexpected ${route}`);
			return { ...reply, request: init };
		}),
	} as unknown as MoveDeps["source"];
}

const exported = {
	manifest: { branch: "feat/move", tip: "abc", wip: true, harness: "claude-code", model: "opus", effort: "high", displayName: "Fix login" },
	handoffNote: "<ao-handoff-request>…</ao-handoff-request>",
	changes: 3,
};
const ok = (data: unknown = {}): Reply => ({ data });
const fail = (code: string, message = code): Reply => ({ error: { code, message } });
const target = { orgId: "org-1", projectId: "proj-1", harness: "claude-code" };
const cloud = (impl: () => Promise<unknown> = async () => ({ session: { id: "cloud-9" } })) =>
	({ createSession: vi.fn(impl) }) as unknown as MoveDeps["cloud"] & { createSession: ReturnType<typeof vi.fn> };

describe("moveSession", () => {
	it("stops, pushes, starts the Cloud session on the branch, then marks the source moved", async () => {
		const calls: string[] = [];
		const source = fakeDaemon({
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": ok(exported),
			"/api/v1/sessions/{sessionId}/move/complete": ok(),
		}, calls);
		const cp = cloud();
		const steps: string[] = [];
		const result = await moveSession({ sessionId: "app-1", moveId: "m-2", target }, { source, cloud: cp, onStep: (step) => steps.push(step) });
		expect(result).toEqual({ sessionId: "cloud-9" });
		expect(calls).toEqual([
			"/api/v1/sessions/{sessionId}/exit-agent",
			"/api/v1/sessions/{sessionId}/move/export",
			"/api/v1/sessions/{sessionId}/move/complete",
		]);
		expect(steps).toEqual(["stop", "export", "import", "complete"]);
		expect(cp.createSession).toHaveBeenCalledWith("org-1", {
			projectId: "proj-1", kind: "worker", harness: "claude-code", displayName: "Fix login",
			prompt: exported.handoffNote, branch: "feat/move", model: "opus", reasoningEffort: "high",
		}, { idempotencyKey: "m-2" });
		const completeBody = (source.POST as ReturnType<typeof vi.fn>).mock.calls[2][1].body;
		expect(completeBody).toEqual({ moveId: "m-2", orgId: "org-1", sessionId: "cloud-9" });
	});

	it("continues when the agent had already exited", async () => {
		const calls: string[] = [];
		const source = fakeDaemon({
			"/api/v1/sessions/{sessionId}/exit-agent": fail("AGENT_EXITED"),
			"/api/v1/sessions/{sessionId}/move/export": ok(exported),
			"/api/v1/sessions/{sessionId}/move/complete": ok(),
		}, calls);
		await expect(moveSession({ sessionId: "app-1", target }, { source, cloud: cloud() })).resolves.toMatchObject({ sessionId: "cloud-9" });
	});

	it("resumes the source agent when the push fails", async () => {
		const calls: string[] = [];
		const source = fakeDaemon({
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": fail("MOVE_REMOTE_AHEAD", "Origin's branch has commits this machine does not."),
			"/api/v1/sessions/{sessionId}/resume-agent": ok(),
		}, calls);
		const cp = cloud();
		const error = await moveSession({ sessionId: "app-1", target }, { source, cloud: cp }).catch((e: unknown) => e);
		expect(error).toBeInstanceOf(MoveError);
		expect(error).toMatchObject({ step: "export", code: "MOVE_REMOTE_AHEAD", rolledBack: true, pushed: undefined });
		expect(calls.at(-1)).toBe("/api/v1/sessions/{sessionId}/resume-agent");
		expect(cp.createSession).not.toHaveBeenCalled();
	});

	it("resumes the source agent when AO Cloud refuses the session", async () => {
		const calls: string[] = [];
		const source = fakeDaemon({
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": ok(exported),
			"/api/v1/sessions/{sessionId}/resume-agent": ok(),
		}, calls);
		const cp = cloud(async () => { throw { code: "branch_in_use", message: "Another session holds feat/move." }; });
		const error = await moveSession({ sessionId: "app-1", target }, { source, cloud: cp }).catch((e: unknown) => e);
		expect(error).toMatchObject({
			step: "import", rolledBack: true, message: "Another session holds feat/move.",
			pushed: { branch: "feat/move", tip: "abc", wip: true },
		});
		expect(calls).not.toContain("/api/v1/sessions/{sessionId}/move/complete");
	});

	it("resends the first Cloud request under the same key when a retry's export differs", async () => {
		const calls: string[] = [];
		const replies = {
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": ok(exported),
			"/api/v1/sessions/{sessionId}/resume-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/complete": ok(),
		};
		const cloudRequest: MoveDeps["cloudRequest"] = {};
		// The first create reached AO Cloud, but its response was lost.
		const cp = cloud(async () => { throw new Error("network error"); });
		await expect(moveSession({ sessionId: "app-1", moveId: "m-3", target }, { source: fakeDaemon(replies, calls), cloud: cp, cloudRequest }))
			.rejects.toMatchObject({ step: "import", rolledBack: true });
		replies["/api/v1/sessions/{sessionId}/move/export"] = ok({ ...exported, handoffNote: "a later note", manifest: { ...exported.manifest, wip: false } });
		cp.createSession.mockImplementation(async () => ({ session: { id: "cloud-9" } }));
		await expect(moveSession({ sessionId: "app-1", moveId: "m-3", target }, { source: fakeDaemon(replies, calls), cloud: cp, cloudRequest }))
			.resolves.toMatchObject({ sessionId: "cloud-9" });
		const [first, second] = cp.createSession.mock.calls;
		expect(second).toEqual(first);
		expect(second[1]).toMatchObject({ prompt: exported.handoffNote });
		expect(second[2]).toEqual({ idempotencyKey: "m-3" });
	});

	it("keeps the move when only marking the source fails", async () => {
		const calls: string[] = [];
		const source = fakeDaemon({
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": ok(exported),
			"/api/v1/sessions/{sessionId}/move/complete": fail("INTERNAL", "disk full"),
		}, calls);
		const result = await moveSession({ sessionId: "app-1", target }, { source, cloud: cloud() });
		expect(result).toMatchObject({ sessionId: "cloud-9", completeError: "disk full" });
		expect(calls).not.toContain("/api/v1/sessions/{sessionId}/resume-agent");
	});
});
