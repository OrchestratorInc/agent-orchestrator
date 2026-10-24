import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import NativeAbortController from "abort-controller";
import { CloudApiError, CloudRequestTimeoutError } from "@aoagents/cloud-client";
const storage = vi.hoisted(() => ({ data: new Map<string, string>(), setItem: vi.fn(), removeItem: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => storage.data.get(key) ?? null,
	setItem: storage.setItem, removeItem: storage.removeItem,
} }));
beforeEach(() => {
	storage.data.clear();
	storage.setItem.mockReset().mockImplementation(async (key: string, value: string) => { storage.data.set(key, value); });
	storage.removeItem.mockReset().mockImplementation(async (key: string) => { storage.data.delete(key); });
});
import { createCloudSessionSource as createSource } from "./source";
import { cloudConversationReplayCache } from "./conversationReplay";
import { acceptCloudOperation, clearCloudOperation, prepareCloudOperation, readCloudOperation, reserveCloudOperation } from "./operation";
beforeEach(() => cloudConversationReplayCache.clear());
function createCloudSessionSource(input: { client: never; orgId: string; scopeKey?: string; isCurrent?: () => boolean }) {
	return createSource({ scopeKey: "test-scope", isCurrent: () => true, ...input });
}

function fullSession(id: string) {
	return {
		id,
		orgId: "o1",
		projectId: "p1",
		kind: "worker",
		harness: "claude-code",
		displayName: `session ${id}`,
		branch: "main",
		mode: "trusted",
		deniedCommands: [],
		activityState: "idle",
		status: "building",
		runtimeConnected: true,
		isTerminated: false,
		createdAt: "2026-09-01T00:00:00Z",
		updatedAt: "2026-09-01T00:00:00Z",
	};
}

function clientStub<T extends Record<string, unknown> = Record<string, never>>(overrides: T = {} as T) {
	return {
		listProjects: vi.fn(async () => ({ items: [], page: { hasMore: false } })),
		listSessions: vi.fn(async () => ({ items: [], page: { hasMore: false } })),
		createSession: vi.fn(async (_orgId: string, _input: import("@aoagents/cloud-client").CreateSessionInput, _options: { idempotencyKey: string }) => ({ session: { id: "s-new" } })),
		getProject: vi.fn(async () => ({ project: { config: {} } })),
		deleteSession: vi.fn(async () => ({ session: { id: "x", desiredState: "deleted" } })),
		...overrides,
	};
}

describe("Cloud Chat with React Native cancellation signals", () => {
	beforeEach(() => vi.stubGlobal("AbortController", NativeAbortController));
	afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });
	const events = [
		{ sessionId: "s1", sequence: 1, type: "chat.user_message", createdAt: "now", payload: { text: "hi" } },
		{ sessionId: "s1", sequence: 2, type: "chat.assistant_delta", createdAt: "now", payload: { text: "hello" } },
	];
	it("loads user and assistant messages without the optional native throwIfAborted method", async () => {
		const controller = new AbortController();
		expect(controller.signal.throwIfAborted).toBeUndefined();
		const source = createCloudSessionSource({ client: clientStub({
			getSession: vi.fn(async () => ({ session: { ...fullSession("s1"), interfaceMode: "chat" } })),
			replayEvents: vi.fn(async () => ({ events, nextAfter: 2, hasMore: false })),
		}) as never, orgId: "o1" });
		const page = await source.getConversationPage("s1", undefined, { signal: controller.signal });
		expect(page.items).toMatchObject([{ role: "user", text: "hi" }, { role: "assistant", text: "hello" }]);
		expect(page.latestSequence).toBe(2);
	});
	it("polls newly arrived replies with native signals", async () => {
		vi.useFakeTimers();
		const source = createCloudSessionSource({ client: clientStub({
			replayEvents: vi.fn().mockResolvedValueOnce({ events: [events[0]], nextAfter: 1, hasMore: false })
				.mockResolvedValue({ events: [events[1]], nextAfter: 2, hasMore: false }),
		}) as never, orgId: "o1" });
		const listener = vi.fn(); const onError = vi.fn();
		const unsubscribe = source.subscribeEvents("s1", listener, { onError });
		try {
			await vi.advanceTimersByTimeAsync(2_000);
			expect(listener.mock.calls.map(([event]) => event.payload.text)).toEqual(["hi", "hello"]);
			expect(onError).not.toHaveBeenCalled();
		} finally { unsubscribe(); }
	});
	it("can retry loading after a cancelled native replay without adopting the cancelled history", async () => {
		let finishReplay!: (value: unknown) => void;
		const source = createCloudSessionSource({ client: clientStub({
			getSession: vi.fn(async () => ({ session: fullSession("s1") })),
			replayEvents: vi.fn().mockImplementationOnce(() => new Promise((resolve) => { finishReplay = resolve; }))
				.mockResolvedValue({ events, nextAfter: 2, hasMore: false }),
		}) as never, orgId: "o1" });
		const controller = new AbortController();
		const page = source.getConversationPage("s1", undefined, { signal: controller.signal });
		const cancelled = expect(page).rejects.toMatchObject({ name: "AbortError" });
		controller.abort(); await cancelled;
		finishReplay({ events, nextAfter: 2, hasMore: false });
		expect(cloudConversationReplayCache.get("test-scope", "s1")).toBeUndefined();
		expect((await source.getConversationPage("s1")).items).toHaveLength(2);
	});
	it("switches to Chat through polling with a native caller signal", async () => {
		vi.useFakeTimers();
		const source = createCloudSessionSource({ client: clientStub({
			getSession: vi.fn().mockResolvedValueOnce({ session: { ...fullSession("s1"), interfaceMode: "tui" } })
				.mockResolvedValueOnce({ session: { ...fullSession("s1"), interfaceMode: "tui" } })
				.mockResolvedValue({ session: { ...fullSession("s1"), interfaceMode: "chat" } }),
			getSessionInterfaceTransition: vi.fn().mockResolvedValueOnce({ supported: true, targetMode: "chat" })
				.mockResolvedValueOnce({ supported: true, targetMode: "chat" })
				.mockResolvedValueOnce({ supported: true, targetMode: "chat", transition: { phase: "target_starting", sourceMode: "tui", targetMode: "chat" } })
				.mockResolvedValue({ supported: true, targetMode: "tui", transition: { phase: "completed", sourceMode: "tui", targetMode: "chat" } }),
			startSessionInterfaceTransition: vi.fn(async () => ({ transition: { phase: "requested", sourceMode: "tui", targetMode: "chat" } })),
		}) as never, orgId: "o1" });
		const switching = source.switchToChat!("s1", { signal: new AbortController().signal }).then(() => "completed", (error: unknown) => error);
		await vi.advanceTimersByTimeAsync(2_000);
		expect(await switching).toBe("completed");
	});
	it("rejects an already cancelled native interface read before accessing Cloud", async () => {
		const getSession = vi.fn(); const getSessionInterfaceTransition = vi.fn();
		const source = createCloudSessionSource({ client: clientStub({ getSession, getSessionInterfaceTransition }) as never, orgId: "o1" });
		const controller = new AbortController(); controller.abort();
		await expect(source.getInterfaceTransition!("s1", { signal: controller.signal })).rejects.toMatchObject({ name: "AbortError" });
		expect(getSession).not.toHaveBeenCalled(); expect(getSessionInterfaceTransition).not.toHaveBeenCalled();
	});
});

describe("createCloudSessionSource", () => {
	it("binds PR and browser capabilities to the same account and rejects retired owners", async () => {
		const listSessionPullRequests = vi.fn(async () => ({ pullRequests: [] }));
		let current = true;
		const source = createCloudSessionSource({ client: clientStub({ listSessionPullRequests }) as never, orgId: "o1", scopeKey: "account-parity", isCurrent: () => current });
		expect(source.pullRequests?.scopeKey).toBe("account-parity");
		expect(source.preview?.scopeKey).toBe("account-parity");
		await expect(source.pullRequests!.list("s1")).resolves.toEqual([]);
		expect(listSessionPullRequests).toHaveBeenCalledWith("o1", "s1", undefined);
		current = false;
		expect(source.preview!.isCurrent()).toBe(false);
		await expect(source.pullRequests!.list("s1")).rejects.toThrow("Cloud account changed");
		expect(listSessionPullRequests).toHaveBeenCalledTimes(1);
	});
	it("exposes Files through the same account-scoped Cloud owner", async () => {
		const getWorkspaceReview = vi.fn(async () => ({ workspaceVersion: "v1", files: [], sections: { committed: [], staged: [], unstaged: [], untracked: [] }, summary: { files: 0, additions: 0, deletions: 0 }, truncated: false }));
		let current = true;
		const source = createCloudSessionSource({ client: clientStub({ getWorkspaceReview }) as never, orgId: "o1", scopeKey: "account-files", isCurrent: () => current });
		expect(source.workspace?.scopeKey).toBe("account-files");
		expect(await source.workspace!.getManifest("s1")).toMatchObject({ sessionId: "s1", workspaceVersion: "v1" });
		current = false;
		await expect(source.workspace!.getManifest("s1")).rejects.toThrow(/account changed/i);
		expect(getWorkspaceReview).toHaveBeenCalledOnce();
	});
	it.each([
		{ prompt: undefined, harness: "codex", title: "codex" },
		{ prompt: "", harness: "claude-code", title: "claude-code" },
		{ prompt: " \n\t ", harness: "opencode", title: "opencode" },
		{ prompt: undefined, harness: undefined, title: "claude-code" },
		{ prompt: "  Inspect the repository\n", harness: "codex", title: "Inspect the repository" },
		{ prompt: "a".repeat(110), harness: "codex", title: "a".repeat(100) },
	])("names a Cloud worker like desktop: $title", async ({ prompt, harness, title }) => {
		const client = clientStub();
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		await source.createSession({ projectId: "p1", prompt, harness, clientRequestId: "title-one" });
		expect(client.createSession).toHaveBeenCalledWith("o1", expect.objectContaining({
			displayName: title, harness: harness ?? "claude-code", prompt: prompt ?? "",
		}), { idempotencyKey: "title-one" });
	});
	it("preserves an already reserved legacy title when retrying instead of changing its idempotent payload", async () => {
		const payload = { projectId: "p1", kind: "worker" as const, harness: "codex", displayName: "New session", prompt: "", mode: "trusted" as const, deniedCommands: [] };
		await reserveCloudOperation("worker-create/p1", prepareCloudOperation(undefined, "test-scope", payload, () => "legacy-one"));
		const client = clientStub();
		await createCloudSessionSource({ client: client as never, orgId: "o1" }).createSession({ projectId: "p1", harness: "codex", clientRequestId: "legacy-one" });
		expect(client.createSession).toHaveBeenCalledWith("o1", payload, { idempotencyKey: "legacy-one" });
	});
	it("acknowledges the original recovery notice without unlocking either controller", async () => {
		const acknowledge = vi.fn(async () => ({ ok: true }));
		const transition = { id: "h1", sourceMode: "chat", targetMode: "tui", phase: "recovery_required", errorDetail: "Recovery needed" };
		const getSessionInterfaceTransition = vi.fn().mockResolvedValueOnce({ supported: true, targetMode: "tui", transition }).mockResolvedValue({ supported: true, targetMode: "chat", transition: { ...transition, noticeAcknowledgedAt: "acknowledged" } });
		const source = createCloudSessionSource({ client: clientStub({ getSession: vi.fn(async () => ({ session: { ...fullSession("s1"), interfaceMode: "tui" } })), getSessionInterfaceTransition, acknowledgeSessionInterfaceTransitionNotice: acknowledge }) as never, orgId: "o1" });
		await source.getInterfaceTransition!("s1");
		await source.acknowledgeInterfaceNotice!("s1", "h1");
		const status = await source.getInterfaceTransition!("s1");
		expect(status.transition?.phase).toBe("recovery_required"); expect(status.transition?.noticeAcknowledgedAt).toEqual(expect.any(String));
		expect(acknowledge).toHaveBeenCalledWith("o1", "s1", "h1", undefined);
	});
	it("refuses an opposite-target active switch and never cancels after source stopping", async () => {
		const start = vi.fn(); const cancel = vi.fn();
		const client = clientStub({ getSession: vi.fn(async () => ({ session: { ...fullSession("s1"), interfaceMode: "chat" } })), getSessionInterfaceTransition: vi.fn(async () => ({ supported: true, targetMode: "tui", transition: { id: "h1", targetMode: "tui", phase: "source_stopping" } })), startSessionInterfaceTransition: start, cancelSessionInterfaceTransition: cancel });
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		await expect(source.startInterfaceTransition!("s1", { targetMode: "chat", policy: "interrupt" })).rejects.toThrow(/other interface/i);
		await expect(source.cancelInterfaceTransition!("s1")).rejects.toThrow(/can no longer be cancelled/i);
		expect(start).not.toHaveBeenCalled(); expect(cancel).not.toHaveBeenCalled();
	});
	it("never sends an approval or cancellation from a retired account source", async () => {
		const decideChatApproval = vi.fn(); const cancelTurn = vi.fn();
		const source = createCloudSessionSource({ client: clientStub({ decideChatApproval, cancelTurn }) as never, orgId: "o1", isCurrent: () => false });
		await expect(source.decideApproval!("s1", "r1", "accept")).rejects.toThrow(/account changed/i);
		await expect(source.cancelTurn("s1", "t1")).rejects.toThrow(/account changed/i);
		expect(decideChatApproval).not.toHaveBeenCalled(); expect(cancelTurn).not.toHaveBeenCalled();
	});
	it("does not finish a Chat handoff on an early mode commit followed by recovery-required", async () => {
		const getSession = vi.fn().mockResolvedValueOnce({ session: { ...fullSession("s1"), interfaceMode: "tui" } }).mockResolvedValue({ session: { ...fullSession("s1"), interfaceMode: "chat" } });
		const getSessionInterfaceTransition = vi.fn().mockResolvedValueOnce({ supported: true, targetMode: "chat" }).mockResolvedValue({ supported: true, targetMode: "tui", transition: { phase: "recovery_required", sourceMode: "tui", targetMode: "chat", errorDetail: "Target failed after the terminal stopped" } });
		const source = createCloudSessionSource({ client: clientStub({ getSession, getSessionInterfaceTransition, startSessionInterfaceTransition: vi.fn(async () => ({ transition: { phase: "requested", sourceMode: "tui", targetMode: "chat" } })) }) as never, orgId: "o1" });
		await expect(source.switchToChat!("s1")).rejects.toThrow("Target failed after the terminal stopped");
	});
	it("refreshes after the adopted tail and keeps history across source remount", async () => {
		const event = { sessionId: "s1", sequence: 1, type: "chat.user_message", createdAt: "now", payload: { text: "one" } };
		const client = clientStub({ getSession: vi.fn(async () => ({ session: fullSession("s1") })), replayEvents: vi.fn().mockResolvedValueOnce({ events: [event], nextAfter: 1, hasMore: false }).mockResolvedValue({ events: [{ ...event, sequence: 2, payload: { text: "two" } }], nextAfter: 2, hasMore: false }) });
		await createCloudSessionSource({ client: client as never, orgId: "o1" }).getConversationPage("s1");
		const page = await createCloudSessionSource({ client: client as never, orgId: "o1" }).getConversationPage("s1");
		expect(client.replayEvents.mock.calls[1]?.[2]).toMatchObject({ after: 1 });
		expect(page.items).toHaveLength(2);
	});
	it("shares initial replay with a subscription and never adopts after account change", async () => {
		let resolve!: (value: unknown) => void; let current = true;
		const client = clientStub({ getSession: vi.fn(async () => ({ session: fullSession("s1") })), replayEvents: vi.fn(() => new Promise((done) => { resolve = done; })) });
		const source = createCloudSessionSource({ client: client as never, orgId: "o1", isCurrent: () => current });
		const page = source.getConversationPage("s1");
		const outcome = expect(page).rejects.toThrow(/account changed/i);
		const unsubscribe = source.subscribeEvents("s1", vi.fn());
		await Promise.resolve(); await Promise.resolve();
		expect(client.replayEvents).toHaveBeenCalledOnce(); current = false;
		resolve({ events: [{ sessionId: "s1", sequence: 1, type: "chat.user_message", createdAt: "now", payload: { text: "private" } }], nextAfter: 1, hasMore: false });
		await outcome; unsubscribe();
		expect(cloudConversationReplayCache.get("test-scope", "s1")).toBeUndefined();
	});
	it("adopts the durable session model and keeps supported overrides across remount", async () => {
		const client = clientStub({ getSession: vi.fn(async () => ({ session: { ...fullSession("s1"), interfaceMode: "chat", model: "durable" } })), replayEvents: vi.fn(async () => ({ events: [], nextAfter: 0, hasMore: false })) });
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		expect((await source.getConversationPage("s1")).settings).toEqual({ model: "durable" });
		await source.setTurnSettings!("s1", { model: "picked", reasoningEffort: "high" });
		expect((await createCloudSessionSource({ client: client as never, orgId: "o1" }).getConversationPage("s1")).settings).toEqual({ model: "picked", reasoningEffort: "high" });
	});
	it("uses persisted Cloud turn choices even when a remounted source sends before reading its page", async () => {
		const sendMessage = vi.fn();
		const client = clientStub({ sendMessage });
		await createCloudSessionSource({ client: client as never, orgId: "o1" }).setTurnSettings!("s1", { model: "picked", reasoningEffort: "high" });
		await createCloudSessionSource({ client: client as never, orgId: "o1" }).sendMessage("s1", { text: "Inspect", clientMessageId: "one" });
		expect(sendMessage).toHaveBeenCalledWith("o1", "s1", { text: "Inspect", model: "picked", reasoningEffort: "high" }, { idempotencyKey: "one" });
	});
	it("steers only the captured active turn instead of posting another user message", async () => {
		const steerTurn = vi.fn(async () => ({ event: { sessionId: "s1", sequence: 2, type: "chat.turn_steer_requested", payload: { turnId: "t1", clientMessageId: "steer-one", text: "Guidance" }, createdAt: "now" } })); const sendMessage = vi.fn();
		const current = { ...fullSession("s1"), interfaceMode: "chat", activeTurn: { id: "t1", state: "running", attemptCount: 1 } };
		const source = createCloudSessionSource({ client: clientStub({ getSession: vi.fn(async () => ({ session: current })), replayEvents: vi.fn(async (_org: string, _session: string, options: { after?: number } = {}) => options.after === 2
			? { events: [{ sessionId: "s1", sequence: 3, type: "chat.turn_steered", payload: { turnId: "t1", clientMessageId: "steer-one", text: "Guidance" }, createdAt: "now" }], nextAfter: 3, hasMore: false }
			: { events: [{ sessionId: "s1", sequence: 1, type: "chat.turn_capabilities", payload: { turnId: "t1", attempt: 1, steering: true }, createdAt: "now" }], nextAfter: 1, hasMore: false }), steerTurn, sendMessage }) as never, orgId: "o1" });
		await source.setTurnSettings!("s1", { reasoningEffort: "default" });
		await source.steerMessage!("s1", { text: "Guidance", clientMessageId: "steer-one" }, "t1");
		expect((await source.getConversationPage("s1")).settings.reasoningEffort).toBe("default");
		expect(steerTurn).toHaveBeenCalledWith("o1", "s1", "t1", "Guidance", { idempotencyKey: "steer-one", signal: expect.anything() });
		await expect(source.steerMessage!("s1", { text: "Guidance", clientMessageId: "steer-two" }, "older")).rejects.toThrow(/turn finished/i);
		expect(sendMessage).not.toHaveBeenCalled();
	});
	it("restores an uncertain spawn with its frozen wire payload and records acceptance before returning", async () => {
		const client = clientStub({ createSession: vi.fn().mockRejectedValueOnce(new Error("Response lost")).mockResolvedValue({ session: { id: "accepted" } }) });
		const options = { projectId: "p1", harness: "codex", clientRequestId: "one" };
		vi.mocked(client.getProject).mockResolvedValue({ project: { config: { worker: { agent: "codex", agentConfig: { model: "first" } } } } } as never);
		await expect(createCloudSessionSource({ client: client as never, orgId: "o1" }).createSession(options)).rejects.toThrow("Response lost");
		vi.mocked(client.getProject).mockResolvedValue({ project: { config: { worker: { agent: "codex", agentConfig: { model: "changed" } } } } } as never);
		const restored = createCloudSessionSource({ client: client as never, orgId: "o1" });
		expect(await restored.createSession(options)).toEqual({ id: "accepted" });
		expect(client.createSession.mock.calls[0]).toEqual(client.createSession.mock.calls[1]);
		expect(await createCloudSessionSource({ client: client as never, orgId: "o1" }).createSession(options)).toEqual({ id: "accepted" });
		expect(client.createSession).toHaveBeenCalledTimes(2);
	});
	it("forwards canonical worker project effort when no explicit override is selected", async () => {
		const client = clientStub({ getProject: vi.fn(async () => ({ project: { config: { worker: { agent: "codex", agentConfig: { model: "project-model", effort: "high" } } } } })) });
		await createCloudSessionSource({ client: client as never, orgId: "o1" }).createSession({ projectId: "p1", harness: "codex", clientRequestId: "canonical" });
		expect(client.createSession).toHaveBeenCalledWith("o1", expect.objectContaining({ model: "project-model", reasoningEffort: "high" }), { idempotencyKey: "canonical" });
	});
	it("releases a confirmed quota rejection so an edited task can use a new identity", async () => {
		const error = new CloudApiError(409, { error: "conflict", code: "SANDBOX_QUOTA_EXCEEDED", message: "Concurrent session limit reached.", requestId: "quota-request" });
		const client = clientStub({ createSession: vi.fn().mockRejectedValueOnce(error).mockResolvedValue({ session: { id: "edited-task" } }) });
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		await expect(source.createSession({ projectId: "p1", prompt: "Original task", model: "first", clientRequestId: "rejected" })).rejects.toBe(error);
		expect(await readCloudOperation("test-scope", "worker-create/p1")).toBeNull();
		expect(await source.createSession({ projectId: "p1", prompt: "Edited task", model: "second", clientRequestId: "fresh" })).toEqual({ id: "edited-task" });
		expect(client.createSession.mock.calls[1]).toEqual(["o1", expect.objectContaining({ prompt: "Edited task", model: "second" }), { idempotencyKey: "fresh" }]);
	});
	it("releases an uncertain request after its unchanged retry receives a confirmed quota rejection", async () => {
		const error = new CloudApiError(409, { error: "conflict", code: "SANDBOX_QUOTA_EXCEEDED", message: "Concurrent session limit reached.", requestId: "retry-quota" });
		const client = clientStub({ createSession: vi.fn().mockRejectedValueOnce(new TypeError("Response lost")).mockRejectedValueOnce(error) });
		const options = { projectId: "p1", prompt: "Original task", model: "original", clientRequestId: "uncertain" };
		await expect(createCloudSessionSource({ client: client as never, orgId: "o1" }).createSession(options)).rejects.toThrow("Response lost");
		await expect(createCloudSessionSource({ client: client as never, orgId: "o1" }).createSession({ ...options, prompt: "Edited task", model: "changed" })).rejects.toBe(error);
		expect(client.createSession.mock.calls[1]).toEqual(client.createSession.mock.calls[0]);
		expect(await readCloudOperation("test-scope", "worker-create/p1")).toBeNull();
	});
	it("releases an authoritative unsupported-interface rejection so Terminal can use a fresh identity", async () => {
		const error = new CloudApiError(409, { error: "conflict", code: "SESSION_MODE_UNSUPPORTED", message: "This harness cannot run in Chat.", requestId: "unsupported" });
		const client = clientStub({ createSession: vi.fn().mockRejectedValueOnce(error).mockResolvedValue({ session: { id: "terminal-task" } }) });
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		await expect(source.createSession({ projectId: "p1", harness: "codex", mode: "chat", prompt: "Inspect", clientRequestId: "chat-request" })).rejects.toBe(error);
		expect(await readCloudOperation("test-scope", "worker-create/p1")).toBeNull();
		expect(await source.createSession({ projectId: "p1", harness: "codex", mode: "tui", prompt: "Inspect", clientRequestId: "terminal-request" })).toEqual({ id: "terminal-task" });
		expect(client.createSession.mock.calls[1]).toEqual(["o1", expect.objectContaining({ interfaceMode: "tui", prompt: "Inspect" }), { idempotencyKey: "terminal-request" }]);
	});
	it("preserves an uncertain Chat request when a later capability rejection happens before idempotency replay", async () => {
		const error = new CloudApiError(409, { error: "conflict", code: "SESSION_MODE_UNSUPPORTED", message: "Chat is no longer available.", requestId: "capability-changed" });
		const client = clientStub({ createSession: vi.fn().mockRejectedValueOnce(new TypeError("Response lost")).mockRejectedValueOnce(error) });
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		const input = { projectId: "p1", harness: "codex", mode: "chat" as const, prompt: "Original task", clientRequestId: "uncertain-chat" };
		await expect(source.createSession(input)).rejects.toThrow("Response lost");
		await expect(source.createSession({ ...input, prompt: "Edited task" })).rejects.toBe(error);
		expect(client.createSession.mock.calls[1]).toEqual(client.createSession.mock.calls[0]);
		expect(await readCloudOperation("test-scope", "worker-create/p1")).toMatchObject({ key: "uncertain-chat", payload: { prompt: "Original task", interfaceMode: "chat" } });
		await expect(source.createSession({ ...input, mode: "tui", clientRequestId: "changed" })).rejects.toThrow(/previous Cloud request/);
	});
	it.each([
		new TypeError("Failed to fetch"),
		new CloudRequestTimeoutError(),
		new CloudApiError(503, { error: "internal_error", code: "internal_error", message: "Unavailable", requestId: "r1" }),
		new CloudApiError(409, { error: "conflict", code: "IDEMPOTENCY_CONFLICT", message: "Key already used", requestId: "r1" }),
		new CloudApiError(409, { error: "conflict", code: "conflict", message: "Concurrent session limit reached.", requestId: "r1" }),
		new CloudApiError(500, { error: "internal_error", code: "SANDBOX_QUOTA_EXCEEDED", message: "Concurrent session limit reached.", requestId: "r1" }),
		new CloudApiError(503, { error: "internal_error", code: "SESSION_MODE_UNSUPPORTED", message: "Unavailable", requestId: "r1" }),
		new CloudApiError(409, { error: "conflict", code: "CHAT_DRIVER_UNAVAILABLE", message: "Unavailable", requestId: "r1" }),
		new Error("SESSION_MODE_UNSUPPORTED"),
		new Error("SANDBOX_QUOTA_EXCEEDED: Concurrent session limit reached."),
	])("retains the original key and frozen payload for every other create failure (%s)", async (error) => {
		const client = clientStub({ createSession: vi.fn().mockRejectedValueOnce(error).mockResolvedValue({ session: { id: "retry-accepted" } }) });
		const options = { projectId: "p1", prompt: "Original task", model: "first", clientRequestId: "original" };
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		await expect(source.createSession(options)).rejects.toBe(error);
		expect(await readCloudOperation("test-scope", "worker-create/p1")).toMatchObject({ key: "original", payload: { prompt: "Original task", model: "first" } });
		await expect(source.createSession({ ...options, clientRequestId: "changed" })).rejects.toThrow(/previous Cloud request/);
		expect(await source.createSession({ ...options, prompt: "Edited task", model: "second" })).toEqual({ id: "retry-accepted" });
		expect(client.createSession.mock.calls[1]).toEqual(client.createSession.mock.calls[0]);
	});
	it("releases only the rejected operation in its own account scope", async () => {
		const otherProject = prepareCloudOperation(undefined, "test-scope", { projectId: "p2" }, () => "other-project");
		const otherAccount = prepareCloudOperation(undefined, "other-account", { projectId: "p1" }, () => "other-account");
		await reserveCloudOperation("worker-create/p2", otherProject);
		await reserveCloudOperation("worker-create/p1", otherAccount);
		const error = new CloudApiError(409, { error: "conflict", code: "SANDBOX_QUOTA_EXCEEDED", message: "Concurrent session limit reached.", requestId: "quota-request" });
		const source = createCloudSessionSource({ client: clientStub({ createSession: vi.fn().mockRejectedValue(error) }) as never, orgId: "o1" });
		await expect(source.createSession({ projectId: "p1", clientRequestId: "rejected" })).rejects.toBe(error);
		expect(await readCloudOperation("test-scope", "worker-create/p1")).toBeNull();
		expect(await readCloudOperation("test-scope", "worker-create/p2")).toEqual(otherProject);
		expect(await readCloudOperation("other-account", "worker-create/p1")).toEqual(otherAccount);
	});
	it("does not clear a newer operation when an older request is rejected", async () => {
		const newer = prepareCloudOperation(undefined, "test-scope", { projectId: "p1", prompt: "Newer task" }, () => "newer");
		const error = new CloudApiError(409, { error: "conflict", code: "SANDBOX_QUOTA_EXCEEDED", message: "Concurrent session limit reached.", requestId: "late-quota" });
		const source = createCloudSessionSource({ client: clientStub({ createSession: vi.fn(async () => {
			await clearCloudOperation("test-scope", "worker-create/p1", "older");
			await reserveCloudOperation("worker-create/p1", newer);
			throw error;
		}) }) as never, orgId: "o1" });
		await expect(source.createSession({ projectId: "p1", clientRequestId: "older" })).rejects.toBe(error);
		expect(await readCloudOperation("test-scope", "worker-create/p1")).toEqual(newer);
	});
	it("does not clear an acceptance recorded while a request is in flight", async () => {
		const error = new CloudApiError(409, { error: "conflict", code: "SANDBOX_QUOTA_EXCEEDED", message: "Concurrent session limit reached.", requestId: "late-quota" });
		const source = createCloudSessionSource({ client: clientStub({ createSession: vi.fn(async () => {
			await acceptCloudOperation("test-scope", "worker-create/p1", "accepted", "accepted-session");
			throw error;
		}) }) as never, orgId: "o1" });
		await expect(source.createSession({ projectId: "p1", clientRequestId: "accepted" })).rejects.toBe(error);
		expect(await readCloudOperation("test-scope", "worker-create/p1")).toMatchObject({ key: "accepted", acceptedId: "accepted-session" });
	});
	it.each(["remove", "save"])("preserves the original quota error and retry record if cleanup cannot %s storage", async (failure) => {
		if (failure === "save") await reserveCloudOperation("worker-create/p2", prepareCloudOperation(undefined, "test-scope", { projectId: "p2" }, () => "other"));
		const error = new CloudApiError(409, { error: "conflict", code: "SANDBOX_QUOTA_EXCEEDED", message: "Concurrent session limit reached.", requestId: "original-reference" });
		const source = createCloudSessionSource({ client: clientStub({ createSession: vi.fn(async () => {
			(failure === "remove" ? storage.removeItem : storage.setItem).mockRejectedValueOnce(new Error("Storage unavailable"));
			throw error;
		}) }) as never, orgId: "o1" });
		await expect(source.createSession({ projectId: "p1", clientRequestId: "rejected" })).rejects.toBe(error);
		expect(await readCloudOperation("test-scope", "worker-create/p1")).toMatchObject({ key: "rejected" });
	});
	it("blocks double taps and stops before POST if the retry record cannot be stored", async () => {
		const client = clientStub();
		storage.setItem.mockRejectedValueOnce(new Error("Storage unavailable"));
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		await expect(source.createSession({ projectId: "p1", clientRequestId: "one" })).rejects.toThrow("Storage unavailable");
		expect(client.createSession).not.toHaveBeenCalled();
		const options = { projectId: "p1", clientRequestId: "one" };
		await Promise.all([source.createSession(options), source.createSession(options)]);
		expect(client.createSession).toHaveBeenCalledOnce();
	});
	it.each(["chat", "tui"] as const)("carries %s mode, model, effort, and reserved request identity to Cloud", async (mode) => {
		const client = clientStub();
		const source = createCloudSessionSource({ client: client as never, orgId: "o1", scopeKey: "scope-one", isCurrent: () => true });
		await source.createSession({ projectId: "p1", prompt: "Inspect", harness: "codex", mode, model: "model-test", reasoningEffort: "high", clientRequestId: "spawn-one" });
		expect(source.scopeKey).toBe("scope-one");
		expect(client.createSession).toHaveBeenCalledWith("o1", expect.objectContaining({ model: "model-test", reasoningEffort: "high" }), { idempotencyKey: "spawn-one" });
		expect(client.createSession.mock.calls[0][1]).toHaveProperty("interfaceMode", mode);
	});
	it.each(["repository_checkout_failed", "working_directory_unusable", "project_setup_failed"])("retries a connected worker after %s", async (code) => {
		const retrySessionStartup = vi.fn(async () => { throw new CloudApiError(409, { error: "conflict", code: "startup_retry_unavailable", message: "Worker has already checked in", requestId: "workspace-retry" }); });
		const requestWorkspaceCheckout = vi.fn(async () => ({ requested: true }));
		const source = createCloudSessionSource({ client: clientStub({
			getSession: vi.fn(async () => ({ session: { ...fullSession("s1"), desiredState: "running", observedState: "running", runtimeState: "running", startupError: { code, message: "Workspace preparation failed" } } })),
			retrySessionStartup, requestWorkspaceCheckout,
		}) as never, orgId: "o1" });
		await source.retryStartup!("s1");
		expect(requestWorkspaceCheckout).toHaveBeenCalledWith("o1", "s1");
		expect(retrySessionStartup).not.toHaveBeenCalled();
	});
	it("rejects a Cloud spawn without reserved identity or after account change", async () => {
		const client = clientStub();
		let current = true;
		const source = createCloudSessionSource({ client: client as never, orgId: "o1", scopeKey: "scope-one", isCurrent: () => current });
		await expect(source.createSession({ projectId: "p1" })).rejects.toThrow("request identity");
		current = false;
		await expect(source.createSession({ projectId: "p1", clientRequestId: "spawn-one" })).rejects.toThrow("account changed");
		expect(client.createSession).not.toHaveBeenCalled();
	});
	it("identifies as the cloud environment", () => {
		expect(createCloudSessionSource({ client: clientStub() as never, orgId: "o1" }).kind).toBe("cloud");
	});

	it("lists projects for the bound org and maps them", async () => {
		const client = clientStub({
			listProjects: vi.fn(async () => ({
				items: [{
					id: "p1", orgId: "o1", displayName: "web",
					repositoryUrl: "https://github.com/acme/web", defaultBranch: "main",
					config: {}, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z",
				}],
				page: { hasMore: false },
			})),
		});
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		expect(await source.listProjects()).toEqual([{ id: "p1", name: "web", kind: "single_repo" }]);
		expect(client.listProjects).toHaveBeenCalledWith("o1", expect.anything());
	});

	// A phone must not stop at page one and silently hide half the board.
	it("follows pagination cursors to the end", async () => {
		const pages = [
			{ items: [fullSession("a")], page: { hasMore: true, nextCursor: "c2" } },
			{ items: [fullSession("b")], page: { hasMore: false } },
		];
		let call = 0;
		const client = clientStub({ listSessions: vi.fn(async () => pages[call++]) });
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		const sessions = await source.listSessions();
		expect(sessions.map((s) => s.id)).toEqual(["a", "b"]);
		expect(client.listSessions).toHaveBeenCalledTimes(2);
	});

	it("spawns a worker session in the bound org, idempotently", async () => {
		const client = clientStub();
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		expect(await source.createSession({ projectId: "p1", prompt: "fix it", harness: "codex", clientRequestId: "spawn-1" }))
			.toEqual({ id: "s-new" });
		expect(client.createSession).toHaveBeenCalledWith("o1", expect.objectContaining({
			projectId: "p1", kind: "worker", harness: "codex", prompt: "fix it",
		}), expect.objectContaining({ idempotencyKey: expect.any(String) }));
	});

	it("deletes a session in the bound org", async () => {
		const client = clientStub();
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		await source.deleteSession("s1");
		expect(client.deleteSession).toHaveBeenCalledWith("o1", "s1");
	});

	it("sends a message with the client's id as the idempotency key", async () => {
		const sendMessage = vi.fn(async () => ({ event: { sessionId: "s1", sequence: 3, type: "chat.user_message", payload: { text: "hi" }, createdAt: "2026-09-01T00:00:00Z" } }));
		const source = createCloudSessionSource({
			client: clientStub({ sendMessage }) as never, orgId: "o1",
		});
		const result = await source.sendMessage("s1", { text: "hi", clientMessageId: "cm-1" });
		expect(sendMessage).toHaveBeenCalledWith("o1", "s1", "hi", expect.objectContaining({ idempotencyKey: "cm-1" }));
		// The wire response is a user-message event, not a turn: there is no
		// honest turnId to report here, so it stays undefined rather than
		// inventing one that would make the UI believe a turn exists.
		expect(result).toEqual({ duplicate: false });
	});

	it("loads Cloud model choices and forwards chosen turn settings with the message", async () => {
		const listChatModels = vi.fn(async () => ({ models: [{ id: "model-x", displayName: "Model X", default: true }] }));
		const sendMessage = vi.fn(async () => ({ event: { sessionId: "s1", sequence: 1, type: "chat.user_message", payload: { text: "hi" }, createdAt: "2026-09-01T00:00:00Z" } }));
		const source = createCloudSessionSource({ client: clientStub({
			listChatModels, sendMessage,
			getSession: vi.fn(async () => ({ session: { ...fullSession("s1"), harness: "codex", interfaceMode: "chat" } })),
			replayEvents: vi.fn(async () => ({ events: [], hasMore: false, nextAfter: 0 })),
		}) as never, orgId: "o1" });
		expect(await source.getChatModels!("s1")).toMatchObject([{ id: "model-x", displayName: "Model X" }]);
		await source.setTurnSettings!("s1", { model: "model-x", reasoningEffort: "high", approvalMode: "auto" });
		expect((await source.getConversationPage("s1")).settings).toEqual({ model: "model-x", reasoningEffort: "high", approvalMode: "auto" });
		await source.sendMessage("s1", { text: "hi", clientMessageId: "cm-1" });
		expect(sendMessage).toHaveBeenCalledWith("o1", "s1", {
			text: "hi", model: "model-x", reasoningEffort: "high", approvalMode: "auto",
		}, { idempotencyKey: "cm-1" });
	});

	it("loads the worker's Claude Code models and effort choices", async () => {
		const listChatModels = vi.fn(async () => ({ models: [{ id: "claude-sonnet", displayName: "Sonnet", default: true, efforts: ["low", "high"], defaultEffort: "high" }] }));
		const source = createCloudSessionSource({ client: clientStub({
			getSession: vi.fn(async () => ({ session: { ...fullSession("s1"), interfaceMode: "chat" } })),
			listChatModels,
		}) as never, orgId: "o1" });
		expect(await source.getChatModels!("s1")).toEqual([{ id: "claude-sonnet", displayName: "Sonnet", default: true, efforts: ["low", "high"], defaultEffort: "high" }]);
		expect(listChatModels).toHaveBeenCalledWith("o1", "s1");
	});

	it("does not present a Terminal-mode cloud session as a Chat controller", async () => {
		const client = clientStub({
			getSession: vi.fn(async () => ({ session: { ...fullSession("s1"), interfaceMode: "tui" } })),
			replayEvents: vi.fn(async () => ({ events: [], hasMore: false, nextAfter: 0 })),
		});
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		const page = await source.getConversationPage("s1");
		expect(page.mode).toBe("tui");
	});

	it("waits for Cloud to commit the Chat handoff before opening Chat", async () => {
		const getSession = vi.fn()
			.mockResolvedValueOnce({ session: { ...fullSession("s1"), interfaceMode: "tui" } })
			.mockResolvedValueOnce({ session: { ...fullSession("s1"), interfaceMode: "tui" } })
			.mockResolvedValue({ session: { ...fullSession("s1"), interfaceMode: "chat" } });
		const getSessionInterfaceTransition = vi.fn(async () => ({ supported: true, targetMode: "chat" }));
		const startSessionInterfaceTransition = vi.fn(async () => ({ transition: { phase: "requested", targetMode: "chat" } }));
		const source = createCloudSessionSource({ client: clientStub({ getSession, getSessionInterfaceTransition, startSessionInterfaceTransition }) as never, orgId: "o1" });
		const switchToChat = (source as typeof source & { switchToChat(id: string): Promise<void> }).switchToChat;
		expect(switchToChat).toBeTypeOf("function");
		vi.useFakeTimers();
		try {
			const switching = switchToChat("s1");
			await vi.advanceTimersByTimeAsync(0);
			expect(startSessionInterfaceTransition).toHaveBeenCalledWith("o1", "s1", { targetMode: "chat", policy: "drain" });
			await vi.advanceTimersByTimeAsync(2_000);
			await switching;
			expect(getSession).toHaveBeenCalledTimes(3);
		} finally {
			vi.useRealTimers();
		}
	});

	it("reports why Cloud cannot hand off instead of navigating to a fake Chat view", async () => {
		const startSessionInterfaceTransition = vi.fn();
		const source = createCloudSessionSource({ client: clientStub({
			getSession: vi.fn(async () => ({ session: { ...fullSession("s1"), interfaceMode: "tui" } })),
			getSessionInterfaceTransition: vi.fn(async () => ({ supported: false, targetMode: "chat", reason: "This harness does not support Chat." })),
			startSessionInterfaceTransition,
		}) as never, orgId: "o1" });
		await expect(source.switchToChat!("s1")).rejects.toThrow("This harness does not support Chat.");
		expect(startSessionInterfaceTransition).not.toHaveBeenCalled();
	});

	it("does not duplicate an existing Cloud Chat handoff", async () => {
		const getSession = vi.fn()
			.mockResolvedValueOnce({ session: { ...fullSession("s1"), interfaceMode: "tui" } })
			.mockResolvedValueOnce({ session: { ...fullSession("s1"), interfaceMode: "chat" } });
		const startSessionInterfaceTransition = vi.fn();
		const source = createCloudSessionSource({ client: clientStub({
			getSession,
			getSessionInterfaceTransition: vi.fn().mockResolvedValueOnce({ supported: true, targetMode: "chat", transition: { phase: "activating", targetMode: "chat" } }).mockResolvedValue({ supported: true, targetMode: "tui" }),
			startSessionInterfaceTransition,
		}) as never, orgId: "o1" });
		await source.switchToChat!("s1");
		expect(startSessionInterfaceTransition).not.toHaveBeenCalled();
	});

	it("cancels a turn with a key stable across retries of the same cancel", async () => {
		const cancelTurn = vi.fn(async (_orgId: string, _sessionId: string, _turnId: string, options: { idempotencyKey: string }) => ({ ok: true }));
		const source = createCloudSessionSource({
			client: clientStub({ cancelTurn }) as never, orgId: "o1",
		});
		await source.cancelTurn("s1", "t1");
		expect(cancelTurn).toHaveBeenCalledWith("o1", "s1", "t1", expect.objectContaining({ idempotencyKey: expect.any(String) }));
		const firstKey = cancelTurn.mock.calls[0]?.[3].idempotencyKey;

		await source.cancelTurn("s1", "t1");
		expect(cancelTurn.mock.calls[1]?.[3].idempotencyKey).toBe(firstKey);
	});

	it("builds a conversation snapshot from a cloud transcript replay", async () => {
		const client = clientStub({
			getSession: vi.fn(async () => ({ session: fullSession("s1") })),
			replayEvents: vi.fn(async () => ({
				events: [
					{ sessionId: "s1", sequence: 1, type: "chat.user_message", payload: { text: "hi" }, createdAt: "2026-09-01T00:00:00Z" },
					{ sessionId: "s1", sequence: 2, type: "chat.assistant_delta", payload: { text: "hello", turnId: "t1", attempt: 1, stream: "stdout" }, createdAt: "2026-09-01T00:00:01Z" },
				],
				hasMore: false,
				nextAfter: 2,
			})),
		});
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		const page = await source.getConversationPage("s1");

		expect(client.getSession).toHaveBeenCalledWith("o1", "s1");
		expect(client.replayEvents).toHaveBeenCalledWith("o1", "s1", expect.objectContaining({ after: 0 }));
		expect(page).toMatchObject({
			conversationId: "s1",
			sessionId: "s1",
			harness: "claude-code",
			mode: "chat",
			controller: { state: "busy" },
			latestSequence: 2,
			oldestSequence: 1,
			hasMoreBefore: false,
			turns: [{ id: "t1", state: "running" }],
			settings: {},
		});
		expect(page.items).toMatchObject([
			{ role: "user", text: "hi" },
			{ role: "assistant", text: "hello", streaming: true },
		]);
	});

	it("projects the cloud active turn into the conversation snapshot", async () => {
		const client = clientStub({
			getSession: vi.fn(async () => ({
				session: {
					...fullSession("s1"),
					activeTurn: {
						id: "t1",
						sessionId: "s1",
						userMessageSequence: 4,
						state: "provisioning",
						attemptCount: 1,
						createdAt: "2026-09-01T00:00:04Z",
						updatedAt: "2026-09-01T00:00:05Z",
					},
				},
			})),
			replayEvents: vi.fn(async () => ({ events: [], hasMore: false, nextAfter: 4 })),
		});
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });

		expect((await source.getConversationPage("s1")).turns).toEqual([{
			id: "t1",
			state: "queued",
			requestedAt: "2026-09-01T00:00:04Z",
		}]);
	});

	it("projects completed Cloud turns and assistant replies from the event history", async () => {
		const getSession = vi.fn(async () => ({ session: { ...fullSession("s1"), interfaceMode: "chat" } }));
		const replayEvents = vi.fn(async () => ({
			events: [
				{ sessionId: "s1", sequence: 1, type: "chat.user_message", payload: { text: "hi", turnId: "t1" }, createdAt: "2026-09-01T00:00:00Z" },
				{ sessionId: "s1", sequence: 2, type: "chat.turn_started", payload: { turnId: "t1" }, createdAt: "2026-09-01T00:00:01Z" },
				{ sessionId: "s1", sequence: 3, type: "chat.assistant_delta", payload: { turnId: "t1", text: "hello" }, createdAt: "2026-09-01T00:00:02Z" },
				{ sessionId: "s1", sequence: 4, type: "chat.turn_completed", payload: { turnId: "t1" }, createdAt: "2026-09-01T00:00:03Z" },
			], hasMore: false, nextAfter: 4,
		}));
		const source = createCloudSessionSource({ client: clientStub({ getSession, replayEvents }) as never, orgId: "o1" });
		const page = await source.getConversationPage("s1");
		expect(page.turns).toMatchObject([{ id: "t1", state: "completed", startedAt: "2026-09-01T00:00:01Z", completedAt: "2026-09-01T00:00:03Z" }]);
		expect(page.controller.state).toBe("ready");
		expect(page.items).toMatchObject([
			{ role: "user", text: "hi", turnId: "t1" },
			{ role: "assistant", text: "hello", turnId: "t1", streaming: false },
		]);
	});

	it("shows an approval request and sends its decision to Cloud", async () => {
		const decideChatApproval = vi.fn(async () => ({ ok: true }));
		const source = createCloudSessionSource({ client: clientStub({
			getSession: vi.fn(async () => ({ session: { ...fullSession("s1"), interfaceMode: "chat" } })),
			replayEvents: vi.fn(async () => ({ events: [
				{ sessionId: "s1", sequence: 1, type: "chat.user_message", payload: { text: "edit it", turnId: "t1" }, createdAt: "2026-09-01T00:00:00Z" },
				{ sessionId: "s1", sequence: 2, type: "chat.turn_started", payload: { turnId: "t1" }, createdAt: "2026-09-01T00:00:01Z" },
				{ sessionId: "s1", sequence: 3, type: "chat.approval_requested", payload: { turnId: "t1", requestId: "r1", summary: "Edit file", decisions: [{ id: "allow_once", label: "Allow once" }] }, createdAt: "2026-09-01T00:00:02Z" },
			], hasMore: false, nextAfter: 3 })),
			decideChatApproval,
		}) as never, orgId: "o1" });
		const page = await source.getConversationPage("s1");
		expect(page.items).toContainEqual(expect.objectContaining({ kind: "activity", activityKind: "approval", status: "pending", requestId: "r1", decisions: [{ id: "allow_once", label: "Allow once" }] }));
		expect(source.decideApproval).toBeTypeOf("function");
		await source.decideApproval!("s1", "r1", "allow_once");
		expect(decideChatApproval).toHaveBeenCalledWith("o1", "s1", "r1", "allow_once");
	});

	// A single replayEvents call is capped at the server's page limit; opening
	// a conversation longer than that must still show its live tail, not just
	// whatever the first page happened to contain.
	it("pages a multi-page transcript to the end before building the snapshot", async () => {
		const pages = [
			{ events: [{ sessionId: "s1", sequence: 1, type: "chat.user_message", payload: { text: "one" }, createdAt: "2026-09-01T00:00:00Z" }], hasMore: true, nextAfter: 1 },
			{ events: [{ sessionId: "s1", sequence: 2, type: "chat.user_message", payload: { text: "two" }, createdAt: "2026-09-01T00:00:01Z" }], hasMore: true, nextAfter: 2 },
			{ events: [{ sessionId: "s1", sequence: 3, type: "chat.user_message", payload: { text: "three" }, createdAt: "2026-09-01T00:00:02Z" }], hasMore: false, nextAfter: 3 },
		];
		let call = 0;
		const client = clientStub({
			getSession: vi.fn(async () => ({ session: fullSession("s1") })),
			replayEvents: vi.fn(async () => pages[call++]),
		});
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		const page = await source.getConversationPage("s1");

		expect(client.replayEvents).toHaveBeenCalledTimes(3);
		expect(client.replayEvents).toHaveBeenNthCalledWith(1, "o1", "s1", expect.objectContaining({ after: 0 }));
		expect(client.replayEvents).toHaveBeenNthCalledWith(2, "o1", "s1", expect.objectContaining({ after: 1 }));
		expect(client.replayEvents).toHaveBeenNthCalledWith(3, "o1", "s1", expect.objectContaining({ after: 2 }));
		expect(page.items.map((item) => (item.kind === "message" ? item.text : undefined))).toEqual(["one", "two", "three"]);
		expect(page.latestSequence).toBe(3);
		expect(page.oldestSequence).toBe(1);
	});

	it("does not skip unseen history based on an empty replay's advertised cursor", async () => {
		const client = clientStub({
			getSession: vi.fn(async () => ({ session: fullSession("s1") })),
			replayEvents: vi.fn(async () => ({ events: [], hasMore: false, nextAfter: 5 })),
		});
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		const page = await source.getConversationPage("s1");
		expect(page.latestSequence).toBe(0);
		expect(page.oldestSequence).toBe(0);
		expect(page.items).toEqual([]);
	});

	it("subscribes by polling the transcript and forwards each event to the listener", async () => {
		vi.useFakeTimers();
		const client = clientStub({
			replayEvents: vi.fn(async () => ({
				events: [{ sessionId: "s1", sequence: 1, type: "chat.user_message", payload: { text: "hi" }, createdAt: "2026-09-01T00:00:00Z" }],
				hasMore: false,
				nextAfter: 1,
			})),
		});
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		const listener = vi.fn();
		const unsubscribe = source.subscribeEvents("s1", listener);

		await vi.advanceTimersByTimeAsync(0);
		expect(listener).toHaveBeenCalledWith(expect.objectContaining({
			seq: 1, sessionId: "s1", type: "chat.user_message",
		}));

		unsubscribe();
		await vi.advanceTimersByTimeAsync(5_000);
		vi.useRealTimers();
	});

	it("resumes a paused session with the bound org id", async () => {
		const resumeSession = vi.fn(async () => ({ session: { id: "s1", desiredState: "active" } }));
		const client = clientStub({ resumeSession });
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		await source.resumeSession("s1");
		expect(resumeSession).toHaveBeenCalledWith("o1", "s1");
	});
	it("retries a failed startup on the existing session and refreshes eligibility after a conflict", async () => {
		const { CloudApiError } = await import("@aoagents/cloud-client");
		const conflict = new CloudApiError(409, { error: "conflict", code: "startup_retry_unavailable", message: "Nothing to retry", requestId: "r1" });
		const getSession = vi.fn(async () => ({ session: { ...fullSession("s1"), runtimeConnected: false, desiredState: "running", runtimeState: "terminated", startupError: { code: "worker_never_started", message: "Could not start", at: "now" } } }));
		const retrySessionStartup = vi.fn().mockRejectedValueOnce(conflict);
		const client = clientStub({ getSession, retrySessionStartup });
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		await expect(source.retryStartup!("s1")).rejects.toBe(conflict);
		expect(getSession).toHaveBeenCalledTimes(2); expect(retrySessionStartup).toHaveBeenCalledWith("o1", "s1");
		expect(client.createSession).not.toHaveBeenCalled();
	});
	it("restores archive via Restore and rejects startup retry for that archive", async () => {
		const restoreSession = vi.fn(); const retrySessionStartup = vi.fn();
		const client = clientStub({ restoreSession, retrySessionStartup, getSession: vi.fn(async () => ({ session: { ...fullSession("s1"), isTerminated: true } })) });
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		await source.restoreSession!("s1"); expect(restoreSession).toHaveBeenCalledWith("o1", "s1");
		await expect(source.retryStartup!("s1")).rejects.toThrow(/not waiting/i); expect(retrySessionStartup).not.toHaveBeenCalled();
	});
});
