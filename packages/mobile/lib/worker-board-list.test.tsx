import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "./api";
import { View } from "react-native";

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

import { updatesPillTop, WorkerBoardList } from "./worker-board-list";

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

describe("updatesPillTop", () => {
	it("keeps the pill below whichever is lower: the header's bottom or the bar", () => {
		expect(updatesPillTop({ overlayTopInset: 116, headerHeight: 52, scrollY: -116 })).toBe(176);
		expect(updatesPillTop({ overlayTopInset: 116, headerHeight: 52, scrollY: 200 })).toBe(124);
		expect(updatesPillTop({ overlayTopInset: 0, headerHeight: 0, scrollY: 0 })).toBe(8);
	});
});

describe("WorkerBoardList's pull to refresh", () => {
	const spinner = () => renderer.root.findByType("FlatList" as never).props.refreshControl.props;

	// UIKit already sets iOS's spinner under the large-title bar. An offset
	// moved it a whole header height lower, onto the rows, and it jumped as the
	// bar's height changed during the pull.
	it("leaves iOS's spinner where UIKit puts it, even under a clear header", () => {
		act(() => { renderer = create(board([older], { overlayTopInset: 152 })); });
		expect(spinner().progressViewOffset).toBeUndefined();
	});

	it("starts Android's spinner below the bar the list runs under", () => {
		fixtures.platform = "android";
		act(() => { renderer = create(board([older], { overlayTopInset: 80 })); });
		expect(spinner().progressViewOffset).toBe(80);
	});
});

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

	// #7982: the pill sat at the bar's bottom, which is where the machine chip
	// row rests under the large title, and hid the chosen machine.
	describe("placement", () => {
		const top = () => [pill()!.props.style({ pressed: false })].flat().find((style: { top?: number }) => style && style.top !== undefined)?.top;
		const header = <View testID="chips" />;
		const scroll = (y: number) => act(() => renderer.root.findByType("FlatList" as never).props.onScroll({ nativeEvent: { contentOffset: { y } } }));
		const layoutHeader = (height: number) => act(() => renderer.root.findByProps({ testID: "chips" }).parent!.props.onLayout({ nativeEvent: { layout: { height } } }));

		it("rests below iOS's header rows, not over the chips under the large title", () => {
			holdNews({ overlayTopInset: 116, ListHeaderComponent: header });
			act(() => renderer.root.findByType("SafeAreaListener" as never).props.onChange({ insets: { top: 116, bottom: 83 }, frame: { height: 852 } }));
			layoutHeader(52);
			expect(top()).toBe(116 + 52 + 8);
		});

		it("follows the header up as the list scrolls, then stays below the bar", () => {
			holdNews({ overlayTopInset: 116, ListHeaderComponent: header });
			layoutHeader(52);
			scroll(-116 + 30);
			expect(top()).toBe(116 + 52 - 30 + 8);
			scroll(400);
			expect(top()).toBe(116 + 8);
		});

		it("rests below Android's header, whose bar spacer is inside it", () => {
			fixtures.platform = "android";
			holdNews({ overlayTopInset: 64, ListHeaderComponent: header });
			layoutHeader(64 + 52);
			expect(top()).toBe(64 + 52 + 8);
		});

		it("sits just below the bar when the page has no header rows", () => {
			holdNews({ overlayTopInset: 116 });
			expect(top()).toBe(116 + 8);
		});
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
