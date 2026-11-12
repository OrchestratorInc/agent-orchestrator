import { createElement, type ReactNode } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "./api";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios" },
	pathname: "/",
	sessions: [] as DashboardSession[],
	tabRenders: 0,
	theme: new Proxy({}, { get: (_target, property) => `#${String(property)}` }),
	push: vi.fn(),
	navigate: vi.fn(),
	router: undefined as unknown as { push: (href: string) => void; navigate: (href: string) => void },
}));
// One router for the whole run, as expo-router's own is stable.
fixtures.router = { push: (href: string) => fixtures.push(href), navigate: (href: string) => fixtures.navigate(href) };

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("react-native", () => ({
	Platform: fixtures.platform,
	StyleSheet: { create: <T,>(styles: T) => styles },
	View: ({ children, ...props }: { children?: ReactNode }) => createElement("View", props, children),
}));
vi.mock("./BoardTabBar", () => ({ BoardTabBar: (props: object) => createElement("BoardTabBar", props) }));
const reselect = vi.hoisted(() => vi.fn());
vi.mock("./boardTabReselect", () => ({ reselectBoardTab: reselect }));
vi.mock("expo-router/unstable-native-tabs", () => {
	const host = (name: string) => ({ children, ...props }: { children?: ReactNode }) => createElement(name, props, children);
	const NativeTabs = Object.assign(({ children, ...props }: { children?: ReactNode }) => {
		fixtures.tabRenders++;
		return createElement("NativeTabs", props, children);
	}, {
		Trigger: Object.assign(host("Trigger"), { Icon: host("TriggerIcon"), Label: host("TriggerLabel"), Badge: host("TriggerBadge") }),
	});
	return { NativeTabs };
});
vi.mock("expo-router", () => ({ useRouter: () => fixtures.router, usePathname: () => fixtures.pathname }));
vi.mock("./store", () => ({ useApp: () => ({ allSessions: fixtures.sessions }) }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme }));

import BoardTabsLayout from "../app/(tabs)/_layout";

function session(id: string, status: string): DashboardSession {
	return { id, projectId: "p", status, lastActivityAt: "2026-10-26T00:00:00Z" } as DashboardSession;
}

let renderer: ReactTestRenderer | undefined;
function render() {
	act(() => { renderer = create(<BoardTabsLayout />); });
	return renderer!;
}
/** The board's own tabs, Workers and Projects, on both platforms. */
function triggers() {
	return renderer!.root.findAllByType("Trigger" as never).filter((trigger) => trigger.props.name !== "compose");
}

function bar() {
	return renderer!.root.findByType("BoardTabBar" as never);
}

beforeEach(() => {
	fixtures.platform.OS = "ios";
	fixtures.pathname = "/";
	fixtures.sessions = [];
	fixtures.tabRenders = 0;
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

describe("board tabs", () => {
	// Pull Requests opens from the board's header instead, as a page.
	it("lays out Workers and Projects as native tabs", () => {
		render();
		expect(triggers().map((trigger) => trigger.props.name)).toEqual(["(workers)", "(projects)"]);
		expect(triggers().map((trigger) => trigger.findByType("TriggerLabel" as never).props.children)).toEqual(["Workers", "Projects"]);
	});

	// Pull Requests and Settings open from the header on both platforms, so
	// the board has no tabs for them.
	it("has no Pull Requests or Settings tab", () => {
		render();
		expect(renderer!.root.findAllByType("Trigger" as never).map((trigger) => trigger.props.name)).toEqual(["(workers)", "(projects)", "compose"]);
	});

	// iOS's bar is the system's own, with New task set apart at its end.
	it("draws no bar of its own on iOS", () => {
		render();
		expect(renderer!.root.findByType("NativeTabs" as never).props.hidden).toBe(false);
		expect(renderer!.root.findAllByType("BoardTabBar" as never)).toHaveLength(0);
	});

	// Material's bar shares the whole width out between its items, so Android
	// hides it and floats the board's own bar, laid out as iOS's is.
	it("hides Material's bar on Android and floats the board's own", () => {
		fixtures.platform.OS = "android";
		fixtures.sessions = [session("a", "needs_input")];
		render();
		expect(renderer!.root.findByType("NativeTabs" as never).props.hidden).toBe(true);
		expect(bar().props).toMatchObject({ selected: "(workers)", waiting: 1 });
		act(() => bar().props.onSelect("(projects)"));
		expect(fixtures.navigate).toHaveBeenCalledWith("/projects");
		act(() => bar().props.onNewTask());
		expect(fixtures.push).toHaveBeenCalledWith("/spawn");
	});

	// Tapping the tab on screen again scrolls its list to the top, as iOS's bar
	// does natively, rather than navigating to where it already is.
	it("scrolls the selected tab to the top when it is tapped again on Android", () => {
		fixtures.platform.OS = "android";
		fixtures.navigate.mockClear();
		reselect.mockClear();
		render();
		act(() => bar().props.onSelect("(workers)"));
		expect(reselect).toHaveBeenCalledWith("(workers)");
		expect(fixtures.navigate).not.toHaveBeenCalled();
	});

	// A page pushed over the board covers the bar; the tab under it stays
	// selected rather than falling back to Workers.
	it("keeps the board's tab selected under a pushed page", () => {
		fixtures.platform.OS = "android";
		fixtures.pathname = "/projects";
		render();
		expect(bar().props.selected).toBe("(projects)");
		fixtures.pathname = "/project/ao";
		act(() => renderer!.update(<BoardTabsLayout />));
		expect(bar().props.selected).toBe("(projects)");
		fixtures.pathname = "/";
		act(() => renderer!.update(<BoardTabsLayout />));
		expect(bar().props.selected).toBe("(workers)");
	});

	// Workers waiting on a person: the one count worth lighting a tab for.
	it("badges Workers with the workers that need you, and nothing else", () => {
		fixtures.sessions = [session("a", "needs_input"), session("b", "stuck"), session("c", "working")];
		render();
		const badges = triggers().map((trigger) => trigger.findAllByType("TriggerBadge" as never).map((badge) => badge.props.children));
		expect(badges).toEqual([["2"], []]);
	});

	// New task in the tab bar's own row on iOS: the search role's item, which
	// iOS 26 sets apart as a round button. Disabled, so a press opens the
	// spawn sheet rather than selecting it; Android never shows it.
	it.each(["ios", "android"])("sets New task apart in the tab bar on %s", (os) => {
		fixtures.platform.OS = os;
		render();
		const compose = renderer!.root.findAllByType("Trigger" as never).find((trigger) => trigger.props.name === "compose")!;
		expect(compose.props).toMatchObject({ role: "search", disabled: true, hidden: os !== "ios", accessibilityLabel: "New task" });
		expect(compose.findByType("TriggerLabel" as never).props.children).toBe("New task");
		act(() => compose.props.listeners.tabPress());
		expect(fixtures.push).toHaveBeenCalledWith("/spawn");
		fixtures.push.mockClear();
	});

	// The store changes on every poll; rebuilding the tabs each time would hand
	// the navigator new options it has to diff.
	it("keeps the tabs still across polls that leave the badge alone", () => {
		fixtures.sessions = [session("a", "needs_input")];
		render();
		const before = fixtures.tabRenders;
		fixtures.sessions = [session("a", "needs_input"), session("b", "working")];
		act(() => renderer!.update(<BoardTabsLayout />));
		expect(fixtures.tabRenders).toBe(before);
		fixtures.sessions = [session("a", "needs_input"), session("b", "needs_input")];
		act(() => renderer!.update(<BoardTabsLayout />));
		expect(fixtures.tabRenders).toBe(before + 1);
	});
});
