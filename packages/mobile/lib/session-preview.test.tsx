import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	params: { id: "session-1", hostId: "host-1" },
	config: { host: "desktop.local", httpPort: "3011", muxPort: "3011", password: "test", hostId: "host-1" },
	navigation: { setOptions: vi.fn() },
	getPreview: vi.fn(),
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"), Pressable: host("Pressable"), Text: host("Text"), View: host("View"),
		Platform: { OS: "ios" },
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	};
});
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
		useLocalSearchParams: () => fixtures.params,
		useNavigation: () => fixtures.navigation,
		useRouter: () => ({ navigate: vi.fn() }),
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
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
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

import SessionPreviewScreen from "../app/preview/[id]";

const appPreview = { entry: "index.html", url: "http://desktop.local:3011/preview/artifacts/index.html", authenticated: true };
let screen: ReactTestRenderer;
const nodes = (name: string) => screen.root.findAll((node) => (node.type as unknown) === name);

/** The header's view menu: Desktop browser or Mobile view, with the system's check on the one showing. */
function modeMenu() {
	const [menu] = nodes("StackToolbarMenu").filter((node) => String(node.props.accessibilityLabel).startsWith("View, "));
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
	await act(async () => { screen = create(<SessionPreviewScreen />); });
}

async function setMobile(enabled: boolean) {
	await act(async () => modeAction(enabled ? "Mobile view" : "Desktop browser").props.onPress());
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
});

afterEach(() => {
	act(() => { screen?.unmount(); });
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
		expect(nodes("StackToolbar")[0].props.placement).toBe("right");
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
