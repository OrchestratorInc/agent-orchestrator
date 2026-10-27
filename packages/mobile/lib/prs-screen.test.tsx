import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { HostedSession } from "./hostedRows";
import type { HostSnapshot } from "./otherHosts";
import type { SessionPRSummary } from "./api";

const fixture = vi.hoisted(() => ({
	allSessions: [] as HostedSession[], hostStates: [] as HostSnapshot[],
	cardRenders: [] as string[], params: {} as { hostId?: string; projectId?: string },
	configForHost: vi.fn(() => ({ host: "ao.test", httpPort: "3011", muxPort: "3011", password: "test" })),
	summaries: new Map<string, SessionPRSummary>(),
	reload: vi.fn(), focus: undefined as (() => void) | undefined, summaryTargets: [] as unknown[][],
	theme: new Proxy({}, { get: () => "#888888" }),
	router: { push: vi.fn(), navigate: vi.fn(), back: vi.fn(), canGoBack: () => true, replace: vi.fn() },
}));
vi.mock("react-native", async () => {
	const React = await import("react");
	return {
		ActivityIndicator: "ActivityIndicator", RefreshControl: "RefreshControl", View: "View", Text: "Text", Platform: { OS: "ios" },
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		Pressable: ({ children, ...props }: { children?: React.ReactNode; accessibilityRole?: string; accessibilityLabel?: string }) => {
			if (props.accessibilityRole === "button" && props.accessibilityLabel?.includes("Pull request")) fixture.cardRenders.push(props.accessibilityLabel);
			return React.createElement("Pressable", props, children);
		},
		SectionList: ({ sections, renderItem, ...props }: { sections: { key: string; data: unknown[] }[]; renderItem: (args: { item: unknown }) => React.ReactNode }) =>
			React.createElement("SectionList", { ...props, sections }, sections.flatMap(section => section.data.map((item, index) => React.createElement(React.Fragment, { key: `${section.key}:${index}` }, renderItem({ item }))))),
	};
});
vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		useRouter: () => fixture.router,
		useLocalSearchParams: () => fixture.params,
		useFocusEffect: (callback: () => void) => { fixture.focus = callback; },
		Stack: { Toolbar: Object.assign(host("StackToolbar"), { Button: host("StackToolbarButton"), Badge: host("StackToolbarBadge") }) },
	};
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0 }) }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {} }));
vi.mock("expo-secure-store", () => ({}));
vi.mock("./installId", () => ({}));
vi.mock("./sentry", () => ({}));
vi.mock("./store", () => ({ useApp: () => ({ ...fixture, activeProjectId: "all", refreshAll: vi.fn() }) }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixture.theme, useThemeState: () => ({ scheme: "dark" }), useThemedStyles: (factory: (theme: object) => object) => factory(fixture.theme) }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn() } }));
vi.mock("./openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("expo-clipboard", () => ({ setStringAsync: vi.fn() }));
vi.mock("@expo/ui/community/menu", async () => {
	const React = await import("react");
	return { MenuView: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("MenuView", props, children) };
});
vi.mock("./Toast", () => ({ ToastHost: "ToastHost" }));
vi.mock("./pageNavigation", () => ({ useOpenPage: () => vi.fn() }));
vi.mock("./pr-filter-control", () => ({ PRFilterControl: "PRFilterControl" }));
vi.mock("./ProjectSwitcher", () => ({ ProjectSwitcher: "ProjectSwitcher" }));
vi.mock("./StaleBanner", () => ({ StaleBanner: "StaleBanner" }));
vi.mock("./UnpairedState", () => ({ UnpairedState: "UnpairedState" }));
vi.mock("./useBoardFailure", () => ({ useBoardFailure: () => ({}) }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));
vi.mock("./usePRSummaries", () => ({ PR_SUMMARY_REUSE_MS: 60_000, usePRSummaries: (targets: unknown[]) => {
	fixture.summaryTargets.push(targets);
	return { summaryFor: (_config: unknown, id: string) => fixture.summaries.get(id), reload: fixture.reload };
} }));
vi.mock("./ui", () => ({ Button: "Button", EmptyState: "EmptyState", ListSectionHeader: "ListSectionHeader" }));

import PRsScreen from "../app/prs/index";
let renderer: ReactTestRenderer | undefined;
function session(hostId: string): HostedSession {
	return {
		id: hostId, hostId, hostName: hostId, projectId: "same-project", displayName: `Task on ${hostId}`,
		status: "idle", mode: "chat", branch: null, issueId: null, issueTitle: null, userPrompt: null, summary: null,
		createdAt: "2026-10-11T00:00:00Z", lastActivityAt: "2026-10-11T00:00:00Z",
		pr: { number: 7, url: "https://github.com/ao/test/pull/7", state: "open" },
	};
}
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.allSessions = [session("mac"), session("windows")];
	fixture.hostStates = fixture.allSessions.map(s => ({
		hostId: s.hostId, name: s.hostName, appearance: { symbol: "desktop" }, endpoints: [], config: null,
		sessions: [s], connection: "open", notificationsUnread: 0, loading: false, error: null, errorStatus: null,
		projects: [], projectsKnown: true, orchestrators: [], orchestratorId: null, stats: {}, lastSyncAt: 0,
	}));
	fixture.cardRenders = []; fixture.params = {}; fixture.summaries.clear();
	fixture.reload.mockClear(); fixture.focus = undefined; fixture.summaryTargets = [];
});
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); vi.restoreAllMocks(); });

// A page now, mounted only when opened, so its details load straight away.
it("loads PR details as the page opens, without reloading them on its first focus", () => {
	act(() => { renderer = create(<PRsScreen />); });
	expect(fixture.summaryTargets.at(-1)).toHaveLength(2);
	act(() => fixture.focus?.());
	expect(fixture.reload).not.toHaveBeenCalled();
});

it("revalidates PR details on a later return but not on quick ones", () => {
	const now = vi.spyOn(Date, "now").mockReturnValue(1_000);
	act(() => { renderer = create(<PRsScreen />); });
	act(() => fixture.focus?.());
	now.mockReturnValue(30_000);
	act(() => fixture.focus?.());
	expect(fixture.reload).not.toHaveBeenCalled();
	now.mockReturnValue(61_001);
	act(() => fixture.focus?.());
	expect(fixture.reload).toHaveBeenCalledTimes(1);
	act(() => fixture.focus?.());
	expect(fixture.reload).toHaveBeenCalledTimes(1);
});

it("does not rebuild PR sections or redraw cards when only host sync/notifications change", () => {
	act(() => { renderer = create(<PRsScreen />); });
	const before = renderer!.root.findByType("SectionList" as never).props.sections;
	fixture.cardRenders = [];
	fixture.hostStates = fixture.hostStates.map(host => ({ ...host, notificationsUnread: 3, lastSyncAt: 100 }));
	act(() => renderer!.update(<PRsScreen />));
	expect(renderer!.root.findByType("SectionList" as never).props.sections).toBe(before);
	expect(fixture.cardRenders).toEqual([]);
});
it("keeps matching PR numbers on different hosts and updates only the changed worker", () => {
	act(() => { renderer = create(<PRsScreen />); });
	expect(fixture.cardRenders).toHaveLength(2);
	fixture.cardRenders = [];
	fixture.allSessions = [{ ...fixture.allSessions[0], displayName: "Updated task" }, fixture.allSessions[1]];
	fixture.hostStates = fixture.hostStates.map((host, index) => ({ ...host, sessions: [fixture.allSessions[index]] }));
	act(() => renderer!.update(<PRsScreen />));
	expect(fixture.cardRenders).toHaveLength(1);
	expect(fixture.cardRenders[0]).toContain("Updated task");
});
it("redraws only the card whose rich summary has loaded", () => {
	act(() => { renderer = create(<PRsScreen />); });
	fixture.cardRenders = [];
	fixture.summaries.set("mac", { number: 7, state: "open", title: "Rich PR title", changedFiles: 0, additions: 0, deletions: 0 } as SessionPRSummary);
	act(() => renderer!.update(<PRsScreen />));
	expect(fixture.cardRenders).toHaveLength(1);
	expect(fixture.cardRenders[0]).toContain("Rich PR title");
});
it("scopes PRs to the requested host and project", () => {
	fixture.params = { hostId: "windows", projectId: "same-project" };
	act(() => { renderer = create(<PRsScreen />); });
	expect(fixture.cardRenders).toHaveLength(1);
	expect(fixture.cardRenders[0]).toContain("windows");
});
it("retains the default PR scroll buffer", () => {
	act(() => { renderer = create(<PRsScreen />); });
	const list = renderer!.root.findByType("SectionList" as never);
	expect(list.props.windowSize).toBeUndefined();
	expect(list.props.maxToRenderPerBatch).toBeUndefined();
	expect(list.props.initialNumToRender).toBeUndefined();
});

// The sheet's bar is clear on iOS 26 and the page is laid out under it, so the
// PR list is the page's first view (after the bar's items, which draw no view
// of their own), and what sat above it now heads the list.
it("makes the PR list the page's first view, inset under the header", () => {
	fixture.hostStates = fixture.hostStates.slice(0, 1);
	act(() => { renderer = create(<PRsScreen />); });
	const screen = renderer!.root.findAllByType("View" as never)[0];
	expect((screen.children[0] as { props: { placement?: string } }).props.placement).toBe("right");
	const first = screen.children[1] as { type: unknown; props: { contentInsetAdjustmentBehavior?: string; ListHeaderComponent?: { props: { children: { type: unknown }[] } } } };
	expect(first.props.contentInsetAdjustmentBehavior).toBe("automatic");
	expect(first.props.ListHeaderComponent?.props.children.map((child) => child.type)).toEqual(["ProjectSwitcher", "PRFilterControl", "StaleBanner"]);
});

// A stand-in scroll view swapped for the list while the sheet came up left
// iOS 26's floating bar over the sheet taking every touch, so the first load
// waits inside the list.
it("loads inside the list rather than in a scroll view standing in for it", () => {
	fixture.allSessions = [];
	fixture.hostStates = fixture.hostStates.map((host) => ({ ...host, sessions: [], loading: true }));
	act(() => { renderer = create(<PRsScreen />); });
	const list = renderer!.root.findByType("SectionList" as never);
	expect(list.props.ListEmptyComponent.props.children.props.accessibilityLabel).toBe("Loading pull requests");
	expect(renderer!.root.findAllByType("TabPageScroll" as never)).toHaveLength(0);
});

// A segmented control heads the list under the title, as each platform places
// one, rather than floating over the list just above the tab bar.
it("heads the list with the Open / Merged / All filter and its counts", () => {
	fixture.allSessions = [
		session("mac"),
		{ ...session("windows"), pr: { number: 8, url: "https://github.com/ao/test/pull/8", state: "merged" } },
	];
	act(() => { renderer = create(<PRsScreen />); });
	type Header = { props: { children: ({ type: unknown; props: { filter: string; counts: object; onChange(filter: string): void } } | null)[] } };
	const list = () => renderer!.root.findByType("SectionList" as never);
	const control = () => (list().props.ListHeaderComponent as Header).props.children.find((child) => child?.type === "PRFilterControl")!;
	expect(control().props.filter).toBe("open");
	expect(control().props.counts).toEqual({ open: 1, merged: 1, all: 2 });
	act(() => control().props.onChange("merged"));
	expect(control().props.filter).toBe("merged");
	expect(fixture.cardRenders.at(-1)).toContain("windows");
	expect(list().props.contentContainerStyle).toEqual({ flexGrow: 1, paddingBottom: 24 });
	expect(renderer!.root.findAllByType("PRFilterDock" as never)).toHaveLength(0);
});

// The refresh control gives each platform's own feedback, and the Retry
// buttons that share this handler buzz once rather than twice.
it("refreshes without a haptic of its own", async () => {
	const { haptics } = await import("./haptics");
	vi.mocked(haptics.tap).mockClear();
	act(() => { renderer = create(<PRsScreen />); });
	const refresh = renderer!.root.findByType("SectionList" as never).props.refreshControl;
	await act(async () => { await refresh.props.onRefresh(); });
	expect(fixture.reload).toHaveBeenCalledTimes(1);
	expect(haptics.tap).not.toHaveBeenCalled();
});

// iOS has no system confirmation for a copy; the page shows one.
it("confirms a copied pull-request link without redrawing the cards", () => {
	act(() => { renderer = create(<PRsScreen />); });
	fixture.cardRenders = [];
	act(() => renderer!.root.findAllByType("MenuView" as never)[0].props.onPressAction({ nativeEvent: { event: "copyLink" } }));
	expect(renderer!.root.findByType("ToastHost" as never).props.toast.current?.text).toBe("Link copied.");
	expect(fixture.cardRenders).toEqual([]);
});

// A sheet over the board on iOS: it closes from its bar, and the board's
// Settings and alerts stay the board's.
it("closes the sheet from its bar instead of carrying the board's items", () => {
	fixture.router.back.mockClear();
	act(() => { renderer = create(<PRsScreen />); });
	const buttons = renderer!.root.findAllByType("StackToolbarButton" as never);
	expect(buttons.map((button) => button.props.accessibilityLabel)).toEqual(["Close pull requests"]);
	act(() => buttons[0].props.onPress());
	expect(fixture.router.back).toHaveBeenCalledTimes(1);
});
