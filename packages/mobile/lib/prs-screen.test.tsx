import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { HostedSession } from "./hostedRows";
import type { HostSnapshot } from "./otherHosts";
import type { SessionPRSummary } from "./api";
import type { ScopedBoard } from "./environment/scopedBoard";
import type { PullRequestSource } from "./pr/source";

const fixture = vi.hoisted(() => ({
	allSessions: [] as HostedSession[], hostStates: [] as HostSnapshot[],
	scopedBoard: { sessions: [], projects: [], orchestrators: [], sources: {} } as ScopedBoard,
	availableSources: [] as { kind: "cloud"; id: string }[],
	sourceFor: vi.fn(), refreshSource: vi.fn(async () => {}), cloudBoard: { loading: false, error: null },
	cloudNotifications: { available: false, unreadCount: 0 },
	localConfigured: true, configResolved: true, navigate: vi.fn(),
	environment: "local" as "local" | "cloud", activeProjectId: "all",
	cardRenders: [] as string[], params: {} as { hostId?: string; projectId?: string; source?: string; sourceId?: string },
	configForHost: vi.fn(() => ({ host: "ao.test", httpPort: "3011", muxPort: "3011", password: "test" })),
	summaries: new Map<string, SessionPRSummary>(),
	reload: vi.fn(), focus: undefined as (() => void) | undefined,
	theme: new Proxy({}, { get: () => "#888888" }),
}));
vi.mock("react-native", async () => {
	const React = await import("react");
	return {
		ActivityIndicator: "ActivityIndicator", RefreshControl: "RefreshControl", View: "View", Text: "Text",
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		Pressable: ({ children, ...props }: { children?: React.ReactNode; accessibilityRole?: string; accessibilityLabel?: string }) => {
			if (props.accessibilityRole === "button" && props.accessibilityLabel?.includes("Pull request")) fixture.cardRenders.push(props.accessibilityLabel);
			return React.createElement("Pressable", props, children);
		},
		SectionList: ({ sections, renderItem, ...props }: { sections: { key: string; data: unknown[] }[]; renderItem: (args: { item: unknown }) => React.ReactNode }) =>
			React.createElement("SectionList", { ...props, sections }, sections.flatMap(section => section.data.map((item, index) => React.createElement(React.Fragment, { key: `${section.key}:${index}` }, renderItem({ item }))))),
	};
});
vi.mock("expo-router", () => ({ useRouter: () => ({ navigate: fixture.navigate }), useLocalSearchParams: () => fixture.params, useFocusEffect: (callback: () => void) => { fixture.focus = callback; } }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0 }) }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {} }));
vi.mock("expo-secure-store", () => ({}));
vi.mock("./installId", () => ({}));
vi.mock("./sentry", () => ({}));
vi.mock("./store", () => ({ useApp: () => ({ ...fixture, refreshAll: vi.fn() }) }));
vi.mock("./cloud/notifications", () => ({ useCloudNotifications: () => fixture.cloudNotifications }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixture.theme, useThemedStyles: (factory: (theme: object) => object) => factory(fixture.theme) }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("./openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("./pageNavigation", () => ({ useOpenPage: () => vi.fn() }));
vi.mock("./pr-filter-dock", () => ({ PRFilterDock: "PRFilterDock" }));
vi.mock("./ProjectSwitcher", () => ({ ProjectSwitcher: "ProjectSwitcher" }));
vi.mock("./StaleBanner", () => ({ StaleBanner: "StaleBanner" }));
vi.mock("./UnpairedState", () => ({ UnpairedState: "UnpairedState" }));
vi.mock("./useBoardFailure", () => ({ useBoardFailure: () => ({}) }));
vi.mock("./useTabScrollToTop", () => ({ useTabScrollToTop: () => undefined }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));
vi.mock("./usePRSummaries", () => ({ PR_SUMMARY_REUSE_MS: 60_000, usePRSummaries: () => ({ summaryFor: (_config: unknown, id: string) => fixture.summaries.get(id), reload: fixture.reload }) }));
vi.mock("./ui", async () => {
	const React = await import("react");
	return { Button: "Button", EmptyState: "EmptyState", HeaderIconButton: "HeaderIconButton", ListSectionHeader: "ListSectionHeader", ScreenHeader: (props: { right?: React.ReactNode }) => React.createElement("ScreenHeader", props, props.right) };
});

import PRsScreen from "../app/(tabs)/prs";
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
	fixture.cloudNotifications = { available: false, unreadCount: 0 };
	fixture.scopedBoard = { sessions: [], projects: [], orchestrators: [], sources: {} };
	fixture.availableSources = []; fixture.sourceFor.mockReset();
	fixture.refreshSource.mockClear(); fixture.configForHost.mockClear();
	fixture.localConfigured = true;
	fixture.configResolved = true;
	fixture.environment = "local"; fixture.activeProjectId = "all";
	fixture.navigate.mockClear();
	fixture.allSessions = [session("mac"), session("windows")];
	fixture.hostStates = fixture.allSessions.map(s => ({
		hostId: s.hostId, name: s.hostName, appearance: { symbol: "desktop" }, endpoints: [], config: null,
		sessions: [s], connection: "open", notificationsUnread: 0, loading: false, error: null, errorStatus: null,
		projects: [], projectsKnown: true, orchestrators: [], orchestratorId: null, stats: {}, lastSyncAt: 0,
	}));
	fixture.cardRenders = []; fixture.params = {}; fixture.summaries.clear();
	fixture.reload.mockClear(); fixture.focus = undefined;
});
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it("loads two real Cloud repositories with the same PR number without a paired desktop", async () => {
	fixture.hostStates = []; fixture.allSessions = []; fixture.localConfigured = false; fixture.environment = "cloud";
	const source = { kind: "cloud", id: "org" } as const;
	fixture.availableSources = [source];
	fixture.scopedBoard = { projects: [], orchestrators: [], sources: {}, sessions: [{ source, value: { ...session("same"), kind: "worker" } }] };
	fixture.sourceFor.mockReturnValue({ pullRequests: { kind: "cloud", scopeKey: "account", isCurrent: () => true, list: async () => ["first", "second"].map((repo) => ({ number: 7, url: `https://github.com/a/${repo}/pull/7`, repo: `a/${repo}`, state: "open", title: repo, additions: 2, deletions: 0, changedFiles: 1, review: {}, ci: {}, mergeability: {} })) } as unknown as PullRequestSource });
	await act(async () => { renderer = create(<PRsScreen />); });
	expect(renderer!.root.findAllByType("UnpairedState" as never)).toHaveLength(0);
	const rows = renderer!.root.findByType("SectionList" as never).props.sections.flatMap((section: { data: unknown[] }) => section.data);
	expect(rows.map((row: { pr: { title: string } }) => row.pr.title)).toEqual(["first", "second"]);
});

it("uses one exact local source clock per visible PR machine, not the active environment clock", () => {
	act(() => { renderer = create(<PRsScreen />); });
	expect(renderer!.root.findAllByType("StaleBanner" as never).map((node) => node.props.sourceKey))
		.toEqual(['["local","mac"]', '["local","windows"]']);
	fixture.params = { hostId: "windows" };
	act(() => { renderer!.update(<PRsScreen />); });
	expect(renderer!.root.findAllByType("StaleBanner" as never).map((node) => node.props.sourceKey)).toEqual(['["local","windows"]']);
});
it("refreshes only the explicitly selected Cloud source without consulting Local pairing", async () => {
	fixture.params = { source: "cloud", sourceId: "org" };
	fixture.availableSources = [{ kind: "cloud", id: "org" }];
	await act(async () => { renderer = create(<PRsScreen />); });
	expect(renderer!.root.findAllByType("StaleBanner" as never)).toHaveLength(0);
	const list = renderer!.root.findByType("SectionList" as never);
	await act(async () => { await list.props.refreshControl.props.onRefresh(); });
	expect(fixture.refreshSource).toHaveBeenCalledWith({ kind: "cloud", id: "org" });
	expect(fixture.configForHost).not.toHaveBeenCalled();
});

it("shows the Cloud inbox bell from PRs with no paired desktop", () => {
	fixture.hostStates = [];
	fixture.allSessions = [];
	fixture.localConfigured = false;
	fixture.cloudNotifications = { available: true, unreadCount: 4 };
	act(() => { renderer = create(<PRsScreen />); });
	const bell = renderer!.root.findByProps({ label: "Notifications" });
	expect(bell.props.badge).toBe(4);
	act(() => bell.props.onPress());
	expect(fixture.navigate).toHaveBeenCalledWith("/notifications");
});
it("adds Cloud unread notifications to every Local host once", () => {
	fixture.hostStates = fixture.hostStates.map((host, index) => ({ ...host, notificationsUnread: index + 2 }));
	fixture.cloudNotifications = { available: true, unreadCount: 4 };
	act(() => { renderer = create(<PRsScreen />); });
	expect(renderer!.root.findByProps({ label: "Notifications" }).props.badge).toBe(9);
});
it("keeps the Local inbox bell while a paired desktop is offline", () => {
	fixture.hostStates = fixture.hostStates.map((host) => ({ ...host, connection: "closed", notificationsUnread: 2 }));
	act(() => { renderer = create(<PRsScreen />); });
	expect(renderer!.root.findByProps({ label: "Notifications" }).props.badge).toBe(4);
});
it("hides the bell when no notification source is configured", () => {
	fixture.hostStates = [];
	fixture.allSessions = [];
	fixture.localConfigured = false;
	act(() => { renderer = create(<PRsScreen />); });
	expect(renderer!.root.findAllByProps({ label: "Notifications" })).toHaveLength(0);
});

it("revalidates retained PR details on a later visit but not on rapid sidebar switches", () => {
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

it("keeps one-host PRs independent of a retained Cloud filter, even when project IDs collide", () => {
	fixture.environment = "cloud"; fixture.activeProjectId = "same-project";
	fixture.hostStates = [{ ...fixture.hostStates[0], projects: [{ id: "same-project", name: "Local A" }, { id: "local-b", name: "Local B" }] }];
	fixture.allSessions = [session("mac"), { ...session("mac"), id: "worker-b", projectId: "local-b", displayName: "Second Local PR", pr: { number: 8, url: "https://github.com/ao/test/pull/8", state: "open" } }];
	act(() => { renderer = create(<PRsScreen />); });
	expect(fixture.cardRenders).toHaveLength(2);
	const picker = renderer!.root.findByType("ProjectSwitcher" as never);
	expect(picker.props).toMatchObject({ hostId: "mac", selected: "all" });
	act(() => picker.props.onSelect("local-b"));
	const rows = renderer!.root.findByType("SectionList" as never).props.sections.flatMap((section: { data: unknown[] }) => section.data);
	expect(rows).toHaveLength(1);
	expect(rows[0].session.id).toBe("worker-b");
	expect(fixture.activeProjectId).toBe("same-project");
});

it("retains a valid Local legacy filter but resets PR selection when its host changes", () => {
	fixture.activeProjectId = "same-project";
	fixture.hostStates = [{ ...fixture.hostStates[0], projects: [{ id: "same-project", name: "Local A" }, { id: "other-project", name: "Local B" }] }];
	act(() => { renderer = create(<PRsScreen />); });
	expect(renderer!.root.findByType("ProjectSwitcher" as never).props.selected).toBe("same-project");
	act(() => renderer!.root.findByType("ProjectSwitcher" as never).props.onSelect("other-project"));
	fixture.environment = "cloud";
	fixture.hostStates = [{ ...fixture.hostStates[0], hostId: "windows" }];
	act(() => renderer!.update(<PRsScreen />));
	expect(renderer!.root.findByType("ProjectSwitcher" as never).props.selected).toBe("all");
});
