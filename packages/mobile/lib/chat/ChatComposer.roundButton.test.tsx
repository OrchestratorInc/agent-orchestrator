import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

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
vi.mock("./PRChip", () => ({ PRChipContent: "PRChipContent", prChipAccessibilityLabel: () => "Pull request" }));

import type { ComponentProps } from "react";
import { ChatComposer } from "./ChatComposer";

const props = (turnState: "running" | "completed") => ({
	sessionId: "session", config: null,
	snapshot: { turns: [{ id: "turn-1", state: turnState }], items: [], controller: { state: "ready" }, settings: {} },
	skills: [], filePaths: [], models: [], restingInset: 0,
	onSend: vi.fn(async () => "send-id"), onSteer: vi.fn(), onAcknowledgeSend: vi.fn(async () => {}), onInterrupt: vi.fn(async () => {}),
} as unknown as ComponentProps<typeof ChatComposer>);
let renderer: ReactTestRenderer;
afterEach(() => { if (renderer) act(() => renderer.unmount()); vi.clearAllMocks(); });

const flatten = (style: unknown): Record<string, unknown> => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : (style && typeof style === "object" ? style as Record<string, unknown> : {});
const discOf = (label: string) => renderer.root.findByProps({ accessibilityLabel: label }).findByType("View" as never);

// The round button changes face in place: React keeps the same views and only
// swaps the disc's fill and the glyph inside it. On Android a view that clips
// (overflow hidden with a radius) skips drawing a child that resized in the same
// update as its own background, so the dictate glyph that replaces the smaller
// stop glyph when a turn ends came out as a blank disc. The disc holds nothing
// that needs clipping, so it must not clip.
it("keeps the round button's disc unclipped as a turn ends and the stop face becomes the dictate face", async () => {
	await act(async () => { renderer = create(<ChatComposer {...props("running")} />); });
	expect(flatten(discOf("Stop turn").props.style).overflow).not.toBe("hidden");
	await act(async () => { renderer.update(<ChatComposer {...props("completed")} />); });
	expect(() => renderer.root.findByProps({ accessibilityLabel: "Stop turn" })).toThrow();
	expect(flatten(discOf("Dictate a message").props.style).overflow).not.toBe("hidden");
});
