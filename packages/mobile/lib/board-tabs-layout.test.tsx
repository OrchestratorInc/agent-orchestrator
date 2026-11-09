import { createElement, type ReactNode } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "./api";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios" },
	fontScale: 1,
	sessions: [] as DashboardSession[],
	tabRenders: 0,
	theme: new Proxy({}, { get: (_target, property) => `#${String(property)}` }),
	push: vi.fn(),
	router: undefined as unknown as { push: (href: string) => void },
}));
// One router for the whole run, as expo-router's own is stable.
fixtures.router = { push: (href: string) => fixtures.push(href) };

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("react-native", () => ({ Platform: fixtures.platform, useWindowDimensions: () => ({ fontScale: fixtures.fontScale }) }));
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

function session(id: string, status: string): DashboardSession {
	return { id, projectId: "p", status, lastActivityAt: "2026-10-26T00:00:00Z" } as DashboardSession;
}

let renderer: ReactTestRenderer | undefined;
function render() {
	act(() => { renderer = create(<BoardTabsLayout />); });
	return renderer!;
}
/** The three board tabs, without the New task button set apart beside them. */
const ANDROID_TABS = ["(pull-requests)", "(preferences)"];

/** The board's own tabs, Workers and Projects, on both platforms. */
function triggers() {
	return renderer!.root.findAllByType("Trigger" as never).filter((trigger) => trigger.props.name !== "compose" && !ANDROID_TABS.includes(trigger.props.name));
}

function trigger(name: string) {
	return renderer!.root.findAllByType("Trigger" as never).find((node) => node.props.name === name)!;
}

beforeEach(() => {
	fixtures.platform.OS = "ios";
	fixtures.fontScale = 1;
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

	// Material's bar takes up to five destinations, so Android has Pull
	// Requests and Settings there rather than in the top app bar; iOS opens both
	// as sheets from the board's bar and never shows these.
	it.each([["android", false], ["ios", true]])("shows Pull Requests and Settings as tabs on %s: hidden %s", (os, hidden) => {
		fixtures.platform.OS = os;
		render();
		expect(ANDROID_TABS.map((name) => trigger(name).props.hidden)).toEqual([hidden, hidden]);
		expect(ANDROID_TABS.map((name) => trigger(name).findByType("TriggerLabel" as never).props.children)).toEqual(["Pull Requests", "Settings"]);
	});

	// At 2× text Android's four labels ran together ("WorkersProjects"), so past
	// the chrome cap only the selected tab keeps its label.
	it.each([[1, "labeled"], [1.3, "labeled"], [2, "selected"]])("labels the tabs at %s× text as %s", (fontScale, mode) => {
		fixtures.platform.OS = "android";
		fixtures.fontScale = fontScale as number;
		render();
		expect(renderer!.root.findByType("NativeTabs" as never).props.labelVisibilityMode).toBe(mode);
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
