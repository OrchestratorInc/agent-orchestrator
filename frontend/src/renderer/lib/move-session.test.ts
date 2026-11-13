import { describe, expect, it, vi } from "vitest";
import { CloudCpError } from "./cloud-cp";
import { MoveError, moveSession, type MoveDeps } from "./move-session";

type Reply = { data?: unknown; error?: unknown };

// fakeDaemon answers requests by route and records each call in order.
function fakeDaemon(replies: Record<string, Reply>, calls: string[]) {
	const answer = vi.fn(async (route: string, init?: { body?: unknown }) => {
		calls.push(route);
		const reply = replies[route];
		if (!reply) throw new Error(`unexpected ${route}`);
		return { ...reply, request: init };
	});
	return { POST: answer, GET: answer } as unknown as MoveDeps["source"];
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
		expect(result).toEqual({ sessionId: "cloud-9", conversation: "note" });
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
		const cp = cloud(async () => { throw new CloudCpError("Another session holds feat/move.", { status: 409, code: "branch_in_use" }); });
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
			.rejects.toMatchObject({ step: "import", rolledBack: true, message: "Could not reach Orchestrator Cloud." });
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

	describe("the agent's conversation", () => {
		const replies = {
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": ok(exported),
			"/api/v1/sessions/{sessionId}/move/transcript": ok({ agentSessionId: "aid-1", transcriptGzip: btoa("gz") }),
			"/api/v1/sessions/{sessionId}/move/complete": ok(),
			"/api/v1/sessions/{sessionId}/resume-agent": ok(),
		};

		it("uploads it after the push and has Cloud resume it", async () => {
			const calls: string[] = [];
			const uploadTranscript = vi.fn(async () => {});
			const cp = cloud();
			const result = await moveSession({ sessionId: "app-1", moveId: "m-5", target }, { source: fakeDaemon(replies, calls), cloud: cp, uploadTranscript });
			expect(result).toEqual({ sessionId: "cloud-9", conversation: "transcript" });
			expect(calls.indexOf("/api/v1/sessions/{sessionId}/move/transcript")).toBe(calls.indexOf("/api/v1/sessions/{sessionId}/move/export") + 1);
			const [importId, agentSessionId, gzip] = uploadTranscript.mock.calls[0] as unknown as [string, string, Uint8Array];
			expect([importId, agentSessionId, Array.from(gzip)]).toEqual(["m-5", "aid-1", [103, 122]]);
			// The note stays the prompt: Cloud starts from it if it cannot place the conversation.
			expect(cp.createSession.mock.calls[0][1]).toMatchObject({ transcriptImportId: "m-5", prompt: exported.handoffNote });
		});

		it("moves the handoff note alone when the upload fails", async () => {
			const cp = cloud();
			const uploadTranscript = vi.fn(async () => { throw new Error("The file is larger than 32 MiB"); });
			const result = await moveSession({ sessionId: "app-1", moveId: "m-6", target }, { source: fakeDaemon(replies, []), cloud: cp, uploadTranscript });
			expect(result.conversation).toBe("note");
			expect(cp.createSession.mock.calls[0][1]).not.toHaveProperty("transcriptImportId");
		});

		it("is not uploaded again when a retry resends the first Cloud request", async () => {
			const calls: string[] = [];
			const uploadTranscript = vi.fn(async () => {});
			const cloudRequest: MoveDeps["cloudRequest"] = {};
			const cp = cloud(async () => { throw new CloudCpError("busy", { status: 503 }); });
			await expect(moveSession({ sessionId: "app-1", moveId: "m-7", target }, { source: fakeDaemon(replies, calls), cloud: cp, cloudRequest, uploadTranscript }))
				.rejects.toMatchObject({ step: "import" });
			cp.createSession.mockImplementation(async () => ({ session: { id: "cloud-9" } }));
			const result = await moveSession({ sessionId: "app-1", moveId: "m-7", target }, { source: fakeDaemon(replies, calls), cloud: cp, cloudRequest, uploadTranscript });
			expect(uploadTranscript).toHaveBeenCalledTimes(1);
			expect(result.conversation).toBe("transcript");
			expect(cp.createSession.mock.calls[1][1]).toEqual(cp.createSession.mock.calls[0][1]);
		});
	});
});
