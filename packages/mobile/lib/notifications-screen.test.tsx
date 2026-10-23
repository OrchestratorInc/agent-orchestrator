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
		clearAllNotifications: vi.fn(async (_config: unknown) => {}),
		alert: vi.fn(),
		focus: (() => {
			const listeners = new Set<() => void>();
			return {
				value: true,
				subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
				set(next: boolean) { this.value = next; for (const listener of listeners) listener(); },
			};
		})(),
		markAllNotificationsRead: vi.fn(async (_config: unknown, _ids?: string[]) => {}),
		refreshHost: vi.fn(async (_hostId: string) => {}),
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
		Alert: { alert: fixtures.alert },
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
vi.mock("expo-router", async () => {
	const React = await import("react");
	return {
		useRouter: () => ({ navigate: vi.fn(), push: vi.fn(), back: vi.fn(), canGoBack: () => false }),
		// Runs the effect while focused and its cleanup on blur, like react-navigation.
		useFocusEffect: (effect: () => (() => void) | void) => {
			const focused = React.useSyncExternalStore(fixtures.focus.subscribe, () => fixtures.focus.value);
			React.useEffect(() => {
				if (!focused) return;
				const cleanup = effect();
				return cleanup ?? undefined;
			}, [effect, focused]);
		},
	};
});
vi.mock("expo-router/react-navigation", async () => {
	const React = await import("react");
	return { useIsFocused: () => React.useSyncExternalStore(fixtures.focus.subscribe, () => fixtures.focus.value) };
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, right: 0, bottom: 0, left: 0 }) }));
vi.mock("./api", () => ({
	getNotifications: fixtures.getNotifications,
	markAllNotificationsRead: fixtures.markAllNotificationsRead,
	markNotificationRead: vi.fn(async () => {}),
	clearNotification: fixtures.clearNotification,
	clearAllNotifications: fixtures.clearAllNotifications,
}));
vi.mock("./store", () => ({ useApp: () => ({
	hostStates: [
		{ hostId: "dev", name: "Dev", appearance: { symbol: "linux" }, config: fixtures.configs.dev, notificationsUnread: 2, sessions: [], loading: false },
		{ hostId: "mac", name: "Mac", appearance: { symbol: "mac-mini" }, config: fixtures.configs.mac, notificationsUnread: 0, sessions: [], loading: false },
	],
	allSessions: [],
	allProjects: [],
	configForHost: (hostId: "dev" | "mac") => fixtures.offline.has(hostId) ? null : fixtures.configs[hostId],
	refreshHost: fixtures.refreshHost,
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

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.getNotifications.mockClear();
	fixtures.clearNotification.mockClear();
	fixtures.markAllNotificationsRead.mockReset();
	fixtures.markAllNotificationsRead.mockImplementation(async () => {});
	fixtures.refreshHost.mockClear();
	fixtures.clearAllNotifications.mockReset();
	fixtures.clearAllNotifications.mockImplementation(async () => {});
	fixtures.alert.mockClear();
	fixtures.offline.clear();
	fixtures.focus.value = true;
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

describe("NotificationsScreen", () => {
	it("clears every connected machine's history from the header after confirming", async () => {
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const header = () => renderer!.root.findByType("ScreenHeader" as never);
		expect(header().props.right.props).toEqual(expect.objectContaining({ icon: "trash", label: "Clear all" }));

		act(() => { header().props.right.props.onPress(); });
		expect(fixtures.clearAllNotifications).not.toHaveBeenCalled();
		const [, , buttons] = fixtures.alert.mock.calls[0] as [string, string, { text: string; onPress?: () => void }[]];
		fixtures.refreshHost.mockClear();
		await act(async () => { buttons.find((button) => button.text === "Clear all")!.onPress!(); });

		expect(fixtures.clearAllNotifications).toHaveBeenCalledTimes(2);
		expect(fixtures.clearAllNotifications).toHaveBeenCalledWith(fixtures.configs.dev);
		expect(fixtures.clearAllNotifications).toHaveBeenCalledWith(fixtures.configs.mac);
		expect(fixtures.refreshHost).toHaveBeenCalledWith("dev");
		expect(fixtures.refreshHost).toHaveBeenCalledWith("mac");
	});

	it("restores a machine's rows when its clear fails and the recovery reload fails too", async () => {
		await act(async () => { renderer = create(<NotificationsScreen />); });
		const titles = () => renderer!.root.findAll((node) => String(node.type) === "Text" && ["First update", "Second update", "Third update"].includes(String(node.props.children))).map((node) => String(node.props.children));
		expect(titles()).toEqual(["First update", "Second update", "Third update"]);

		fixtures.clearAllNotifications.mockImplementation(async (config: unknown) => {
			if (config === fixtures.configs.dev) throw new Error("connection lost");
		});
		const base = fixtures.getNotifications.getMockImplementation()!;
		fixtures.getNotifications.mockImplementation(async () => { throw new Error("connection lost"); });
		try {
			const header = renderer!.root.findByType("ScreenHeader" as never);
			act(() => { header.props.right.props.onPress(); });
			const [, , buttons] = fixtures.alert.mock.calls[0] as [string, string, { text: string; onPress?: () => void }[]];
			await act(async () => { buttons.find((button) => button.text === "Clear all")!.onPress!(); });
		} finally {
			fixtures.getNotifications.mockImplementation(base);
		}

		// Dev's delete failed, so its history is still there; Mac's succeeded.
		expect(titles()).toEqual(["First update", "Second update"]);
		expect(renderer!.root.findAll((node) => String(node.type) === "Text" && String(node.props.children) === "Couldn't clear notifications on Dev.")).toHaveLength(1);
	});

	it("leaves an offline machine's rows alone and says so", async () => {
		await act(async () => { renderer = create(<NotificationsScreen />); });
		fixtures.offline.add("mac");
		const header = renderer!.root.findByType("ScreenHeader" as never);
		act(() => { header.props.right.props.onPress(); });
		const [, , buttons] = fixtures.alert.mock.calls[0] as [string, string, { text: string; onPress?: () => void }[]];
		await act(async () => { buttons.find((button) => button.text === "Clear all")!.onPress!(); });

		expect(fixtures.clearAllNotifications).toHaveBeenCalledTimes(1);
		expect(fixtures.clearAllNotifications).toHaveBeenCalledWith(fixtures.configs.dev);
		expect(renderer!.root.findAll((node) => String(node.type) === "Text" && String(node.props.children).includes("Mac is offline"))).toHaveLength(1);
	});

	it("marks the loaded unread rows read on open and keeps them highlighted for the visit", async () => {
		await act(async () => { renderer = create(<NotificationsScreen />); });

		expect(fixtures.markAllNotificationsRead).toHaveBeenCalledTimes(1);
		expect(fixtures.markAllNotificationsRead).toHaveBeenCalledWith(fixtures.configs.dev, ["dev-1", "dev-2"]);
		expect(fixtures.refreshHost).toHaveBeenCalledWith("dev");
		// Both acknowledged rows still carry the unread dot.
		expect(renderer!.root.findAllByType("Dot" as never)).toHaveLength(2);

		// Leaving for a session keeps the screen mounted; coming back shows them read.
		act(() => { fixtures.focus.set(false); });
		expect(renderer!.root.findAllByType("Dot" as never)).toHaveLength(0);
		expect(fixtures.markAllNotificationsRead).toHaveBeenCalledTimes(1);
	});

	it("does not ack rows that load while the screen is blurred, and acks them on return", async () => {
		await act(async () => { renderer = create(<NotificationsScreen />); });
		expect(fixtures.markAllNotificationsRead).toHaveBeenCalledTimes(1);

		// Into a session: the screen stays mounted, and a reload brings a new unread row.
		act(() => { fixtures.focus.set(false); });
		const base = fixtures.getNotifications.getMockImplementation()!;
		try {
			fixtures.getNotifications.mockImplementation(async (config: { hostId: "dev" | "mac" }) => {
				const page = await base(config);
				if (config.hostId !== "mac") return page;
				return { ...page, notifications: [{ id: "mac-2", sessionId: "worker-2", projectId: "project-2", prUrl: "", type: "needs_input", title: "Fresh", body: "", status: "unread", createdAt: "2026-10-08T10:30:00Z" }, ...page.notifications], unreadCount: 1 };
			});
			fixtures.markAllNotificationsRead.mockClear();
			fixtures.refreshHost.mockClear();
			const list = renderer!.root.findByType("SectionList" as never);
			await act(async () => { await list.props.refreshControl.props.onRefresh(); });
			expect(fixtures.markAllNotificationsRead).not.toHaveBeenCalled();
			expect(fixtures.refreshHost).not.toHaveBeenCalled();

			await act(async () => { fixtures.focus.set(true); });
			expect(fixtures.markAllNotificationsRead).toHaveBeenCalledWith(fixtures.configs.mac, ["mac-2"]);
		} finally {
			fixtures.getNotifications.mockImplementation(base);
		}
	});

	it("ignores a failure from an ack that a newer successful ack superseded", async () => {
		let rejectFirst!: (cause: Error) => void;
		fixtures.markAllNotificationsRead.mockImplementationOnce(() => new Promise<void>((_resolve, reject) => { rejectFirst = reject; }));
		await act(async () => { renderer = create(<NotificationsScreen />); });
		expect(fixtures.markAllNotificationsRead).toHaveBeenCalledTimes(1);

		// The first ack is still in flight when a refresh re-acks the same rows, and that one succeeds.
		const list = renderer!.root.findByType("SectionList" as never);
		await act(async () => { await list.props.refreshControl.props.onRefresh(); });
		expect(fixtures.markAllNotificationsRead).toHaveBeenCalledTimes(2);
		expect(fixtures.markAllNotificationsRead).toHaveBeenLastCalledWith(fixtures.configs.dev, ["dev-1", "dev-2"]);

		await act(async () => { rejectFirst(new Error("timed out")); });
		// Leaving drops the visit highlight, so any dot left is a row the screen thinks is unread.
		act(() => { fixtures.focus.set(false); });
		expect(renderer!.root.findAllByType("Dot" as never)).toHaveLength(0);
		expect(renderer!.root.findAll((node) => String(node.type) === "Text" && String(node.props.children).startsWith("Couldn't mark"))).toHaveLength(0);
	});

	it("keeps rows read when only the badge refresh fails", async () => {
		fixtures.refreshHost.mockRejectedValueOnce(new Error("offline"));
		await act(async () => { renderer = create(<NotificationsScreen />); });
		act(() => { fixtures.focus.set(false); });
		expect(renderer!.root.findAllByType("Dot" as never)).toHaveLength(0);
	});

	it("restores unread rows when the ack fails without retrying in a loop", async () => {
		fixtures.markAllNotificationsRead.mockRejectedValue(new Error("offline"));
		await act(async () => { renderer = create(<NotificationsScreen />); });
		await act(async () => {});

		expect(fixtures.markAllNotificationsRead).toHaveBeenCalledTimes(1);
		expect(fixtures.refreshHost).not.toHaveBeenCalled();
		const dots = renderer!.root.findAllByType("Dot" as never);
		expect(dots).toHaveLength(2);

		fixtures.markAllNotificationsRead.mockImplementation(async () => {});
		const list = renderer!.root.findByType("SectionList" as never);
		await act(async () => { await list.props.refreshControl.props.onRefresh(); });
		expect(fixtures.markAllNotificationsRead).toHaveBeenCalledTimes(2);
		expect(fixtures.markAllNotificationsRead).toHaveBeenLastCalledWith(fixtures.configs.dev, ["dev-1", "dev-2"]);
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

	it("hides Clear all when there is nothing to clear", async () => {
		fixtures.getNotifications.mockImplementation(async () => ({ notifications: [], nextCursor: undefined, unreadCount: 0 }));
		await act(async () => { renderer = create(<NotificationsScreen />); });
		expect(renderer!.root.findByType("ScreenHeader" as never).props.right).toBeUndefined();
	});
});
