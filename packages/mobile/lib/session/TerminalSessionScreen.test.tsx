import { isValidElement, type ReactElement } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type Handlers = {
	onStatus: (status: string) => void;
	onTerminalResize: (id: string, cols: number, rows: number) => void;
};

const fixtures = vi.hoisted(() => {
	const interfaceSwitch = () => ({
		status: undefined as { supported?: boolean } | undefined,
		transition: undefined as Record<string, unknown> | undefined,
		loading: false,
		starting: false,
		cancelling: false,
		acknowledgingNotice: false,
		error: undefined as string | undefined,
		acknowledgeNoticeError: undefined as string | undefined,
		fetchFailed: false,
		start: vi.fn(async () => {}),
		cancel: vi.fn(async () => {}),
		acknowledgeNotice: vi.fn(async (_id: string) => {}),
		refresh: vi.fn(async () => undefined),
	});
	return {
		os: "ios" as "ios" | "android",
		params: { id: "worker-1" } as Record<string, string | undefined>,
		config: { host: "dev.local" } as { host: string },
		sessions: [] as Record<string, unknown>[],
		interfaceSwitch: interfaceSwitch(),
		freshInterfaceSwitch: interfaceSwitch,
		mux: undefined as { handlers: Handlers } | undefined,
		xterm: { injectJavaScript: vi.fn(), write: vi.fn(), resize: vi.fn() },
		navigation: { setOptions: vi.fn() },
		router: { push: vi.fn(), back: vi.fn(), replace: vi.fn(), canGoBack: () => true },
		alert: vi.fn(),
		backOr: vi.fn(),
		getPreview: vi.fn(async (): Promise<{ entry: string; url: string } | null> => null),
		killSession: vi.fn(async () => {}),
	};
});

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Alert: { alert: fixtures.alert },
		Keyboard: { addListener: () => ({ remove: () => {} }), dismiss: vi.fn() },
		LayoutAnimation: { configureNext: vi.fn(), Types: { keyboard: "keyboard" } },
		Platform: { get OS() { return fixtures.os; }, Version: "26.0" },
		StyleSheet: { create: (styles: object) => styles, absoluteFill: {}, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	const Toolbar = Object.assign(host("StackToolbar"), {
		Badge: host("StackToolbarBadge"),
		Button: host("StackToolbarButton"),
		Label: host("StackToolbarLabel"),
		Menu: host("StackToolbarMenu"),
		MenuAction: host("StackToolbarMenuAction"),
		View: host("StackToolbarView"),
	});
	return {
		Stack: { Toolbar },
		useLocalSearchParams: () => fixtures.params,
		useNavigation: () => fixtures.navigation,
		useRouter: () => fixtures.router,
	};
});
vi.mock("@fressh/react-native-xtermjs-webview", async () => {
	const React = await import("react");
	return { XtermJsWebView: (props: object) => React.createElement("XtermJsWebView", props) };
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, right: 0, bottom: 0, left: 0 }) }));
vi.mock("../api", () => ({
	ApiError: class ApiError extends Error {},
	getPreview: fixtures.getPreview,
	killSession: fixtures.killSession,
	killSessionReviewer: vi.fn(async () => {}),
	sendMessage: vi.fn(async () => {}),
}));
vi.mock("../config", () => ({ isConfigured: (config: { host: string }) => config.host.length > 0 }));
vi.mock("../theme", () => ({ terminalTheme: () => ({}) }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));
// The real one waits a frame; the screen only needs both steps to have run.
vi.mock("../headerRightSwap", () => ({
	resetHeaderRightForSwap: (clear: () => void, ready: () => void) => {
		clear();
		ready();
		return () => {};
	},
}));
vi.mock("../mux", () => ({
	MuxClient: class {
		handlers: Handlers;
		connect = vi.fn();
		disconnect = vi.fn();
		openTerminal = vi.fn();
		resize = vi.fn();
		sendInput = vi.fn();
		constructor(_config: unknown, handlers: Handlers) {
			this.handlers = handlers;
			fixtures.mux = this;
		}
	},
}));
vi.mock("../native-header-items", () => ({ glassHeaderControl: () => ({}) }));
vi.mock("../store", () => ({
	useApp: () => ({
		sessions: fixtures.sessions,
		orchestrators: [],
		restore: vi.fn(async () => {}),
		refresh: vi.fn(async () => {}),
		config: fixtures.config,
	}),
}));
vi.mock("../voice/useVoiceInput", () => ({ useVoiceInput: () => ({ state: "idle", partial: "", mode: "hold", error: undefined }) }));
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_target, key) => `#${String(key)}` });
	return {
		useTheme: () => theme,
		useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme),
		useThemeState: () => ({ scheme: "dark", style: "graphite" }),
	};
});
vi.mock("../chat/api", () => ({ closeShellTerminal: vi.fn(async () => {}) }));
vi.mock("./useInterfaceTransition", async () => ({
	...(await vi.importActual<typeof import("./interfaceTransition")>("./interfaceTransition")),
	useInterfaceTransition: () => fixtures.interfaceSwitch,
}));
vi.mock("../backNavigation", () => ({ backOr: fixtures.backOr }));
vi.mock("../Spinner", async () => {
	const React = await import("react");
	return { Spinner: (props: object) => React.createElement("Spinner", props) };
});
vi.mock("../AppSymbol", async () => {
	const React = await import("react");
	return { AppSymbol: (props: object) => React.createElement("AppSymbol", props) };
});
vi.mock("../ui", async () => {
	const React = await import("react");
	return {
		Button: (props: object) => React.createElement("Button", props),
		EmptyState: (props: object) => React.createElement("EmptyState", props),
	};
});
vi.mock("./Composer", async () => {
	const React = await import("react");
	return { Composer: (props: object) => React.createElement("Composer", props) };
});
vi.mock("./KeyRow", async () => {
	const React = await import("react");
	return { KeyRow: (props: object) => React.createElement("KeyRow", props) };
});
vi.mock("./ScrollTopButton", async () => {
	const React = await import("react");
	return { ScrollTopButton: (props: object) => React.createElement("ScrollTopButton", props) };
});
vi.mock("./TerminalNotice", async () => {
	const React = await import("react");
	return {
		TerminalNotice: (props: object) => React.createElement("TerminalNotice", props),
		TerminalNoticeHost: (props: object) => React.createElement("TerminalNoticeHost", props),
	};
});
vi.mock("./InterfaceSwitchConfirm", async () => {
	const React = await import("react");
	return { InterfaceSwitchConfirm: (props: object) => React.createElement("InterfaceSwitchConfirm", props) };
});
vi.mock("./NoticeDismissButton", async () => {
	const React = await import("react");
	return { NoticeDismissButton: (props: object) => React.createElement("NoticeDismissButton", props) };
});

import { Stack } from "expo-router";
import TerminalScreen from "./TerminalSessionScreen";
import { ARCHIVE_SESSION_CONFIRM } from "../worker-action-model";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.os = "ios";
	fixtures.params = { id: "worker-1" };
	fixtures.config = { host: "dev.local" };
	fixtures.sessions = [{ id: "worker-1", projectId: "project-1", status: "working", activity: "idle" }];
	fixtures.interfaceSwitch = fixtures.freshInterfaceSwitch();
	fixtures.mux = undefined;
	fixtures.getPreview.mockReset().mockResolvedValue(null);
	fixtures.killSession.mockReset().mockResolvedValue(undefined);
	for (const fn of [fixtures.alert, fixtures.backOr, fixtures.xterm.injectJavaScript, fixtures.navigation.setOptions]) fn.mockReset();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

async function mount() {
	await act(async () => {
		renderer = create(<TerminalScreen />, {
			createNodeMock: (element) => (element.type === "XtermJsWebView" ? fixtures.xterm : null),
		});
	});
}

/** The daemon reports the shared grid, as it does once the terminal is attached. */
async function attachGrid(cols: number, rows: number) {
	await act(async () => fixtures.mux!.handlers.onTerminalResize("worker-1", cols, rows));
}

const find = (type: string) => renderer!.root.findAllByType(type as never);
const toolbarButtons = () => find("StackToolbarButton");
const rootMenu = () => find("StackToolbarMenu").find((menu) => menu.props.accessibilityLabel === "Terminal actions");
const elements = (children: unknown): ReactElement<Record<string, unknown>>[] =>
	Array.isArray(children)
		? children.flatMap(elements)
		: isValidElement<Record<string, unknown>>(children) ? [children] : [];
const menuText = (items: ReactElement<Record<string, unknown>>[]) => items.map((item) => (item.props.inline ? elements(item.props.children).map((inner) => inner.props.children) : item.props.children));
const action = (menu: ReactTestInstance, label: string) =>
	elements(menu.props.children)
		.flatMap((item) => (item.props.inline ? elements(item.props.children) : [item]))
		.find((item) => item.props.children === label)!;

describe("the terminal screen's header on iOS", () => {
	it("puts Chat, preview and files in the bar and everything else in one menu", async () => {
		await mount();
		await attachGrid(120, 40);
		expect(toolbarButtons().map((button) => [button.props.icon, button.props.accessibilityLabel])).toEqual([
			["bubble.left", "Open Chat interface"],
			["globe", "Open preview"],
			["doc.text.magnifyingglass", "Session files and changes"],
		]);
		const menu = rootMenu()!;
		expect(menu.props).toMatchObject({ icon: "ellipsis.circle", title: "Grid 120 × 40" });
		// The bar button is named for what it opens; the grid only heads the menu.
		expect(menuText(elements(menu.props.children))).toEqual([
			"Terminal actions",
			"Smaller",
			"Larger",
			"Fit width",
			["Archive session"],
		]);
		expect(elements(menu.props.children)[0].type).toBe(Stack.Toolbar.Label);
		expect(action(menu, "Archive session").props).toMatchObject({ destructive: true, icon: "archivebox" });
	});

	it("zooms only the phone's viewport from the menu", async () => {
		await mount();
		await attachGrid(120, 40);
		act(() => (action(rootMenu()!, "Smaller").props.onPress as () => void)());
		act(() => (action(rootMenu()!, "Larger").props.onPress as () => void)());
		act(() => (action(rootMenu()!, "Fit width").props.onPress as () => void)());
		expect(fixtures.xterm.injectJavaScript.mock.calls.map(([script]) => script)).toEqual([
			"window.__aoAdjustTerminalZoom(-1); true;",
			"window.__aoAdjustTerminalZoom(1); true;",
			"window.__aoFitTerminalWidth(); true;",
		]);
	});

	it("fills the globe in, not only colours it, once a preview is ready", async () => {
		fixtures.getPreview.mockResolvedValue({ entry: "dist/index.html", url: "http://dev.local/preview" });
		await mount();
		const globe = toolbarButtons()[1];
		expect(globe.props).toMatchObject({ icon: "globe.americas.fill", tintColor: "#green", accessibilityLabel: "Open preview, preview ready" });
		// iOS before 26 draws no badge, and an empty one is a blank capsule after.
		expect(globe.findAllByType("StackToolbarBadge" as never)).toHaveLength(0);
	});

	it("keeps a repo README from reading as a ready preview", async () => {
		fixtures.getPreview.mockResolvedValue({ entry: "README.md", url: "http://dev.local/preview" });
		await mount();
		expect(toolbarButtons()[1].props).toMatchObject({ icon: "globe", tintColor: undefined, accessibilityLabel: "Open preview" });
	});

	it("stands a labelled spinner in for the Chat switch while it checks", async () => {
		fixtures.interfaceSwitch.starting = true;
		await mount();
		expect(toolbarButtons().map((button) => button.props.icon)).toEqual(["globe", "doc.text.magnifyingglass"]);
		const slot = find("StackToolbarView")[0];
		expect(slot.props.hidesSharedBackground).toBe(true);
		expect(slot.findByType("Spinner" as never).props.accessibilityLabel).toBe("Checking whether Chat can open");
	});
});

describe("the terminal screen's header on Android", () => {
	it("lists the grid as an inert first row and sections the rest", async () => {
		fixtures.os = "android";
		await mount();
		await attachGrid(120, 40);
		const menu = rootMenu()!;
		expect(menu.props.title).toBeUndefined();
		expect(menuText(elements(menu.props.children))).toEqual(["Grid 120 × 40", ["Smaller", "Larger", "Fit width"], ["Archive session"]]);
		expect(elements(menu.props.children)[0].props.disabled).toBe(true);
		expect(action(menu, "Smaller").props.icon).toBeUndefined();
	});

	it("marks a ready preview with the badge dot", async () => {
		fixtures.os = "android";
		fixtures.getPreview.mockResolvedValue({ entry: "dist/index.html", url: "http://dev.local/preview" });
		await mount();
		const globe = toolbarButtons()[1];
		expect(globe.props.tintColor).toBeUndefined();
		expect(globe.findByType("StackToolbarBadge" as never).props.style).toEqual({ backgroundColor: "#green" });
	});
});

describe("the terminal screen without a paired machine", () => {
	it("offers no menu, since there is nothing to zoom or archive", async () => {
		fixtures.config = { host: "" };
		await mount();
		expect(find("EmptyState")[0].props).toMatchObject({ icon: "devices", title: "No machine paired" });
		expect(find("StackToolbarMenu")).toHaveLength(0);
		expect(find("TerminalNotice")).toHaveLength(0);
		expect(fixtures.mux).toBeUndefined();
	});
});

describe("the terminal screen's notices", () => {
	it("reports a failed archive on the notice row", async () => {
		fixtures.killSession.mockRejectedValue(new Error("The daemon refused."));
		await mount();
		await attachGrid(120, 40);
		act(() => (action(rootMenu()!, "Archive session").props.onPress as () => void)());
		const [title, , buttons] = fixtures.alert.mock.calls[0] as [string, string, { style: string; onPress?: () => Promise<void> }[]];
		expect(title).toBe(ARCHIVE_SESSION_CONFIRM.title);
		await act(async () => buttons.find((button) => button.style === "destructive")!.onPress!());
		expect(find("TerminalNotice")[0].props.notice).toMatchObject({ text: "Couldn't archive the session. The daemon refused.", tone: "error" });
		expect(find("TerminalNoticeHost")[0].props.notice).toMatchObject({ tone: "error" });
		expect(fixtures.backOr).not.toHaveBeenCalled();
	});

	it("names the interface-switch notice's close button and shows it busy", async () => {
		fixtures.interfaceSwitch.transition = { id: "transition-1", phase: "failed", errorDetail: "Chat could not start." };
		fixtures.interfaceSwitch.acknowledgingNotice = true;
		await mount();
		const close = find("NoticeDismissButton")[0];
		expect(close.props).toMatchObject({ accessibilityLabel: "Dismiss interface switch error", busy: true });
		act(() => (close.props.onPress as () => void)());
		expect(fixtures.interfaceSwitch.acknowledgeNotice).toHaveBeenCalledWith("transition-1");
	});
});

describe("the terminal grid's overlays", () => {
	const webView = () => find("XtermJsWebView")[0];

	it("floats the scroll-to-top button while the WebView says there is older output", async () => {
		await mount();
		expect(find("ScrollTopButton")).toHaveLength(0);
		act(() => webView().props.logger.log("debug", "AO_SCROLLTOP 1"));
		act(() => find("ScrollTopButton")[0].props.onPress());
		expect(fixtures.xterm.injectJavaScript).toHaveBeenCalledWith("window.__aoScrollTop(); true;");
		act(() => webView().props.logger.log("debug", "AO_SCROLLTOP 0"));
		expect(find("ScrollTopButton")).toHaveLength(0);
	});

	it("keeps explaining a crashed terminal view, with a way out", async () => {
		await mount();
		act(() => webView().props.logger.log("debug", "AO_SCROLLTOP 1"));
		act(() => webView().props.webViewOptions.onRenderProcessGone());
		const alert = find("View").find((view) => view.props.accessibilityRole === "alert")!;
		const state = alert.findByType("EmptyState" as never);
		expect(state.props).toMatchObject({ icon: "warning", iconColor: "#red", title: "The terminal view stopped working" });
		expect(find("ScrollTopButton")).toHaveLength(0);
		expect(find("TerminalNotice")[0].props.notice).toBeNull();
		act(() => (state.props.action as ReactElement<{ title: string; onPress: () => void }>).props.onPress());
		expect(fixtures.backOr).toHaveBeenCalledWith(fixtures.router);
	});
});
