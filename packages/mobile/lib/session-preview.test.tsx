import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	params: { id: "session-1", hostId: "host-1" },
	config: { host: "desktop.local", httpPort: "3011", muxPort: "3011", password: "test", hostId: "host-1" },
	navigation: { setOptions: vi.fn() },
	getPreview: vi.fn(),
	platform: { OS: "ios" },
	router: { navigate: vi.fn(), dismissTo: vi.fn() },
	backHandlers: [] as Array<() => boolean>,
	web: { reload: vi.fn(), goBack: vi.fn() },
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"), Pressable: host("Pressable"), Text: host("Text"), View: host("View"),
		Platform: fixtures.platform,
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
		BackHandler: {
			addEventListener: (_event: string, handler: () => boolean) => {
				fixtures.backHandlers.push(handler);
				return { remove: () => { fixtures.backHandlers.splice(fixtures.backHandlers.indexOf(handler), 1); } };
			},
		},
	};
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, right: 0, bottom: 34, left: 0 }) }));
vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Stack: {
			Toolbar: Object.assign(host("StackToolbar"), {
				Button: host("StackToolbarButton"),
				Menu: host("StackToolbarMenu"),
				MenuAction: host("StackToolbarMenuAction"),
			}),
		},
		useFocusEffect: (effect: () => void | (() => void)) => React.useEffect(effect, [effect]),
		useLocalSearchParams: () => fixtures.params,
		useNavigation: () => fixtures.navigation,
		useRouter: () => fixtures.router,
	};
});
vi.mock("react-native-webview", async () => {
	const React = await import("react");
	return { WebView: (props: object) => React.createElement("WebView", props) };
});
vi.mock("./api", () => ({ getPreview: fixtures.getPreview }));
vi.mock("./config", () => ({ authHeaders: () => ({ Authorization: "Bearer test" }) }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
vi.mock("./connectionError", () => ({ userFacingError: String }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));
vi.mock("./store", () => ({
	HostScope: ({ children }: { children: React.ReactNode }) => children,
	useApp: () => ({ config: fixtures.config, currentHostId: fixtures.config.hostId, connection: "open" }),
}));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "#888" });
	return {
		useTheme: () => theme,
		useThemeState: () => ({ theme, scheme: "dark" }),
		useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme),
	};
});
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("./ui", async () => {
	const React = await import("react");
	return { Button: (props: object) => React.createElement("Button", props), EmptyState: (props: object) => React.createElement("EmptyState", props) };
});
vi.mock("./preview/BrowserLivePane", async () => {
	const React = await import("react");
	return { BrowserLivePane: (props: object) => React.createElement("BrowserLivePane", props) };
});
vi.mock("./preview/LoadingBar", async () => {
	const React = await import("react");
	return { LoadingBar: (props: object) => React.createElement("LoadingBar", props) };
});

import SessionPreviewScreen from "../app/preview/[id]";

const appPreview = { entry: "index.html", url: "http://desktop.local:3011/preview/artifacts/index.html", authenticated: true };
let screen: ReactTestRenderer;
let header: ReactTestRenderer | undefined;
const nodes = (name: string) => screen.root.findAll((node) => (node.type as unknown) === name);
const lastTitle = () => fixtures.navigation.setOptions.mock.lastCall?.[0].title;
const webView = () => nodes("WebView")[0];
const loadingBar = () => nodes("LoadingBar")[0].props;

function headerButton(label: string) {
	return nodes("StackToolbarButton").find((node) => node.props.accessibilityLabel === label);
}

/**
 * Where the header's items are drawn: the page's own toolbar, or, while the
 * live browser shows, the item the page hands to it for its toolbar.
 */
function headerRoot() {
	const [pane] = nodes("BrowserLivePane");
	if (!pane) return screen.root;
	act(() => {
		header ??= create(<></>);
		header.update(pane.props.headerItem ?? <></>);
	});
	return header!.root;
}

/** The header's view menu: Desktop browser or Mobile view, with the system's check on the one showing. */
function modeMenu() {
	const [menu] = headerRoot().findAll((node) => (node.type as unknown) === "StackToolbarMenu" && String(node.props.accessibilityLabel).startsWith("View, "));
	expect(menu, "the header has the view menu").toBeDefined();
	return menu;
}

function modeAction(label: "Desktop browser" | "Mobile view") {
	return modeMenu().find((node) => (node.type as unknown) === "StackToolbarMenuAction" && node.props.children === label);
}

function mobileView() {
	const action = modeAction("Mobile view");
	return { checked: Boolean(action.props.isOn), disabled: Boolean(action.props.disabled) };
}

async function openPreview() {
	// The WebView's handle, for the page's reload and history calls.
	await act(async () => { screen = create(<SessionPreviewScreen />, { createNodeMock: (element) => element.type === "WebView" ? fixtures.web : null }); });
}

async function setMobile(enabled: boolean) {
	const action = modeAction(enabled ? "Mobile view" : "Desktop browser");
	await act(async () => action.props.onPress());
}

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.stubGlobal("IS_REACT_NATIVE_TEST_ENVIRONMENT", true);
	fixtures.params.id = "session-1";
	fixtures.params.hostId = "host-1";
	fixtures.config = { host: "desktop.local", httpPort: "3011", muxPort: "3011", password: "test", hostId: "host-1" };
	fixtures.navigation.setOptions.mockClear();
	fixtures.getPreview.mockReset().mockResolvedValue(appPreview);
	fixtures.platform.OS = "ios";
	fixtures.router.navigate.mockClear();
	fixtures.router.dismissTo.mockClear();
	fixtures.backHandlers.length = 0;
	fixtures.web.reload.mockClear();
	fixtures.web.goBack.mockClear();
});

afterEach(() => {
	act(() => { screen?.unmount(); header?.unmount(); });
	header = undefined;
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

describe("session preview mode", () => {

	it("defaults to the live browser with the view choice as a native header menu", async () => {
		await openPreview();
		expect(fixtures.getPreview).toHaveBeenCalledWith(fixtures.config, "session-1");
		expect(nodes("BrowserLivePane")).toHaveLength(1);
		expect(nodes("WebView")).toHaveLength(0);
		expect(screen.root.findAll((node) => node.props.accessibilityRole === "switch")).toHaveLength(0);
		expect(screen.root.findAll((node) => node.props.accessibilityRole === "tablist")).toHaveLength(0);
		// The live browser draws the bar's right side, so the page hands it the menu.
		expect(nodes("StackToolbar")).toHaveLength(0);
		expect(nodes("BrowserLivePane")[0].props.headerItem).toBeTruthy();
		expect(modeMenu().props).toMatchObject({ accessibilityLabel: "View, Desktop browser", icon: "desktopcomputer" });
		expect(modeAction("Desktop browser").props.isOn).toBe(true);
		expect(mobileView()).toEqual({ checked: false, disabled: false });
	});

	it("falls back to Mobile view when sharing is disabled and preserves an explicit switch back", async () => {
		await openPreview();
		await act(async () => nodes("BrowserLivePane")[0].props.onDisabled());
		expect(nodes("WebView")[0].props.source.uri).toBe(appPreview.url);
		expect(mobileView().checked).toBe(true);
		await setMobile(false);
		await act(async () => nodes("BrowserLivePane")[0].props.onDisabled());
		await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
		expect(nodes("BrowserLivePane")).toHaveLength(1);
		expect(mobileView().checked).toBe(false);
	});

	it("waits for a selected preview before applying the disabled-sharing fallback", async () => {
		let resolve!: (preview: typeof appPreview) => void;
		fixtures.getPreview.mockReturnValue(new Promise((done) => { resolve = done; }));
		await openPreview();
		await act(async () => nodes("BrowserLivePane")[0].props.onDisabled());
		expect(nodes("BrowserLivePane")).toHaveLength(1);
		expect(mobileView().disabled).toBe(true);
		await act(async () => resolve(appPreview));
		expect(nodes("WebView")[0].props.source.uri).toBe(appPreview.url);
	});

	it("does not carry disabled sharing to another session", async () => {
		await openPreview();
		await act(async () => nodes("BrowserLivePane")[0].props.onDisabled());
		expect(nodes("WebView")).toHaveLength(1);
		fixtures.params.id = "session-2";
		await act(async () => { screen.update(<SessionPreviewScreen />); });
		expect(nodes("BrowserLivePane")[0].props.sessionID).toBe("session-2");
		expect(mobileView().checked).toBe(false);
	});

	it.each(["explicit", "disabled-sharing fallback"])("preserves %s Mobile view across a LAN-to-tunnel endpoint switch", async (choice) => {
		await openPreview();
		if (choice === "explicit") await setMobile(true);
		else await act(async () => nodes("BrowserLivePane")[0].props.onDisabled());
		let resolve!: (preview: typeof appPreview) => void;
		fixtures.getPreview.mockReturnValue(new Promise((done) => { resolve = done; }));
		fixtures.config = { ...fixtures.config, host: "tunnel.example.com", httpPort: "443" };
		await act(async () => { screen.update(<SessionPreviewScreen />); });
		// Keep the selected surface while fetching a URL for the new endpoint.
		// An old URL must never be rendered with the new endpoint's credentials.
		expect(nodes("BrowserLivePane")).toHaveLength(0);
		expect(nodes("WebView")).toHaveLength(0);
		expect(mobileView().checked).toBe(true);
		const updated = { ...appPreview, url: "https://tunnel.example.com/preview/artifacts/index.html" };
		await act(async () => resolve(updated));
		expect(nodes("BrowserLivePane")).toHaveLength(0);
		expect(nodes("WebView")[0].props.source.uri).toBe(updated.url);
		expect(mobileView().checked).toBe(true);
	});

	it("preserves an explicit Browser choice across an endpoint switch after disabled sharing", async () => {
		await openPreview();
		await act(async () => nodes("BrowserLivePane")[0].props.onDisabled());
		await setMobile(false);
		fixtures.config = { ...fixtures.config, host: "tunnel.example.com", httpPort: "443" };
		await act(async () => { screen.update(<SessionPreviewScreen />); });
		await act(async () => nodes("BrowserLivePane")[0].props.onDisabled());
		expect(nodes("BrowserLivePane")).toHaveLength(1);
		expect(mobileView().checked).toBe(false);
	});

	it("starts a different host in streaming mode rather than keeping its predecessor's selection", async () => {
		await openPreview();
		await act(async () => nodes("BrowserLivePane")[0].props.onDisabled());
		fixtures.params.hostId = "host-2";
		fixtures.config = { ...fixtures.config, hostId: "host-2" };
		await act(async () => { screen.update(<SessionPreviewScreen />); });
		expect(nodes("BrowserLivePane")).toHaveLength(1);
		expect(mobileView().checked).toBe(false);
	});

	it("shows the view's own device in the bar while switching to the responsive preview and back", async () => {
		await openPreview();
		await setMobile(true);
		expect(nodes("BrowserLivePane")).toHaveLength(0);
		expect(nodes("WebView")[0].props.source).toEqual({ uri: appPreview.url, headers: { Authorization: "Bearer test" } });
		expect(mobileView().checked).toBe(true);
		expect(modeMenu().props).toMatchObject({ accessibilityLabel: "View, Mobile view", icon: "iphone" });
		expect(nodes("StackToolbar")[0].props.placement).toBe("right");
		await setMobile(false);
		expect(nodes("WebView")).toHaveLength(0);
		expect(nodes("BrowserLivePane")).toHaveLength(1);
		expect(mobileView().checked).toBe(false);
		expect(modeMenu().props.icon).toBe("desktopcomputer");
	});

	it("keeps the chosen mode during preview polls while updating the app URL", async () => {
		await openPreview();
		await setMobile(true);
		fixtures.getPreview.mockResolvedValue({ ...appPreview, url: "http://desktop.local:4317/updated" });
		await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
		expect(nodes("WebView")[0].props.source.uri).toBe("http://desktop.local:4317/updated");
		expect(mobileView().checked).toBe(true);
		await setMobile(false);
		await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
		expect(nodes("BrowserLivePane")).toHaveLength(1);
		expect(mobileView().checked).toBe(false);
	});

	it("disables responsive mode without an app preview and stays live when one becomes available", async () => {
		fixtures.getPreview.mockResolvedValue(null);
		await openPreview();
		expect(nodes("BrowserLivePane")).toHaveLength(1);
		expect(mobileView()).toEqual({ checked: false, disabled: true });
		expect(modeAction("Mobile view").props.subtitle).toBe("No app preview for this session yet");
		fixtures.getPreview.mockResolvedValue(appPreview);
		await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
		expect(nodes("BrowserLivePane")).toHaveLength(1);
		expect(mobileView()).toEqual({ checked: false, disabled: false });
	});

	it("returns to the live browser if the selected app preview is cleared", async () => {
		await openPreview();
		await setMobile(true);
		fixtures.getPreview.mockResolvedValue(null);
		await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
		expect(nodes("BrowserLivePane")).toHaveLength(1);
		expect(mobileView()).toEqual({ checked: false, disabled: true });
	});

	it("starts another session in streaming mode after using its predecessor's responsive preview", async () => {
		await openPreview();
		await setMobile(true);
		fixtures.params.id = "session-2";
		await act(async () => { screen.update(<SessionPreviewScreen />); });
		expect(nodes("BrowserLivePane")[0].props.sessionID).toBe("session-2");
		expect(mobileView().checked).toBe(false);
	});
});

describe("a preview from another machine", () => {
	it("goes back to the board beneath rather than stacking a second one", async () => {
		fixtures.params.hostId = "host-2";
		await openPreview();
		const [empty] = nodes("EmptyState");
		expect(empty.props.title).toBe("Preview belongs to another machine");
		expect(nodes("StackToolbar")).toHaveLength(0);
		await act(async () => empty.props.action.props.onPress());
		expect(fixtures.router.dismissTo).toHaveBeenCalledWith("/");
		expect(fixtures.router.navigate).not.toHaveBeenCalled();
	});
});

describe("app preview controls", () => {
	it("reloads from a native header button only while the app preview shows", async () => {
		await openPreview();
		expect(headerButton("Reload app preview")).toBeUndefined();
		await setMobile(true);
		const reload = headerButton("Reload app preview");
		expect(reload?.props.icon).toBe("arrow.clockwise");
		await act(async () => reload?.props.onPress());
		expect(fixtures.web.reload).toHaveBeenCalledTimes(1);
		expect(nodes("Pressable")).toHaveLength(0);
	});

	it("shows the page's own load progress in a native bar", async () => {
		await openPreview();
		await setMobile(true);
		expect(loadingBar()).toMatchObject({ loading: false, accessibilityLabel: "Loading app preview" });
		await act(async () => webView().props.onLoadStart());
		expect(loadingBar()).toMatchObject({ loading: true, progress: 0 });
		await act(async () => webView().props.onLoadProgress({ nativeEvent: { progress: 0.4 } }));
		expect(loadingBar()).toMatchObject({ loading: true, progress: 0.4 });
		await act(async () => webView().props.onLoadEnd());
		expect(loadingBar().loading).toBe(false);
	});

	it("lets iOS swipe through the page's history and pull to reload it", async () => {
		await openPreview();
		await setMobile(true);
		expect(webView().props).toMatchObject({ allowsBackForwardNavigationGestures: true, pullToRefreshEnabled: true, refreshControlLightMode: true });
		expect(webView().props.startInLoadingState).toBeUndefined();
	});

	it("titles the bar with the page on show", async () => {
		await openPreview();
		expect(lastTitle()).toBe("Preview");
		await act(async () => nodes("BrowserLivePane")[0].props.onTitle("Desktop dashboard"));
		expect(lastTitle()).toBe("Desktop dashboard");
		await act(async () => nodes("BrowserLivePane")[0].props.onTitle(undefined));
		expect(lastTitle()).toBe("Preview");
		await setMobile(true);
		expect(lastTitle()).toBe("index.html");
		await act(async () => webView().props.onNavigationStateChange({ url: appPreview.url, title: "Todo app", canGoBack: false }));
		expect(lastTitle()).toBe("Todo app");
		// Android names a page by its address until the page names itself.
		await act(async () => webView().props.onNavigationStateChange({ url: appPreview.url, title: appPreview.url, canGoBack: false }));
		expect(lastTitle()).toBe("index.html");
		await setMobile(false);
		expect(lastTitle()).toBe("Preview");
	});

	it("walks the page's history with Android's back before leaving Preview", async () => {
		fixtures.platform.OS = "android";
		await openPreview();
		expect(fixtures.backHandlers).toHaveLength(0);
		await setMobile(true);
		expect(fixtures.backHandlers).toHaveLength(1);
		expect(fixtures.backHandlers[0]()).toBe(false);
		await act(async () => webView().props.onNavigationStateChange({ url: appPreview.url + "#done", title: "", canGoBack: true }));
		expect(fixtures.backHandlers[0]()).toBe(true);
		expect(fixtures.web.goBack).toHaveBeenCalledTimes(1);
		await setMobile(false);
		expect(fixtures.backHandlers).toHaveLength(0);
	});

	it("leaves iOS's back to the navigation stack", async () => {
		await openPreview();
		await setMobile(true);
		expect(fixtures.backHandlers).toHaveLength(0);
	});

	it("announces a failed load with a retry that reloads the page", async () => {
		await openPreview();
		await setMobile(true);
		await act(async () => webView().props.onHttpError({ nativeEvent: { statusCode: 404 } }));
		const banner = screen.root.find((node) => node.props.accessibilityRole === "alert");
		expect(banner.findByType("Text" as never).props.children).toBe("This page wasn't found. The agent may have moved or removed it.");
		await act(async () => banner.findByType("Button" as never).props.onPress());
		expect(fixtures.web.reload).toHaveBeenCalledTimes(1);
		expect(screen.root.findAll((node) => node.props.accessibilityRole === "alert")).toHaveLength(0);
	});
});
