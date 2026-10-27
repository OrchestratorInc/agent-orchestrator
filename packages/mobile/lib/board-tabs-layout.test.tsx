import { createElement, type ReactNode } from "react";
import { createRequire } from "node:module";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "./api";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios" },
	sessions: [] as DashboardSession[],
	tabRenders: 0,
	theme: new Proxy({}, { get: (_target, property) => `#${String(property)}` }),
	push: vi.fn(),
	router: undefined as unknown as { push: (href: string) => void },
}));
// One router for the whole run, as expo-router's own is stable.
fixtures.router = { push: (href: string) => fixtures.push(href) };

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("react-native", () => ({ Platform: fixtures.platform }));
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
vi.mock("expo-router", () => ({ useRouter: () => fixtures.router }));
vi.mock("./store", () => ({ useApp: () => ({ allSessions: fixtures.sessions }) }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme }));

import BoardTabsLayout from "../app/(tabs)/_layout";

// Exercise the installed Expo router, not a fake navigation state machine.
// NativeTabs' router is this one, with expo-router's own params on top.
const { TabRouter } = createRequire(import.meta.url)("expo-router/build/react-navigation/routers/TabRouter.js");

function session(id: string, status: string): DashboardSession {
	return { id, projectId: "p", status, lastActivityAt: "2026-10-26T00:00:00Z" } as DashboardSession;
}

let renderer: ReactTestRenderer | undefined;
function render() {
	act(() => { renderer = create(<BoardTabsLayout />); });
	return renderer!;
}
/** The three board tabs, without the New task button set apart beside them. */
function triggers() {
	return renderer!.root.findAllByType("Trigger" as never).filter((trigger) => trigger.props.name !== "compose");
}

beforeEach(() => {
	fixtures.platform.OS = "ios";
	fixtures.sessions = [];
	fixtures.tabRenders = 0;
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

describe("board tabs", () => {
	it("lays out Workers, Projects and Pull Requests as native tabs with system glyphs", () => {
		render();
		expect(triggers().map((trigger) => trigger.props.name)).toEqual(["(workers)", "(projects)", "(prs)"]);
		expect(triggers().map((trigger) => trigger.findByType("TriggerLabel" as never).props.children)).toEqual(["Workers", "Projects", "Pull Requests"]);
		const [workers, projects, prs] = triggers().map((trigger) => trigger.findByType("TriggerIcon" as never).props);
		expect(workers).toMatchObject({ sf: "square.stack.3d.up.fill", md: "stacks" });
		expect(projects).toMatchObject({ sf: "folder.fill", md: "folder" });
		// SF Symbols has a pull request; Android draws lucide's from a bitmap.
		expect(prs.sf).toBe("arrow.triangle.pull");
		expect(String(prs.src)).toContain("tab-pull-request");
	});

	// Tapping the current tab again scrolls its list to the top, natively.
	it("leaves re-tap to scroll to top on", () => {
		render();
		for (const trigger of triggers()) {
			expect(trigger.props.disableScrollToTop).toBeUndefined();
			expect(trigger.props.disablePopToTop).toBeUndefined();
		}
	});

	it("adapts to the iPad sidebar and gives way on scroll", () => {
		render();
		expect(renderer!.root.findByType("NativeTabs" as never).props).toMatchObject({ sidebarAdaptable: true, minimizeBehavior: "onScrollDown" });
	});

	// Named themes have to look right in the bar too, so its colours are the
	// theme's rather than the system's or Material You's.
	it.each(["ios", "android"])("colours the tab bar from the theme on %s", (os) => {
		fixtures.platform.OS = os;
		render();
		expect(renderer!.root.findByType("NativeTabs" as never).props).toMatchObject({
			tintColor: "#accent",
			iconColor: { default: "#textSecondary", selected: "#accent" },
			labelStyle: { default: { color: "#textSecondary" }, selected: { color: "#accent" } },
			indicatorColor: "#accentTint",
			backgroundColor: os === "android" ? "#bgSurface" : undefined,
		});
	});

	// Any font family on iOS also takes React Native's 14pt default, which makes
	// the bar bigger than the system's, so iOS keeps the system font.
	it.each([["ios", undefined], ["android", "DMSans_500Medium"]])("sets the tab label font on %s", (os, family) => {
		fixtures.platform.OS = os as string;
		render();
		const { labelStyle } = renderer!.root.findByType("NativeTabs" as never).props;
		expect(labelStyle.default.fontFamily).toBe(family);
		expect(labelStyle.selected.fontFamily).toBe(family);
		expect(labelStyle.default.fontSize).toBeUndefined();
	});

	// Workers waiting on a person: the one count worth lighting a tab for.
	it("badges Workers with the workers that need you, and nothing else", () => {
		fixtures.sessions = [session("a", "needs_input"), session("b", "stuck"), session("c", "working")];
		render();
		const badges = triggers().map((trigger) => trigger.findAllByType("TriggerBadge" as never).map((badge) => badge.props.children));
		expect(badges).toEqual([["2"], [], []]);
	});

	// New task in the tab bar's own row on iOS: the search role's item, which
	// iOS 26 sets apart as a round button. Disabled, so a press opens the
	// spawn sheet rather than selecting it; Android never shows it.
	it.each(["ios", "android"])("sets New task apart in the tab bar on %s", (os) => {
		fixtures.platform.OS = os;
		render();
		const compose = renderer!.root.findAllByType("Trigger" as never).find((trigger) => trigger.props.name === "compose")!;
		expect(compose.props).toMatchObject({ role: "search", disabled: true, hidden: os !== "ios", accessibilityLabel: "New task" });
		expect(compose.findByType("TriggerIcon" as never).props).toMatchObject({ sf: "square.and.pencil" });
		act(() => compose.props.listeners.tabPress());
		expect(fixtures.push).toHaveBeenCalledWith("/spawn");
		fixtures.push.mockClear();
	});

	it("shows no badge while nothing is waiting", () => {
		fixtures.sessions = [session("a", "working")];
		render();
		expect(renderer!.root.findAllByType("TriggerBadge" as never)).toHaveLength(0);
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

describe("board tab lifecycle", () => {
	const options = { routeNames: ["(workers)", "(projects)", "(prs)"], routeParamList: {}, routeGetIdList: {} };

	it("keeps route and navigator keys when moving among the three tabs", () => {
		const router = TabRouter({ backBehavior: "initialRoute" });
		let state = router.getInitialState(options);
		const key = state.key;
		const routes = state.routes.map((route: { key: string }) => route.key);
		expect(state.routes[state.index].name).toBe("(workers)");
		for (const name of ["(projects)", "(workers)", "(prs)", "(workers)", "(prs)"]) {
			state = router.getStateForAction(state, { type: "NAVIGATE", payload: { name } }, options);
			expect(state.key).toBe(key);
			expect(state.routes.map((route: { key: string }) => route.key)).toEqual(routes);
			expect(state.routes[state.index].name).toBe(name);
		}
	});

	// NativeTabs' Android default, and Material's: Back from another tab returns
	// to the first, and Back there leaves the board.
	it("goes back to Workers before leaving the board", () => {
		const router = TabRouter({ backBehavior: "initialRoute" });
		let state = router.getStateForAction(router.getInitialState(options), { type: "NAVIGATE", payload: { name: "(prs)" } }, options);
		state = router.getStateForAction(state, { type: "GO_BACK" }, options);
		expect(state.routes[state.index].name).toBe("(workers)");
		expect(router.getStateForAction(state, { type: "GO_BACK" }, options)).toBeNull();
	});
});
