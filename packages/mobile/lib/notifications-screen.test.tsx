import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => {
	const configs = {
		dev: { host: "dev.local", httpPort: "3011", muxPort: "3012", secure: false, password: "secret", hostId: "dev" },
		mac: { host: "mac.local", httpPort: "3011", muxPort: "3012", secure: false, password: "secret", hostId: "mac" },
	};
	return {
		configs,
		offline: new Set<string>(),
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
			props,
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
vi.mock("expo-router", () => ({ useRouter: () => ({ navigate: vi.fn(), push: vi.fn(), back: vi.fn(), canGoBack: () => false }) }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, right: 0, bottom: 0, left: 0 }) }));
vi.mock("./api", () => ({
	getNotifications: fixtures.getNotifications,
	markAllNotificationsRead: vi.fn(async () => {}),
	markNotificationRead: vi.fn(async () => {}),
	clearNotification: fixtures.clearNotification,
}));
vi.mock("./store", () => ({ useApp: () => ({
	hostStates: [
		{ hostId: "dev", name: "Dev", appearance: { symbol: "linux" }, config: fixtures.configs.dev, notificationsUnread: 2, sessions: [], loading: false },
		{ hostId: "mac", name: "Mac", appearance: { symbol: "mac-mini" }, config: fixtures.configs.mac, notificationsUnread: 0, sessions: [], loading: false },
	],
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
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn(), warning: vi.fn() } }));
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

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.getNotifications.mockClear();
	fixtures.clearNotification.mockClear();
	fixtures.offline.clear();
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

describe("NotificationsScreen", () => {
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
