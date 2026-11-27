import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";
import { Keyboard, TextInput } from "react-native";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true, requestAnimationFrame: (cb: () => void) => { cb(); return 1; } });
const { request, storage, paste } = vi.hoisted(() => ({ paste: { handler: null as null | ((assets: unknown[]) => void) }, request: vi.fn(), storage: { getItem: vi.fn(async (_key: string) => null as string | null), setItem: vi.fn(async (_key: string, _value: string) => {}), removeItem: vi.fn(async (_key: string) => {}) } }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: storage }));
vi.mock("react-native", () => ({
	Platform: { OS: "ios" },
	ActivityIndicator: "ActivityIndicator", Image: "Image", Keyboard: { dismiss: vi.fn() },
	PanResponder: { create: () => ({ panHandlers: {} }) }, Pressable: "Pressable", ScrollView: "ScrollView", Text: "Text", TextInput: "TextInput", View: "View",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1, absoluteFill: {} },
	useWindowDimensions: () => ({ fontScale: 1 }),
}));
vi.mock("react-native-reanimated", () => {
	const spring = (): object => new Proxy({}, { get: () => () => spring() });
	return { default: { View: "AnimatedView" }, FadeIn: { duration: () => undefined }, FadeOut: { duration: () => undefined }, ZoomIn: spring(), interpolate: () => 0, measure: () => null, useAnimatedReaction: () => undefined, useAnimatedRef: () => ({ current: null }), useAnimatedStyle: () => ({}), useFrameCallback: () => ({ setActive: () => {} }), useSharedValue: (value: unknown) => ({ value }), withSpring: (value: unknown) => value, withTiming: (value: unknown) => value };
});
vi.mock("../useReducedMotion", () => ({ useReducedMotion: () => false }));
vi.mock("react-native-svg", () => ({ default: "Svg", Defs: "Defs", LinearGradient: "LinearGradient", Rect: "Rect", Stop: "Stop" }));
vi.mock("../ui", () => ({ Button: "Button" }));
vi.mock("react-native-keyboard-controller", () => ({ useReanimatedKeyboardAnimation: () => ({ progress: { value: 0 } }), useReanimatedFocusedInput: () => ({ input: { value: null } }), useKeyboardHandler: () => {} }));
vi.mock("expo-router", () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock("expo-secure-store", () => ({}));
vi.mock("expo-document-picker", () => ({}));
vi.mock("expo-image-picker", () => ({ launchImageLibraryAsync: vi.fn() }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("../api", () => ({ apiRequest: request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("./conversationEvents", () => ({ subscribeConversationEvents: () => () => {} }));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => null }));
// Saving the transcript for after a restart is not under test here.
vi.mock("./savedConversations", () => ({ conversationSaver: { schedule: () => {}, flush: async () => {} }, loadSavedConversation: async () => null }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn(), success: vi.fn(), error: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (factory: (theme: object) => unknown) => factory({}) }));
vi.mock("../voice/MicKey", () => ({ MicKey: "MicKey" }));
vi.mock("../voice/useVoiceInput", () => ({ useVoiceInput: () => ({}) }));
vi.mock("./chatSheetRegistry", () => ({ chatSheetRoute: vi.fn() }));
vi.mock("./ChatAttachmentMenu", () => ({ ChatAttachmentMenu: "ChatAttachmentMenu" }));
vi.mock("../ImageViewer", () => ({ ImageViewer: "ImageViewer" }));
vi.mock("./useImagePaste", () => ({ useImagePaste: (handler: typeof paste.handler) => { paste.handler = handler; return () => {}; } }));
vi.mock("./composer-glass", () => ({ ComposerGlass: "ComposerGlass", composerGlassSupported: false }));
vi.mock("./ChatTurnSettingsControl", () => ({ ChatTurnSettingsControl: "ChatTurnSettingsControl" }));
vi.mock("./RequestCard", () => ({ RequestCard: "RequestCard" }));
vi.mock("../effort-slider", () => ({ EffortSliderOverlay: "EffortSliderOverlay" }));
vi.mock("../permission-picker", () => ({ PermissionPickerHost: "PermissionPickerHost", PermissionPickerButton: "PermissionPickerButton" }));
vi.mock("../model-wheel", () => ({ ModelWheelHost: "ModelWheelHost" }));
vi.mock("./PRChip", () => ({ PRChipContent: "PRChipContent", prChipAccessibilityLabel: () => "Pull request" }));

import { ChatComposer } from "./ChatComposer";
import { ComposerGlass } from "./composer-glass";
import { decodeResponseDraft, encodeResponseDraft } from "./responseQuotes";
import { useMobileConversation } from "./useConversation";
import { ChatAttachmentMenu } from "./ChatAttachmentMenu";
import { HardwareReturn } from "./HardwareReturn";
import { launchImageLibraryAsync } from "expo-image-picker";

const source = { conversationId: "conversation", messageId: "response", revision: 1 };

const props = () => ({
	sessionId: "session", config: null, machine: "",
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
	// The field's slot in the row is the hardware-Return wrapper around it.
	const panel = renderer.root.findByType(HardwareReturn).parent?.parent;
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
	p.machine = "http.localhost.1234";
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
	p.machine = "host.draft-rejection-host";
	function Harness({ quote = true }: { quote?: boolean }) {
		const conversation = useMobileConversation(p.config, p.machine, p.sessionId);
		return <ChatComposer {...p} onSend={(text, images, resources, excerpts, typed) => conversation.send(text, images, resources, excerpts, undefined, typed)} onAcknowledgeSend={conversation.acknowledgeSend}
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

const noModifiers = { command: false, control: false, option: false };

it("sends on hardware Return and keeps the field focused for the next message", async () => {
	let delivered!: (id: string) => void;
	const p = { ...props(), onSend: vi.fn(() => new Promise<string>((resolve) => { delivered = resolve; })) };
	const focus = vi.fn();
	// The renderer has no native field behind the ref; give it one that can take focus.
	await act(async () => { renderer = create(<ChatComposer {...p} />, { createNodeMock: (element) => element.type === "TextInput" ? { focus } : null }); });
	act(() => renderer.root.findByType(TextInput).props.onChangeText("Run the tests"));
	await act(async () => renderer.root.findByType(HardwareReturn).props.onReturn(noModifiers));
	expect(p.onSend).toHaveBeenCalledWith("Run the tests", undefined, undefined, undefined);
	// In flight: the field is read-only, so focus waits for the send to land.
	expect(renderer.root.findByType(HardwareReturn).props.enabled).toBe(false);
	expect(focus).not.toHaveBeenCalled();
	await act(async () => delivered("send-id"));
	expect(p.onSteer).not.toHaveBeenCalled();
	// The keyboard stays and the field takes focus back once it is editable again,
	// so the next message can be typed straight away.
	expect(Keyboard.dismiss).not.toHaveBeenCalled();
	expect(focus).toHaveBeenCalledOnce();
	expect(renderer.root.findByType(TextInput).props.value).toBe("");

	// The send button still puts the keyboard away, and does not pull focus back.
	act(() => renderer.root.findByType(TextInput).props.onChangeText("Then this"));
	await act(async () => renderer.root.findByProps({ accessibilityLabel: "Send message" }).props.onPress());
	await act(async () => delivered("send-id-2"));
	expect(Keyboard.dismiss).toHaveBeenCalledOnce();
	expect(focus).toHaveBeenCalledOnce();
});

it("turns hardware Return off while the agent is stopped", async () => {
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} snapshot={{ ...p.snapshot, controller: { state: "stopped" } } as typeof p.snapshot} />); });
	expect(renderer.root.findByType(HardwareReturn).props.enabled).toBe(false);
});

it("stays open across a hardware-Return send while the read-only field is away", async () => {
	let delivered!: (id: string) => void;
	const opened: boolean[] = [];
	const p = { ...props(), onSend: vi.fn(() => new Promise<string>((resolve) => { delivered = resolve; })), onExpandedChange: (expanded: boolean) => { opened.push(expanded); } };
	const focus = vi.fn();
	await act(async () => { renderer = create(<ChatComposer {...p} />, { createNodeMock: (element) => element.type === "TextInput" ? { focus } : null }); });
	const field = () => renderer.root.findByType(TextInput);
	act(() => field().props.onFocus());
	act(() => field().props.onChangeText("Run the tests"));
	await act(async () => renderer.root.findByType(HardwareReturn).props.onReturn(noModifiers));
	// Going read-only for the send takes the field's focus.
	act(() => field().props.onBlur());
	// The send lands: the draft clears before the field has its focus back.
	await act(async () => delivered("send-id"));
	expect(focus).toHaveBeenCalledOnce();
	act(() => field().props.onFocus());
	expect(opened.at(-1)).toBe(true);
	expect(opened.slice(opened.indexOf(true))).not.toContain(false);
	// Once the field has its focus back, leaving it empty closes the composer as usual.
	act(() => field().props.onBlur());
	expect(opened.at(-1)).toBe(false);
});

it("gives Return back to the field while the composer cannot send", async () => {
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} pending />); });
	expect(renderer.root.findByType(HardwareReturn).props.enabled).toBe(false);
	await act(async () => renderer.update(<ChatComposer {...p} disabled />));
	expect(renderer.root.findByType(HardwareReturn).props.enabled).toBe(false);
	await act(async () => renderer.update(<ChatComposer {...p} />));
	expect(renderer.root.findByType(HardwareReturn).props.enabled).toBe(true);
});

it("steers on Cmd+Return only when the draft can steer, and sends otherwise", async () => {
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	act(() => renderer.root.findByType(TextInput).props.onChangeText("Idle, so this sends"));
	await act(async () => renderer.root.findByType(HardwareReturn).props.onReturn({ ...noModifiers, command: true }));
	expect(p.onSend).toHaveBeenCalledWith("Idle, so this sends", undefined, undefined, undefined);
	expect(p.onSteer).not.toHaveBeenCalled();

	const running = { ...p.snapshot, capabilities: ["steer"], turns: [{ id: "turn", state: "running" }] } as unknown as typeof p.snapshot;
	const steering = { ...p, snapshot: running, onSteer: vi.fn(async () => "steer-id") };
	await act(async () => renderer.update(<ChatComposer {...steering} />));
	act(() => renderer.root.findByType(TextInput).props.onChangeText("Use the other branch"));
	await act(async () => renderer.root.findByType(HardwareReturn).props.onReturn({ ...noModifiers, control: true }));
	expect(steering.onSteer).toHaveBeenCalledWith("Use the other branch");
});

// Ultracode is Claude Code's: a send that switches the chat to another agent
// goes to that agent, which must not get a prefix nobody typed. It is a switch
// apart from the level, so it asks at High on a model with no Extra High.
it("asks for Ultracode only when the message goes to Claude Code, at any level", async () => {
	const ultracodeKey = "ao.chat.ultracode.host.m.session";
	storage.getItem.mockImplementation(async (key: string) => (key === ultracodeKey ? "1" : null));
	const sendWith = async (over: { pending?: { harness: string; modelLabel: string }; cloud?: boolean } = {}) => {
		const p = {
			...props(),
			machine: "host.m",
			cloud: over.cloud,
			snapshot: { harness: "claude-code", turns: [], items: [], controller: { state: "idle" }, capabilities: [], settings: { model: "sonnet", reasoningEffort: "high" } },
			models: [{ id: "sonnet", displayName: "Sonnet", efforts: ["low", "medium", "high"], defaultEffort: "high" }],
			otherAgents: { agents: [], models: {}, pending: over.pending, onPick: vi.fn() },
		} as unknown as ComponentProps<typeof ChatComposer>;
		await act(async () => { renderer = create(<ChatComposer {...p} />); });
		// The switch the slider carries: none while the message goes elsewhere.
		const offered = renderer.root.findAll((node) => node.props.overKeyboard === true && "ultracode" in node.props && node.props.onRelease).some((node) => node.props.ultracode?.on === true);
		act(() => renderer.root.findByType(TextInput).props.onChangeText("fix it"));
		await act(async () => renderer.root.findByType(HardwareReturn).props.onReturn(noModifiers));
		act(() => renderer.unmount());
		return { text: vi.mocked(p.onSend).mock.calls[0]?.[0], offered };
	};
	expect(await sendWith()).toEqual({ text: "Ultracode:\nfix it", offered: true });
	expect(await sendWith({ pending: { harness: "codex", modelLabel: "GPT" } })).toEqual({ text: "fix it", offered: false });
	// Orchestrator Cloud has no Ultracode, as on desktop, whatever the phone kept.
	expect(await sendWith({ cloud: true })).toEqual({ text: "fix it", offered: false });
});

// Ultracode and Fast mode over the slider, kept on the phone as the store would.
const keptOnPhone = () => {
	const stored = new Map<string, string>();
	storage.getItem.mockImplementation(async (key: string) => stored.get(key) ?? null);
	storage.setItem.mockImplementation(async (key: string, value: string) => { stored.set(key, value); });
	storage.removeItem.mockImplementation(async (key: string) => { stored.delete(key); });
	return stored;
};
const sliderHost = () => renderer.root.findAll((node) => node.props.overKeyboard === true && node.props.onRelease && "ultracode" in node.props)[0];
const sendText = async (text: string) => {
	act(() => renderer.root.findByType(TextInput).props.onChangeText(text));
	await act(async () => renderer.root.findByType(HardwareReturn).props.onReturn(noModifiers));
};
const chatOn = (harness: string, model: string) => ({ harness, turns: [], items: [], controller: { state: "idle" }, capabilities: [], settings: { model } });

// Desktop offers Ultracode on a pending switch to Claude Code, asks for it in
// the message that switches, and keeps it on after (SessionChatSurface's
// switched()). A pick for the agent already running is no switch, so this
// starts from Codex.
it("offers a switch to Claude Code its own Ultracode, asks for it in the switching message, and keeps it on after", async () => {
	const stored = keptOnPhone();
	const key = "ao.chat.ultracode.host.m.session";
	const onPick = vi.fn();
	const base = { ...props(), machine: "host.m", snapshot: chatOn("codex", "gpt-5.5"), models: [{ id: "gpt-5.5", displayName: "GPT-5.5" }] };
	const withPick = (pending?: object) => ({ ...base, otherAgents: { agents: [{ id: "claude-code", label: "Claude Code" }], models: {}, pending, onPick } }) as unknown as ComponentProps<typeof ChatComposer>;
	const pick = { harness: "claude-code", model: "opus", modelLabel: "Opus" };
	await act(async () => { renderer = create(<ChatComposer {...withPick(pick)} />); });
	expect(sliderHost().props.ultracode).toEqual({ on: false, toggle: expect.any(Function) });
	act(() => sliderHost().props.ultracode.toggle());
	expect(onPick).toHaveBeenLastCalledWith({ ...pick, ultracode: true });

	await act(async () => renderer.update(<ChatComposer {...withPick({ ...pick, ultracode: true })} />));
	expect(sliderHost().props.ultracode.on).toBe(true);
	await sendText("fix it");
	expect(base.onSend).toHaveBeenLastCalledWith("Ultracode:\nfix it", undefined, undefined, undefined, "fix it");
	expect(stored.get(key)).toBe("1");

	// The chat now runs Claude Code: the messages after the switch ask for it too.
	const after = { ...withPick(undefined), snapshot: chatOn("claude-code", "opus"), models: [{ id: "opus", displayName: "Opus" }] } as unknown as ComponentProps<typeof ChatComposer>;
	await act(async () => renderer.update(<ChatComposer {...after} />));
	expect(sliderHost().props.ultracode.on).toBe(true);
	await sendText("and this");
	expect(base.onSend).toHaveBeenLastCalledWith("Ultracode:\nand this", undefined, undefined, undefined, "and this");
});

// Desktop turns Ultracode off with a switch away; a switch back to Claude Code
// must not bring back a flag nobody turned on again.
it("drops Ultracode with a switch away, and a switch back to Claude Code does not bring it back", async () => {
	const stored = keptOnPhone();
	const key = "ao.chat.ultracode.host.m.session";
	stored.set(key, "1");
	const onPick = vi.fn();
	const base = { ...props(), machine: "host.m" };
	const at = (snapshot: object, model: string, pending?: object) => ({ ...base, snapshot, models: [{ id: model, displayName: model }], otherAgents: { agents: [], models: {}, pending, onPick } }) as unknown as ComponentProps<typeof ChatComposer>;
	const claude = chatOn("claude-code", "opus");
	const codex = chatOn("codex", "gpt-5.5");
	await act(async () => { renderer = create(<ChatComposer {...at(claude, "opus")} />); });
	expect(sliderHost().props.ultracode.on).toBe(true);
	// To Codex: no prefix, and off once the switch lands.
	await act(async () => renderer.update(<ChatComposer {...at(claude, "opus", { harness: "codex", modelLabel: "GPT-5.5" })} />));
	await sendText("one");
	expect(base.onSend).toHaveBeenLastCalledWith("one", undefined, undefined, undefined);
	expect(stored.has(key)).toBe(false);
	await act(async () => renderer.update(<ChatComposer {...at(codex, "gpt-5.5")} />));
	// Back to Claude Code with Ultracode not picked for the switch.
	await act(async () => renderer.update(<ChatComposer {...at(codex, "gpt-5.5", { harness: "claude-code", modelLabel: "Opus" })} />));
	expect(sliderHost().props.ultracode.on).toBe(false);
	await sendText("two");
	expect(base.onSend).toHaveBeenLastCalledWith("two", undefined, undefined, undefined);
	await act(async () => renderer.update(<ChatComposer {...at(claude, "opus")} />));
	expect(sliderHost().props.ultracode.on).toBe(false);
	await sendText("three");
	expect(base.onSend).toHaveBeenLastCalledWith("three", undefined, undefined, undefined);
});

// The same holds when the chat moves to another agent elsewhere (the desktop,
// another phone) while this composer is up.
it("drops a kept Ultracode once the chat is seen running another agent", async () => {
	const stored = keptOnPhone();
	const key = "ao.chat.ultracode.host.m.session";
	stored.set(key, "1");
	const at = (snapshot: object) => ({ ...props(), machine: "host.m", snapshot, models: [] }) as unknown as ComponentProps<typeof ChatComposer>;
	await act(async () => { renderer = create(<ChatComposer {...at(chatOn("claude-code", "opus"))} />); });
	expect(sliderHost().props.ultracode.on).toBe(true);
	await act(async () => renderer.update(<ChatComposer {...at(chatOn("codex", "gpt-5.5"))} />));
	expect(stored.has(key)).toBe(false);
	const back = at(chatOn("claude-code", "opus"));
	await act(async () => renderer.update(<ChatComposer {...back} />));
	expect(sliderHost().props.ultracode.on).toBe(false);
	await sendText("fix it");
	expect(back.onSend).toHaveBeenLastCalledWith("fix it", undefined, undefined, undefined);
});

// With a switch picked, the bolt is the switch's Fast mode (desktop's pending
// switch), sent with it: never the running agent's.
it("offers the switch's own Fast mode while a switch is picked, never the running agent's", async () => {
	const onPick = vi.fn();
	const base = {
		...props(),
		machine: "host.m",
		snapshot: chatOn("claude-code", "opus"),
		// The running agent's model has a Fast mode of its own.
		models: [{ id: "opus", displayName: "Opus", efforts: ["low", "high"], defaultEffort: "high", supportsFast: true }],
	};
	const fast = { codex: { models: ["gpt-5.5"], byDefault: false } };
	const withPick = (pending?: object) => ({ ...base, otherAgents: { agents: [{ id: "codex", label: "Codex" }], models: {}, fast, pending, onPick } }) as unknown as ComponentProps<typeof ChatComposer>;
	await act(async () => { renderer = create(<ChatComposer {...withPick()} />); });
	expect(sliderHost().props.fast).toMatchObject({ on: false });
	const running = sliderHost().props.fast;

	const pick = { harness: "codex", model: "gpt-5.5", modelLabel: "GPT-5.5" };
	await act(async () => renderer.update(<ChatComposer {...withPick(pick)} />));
	expect(sliderHost().props.fast).not.toBe(running);
	expect(sliderHost().props.fast).toEqual({ on: false, toggle: expect.any(Function) });
	act(() => sliderHost().props.fast.toggle());
	expect(onPick).toHaveBeenLastCalledWith({ ...pick, fastMode: true });
	await act(async () => renderer.update(<ChatComposer {...withPick({ ...pick, fastMode: true })} />));
	expect(sliderHost().props.fast.on).toBe(true);
	act(() => sliderHost().props.fast.toggle());
	expect(onPick).toHaveBeenLastCalledWith({ ...pick, fastMode: undefined });

	// A model of the switch with no Fast mode: no bolt, and not the running agent's either.
	await act(async () => renderer.update(<ChatComposer {...withPick({ harness: "codex", model: "gpt-5.1-mini", modelLabel: "mini" })} />));
	expect(sliderHost().props.fast).toBeUndefined();
});
