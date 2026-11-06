import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "./api";

const fixtures = vi.hoisted(() => ({
	platform: "ios",
	scrollToOffset: vi.fn(),
	theme: new Proxy({}, { get: (_, key) => `#${String(key)}` }),
}));

vi.mock("expo-router", async () => {
	const React = await import("react");
	return {
		useRouter: () => ({ push: vi.fn() }),
		useFocusEffect: (effect: () => void | (() => void)) => React.useEffect(effect, [effect]),
	};
});
vi.mock("react-native", async () => {
	const React = await import("react");
	// Renders its rows, so the pill and the order are on screen, and hands its
	// ref the one imperative call the board makes.
	const FlatList = React.forwardRef(function FlatList(
		{ data, renderItem, ListHeaderComponent, ...props }: { data: unknown[]; renderItem(info: { item: unknown }): React.ReactNode; ListHeaderComponent?: React.ReactNode },
		ref,
	) {
		React.useImperativeHandle(ref, () => ({ scrollToOffset: fixtures.scrollToOffset }));
		return React.createElement("FlatList", props, ListHeaderComponent, data.map((item, index) => React.createElement(React.Fragment, { key: index }, renderItem({ item }))));
	});
	return {
		Alert: { alert: vi.fn() },
		AppState: { addEventListener: () => ({ remove() {} }) },
		FlatList,
		Platform: { get OS() { return fixtures.platform; }, select: (choices: Record<string, unknown>) => choices[fixtures.platform] ?? choices.default },
		Pressable: "Pressable",
		RefreshControl: "RefreshControl",
		StyleSheet: { create: (styles: object) => styles, absoluteFill: {} },
		Text: "Text",
		View: "View",
		useWindowDimensions: () => ({ fontScale: 1 }),
	};
});
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	return { LayoutAnimationConfig: ({ children }: { children: React.ReactNode }) => React.createElement(React.Fragment, null, children) };
});
vi.mock("react-native-safe-area-context", () => ({ SafeAreaListener: "SafeAreaListener" }));
vi.mock("./store", () => ({ useApp: () => ({
	projects: [], allProjects: [], currentHostId: "mac", connection: "open", hostStates: [],
	kill: vi.fn(), renameWorker: vi.fn(), setWorkerPinned: vi.fn(), restore: vi.fn(), resumeAgent: vi.fn(),
}) }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme),
}));
vi.mock("./archive/useArchivedSessions", () => ({
	useArchivedSessions: () => ({ rows: [], total: 0, loading: false, error: null, hasMore: false, retry: vi.fn(), loadMore: vi.fn() }),
}));
vi.mock("./worker-list-row", () => ({ WorkerListRow: "WorkerListRow" }));
vi.mock("./archived-worker-row", () => ({ ArchivedWorkerRow: "ArchivedWorkerRow" }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./Spinner", () => ({ Spinner: "Spinner" }));
vi.mock("./ui", () => ({ ListSectionHeader: "ListSectionHeader" }));
vi.mock("./haptics", () => ({ haptics: { success: vi.fn(), error: vi.fn(), warning: vi.fn() } }));
vi.mock("./pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: {} }) }));
vi.mock("./refreshControlColors", () => ({ refreshControlColors: () => ({}) }));

import { WorkerBoardList } from "./worker-board-list";

let renderer: ReactTestRenderer;

function worker(id: string, lastEventAt: string): DashboardSession {
	return {
		id, projectId: "project-1", status: "working", mode: "chat", branch: null,
		issueId: null, issueTitle: null, userPrompt: null, displayName: null, summary: null,
		createdAt: "2026-11-01T00:00:00Z", lastActivityAt: lastEventAt, lastEventAt,
	} as DashboardSession;
}

function board(sessions: DashboardSession[], props: Partial<Parameters<typeof WorkerBoardList>[0]> = {}) {
	return <WorkerBoardList sessions={sessions} contentBottomInset={0} contentInsetAdjustmentBehavior="automatic" refreshing={false} onRefresh={() => {}} {...props} />;
}

function shownOrder(): string[] {
	return renderer.root.findAllByType("WorkerListRow" as never).map((row) => row.props.session.id);
}

function pill() {
	return renderer.root.findAllByType("Pressable" as never).find((node) => String(node.props.accessibilityLabel).endsWith("Show latest."));
}

const older = worker("older", "2026-11-01T01:00:00Z");
const newer = worker("newer", "2026-11-01T02:00:00Z");

/** Mounts the board, then lets the older worker take news while it is held. */
function holdNews(props: Partial<Parameters<typeof WorkerBoardList>[0]> = {}) {
	act(() => { renderer = create(board([older, newer], props)); });
	act(() => { renderer.update(board([worker("older", "2026-11-01T03:00:00Z"), newer], props)); });
}

beforeEach(() => {
	fixtures.platform = "ios";
	fixtures.scrollToOffset.mockClear();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
});
afterEach(() => { act(() => renderer?.unmount()); vi.unstubAllGlobals(); });

describe("WorkerBoardList's updated pill", () => {
	it("holds the order behind the pill until it is tapped", () => {
		holdNews();
		expect(shownOrder()).toEqual(["newer", "older"]);
		expect(pill()?.props.accessibilityLabel).toBe("1 updated. Show latest.");
		act(() => pill()!.props.onPress());
		expect(shownOrder()).toEqual(["older", "newer"]);
		expect(pill()).toBeUndefined();
	});

	it("scrolls to the top on a page that passes no list ref, as a project's page does", () => {
		fixtures.platform = "android";
		holdNews();
		act(() => pill()!.props.onPress());
		expect(fixtures.scrollToOffset).toHaveBeenCalledTimes(1);
		expect(fixtures.scrollToOffset).toHaveBeenCalledWith({ offset: 0, animated: false });
	});

	it("scrolls iOS's inset list to where it rests under a clear header, not under the bar", () => {
		holdNews();
		const listener = renderer.root.findByType("SafeAreaListener" as never);
		act(() => listener.props.onChange({ insets: { top: 116, bottom: 83 }, frame: { height: 852 } }));
		expect(renderer.root.findByType("FlatList" as never).props.scrollToOverflowEnabled).toBe(true);
		act(() => pill()!.props.onPress());
		expect(fixtures.scrollToOffset).toHaveBeenCalledWith({ offset: -116, animated: false });
	});

	it("scrolls once per tap and never again on a later re-sort", () => {
		holdNews();
		act(() => pill()!.props.onPress());
		expect(fixtures.scrollToOffset).toHaveBeenCalledTimes(1);
		// More news, accepted in place by the next poll: a new snapshot, no tap.
		act(() => { renderer.update(board([worker("older", "2026-11-01T04:00:00Z"), newer])); });
		expect(fixtures.scrollToOffset).toHaveBeenCalledTimes(1);
	});
});
