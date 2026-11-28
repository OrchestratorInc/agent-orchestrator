import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true, requestAnimationFrame: (cb: () => void) => { cb(); return 1; } });
const { request, storage, paste, frames } = vi.hoisted(() => ({
	paste: { handler: null as null | ((assets: unknown[]) => void) },
	request: vi.fn(),
	storage: { getItem: vi.fn(async (_key: string) => null as string | null), setItem: vi.fn(async (_key: string, _value: string) => {}), removeItem: vi.fn(async (_key: string) => {}) },
	// The field row's mounted view, as the animated ref reports it on the UI thread.
	frames: { node: null as unknown, measured: 0 },
}));
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
	// An animated ref is a callback ref on the JS side and a getter on the UI thread.
	const fieldRow = Object.assign((...args: unknown[]) => {
		if (args.length > 0) { frames.node = args[0] ?? null; return undefined; }
		return frames.node;
	}, { current: null });
	// Reanimated 4.5.1's native measure(): only a -1 tag counts as "not rendered".
	// On Fabric an unmounted ref reads null, which goes straight to _measure and throws.
	const measure = (ref: () => unknown) => {
		const node = ref();
		if (node === -1) return null;
		if (node === null || node === undefined) throw new Error("Value is null, expected an Object");
		frames.measured += 1;
		return { x: 0, y: 12, width: 320, height: 44, pageX: 0, pageY: 0 };
	};
	// A frame callback that is switched on runs on the next frame; run it right away.
	const useFrameCallback = (callback: (info: { timestamp: number }) => void) => ({
		setActive: (active: boolean) => { if (active) callback({ timestamp: 0 }); },
	});
	return { default: { View: "AnimatedView" }, FadeIn: { duration: () => undefined }, FadeOut: { duration: () => undefined }, ZoomIn: spring(), interpolate: () => 0, measure, useAnimatedReaction: () => undefined, useAnimatedRef: () => fieldRow, useAnimatedStyle: () => ({}), useFrameCallback, useSharedValue: (value: unknown) => ({ value }), withSpring: (value: unknown) => value, withTiming: (value: unknown) => value };
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
vi.mock("./PRChip", () => ({ PRChipContent: "PRChipContent", prChipAccessibilityLabel: () => "Pull request" }));
import { ChatComposer } from "./ChatComposer";
import type { RequestDockModel } from "./requestDockModel";

const approval: RequestDockModel = {
	kind: "approval", title: "Approval needed", requestId: "request-1", sequence: 3,
	pages: [], canAnswerInline: true, busy: false,
};

const props = () => ({
	sessionId: "session", config: null, machine: "",
	snapshot: { turns: [], items: [], controller: { state: "idle" }, settings: {} },
	skills: [], filePaths: [], models: [], restingInset: 0,
	fieldGrowth: { value: 0 },
	onSend: vi.fn(async () => "send-id"), onSteer: vi.fn(), onAcknowledgeSend: vi.fn(async () => {}), onQuoteHandled: vi.fn(),
} as unknown as ComponentProps<typeof ChatComposer>);

// Host views get a stand-in instance, so a mounted field row reads as a node.
const nodeMock = () => ({});
let renderer: ReactTestRenderer | undefined;
beforeEach(() => { frames.node = null; frames.measured = 0; });
afterEach(() => { if (renderer) act(() => renderer?.unmount()); renderer = undefined; vi.clearAllMocks(); });

// Opening a chat that is waiting on an approval or a question put the card in the
// field row's place, but the row watch still started on mount and measured a row that
// was never rendered: Reanimated threw on the UI thread and the app went blank.
it("opens with a pending request without measuring the field row it replaces", async () => {
	await act(async () => { renderer = create(<ChatComposer {...props()} request={approval} />, { createNodeMock: nodeMock }); });
	expect(renderer?.root.findAllByType("RequestCard" as never)).toHaveLength(1);
	expect(frames.measured).toBe(0);
});

it("still follows the field row while it is on screen", async () => {
	await act(async () => { renderer = create(<ChatComposer {...props()} />, { createNodeMock: nodeMock }); });
	expect(frames.measured).toBeGreaterThan(0);
});
