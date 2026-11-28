import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";

// After the phone closed the app, a chat shows the page saved on the phone
// until the machine answers. That page is a record: a request it held may be
// answered by now and a turn it showed running may have ended days ago, so it
// must offer no Approve/Deny and show nothing as working.
const fixture = vi.hoisted(() => ({ request: vi.fn(), stored: new Map<string, string>() }));
vi.mock("../api", () => ({ apiRequest: fixture.request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ machineIdentity: (cfg: { hostId?: string }) => `host.${cfg.hostId}`, authHeaders: () => ({}), httpBase: () => "http://qa-host", DEFAULT_CONFIG: {}, hostMachineIdentity: (id: string) => `host.${id}` }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => fixture.stored.get(key) ?? null,
	setItem: async (key: string, value: string) => { fixture.stored.set(key, value); },
	removeItem: async (key: string) => { fixture.stored.delete(key); },
	getAllKeys: async () => [...fixture.stored.keys()],
	multiRemove: async (keys: string[]) => { for (const key of keys) fixture.stored.delete(key); },
} }));
vi.mock("expo-file-system", () => import("./savedConversations.fakes").then((fakes) => fakes.fileSystemModule));
vi.mock("expo-crypto", () => import("./savedConversations.fakes").then((fakes) => fakes.cryptoModule));
vi.mock("expo-secure-store", () => import("./savedConversations.fakes").then((fakes) => fakes.secureStoreModule));
vi.mock("./conversationEvents", () => ({ subscribeConversationEvents: () => () => {} }));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => null }));
import { requestDockModel } from "./requestDockModel";
import { conversationSaver, createConversationSaver, forgetSavedConversations } from "./savedConversations";
import { resetSavedChatFakes, savedFileNames } from "./savedConversations.fakes";
import { resetSavedChatMachines } from "./savedChatMachines";
import type { ConversationPage } from "./snapshot";
import { activeTurn, pendingApproval } from "./types";
import { useMobileConversation, type MobileConversation } from "./useConversation";

const lan = { hostId: "a", host: "192.168.1.20" } as ServerConfig;
let renderer: ReactTestRenderer | undefined;
let conversation!: MobileConversation;
function Harness({ cfg }: { cfg: ServerConfig | null }) {
	conversation = useMobileConversation(cfg, "host.a", "saved-chat");
	return null;
}

const savedMidTurn = {
	conversationId: "c-saved", sessionId: "saved-chat", harness: "claude-code", mode: "worker",
	controller: { state: "busy" }, latestSequence: 3, oldestSequence: 1, hasMoreBefore: false, settings: {},
	turns: [{ id: "t1", state: "running", requestedAt: "2026-11-01T10:00:00Z", startedAt: "2026-11-01T10:00:00Z" }],
	items: [
		{ kind: "message", id: "m1", turnId: "t1", sequence: 1, role: "user", text: "deploy it" },
		{ kind: "activity", id: "a1", turnId: "t1", sequence: 2, revision: 1, activityKind: "command", status: "running", summary: "npm run build", createdAt: "2026-11-01T10:00:01Z" },
		{ kind: "activity", id: "a2", turnId: "t1", sequence: 3, revision: 1, activityKind: "approval", status: "pending", summary: "Run deploy", requestId: "r1", decisions: [{ id: "allow", label: "Allow" }], createdAt: "2026-11-01T10:00:02Z" },
	],
} as unknown as ConversationPage;

beforeEach(async () => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.stored.clear();
	resetSavedChatFakes();
	resetSavedChatMachines();
	// Saved on the phone before the app was closed.
	const saver = createConversationSaver();
	saver.schedule("host.a/saved-chat", savedMidTurn);
	await saver.flush();
	fixture.request.mockReset().mockImplementation(async (cfg: ServerConfig) => ({
		json: async () => ({ controller: "ready", capabilities: [], messages: [], conversationId: `c-${cfg.hostId}` }),
	}));
});
afterEach(async () => { if (renderer) await act(async () => renderer!.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

it("shows the saved page read-only until the first live read replaces it", async () => {
	// No connection yet: only the saved page can show.
	await act(async () => { renderer = create(<Harness cfg={null} />); });
	const snapshot = conversation.snapshot!;
	expect(conversation.saved).toBe(true);
	expect(snapshot.conversationId).toBe("c-saved");
	expect(snapshot.items.map((item) => item.id)).toEqual(["m1", "a1"]);
	// No request dock or inline Approve/Deny, and nothing working: no running
	// turn (its timer), no running step (its spinner), no busy agent.
	expect(pendingApproval(snapshot)).toBeUndefined();
	expect(requestDockModel(snapshot, { approval: false, input: false })).toBeNull();
	expect(activeTurn(snapshot)).toBeUndefined();
	expect(snapshot.items.some((item) => item.kind === "activity" && item.status === "running")).toBe(false);
	expect(snapshot.controller.state).toBe("ready");

	await act(async () => renderer!.update(<Harness cfg={lan} />));
	expect(conversation.saved).toBe(false);
	expect(conversation.snapshot?.conversationId).toBe("c-a");
});

it("saves nothing from a read that answers after its machine was forgotten", async () => {
	let answer!: () => void;
	fixture.request.mockReset().mockImplementation((cfg: ServerConfig) => new Promise((resolve) => {
		answer = () => resolve({ json: async () => ({ controller: "ready", capabilities: [], messages: [], conversationId: `c-late-${cfg.hostId}` }) });
	}));
	await act(async () => { renderer = create(<Harness cfg={lan} />); });
	expect(fixture.request).toHaveBeenCalled();

	// Forgotten while the read is under way; the screen has not caught up yet.
	await forgetSavedConversations({ id: "a", name: "A", platform: "darwin", endpoints: [], lastConnected: 1 });
	expect(savedFileNames()).toEqual([]);
	await act(async () => answer());
	expect(conversation.snapshot?.conversationId).toBe("c-late-a");
	await conversationSaver.flush();

	expect(savedFileNames()).toEqual([]);
});
