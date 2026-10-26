import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";
import type { ConversationEvent } from "./sse";

const state = vi.hoisted(() => ({
	request: vi.fn(),
	listener: undefined as ((event: ConversationEvent) => void) | undefined,
	healthy: false,
	pollEvery: null as number | null,
}));
vi.mock("../api", () => ({ apiRequest: state.request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ machineIdentity: () => "qa-host", authHeaders: () => ({}), httpBase: () => "http://qa-host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: async () => null, setItem: async () => {}, removeItem: async () => {} } }));
vi.mock("./conversationEvents", () => ({
	subscribeConversationEvents: (_cfg: unknown, _session: string, listener: (event: ConversationEvent) => void) => {
		state.listener = listener;
		return () => { state.listener = undefined; };
	},
	conversationStreamHealthy: () => state.healthy,
}));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => state.pollEvery }));
import { useMobileConversation, type MobileConversation } from "./useConversation";

const config = { host: "qa-host" } as ServerConfig;
let renderer: ReactTestRenderer | undefined;
let conversation: MobileConversation;
function Harness() { conversation = useMobileConversation(config, "qa-session"); return null; }

type Pending = { signal?: AbortSignal; finish: () => void };
let pages: Pending[];
const page = { controller: "ready", messages: [], latestSequence: 1 };

function chatEvent(seq: number): ConversationEvent {
	return { seq, projectId: "p", sessionId: "qa-session", type: "chat_message", payload: { conversationId: "c" }, createdAt: "" };
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers();
	vi.clearAllMocks();
	pages = [];
	state.healthy = false;
	state.pollEvery = null;
	state.request.mockImplementation((_cfg, _path, init?: { signal?: AbortSignal }) => new Promise((resolve, reject) => {
		init?.signal?.addEventListener("abort", () => reject(Object.assign(new Error("cancelled"), { name: "AbortError" })));
		pages.push({ signal: init?.signal, finish: () => resolve({ json: async () => page }) });
	}));
});
afterEach(async () => {
	if (renderer) await act(async () => renderer!.unmount());
	renderer = undefined;
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

async function mount() {
	await act(async () => { renderer = create(<Harness />); });
	expect(pages).toHaveLength(1);
}

it("coalesces live events during a slow page load into one follow-up load", async () => {
	await mount();
	await act(async () => {
		for (let seq = 1; seq <= 5; seq++) {
			state.listener!(chatEvent(seq));
			await vi.advanceTimersByTimeAsync(200);
		}
	});
	// Nothing was cancelled and nothing new started while the first page downloads.
	expect(pages).toHaveLength(1);
	expect(pages[0].signal?.aborted).toBe(false);
	await act(async () => { pages[0].finish(); await vi.advanceTimersByTimeAsync(0); });
	expect(pages).toHaveLength(2);
	await act(async () => { pages[1].finish(); await vi.advanceTimersByTimeAsync(1_000); });
	expect(pages).toHaveLength(2);
	expect(conversation.loading).toBe(false);
});

it("cancels a superseded page download when an explicit refresh starts", async () => {
	await mount();
	let refreshed!: Promise<void>;
	await act(async () => { refreshed = conversation.refresh(); });
	expect(pages[0].signal?.aborted).toBe(true);
	expect(pages).toHaveLength(2);
	await act(async () => { pages[1].finish(); await refreshed; });
	expect(conversation.snapshot?.latestSequence).toBe(1);
	expect(conversation.error).toBeUndefined();
});

it("refreshes when the event stream reopens at head", async () => {
	await mount();
	await act(async () => { pages[0].finish(); await vi.advanceTimersByTimeAsync(0); });
	await act(async () => {
		state.listener!({ seq: 0, projectId: "", sessionId: "qa-session", type: "stream_resync", createdAt: "" });
		await vi.advanceTimersByTimeAsync(200);
	});
	expect(pages).toHaveLength(2);
});

it("polls over the tunnel only while the event stream is not delivering", async () => {
	state.pollEvery = 2_000;
	state.healthy = true;
	await mount();
	await act(async () => { pages[0].finish(); await vi.advanceTimersByTimeAsync(10_000); });
	expect(pages).toHaveLength(1);
	state.healthy = false;
	await act(async () => { await vi.advanceTimersByTimeAsync(2_200); });
	expect(pages).toHaveLength(2);
});
