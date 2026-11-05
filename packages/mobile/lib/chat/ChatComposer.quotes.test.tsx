import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";
import { TextInput } from "react-native";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true, requestAnimationFrame: (cb: () => void) => { cb(); return 1; } });
const { request, storage, paste } = vi.hoisted(() => ({ paste: { handler: null as null | ((assets: unknown[]) => void) }, request: vi.fn(), storage: { getItem: vi.fn(async (_key: string) => null as string | null), setItem: vi.fn(async (_key: string, _value: string) => {}), removeItem: vi.fn(async (_key: string) => {}) } }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: storage }));
vi.mock("react-native", () => ({
	Platform: { OS: "ios" },
	ActivityIndicator: "ActivityIndicator", Image: "Image", Keyboard: { dismiss: vi.fn() },
	PanResponder: { create: () => ({ panHandlers: {} }) }, Pressable: "Pressable", ScrollView: "ScrollView", Text: "Text", TextInput: "TextInput", View: "View",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1, absoluteFill: {} },
}));
vi.mock("react-native-reanimated", () => ({ default: { View: "AnimatedView" }, Easing: { out: () => {}, cubic: () => {} }, interpolateColor: () => "gray", runOnJS: (cb: unknown) => cb, useAnimatedStyle: () => ({}), useSharedValue: (value: unknown) => ({ value }), withSpring: () => {}, withTiming: () => {} }));
vi.mock("react-native-keyboard-controller", () => ({ useReanimatedKeyboardAnimation: () => ({ progress: { value: 0 } }) }));
vi.mock("expo-router", () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock("expo-secure-store", () => ({}));
vi.mock("expo-document-picker", () => ({}));
vi.mock("expo-image-picker", () => ({ launchImageLibraryAsync: vi.fn() }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("../api", () => ({ apiRequest: request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("./conversationEvents", () => ({ subscribeConversationEvents: () => () => {} }));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => null }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn(), success: vi.fn(), error: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (factory: (theme: object) => unknown) => factory({}) }));
vi.mock("../voice/MicKey", () => ({ MicKey: "MicKey" }));
vi.mock("../voice/useVoiceInput", () => ({ useVoiceInput: () => ({}) }));
vi.mock("./chatSheetRegistry", () => ({ chatSheetRoute: vi.fn() }));
vi.mock("./ChatAttachmentMenu", () => ({ ChatAttachmentMenu: "ChatAttachmentMenu" }));
vi.mock("./useImagePaste", () => ({ useImagePaste: (handler: typeof paste.handler) => { paste.handler = handler; return () => {}; } }));
vi.mock("./composer-glass", () => ({ ComposerGlass: "ComposerGlass", composerGlassSupported: false }));
vi.mock("./ChatTurnSettingsControl", () => ({ ChatTurnSettingsControl: "ChatTurnSettingsControl" }));
vi.mock("./RequestCard", () => ({ RequestCard: "RequestCard" }));
vi.mock("./PRReviewPrompt", () => ({ PRReviewPrompt: "PRReviewPrompt" }));

import { ChatComposer } from "./ChatComposer";
import { ComposerGlass } from "./composer-glass";
import { decodeResponseDraft, encodeResponseDraft } from "./responseQuotes";
import { useMobileConversation } from "./useConversation";
import { ChatAttachmentMenu } from "./ChatAttachmentMenu";
import { launchImageLibraryAsync } from "expo-image-picker";

const source = { conversationId: "conversation", messageId: "response", revision: 1 };

const props = () => ({
	sessionId: "session", config: null,
	snapshot: { turns: [], items: [], controller: { state: "idle" }, settings: {} },
	skills: [], filePaths: [], models: [], restingInset: 0,
	onSend: vi.fn(async () => "send-id"), onSteer: vi.fn(), onAcknowledgeSend: vi.fn(async () => {}), onQuoteHandled: vi.fn(),
} as unknown as ComponentProps<typeof ChatComposer>);
let renderer: ReactTestRenderer;
afterEach(() => { if (renderer) act(() => renderer.unmount()); vi.clearAllMocks(); vi.useRealTimers(); });

it("adds and removes selected text while preserving the existing editable reply, without sending", async () => {
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	act(() => renderer.root.findByType(TextInput).props.onChangeText("Explain this"));
	act(() => renderer.update(<ChatComposer {...p} quoteRequest={{ id: "q1", text: "Title\n• Bullet", source, composerKey: "session" }} />));
	expect(renderer.root.findByType(TextInput).props.value).toBe("Explain this");
	const quote = renderer.root.findByProps({ accessibilityLabel: "Quoted response: Title\n• Bullet" });
	const panel = renderer.root.findByType(TextInput).parent?.parent;
	expect(quote.parent?.parent?.parent).toBe(panel);
	expect(panel?.findAllByType(ComposerGlass)).toHaveLength(1);
	expect(p.onSend).not.toHaveBeenCalled();
	expect(p.onQuoteHandled).toHaveBeenCalledWith("q1");
	const remove = renderer.root.findByProps({ accessibilityLabel: "Remove quote: Title\n• Bullet" });
	act(() => remove.props.onPress());
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Quoted response: Title\n• Bullet" })).toHaveLength(0);
	expect(renderer.root.findByType(TextInput).props.value).toBe("Explain this");
	await act(async () => renderer.root.findByProps({ accessibilityLabel: "Send message" }).props.onPress());
	expect(p.onSend).toHaveBeenCalledWith("Explain this", undefined, undefined, undefined);
});

it("sends desktop-compatible source references separately from the reply, then clears both", async () => {
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} quoteRequest={{ id: "q1", text: "Title\n\n• Bullet 👋", source, composerKey: "session" }} />); });
	act(() => renderer.root.findByType(TextInput).props.onChangeText("Why?"));
	await act(async () => renderer.root.findByProps({ accessibilityLabel: "Send message" }).props.onPress());
	expect(p.onSend).toHaveBeenCalledExactlyOnceWith("Why?", undefined, undefined, [{ ...source, text: "Title\n\n• Bullet 👋" }]);
	expect(renderer.root.findByType(TextInput).props.value).toBe("");
	expect(renderer.root.findAll((node) => String(node.props.accessibilityLabel).startsWith("Remove quote:"))).toHaveLength(0);
});

it("keeps a quote added while an earlier send is waiting for acknowledgement", async () => {
	const p = props();
	let finish!: (id: string) => void;
	p.onSend = vi.fn(() => new Promise<string>((resolve) => { finish = resolve; }));
	await act(async () => { renderer = create(<ChatComposer {...p} quoteRequest={{ id: "q1", text: "First", source, composerKey: "session" }} />); });
	act(() => renderer.root.findByProps({ accessibilityLabel: "Send message" }).props.onPress());
	act(() => renderer.update(<ChatComposer {...p} quoteRequest={{ id: "q2", text: "Second", source, composerKey: "session" }} />));
	await act(async () => finish("send-id"));
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Quoted response: Second" })).toHaveLength(1);
});

it("restores a saved quote alongside a typed draft", async () => {
	const p = props();
	p.config = { host: "localhost", httpPort: "1234", muxPort: "1234", password: "test" } as ComponentProps<typeof ChatComposer>["config"];
	storage.getItem.mockResolvedValueOnce(encodeResponseDraft("Saved reply", [{ id: "saved", text: "Saved excerpt", source }]));
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	expect(renderer.root.findByType(TextInput).props.value).toBe("Saved reply");
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Quoted response: Saved excerpt" })).toHaveLength(1);
});

it("saves an immediately submitted reply and quote before an old host rejects its image, then restores both", async () => {
	vi.useFakeTimers();
	const stored = new Map<string, string>();
	storage.getItem.mockImplementation(async (key: string) => stored.get(key) ?? null);
	storage.setItem.mockImplementation(async (key: string, value: string) => { stored.set(key, value); });
	storage.removeItem.mockImplementation(async (key: string) => { stored.delete(key); });
	request.mockImplementation(async (_cfg, _path, init) => ({ json: async () => init?.method === "POST"
		? { paths: [".ao/attachments/image.png"] }
		: { controller: "ready", capabilities: ["images"], messages: [] } }));
	vi.mocked(launchImageLibraryAsync).mockResolvedValue({ canceled: false, assets: [{ uri: "qa-image", width: 1, height: 1, base64: "aGVsbG8=", mimeType: "image/png" }] });
	const p = props();
	p.config = { host: "localhost", httpPort: "1234", muxPort: "1234", password: "test", hostId: "draft-rejection-host" };
	function Harness({ quote = true }: { quote?: boolean }) {
		const conversation = useMobileConversation(p.config, p.sessionId);
		return <ChatComposer {...p} onSend={conversation.send} onAcknowledgeSend={conversation.acknowledgeSend}
			quoteRequest={quote ? { id: "immediate", text: "Quoted context", source, composerKey: "session" } : undefined} />;
	}
	await act(async () => { renderer = create(<Harness />); });
	await act(async () => renderer.root.findByType(ChatAttachmentMenu).props.onChoosePhoto());
	act(() => renderer.root.findByType(TextInput).props.onChangeText("Latest reply"));
	expect(storage.setItem).not.toHaveBeenCalled(); // The 250 ms autosave has not run.
	request.mockClear();
	await act(async () => renderer.root.findByProps({ accessibilityLabel: "Send message" }).props.onPress());
	expect(request.mock.calls.filter(call => call[2]?.method === "POST")).toEqual([]);
	const saved = [...stored].find(([key]) => key.startsWith("ao.chat.draft."));
	expect(saved).toBeDefined();
	expect(decodeResponseDraft(saved![1])).toMatchObject({ text: "Latest reply", quotes: [{ text: "Quoted context", source }] });
	await act(async () => renderer.unmount());
	await act(async () => { renderer = create(<Harness quote={false} />); });
	expect(renderer.root.findByType(TextInput).props.value).toBe("Latest reply");
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Quoted response: Quoted context" })).toHaveLength(1);
});

it("keeps a photo picked while Android remounts the chat, labelled by its bytes", async () => {
	const JPEG = "/9j/4AAQSkZJRgAB";
	let pick!: (result: unknown) => void;
	vi.mocked(launchImageLibraryAsync).mockImplementation(() => new Promise((resolve) => { pick = resolve; }) as never);
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	act(() => renderer.root.findByType(ChatAttachmentMenu).props.onChoosePhoto());
	// Android's picker is its own activity: the app backgrounds and the chat
	// unmounts behind it, so the pick resolves into a composer that is gone.
	act(() => renderer.unmount());
	// Android exports base64 as JPEG but still reports a screenshot's PNG type.
	await act(async () => pick({ canceled: false, assets: [{ uri: "file:///shot.png", fileName: "shot.png", width: 1, height: 1, base64: JPEG, mimeType: "image/png" }] }));
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Remove shot.png" })).toHaveLength(1);
	await act(async () => renderer.root.findByProps({ accessibilityLabel: "Send message" }).props.onPress());
	expect(p.onSend).toHaveBeenCalledExactlyOnceWith("", [{ mimeType: "image/jpeg", data: JPEG }], undefined, undefined);
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Remove shot.png" })).toHaveLength(0);
});

it("accepts an iPhone HEIC pick, which the picker exports as JPEG, instead of rejecting its label", async () => {
	const JPEG = "/9j/4AAQSkZJRgAB";
	vi.mocked(launchImageLibraryAsync).mockResolvedValue({ canceled: false, assets: [{ uri: "file:///IMG_0001.heic", fileName: "IMG_0001.heic", width: 1, height: 1, base64: JPEG, mimeType: "image/heic" }] } as never);
	const p = props();
	p.sessionId = "heic-session";
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	await act(async () => renderer.root.findByType(ChatAttachmentMenu).props.onChoosePhoto());
	expect(renderer.root.findAll((node) => node.props.accessibilityRole === "alert")).toHaveLength(0);
	await act(async () => renderer.root.findByProps({ accessibilityLabel: "Send message" }).props.onPress());
	expect(p.onSend).toHaveBeenCalledExactlyOnceWith("", [{ mimeType: "image/jpeg", data: JPEG }], undefined, undefined);
});

it("clears only what a send submitted, keeping a photo attached in a reopened composer meanwhile", async () => {
	const JPEG = "/9j/4AAQSkZJRgAB";
	const photo = (name: string) => ({ canceled: false, assets: [{ uri: `file:///${name}`, fileName: name, width: 1, height: 1, base64: JPEG, mimeType: "image/jpeg" }] });
	vi.mocked(launchImageLibraryAsync).mockResolvedValueOnce(photo("a.jpg") as never).mockResolvedValueOnce(photo("b.jpg") as never);
	let finish!: (id: string) => void;
	const p = props();
	p.sessionId = "deferred-send-session";
	p.onSend = vi.fn(() => new Promise<string>((resolve) => { finish = resolve; }));
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	await act(async () => renderer.root.findByType(ChatAttachmentMenu).props.onChoosePhoto());
	act(() => { void renderer.root.findByProps({ accessibilityLabel: "Send message" }).props.onPress(); });
	// Leave and reopen the conversation while the send is still in flight.
	act(() => renderer.unmount());
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	await act(async () => renderer.root.findByType(ChatAttachmentMenu).props.onChoosePhoto());
	await act(async () => finish("send-id"));
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Remove a.jpg" })).toHaveLength(0);
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Remove b.jpg" })).toHaveLength(1);
});

it("attaches an image pasted into the field and sends it with the reply", async () => {
	const p = props();
	p.sessionId = "paste-session";
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	act(() => paste.handler?.([{ uri: "paste-1", fileName: "Pasted image", mimeType: "image/png", fileSize: 5, base64: "aGVsbG8=" }]));
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Remove Pasted image" })).toHaveLength(1);
	act(() => paste.handler?.([{ uri: "paste-2", fileName: "Pasted image", mimeType: "image/png", fileSize: 11 * 1024 * 1024 }]));
	expect(renderer.root.findByProps({ accessibilityRole: "alert" }).props.children).toBe("Each image must be under 10 MB.");
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Remove Pasted image" })).toHaveLength(1);
	await act(async () => renderer.root.findByProps({ accessibilityLabel: "Send message" }).props.onPress());
	expect(p.onSend).toHaveBeenCalledWith("", [{ mimeType: "image/png", data: "aGVsbG8=" }], undefined, undefined);
});
