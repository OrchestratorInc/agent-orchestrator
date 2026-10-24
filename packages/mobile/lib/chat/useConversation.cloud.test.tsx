import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
const f = vi.hoisted(() => ({ data: new Map<string, string>(), focused: true, appState: "active", listener: undefined as ((state: string) => void) | undefined, unsubscribe: vi.fn(), subscribe: vi.fn(), steer: vi.fn(async () => ({ duplicate: false })), send: vi.fn(async () => ({ duplicate: false })) }));
vi.mock("expo-router", async () => { const { useEffect } = await import("react"); return { useFocusEffect: (effect: () => void) => useEffect(() => f.focused ? effect() : undefined, [effect, f.focused]) }; });
vi.mock("react-native", () => ({ AppState: { get currentState() { return f.appState; }, addEventListener: (_: string, callback: (state: string) => void) => { f.listener = callback; return { remove: () => { f.listener = undefined; } }; } } }));
vi.mock("../api", () => ({ apiRequest: vi.fn(), ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ machineIdentity: () => "test-host", authHeaders: () => ({}), httpBase: () => "http://test-host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => f.data.get(key) ?? null, setItem: async (key: string, value: string) => { f.data.set(key, value); }, removeItem: async (key: string) => { f.data.delete(key); },
} }));
vi.mock("./conversationEvents", () => ({ subscribeConversationEvents: () => () => {} }));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => null, CONVERSATION_POLL_MS: 2000 }));
import { useMobileConversation, type MobileConversation } from "./useConversation";
import { pendingSendKey } from "./pendingSend";
import { createCloudSessionSource } from "../cloud/source";
import { apiRequest } from "../api";
const source = { kind: "cloud", scopeKey: "account-one", getConversationPage: async () => ({ conversationId: "s1", sessionId: "s1", harness: "codex", mode: "chat", controller: { state: "busy" }, turns: [{ id: "t1", state: "running", requestedAt: "now" }], items: [], settings: {}, latestSequence: 0, oldestSequence: 0, hasMoreBefore: false, capabilities: ["steer"] }), sendMessage: f.send, steerMessage: f.steer } as unknown as import("../environment/types").SessionSource;
let renderer: ReactTestRenderer | undefined;
let conversation: MobileConversation;
const renders: Array<import("./types").ConversationSnapshot | undefined> = [];
function Harness({ current = source, config = null }: { current?: import("../environment/types").SessionSource; config?: import("../config").ServerConfig | null }) { conversation = useMobileConversation(config, "s1", current); renders.push(conversation.snapshot); return null; }
source.subscribeEvents = f.subscribe;
beforeEach(() => { f.focused = true; f.appState = "active"; f.unsubscribe.mockReset(); f.subscribe.mockReset().mockReturnValue(f.unsubscribe); });
beforeEach(() => { f.data.clear(); f.send.mockClear(); f.steer.mockClear(); vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); vi.spyOn(console, "error").mockImplementation(() => {}); });
afterEach(async () => { if (renderer) await act(async () => renderer!.unmount()); renderer = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals(); });
it("delivers Cloud steering to the captured active turn, not a new message", async () => {
	await act(async () => { renderer = create(<Harness />); });
	await act(async () => { await conversation.steer("Focus on the tests"); });
	 expect(f.steer).toHaveBeenCalledWith("s1", expect.objectContaining({ text: "Focus on the tests", clientMessageId: expect.any(String) }), "t1");
	 expect(f.send).not.toHaveBeenCalled();
});
const localConfig = { host: "local-host", httpPort: "1234", muxPort: "1234", password: "test" };
it("rejects unsupported Cloud queue promotion even with a paired daemon available", async () => {
	vi.mocked(apiRequest).mockClear();
	await act(async () => { renderer = create(<Harness config={localConfig} />); });
	await act(async () => { await expect(conversation.promoteQueuedTurn("queued")).rejects.toThrow("Queued Cloud messages cannot be steered"); });
	expect(apiRequest).not.toHaveBeenCalled();
});
it("retains the paired daemon route for local queued promotion", async () => {
	vi.mocked(apiRequest).mockClear();
	const local = { ...source, kind: "local" as const };
	await act(async () => { renderer = create(<Harness current={local} config={localConfig} />); });
	await act(async () => { await conversation.promoteQueuedTurn("queued / one"); });
	expect(apiRequest).toHaveBeenCalledExactlyOnceWith(localConfig, "/api/v1/sessions/s1/conversation/turns/queued%20%2F%20one/steer", { method: "POST" });
});
it("scopes an uncertain message to the account and freezes its model choices", async () => {
	f.send.mockRejectedValueOnce(new Error("Response lost"));
	await act(async () => { renderer = create(<Harness />); });
	await act(async () => { await expect(conversation.send("Inspect")).rejects.toThrow("Response lost"); });
	expect(f.data.has(pendingSendKey("account-one", "s1"))).toBe(true);
	expect(f.data.has(pendingSendKey("cloud", "s1"))).toBe(false);
	expect(JSON.parse(f.data.get(pendingSendKey("account-one", "s1"))!).settings).toEqual({});
});
it("pauses Cloud polling in background and on blur then resumes in foreground", async () => {
	await act(async () => { renderer = create(<Harness />); });
	expect(f.subscribe).toHaveBeenCalledOnce();
	await act(async () => { f.listener?.("background"); }); expect(f.unsubscribe).toHaveBeenCalledOnce();
	await act(async () => { f.listener?.("active"); }); expect(f.subscribe).toHaveBeenCalledTimes(2);
	f.focused = false; await act(async () => { renderer!.update(<Harness />); }); expect(f.unsubscribe).toHaveBeenCalledTimes(2);
});
it("never renders the previous account transcript during a source change", async () => {
	const first = { ...source, getConversationPage: async () => ({ ...await source.getConversationPage("s1"), items: [{ kind: "message" as const, id: "private", role: "assistant" as const, origin: "provider" as const, text: "Private account one", sequence: 1, revision: 1, streaming: false, createdAt: "now" }] }) };
	await act(async () => { renderer = create(<Harness current={first} />); });
	renders.length = 0;
	const second = { ...source, scopeKey: "account-two", getConversationPage: () => new Promise<import("./api").ConversationPage>(() => {}) };
	await act(async () => { renderer!.update(<Harness current={second} />); });
	expect(renders.every((snapshot) => !snapshot?.items.some((item) => item.id === "private"))).toBe(true);
});
it("loads Cloud turn options and applies Plan through the Cloud source", async () => {
	const models = [{ id: "claude-sonnet", displayName: "Sonnet", default: true, efforts: ["low", "high"] }];
	let mode = "agent";
	const options = () => [{ id: "cloud-execution-mode", name: "Mode", category: "execution_mode", type: "select" as const, currentValue: mode, choices: [{ value: "agent", name: "Agent" }, { value: "plan", name: "Plan" }] }];
	const current = { ...source, scopeKey: "cloud-turn-options", getChatTurnOptions: async () => ({ models, configOptions: options() }), setChatConfigOption: async (_id: string, _option: string, choice: { value: string } | { enabled: boolean }) => { if ("value" in choice) mode = choice.value; return options(); } };
	await act(async () => { renderer = create(<Harness current={current} />); });
	await act(async () => { await conversation.loadTurnOptions(); });
	expect(conversation.models).toEqual(models);
	expect(conversation.configOptions[0]).toMatchObject({ currentValue: "agent" });
	await act(async () => { await conversation.setConfigOption("cloud-execution-mode", { value: "plan" }); });
	expect(conversation.configOptions[0]).toMatchObject({ currentValue: "plan" });
});
it("does not restore a stale Agent mode when an open settings sheet chooses a model after Plan", async () => {
	const client = {
		getSession: async () => ({ session: { id: "s1", orgId: "org", projectId: "project", harness: "claude-code", mode: "trusted", interfaceMode: "chat", desiredState: "running", isTerminated: false } }),
		listChatModels: async () => ({ models: [{ id: "claude-sonnet", displayName: "Sonnet", default: true, efforts: ["low", "high"] }], modes: ["default", "plan"] }),
		replayEvents: async () => ({ events: [], nextAfter: 0, hasMore: false }),
		sendMessage: vi.fn(async () => ({})),
	};
	const current = createCloudSessionSource({ client: client as never, orgId: "org", scopeKey: "stale-sheet", isCurrent: () => true });
	await current.setTurnSettings!("s1", { executionMode: "agent" });
	await act(async () => { renderer = create(<Harness current={current} />); });
	const oldChooseSettings = conversation.chooseSettings;
	const oldSettings = conversation.snapshot!.settings;
	await act(async () => { await conversation.setConfigOption("cloud-execution-mode", { value: "plan" }); });
	await act(async () => { await oldChooseSettings({ ...oldSettings, model: "claude-sonnet", reasoningEffort: "high" }); });
	client.sendMessage.mockRejectedValueOnce(new Error("Response lost"));
	await act(async () => { await expect(conversation.send("Inspect")).rejects.toThrow("Response lost"); });
	const pendingId = conversation.pendingSends[0].id;
	await act(async () => { renderer!.unmount(); });
	await current.setTurnSettings!("s1", { executionMode: "agent" });
	await act(async () => { renderer = create(<Harness current={current} />); });
	expect(conversation.pendingSends[0]).toMatchObject({ id: pendingId, settings: { executionMode: "plan" } });
	await act(async () => { await conversation.retrySend(pendingId); });
	expect(client.sendMessage).toHaveBeenCalledWith("org", "s1", { text: "Inspect", model: "claude-sonnet", reasoningEffort: "high", mode: "read-only" }, { idempotencyKey: expect.any(String) });
	expect(client.sendMessage.mock.calls[1]).toEqual(client.sendMessage.mock.calls[0]);
	await act(async () => { await conversation.acknowledgeSend(pendingId); });
	expect(f.data.has(pendingSendKey("stale-sheet", "s1"))).toBe(false);
});
it("rediscovers native settings on a real hook remount with the same Cloud source and cached catalog", async () => {
	let native = { model: "native-a", reasoningEffort: "low" };
	const client = {
		getSession: async () => ({ session: { id: "s1", harness: "claude-code", mode: "trusted", interfaceMode: "chat", desiredState: "running", isTerminated: false } }),
		listChatModels: async () => ({ ...native, models: [{ id: "native-a", displayName: "A", default: true, efforts: ["low", "high"] }, { id: "native-b", displayName: "B", default: false, efforts: ["low", "high"] }], modes: ["default", "plan"] }),
		replayEvents: async () => ({ events: [], nextAfter: 0, hasMore: false }),
	};
	const current = createCloudSessionSource({ client: client as never, orgId: "org", scopeKey: "same-source-discovery", isCurrent: () => true });
	await act(async () => { renderer = create(<Harness current={current} />); });
	await act(async () => { await conversation.loadTurnOptions(); });
	expect(conversation.snapshot!.settings).toMatchObject({ model: "native-a", reasoningEffort: "low" });
	await act(async () => { await conversation.setConfigOption("cloud-execution-mode", { value: "plan" }); });
	await act(async () => { renderer!.unmount(); });
	native = { model: "native-b", reasoningEffort: "high" };
	await act(async () => { renderer = create(<Harness current={current} />); });
	await act(async () => { await conversation.loadTurnOptions(); });
	expect(conversation.snapshot!.settings).toMatchObject({ model: "native-b", reasoningEffort: "high", executionMode: "plan" });
	await act(async () => { await conversation.chooseSettings({ model: "manual", reasoningEffort: "medium" }); });
	await act(async () => { await conversation.loadTurnOptions(); });
	expect(conversation.snapshot!.settings).toMatchObject({ model: "manual", reasoningEffort: "medium", executionMode: "plan" });
	native = { model: "native-a", reasoningEffort: "high" };
	await act(async () => { await conversation.loadTurnOptions({ refresh: true }); });
	expect(conversation.snapshot!.settings).toMatchObject({ model: "native-a", reasoningEffort: "high", executionMode: "plan" });
});
it.each(["older-first", "newer-first"])("rejects an older Cloud catalog result and only publishes the newest refresh (%s)", async (order) => {
	const finish: Array<(value: import("./types").ChatTurnOptions) => void> = [];
	const current = { ...source, scopeKey: `catalog-order-${order}`, getChatTurnOptions: () => new Promise<import("./types").ChatTurnOptions>((resolve) => { finish.push(resolve); }) };
	await act(async () => { renderer = create(<Harness current={current} />); });
	let older!: Promise<unknown>; let newer!: Promise<unknown>;
	await act(async () => { older = conversation.loadTurnOptions().then((value) => ({ value }), (error: unknown) => ({ error })); });
	await act(async () => { newer = conversation.loadTurnOptions({ refresh: true }).then((value) => ({ value }), (error: unknown) => ({ error })); });
	expect(finish).toHaveLength(2);
	const a = { models: [{ id: "old", displayName: "Old", default: true }], configOptions: [] };
	const b = { models: [{ id: "new", displayName: "New", default: true }], configOptions: [] };
	if (order === "older-first") { await act(async () => { finish[0](a); await older; }); await act(async () => { finish[1](b); await newer; }); }
	else { await act(async () => { finish[1](b); await newer; }); await act(async () => { finish[0](a); await older; }); }
	expect(await older).toMatchObject({ error: { name: "AbortError" } });
	expect(await newer).toMatchObject({ value: b });
	expect(conversation.models).toEqual([{ id: "new", displayName: "New", default: true }]);
});
it("adopts native settings on normal retry after two callers share a failed first discovery", async () => {
	let rejectFirst!: (error: Error) => void;
	const client = {
		getSession: async () => ({ session: { id: "s1", harness: "claude-code", model: "old", mode: "trusted", interfaceMode: "chat", desiredState: "running", isTerminated: false } }),
		listChatModels: vi.fn(async () => ({ model: "native-new", reasoningEffort: "high", models: [{ id: "native-new", displayName: "Native new", default: true, efforts: ["low", "high"] }], modes: ["default", "plan"] })),
		replayEvents: async () => ({ events: [], nextAfter: 0, hasMore: false }),
	};
	client.listChatModels.mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectFirst = reject; }));
	const current = createCloudSessionSource({ client: client as never, orgId: "org", scopeKey: "shared-first-failure", isCurrent: () => true });
	await act(async () => { renderer = create(<Harness current={current} />); });
	let initial!: Promise<unknown>; let joined!: Promise<unknown>;
	await act(async () => { initial = conversation.loadTurnOptions().catch((error: unknown) => error); });
	await act(async () => { joined = conversation.loadTurnOptions().catch((error: unknown) => error); });
	expect(client.listChatModels).toHaveBeenCalledTimes(1);
	await act(async () => { rejectFirst(new Error("Worker discovery failed")); await Promise.all([initial, joined]); });
	await act(async () => { await conversation.loadTurnOptions(); });
	expect(conversation.snapshot!.settings).toMatchObject({ model: "native-new", reasoningEffort: "high" });
	expect(client.listChatModels).toHaveBeenCalledTimes(2);
});
