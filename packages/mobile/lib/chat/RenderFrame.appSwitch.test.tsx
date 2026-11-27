import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

// A chat's rendered pages through an app switch. The app drops its verified
// connection (`config`) in the background and while it races the machine's
// endpoints on return; the machine (`machine`) stays. A page already on screen
// must stay as it is, with the same WebView and source, instead of folding to
// its title and loading again; one first mounted while the connection is away
// waits for it.
vi.mock("react-native", () => ({
	View: "View",
	Text: "Text",
	Pressable: "Pressable",
	Platform: { OS: "ios" },
	StyleSheet: { create: (styles: object) => styles, absoluteFill: {} },
	useWindowDimensions: () => ({ width: 390, height: 800 }),
}));
vi.mock("expo-router", () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock("react-native-webview", () => ({ WebView: "WebView" }));
vi.mock("expo-router/react-navigation", () => ({ useIsFocused: () => true }));
vi.mock("../useInlinedArtifactPage", () => ({ useInlinedArtifactPage: () => ({ failed: false }) }));
vi.mock("expo-linking", () => ({ openURL: vi.fn() }));
vi.mock("../haptics", () => ({ haptics: { tap: () => {} } }));
vi.mock("../config", () => ({
	authHeaders: (config: { password: string }) => ({ Authorization: `Bearer ${config.password}` }),
	httpBase: (config: { host: string; httpPort: string }) => `http://${config.host}:${config.httpPort}`,
	machineIdentity: () => "host.lab-host",
}));
// The store, changed from the test the way an app switch changes it.
const store = vi.hoisted(() => ({
	app: { config: null as Record<string, string> | null, machine: "host.lab-host", currentHostId: "lab-host" },
	listeners: new Set<() => void>(),
}));
vi.mock("../store", async () => {
	const React = await import("react");
	const subscribe = (listener: () => void) => { store.listeners.add(listener); return () => { store.listeners.delete(listener); }; };
	return { useApp: () => React.useSyncExternalStore(subscribe, () => store.app) };
});
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "#000" });
	return { useTheme: () => theme, useThemeState: () => ({ scheme: "dark" }), useThemedStyles: (factory: (t: unknown) => unknown) => factory(theme) };
});
vi.mock("../ui", () => ({ Button: "Button", EmptyState: "EmptyState" }));
vi.mock("./RenderSkeleton", () => ({ RenderSkeleton: "RenderSkeleton" }));
vi.mock("./renderHeightStore", () => ({ startRenderHeightStore: () => {} }));

import { RenderFrame } from "./RenderFrame";
import { RenderViewer } from "./RenderViewer";

const lan = { host: "192.168.1.20", httpPort: "3011", muxPort: "3011", password: "secret", hostId: "lab-host" };
const render = { id: "page", title: "Page", height: 300, path: "/api/v1/sessions/s/renders/page" };
const setApp = (patch: Partial<typeof store.app>) => act(() => {
	store.app = { ...store.app, ...patch };
	for (const listener of store.listeners) listener();
});
let renderer: ReactTestRenderer | undefined;
const nodeMock = { createNodeMock: () => ({ measureInWindow: (callback: (x: number, y: number, w: number, h: number) => void) => callback(0, 100, 350, 300), injectJavaScript: () => {} }) };
const webViews = (root: ReactTestRenderer) => root.root.findAll((node) => node.type === ("WebView" as never));
const folded = (root: ReactTestRenderer) => root.root.findAll((node) => node.type === ("Pressable" as never) && node.props.disabled === true).length > 0;
function mountFrame(): ReactTestRenderer {
	let mounted: ReactTestRenderer | undefined;
	act(() => { mounted = create(<RenderFrame render={render} />, nodeMock); });
	const frame = mounted!.root.findAll((node) => node.type === ("View" as never) && typeof node.props.onLayout === "function")[0];
	if (frame) act(() => frame.props.onLayout({ nativeEvent: { layout: { width: 350 } } }));
	return mounted!;
}

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	store.app = { config: lan, machine: "host.lab-host", currentHostId: "lab-host" };
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

it("keeps a rendered page on screen, with the same WebView and source, through an app switch", () => {
	renderer = mountFrame();
	const [page] = webViews(renderer);
	const source = page.props.source;
	expect(source).toEqual({ uri: expect.stringContaining("http://192.168.1.20:3011/api/v1/sessions/s/renders/page"), headers: { Authorization: "Bearer secret" } });

	setApp({ config: null });
	expect(folded(renderer)).toBe(false);
	expect(webViews(renderer)[0]).toBe(page);
	expect(webViews(renderer)[0].props.source).toBe(source);

	// Verified again: a new object for the same endpoint and credential.
	setApp({ config: { ...lan } });
	expect(webViews(renderer)[0]).toBe(page);
	expect(webViews(renderer)[0].props.source).toBe(source);
});

it("loads a page from the new endpoint once it is verified on another one", () => {
	renderer = mountFrame();
	const source = webViews(renderer)[0].props.source;
	setApp({ config: null });
	setApp({ config: { ...lan, host: "lab.ts.net" } });
	expect(webViews(renderer)[0].props.source).not.toBe(source);
	expect(webViews(renderer)[0].props.source.uri).toContain("http://lab.ts.net:3011");
});

it("starts a frame mounted while the connection is away without its page, and loads it once verified", () => {
	store.app = { ...store.app, config: null };
	renderer = mountFrame();
	expect(webViews(renderer)).toHaveLength(0);
	setApp({ config: lan });
	const frame = renderer.root.findAll((node) => node.type === ("View" as never) && typeof node.props.onLayout === "function")[0];
	act(() => frame.props.onLayout({ nativeEvent: { layout: { width: 350 } } }));
	expect(webViews(renderer)).toHaveLength(1);
});

it("lets another machine's selection take the page down", () => {
	renderer = mountFrame();
	setApp({ config: null, machine: "host.other" });
	expect(folded(renderer)).toBe(true);
	expect(webViews(renderer)).toHaveLength(0);
});

it("keeps the full-screen page through an app switch instead of saying the machine isn't connected", () => {
	act(() => { renderer = create(<RenderViewer path={render.path} title="Page" />, nodeMock); });
	const [page] = webViews(renderer!);
	setApp({ config: null });
	expect(renderer!.root.findAll((node) => node.type === ("EmptyState" as never))).toHaveLength(0);
	expect(webViews(renderer!)[0]).toBe(page);
});
