import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
const f = vi.hoisted(() => ({ data: new Map<string, string>(), steer: vi.fn(async () => ({ duplicate: false })), send: vi.fn(async () => ({ duplicate: false })) }));
vi.mock("../api", () => ({ apiRequest: vi.fn(), ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ machineIdentity: () => "test-host", authHeaders: () => ({}), httpBase: () => "http://test-host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => f.data.get(key) ?? null, setItem: async (key: string, value: string) => { f.data.set(key, value); }, removeItem: async (key: string) => { f.data.delete(key); },
} }));
vi.mock("./conversationEvents", () => ({ subscribeConversationEvents: () => () => {} }));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => null, CONVERSATION_POLL_MS: 2000 }));
import { useMobileConversation, type MobileConversation } from "./useConversation";
const source = { kind: "cloud", scopeKey: "account-one", getConversationPage: async () => ({ conversationId: "s1", sessionId: "s1", harness: "codex", mode: "chat", controller: { state: "busy" }, turns: [{ id: "t1", state: "running", requestedAt: "now" }], items: [], settings: {}, latestSequence: 0, oldestSequence: 0, hasMoreBefore: false, capabilities: ["steer"] }), sendMessage: f.send, steerMessage: f.steer } as unknown as import("../environment/types").SessionSource;
let renderer: ReactTestRenderer | undefined;
let conversation: MobileConversation;
function Harness() { conversation = useMobileConversation(null, "s1", source); return null; }
beforeEach(() => { f.data.clear(); f.send.mockClear(); f.steer.mockClear(); vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); vi.spyOn(console, "error").mockImplementation(() => {}); });
afterEach(async () => { if (renderer) await act(async () => renderer!.unmount()); renderer = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals(); });
it("delivers Cloud steering to the captured active turn, not a new message", async () => {
	await act(async () => { renderer = create(<Harness />); });
	await act(async () => { await conversation.steer("Focus on the tests"); });
	 expect(f.steer).toHaveBeenCalledWith("s1", expect.objectContaining({ text: "Focus on the tests", clientMessageId: expect.any(String) }), "t1");
	 expect(f.send).not.toHaveBeenCalled();
});
