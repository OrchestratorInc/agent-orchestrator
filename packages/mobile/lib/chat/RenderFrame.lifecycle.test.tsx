import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";

vi.mock("react-native", () => ({
	View: "View",
	Text: "Text",
	Pressable: "Pressable",
	Modal: "Modal",
	StyleSheet: { create: (styles: object) => styles, absoluteFill: {} },
	useWindowDimensions: () => ({ width: 390, height: 800 }),
}));
vi.mock("react-native-webview", () => ({ WebView: "WebView" }));
// Like the real hook, focus changes re-render through a subscription, past React.memo.
const navigation = vi.hoisted(() => ({ focused: true, listeners: new Set<() => void>() }));
vi.mock("expo-router/react-navigation", async () => {
	const React = await import("react");
	const subscribe = (listener: () => void) => { navigation.listeners.add(listener); return () => { navigation.listeners.delete(listener); }; };
	return { useIsFocused: () => React.useSyncExternalStore(subscribe, () => navigation.focused) };
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }) }));
vi.mock("expo-linking", () => ({ openURL: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {} }));
vi.mock("expo-secure-store", () => ({}));
vi.mock("../haptics", () => ({ haptics: { tap: () => {} } }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../store", () => ({ useApp: () => ({ config: { host: "active-a", httpPort: "3011", muxPort: "", password: "secret-a", hostId: "host-a" } }) }));
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "#000" });
	return { useTheme: () => theme, useThemeState: () => ({ scheme: "dark" }), useThemedStyles: (factory: (t: unknown) => unknown) => factory(theme) };
});
vi.mock("./RenderSkeleton", () => ({ RenderSkeleton: "RenderSkeleton" }));
vi.mock("./renderHeightStore", () => ({ startRenderHeightStore: () => {} }));

import { RenderFrame } from "./RenderFrame";
import { notifyRenderViewport } from "./renderFrameModel";

const render = { id: "page", title: "Page", height: 300, path: "/api/v1/sessions/s/renders/page" };
const config: ServerConfig = { host: "routed-b", httpPort: "3443", muxPort: "", secure: true, password: "secret-b", hostId: "host-b" };
// Where the frame sits in the window; measureInWindow reads it.
let frameTop = 100;
let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	frameTop = 100;
	navigation.focused = true;
	act(() => {
		renderer = create(<RenderFrame render={render} config={config} />, {
			createNodeMock: () => ({
				measureInWindow: (callback: (x: number, y: number, w: number, h: number) => void) => callback(0, frameTop, 350, 300),
				injectJavaScript: () => {},
			}),
		});
	});
	// Layout gives the frame a width and a first measure; it is on screen, so its page mounts.
	act(() => frame().props.onLayout({ nativeEvent: { layout: { width: 350 } } }));
});

afterEach(() => {
	act(() => renderer?.unmount());
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

const frame = () => renderer!.root.findAll((node) => node.type === ("View" as never) && typeof node.props.onLayout === "function")[0];
const webView = () => renderer!.root.findAll((node) => node.type === ("WebView" as never))[0];
const skeletonShown = () => renderer!.root.findAll((node) => node.type === ("RenderSkeleton" as never)).length > 0;
// Viewport checks run on the next frame.
const moveFrame = (top: number) => act(() => { frameTop = top; notifyRenderViewport(); vi.advanceTimersByTime(20); });
const setFocused = (focused: boolean) => act(() => { navigation.focused = focused; for (const listener of navigation.listeners) listener(); });

it.each(["inline", "fullscreen"])("loads the %s page from the routed host with its own credentials", (mode) => {
	if (mode === "fullscreen") act(() => renderer!.root.findByProps({ accessibilityLabel: "Expand Page" }).props.onPress());
	const page = renderer!.root.findAll((node) => node.type === ("WebView" as never) && node.props.scrollEnabled === (mode === "fullscreen"))[0];
	expect(page.props.source).toEqual({
		uri: expect.stringMatching(/^https:\/\/routed-b:3443\/api\/v1\/sessions\/s\/renders\/page#/),
		headers: { Authorization: "Bearer secret-b", "X-AO-Expected-Host-ID": "host-b" },
	});
});

it("updates inline and expanded page requests when the routed host endpoint or credentials change", () => {
	act(() => renderer!.root.findByProps({ accessibilityLabel: "Expand Page" }).props.onPress());
	act(() => renderer!.update(<RenderFrame render={render} config={{ ...config, host: "new-b", password: "rotated-b" }} />));
	const pages = renderer!.root.findAll((node) => node.type === ("WebView" as never));
	expect(pages).toHaveLength(2);
	for (const page of pages) expect(page.props.source).toEqual({
		uri: expect.stringMatching(/^https:\/\/new-b:3443\/api\/v1\/sessions\/s\/renders\/page#/),
		headers: { Authorization: "Bearer rotated-b", "X-AO-Expected-Host-ID": "host-b" },
	});
});

it("shows a noninteractive page title for Cloud even with an active local pairing", () => {
	act(() => renderer!.root.findByProps({ accessibilityLabel: "Expand Page" }).props.onPress());
	act(() => renderer!.update(<RenderFrame render={render} config={null} />));
	expect(renderer!.root.findAllByType("WebView" as never)).toHaveLength(0);
	expect(renderer!.root.findAllByType("Modal" as never)).toHaveLength(0);
	expect(renderer!.root.findAllByType("Text" as never).map((node) => node.props.children)).toEqual(["Page"]);
	const fallback = renderer!.root.findByType("Pressable" as never);
	expect(fallback.props.disabled).toBe(true);
	expect(fallback.props.accessibilityRole).toBeUndefined();
});

it("keeps the skeleton for a page that returns after leaving range mid-load", () => {
	expect(webView()).toBeDefined();
	// The page's document loads, which arms the fallback that shows a page that never reports a height.
	act(() => webView().props.onLoadEnd());
	// Scrolled far out of range before the page reported its height: the WebView goes.
	moveFrame(10_000);
	act(() => { vi.advanceTimersByTime(500); });
	expect(webView()).toBeUndefined();
	// The old page's fallback would have fired by now.
	act(() => { vi.advanceTimersByTime(5_000); });
	// Back in range: a fresh WebView loads, and the skeleton covers it until its height arrives.
	moveFrame(100);
	expect(webView()).toBeDefined();
	expect(skeletonShown()).toBe(true);
});

it("keeps the skeleton for a page remounted after its process died", () => {
	act(() => webView().props.onLoadEnd());
	// The OS ends the page's process; the frame mounts the page afresh.
	act(() => webView().props.onRenderProcessGone());
	expect(webView()).toBeDefined();
	act(() => { vi.advanceTimersByTime(5_000); });
	expect(skeletonShown()).toBe(true);
});

it("still shows a page that loads but never reports a height", () => {
	act(() => webView().props.onLoadEnd());
	expect(skeletonShown()).toBe(true);
	act(() => { vi.advanceTimersByTime(5_000); });
	expect(skeletonShown()).toBe(false);
});

it("shows the page as soon as it reports its height", () => {
	act(() => webView().props.onLoadEnd());
	act(() => webView().props.onMessage({ nativeEvent: { data: JSON.stringify({ type: "height", height: 280 }) } }));
	expect(skeletonShown()).toBe(false);
});

it("keeps a page through a moment out of range, as when earlier history loads above it", () => {
	const page = webView();
	act(() => page.props.onMessage({ nativeEvent: { data: JSON.stringify({ type: "height", height: 280 }) } }));
	// The list lays out with everything pushed down, then the scroll anchor puts it back.
	moveFrame(10_000);
	act(() => { vi.advanceTimersByTime(100); });
	moveFrame(100);
	act(() => { vi.advanceTimersByTime(1_000); });
	// The same WebView, still showing its page: nothing unloaded or reloaded.
	expect(webView()).toBe(page);
	expect(skeletonShown()).toBe(false);
});

it("lets a page go once it has really left range", () => {
	moveFrame(10_000);
	act(() => { vi.advanceTimersByTime(300); });
	expect(webView()).toBeDefined();
	act(() => { vi.advanceTimersByTime(200); });
	expect(webView()).toBeUndefined();
});

it("lets its page go under a pushed screen and loads it again on return", () => {
	act(() => webView().props.onMessage({ nativeEvent: { data: JSON.stringify({ type: "height", height: 280 }) } }));
	// Another screen is pushed over the session: the frame is still laid out "in view".
	setFocused(false);
	act(() => { vi.advanceTimersByTime(500); });
	expect(webView()).toBeUndefined();
	setFocused(true);
	expect(webView()).toBeDefined();
});
