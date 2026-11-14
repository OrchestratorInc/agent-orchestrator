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
	return { default: { View: "AnimatedView" }, FadeIn: { duration: () => undefined }, FadeOut: { duration: () => undefined }, ZoomIn: spring(), interpolate: () => 0, useAnimatedStyle: () => ({}), useSharedValue: (value: unknown) => ({ value }), withSpring: (value: unknown) => value, withTiming: (value: unknown) => value };
});
vi.mock("../useReducedMotion", () => ({ useReducedMotion: () => false }));
vi.mock("react-native-svg", () => ({ default: "Svg", Defs: "Defs", LinearGradient: "LinearGradient", Rect: "Rect", Stop: "Stop" }));
vi.mock("../ui", () => ({ Button: "Button" }));
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
vi.mock("../ImageViewer", () => ({ ImageViewer: "ImageViewer" }));
vi.mock("./useImagePaste", () => ({ useImagePaste: (handler: typeof paste.handler) => { paste.handler = handler; return () => {}; } }));
vi.mock("./composer-glass", () => ({ ComposerGlass: "ComposerGlass", composerGlassSupported: false }));
vi.mock("./ChatTurnSettingsControl", () => ({ ChatTurnSettingsControl: "ChatTurnSettingsControl" }));
vi.mock("./RequestCard", () => ({ RequestCard: "RequestCard" }));
vi.mock("../effort-slider", () => ({ EffortSliderOverlay: "EffortSliderOverlay" }));
vi.mock("./ComposerSuggestionPanel", () => ({ ComposerSuggestionPanel: "ComposerSuggestionPanel" }));

import { ChatComposer } from "./ChatComposer";
import { chatSheetRoute } from "./chatSheetRegistry";
import { encodeResponseDraft } from "./responseQuotes";

const skills = [
	{ name: "approve", description: "Approve a pending dangerous command", source: "AO" },
	{ name: "background", description: "Run a prompt in the background", source: "AO" },
];

const props = () => ({
	sessionId: "session", config: null, machine: "",
	snapshot: { turns: [], items: [], controller: { state: "idle" }, settings: {} },
	skills: [], filePaths: [], models: [], restingInset: 0,
	onLoadSkills: vi.fn(async () => skills),
	onLoadFiles: vi.fn(async () => ({ paths: ["src/app.ts", "README.md"], truncated: false })),
	onSend: vi.fn(async () => "send-id"), onSteer: vi.fn(), onAcknowledgeSend: vi.fn(async () => {}), onQuoteHandled: vi.fn(),
} as unknown as ComponentProps<typeof ChatComposer>);
let renderer: ReactTestRenderer;
afterEach(() => { if (renderer) act(() => renderer.unmount()); vi.clearAllMocks(); });

async function type(value: string, cursor = value.length) {
	const field = renderer.root.findByType(TextInput);
	await act(async () => {
		field.props.onFocus();
		field.props.onChangeText(value);
		field.props.onSelectionChange({ nativeEvent: { selection: { start: cursor, end: cursor } } });
	});
}
const panels = () => renderer.root.findAllByType("ComposerSuggestionPanel" as never);
const labels = () => (panels()[0]?.props.items as { label: string }[] | undefined)?.map((item) => item.label);

it("lists skills inline above the composer when a message starts with /, without opening a sheet", async () => {
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	expect(panels()).toHaveLength(0);
	await type("/");
	expect(p.onLoadSkills).toHaveBeenCalledTimes(1);
	expect(panels()).toHaveLength(1);
	expect(panels()[0].props.kind).toBe("skills");
	expect(labels()).toEqual(["approve", "background"]);
	expect(chatSheetRoute).not.toHaveBeenCalled();
});

it("filters as the query is typed and loads the catalog once per trigger", async () => {
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	await type("/");
	await type("/ba");
	expect(labels()).toEqual(["background"]);
	expect(p.onLoadSkills).toHaveBeenCalledTimes(1);
});

it("inserts the chosen skill, closes the list, and keeps the rest of the draft", async () => {
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	await type("/ap");
	await act(async () => panels()[0].props.onSelect("approve"));
	expect(renderer.root.findByType(TextInput).props.value).toBe("/approve ");
	expect(panels()).toHaveLength(0);
});

it("shows a loading state until the catalog arrives", async () => {
	const p = props();
	let resolve!: (value: typeof skills) => void;
	p.onLoadSkills = vi.fn(() => new Promise<typeof skills>((done) => { resolve = done; })) as never;
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	await type("/");
	expect(panels()[0].props.items).toBeUndefined();
	await act(async () => resolve(skills));
	expect(labels()).toEqual(["approve", "background"]);
});

it("stays out of the way of a slash mid-message and of text that matches no skill", async () => {
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	await type("hello /");
	expect(panels()).toHaveLength(0);
	await type("/usr/local/bin");
	expect(panels()).toHaveLength(0);
});

it("lists worktree files for @ and inserts the path without the sigil", async () => {
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	await type("look at @app");
	expect(p.onLoadFiles).toHaveBeenCalledTimes(1);
	expect(panels()[0].props.kind).toBe("files");
	expect(labels()).toEqual(["app.ts"]);
	await act(async () => panels()[0].props.onSelect("src/app.ts"));
	expect(renderer.root.findByType(TextInput).props.value).toBe("look at src/app.ts ");
	expect(panels()).toHaveLength(0);
});

it("hides the list when the field loses focus and brings it back on focus", async () => {
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	await type("/");
	await act(async () => renderer.root.findByType(TextInput).props.onBlur());
	expect(panels()).toHaveLength(0);
	await act(async () => renderer.root.findByType(TextInput).props.onFocus());
	expect(panels()).toHaveLength(1);
});

it("reads an unreported caret as the end of text set from outside the field", async () => {
	// A restored draft puts the native caret at the end without a selection event.
	const p = props();
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	const field = renderer.root.findByType(TextInput);
	await act(async () => { field.props.onFocus(); field.props.onChangeText("/ap"); });
	expect(labels()).toEqual(["approve"]);
});

it("keeps the reported caret when a late draft read leaves the typed text in place", async () => {
	let restore!: (value: string | null) => void;
	storage.getItem.mockImplementationOnce(() => new Promise<string | null>((done) => { restore = done; }));
	const p = { ...props(), machine: "machine" } as ComponentProps<typeof ChatComposer>;
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	await type("/ap fix", 3);
	expect(labels()).toEqual(["approve"]);
	await act(async () => restore(encodeResponseDraft("Saved reply", [])));
	expect(renderer.root.findByType(TextInput).props.value).toBe("/ap fix");
	expect(labels()).toEqual(["approve"]);
});

it("lists skills on focus for a restored / draft", async () => {
	storage.getItem.mockResolvedValueOnce(encodeResponseDraft("/ap", []));
	const p = { ...props(), machine: "machine" } as ComponentProps<typeof ChatComposer>;
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	expect(renderer.root.findByType(TextInput).props.value).toBe("/ap");
	await act(async () => renderer.root.findByType(TextInput).props.onFocus());
	expect(labels()).toEqual(["approve"]);
});
