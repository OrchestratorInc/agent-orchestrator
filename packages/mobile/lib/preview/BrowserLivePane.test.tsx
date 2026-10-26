import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { BrowserLiveState } from "../browserLive";
import { space } from "../tokens";

type Handlers = {
	onStatus(status: "connecting" | "open" | "closed" | "error", message?: string): void;
	onState(state: BrowserLiveState): void;
	onFrame(frame: { sequence: bigint; capturedAtMs: bigint; width: number; height: number; jpeg: ArrayBuffer }): void;
	onDisabled?(): void;
};

const fixtures = vi.hoisted(() => ({
	config: { host: "desktop.local", httpPort: "3011", muxPort: "3011", password: "test" },
	platform: { OS: "android" },
	clients: [] as Array<{ handlers: Handlers; sent: unknown[]; closed: boolean }>,
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Image: host("Image"), View: host("View"),
		Platform: fixtures.platform,
		StyleSheet: { create: (styles: unknown) => styles, absoluteFill: { position: "absolute", top: 0, right: 0, bottom: 0, left: 0 } },
	};
});
vi.mock("react-native-gesture-handler", async () => {
	const React = await import("react");
	// Each builder records what it was configured with, so a test can run the
	// gesture's callbacks as RNGH would.
	const builder = (kind: string) => {
		const config: Record<string, unknown> = { kind };
		const gesture: Record<string, unknown> = new Proxy(config, {
			get: (target, prop: string) => prop in target ? target[prop] : (value: unknown) => { target[prop] = value; return gesture; },
		});
		return gesture;
	};
	return {
		Gesture: { Tap: () => builder("tap"), Pan: () => builder("pan"), Pinch: () => builder("pinch"), Race: (...gestures: unknown[]) => ({ race: gestures }) },
		GestureDetector: ({ gesture, children }: { gesture: unknown; children: React.ReactNode }) => React.createElement("GestureDetector", { gesture }, children),
	};
});
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	return {
		default: { View: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("AnimatedView", props, children) },
		useSharedValue: (initial: unknown) => React.useRef({ value: initial }).current,
		useAnimatedStyle: (style: () => unknown) => style(),
		withTiming: (value: unknown) => value,
	};
});
vi.mock("react-native-worklets", () => ({ scheduleOnRN: (run: (...args: unknown[]) => void, ...args: unknown[]) => run(...args) }));
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
				Spacer: host("StackToolbarSpacer"),
			}),
		},
	};
});
vi.mock("../browserLive", () => ({
	BrowserLiveClient: class {
		readonly record: { handlers: Handlers; sent: unknown[]; closed: boolean };
		constructor(_config: unknown, _sessionID: string, handlers: Handlers) {
			this.record = { handlers, sent: [], closed: false };
			fixtures.clients.push(this.record);
		}
		connect() { this.record.handlers.onStatus("connecting"); }
		send(command: unknown) { this.record.sent.push(command); }
		close() { this.record.closed = true; }
	},
}));
vi.mock("../store", () => ({ useApp: () => ({ config: fixtures.config }) }));
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_target, name) => `theme.${String(name)}` });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});
vi.mock("../useReducedMotion", () => ({ useReducedMotion: () => false }));
vi.mock("../Toast", async () => {
	const React = await import("react");
	return { ToastHost: (props: object) => React.createElement("ToastHost", props) };
});
vi.mock("../ui", async () => {
	const React = await import("react");
	return {
		Button: (props: object) => React.createElement("Button", props),
		EmptyState: ({ action, ...props }: { action?: React.ReactNode }) => React.createElement("EmptyState", props, action),
	};
});
vi.mock("./LoadingBar", async () => {
	const React = await import("react");
	return { LoadingBar: (props: object) => React.createElement("LoadingBar", props) };
});
vi.mock("./OpenURLPrompt", async () => {
	const React = await import("react");
	return { OpenURLPrompt: (props: object) => React.createElement("OpenURLPrompt", props) };
});

import { BrowserLivePane } from "./BrowserLivePane";

const page: BrowserLiveState = {
	url: "http://localhost:3000/",
	title: "Todo app",
	loading: false,
	canGoBack: true,
	canGoForward: false,
	activeTabId: "t1",
	tabs: [
		{ id: "t1", title: "Todo app", url: "http://localhost:3000/", active: true },
		{ id: "t2", title: "", url: "http://localhost:6006/", active: false },
	],
	width: 1280,
	height: 800,
};

let screen: ReactTestRenderer;
const onTitle = vi.fn();
const headerItem = <span key="mode">view menu</span>;
const nodes = (name: string) => screen.root.findAll((node) => (node.type as unknown) === name);
const client = () => fixtures.clients.at(-1)!;
const sent = () => client().sent;

function bottomToolbar() {
	const [toolbar] = nodes("StackToolbar").filter((node) => node.props.placement === undefined);
	return toolbar;
}

function toolbarButton(label: string): ReactTestInstance {
	return bottomToolbar().find((node) => (node.type as unknown) === "StackToolbarButton" && node.props.accessibilityLabel === label);
}

function menuAction(within: ReactTestInstance, label: string): ReactTestInstance {
	return within.find((node) => (node.type as unknown) === "StackToolbarMenuAction" && node.props.children === label);
}

function tabsMenu() {
	return bottomToolbar().find((node) => (node.type as unknown) === "StackToolbarMenu" && String(node.props.accessibilityLabel).startsWith("Tabs"));
}

type GestureConfig = Record<string, ((...args: never[]) => void) | unknown>;
function gesture(kind: "tap" | "pan" | "pinch"): Record<string, (...args: unknown[]) => void> & GestureConfig {
	const race = nodes("GestureDetector")[0].props.gesture.race as GestureConfig[];
	return race.find((entry) => entry.kind === kind) as never;
}

async function render() {
	await act(async () => { screen = create(<BrowserLivePane sessionID="session-1" onTitle={onTitle} headerItem={headerItem} />); });
}

async function open(state: BrowserLiveState = page) {
	await act(async () => {
		client().handlers.onStatus("open");
		client().handlers.onState(state);
	});
}

/** A 1280×800 frame in a 390×600 viewport: fitted 390×243.75 at the top of 178.125. */
async function showFrame() {
	await act(async () => {
		nodes("View").find((node) => typeof node.props.onLayout === "function")!.props.onLayout({ nativeEvent: { layout: { width: 390, height: 600 } } });
		client().handlers.onFrame({ sequence: 1n, capturedAtMs: 1n, width: 1280, height: 800, jpeg: Uint8Array.from([0xff, 0xd8, 0xff]).buffer });
	});
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.platform.OS = "android";
	fixtures.clients.length = 0;
	onTitle.mockClear();
});

afterEach(() => {
	act(() => { screen?.unmount(); });
	vi.unstubAllGlobals();
});

describe("live browser controls", () => {
	it("draws Back, Forward, Reload and the tabs in Android's floating bottom toolbar from the desktop's state", async () => {
		await render();
		await open();
		const toolbar = bottomToolbar();
		expect(toolbar.props).toMatchObject({ tintColor: "theme.accent", backgroundColor: "theme.bgElevated" });
		expect(toolbarButton("Back").props).toMatchObject({ icon: "chevron.backward", disabled: false });
		expect(toolbarButton("Forward").props).toMatchObject({ icon: "chevron.forward", disabled: true });
		expect(toolbarButton("Reload").props).toMatchObject({ icon: "arrow.clockwise", disabled: false });
		expect(tabsMenu().props).toMatchObject({ icon: "square.on.square", accessibilityLabel: "Tabs, 2 open, showing Todo app" });
		// Android draws a spacer only with a fixed width; one without is dropped.
		const gaps = toolbar.findAll((node) => (node.type as unknown) === "StackToolbarSpacer");
		expect(gaps.map((gap) => gap.props.width)).toEqual([space.md, space.md]);
		await act(async () => toolbarButton("Back").props.onPress());
		await act(async () => toolbarButton("Reload").props.onPress());
		expect(sent()).toEqual([
			{ type: "navigate", payload: { action: "back" } },
			{ type: "navigate", payload: { action: "reload" } },
		]);
	});

	it("turns Reload into Stop and shows a native progress bar while a page loads", async () => {
		await render();
		await open({ ...page, loading: true });
		expect(nodes("LoadingBar")[0].props).toMatchObject({ loading: true, accessibilityLabel: "Loading page" });
		expect(nodes("LoadingBar")[0].props.progress).toBeUndefined();
		await act(async () => toolbarButton("Stop loading").props.onPress());
		expect(toolbarButton("Stop loading").props.icon).toBe("xmark");
		expect(sent()).toEqual([{ type: "navigate", payload: { action: "stop" } }]);
		await open();
		expect(nodes("LoadingBar")[0].props.loading).toBe(false);
	});

	it("lists the tabs with the showing one checked, and opens, switches and closes them", async () => {
		await render();
		await open();
		const menu = tabsMenu();
		expect(menuAction(menu, "Todo app").props.isOn).toBe(true);
		const storybook = menuAction(menu, "http://localhost:6006/");
		expect(storybook.props.isOn).toBe(false);
		await act(async () => storybook.props.onPress());
		await act(async () => menuAction(menu, "New tab").props.onPress());
		await act(async () => menuAction(menu, "Close this tab").props.onPress());
		expect(sent()).toEqual([
			{ type: "tab", payload: { action: "select", tabId: "t2" } },
			{ type: "tab", payload: { action: "new" } },
			{ type: "tab", payload: { action: "close", tabId: "t1" } },
		]);
	});

	it("names the showing tab in the menu's label, which TalkBack reads in place of the check", async () => {
		await render();
		await open({ ...page, activeTabId: "t2", tabs: page.tabs.map((tab) => ({ ...tab, active: tab.id === "t2" })) });
		expect(tabsMenu().props.accessibilityLabel).toBe("Tabs, 2 open, showing http://localhost:6006/");
		await open({ ...page, tabs: [] });
		expect(tabsMenu().props.accessibilityLabel).toBe("Tabs, 0 open");
	});

	it("opens an address typed into the system prompt", async () => {
		await render();
		await open();
		expect(nodes("OpenURLPrompt")[0].props.visible).toBe(false);
		await act(async () => menuAction(tabsMenu(), "Open URL…").props.onPress());
		const prompt = nodes("OpenURLPrompt")[0];
		expect(prompt.props.visible).toBe(true);
		await act(async () => {
			prompt.props.onDismiss();
			prompt.props.onSubmit("localhost:5173");
		});
		expect(nodes("OpenURLPrompt")[0].props.visible).toBe(false);
		expect(sent()).toEqual([{ type: "navigate", payload: { action: "open", url: "localhost:5173" } }]);
	});

	it("disables the controls until the stream opens", async () => {
		await render();
		expect(toolbarButton("Back").props.disabled).toBe(true);
		expect(toolbarButton("Reload").props.disabled).toBe(true);
		expect(tabsMenu().props.disabled).toBe(true);
	});

	it("ends the header with the page's own item", async () => {
		await render();
		await open();
		const [header] = nodes("StackToolbar").filter((node) => node.props.placement === "right");
		expect(header.findAll((node) => (node.type as unknown) === "StackToolbarMenu")).toHaveLength(0);
		expect(header.findByType("span" as never).props.children).toBe("view menu");
	});

	it("gives iOS the controls as one header menu, led by a row for history and reload", async () => {
		fixtures.platform.OS = "ios";
		await render();
		await open();
		expect(bottomToolbar()).toBeUndefined();
		const [header] = nodes("StackToolbar").filter((node) => node.props.placement === "right");
		const menu = header.find((node) => (node.type as unknown) === "StackToolbarMenu" && node.props.accessibilityLabel === "Browser controls");
		expect(menu.props.icon).toBe("ellipsis.circle");
		const palette = menu.find((node) => (node.type as unknown) === "StackToolbarMenu" && node.props.palette === true);
		expect(palette.findAll((node) => (node.type as unknown) === "StackToolbarMenuAction").map((action) => action.props.children)).toEqual(["Back", "Forward", "Reload"]);
		expect(menuAction(menu, "Forward").props.disabled).toBe(true);
		expect(menuAction(menu, "Todo app").props.isOn).toBe(true);
		await act(async () => menuAction(menu, "Back").props.onPress());
		await act(async () => menuAction(menu, "Open URL…").props.onPress());
		expect(sent()).toEqual([{ type: "navigate", payload: { action: "back" } }]);
		expect(nodes("OpenURLPrompt")[0].props.visible).toBe(true);
		// The page's own item still ends the bar.
		expect(header.findByType("span" as never).props.children).toBe("view menu");
		expect(nodes("ToastHost")[0].props.bottomOffset).toBeUndefined();
	});

	it("keeps Android's disabled glyphs visibly dimmed", async () => {
		await render();
		await open();
		expect(toolbarButton("Back").props.tintColor).toBe("theme.accent");
		expect(toolbarButton("Forward").props.tintColor).toBe("theme.textFaint");
	});

	it("titles the bar with the desktop's page while one shows", async () => {
		await render();
		expect(onTitle).toHaveBeenLastCalledWith(undefined);
		await open();
		expect(onTitle).toHaveBeenLastCalledWith("Todo app");
		await open({ ...page, url: "", title: "" });
		expect(onTitle).toHaveBeenLastCalledWith(undefined);
		await open();
		act(() => screen.unmount());
		expect(onTitle).toHaveBeenLastCalledWith(undefined);
	});
});

describe("live browser states", () => {
	it("shows a refused command as a toast and keeps the page up", async () => {
		await render();
		await open();
		await act(async () => client().handlers.onStatus("open", "Desktop browser control is active"));
		expect(nodes("ToastHost")[0].props.toast.current.text).toBe("Desktop browser control is active");
		// Above the bottom toolbar: the safe area, the bar, and a margin.
		expect(nodes("ToastHost")[0].props.bottomOffset).toBe(34 + 64 + 12);
		expect(screen.root.findAll((node) => node.props.accessibilityRole === "alert")).toHaveLength(0);
	});

	it("announces a failed connection with a retry that reconnects", async () => {
		await render();
		await act(async () => client().handlers.onStatus("error", "Another phone is controlling this browser."));
		const alert = screen.root.find((node) => node.props.accessibilityRole === "alert");
		const empty = alert.findByType("EmptyState" as never);
		expect(empty.props).toMatchObject({ icon: "warning", iconColor: "theme.red", title: "Couldn't connect to the browser", message: "Another phone is controlling this browser." });
		await act(async () => alert.findByType("Button" as never).props.onPress());
		expect(fixtures.clients).toHaveLength(2);
		expect(fixtures.clients[0].closed).toBe(true);
	});

	it("offers Open URL when nothing is open on the desktop", async () => {
		await render();
		await open({ ...page, url: "", title: "", tabs: [] });
		const empty = nodes("EmptyState")[0];
		expect(empty.props).toMatchObject({ icon: "desktop", title: "Nothing is open in the desktop browser yet" });
		await act(async () => empty.findByType("Button" as never).props.onPress());
		expect(nodes("OpenURLPrompt")[0].props.visible).toBe(true);
	});
});

describe("live browser gestures", () => {
	it("stays still until there is a frame to touch", async () => {
		await render();
		await open();
		expect(gesture("tap").enabled).toBe(false);
		await showFrame();
		expect(gesture("tap").enabled).toBe(true);
		expect(gesture("pan").enabled).toBe(true);
		expect(gesture("pinch").enabled).toBe(true);
		const frame = nodes("GestureDetector")[0].findByType("View" as never);
		expect(frame.props).toMatchObject({ accessible: true, accessibilityRole: "image", accessibilityLabel: "Desktop browser, Todo app" });
	});

	it("clicks the page where a tap lands, and ignores the letterbox", async () => {
		await render();
		await open();
		await showFrame();
		await act(async () => gesture("tap").onEnd({ x: 195, y: 300 }, true));
		await act(async () => gesture("tap").onEnd({ x: 195, y: 20 }, true));
		await act(async () => gesture("tap").onEnd({ x: 195, y: 300 }, false));
		expect(sent()).toEqual([
			{ type: "input", payload: { kind: "pointer", phase: "down", x: 0.5, y: 0.5, button: "left" } },
			{ type: "input", payload: { kind: "pointer", phase: "up", x: 0.5, y: 0.5, button: "left" } },
		]);
	});

	it("scrolls with one finger and maps taps and drags through a pinch zoom", async () => {
		await render();
		await open();
		await showFrame();
		const fitted = 390 / 1280;
		await act(async () => gesture("pan").onChange({ changeX: 0, changeY: -30 }));
		expect(sent()).toEqual([{ type: "input", payload: { kind: "wheel", deltaX: 0, deltaY: -30 / fitted } }]);
		client().sent.length = 0;

		// Double the frame about the viewport's centre.
		await act(async () => {
			gesture("pinch").onStart({ focalX: 195, focalY: 300 });
			gesture("pinch").onUpdate({ focalX: 195, focalY: 300, scale: 2 });
			gesture("pinch").onEnd();
		});
		await act(async () => gesture("pan").onChange({ changeX: 0, changeY: -30 }));
		await act(async () => gesture("tap").onEnd({ x: 195 + 97.5, y: 300 }, true));
		expect(sent()[0]).toEqual({ type: "input", payload: { kind: "wheel", deltaX: 0, deltaY: -30 / (fitted * 2) } });
		expect(sent()[1]).toEqual({ type: "input", payload: { kind: "pointer", phase: "down", x: 0.625, y: 0.5, button: "left" } });
	});

	it("marks where a finger touched", async () => {
		await render();
		await open();
		await showFrame();
		await act(async () => gesture("tap").onBegin({ x: 40, y: 80 }));
		await act(async () => { screen.update(<BrowserLivePane sessionID="session-1" onTitle={onTitle} headerItem={headerItem} />); });
		const mark = nodes("AnimatedView").find((node) => node.props.style.some?.((style: { opacity?: number }) => style?.opacity === 1));
		expect(mark).toBeDefined();
	});
});
