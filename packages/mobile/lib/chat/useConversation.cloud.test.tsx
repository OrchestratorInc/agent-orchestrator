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
const source = { kind: "cloud", scopeKey: "account-one", getConversationPage: async () => ({ conversationId: "s1", sessionId: "s1", harness: "codex", mode: "chat", controller: { state: "busy" }, turns: [{ id: "t1", state: "running", requestedAt: "now" }], items: [], settings: {}, latestSequence: 0, oldestSequence: 0, hasMoreBefore: false, capabilities: ["steer"] }), sendMessage: f.send, steerMessage: f.steer } as unknown as import("../environment/types").SessionSource;
let renderer: ReactTestRenderer | undefined;
let conversation: MobileConversation;
const renders: Array<import("./types").ConversationSnapshot | undefined> = [];
function Harness({ current = source }: { current?: import("../environment/types").SessionSource }) { conversation = useMobileConversation(null, "s1", current); renders.push(conversation.snapshot); return null; }
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
