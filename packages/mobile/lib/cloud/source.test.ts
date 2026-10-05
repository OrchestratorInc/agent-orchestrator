import { describe, expect, it, vi } from "vitest";
import { createCloudSessionSource } from "./source";

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
		createSession: vi.fn(async () => ({ session: { id: "s-new" } })),
		deleteSession: vi.fn(async () => ({ session: { id: "x", desiredState: "deleted" } })),
		...overrides,
	};
}

describe("createCloudSessionSource", () => {
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
		expect(await source.createSession({ projectId: "p1", prompt: "fix it", harness: "codex" }))
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

	it("does not offer Codex models to a Claude Code Cloud session", async () => {
		const listChatModels = vi.fn();
		const source = createCloudSessionSource({ client: clientStub({
			getSession: vi.fn(async () => ({ session: { ...fullSession("s1"), interfaceMode: "chat" } })),
			listChatModels,
		}) as never, orgId: "o1" });
		expect(await source.getChatModels!("s1")).toEqual([]);
		expect(listChatModels).not.toHaveBeenCalled();
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
			getSessionInterfaceTransition: vi.fn(async () => ({ supported: true, targetMode: "chat", transition: { phase: "activating", targetMode: "chat" } })),
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
		expect(client.replayEvents).toHaveBeenCalledWith("o1", "s1", { after: 0 });
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
		expect(client.replayEvents).toHaveBeenNthCalledWith(1, "o1", "s1", { after: 0 });
		expect(client.replayEvents).toHaveBeenNthCalledWith(2, "o1", "s1", { after: 1 });
		expect(client.replayEvents).toHaveBeenNthCalledWith(3, "o1", "s1", { after: 2 });
		expect(page.items.map((item) => (item.kind === "message" ? item.text : undefined))).toEqual(["one", "two", "three"]);
		expect(page.latestSequence).toBe(3);
		expect(page.oldestSequence).toBe(1);
	});

	it("falls back to the cursor for latest/oldest sequence on an empty replay", async () => {
		const client = clientStub({
			getSession: vi.fn(async () => ({ session: fullSession("s1") })),
			replayEvents: vi.fn(async () => ({ events: [], hasMore: false, nextAfter: 5 })),
		});
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		const page = await source.getConversationPage("s1");
		expect(page.latestSequence).toBe(5);
		expect(page.oldestSequence).toBe(5);
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
});
