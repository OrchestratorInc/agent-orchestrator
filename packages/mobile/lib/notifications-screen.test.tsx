import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudNotification } from "@aoagents/cloud-client";
import type { ScopedBoard, SourceRef } from "./environment/scopedBoard";

const fixtures = vi.hoisted(() => {
	const configs = {
		dev: { host: "dev.local", httpPort: "3011", muxPort: "3012", secure: false, password: "secret", hostId: "dev" },
		mac: { host: "mac.local", httpPort: "3011", muxPort: "3012", secure: false, password: "secret", hostId: "mac" },
	};
	return {
		configs,
		hostsEnabled: true,
		localTerminated: false,
		board: { sessions: [], orchestrators: [], projects: [], sources: {} } as ScopedBoard,
		navigate: vi.fn(),
		restoreOn: vi.fn(async () => {}),
		refreshSource: vi.fn(async () => {}),
		cloud: {
			available: false, source: null as SourceRef | null, scopeKey: null as string | null, ownerKey: null as string | null,
			items: [] as CloudNotification[], unreadCount: 0, loading: false, refreshing: false, loadingMore: false,
			hasMore: false, error: null as string | null,
			refresh: vi.fn(async () => {}), loadMore: vi.fn(async () => {}),
			markRead: vi.fn(async () => {}), markAllRead: vi.fn(async () => {}), clearOne: vi.fn(async () => {}),
		},
		offline: new Set<string>(),
		markAllNotificationsRead: vi.fn(async () => {}),
		clearNotification: vi.fn(async () => {}),
		getNotifications: vi.fn(async (config: { hostId: "dev" | "mac" }) => ({
			notifications: config.hostId === "dev"
				? [
					{ id: "dev-1", sessionId: "worker-1", projectId: "project-1", prUrl: "", type: "needs_input", title: "First update", body: "One", status: "unread", createdAt: "2026-10-08T10:00:00Z" },
					{ id: "dev-2", sessionId: "worker-1", projectId: "project-1", prUrl: "", type: "ready_to_merge", title: "Second update", body: "Two", status: "unread", createdAt: "2026-10-08T09:00:00Z" },
				]
				: [{ id: "mac-1", sessionId: "worker-2", projectId: "project-2", prUrl: "", type: "pr_merged", title: "Third update", body: "Three", status: "read", createdAt: "2026-10-08T08:00:00Z" }],
			nextCursor: undefined as string | undefined,
			unreadCount: config.hostId === "dev" ? 2 : 0,
		})),
	};
});

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"),
		Alert: { alert: vi.fn() },
		Pressable: host("Pressable"),
		RefreshControl: host("RefreshControl"),
		SectionList: ({ sections, renderItem, renderSectionHeader, ListHeaderComponent, ListFooterComponent, ListEmptyComponent, ...props }: {
			sections: { key: string; data: unknown[] }[];
			renderItem: (args: { item: unknown }) => React.ReactNode;
			renderSectionHeader: (args: { section: { key: string; data: unknown[] } }) => React.ReactNode;
			ListHeaderComponent?: React.ReactNode;
			ListFooterComponent?: React.ReactNode;
			ListEmptyComponent?: React.ReactNode;
		}) => React.createElement(
			"SectionList",
			{ ...props, sections },
			ListHeaderComponent,
			...sections.flatMap((section) => [
				renderSectionHeader({ section }),
				...section.data.map((item) => renderItem({ item })),
			]),
			ListFooterComponent,
			sections.every((section) => section.data.length === 0) ? ListEmptyComponent : null,
		),
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("expo-router", async () => {
	const React = await import("react");
	return { useRouter: () => ({ navigate: fixtures.navigate, push: vi.fn(), back: vi.fn(), canGoBack: () => false }), useFocusEffect: (callback: () => void | (() => void)) => React.useEffect(callback, [callback]) };
});
vi.mock("./cloud/notifications", () => ({ useCloudNotifications: () => fixtures.cloud }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, right: 0, bottom: 0, left: 0 }) }));
vi.mock("./api", () => ({
	getNotifications: fixtures.getNotifications,
	markAllNotificationsRead: fixtures.markAllNotificationsRead,
	markNotificationRead: vi.fn(async () => {}),
	clearNotification: fixtures.clearNotification,
}));
vi.mock("./store", () => ({ useApp: () => ({
	hostStates: fixtures.hostsEnabled ? [
		{ hostId: "dev", name: "Dev", appearance: { symbol: "linux" }, config: fixtures.configs.dev, notificationsUnread: 2, sessions: [{ id: "worker-1", isTerminated: fixtures.localTerminated }], loading: false },
		{ hostId: "mac", name: "Mac", appearance: { symbol: "mac-mini" }, config: fixtures.configs.mac, notificationsUnread: 0, sessions: [], loading: false },
	] : [],
	configResolved: true,
	scopedBoard: fixtures.board,
	refreshSource: fixtures.refreshSource,
	restoreOn: fixtures.restoreOn,
	allSessions: [],
	allProjects: [],
	configForHost: (hostId: "dev" | "mac") => fixtures.offline.has(hostId) ? null : fixtures.configs[hostId],
	restore: vi.fn(async () => {}),
}) }));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "#000" });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("react-native-svg", () => ({ default: "Svg", Path: "Path", Rect: "Rect" }));
vi.mock("./AgentLogo", async () => {
	const React = await import("react");
	return { AgentLogo: (props: object) => React.createElement("AgentLogo", props) };
});
vi.mock("./notification-type-icon", async () => {
	const React = await import("react");
	return { NotificationTypeIcon: (props: object) => React.createElement("NotificationTypeIcon", props) };
});
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));
vi.mock("./pageNavigation", () => ({ useOpenPage: () => vi.fn() }));
vi.mock("./useNow", () => ({ MINUTE_MS: 60_000, useNow: () => Date.parse("2026-10-08T11:00:00Z") }));
vi.mock("./ui", async () => {
	const React = await import("react");
	const host = (name: string) => (props: object) => React.createElement(name, props);
	return { Button: host("Button"), Dot: host("Dot"), EmptyState: host("EmptyState"), HeaderIconButton: host("HeaderIconButton"), ScreenHeader: host("ScreenHeader") };
});
vi.mock("./UnpairedState", async () => {
	const React = await import("react");
	return { UnpairedState: () => React.createElement("UnpairedState") };
});
vi.mock("./backNavigation", () => ({ backOr: vi.fn() }));
vi.mock("./connectionError", () => ({ userFacingError: (_cause: unknown, fallback: string) => fallback }));
vi.mock("./NotificationSwipeRow", async () => {
	const React = await import("react");
	return { NotificationSwipeRow: ({ children, ...props }: { children: React.ReactNode; onClear: () => boolean }) => React.createElement("NotificationSwipeRow", props, children) };
});

import NotificationsScreen from "../app/notifications";
import { MachineIdentity } from "./MachineIdentity";

let renderer: ReactTestRenderer | undefined;
const initialConfigs = { ...fixtures.configs };
const initialGetNotifications = fixtures.getNotifications.getMockImplementation()!;
type InboxRow = { id: string; environment: "local" | "cloud"; status: string; title: string };
const inboxRows = (): InboxRow[] => renderer!.root.findByType("SectionList" as never).props.sections.flatMap((section: { data: InboxRow[] }) => section.data);

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.getNotifications.mockReset().mockImplementation(initialGetNotifications);
	fixtures.markAllNotificationsRead.mockReset().mockResolvedValue(undefined);
	Object.assign(fixtures.configs, initialConfigs);
	fixtures.clearNotification.mockClear();
	fixtures.offline.clear();
	fixtures.hostsEnabled = true;
	fixtures.localTerminated = false;
	fixtures.navigate.mockClear(); fixtures.restoreOn.mockReset().mockResolvedValue(undefined); fixtures.refreshSource.mockClear();
	Object.assign(fixtures.cloud, { available: false, source: null, scopeKey: null, ownerKey: null, items: [], unreadCount: 0, loading: false, refreshing: false, loadingMore: false, hasMore: false, error: null });
	for (const operation of [fixtures.cloud.markRead, fixtures.cloud.markAllRead, fixtures.cloud.clearOne, fixtures.cloud.refresh, fixtures.cloud.loadMore]) operation.mockReset().mockResolvedValue(undefined);
	fixtures.board = { projects: [], sessions: [], orchestrators: [], sources: {} };
	const original = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => { if (!String(args[0]).startsWith("react-test-renderer is deprecated")) original(...args); });
});

function enableCloud(overrides: Partial<CloudNotification> = {}) {
	const notification: CloudNotification = { id: "dev-1", orgId: "org-1", recipientUserId: "user-1", source: "cloud", sessionId: "worker-1", projectId: "project-1", type: "agent_completed", title: "Cloud finished", body: "Finished work", status: "unread", metadata: {}, createdAt: "2026-10-08T10:30:00Z", updatedAt: "2026-10-08T10:30:00Z", ...overrides };
	Object.assign(fixtures.cloud, { available: true, source: { kind: "cloud", id: "org-1" }, scopeKey: "account-1:org-1", ownerKey: "epoch-1:account-1:org-1", items: [notification], unreadCount: 4 });
	fixtures.board = { ...fixtures.board, sessions: [{ source: { kind: "cloud", id: "org-1" }, value: { id: "worker-1", projectId: "project-1", mode: "chat", isTerminated: false } as never }] };
	return notification;
}

describe("Cloud notifications in the existing inbox", () => {
	it("shows ready Cloud rows while a paired machine's initial history is still loading", async () => {
		enableCloud();
		fixtures.getNotifications.mockImplementationOnce(() => new Promise(() => {}));
		await act(async () => { renderer = create(<NotificationsScreen />); });
		expect(renderer!.root.findAllByType(MachineIdentity).map((identity) => identity.props.name)).toContain("Cloud");
	});
	it("refreshes the shared Cloud inbox when the notification screen is focused", async () => {
		enableCloud();
		await act(async () => { renderer = create(<NotificationsScreen />); });
		expect(fixtures.cloud.refresh).toHaveBeenCalledOnce();
	});
	it("merges Cloud and local records with colliding IDs and one source identity per row", async () => {
		enableCloud();
		await act(async () => { renderer = create(<NotificationsScreen />); });
		expect(renderer!.root.findAllByType(MachineIdentity).map((identity) => identity.props.name)).toEqual(["Cloud", "Dev", "Dev", "Mac"]);
		const list = renderer!.root.findByType("SectionList" as never);
		const rows = list.props.sections.flatMap((section: { data: unknown[] }) => section.data);
		expect(new Set(rows.map(list.props.keyExtractor)).size).toBe(4);
		expect(list.props.keyExtractor(rows[0], 0)).toBe('["cloud","epoch-1:account-1:org-1","dev-1"]');
	});
	it("opens and marks the exact Cloud notification without using the colliding local host", async () => {
		enableCloud();
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const row = renderer!.root.findAll((node) => String(node.type) === "Pressable" && String(node.props.accessibilityLabel).startsWith("Cloud finished,"))[0];
		expect(row).toBeDefined();
		await act(async () => { row.props.onPress(); });
		expect(fixtures.cloud.markRead).toHaveBeenCalledWith(["dev-1"]);
		expect(fixtures.navigate).toHaveBeenCalledWith({ pathname: "/session/[id]", params: { id: "worker-1", source: "cloud", sourceId: "org-1" } });
	});
	it("clears Cloud through its owner rather than the daemon delete endpoint", async () => {
		const notification = enableCloud();
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const row = renderer!.root.findAllByType("NotificationSwipeRow" as never)[0];
		await act(async () => { expect(row.props.onClear()).toBe(true); });
		expect(fixtures.cloud.clearOne).toHaveBeenCalledWith(notification);
		expect(fixtures.clearNotification).not.toHaveBeenCalled();
	});
	it("offers Cloud-only history without asking the signed-in user to pair a machine", async () => {
		enableCloud(); fixtures.hostsEnabled = false; fixtures.cloud.items = [];
		await act(async () => { renderer = create(<NotificationsScreen />); });
		expect(renderer!.root.findAllByType("UnpairedState" as never)).toHaveLength(0);
		expect(renderer!.root.findAllByType("EmptyState" as never).some((node) => node.props.title === "All caught up")).toBe(true);
	});
	it("does not claim a failed Cloud-only inbox is all caught up", async () => {
		enableCloud(); fixtures.hostsEnabled = false; fixtures.cloud.items = []; fixtures.cloud.error = "Cloud is unreachable";
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const empty = renderer!.root.findAllByType("EmptyState" as never);
		expect(empty.some((node) => node.props.title === "All caught up")).toBe(false);
		expect(empty.some((node) => node.props.title === "Couldn't load Cloud notifications")).toBe(true);
	});
	it("does not navigate after an old login's restore finishes", async () => {
		enableCloud({ type: "needs_input" });
		fixtures.board = { ...fixtures.board, sessions: fixtures.board.sessions.map((entry) => ({ ...entry, value: { ...entry.value, isTerminated: true } })) };
		let finish!: () => void;
		fixtures.restoreOn.mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; }));
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const restore = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Restore Cloud finished")[0];
		expect(restore).toBeDefined();
		await act(async () => { restore.props.onPress(); });
		fixtures.cloud.ownerKey = "epoch-2:account-1:org-1";
		await act(async () => { renderer!.update(<NotificationsScreen />); });
		await act(async () => { finish(); });
		expect(fixtures.navigate).not.toHaveBeenCalled();
	});
	it("includes Cloud in mark-all even without a configured local machine", async () => {
		enableCloud(); fixtures.hostsEnabled = false;
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const header = renderer!.root.findByType("ScreenHeader" as never);
		expect(header.props.right).toBeDefined();
		await act(async () => { await header.props.right.props.onPress(); });
		expect(fixtures.cloud.markAllRead).toHaveBeenCalledOnce();
	});
	it("restores failed local mark-all state after switching Cloud accounts", async () => {
		const notification = enableCloud();
		let failLocal!: (cause: Error) => void;
		let finishCloud!: () => void;
		fixtures.markAllNotificationsRead.mockImplementationOnce(() => new Promise<void>((_resolve, reject) => { failLocal = reject; }));
		fixtures.cloud.markAllRead.mockImplementationOnce(() => new Promise<void>((resolve) => { finishCloud = resolve; }));
		await act(async () => { renderer = create(<NotificationsScreen />); });
		await act(async () => { renderer!.root.findByType("ScreenHeader" as never).props.right.props.onPress(); });
		Object.assign(fixtures.cloud, {
			scopeKey: "account-2:org-2", ownerKey: "epoch-2:account-2:org-2", source: { kind: "cloud", id: "org-2" },
			items: [{ ...notification, orgId: "org-2", recipientUserId: "user-2", title: "New account update" }], unreadCount: 1,
		});
		await act(async () => { renderer!.update(<NotificationsScreen />); });
		await act(async () => { failLocal(new Error("Dev is unavailable")); finishCloud(); });
		expect(inboxRows().filter((row) => row.environment === "local").map((row) => row.status)).toEqual(["unread", "unread", "read"]);
		expect(inboxRows().filter((row) => row.environment === "cloud").map((row) => row.status)).toEqual(["unread"]);
		expect(renderer!.root.findAll((node) => String(node.type) === "Text" && node.children.join("") === "Couldn't mark every notification read. Refresh and try again.")).toHaveLength(1);
	});
	it("recovers failed local mark-all state while an old Cloud request is still pending", async () => {
		enableCloud();
		let failLocal!: (cause: Error) => void;
		fixtures.markAllNotificationsRead.mockImplementationOnce(() => new Promise<void>((_resolve, reject) => { failLocal = reject; }));
		fixtures.cloud.markAllRead.mockImplementationOnce(() => new Promise<void>(() => {}));
		await act(async () => { renderer = create(<NotificationsScreen />); });
		await act(async () => { renderer!.root.findByType("ScreenHeader" as never).props.right.props.onPress(); });
		fixtures.cloud.ownerKey = "epoch-2:account-1:org-1";
		await act(async () => { renderer!.update(<NotificationsScreen />); });
		fixtures.getNotifications.mockRejectedValueOnce(new Error("Still offline")).mockRejectedValueOnce(new Error("Still offline"));
		await act(async () => { failLocal(new Error("Dev is unavailable")); });
		expect(inboxRows().filter((row) => row.environment === "local").map((row) => row.status)).toEqual(["unread", "unread", "read"]);
		expect(renderer!.root.findAll((node) => String(node.type) === "Text" && node.children.join("") === "Couldn't mark every notification read. Refresh and try again.")).toHaveLength(1);
	});
	it("retains unread local rows when another row is cleared and both mark-all and recovery fail", async () => {
		let failLocal!: (cause: Error) => void;
		fixtures.markAllNotificationsRead.mockImplementationOnce(() => new Promise<void>((_resolve, reject) => { failLocal = reject; }));
		await act(async () => { renderer = create(<NotificationsScreen />); });
		await act(async () => { renderer!.root.findByType("ScreenHeader" as never).props.right.props.onPress(); });
		const secondRow = renderer!.root.findAllByType("NotificationSwipeRow" as never)[1];
		await act(async () => { secondRow.props.onClear(); });
		fixtures.getNotifications.mockRejectedValueOnce(new Error("Still offline")).mockRejectedValueOnce(new Error("Still offline"));
		await act(async () => { failLocal(new Error("Dev is unavailable")); });
		expect(inboxRows().filter((row) => row.environment === "local").map((row) => [row.id, row.status])).toEqual([["dev-1", "unread"], ["mac-1", "read"]]);
	});
	it("ignores a local mark-all failure after the host endpoint changes", async () => {
		let failLocal!: (cause: Error) => void;
		fixtures.markAllNotificationsRead.mockImplementationOnce(() => new Promise<void>((_resolve, reject) => { failLocal = reject; }));
		await act(async () => { renderer = create(<NotificationsScreen />); });
		await act(async () => { renderer!.root.findByType("ScreenHeader" as never).props.right.props.onPress(); });
		fixtures.configs.dev = { ...fixtures.configs.dev, host: "replacement.local" };
		fixtures.getNotifications.mockResolvedValueOnce({
			notifications: [{ id: "replacement-1", sessionId: "worker-1", projectId: "project-1", prUrl: "", type: "needs_input", title: "Replacement update", body: "", status: "unread", createdAt: "2026-10-08T10:00:00Z" }], nextCursor: undefined, unreadCount: 1,
		});
		await act(async () => { renderer!.update(<NotificationsScreen />); });
		await act(async () => { failLocal(new Error("Old Dev is unavailable")); });
		expect(inboxRows().filter((row) => row.environment === "local").map((row) => [row.id, row.status])).toEqual([["replacement-1", "unread"], ["mac-1", "read"]]);
		expect(renderer!.root.findAll((node) => String(node.type) === "Text" && node.children.join("") === "Couldn't mark every notification read. Refresh and try again.")).toHaveLength(0);
	});
	it("preserves loaded local history and new unread arrivals when mark-all succeeds", async () => {
		enableCloud();
		fixtures.cloud.markAllRead.mockImplementationOnce(() => new Promise<void>(() => {}));
		const record = (id: string, status: string, createdAt: string) => ({ id, sessionId: "worker-1", projectId: "project-1", prUrl: "", type: "needs_input", title: id, body: "", status, createdAt });
		fixtures.getNotifications.mockResolvedValueOnce({ notifications: [record("dev-first", "unread", "2026-10-08T10:00:00Z")], nextCursor: "cursor-1", unreadCount: 2 });
		await act(async () => { renderer = create(<NotificationsScreen />); });
		fixtures.getNotifications.mockResolvedValueOnce({ notifications: [record("dev-older", "unread", "2026-10-08T09:00:00Z")], nextCursor: undefined, unreadCount: 2 });
		await act(async () => { renderer!.root.findByType("SectionList" as never).props.onEndReached(); });
		fixtures.getNotifications.mockResolvedValueOnce({ notifications: [record("dev-new", "unread", "2026-10-08T10:30:00Z"), record("dev-first", "read", "2026-10-08T10:00:00Z")], nextCursor: "cursor-1", unreadCount: 1 });
		await act(async () => { renderer!.root.findByType("ScreenHeader" as never).props.right.props.onPress(); });
		expect(inboxRows().filter((row) => row.environment === "local").map((row) => [row.id, row.status])).toEqual([["dev-new", "unread"], ["dev-first", "read"], ["dev-older", "read"], ["mac-1", "read"]]);
		expect(renderer!.root.findByType("ScreenHeader" as never).props.right).toBeDefined();
		fixtures.getNotifications.mockResolvedValueOnce({ notifications: [record("dev-middle", "unread", "2026-10-08T09:30:00Z"), record("dev-older", "read", "2026-10-08T09:00:00Z")], nextCursor: undefined, unreadCount: 2 });
		await act(async () => { renderer!.root.findByType("SectionList" as never).props.onEndReached(); });
		expect(inboxRows().filter((row) => row.environment === "local").map((row) => [row.id, row.status])).toEqual([["dev-new", "unread"], ["dev-middle", "unread"], ["dev-first", "read"], ["dev-older", "read"], ["mac-1", "read"]]);
	});
	it("does not resurrect a local row cleared while mark-all reconciliation is pending", async () => {
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const snapshot = await initialGetNotifications({ hostId: "dev" });
		let finishRead!: () => void;
		fixtures.getNotifications.mockImplementationOnce(() => new Promise((resolve) => {
			finishRead = () => resolve({ ...snapshot, notifications: snapshot.notifications.map((item) => ({ ...item, status: "read" })), unreadCount: 0 });
		}));
		await act(async () => { renderer!.root.findByType("ScreenHeader" as never).props.right.props.onPress(); });
		const secondRow = renderer!.root.findAllByType("NotificationSwipeRow" as never)[1];
		await act(async () => { secondRow.props.onClear(); });
		await act(async () => { finishRead(); });
		expect(inboxRows().filter((row) => row.environment === "local").map((row) => [row.id, row.status])).toEqual([["dev-1", "read"], ["mac-1", "read"]]);
	});
	it("waits for a missing Cloud session instead of navigating to local PRs", async () => {
		enableCloud({ type: "review_completed" }); fixtures.board = { ...fixtures.board, sessions: [] };
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const row = renderer!.root.findAll((node) => String(node.type) === "Pressable" && String(node.props.accessibilityLabel).startsWith("Cloud finished,"))[0];
		expect(row).toBeDefined();
		await act(async () => { row.props.onPress(); });
		expect(fixtures.refreshSource).toHaveBeenCalledWith({ kind: "cloud", id: "org-1" });
		expect(fixtures.navigate).not.toHaveBeenCalled();
	});
	it("ignores an old Cloud row after signing in again to the same account", async () => {
		enableCloud();
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const row = renderer!.root.findAll((node) => String(node.type) === "Pressable" && String(node.props.accessibilityLabel).startsWith("Cloud finished,"))[0];
		expect(row).toBeDefined();
		const press = row.props.onPress;
		fixtures.cloud.ownerKey = "epoch-2:account-1:org-1";
		await act(async () => { renderer!.update(<NotificationsScreen />); });
		await act(async () => { press(); });
		expect(fixtures.cloud.markRead).not.toHaveBeenCalled(); expect(fixtures.navigate).not.toHaveBeenCalled();
	});
	it("does not restore Cloud on a row tap, but offers explicit source-qualified restore", async () => {
		enableCloud({ type: "needs_input" });
		fixtures.board = { ...fixtures.board, sessions: fixtures.board.sessions.map((entry) => ({ ...entry, value: { ...entry.value, isTerminated: true } })) };
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const row = renderer!.root.findAll((node) => String(node.type) === "Pressable" && String(node.props.accessibilityLabel).startsWith("Cloud finished,"))[0];
		expect(row).toBeDefined();
		await act(async () => { row.props.onPress(); });
		expect(fixtures.restoreOn).not.toHaveBeenCalled(); expect(fixtures.navigate).not.toHaveBeenCalled();
		const restore = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Restore Cloud finished")[0];
		expect(restore).toBeDefined();
		await act(async () => { restore.props.onPress(); });
		expect(fixtures.restoreOn).toHaveBeenCalledWith({ kind: "cloud", id: "org-1" }, "worker-1");
		expect(fixtures.navigate).toHaveBeenCalledWith({ pathname: "/session/[id]", params: { id: "worker-1", source: "cloud", sourceId: "org-1" } });
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

describe("NotificationsScreen", () => {
	it("restores a local notification through its own machine and opens the qualified local route", async () => {
		fixtures.localTerminated = true;
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const restore = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Restore First update")[0];
		expect(restore).toBeDefined();
		await act(async () => { restore.props.onPress(); });
		expect(fixtures.restoreOn).toHaveBeenCalledWith({ kind: "local", id: "dev" }, "worker-1");
		expect(fixtures.navigate).toHaveBeenCalledWith({ pathname: "/session/[id]", params: { id: "worker-1", hostId: "dev", source: "local", sourceId: "dev" } });
	});
	it("refuses to clear a retained offline notification and accepts it after reconnecting", async () => {
		await act(async () => { renderer = create(<NotificationsScreen />); });
		fixtures.offline.add("dev");
		const firstSwipe = renderer!.root.findAllByType("NotificationSwipeRow" as never)[0];
		act(() => { expect(firstSwipe.props.onClear()).toBe(false); });
		expect(fixtures.clearNotification).not.toHaveBeenCalled();
		expect(renderer!.root.findAllByType("NotificationSwipeRow" as never)).toHaveLength(3);
		fixtures.offline.clear();
		await act(async () => { expect(firstSwipe.props.onClear()).toBe(true); });
		expect(fixtures.clearNotification).toHaveBeenCalledWith(fixtures.configs.dev, "dev-1");
		expect(renderer!.root.findAllByType("NotificationSwipeRow" as never)).toHaveLength(2);
	});
	it("renders a flat cross-machine timeline with the owning machine identity on every row", async () => {
		await act(async () => { renderer = create(<NotificationsScreen />); });

		expect(renderer!.root.findAll((node) => String(node.type) === "AgentLogo")).toHaveLength(0);
		expect(renderer!.root.findAll((node) => String(node.type) === "Pressable" && String(node.props.accessibilityLabel).startsWith("Clear "))).toHaveLength(0);
		expect(renderer!.root.findAllByType(MachineIdentity).map((identity) => ({ name: identity.props.name, appearance: identity.props.appearance }))).toEqual([
			{ name: "Dev", appearance: { symbol: "linux" } },
			{ name: "Dev", appearance: { symbol: "linux" } },
			{ name: "Mac", appearance: { symbol: "mac-mini" } },
		]);
	});

	it("clears a notification through the hidden delete gesture action", async () => {
		await act(async () => { renderer = create(<NotificationsScreen />); });

		const firstRow = renderer!.root.find((node) => node.props.accessibilityLabel === "First update, Needs input, Dev machine");
		expect(firstRow.props.accessibilityActions).toEqual([{ name: "delete", label: "Clear notification" }]);

		await act(async () => {
			firstRow.props.onAccessibilityAction({ nativeEvent: { actionName: "delete" } });
		});

		expect(fixtures.clearNotification).toHaveBeenCalledWith(fixtures.configs.dev, "dev-1");
		expect(renderer!.root.findAll((node) => node.props.accessibilityLabel === "First update, Needs input, Dev machine")).toHaveLength(0);
	});

	it("releases pagination when a refresh supersedes a page that is still loading", async () => {
		const page = (id: string, nextCursor?: string) => ({
			notifications: [{ id, sessionId: "worker-1", projectId: "project-1", prUrl: "", type: "needs_input", title: id, body: "", status: "read", createdAt: "2026-10-08T07:00:00Z" }],
			nextCursor,
			unreadCount: 0,
		});
		fixtures.getNotifications.mockImplementation(async (config: { hostId: "dev" | "mac" }) =>
			config.hostId === "dev" ? page("dev-first", "cursor-1") : page("mac-first"));
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const list = () => renderer!.root.findByType("SectionList" as never);

		let releaseMore!: () => void;
		fixtures.getNotifications.mockImplementationOnce(() => new Promise((resolve) => { releaseMore = () => resolve(page("dev-stale")); }));
		act(() => { list().props.onEndReached(); });
		await act(async () => { await list().props.refreshControl.props.onRefresh(); });
		await act(async () => { releaseMore(); });

		expect(renderer!.root.findAllByType("ActivityIndicator" as never)).toHaveLength(0);
		fixtures.getNotifications.mockClear();
		await act(async () => { list().props.onEndReached(); });
		expect(fixtures.getNotifications).toHaveBeenCalledWith(fixtures.configs.dev, expect.objectContaining({ cursor: "cursor-1" }));
	});
});
