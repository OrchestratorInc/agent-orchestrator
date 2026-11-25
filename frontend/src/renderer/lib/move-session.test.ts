import { describe, expect, it, vi } from "vitest";
import { CloudCpError } from "./cloud-cp";
import { cloudReasoningEffort, MOVE_EXPORT_BUDGET_MS, MOVE_EXPORT_TIMED_OUT, MOVE_IN_PROGRESS, MoveError, moveLeftSessionAsItWas, moveSession, moveSessionHere, TRANSCRIPT_NOT_CAPTURED, type MoveDeps, type MoveHereDeps } from "./move-session";

type Reply = { data?: unknown; error?: unknown };

// fakeDaemon answers requests by route and records each call in order.
function fakeDaemon(replies: Record<string, Reply>, calls: string[]) {
	const answer = vi.fn(async (route: string, init?: { body?: unknown }) => {
		calls.push(route);
		const reply = replies[route];
		if (!reply) throw new Error(`unexpected ${route}`);
		return { ...reply, request: init };
	});
	return { POST: answer, GET: answer, DELETE: answer } as unknown as MoveDeps["source"];
}

const exported = {
	manifest: { branch: "feat/move", tip: "abc", wip: true, harness: "claude-code", model: "opus", effort: "high", displayName: "Fix login" },
	handoffNote: "<ao-handoff-request>…</ao-handoff-request>",
	changes: 3,
};
const ok = (data: unknown = {}): Reply => ({ data });
const fail = (code: string, message = code): Reply => ({ error: { code, message } });
const target = { orgId: "org-1", projectId: "proj-1", harness: "claude-code" };
const cloud = (impl: () => Promise<unknown> = async () => ({ session: { id: "cloud-9" } }), createProject: () => Promise<unknown> = async () => ({ project: { id: "cloud-new" } })) =>
	({ createSession: vi.fn(impl), createProject: vi.fn(createProject) }) as unknown as MoveDeps["cloud"] & Record<"createSession" | "createProject", ReturnType<typeof vi.fn>>;

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
		expect(result).toEqual({ projectId: "proj-1", sessionId: "cloud-9", conversation: "note" });
		expect(calls).toEqual([
			"/api/v1/sessions/{sessionId}/exit-agent",
			"/api/v1/sessions/{sessionId}/move/export",
			"/api/v1/sessions/{sessionId}/move/complete",
		]);
		expect(steps).toEqual(["stop", "export", "import", "complete"]);
		expect(cp.createSession).toHaveBeenCalledWith("org-1", {
			projectId: "proj-1", kind: "worker", harness: "claude-code", displayName: "Fix login",
			prompt: exported.handoffNote, branch: "feat/move", interfaceMode: "tui", model: "opus", reasoningEffort: "high",
		}, { idempotencyKey: "m-2" });
		const completeBody = (source.POST as ReturnType<typeof vi.fn>).mock.calls[2][1].body;
		expect(completeBody).toEqual({ moveId: "m-2", orgId: "org-1", sessionId: "cloud-9" });
	});

	it("keeps a session in Chat in Chat, with its approval mode, when Cloud runs its agent in Chat; otherwise in the terminal, and says so", async () => {
		const chatExport = { ...exported, manifest: { ...exported.manifest, mode: "chat", permissions: "accept-edits" } };
		const run = async (chat: boolean) => {
			const source = fakeDaemon({
				"/api/v1/sessions/{sessionId}/exit-agent": ok(),
				"/api/v1/sessions/{sessionId}/move/export": ok(chatExport),
				"/api/v1/sessions/{sessionId}/move/complete": ok(),
			}, []);
			const cp = cloud();
			const result = await moveSession({ sessionId: "app-1", moveId: "m-chat", target: { ...target, chat } }, { source, cloud: cp });
			return { request: cp.createSession.mock.calls[0][1], result };
		};
		const inChat = await run(true);
		expect(inChat.request).toMatchObject({ interfaceMode: "chat", approvalMode: "accept-edits" });
		expect(inChat.result.warnings ?? []).toEqual([]);
		const inTerminal = await run(false);
		expect(inTerminal.request).toMatchObject({ interfaceMode: "tui", approvalMode: "accept-edits" });
		expect(inTerminal.result.warnings).toEqual([{ kind: "interface", reason: "claude-code" }]);
	});

	describe("when Orchestrator Cloud has no project for the repository", () => {
		const newProject = { displayName: "app", repositoryUrl: "https://github.com/acme/app", defaultBranch: "main" };
		const noProject = { ...target, projectId: "" };

		it("creates it before stopping the agent, then starts the session in it", async () => {
			const calls: string[] = [];
			const source = fakeDaemon({
				"/api/v1/sessions/{sessionId}/exit-agent": ok(),
				"/api/v1/sessions/{sessionId}/move/export": ok(exported),
				"/api/v1/sessions/{sessionId}/move/complete": ok(),
			}, calls);
			const cp = cloud();
			const steps: string[] = [];
			const onProjectCreated = vi.fn();
			const result = await moveSession(
				{ sessionId: "app-1", moveId: "m-2", target: noProject, createProject: newProject },
				{ source, cloud: cp, onStep: (step) => steps.push(step), onProjectCreated },
			);
			expect(steps).toEqual(["project", "stop", "export", "import", "complete"]);
			expect(cp.createProject).toHaveBeenCalledWith("org-1", newProject, { idempotencyKey: "m-2-project" });
			expect(onProjectCreated).toHaveBeenCalledWith("cloud-new");
			expect(cp.createSession.mock.calls[0][1]).toMatchObject({ projectId: "cloud-new" });
			expect(result).toMatchObject({ projectId: "cloud-new", sessionId: "cloud-9" });
		});

		it("leaves the session here as it was when Cloud refuses the project", async () => {
			const calls: string[] = [];
			const source = fakeDaemon({}, calls);
			const cp = cloud(undefined, async () => { throw new CloudCpError("The GitHub App cannot reach acme/app.", { status: 422, code: "repository_unavailable" }); });
			const error = await moveSession({ sessionId: "app-1", target: noProject, createProject: newProject }, { source, cloud: cp }).catch((e: unknown) => e);
			expect(error).toMatchObject({ step: "project", code: "repository_unavailable", rolledBack: true, pushed: undefined, message: "The GitHub App cannot reach acme/app." });
			expect(calls).toEqual([]);
			expect(cp.createSession).not.toHaveBeenCalled();
		});
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

	it("asks Cloud again under the same key when a create's answer is lost", async () => {
		const calls: string[] = [];
		const source = fakeDaemon({
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": ok(exported),
			"/api/v1/sessions/{sessionId}/move/complete": ok(),
		}, calls);
		const cp = cloud();
		cp.createSession.mockRejectedValueOnce(new CloudCpError("Bad gateway", { status: 502 }));
		await expect(moveSession({ sessionId: "app-1", moveId: "m-4", target }, { source, cloud: cp })).resolves.toMatchObject({ sessionId: "cloud-9" });
		const [first, second] = cp.createSession.mock.calls;
		expect(second).toEqual(first);
		expect(calls).not.toContain("/api/v1/sessions/{sessionId}/resume-agent");
	});

	it("keeps the agent here stopped when a refusal follows a lost answer, also on Try again", async () => {
		const calls: string[] = [];
		const replies = {
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": ok(exported),
			"/api/v1/sessions/{sessionId}/resume-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/complete": ok(),
		};
		const cloudRequest: MoveDeps["cloudRequest"] = {};
		const expired = new CloudCpError("Sign in again.", { status: 401, code: "unauthorized" });
		const cp = cloud();
		// The first create may have reached Cloud; the retry is refused because the sign-in expired.
		cp.createSession.mockRejectedValueOnce(new Error("network error")).mockRejectedValueOnce(expired);
		await expect(moveSession({ sessionId: "app-1", moveId: "m-6", target }, { source: fakeDaemon(replies, calls), cloud: cp, cloudRequest }))
			.rejects.toMatchObject({ step: "import", rolledBack: false, message: expect.stringContaining("Sign in again.") });
		// Try again is refused too, and still does not resume the agent.
		cp.createSession.mockRejectedValueOnce(expired);
		await expect(moveSession({ sessionId: "app-1", moveId: "m-6", target }, { source: fakeDaemon(replies, calls), cloud: cp, cloudRequest }))
			.rejects.toMatchObject({ rolledBack: false });
		expect(calls).not.toContain("/api/v1/sessions/{sessionId}/resume-agent");
		// Once Cloud answers, the move finishes in the session the first create made.
		await expect(moveSession({ sessionId: "app-1", moveId: "m-6", target }, { source: fakeDaemon(replies, calls), cloud: cp, cloudRequest }))
			.resolves.toMatchObject({ sessionId: "cloud-9" });
	});

	it("asks Cloud for an unanswered create's session on Try again before exporting again", async () => {
		const cloudRequest: MoveDeps["cloudRequest"] = {};
		const cp = cloud(async () => { throw new Error("network error"); });
		const first: string[] = [];
		await expect(moveSession({ sessionId: "app-1", moveId: "m-7", target }, {
			source: fakeDaemon({ "/api/v1/sessions/{sessionId}/exit-agent": ok(), "/api/v1/sessions/{sessionId}/move/export": ok(exported) }, first),
			cloud: cp,
			cloudRequest,
		})).rejects.toMatchObject({ rolledBack: false });
		// The Cloud session has since pushed to the branch, so a new export would be refused.
		const retry: string[] = [];
		const source = fakeDaemon({
			"/api/v1/sessions/{sessionId}/move/export": fail("MOVE_REMOTE_AHEAD", "Origin's branch has commits this machine does not."),
			"/api/v1/sessions/{sessionId}/resume-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/complete": ok(),
		}, retry);
		cp.createSession.mockImplementation(async () => ({ session: { id: "cloud-9" } }));
		const steps: string[] = [];
		await expect(moveSession({ sessionId: "app-1", moveId: "m-7", target }, { source, cloud: cp, cloudRequest, onStep: (step) => steps.push(step) }))
			.resolves.toMatchObject({ sessionId: "cloud-9" });
		expect(retry).toEqual(["/api/v1/sessions/{sessionId}/move/complete"]);
		expect(steps).toEqual(["import", "complete"]);
		expect(cp.createSession.mock.calls.at(-1)).toEqual(cp.createSession.mock.calls[0]);
	});

	it("never resumes the agent here while a create of the move is unsettled", async () => {
		const calls: string[] = [];
		const source = fakeDaemon({
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": fail("MOVE_REMOTE_AHEAD", "Origin's branch has commits this machine does not."),
			"/api/v1/sessions/{sessionId}/resume-agent": ok(),
		}, calls);
		const error = await moveSession({ sessionId: "app-1", moveId: "m-8", target }, { source, cloud: cloud(), cloudRequest: { createUnsettled: true } }).catch((e: unknown) => e);
		expect(error).toMatchObject({ step: "export", rolledBack: false, message: expect.stringContaining("An earlier attempt may already have started the session") });
		expect(calls).not.toContain("/api/v1/sessions/{sessionId}/resume-agent");
	});

	it("leaves recording the move to the retry queue when this computer does not answer", async () => {
		// No reply for move/complete: the daemon client throws, as it does when the request gets no answer.
		const source = fakeDaemon({
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": ok(exported),
		}, []);
		const result = await moveSession({ sessionId: "app-1", moveId: "m-5", target }, { source, cloud: cloud() });
		expect(result).toMatchObject({ sessionId: "cloud-9", completeError: "unexpected /api/v1/sessions/{sessionId}/move/complete" });
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
		// The first creates reached AO Cloud, but their answers were lost.
		const cp = cloud(async () => { throw new Error("network error"); });
		await expect(moveSession({ sessionId: "app-1", moveId: "m-3", target }, { source: fakeDaemon(replies, calls), cloud: cp, cloudRequest }))
			.rejects.toMatchObject({ step: "import", rolledBack: false, message: expect.stringContaining("Orchestrator Cloud did not answer") });
		// The session may run in Cloud, so the agent here stays stopped.
		expect(calls).not.toContain("/api/v1/sessions/{sessionId}/resume-agent");
		replies["/api/v1/sessions/{sessionId}/move/export"] = ok({ ...exported, handoffNote: "a later note", manifest: { ...exported.manifest, wip: false } });
		cp.createSession.mockImplementation(async () => ({ session: { id: "cloud-9" } }));
		await expect(moveSession({ sessionId: "app-1", moveId: "m-3", target }, { source: fakeDaemon(replies, calls), cloud: cp, cloudRequest }))
			.resolves.toMatchObject({ sessionId: "cloud-9" });
		const [first, second] = cp.createSession.mock.calls;
		expect(second).toEqual(first);
		expect(second[1]).toMatchObject({ prompt: exported.handoffNote });
		expect(second[2]).toEqual({ idempotencyKey: "m-3" });
	});

	it("keeps the session's approval mode in Orchestrator Cloud", async () => {
		const source = fakeDaemon({
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": ok({ ...exported, manifest: { ...exported.manifest, permissions: "bypass-permissions" } }),
			"/api/v1/sessions/{sessionId}/move/complete": ok(),
		}, []);
		const cp = cloud();
		await moveSession({ sessionId: "app-1", moveId: "m-2", target }, { source, cloud: cp });
		expect(cp.createSession.mock.calls[0][1]).toMatchObject({ approvalMode: "bypass-permissions" });
		// Another agent's modes are its own, and an unknown one is left to Cloud.
		const other = fakeDaemon({
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": ok({ ...exported, manifest: { ...exported.manifest, permissions: "yolo" } }),
			"/api/v1/sessions/{sessionId}/move/complete": ok(),
		}, []);
		await moveSession({ sessionId: "app-1", moveId: "m-3", target }, { source: other, cloud: cp });
		expect(cp.createSession.mock.calls[1][1]).not.toHaveProperty("approvalMode");
	});

	it("exports under the same move id on Try again after the export got no answer", async () => {
		const calls: string[] = [];
		const replies: Record<string, Reply> = {
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/complete": ok(),
		};
		const source = fakeDaemon(replies, calls);
		const cp = cloud();
		await expect(moveSession({ sessionId: "app-1", moveId: "m-4", target }, { source, cloud: cp })).rejects.toMatchObject({ step: "export", followUp: "unknown" });
		replies["/api/v1/sessions/{sessionId}/move/export"] = ok(exported);
		await moveSession({ sessionId: "app-1", moveId: "m-4", target }, { source, cloud: cp });
		const exports = (source.POST as ReturnType<typeof vi.fn>).mock.calls.filter((call) => call[0] === "/api/v1/sessions/{sessionId}/move/export");
		expect(exports.map((call) => (call[1] as { body: { moveId: string } }).body.moveId)).toEqual(["m-4", "m-4"]);
		expect(cp.createSession.mock.calls[0][2]).toEqual({ idempotencyKey: "m-4" });
	});

	it("hands each Cloud request over before creating it, so a relaunch can finish the move", async () => {
		const order: string[] = [];
		const source = fakeDaemon({
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": ok(exported),
			"/api/v1/sessions/{sessionId}/move/complete": ok(),
		}, []);
		const cp = cloud(async () => { order.push("create"); return { session: { id: "cloud-9" } }; });
		const onCreate = vi.fn(() => { order.push("saved"); });
		await moveSession({ sessionId: "app-1", moveId: "m-2", target }, { source, cloud: cp, onCreate });
		expect(order).toEqual(["saved", "create"]);
		expect(onCreate).toHaveBeenCalledWith({
			request: cp.createSession.mock.calls[0][1], key: "m-2", pushed: { branch: "feat/move", tip: "abc", wip: true },
		});
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
			expect(result).toEqual({ projectId: "proj-1", sessionId: "cloud-9", conversation: "transcript" });
			expect(calls.indexOf("/api/v1/sessions/{sessionId}/move/transcript")).toBe(calls.indexOf("/api/v1/sessions/{sessionId}/move/export") + 1);
			const [importId, harness, agentSessionId, gzip] = uploadTranscript.mock.calls[0] as unknown as [string, string, string, Uint8Array];
			expect([importId, harness, agentSessionId, Array.from(gzip)]).toEqual(["m-5", "claude-code", "aid-1", [103, 122]]);
			// The note stays the prompt: Cloud starts from it if it cannot place the conversation.
			expect(cp.createSession.mock.calls[0][1]).toMatchObject({ transcriptImportId: "m-5", prompt: exported.handoffNote });
		});

		it("uploads the sidecar folder after the transcript, and moves the transcript when that fails", async () => {
			const withSidecar = { ...replies, "/api/v1/sessions/{sessionId}/move/transcript": ok({ agentSessionId: "aid-1", transcriptGzip: btoa("gz"), sidecarGzip: btoa("tar") }) };
			const order: string[] = [];
			const uploadTranscript = vi.fn(async () => { order.push("transcript"); });
			const uploadTranscriptSidecar = vi.fn(async () => { order.push("sidecar"); throw new Error("over 32 MiB"); });
			const cp = cloud();
			const result = await moveSession({ sessionId: "app-1", moveId: "m-9", target }, { source: fakeDaemon(withSidecar, []), cloud: cp, uploadTranscript, uploadTranscriptSidecar });
			expect(order).toEqual(["transcript", "sidecar"]);
			const [importId, gzip] = uploadTranscriptSidecar.mock.calls[0] as unknown as [string, Uint8Array];
			expect([importId, Array.from(gzip)]).toEqual(["m-9", [116, 97, 114]]);
			expect(result.conversation).toBe("transcript");
			expect(cp.createSession.mock.calls[0][1]).toMatchObject({ transcriptImportId: "m-9" });
		});

		it("uploads an OpenCode 2 conversation for Cloud's opencode", async () => {
			const cp = cloud();
			const uploadTranscript = vi.fn(async () => {});
			const v2 = { ...replies, "/api/v1/sessions/{sessionId}/move/export": ok({ ...exported, manifest: { ...exported.manifest, harness: "opencode-v2" } }) };
			const result = await moveSession({ sessionId: "app-1", moveId: "m-11", target: { ...target, harness: "opencode" } }, { source: fakeDaemon(v2, []), cloud: cp, uploadTranscript });
			expect((uploadTranscript.mock.calls[0] as unknown as string[])[1]).toBe("opencode");
			expect(result.conversation).toBe("transcript");
			expect(cp.createSession.mock.calls[0][1]).toMatchObject({ harness: "opencode", transcriptImportId: "m-11" });
		});

		it("is not uploaded when Cloud runs another agent", async () => {
			const cp = cloud();
			const uploadTranscript = vi.fn(async () => {});
			const result = await moveSession({ sessionId: "app-1", moveId: "m-10", target: { ...target, harness: "codex" } }, { source: fakeDaemon(replies, []), cloud: cp, uploadTranscript });
			expect(uploadTranscript).not.toHaveBeenCalled();
			expect(result.conversation).toBe("note");
		});

		it("says why when Cloud refuses the conversation, or its sidecar folder, never silently", async () => {
			const limit = "Too many uploaded conversations are waiting to be resumed. Finish or cancel a move, or wait a day.";
			const refused = await moveSession({ sessionId: "app-1", moveId: "m-20", target }, {
				source: fakeDaemon(replies, []), cloud: cloud(), uploadTranscript: vi.fn(async () => { throw new Error(limit); }),
			});
			expect(refused).toMatchObject({ conversation: "note", warnings: [{ kind: "transcript", reason: limit }] });

			const omitted = { ...replies, "/api/v1/sessions/{sessionId}/move/transcript": ok({ agentSessionId: "aid-1", transcriptGzip: btoa("gz"), sidecarOmitted: "too_large" }) };
			const uploadTranscriptSidecar = vi.fn(async () => {});
			const withoutSidecar = await moveSession({ sessionId: "app-1", moveId: "m-21", target }, {
				source: fakeDaemon(omitted, []), cloud: cloud(), uploadTranscript: vi.fn(async () => {}), uploadTranscriptSidecar,
			});
			expect(withoutSidecar).toMatchObject({ conversation: "transcript", warnings: [{ kind: "sidecar", reason: "It is over Orchestrator Cloud's upload limit." }] });
			expect(uploadTranscriptSidecar).not.toHaveBeenCalled();

			const withSidecar = { ...replies, "/api/v1/sessions/{sessionId}/move/transcript": ok({ agentSessionId: "aid-1", transcriptGzip: btoa("gz"), sidecarGzip: btoa("tar") }) };
			const sidecarFailed = await moveSession({ sessionId: "app-1", moveId: "m-22", target }, {
				source: fakeDaemon(withSidecar, []), cloud: cloud(), uploadTranscript: vi.fn(async () => {}),
				uploadTranscriptSidecar: vi.fn(async () => { throw new Error("The body is over 32 MiB."); }),
			});
			expect(sidecarFailed).toMatchObject({ conversation: "transcript", warnings: [{ kind: "sidecar", reason: "The body is over 32 MiB." }] });
		});

		it("moves the handoff note alone when the upload fails", async () => {
			const cp = cloud();
			const uploadTranscript = vi.fn(async () => { throw new Error("The file is larger than 32 MiB"); });
			const result = await moveSession({ sessionId: "app-1", moveId: "m-6", target }, { source: fakeDaemon(replies, []), cloud: cp, uploadTranscript });
			expect(result.conversation).toBe("note");
			expect(cp.createSession.mock.calls[0][1]).not.toHaveProperty("transcriptImportId");
		});

		it("moves the handoff note alone when reading the conversation gets no answer", async () => {
			const cp = cloud();
			const uploadTranscript = vi.fn(async () => {});
			const source = fakeDaemon({ ...replies, "/api/v1/sessions/{sessionId}/move/transcript": undefined as unknown as Reply }, []);
			const result = await moveSession({ sessionId: "app-1", moveId: "m-8", target }, { source, cloud: cp, uploadTranscript });
			expect(result).toEqual({
				projectId: "proj-1", sessionId: "cloud-9", conversation: "note",
				warnings: [{ kind: "transcript", reason: "This computer's Orchestrator did not answer." }],
			});
			expect(uploadTranscript).not.toHaveBeenCalled();
			expect(cp.createSession.mock.calls[0][1]).not.toHaveProperty("transcriptImportId");
		});

		it("is uploaded again under a fresh id when Try again follows Cloud's refusal", async () => {
			const uploadTranscript = vi.fn(async () => {});
			const cloudRequest: MoveDeps["cloudRequest"] = {};
			const cp = cloud(async () => { throw new CloudCpError("Another session holds feat/move.", { status: 409, code: "branch_in_use" }); });
			await expect(moveSession({ sessionId: "app-1", moveId: "m-12", target }, { source: fakeDaemon(replies, []), cloud: cp, cloudRequest, uploadTranscript }))
				.rejects.toMatchObject({ step: "import", rolledBack: true });
			// The agent here was resumed and may have gone on; the next export's note is newer.
			const later = { ...replies, "/api/v1/sessions/{sessionId}/move/export": ok({ ...exported, handoffNote: "a later note" }) };
			cp.createSession.mockImplementation(async () => ({ session: { id: "cloud-9" } }));
			await moveSession({ sessionId: "app-1", moveId: "m-12", target }, { source: fakeDaemon(later, []), cloud: cp, cloudRequest, uploadTranscript });
			const [firstId, secondId] = uploadTranscript.mock.calls.map((call) => (call as unknown as string[])[0]);
			expect(firstId).toBe("m-12");
			expect(secondId).not.toBe("m-12");
			const [first, second] = cp.createSession.mock.calls;
			expect(first[2]).toEqual({ idempotencyKey: "m-12" });
			expect(second[1]).toMatchObject({ prompt: "a later note", transcriptImportId: secondId });
			expect(second[2]).toEqual({ idempotencyKey: secondId });
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

describe("moveSessionHere", () => {
	const tip = "a".repeat(40);
	const cloudExport = {
		manifest: { sourceSessionId: "cloud-1", sourceLabel: "AO Cloud", repoKey: "github.com/acme/app", branch: "feat/login", tip, wip: true, harness: "claude-code" },
		handoffNote: "moved",
		transcript: { agentSessionId: "conv-1", projectDir: "/p", transcriptGzip: "H4sI" },
	};
	const settings = { autoReview: true, terminateOnPrMerge: false, autoInjectReview: true, autoInjectCI: false };
	const input = { orgId: "org-1", cloudSessionId: "cloud-1", projectId: "proj-1", moveId: "m-1", settings };
	const cloudFake = (over: Partial<Record<"exportSessionMove" | "getSessionMoveExport" | "restartSessionAgent" | "deleteSession", () => Promise<unknown>>> = {}) => ({
		exportSessionMove: vi.fn(over.exportSessionMove ?? (async () => cloudExport)),
		getSessionMoveExport: vi.fn(over.getSessionMoveExport ?? (async () => cloudExport)),
		restartSessionAgent: vi.fn(over.restartSessionAgent ?? (async () => ({ requested: true }))),
		deleteSession: vi.fn(over.deleteSession ?? (async () => ({ session: { id: "cloud-1", desiredState: "deleted" } }))),
	}) as unknown as MoveHereDeps["cloud"] & Record<"exportSessionMove" | "getSessionMoveExport" | "restartSessionAgent" | "deleteSession", ReturnType<typeof vi.fn>>;
	const daemon = (reply: Reply, calls: string[] = []) => fakeDaemon({ "/api/v1/sessions/move/import": reply }, calls) as unknown as MoveHereDeps["daemon"];

	it("pushes in Cloud, starts the session here with the conversation, then deletes the Cloud session", async () => {
		const cp = cloudFake();
		const local = daemon(ok({ session: { id: "local-9" }, warnings: ["w"] }));
		const steps: string[] = [];
		const result = await moveSessionHere(input, { daemon: local, cloud: cp, onStep: (step) => steps.push(step) });
		expect(result).toEqual({ projectId: "proj-1", sessionId: "local-9", conversation: "transcript", warnings: ["w"] });
		expect(steps).toEqual(["stop", "import", "complete"]);
		const body = (local.POST as ReturnType<typeof vi.fn>).mock.calls[0][1].body;
		expect(body).toEqual({
			projectId: "proj-1", moveId: "m-1", handoffNote: "moved", transcript: cloudExport.transcript,
			manifest: { ...cloudExport.manifest, ...settings },
		});
		expect(cp.deleteSession).toHaveBeenCalledWith("org-1", "cloud-1");
		expect(cp.restartSessionAgent).not.toHaveBeenCalled();
	});

	it("starts the session here in the interface it had in Cloud", async () => {
		const bodyFor = async (cloudInterface?: "chat" | "terminal") => {
			const cp = cloudFake({ exportSessionMove: async () => ({ ...cloudExport, manifest: { ...cloudExport.manifest, ...(cloudInterface ? { interface: cloudInterface } : {}) } }) });
			const local = daemon(ok({ session: { id: "local-9" } }));
			await moveSessionHere(input, { daemon: local, cloud: cp });
			return (local.POST as ReturnType<typeof vi.fn>).mock.calls[0][1].body.manifest;
		};
		const chat = await bodyFor("chat");
		expect(chat).toMatchObject({ mode: "chat" });
		expect(chat).not.toHaveProperty("interface");
		expect(await bodyFor("terminal")).toMatchObject({ mode: "tui" });
		// An older control plane says nothing: this computer's default.
		expect(await bodyFor()).not.toHaveProperty("mode");
	});

	it("starts the Cloud agent again when Cloud cannot push", async () => {
		const cp = cloudFake({ exportSessionMove: async () => { throw new CloudCpError("could not fetch origin", { status: 502, code: "WORKER_REQUEST_FAILED" }); } });
		const calls: string[] = [];
		const error = await moveSessionHere(input, { daemon: daemon(ok(), calls), cloud: cp }).catch((e: unknown) => e);
		expect(error).toMatchObject({ step: "export", code: "WORKER_REQUEST_FAILED", rolledBack: true, pushed: undefined, message: "could not fetch origin" });
		expect(calls).toEqual([]);
		expect(cp.deleteSession).not.toHaveBeenCalled();
	});

	it("starts the Cloud agent again and names the pushed commit when this computer refuses", async () => {
		const cp = cloudFake();
		const error = await moveSessionHere(input, { daemon: daemon(fail("MOVE_LOCAL_AHEAD", "feat/login here has commits")), cloud: cp }).catch((e: unknown) => e);
		expect(error).toBeInstanceOf(MoveError);
		expect(error).toMatchObject({ step: "import", code: "MOVE_LOCAL_AHEAD", rolledBack: true, pushed: { branch: "feat/login", tip, wip: true } });
		expect(cp.restartSessionAgent).toHaveBeenCalledWith("org-1", "cloud-1");
		expect(cp.deleteSession).not.toHaveBeenCalled();
	});

	it("keeps the Cloud agent stopped when this computer does not answer the import", async () => {
		const cp = cloudFake();
		// No reply for move/import: the daemon client throws.
		const error = await moveSessionHere(input, { daemon: fakeDaemon({}, []) as unknown as MoveHereDeps["daemon"], cloud: cp }).catch((e: unknown) => e);
		expect(error).toMatchObject({ step: "import", rolledBack: false, pushed: { branch: "feat/login", tip, wip: true } });
		expect(cp.restartSessionAgent).not.toHaveBeenCalled();
		expect(cp.deleteSession).not.toHaveBeenCalled();
	});

	it("says the agent stayed stopped when Cloud cannot start it again", async () => {
		const cp = cloudFake({ restartSessionAgent: async () => { throw new Error("offline"); } });
		const error = await moveSessionHere(input, { daemon: daemon(fail("MOVE_IMPORT_FAILED")), cloud: cp }).catch((e: unknown) => e);
		expect(error).toMatchObject({ step: "import", rolledBack: false });
	});

	it("keeps the session here when Cloud does not delete its copy", async () => {
		const cp = cloudFake({ deleteSession: async () => { throw new CloudCpError("busy", { status: 503 }); } });
		const result = await moveSessionHere(input, { daemon: daemon(ok({ session: { id: "local-9" } })), cloud: cp });
		expect(result).toMatchObject({ sessionId: "local-9", deleteError: "busy" });
		expect(cp.restartSessionAgent).not.toHaveBeenCalled();
	});

	it("keeps the Cloud session when part of the conversation did not move here", async () => {
		const cp = cloudFake();
		const warning = "Subagent transcripts and saved tool output stayed in Orchestrator Cloud: bad tar";
		const steps: string[] = [];
		const result = await moveSessionHere(input, {
			daemon: daemon(ok({ session: { id: "local-9" }, warnings: [warning], conversationIncomplete: true })),
			cloud: cp,
			onStep: (step) => steps.push(step),
		});
		expect(result).toEqual({ projectId: "proj-1", sessionId: "local-9", conversation: "transcript", warnings: [warning], keptInCloud: true });
		expect(steps).toEqual(["stop", "import"]);
		expect(cp.deleteSession).not.toHaveBeenCalled();
	});

	describe("an export Orchestrator Cloud is still running (202)", () => {
		const running = (retryAfterMs?: number) => ({ status: "running", moveId: "exp-1", branchMayBePushed: true, ...(retryAfterMs ? { retryAfterMs } : {}) });

		it("is asked for again, honoring Retry-After within 3 to 5 s, until it answers with the move", async () => {
			const cp = cloudFake({ exportSessionMove: async () => running(10_000) });
			cp.getSessionMoveExport.mockResolvedValueOnce(running()).mockResolvedValueOnce(running(3_500)).mockResolvedValueOnce(cloudExport);
			const delays: number[] = [];
			const moves: (string | undefined)[] = [];
			const onExportRunning = vi.fn();
			const result = await moveSessionHere({ ...input, branch: "feat/login" }, {
				daemon: daemon(ok({ session: { id: "local-9" } })), cloud: cp, onExportRunning,
				onExportMove: (id) => moves.push(id), wait: async (ms) => { delays.push(ms); },
			});
			expect(result).toMatchObject({ sessionId: "local-9" });
			expect(delays).toEqual([5_000, 4_000, 3_500]);
			expect(cp.getSessionMoveExport).toHaveBeenCalledWith("org-1", "cloud-1", "exp-1");
			expect(onExportRunning).toHaveBeenCalledTimes(3);
			// Done: a later Try again asks Cloud again, without this moveId.
			expect(moves).toEqual(["exp-1", "exp-1", "exp-1", undefined]);
			expect(cp.deleteSession).toHaveBeenCalledWith("org-1", "cloud-1");
		});

		it("names the branch that may be pushed when it times out, and restarts the Cloud agent", async () => {
			const timedOut = (branchMayBePushed: boolean) => new CloudCpError("AO Cloud started this move but did not finish it in time.", {
				status: 504, code: MOVE_EXPORT_TIMED_OUT, details: { moveId: "exp-1", branchMayBePushed },
			});
			const cp = cloudFake({ exportSessionMove: async () => running(), getSessionMoveExport: async () => { throw timedOut(true); } });
			const moves: (string | undefined)[] = [];
			const calls: string[] = [];
			const error = await moveSessionHere({ ...input, branch: "feat/login" }, {
				daemon: daemon(ok(), calls), cloud: cp, onExportMove: (id) => moves.push(id), wait: async () => {},
			}).catch((e: unknown) => e);
			expect(error).toMatchObject({ step: "export", code: MOVE_EXPORT_TIMED_OUT, mayBePushed: "feat/login", rolledBack: true });
			expect(cp.restartSessionAgent).toHaveBeenCalledWith("org-1", "cloud-1");
			// That export can no longer finish, so Try again starts another.
			expect(moves.at(-1)).toBeUndefined();
			expect(calls).toEqual([]);
			const notPushed = cloudFake({ exportSessionMove: async () => { throw timedOut(false); } });
			await expect(moveSessionHere({ ...input, branch: "feat/login" }, { daemon: daemon(ok()), cloud: notPushed }))
				.rejects.toMatchObject({ code: MOVE_EXPORT_TIMED_OUT, mayBePushed: undefined });
		});

		it("is adopted on Try again under its moveId, without a second export", async () => {
			const cp = cloudFake();
			const result = await moveSessionHere({ ...input, exportMoveId: "exp-1" }, { daemon: daemon(ok({ session: { id: "local-9" } })), cloud: cp });
			expect(result).toMatchObject({ sessionId: "local-9" });
			expect(cp.exportSessionMove).toHaveBeenCalledTimes(1);
			expect(cp.exportSessionMove).toHaveBeenCalledWith("org-1", "cloud-1", { moveId: "exp-1" });
		});

		it("keeps the Cloud agent stopped and its moveId when an answer is lost, or the lease runs out", async () => {
			const lost = cloudFake({ exportSessionMove: async () => running(), getSessionMoveExport: async () => { throw new CloudCpError("fetch failed", { status: 0 }); } });
			const moves: (string | undefined)[] = [];
			await expect(moveSessionHere(input, { daemon: daemon(ok()), cloud: lost, onExportMove: (id) => moves.push(id), wait: async () => {} }))
				.rejects.toMatchObject({ step: "export", rolledBack: false, followUp: "settle" });
			expect(lost.restartSessionAgent).not.toHaveBeenCalled();
			expect(moves.at(-1)).toBe("exp-1");

			let clock = 0;
			const slow = cloudFake({ exportSessionMove: async () => running(), getSessionMoveExport: async () => running() });
			await expect(moveSessionHere(input, {
				daemon: daemon(ok()), cloud: slow, now: () => clock, wait: async (ms) => { clock += ms * 30; },
			})).rejects.toMatchObject({ step: "export", followUp: "settle" });
			expect(clock).toBeGreaterThanOrEqual(MOVE_EXPORT_BUDGET_MS);
			expect(slow.restartSessionAgent).not.toHaveBeenCalled();
		});

		it("keeps waiting when Cloud will not restart the agent because the export runs after all, and Try again adopts it", async () => {
			// The export's answer is lost before its moveId came back, and the
			// restart that would keep the session in Cloud is refused: the export
			// still runs and holds the session.
			const lost = cloudFake({
				exportSessionMove: async () => { throw new CloudCpError("fetch failed", { status: 0 }); },
				restartSessionAgent: async () => { throw new CloudCpError("moving right now", { status: 409, code: MOVE_IN_PROGRESS }); },
			});
			const moves: (string | undefined)[] = [];
			const calls: string[] = [];
			const error = await moveSessionHere({ ...input, branch: "feat/login" }, {
				daemon: daemon(ok(), calls), cloud: lost, onExportMove: (id) => moves.push(id), wait: async () => {},
			}).catch((e: unknown) => e);
			expect(error).toBeInstanceOf(MoveError);
			expect(error).toMatchObject({ step: "export", code: MOVE_IN_PROGRESS, rolledBack: false, followUp: "settle", mayBePushed: "feat/login" });
			expect(lost.restartSessionAgent).toHaveBeenCalledTimes(1);
			expect(calls).toEqual([]);
			expect(lost.deleteSession).not.toHaveBeenCalled();

			// Try again has no moveId to wait on; asked again, Cloud answers with
			// the move that holds the session, and the move finishes under it.
			const again = cloudFake({ exportSessionMove: async () => running() });
			again.getSessionMoveExport.mockResolvedValueOnce({ ...cloudExport, moveId: "exp-1" });
			const result = await moveSessionHere({ ...input, branch: "feat/login" }, { daemon: daemon(ok({ session: { id: "local-9" } })), cloud: again, wait: async () => {} });
			expect(result).toMatchObject({ sessionId: "local-9", exportMoveId: "exp-1" });
			expect(again.exportSessionMove).toHaveBeenCalledWith("org-1", "cloud-1", undefined);
			expect(again.getSessionMoveExport).toHaveBeenCalledWith("org-1", "cloud-1", "exp-1");
			expect(again.restartSessionAgent).not.toHaveBeenCalled();
			expect(again.deleteSession).toHaveBeenCalledWith("org-1", "cloud-1", { moveId: "exp-1" });
		});

		it("finishes under the same move when Cloud answers a second export with the move that holds the session", async () => {
			// The export answered, but this computer did not answer the import.
			const held = { ...cloudExport, moveId: "exp-1" };
			const first = cloudFake({ exportSessionMove: async () => held });
			await expect(moveSessionHere(input, { daemon: fakeDaemon({}, []) as unknown as MoveHereDeps["daemon"], cloud: first }))
				.rejects.toMatchObject({ step: "import", rolledBack: false, followUp: "settle" });
			// Try again asks for an export again; Cloud answers with the same
			// move, and the import here returns the session it started.
			const again = cloudFake({ exportSessionMove: async () => held });
			const local = daemon(ok({ session: { id: "local-9" } }));
			const result = await moveSessionHere(input, { daemon: local, cloud: again });
			expect(result).toMatchObject({ sessionId: "local-9", exportMoveId: "exp-1" });
			expect((local.POST as ReturnType<typeof vi.fn>).mock.calls[0][1].body).toMatchObject({ moveId: "m-1" });
			expect(again.deleteSession).toHaveBeenCalledWith("org-1", "cloud-1", { moveId: "exp-1" });
		});
	});

	it.each(["MOVE_TURNS_QUEUED", "INTERFACE_TRANSITION_IN_PROGRESS"])("leaves the Cloud session as it was, without a restart, when Cloud refuses the export with %s", async (code) => {
		const cp = cloudFake({ exportSessionMove: async () => { throw new CloudCpError("refused (CP text)", { status: 409, code }); } });
		const calls: string[] = [];
		const error = await moveSessionHere(input, { daemon: daemon(ok(), calls), cloud: cp }).catch((e: unknown) => e);
		expect(error).toBeInstanceOf(MoveError);
		expect(error).toMatchObject({ step: "export", code, rolledBack: true, followUp: undefined });
		expect(moveLeftSessionAsItWas(error as MoveError)).toBe(true);
		// Nothing stopped, so nothing starts again; a restart could cut into a switch.
		expect(cp.restartSessionAgent).not.toHaveBeenCalled();
		expect(calls).toEqual([]);
		expect(cp.deleteSession).not.toHaveBeenCalled();
	});

	it("keeps the Cloud session when its export leaves out the conversation its preview listed", async () => {
		const cp = cloudFake({ exportSessionMove: async () => ({ ...cloudExport, transcript: undefined }) });
		const result = await moveSessionHere({ ...input, expectTranscript: true }, { daemon: daemon(ok({ session: { id: "local-9" } })), cloud: cp });
		expect(result).toMatchObject({ sessionId: "local-9", conversation: "note", keptInCloud: true });
		expect(cp.deleteSession).not.toHaveBeenCalled();
	});

	it("keeps the Cloud session when Cloud will not delete it for an unsaved conversation", async () => {
		const cp = cloudFake({ deleteSession: async () => { throw new CloudCpError("not saved yet", { status: 409, code: TRANSCRIPT_NOT_CAPTURED }); } });
		const result = await moveSessionHere(input, { daemon: daemon(ok({ session: { id: "local-9" } })), cloud: cp });
		expect(result).toMatchObject({ sessionId: "local-9", keptInCloud: true });
		expect(result.deleteError).toBeUndefined();
	});

	it("starts the Cloud agent again and imports nothing when Cloud could not save the conversation", async () => {
		const cp = cloudFake({ exportSessionMove: async () => { throw new CloudCpError("Try again.", { status: 409, code: TRANSCRIPT_NOT_CAPTURED }); } });
		const calls: string[] = [];
		const error = await moveSessionHere(input, { daemon: daemon(ok(), calls), cloud: cp }).catch((e: unknown) => e);
		expect(error).toMatchObject({ step: "export", code: TRANSCRIPT_NOT_CAPTURED, rolledBack: true });
		expect(calls).toEqual([]);
		expect(cp.deleteSession).not.toHaveBeenCalled();
	});

	it("hands the project over before the import, so a relaunch returns the same session", async () => {
		const order: string[] = [];
		const local = fakeDaemon({ "/api/v1/sessions/move/import": ok({ session: { id: "local-9" } }) }, order) as unknown as MoveHereDeps["daemon"];
		await moveSessionHere(input, { daemon: local, cloud: cloudFake(), onImport: (projectId) => order.push(`saved ${projectId}`) });
		expect(order).toEqual(["saved proj-1", "/api/v1/sessions/move/import"]);
	});

	it("deletes only the copy its export left, and keeps a Cloud session used again since", async () => {
		const exportedWithId = { ...cloudExport, moveId: "exp-1" };
		const cp = cloudFake({ exportSessionMove: async () => exportedWithId });
		await moveSessionHere(input, { daemon: daemon(ok({ session: { id: "local-9" } })), cloud: cp });
		expect(cp.deleteSession).toHaveBeenCalledWith("org-1", "cloud-1", { moveId: "exp-1" });

		const inUse = cloudFake({
			exportSessionMove: async () => exportedWithId,
			deleteSession: async () => { throw new CloudCpError("The session was used after the move.", { status: 409, code: "MOVE_COPY_IN_USE" }); },
		});
		const result = await moveSessionHere(input, { daemon: daemon(ok({ session: { id: "local-9" } })), cloud: inUse });
		expect(result).toMatchObject({ keptInCloud: true, copyInUse: true, exportMoveId: "exp-1" });
		expect(result.deleteError).toBeUndefined();
	});

	it("deletes the Cloud session when only a pull request or setting did not carry over", async () => {
		const cp = cloudFake();
		const result = await moveSessionHere(input, { daemon: daemon(ok({ session: { id: "local-9" }, warnings: ["Could not claim https://github.com/acme/app/pull/1: gone"] })), cloud: cp });
		expect(result.keptInCloud).toBeUndefined();
		expect(cp.deleteSession).toHaveBeenCalledWith("org-1", "cloud-1");
	});

	describe("when no project here has the repository", () => {
		const clone = { remoteUrl: "https://github.com/acme/app", destinationParent: "~/ao/projects", repoKey: "github.com/acme/app" };
		const { projectId: _unused, ...rest } = input;
		const cloneInput = { ...rest, clone };
		const exists = { error: { code: "CLONE_DESTINATION_EXISTS", message: "exists", details: { path: "/Users/me/ao/projects/app" } } };
		const imported = ok({ session: { id: "local-9" } });

		it("clones it before anything in Cloud stops, then moves into the new project", async () => {
			const cp = cloudFake();
			const calls: string[] = [];
			const local = fakeDaemon({ "/api/v1/projects/clone": ok({ project: { id: "app" } }), "/api/v1/sessions/move/import": imported }, calls) as unknown as MoveHereDeps["daemon"];
			const steps: string[] = [];
			const onCloned = vi.fn();
			const result = await moveSessionHere(cloneInput, { daemon: local, cloud: cp, onStep: (step) => steps.push(step), onCloned });
			expect(result).toMatchObject({ projectId: "app", sessionId: "local-9" });
			expect(steps).toEqual(["clone", "stop", "import", "complete"]);
			expect(onCloned).toHaveBeenCalledWith("app");
			expect((local.POST as ReturnType<typeof vi.fn>).mock.calls[0][1].body).toEqual({ remoteUrl: clone.remoteUrl, destinationParent: "~/ao/projects" });
			expect((local.POST as ReturnType<typeof vi.fn>).mock.calls[1][1].body.projectId).toBe("app");
		});

		it("uses a checkout of the same repository already where the clone would go", async () => {
			const calls: string[] = [];
			const local = fakeDaemon({
				"/api/v1/projects/clone": exists,
				"/api/v1/projects": ok({ project: { id: "app-2", repo: "git@github.com:Acme/App.git" } }),
				"/api/v1/sessions/move/import": imported,
			}, calls) as unknown as MoveHereDeps["daemon"];
			const result = await moveSessionHere(cloneInput, { daemon: local, cloud: cloudFake() });
			expect(result.projectId).toBe("app-2");
			expect((local.POST as ReturnType<typeof vi.fn>).mock.calls[1][1].body).toEqual({ path: "/Users/me/ao/projects/app" });
		});

		it("removes and refuses a folder there that holds another repository, leaving Cloud as it was", async () => {
			const cp = cloudFake();
			const calls: string[] = [];
			const local = fakeDaemon({
				"/api/v1/projects/clone": exists,
				"/api/v1/projects": ok({ project: { id: "app-2", repo: "https://github.com/acme/other" } }),
				"/api/v1/projects/{id}": ok(),
			}, calls) as unknown as MoveHereDeps["daemon"];
			const error = await moveSessionHere(cloneInput, { daemon: local, cloud: cp }).catch((e: unknown) => e);
			expect(error).toMatchObject({ step: "clone", rolledBack: true, message: "/Users/me/ao/projects/app holds another repository. Move or rename it, then try again." });
			expect(calls).toEqual(["/api/v1/projects/clone", "/api/v1/projects", "/api/v1/projects/{id}"]);
			expect(cp.exportSessionMove).not.toHaveBeenCalled();
		});

		it("refuses a folder there that is already a project for another repository", async () => {
			const cp = cloudFake();
			const local = fakeDaemon({
				"/api/v1/projects/clone": exists,
				"/api/v1/projects": fail("PATH_ALREADY_REGISTERED", "A project at this path is already registered"),
			}, []) as unknown as MoveHereDeps["daemon"];
			const error = await moveSessionHere(cloneInput, { daemon: local, cloud: cp }).catch((e: unknown) => e);
			expect(error).toMatchObject({ step: "clone", message: "/Users/me/ao/projects/app holds another repository. Move or rename it, then try again." });
			expect(cp.exportSessionMove).not.toHaveBeenCalled();
		});

		it("leaves Cloud as it was when the clone fails", async () => {
			const cp = cloudFake();
			const local = fakeDaemon({ "/api/v1/projects/clone": fail("CLONE_EMPTY_REPOSITORY", "Orchestrator needs a repository with at least one commit.") }, []) as unknown as MoveHereDeps["daemon"];
			const error = await moveSessionHere(cloneInput, { daemon: local, cloud: cp }).catch((e: unknown) => e);
			expect(error).toMatchObject({ step: "clone", code: "CLONE_EMPTY_REPOSITORY", message: "Orchestrator needs a repository with at least one commit." });
			expect(cp.exportSessionMove).not.toHaveBeenCalled();
			expect(cp.restartSessionAgent).not.toHaveBeenCalled();
		});
	});

	it("treats a Cloud session that is already gone as deleted", async () => {
		const cp = cloudFake({ deleteSession: async () => { throw new CloudCpError("not found", { status: 404 }); } });
		const result = await moveSessionHere(input, { daemon: daemon(ok({ session: { id: "local-9" } })), cloud: cp });
		expect(result.deleteError).toBeUndefined();
	});
});

describe("moveSession when this computer's Orchestrator does not answer", () => {
	const unknown = /did not answer, so the outcome is unknown\. Check the session before trying again\./;

	it.each([
		["stopping the agent", {}, "stop"],
		["the push", { "/api/v1/sessions/{sessionId}/exit-agent": ok() }, "export"],
	] as const)("claims nothing was undone when %s gets no answer", async (_, replies, step) => {
		const calls: string[] = [];
		const cp = cloud();
		const error = await moveSession({ sessionId: "app-1", moveId: "m-1", target }, { source: fakeDaemon(replies, calls), cloud: cp }).catch((e: unknown) => e);
		expect(error).toBeInstanceOf(MoveError);
		expect(error).toMatchObject({ step, rolledBack: false, followUp: "unknown" });
		expect((error as MoveError).message).toMatch(unknown);
		// Nothing resumes the agent: the push may have happened.
		expect(calls).not.toContain("/api/v1/sessions/{sessionId}/resume-agent");
		expect(cp.createSession).not.toHaveBeenCalled();
	});

	it("does not say the agent was resumed when resuming it gets no answer", async () => {
		const calls: string[] = [];
		const source = fakeDaemon({
			"/api/v1/sessions/{sessionId}/exit-agent": ok(),
			"/api/v1/sessions/{sessionId}/move/export": fail("MOVE_REMOTE_AHEAD", "Origin's branch has commits this machine does not."),
		}, calls);
		const error = await moveSession({ sessionId: "app-1", moveId: "m-1", target }, { source, cloud: cloud() }).catch((e: unknown) => e);
		expect(calls).toContain("/api/v1/sessions/{sessionId}/resume-agent");
		expect(error).toMatchObject({ step: "export", code: "MOVE_REMOTE_AHEAD", rolledBack: false, followUp: "unknown" });
		expect((error as MoveError).message).toMatch(/^Origin's branch has commits this machine does not\. .*outcome is unknown/);
	});
});

describe("moveSession before pushing", () => {
	it("refuses to create a Cloud project on an unknown default branch, before stopping the agent", async () => {
		for (const defaultBranch of ["auto", "", " "]) {
			const calls: string[] = [];
			const cp = cloud();
			const error = await moveSession(
				{ sessionId: "app-1", moveId: "m-1", target: { ...target, projectId: "" }, createProject: { displayName: "app", repositoryUrl: "https://github.com/acme/app", defaultBranch } },
				{ source: fakeDaemon({}, calls), cloud: cp },
			).catch((e: unknown) => e);
			expect(error).toMatchObject({ step: "project", rolledBack: true });
			expect(cp.createProject).not.toHaveBeenCalled();
			expect(calls).toEqual([]);
		}
	});

	it("sends Cloud only a reasoning effort it accepts", async () => {
		for (const [local, sent] of [["xhigh", "xhigh"], ["off", "none"], ["HIGH", "high"], ["turbo", undefined], ["", undefined]] as const) {
			const source = fakeDaemon({
				"/api/v1/sessions/{sessionId}/exit-agent": ok(),
				"/api/v1/sessions/{sessionId}/move/export": ok({ ...exported, manifest: { ...exported.manifest, effort: local } }),
				"/api/v1/sessions/{sessionId}/move/complete": ok(),
			}, []);
			const cp = cloud();
			await moveSession({ sessionId: "app-1", moveId: "m-1", target }, { source, cloud: cp });
			expect(cp.createSession.mock.calls[0][1].reasoningEffort).toBe(sent);
		}
		expect(cloudReasoningEffort(undefined)).toBeUndefined();
	});
});
