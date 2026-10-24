import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import NativeAbortController from "abort-controller";
import { TextInput } from "react-native";

const storage = vi.hoisted(() => new Map<string, string>());
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => storage.get(key) ?? null,
	setItem: async (key: string, value: string) => { storage.set(key, value); },
	removeItem: async (key: string) => { storage.delete(key); },
} }));
vi.mock("react-native", () => ({
	Platform: { OS: "ios" }, AppState: { currentState: "active", addEventListener: () => ({ remove: () => {} }) },
	ActivityIndicator: "ActivityIndicator", Image: "Image", Keyboard: { dismiss: vi.fn() },
	PanResponder: { create: () => ({ panHandlers: {} }) }, Pressable: "Pressable", ScrollView: "ScrollView", Text: "Text", TextInput: "TextInput", View: "View",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1, absoluteFill: {} },
}));
vi.mock("react-native-reanimated", () => ({ default: { View: "AnimatedView" }, Easing: { out: () => {}, cubic: () => {} }, interpolateColor: () => "gray", runOnJS: (cb: unknown) => cb, useAnimatedStyle: () => ({}), useSharedValue: (value: unknown) => ({ value }), withSpring: () => {}, withTiming: () => {} }));
vi.mock("react-native-keyboard-controller", () => ({ useReanimatedKeyboardAnimation: () => ({ progress: { value: 0 } }) }));
vi.mock("expo-router", async () => { const { useEffect } = await import("react"); return { useRouter: () => ({ push: vi.fn() }), useFocusEffect: (effect: () => void) => useEffect(effect, [effect]) }; });
vi.mock("expo-secure-store", () => ({}));
vi.mock("expo-document-picker", () => ({ getDocumentAsync: vi.fn() }));
vi.mock("expo-image-picker", () => ({ launchImageLibraryAsync: vi.fn() }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("../api", () => ({ apiRequest: vi.fn(), ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ machineIdentity: () => "test-host", authHeaders: () => ({}), httpBase: () => "http://test-host" }));
vi.mock("./conversationEvents", () => ({ subscribeConversationEvents: () => () => {} }));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => null }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn(), success: vi.fn(), error: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (factory: (theme: object) => unknown) => factory({}) }));
vi.mock("../voice/MicKey", () => ({ MicKey: "MicKey" }));
vi.mock("../voice/useVoiceInput", () => ({ useVoiceInput: () => ({}) }));
vi.mock("./chatSheetRegistry", () => ({ chatSheetRoute: vi.fn() }));
vi.mock("./ChatAttachmentMenu", () => ({ ChatAttachmentMenu: "ChatAttachmentMenu" }));
vi.mock("./composer-glass", () => ({ ComposerGlass: "ComposerGlass", composerGlassSupported: false }));
vi.mock("./ChatTurnSettingsControl", () => ({ ChatTurnSettingsControl: "ChatTurnSettingsControl" }));
vi.mock("./RequestCard", () => ({ RequestCard: "RequestCard" }));
vi.mock("./PRReviewPrompt", () => ({ PRReviewPrompt: "PRReviewPrompt" }));

import { ChatComposer } from "./ChatComposer";
import { useMobileConversation, type MobileConversation } from "./useConversation";
import { cloudChatDraftKey, pendingSendKey } from "./pendingSend";
import { decodeResponseDraft } from "./responseQuotes";
import { createCloudSessionSource } from "../cloud/source";
import { cloudConversationReplayCache } from "../cloud/conversationReplay";

const event = (sequence: number, type: string, payload: Record<string, unknown>) => ({ sessionId: "s1", sequence, type, payload, createdAt: "now" });
function fixture() {
	const history = [event(1, "chat.turn_started", { turnId: "t1", attempt: 1 }), event(2, "chat.turn_capabilities", { turnId: "t1", attempt: 1, steering: true })];
	const poll = vi.fn().mockResolvedValue({ events: [], nextAfter: 7, hasMore: false });
	const steerTurn = vi.fn(async (_org: string, _session: string, _turn: string, _text: string, options: { idempotencyKey: string }) => ({ event: event(7, "chat.turn_steer_requested", { turnId: "t1", clientMessageId: options.idempotencyKey, text: "Keep this guidance" }) }));
	const sendMessage = vi.fn();
	const source = createCloudSessionSource({ scopeKey: "steering-account", isCurrent: () => true, orgId: "o1", client: {
		getSession: async () => ({ session: { id: "s1", orgId: "o1", projectId: "p1", kind: "worker", harness: "codex", interfaceMode: "chat", mode: "trusted", status: "working", activityState: "active", runtimeConnected: true, isTerminated: false, createdAt: "now", updatedAt: "now", activeTurn: { id: "t1", state: "running", attemptCount: 1 } } }),
		replayEvents: async (_org: string, _session: string, options: { after?: number; limit?: number } = {}) => options.limit === 100
			? poll(options) : { events: history.filter((item) => item.sequence > (options.after ?? 0)), nextAfter: 2, hasMore: false },
		steerTurn, sendMessage,
	} as never });
	source.subscribeEvents = () => () => {};
	return { source, poll, steerTurn, sendMessage };
}
let renderer: ReactTestRenderer | undefined;
let conversation: MobileConversation;
function Harness({ source }: { source: ReturnType<typeof createCloudSessionSource> }) {
	conversation = useMobileConversation(null, "s1", source);
	return conversation.snapshot ? <ChatComposer sessionId="s1" config={null} draftScopeKey={source.scopeKey} snapshot={conversation.snapshot}
		skills={[]} filePaths={[]} models={[]} restingInset={0} onSend={conversation.send} onSteer={conversation.steer}
		onAcknowledgeSend={conversation.acknowledgeSend} completedRetry={conversation.completedRetry}
		{...({} as Omit<React.ComponentProps<typeof ChatComposer>, "sessionId" | "config" | "snapshot" | "skills" | "filePaths" | "models" | "restingInset" | "onSend" | "onSteer" | "onAcknowledgeSend">)} /> : null;
}
const draftKey = cloudChatDraftKey("steering-account", "s1");
const pendingKey = pendingSendKey("steering-account", "s1");
beforeEach(() => {
	storage.clear(); cloudConversationReplayCache.clear(); vi.useFakeTimers(); vi.stubGlobal("AbortController", NativeAbortController);
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); vi.stubGlobal("IS_REACT_NATIVE_TEST_ENVIRONMENT", true);
	vi.stubGlobal("requestAnimationFrame", (cb: () => void) => { cb(); return 1; });
	vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(async () => { if (renderer) await act(async () => renderer!.unmount()); renderer = undefined; vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });
async function enterAndSteer(source: ReturnType<typeof createCloudSessionSource>) {
	await act(async () => { renderer = create(<Harness source={source} />); });
	act(() => renderer!.root.findByType(TextInput).props.onChangeText("Keep this guidance"));
	await act(async () => { renderer!.root.findByProps({ accessibilityLabel: "Steer this turn now" }).props.onPress(); });
}

it("keeps the real composer draft and pending steer until delayed provider acceptance", async () => {
	const { source, poll, steerTurn, sendMessage } = fixture();
	await enterAndSteer(source);
	expect(renderer!.root.findByType(TextInput).props.value).toBe("Keep this guidance");
	expect(decodeResponseDraft(storage.get(draftKey)!).text).toBe("Keep this guidance");
	const pending = JSON.parse(storage.get(pendingKey)!);
	expect(pending).toMatchObject({ kind: "steer", targetTurnId: "t1", text: "Keep this guidance" });
	expect(conversation.pendingSends).toMatchObject([{ id: pending.id, state: "sending" }]);
	poll.mockResolvedValue({ events: [event(8, "chat.turn_steered", { turnId: "t1", clientMessageId: pending.id, text: "Keep this guidance" })], nextAfter: 8, hasMore: false });
	await act(async () => { await vi.advanceTimersByTimeAsync(500); });
	expect(renderer!.root.findByType(TextInput).props.value).toBe("");
	expect(storage.has(draftKey)).toBe(false);
	expect(storage.has(pendingKey)).toBe(false);
	expect(conversation.pendingSends).toEqual([]);
	expect(steerTurn.mock.calls[0][4].idempotencyKey).toBe(pending.id);
	expect(sendMessage).not.toHaveBeenCalled();
});

it.each(["provider-rejected", "unknown"])("retains the real draft and retry identity after %s guidance", async (result) => {
	const { source, poll, steerTurn, sendMessage } = fixture();
	await enterAndSteer(source);
	expect(storage.has(pendingKey)).toBe(true);
	const pending = JSON.parse(storage.get(pendingKey)!);
	if (result === "provider-rejected") poll.mockResolvedValue({ events: [event(8, "chat.turn_steer_failed", { turnId: "t1", clientMessageId: pending.id, text: "Keep this guidance", error: "Provider declined guidance" })], nextAfter: 8, hasMore: false });
	await act(async () => { await vi.advanceTimersByTimeAsync(result === "unknown" ? 30_000 : 500); });
	expect(renderer!.root.findByType(TextInput).props.value).toBe("Keep this guidance");
	expect(decodeResponseDraft(storage.get(draftKey)!).text).toBe("Keep this guidance");
	expect(JSON.parse(storage.get(pendingKey)!)).toMatchObject({ id: pending.id, targetTurnId: "t1", kind: "steer" });
	expect(conversation.pendingSends).toMatchObject([{ id: pending.id, state: "failed", error: expect.stringMatching(result === "unknown" ? /unknown/i : /declined guidance/i) }]);
	if (result === "unknown") {
		poll.mockResolvedValue({ events: [event(8, "chat.turn_steered", { turnId: "t1", clientMessageId: pending.id, text: "Keep this guidance" })], nextAfter: 8, hasMore: false });
		await act(async () => { await conversation.retrySend(pending.id); });
		expect(steerTurn.mock.calls.map((call) => call[4].idempotencyKey)).toEqual([pending.id, pending.id]);
		expect(renderer!.root.findByType(TextInput).props.value).toBe("");
		expect(storage.has(pendingKey)).toBe(false);
		expect(storage.has(draftKey)).toBe(false);
	}
	expect(sendMessage).not.toHaveBeenCalled();
});
