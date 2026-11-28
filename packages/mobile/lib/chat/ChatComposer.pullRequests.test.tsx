import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";
import { Keyboard, TextInput } from "react-native";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true, requestAnimationFrame: (cb: () => void) => { cb(); return 1; } });
const { request, storage, paste, keyboard, effortState } = vi.hoisted(() => ({ effortState: { toggled: 0 }, keyboard: { dismiss: (() => {}) as () => void, listeners: new Map<string, Set<() => void>>(), addListener(event: string, handler: () => void) { const set = keyboard.listeners.get(event) ?? new Set(); keyboard.listeners.set(event, set); set.add(handler); return { remove: () => set.delete(handler) }; } }, paste: { handler: null as null | ((assets: unknown[]) => void) }, request: vi.fn(), storage: { getItem: vi.fn(async (_key: string) => null as string | null), setItem: vi.fn(async (_key: string, _value: string) => {}), removeItem: vi.fn(async (_key: string) => {}) } }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: storage }));
vi.mock("react-native", () => ({
	Platform: { OS: "ios" },
	ActivityIndicator: "ActivityIndicator", Image: "Image", Keyboard: keyboard, BackHandler: { addEventListener: () => ({ remove: () => {} }) },
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
vi.mock("./PRChip", () => ({ PRChipContent: "PRChipContent", prChipAccessibilityLabel: (pr: { number: number }, _summary: unknown, badge?: { more: number }) => `Pull request ${pr.number}${badge?.more ? ` and ${badge.more} more` : ""}` }));
vi.mock("../pull-request-picker", () => ({ PullRequestPickerOverlay: "PullRequestPickerOverlay" }));
// A real open/close for the effort slider, with a level to pick, so the two overlays can close each other.
vi.mock("./useChatEffort", async () => {
	const React = await import("react");
	return { useChatEffort: () => {
		const [open, setOpen] = React.useState(false);
		// Stable, as the real hook's are (useEffortSliderOpen's callbacks).
		const toggle = React.useCallback(() => setOpen((v: boolean) => !v), []);
		const close = React.useCallback(() => setOpen(false), []);
		return { choices: [{ value: "high", label: "High", selected: true }], open, toggle, close, choose: () => {}, release: () => {} };
	} };
});

import { ChatComposer } from "./ChatComposer";
import { ChatTurnSettingsControl } from "./ChatTurnSettingsControl";
import { markTouchInside, rootTouchStart } from "../touchOutside";
import type { DashboardPR } from "../api";
import { sessionPullRequestModel } from "../sessionPullRequests";

const pr = (number: number, state: "open" | "merged" | "closed"): DashboardPR => ({ number, url: `https://forge.test/pr/${number}`, state }) as DashboardPR;
const model = (...prs: DashboardPR[]) => sessionPullRequestModel({ prs }, () => undefined)!;

const props = (extra: Record<string, unknown> = {}) => ({
	sessionId: "session", config: null, machine: "",
	snapshot: { turns: [], items: [], controller: { state: "idle" }, settings: {} },
	skills: [], filePaths: [], models: [], restingInset: 0,
	onSend: vi.fn(async () => "send-id"), onSteer: vi.fn(), onAcknowledgeSend: vi.fn(async () => {}),
	onOpenPullRequest: vi.fn(),
	...extra,
} as unknown as ComponentProps<typeof ChatComposer>);

let renderer: ReactTestRenderer;
afterEach(() => { if (renderer) act(() => renderer.unmount()); vi.clearAllMocks(); keyboard.listeners.clear(); });

const chip = () => renderer.root.find((n) => typeof n.props.accessibilityLabel === "string" && n.props.accessibilityLabel.startsWith("Pull request ") && typeof n.props.onPress === "function");
const picker = () => renderer.root.findAll((n) => (n.type as unknown) === "PullRequestPickerOverlay");
// The overlay only draws once the pill has a height, as the effort slider's does.
const layOut = () => act(() => renderer.root.findAll((n) => typeof n.props.onLayout === "function").forEach((n) => n.props.onLayout({ nativeEvent: { layout: { height: 74, width: 300, x: 0, y: 0 } } })));

async function render(extra: Record<string, unknown>) {
	const p = props(extra);
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	layOut();
	return p;
}

it("shows the chip for a worker whose PRs are all merged, with the others' +N", async () => {
	await render({ pullRequests: model(pr(9125, "merged"), pr(9120, "merged")) });
	const content = renderer.root.find((n) => (n.type as unknown) === "PRChipContent");
	expect(content.props.pr.number).toBe(9125);
	expect(content.props.more).toEqual({ count: 1, tone: "merged" });
});

it("one PR opens its review", async () => {
	const p = await render({ pullRequests: model(pr(9129, "open")) });
	expect(chip().props.accessibilityHint).toBe("Opens the pull request review");
	act(() => chip().props.onPress());
	expect(p.onOpenPullRequest).toHaveBeenCalledWith(expect.objectContaining({ number: 9129 }));
	expect(picker()).toHaveLength(0);
});

it("several PRs open the list, and a row opens its review", async () => {
	const p = await render({ pullRequests: model(pr(9137, "open"), pr(9125, "merged"), pr(9001, "closed")) });
	expect(chip().props.accessibilityHint).toBe("Lists this worker's pull requests");
	act(() => chip().props.onPress());
	expect(picker()).toHaveLength(1);
	expect(picker()[0].props.rows.map((r: { number: number }) => r.number)).toEqual([9137, 9125, 9001]);
	act(() => picker()[0].props.onChoose(pr(9125, "merged")));
	expect(p.onOpenPullRequest).toHaveBeenCalledWith(expect.objectContaining({ number: 9125 }));
	expect(picker()).toHaveLength(0);
});

it("the list and the effort slider close each other", async () => {
	await render({ pullRequests: model(pr(9137, "open"), pr(9125, "merged")) });
	const effortOpen = () => renderer.root.findByType(ChatTurnSettingsControl).props.effort.open;
	act(() => chip().props.onPress());
	expect(picker()).toHaveLength(1);
	act(() => renderer.root.findByType(ChatTurnSettingsControl).props.effort.onToggle());
	expect(effortOpen()).toBe(true);
	expect(picker()).toHaveLength(0);
	act(() => chip().props.onPress());
	expect(picker()).toHaveLength(1);
	expect(effortOpen()).toBe(false);
});

// Review Focus 3: the touch that closed the actions sheet is not a touch outside the list.
it("a menu request opens the list after the sheet's touch", async () => {
	const p = props({ pullRequests: model(pr(9137, "open"), pr(9125, "merged")), pullRequestListRequest: 0 });
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	layOut();
	expect(picker()).toHaveLength(0);
	act(() => rootTouchStart());
	act(() => renderer.update(<ChatComposer {...p} pullRequestListRequest={1} />));
	expect(picker()).toHaveLength(1);
	act(() => { markTouchInside(); rootTouchStart(); });
	expect(picker()).toHaveLength(1);
});

// A handled menu request is consumed: the PRs changing afterwards must not replay it.
it("does not reopen the list for a menu request it already handled", async () => {
	const two = model(pr(9137, "open"), pr(9125, "merged"));
	const p = props({ pullRequests: two, pullRequestListRequest: 0 });
	await act(async () => { renderer = create(<ChatComposer {...p} />); });
	layOut();
	act(() => renderer.update(<ChatComposer {...p} pullRequestListRequest={1} />));
	expect(picker()).toHaveLength(1);
	act(() => rootTouchStart());
	expect(picker()).toHaveLength(0);
	act(() => renderer.update(<ChatComposer {...p} pullRequests={model(pr(9137, "open"), pr(9125, "merged"), pr(9001, "closed"))} pullRequestListRequest={1} />));
	expect(picker()).toHaveLength(0);
	act(() => renderer.update(<ChatComposer {...p} pullRequests={model(pr(9137, "open"), pr(9125, "merged"), pr(9001, "closed"))} pullRequestListRequest={2} />));
	expect(picker()).toHaveLength(1);
});

// Review Focus 4: a chip tap with the keyboard up closes the keyboard, not the list.
it("pressing the chip with the keyboard up keeps the list open", async () => {
	const dismiss = vi.fn(() => { for (const listener of [...(keyboard.listeners.get("keyboardDidHide") ?? [])]) listener(); });
	keyboard.dismiss = dismiss;
	await render({ pullRequests: model(pr(9137, "open"), pr(9125, "merged")) });
	act(() => chip().props.onPress());
	expect(dismiss).toHaveBeenCalled();
	expect(picker()).toHaveLength(1);
});

it("says the others to a screen reader", async () => {
	await render({ pullRequests: model(pr(9137, "open"), pr(9125, "merged"), pr(9120, "merged"), pr(9101, "merged")) });
	expect(chip().props.accessibilityLabel).toBe("Pull request 9137 and 3 more");
});

it("shows no chip without pull requests", async () => {
	await render({});
	expect(renderer.root.findAll((n) => typeof n.props.accessibilityLabel === "string" && n.props.accessibilityLabel.startsWith("Pull request "))).toHaveLength(0);
});
