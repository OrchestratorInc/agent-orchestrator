import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "./api";
import { createContext, createElement, useContext, useImperativeHandle, Fragment } from "react";

const appContext = createContext({ currentHostId: "host-1", notificationsUnread: 0 });
const counters = vi.hoisted(() => ({ renders: 0, scrollToOffset: vi.fn(), foreground: (_state: string) => {}, focus: () => {} }));
const boardStore = vi.hoisted(() => ({
	projects: [], allProjects: [], connection: "open", hostStates: [] as { hostId: string; connection: string }[],
	kill: vi.fn(), renameWorker: vi.fn(), setWorkerPinned: vi.fn(), restore: vi.fn(), resumeAgent: vi.fn(),
	configForHost: (hostId: string) => ({ host: `${hostId}.test`, httpPort: "3011", muxPort: "3011", password: "secret", hostId }),
}));
// Each archive page request waits here until the test answers it.
const archivePages = vi.hoisted(() => ({
	requests: [] as { hostId?: string; cursor?: string | null; resolve(page: { sessions: unknown[]; nextCursor: string | null }): void; reject(cause: unknown): void }[],
}));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("./api", async (importOriginal) => ({
	...await importOriginal<typeof import("./api")>(),
	getArchivedSessions: (cfg: { hostId?: string }, options: { cursor?: string | null } = {}) =>
		new Promise((resolve, reject) => { archivePages.requests.push({ hostId: cfg.hostId, cursor: options.cursor, resolve, reject }); }),
}));

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

// expo-router's useRouter returns one stable router object; mirror that.
const router = vi.hoisted(() => ({ push: vi.fn() }));
vi.mock("expo-router", () => ({ useRouter: () => router, useFocusEffect: (callback: () => void) => { counters.focus = callback; } }));
vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator",
	Alert: { alert: vi.fn() }, AppState: { addEventListener: (_event: string, callback: (state: string) => void) => { counters.foreground = callback; return { remove: vi.fn() }; } }, RefreshControl: "RefreshControl",
	Keyboard: { dismiss: vi.fn() },
	Platform: { OS: "ios" },
	Pressable: "Pressable",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	Text: "Text",
	TextInput: "TextInput",
	View: "View",
	FlatList: TestList,
}));
vi.mock("./AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("react-native-svg", () => ({ default: "Svg", Path: "Path", Rect: "Rect" }));
vi.mock("./haptics", () => ({ haptics: { select: vi.fn(), tap: vi.fn(), warning: vi.fn(), success: vi.fn(), error: vi.fn() } }));
vi.mock("./agentsView", async (importOriginal) => ({
	...await importOriginal<typeof import("./agentsView")>(),
	prLine: () => undefined,
	workerRowPresentation: () => { counters.renders++; return { title: "Polish workers", project: "Agent Orchestrator", branch: "feat/mobile", trailing: "2m", trailingKind: "time" }; },
	workerStatusGlyph: () => undefined,
}));
vi.mock("./prView", () => ({ toneColor: () => "tone" }));
vi.mock("./theme", () => ({ statusVisual: () => ({ label: "Working", color: "orange", breathing: false }) }));
vi.mock("./ThemeProvider", () => {
	const theme = {
		bgBase: "base", bgElevated: "elevated", bgElevatedHover: "hover", bgSubtle: "subtle",
		borderDefault: "border", borderSubtle: "divider", accent: "accent", onAccent: "on-accent",
		textPrimary: "primary", textSecondary: "secondary", textTertiary: "tertiary", textFaint: "faint", fontMono: "mono",
		blue: "blue", green: "green", purple: "purple", orange: "orange", amber: "amber",
		tintBlue: "blue-tint", tintGreen: "green-tint", tintPurple: "purple-tint", tintOrange: "orange-tint", tintAmber: "amber-tint",
	};
	return { useTheme: () => theme, useThemedStyles: (factory: (value: typeof theme) => unknown) => factory(theme) };
});
vi.mock("./worker-action-model", () => ({ workerContextActions: () => [] }));
vi.mock("./worker-row-actions", () => ({ WorkerRowActions: "WorkerRowActions" }));
vi.mock("./worker-row-interaction", () => ({ WorkerRowInteraction: ({ children }: { children: React.ReactNode }) => <>{children}</> }));
vi.mock("./ui", () => ({ ListSectionHeader: "ListSectionHeader", Spinning: ({ children }: { children: React.ReactNode }) => <>{children}</> }));
vi.mock("./store", () => ({ useApp: () => ({ ...boardStore, ...useContext(appContext) }) }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => false }));
vi.mock("react-native-reanimated", () => ({
	default: { FlatList: TestList },
	LayoutAnimationConfig: ({ children }: { children: React.ReactNode }) => <>{children}</>,
	LinearTransition: { duration: () => ({}) },
}));

function TestList({ ref, ...props }: { ref?: React.Ref<unknown>; data: {key: string}[]; renderItem: (value: {item: unknown}) => React.ReactNode }) {
	useImperativeHandle(ref, () => ({ scrollToOffset: counters.scrollToOffset }), []);
	return createElement("virtual-list", props, props.data.map(item => <Fragment key={item.key}>{props.renderItem({ item })}</Fragment>));
}
vi.mock("./pageNavigation", () => ({ useOpenPage: () => vi.fn() }));
vi.mock("./reviewView", () => ({ reviewRouteForSession: () => undefined }));

import { WorkerListRow } from "./worker-list-row";
import { WorkerBoardList } from "./worker-board-list";

const session: DashboardSession = {
	id: "worker-1",
	projectId: "project-1",
	status: "working",
	mode: "chat",
	branch: "feat/mobile",
	issueId: null,
	issueTitle: null,
	userPrompt: null,
	displayName: "Polish workers",
	summary: null,
	createdAt: "2026-10-08T00:00:00Z",
	lastActivityAt: "2026-10-08T00:00:00Z",
};

describe("WorkerListRow", () => {
	// FlatList's default window (21 viewports) kept ~200 rows mounted, so opening
	// a long Archive built them all at once and stalled the board.
	it("keeps a bounded scroll window and does not enable clipping", () => {
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<WorkerBoardList sessions={[session]} contentBottomInset={0} refreshing={false} onRefresh={vi.fn()} />); });
		const list = renderer.root.findByType("virtual-list" as never);
		expect(list.props.windowSize).toBe(7);
		expect(list.props.maxToRenderPerBatch).toBe(8);
		expect(list.props.initialNumToRender).toBe(12);
		expect(list.props.removeClippedSubviews).toBe(false);
		act(() => renderer.unmount());
	});
	it("keeps the rendered list data when focus and foreground do not change its order", () => {
		const sessions = [session];
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<WorkerBoardList sessions={sessions} contentBottomInset={0} refreshing={false} onRefresh={vi.fn()} />); });
		const before = renderer.root.findByType("virtual-list" as never).props.data;
		act(() => counters.focus());
		expect(renderer.root.findByType("virtual-list" as never).props.data).toBe(before);
		act(() => counters.foreground("active"));
		expect(renderer.root.findByType("virtual-list" as never).props.data).toBe(before);
		act(() => renderer.unmount());
	});
	it("accepts pending order without animating recycled cells or scrolling during the reorder", () => {
		const a = { ...session, id: "a", hostId: "mac", lastEventAt: "2026-10-08T02:00:00Z" };
		const b = { ...session, id: "b", hostId: "mac", lastEventAt: "2026-10-08T01:00:00Z" };
		const scrollToOffset = counters.scrollToOffset.mockClear();
		const listRef = { current: null } as never;
		const render = (sessions: DashboardSession[]) => <WorkerBoardList sessions={sessions} listRef={listRef} contentBottomInset={0} refreshing={false} onRefresh={vi.fn()} />;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(render([a, b])); });
		act(() => { renderer.update(render([a, { ...b, lastEventAt: "2026-10-08T03:00:00Z" }])); });
		const pill = renderer.root.findAllByType("Pressable" as never).find(node => node.props.accessibilityLabel?.includes("updated"))!;
		expect(pill).toBeTruthy();
		act(() => { pill.props.onPress(); });
		const list = renderer.root.findByType("virtual-list" as never);
		expect(list.props.data.filter((row: {kind: string}) => row.kind === "session").map((row: {session: DashboardSession}) => row.session.id)).toEqual(["b", "a"]);
		expect(list.props.itemLayoutAnimation).toBeUndefined();
		expect(scrollToOffset).toHaveBeenCalledWith({ offset: 0, animated: false });
		act(() => renderer.unmount());
	});

	it("does not show an update pill when another host hydrates 75 existing workers", () => {
		const local = { ...session, hostId: "mac", lastEventAt: "2026-10-08T02:00:00Z" };
		const remote = Array.from({ length: 75 }, (_, index) => ({ ...session, id: `dev-${index}`, hostId: "dev", lastEventAt: index < 35 ? "2026-10-08T03:00:00Z" : "2026-10-08T01:00:00Z" }));
		const render = (sessions: DashboardSession[]) => <WorkerBoardList sessions={sessions} contentBottomInset={0} refreshing={false} onRefresh={vi.fn()} />;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(render([local])); });
		act(() => { renderer.update(render([local, ...remote])); });
		expect(renderer.root.findAllByType("Pressable" as never).filter(node => node.props.accessibilityLabel?.includes("updated"))).toHaveLength(0);
		act(() => renderer.unmount());
	});
	it("keeps the board data and bound worker actions stable across notification publications", () => {
		const sessions = Array.from({ length: 120 }, (_, index) => ({ ...session, id: String(index) }));
		const board = <WorkerBoardList sessions={sessions} contentBottomInset={0} refreshing={false} onRefresh={vi.fn()} />;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<appContext.Provider value={{ currentHostId: "host-1", notificationsUnread: 0 }}>{board}</appContext.Provider>); });
		const before = renderer.root.findByType("virtual-list" as never).props;
		counters.renders = 0;
		act(() => { renderer.update(<appContext.Provider value={{ currentHostId: "host-1", notificationsUnread: 3 }}>{board}</appContext.Provider>); });
		const after = renderer.root.findByType("virtual-list" as never).props;
		expect(after.data).toBe(before.data);
		expect(after.renderItem).toBe(before.renderItem);
		expect(counters.renders).toBe(0);
		expect(after.itemLayoutAnimation).toBeUndefined();
		act(() => renderer.unmount());
	});
	it("does not redraw unchanged workers when the app notification count changes", () => {
		const props = {
			session, projectName: "Project", isRenaming: false, nowBucket: 1,
			onSwipeOpen: vi.fn(), onSwipeClose: vi.fn(), onRenameStart: vi.fn(), onRenameCancel: vi.fn(),
			onRename: vi.fn(async () => {}), onSetPinned: vi.fn(async () => {}),
			onDelete: vi.fn(), onResume: vi.fn(), onRestore: vi.fn(),
		};
		const rows = Array.from({ length: 120 }, (_, index) => <WorkerListRow key={index} {...props} />);
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<appContext.Provider value={{ currentHostId: "host-1", notificationsUnread: 0 }}>{rows}</appContext.Provider>); });
		counters.renders = 0;
		act(() => { renderer.update(<appContext.Provider value={{ currentHostId: "host-1", notificationsUnread: 3 }}>{rows}</appContext.Provider>); });
		expect(counters.renders).toBe(0);
		act(() => renderer.unmount());
	});
	it("places a distinct machine name and symbol on the bottom metadata line", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(
				<WorkerListRow
					session={session}
					projectName="Agent Orchestrator"
					machine={{ name: "A very long development machine name", appearance: { symbol: "server" } }}
					isRenaming={false}
					nowBucket={1}
					onSwipeOpen={vi.fn()}
					onSwipeClose={vi.fn()}
					onRenameStart={vi.fn()}
					onRenameCancel={vi.fn()}
					onRename={vi.fn(async () => {})}
					onSetPinned={vi.fn(async () => {})}
					onDelete={vi.fn()}
					onResume={vi.fn()}
					onRestore={vi.fn()}
				/>,
			);
		});

		expect(renderer.root.findByProps({ testID: "worker-machine-identity" })).toBeTruthy();
		expect(renderer.root.findByProps({ testID: "machine-identity-name" }).props.children).toBe("A very long development machine name");
		expect(renderer.root.findByProps({ testID: "machine-identity-symbol" }).props.appearance).toEqual({ symbol: "server" });
	});
});

describe("archived workers", () => {
	const live = { ...session, id: "live", hostId: "mac" };
	// The archive is ordered by updatedAt, newest first, as the daemon pages it.
	const archivedA = { ...session, id: "old-a", hostId: "mac", hostName: "Mac", isTerminated: true, status: "terminated", updatedAt: "2026-10-08T02:00:00Z" };
	const archivedB = { ...session, id: "old-b", hostId: "mac", hostName: "Mac", isTerminated: true, status: "terminated", updatedAt: "2026-10-08T01:00:00Z" };
	const board = (sessions: DashboardSession[], query = "") => (
		<WorkerBoardList sessions={sessions} query={query} initialArchiveOpen contentBottomInset={0} refreshing={false} onRefresh={vi.fn()} />
	);
	const restoreButtons = (renderer: ReactTestRenderer) =>
		renderer.root.findAllByType("Pressable" as never).filter((node) => node.props.accessibilityLabel?.startsWith("Restore "));
	const flush = async () => { await act(async () => { await Promise.resolve(); }); };

	// Desktop's archive card has one action, Restore, and does not open the
	// session. Matching it here is what keeps an open Archive cheap: no menu,
	// swipe container or gesture handlers behind every archived row.
	it("renders archived sessions read-only with Restore as their only action", () => {
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([live, archivedA, archivedB])); });
		const rows = renderer.root.findByType("virtual-list" as never).props.data as { kind: string; session?: DashboardSession }[];
		expect(rows.filter((row) => row.kind === "archived").map((row) => row.session!.id)).toEqual(["old-a", "old-b"]);
		expect(rows.filter((row) => row.kind === "session").map((row) => row.session!.id)).toEqual(["live"]);
		expect(restoreButtons(renderer)).toHaveLength(2);
		expect(renderer.root.findAll((node) => node.props.testID === "archived-machine-identity" && typeof node.type === "string")).toHaveLength(2);
		act(() => renderer.unmount());
	});

	it("keeps an archived match read-only inside search results", () => {
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([live, archivedA], "polish")); });
		const rows = renderer.root.findByType("virtual-list" as never).props.data as { kind: string }[];
		expect(rows.map((row) => row.kind)).toEqual(["header", "session", "archived"]);
		act(() => renderer.unmount());
	});

	it("restores on its host and then opens the session", async () => {
		router.push.mockClear();
		boardStore.restore.mockReset().mockResolvedValue(undefined);
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([archivedA])); });
		act(() => { restoreButtons(renderer)[0].props.onPress(); });
		await flush();
		expect(boardStore.restore).toHaveBeenCalledWith("old-a", "mac");
		expect(router.push).toHaveBeenCalledWith({
			pathname: "/session/[id]",
			params: { id: "old-a", projectId: "project-1", hostId: "mac" },
		});
		act(() => renderer.unmount());
	});

	it("shows a failed restore under its own row and does not navigate", async () => {
		router.push.mockClear();
		boardStore.restore.mockReset().mockRejectedValue(new Error("Lab: restore failed on purpose."));
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([archivedA, archivedB])); });
		act(() => { restoreButtons(renderer)[1].props.onPress(); });
		await flush();
		const alerts = renderer.root.findAll((node) => node.props.accessibilityRole === "alert" && typeof node.type === "string");
		expect(alerts).toHaveLength(1);
		expect(router.push).not.toHaveBeenCalled();
		// The control is usable again for a retry.
		expect(restoreButtons(renderer).every((button) => !button.props.disabled)).toBe(true);
		act(() => renderer.unmount());
	});

	// Desktop disables Restore while the machine is disconnected: the request
	// could not reach it, and the failure would only say so after a timeout.
	it("disables Restore while the session's machine is offline", () => {
		boardStore.restore.mockReset();
		const other = { ...archivedB, hostId: "dev", hostName: "Dev" };
		let renderer!: ReactTestRenderer;
		const scoped = (hostStates: { hostId: string; connection: string }[]) => (
			<appContext.Provider value={{ currentHostId: "host-1", notificationsUnread: 0, hostStates } as never}>{board([archivedA, other])}</appContext.Provider>
		);
		act(() => { renderer = create(scoped([{ hostId: "mac", connection: "closed" }, { hostId: "dev", connection: "open" }])); });
		const [offline, online] = restoreButtons(renderer);
		expect(offline.props.disabled).toBe(true);
		expect(offline.props.accessibilityHint).toBe("Unavailable while this machine is offline.");
		expect(online.props.disabled).toBe(false);
		act(() => { offline.props.onPress(); });
		expect(boardStore.restore).not.toHaveBeenCalled();
		// Reconnecting brings it back.
		act(() => { renderer.update(scoped([{ hostId: "mac", connection: "open" }, { hostId: "dev", connection: "open" }])); });
		expect(restoreButtons(renderer).map((button) => button.props.disabled)).toEqual([false, false]);
		act(() => renderer.unmount());
	});

	it("uses the board's own connection for a row without a host id", () => {
		const scopedSession = { ...session, id: "scoped", isTerminated: true, status: "terminated" };
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(
				<appContext.Provider value={{ currentHostId: "host-1", notificationsUnread: 0, connection: "connecting" } as never}>{board([scopedSession])}</appContext.Provider>,
			);
		});
		expect(restoreButtons(renderer)[0].props.disabled).toBe(true);
		act(() => renderer.unmount());
	});

	// Tabs keep visited screens mounted, so leaving the board for another tab or
	// session blurs it without unmounting. A restore that finishes then must not
	// pull the user back, and must not leave Restore disabled either.
	it("does not open a restored session after the board loses focus", async () => {
		router.push.mockClear();
		let finish!: () => void;
		boardStore.restore.mockReset().mockImplementation(() => new Promise<void>((resolve) => { finish = resolve; }));
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([archivedA])); });
		let blur!: () => void;
		act(() => { blur = (counters.focus as () => () => void)(); });
		act(() => { restoreButtons(renderer)[0].props.onPress(); });
		act(() => blur());
		await act(async () => { finish(); await Promise.resolve(); });
		expect(boardStore.restore).toHaveBeenCalledTimes(1);
		expect(router.push).not.toHaveBeenCalled();
		expect(restoreButtons(renderer)[0].props.disabled).toBe(false);
		act(() => renderer.unmount());
	});

	it("runs one restore at a time", async () => {
		let finish!: () => void;
		boardStore.restore.mockReset().mockImplementation(() => new Promise<void>((resolve) => { finish = resolve; }));
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([archivedA, archivedB])); });
		act(() => { restoreButtons(renderer)[0].props.onPress(); });
		expect(restoreButtons(renderer).map((button) => button.props.disabled)).toEqual([true, true]);
		expect(restoreButtons(renderer)[0].props.accessibilityState).toEqual({ disabled: true, busy: true });
		act(() => { restoreButtons(renderer)[1].props.onPress(); });
		expect(boardStore.restore).toHaveBeenCalledTimes(1);
		await act(async () => { finish(); await Promise.resolve(); });
		expect(restoreButtons(renderer).map((button) => button.props.disabled)).toEqual([false, false]);
		act(() => renderer.unmount());
	});
});

describe("a machine that pages its archive", () => {
	const live = { ...session, id: "live", hostId: "mac", hostName: "Mac" };
	const arch = (id: string, day: number) => ({ ...session, id, displayName: `Archived ${id}`, isTerminated: true, status: "terminated", updatedAt: `2026-10-0${day}T00:00:00Z` });
	const source = (total: number) => [{ hostId: "mac", host: { name: "Mac", appearance: { symbol: "server" as const } }, summary: { total, byProject: { "project-1": total } } }];
	const board = (sessions: DashboardSession[], sources = source(3), query = "") => (
		<WorkerBoardList sessions={sessions} archiveSources={sources} query={query} contentBottomInset={0} refreshing={false} onRefresh={vi.fn()} />
	);
	const list = (renderer: ReactTestRenderer) => renderer.root.findByType("virtual-list" as never);
	const rows = (renderer: ReactTestRenderer) => list(renderer).props.data as { kind: string; session?: DashboardSession }[];
	const archivedIds = (renderer: ReactTestRenderer) => rows(renderer).filter((row) => row.kind === "archived").map((row) => row.session!.id);
	const archiveHeader = (renderer: ReactTestRenderer) =>
		renderer.root.findAllByType("Pressable" as never).find((node) => node.props.accessibilityLabel?.startsWith("Archive,"))!;
	const answer = async (page: { sessions: unknown[]; nextCursor: string | null }) => {
		await act(async () => { archivePages.requests.shift()!.resolve(page); });
	};

	// The poll no longer carries the archive, only its size; the rows are
	// fetched a page at a time, and only once someone opens it.
	it("counts the archive from the poll and fetches pages only once Archive opens", async () => {
		archivePages.requests.length = 0;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([live])); });
		expect(archiveHeader(renderer).props.accessibilityLabel).toBe("Archive, 3 sessions");
		expect(archivePages.requests).toHaveLength(0);

		act(() => { archiveHeader(renderer).props.onPress(); });
		expect(archivePages.requests).toEqual([expect.objectContaining({ hostId: "mac", cursor: null })]);
		await answer({ sessions: [arch("a3", 3), arch("a2", 2)], nextCursor: "after-a2" });
		expect(archivedIds(renderer)).toEqual(["a3", "a2"]);
		expect(rows(renderer).at(-1)?.kind).toBe("archive-status");
		// Archived rows carry their machine, like the live rows around them.
		expect(rows(renderer).find((row) => row.kind === "archived")?.session).toMatchObject({ hostId: "mac", hostName: "Mac" });

		act(() => { list(renderer).props.onEndReached(); });
		expect(archivePages.requests).toEqual([expect.objectContaining({ cursor: "after-a2" })]);
		await answer({ sessions: [arch("a1", 1)], nextCursor: null });
		expect(archivedIds(renderer)).toEqual(["a3", "a2", "a1"]);
		expect(rows(renderer).some((row) => row.kind === "archive-status")).toBe(false);
		act(() => renderer.unmount());
	});

	// The 1,000-row cap bounds a search, which reads the archive by itself.
	// Browsing pages only as far as the user scrolls, so it must never stop there.
	it("keeps paging past 1,000 rows while browsing", async () => {
		archivePages.requests.length = 0;
		const base = Date.parse("2026-10-01T00:00:00Z");
		const page = (index: number) => Array.from({ length: 50 }, (_, row) => {
			const n = index * 50 + row;
			return { ...session, id: `old-${String(n).padStart(4, "0")}`, isTerminated: true, status: "terminated", updatedAt: new Date(base - n * 60_000).toISOString() };
		});
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([live], source(1200))); });
		act(() => { archiveHeader(renderer).props.onPress(); });
		for (let index = 0; index < 20; index++) {
			if (index > 0) act(() => { list(renderer).props.onEndReached(); });
			expect(archivePages.requests).toHaveLength(1);
			await answer({ sessions: page(index), nextCursor: `after-${index}` });
		}
		expect(archivedIds(renderer)).toHaveLength(1000);
		act(() => { list(renderer).props.onEndReached(); });
		expect(archivePages.requests).toEqual([expect.objectContaining({ cursor: "after-19" })]);
		expect(rows(renderer).at(-1)?.kind).toBe("archive-status");
		act(() => renderer.unmount());
	});

	// A count change that lands while a page is in flight cannot refresh then;
	// it must when that request settles, or a newly archived session stays
	// missing until the count moves again.
	it("refreshes the archive once an in-flight page settles after the count moved", async () => {
		archivePages.requests.length = 0;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([live], source(3))); });
		act(() => { archiveHeader(renderer).props.onPress(); });
		await answer({ sessions: [arch("a3", 3), arch("a2", 2)], nextCursor: "after-a2" });
		act(() => { list(renderer).props.onEndReached(); });
		expect(archivePages.requests).toEqual([expect.objectContaining({ cursor: "after-a2" })]);
		const nextPage = archivePages.requests.shift()!;
		act(() => { renderer.update(board([live], source(4))); });
		expect(archivePages.requests).toHaveLength(0);
		await act(async () => { nextPage.resolve({ sessions: [arch("a1", 1)], nextCursor: null }); });
		expect(archivePages.requests).toEqual([expect.objectContaining({ cursor: null })]);
		await answer({ sessions: [arch("a4", 4), arch("a3", 3), arch("a2", 2), arch("a1", 1)], nextCursor: null });
		expect(archivedIds(renderer)).toEqual(["a4", "a3", "a2", "a1"]);
		act(() => renderer.unmount());
	});

	// Sessions deleted on desktop never come back as live rows, so only the
	// archive's own refresh can drop them from pages kept from before.
	const numbered = (from: number, count: number) => Array.from({ length: count }, (_, index) => {
		const n = from + index;
		return { ...session, id: `old-${String(n).padStart(4, "0")}`, isTerminated: true, status: "terminated", updatedAt: new Date(Date.parse("2026-10-01T00:00:00Z") - n * 60_000).toISOString() };
	});
	it("drops a session deleted on desktop when the refreshed first page is the whole archive", async () => {
		archivePages.requests.length = 0;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([live], source(51))); });
		act(() => { archiveHeader(renderer).props.onPress(); });
		await answer({ sessions: numbered(0, 50), nextCursor: "after-49" });
		act(() => { list(renderer).props.onEndReached(); });
		await answer({ sessions: numbered(50, 1), nextCursor: null });
		expect(archivedIds(renderer)).toHaveLength(51);
		// The oldest is deleted on desktop; the poll reports 50.
		act(() => { renderer.update(board([live], source(50))); });
		expect(archivePages.requests).toEqual([expect.objectContaining({ cursor: null })]);
		await answer({ sessions: numbered(0, 50), nextCursor: null });
		expect(archivedIds(renderer)).toHaveLength(50);
		expect(archivedIds(renderer)).not.toContain("old-0050");
		act(() => renderer.unmount());
	});

	it("reloads pages below the first when the archive shrank, instead of keeping stale rows", async () => {
		archivePages.requests.length = 0;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([live], source(120))); });
		act(() => { archiveHeader(renderer).props.onPress(); });
		await answer({ sessions: numbered(0, 50), nextCursor: "after-49" });
		act(() => { list(renderer).props.onEndReached(); });
		await answer({ sessions: numbered(50, 50), nextCursor: "after-99" });
		act(() => { list(renderer).props.onEndReached(); });
		await answer({ sessions: numbered(100, 20), nextCursor: null });
		expect(archivedIds(renderer)).toHaveLength(120);
		// old-0075 is deleted on desktop: the count drops to 119.
		act(() => { renderer.update(board([live], source(119))); });
		await answer({ sessions: numbered(0, 50), nextCursor: "after-49" });
		expect(archivedIds(renderer)).toHaveLength(50);
		expect(rows(renderer).at(-1)?.kind).toBe("archive-status");
		act(() => { list(renderer).props.onEndReached(); });
		expect(archivePages.requests).toEqual([expect.objectContaining({ cursor: "after-49" })]);
		await answer({ sessions: [...numbered(50, 25), ...numbered(76, 25)], nextCursor: "after-100" });
		expect(archivedIds(renderer)).not.toContain("old-0075");
		act(() => renderer.unmount());
	});

	// More than a page archived since the last refresh: page one no longer
	// reaches the rows loaded before, so keeping them with the old end state
	// would leave sessions in between that nothing ever loads.
	it("follows the refreshed cursor when the new first page does not reach the loaded rows", async () => {
		archivePages.requests.length = 0;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([live], source(1))); });
		act(() => { archiveHeader(renderer).props.onPress(); });
		await answer({ sessions: numbered(1000, 1), nextCursor: null });
		expect(archivedIds(renderer)).toEqual(["old-1000"]);
		// 51 newer sessions are archived while the phone is in the background.
		act(() => { renderer.update(board([live], source(52))); });
		expect(archivePages.requests).toEqual([expect.objectContaining({ cursor: null })]);
		await answer({ sessions: numbered(0, 50), nextCursor: "after-0049" });
		expect(archivedIds(renderer)).toHaveLength(50);
		expect(rows(renderer).at(-1)?.kind).toBe("archive-status");
		act(() => { list(renderer).props.onEndReached(); });
		expect(archivePages.requests).toEqual([expect.objectContaining({ cursor: "after-0049" })]);
		await answer({ sessions: [...numbered(50, 1), ...numbered(1000, 1)], nextCursor: null });
		expect(archivedIds(renderer)).toHaveLength(52);
		expect(archivedIds(renderer).slice(-2)).toEqual(["old-0050", "old-1000"]);
		act(() => renderer.unmount());
	});

	it("drops a restored session from the archive as soon as it is live again", async () => {
		archivePages.requests.length = 0;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([live])); });
		act(() => { archiveHeader(renderer).props.onPress(); });
		await answer({ sessions: [arch("a2", 2), arch("a1", 1)], nextCursor: null });
		const restored = { ...arch("a2", 2), hostId: "mac", hostName: "Mac", isTerminated: false, status: "idle" };
		act(() => { renderer.update(board([live, restored])); });
		expect(archivedIds(renderer)).toEqual(["a1"]);
		act(() => renderer.unmount());
	});

	it("reads every page while searching so an archived match can be found", async () => {
		archivePages.requests.length = 0;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([live], source(3), "archived")); });
		expect(archivePages.requests).toHaveLength(1);
		await answer({ sessions: [arch("a3", 3), arch("a2", 2)], nextCursor: "after-a2" });
		expect(archivePages.requests).toEqual([expect.objectContaining({ cursor: "after-a2" })]);
		await answer({ sessions: [arch("a1", 1)], nextCursor: null });
		expect(archivedIds(renderer)).toEqual(["a3", "a2", "a1"]);
		act(() => renderer.unmount());
	});

	// A search with no match yet must not read as "no results" when the archive
	// could not be read: the error and Retry stay until it succeeds.
	it("keeps a failed archive search's Retry when nothing matched", async () => {
		archivePages.requests.length = 0;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([live], source(3), "only-in-the-archive")); });
		expect(archivePages.requests).toHaveLength(1);
		await act(async () => { archivePages.requests.shift()!.reject(new Error("socket hang up")); });
		const retry = renderer.root.findAllByType("Pressable" as never).find((node) => node.props.accessibilityLabel === "Retry loading archived sessions");
		expect(retry).toBeTruthy();
		expect(rows(renderer).map((row) => row.kind)).toEqual(["header", "archive-status"]);
		act(() => { retry!.props.onPress(); });
		expect(archivePages.requests).toHaveLength(1);
		act(() => renderer.unmount());
	});

	it("offers a retry when a page fails", async () => {
		archivePages.requests.length = 0;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(board([live])); });
		act(() => { archiveHeader(renderer).props.onPress(); });
		await act(async () => { archivePages.requests.shift()!.reject(new Error("socket hang up")); });
		const retry = renderer.root.findAllByType("Pressable" as never).find((node) => node.props.accessibilityLabel === "Retry loading archived sessions");
		expect(retry).toBeTruthy();
		act(() => { retry!.props.onPress(); });
		expect(archivePages.requests).toHaveLength(1);
		await answer({ sessions: [arch("a1", 1)], nextCursor: null });
		expect(archivedIds(renderer)).toEqual(["a1"]);
		act(() => renderer.unmount());
	});
});
