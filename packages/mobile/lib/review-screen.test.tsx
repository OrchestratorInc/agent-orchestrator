import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { DashboardSession, SessionReviews } from "./api";

const fixtures = vi.hoisted(() => ({
	config: { host: "dev.local", httpPort: "3011", muxPort: "3012", secure: false, password: "secret", hostId: "dev" },
	sessions: [] as DashboardSession[],
	getSession: vi.fn(),
	reviewerSurface: undefined as undefined | { mode: string; reviewId: string; harness: string },
	restore: vi.fn(async (_id: string, _hostId?: string) => {}),
	theme: new Proxy({}, { get: () => "#000000" }),
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"), Alert: { alert: vi.fn() }, Platform: { OS: "ios", select: (options: { ios?: unknown; default?: unknown }) => options.ios ?? options.default },
		Pressable: ({ children, style, ...props }: { children?: React.ReactNode | ((state: { pressed: boolean }) => React.ReactNode); style?: unknown }) =>
			React.createElement("Pressable", { ...props, style: typeof style === "function" ? style({ pressed: false }) : style }, typeof children === "function" ? children({ pressed: false }) : children),
		RefreshControl: host("RefreshControl"), ScrollView: host("ScrollView"), Text: host("Text"), View: host("View"),
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
	};
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }) }));
vi.mock("@expo/vector-icons", () => ({ Feather: "Feather" }));
vi.mock("expo-router", async () => {
	const React = await import("react");
	return {
		useLocalSearchParams: () => ({ sessionId: "worker-1", prNumber: "12", prUrl: "https://github.com/acme/repo/pull/12", hostId: "dev" }),
		useFocusEffect: (effect: () => void) => React.useEffect(() => { effect(); }, [effect]),
		useNavigation: () => ({ setOptions: vi.fn(), getState: () => ({ routes: [], index: 0 }) }),
		useRouter: () => ({ push: vi.fn(), back: vi.fn(), canGoBack: () => false }),
	};
});
vi.mock("./api", () => ({
	getSessionReviews: vi.fn(async (): Promise<SessionReviews> => ({
		reviews: [{ prUrl: "https://github.com/acme/repo/pull/12", prNumber: 12, title: "Improve mobile review", targetSha: "abcdef123456", status: "needs_review" }],
		runs: [],
		reviewerSurface: fixtures.reviewerSurface,
	} as unknown as SessionReviews)),
	getSessionPR: vi.fn(async () => []),
	getSession: fixtures.getSession,
	sessionTitle: (session: DashboardSession) => session.displayName ?? session.id,
	cancelSessionReview: vi.fn(), killSessionReviewer: vi.fn(), mergeSessionPR: vi.fn(), restoreSessionReviewer: vi.fn(), sendMessage: vi.fn(), triggerSessionReview: vi.fn(),
}));
vi.mock("./store", () => ({
	HostScope: ({ children }: { children: unknown }) => children,
	useApp: () => ({ config: fixtures.config, sessions: fixtures.sessions, currentHostId: "dev", restore: fixtures.restore }),
}));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme) }));
vi.mock("./ui", () => ({ Button: "Button", Dot: "Dot", EmptyState: "EmptyState", ListSectionHeader: "ListSectionHeader" }));
vi.mock("./AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("./SwipeToMerge", () => ({ SwipeToMerge: "SwipeToMerge" }));
vi.mock("./item-actions-menu", () => ({ ItemActionsMenu: "ItemActionsMenu" }));
vi.mock("./chat/ChatMarkdown", () => ({ ChatMarkdown: "ChatMarkdown" }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn() } }));
vi.mock("./openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("./pageNavigation", () => ({ useOpenPage: () => vi.fn() }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));

import ReviewDetailScreen from "../app/review/[sessionId]";

const worker = (over: Partial<DashboardSession> = {}) => ({
	id: "worker-1", projectId: "repo", status: "idle", harness: "claude-code", displayName: "Fix the PR page", ...over,
}) as DashboardSession;

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.sessions = [];
	fixtures.getSession.mockReset();
	fixtures.restore.mockReset();
	fixtures.reviewerSurface = undefined;
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

async function render() {
	await act(async () => { renderer = create(<ReviewDetailScreen />); });
	// Let the reviews load and the worker read settle.
	await act(async () => { await Promise.resolve(); });
}
const pill = (label: string) => renderer!.root.findAll((node) => (node.type as unknown) === "Pressable" && node.props.accessibilityLabel === label);
// The row's own label goes on to name the worker; the pill's stops at "Open worker".
const workerRow = () => renderer!.root.findAll((node) => (node.type as unknown) === "Pressable" && String(node.props.accessibilityLabel).startsWith("Open worker "));
const alerts = () => renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.accessibilityRole === "alert").map((node) => node.props.children);
const outlined = (node: { props: { style?: unknown } }) => [node.props.style].flat(Infinity).some((style) => (style as { borderWidth?: number } | null)?.borderWidth === 1);

it("reads an archived worker the board poll leaves out and offers Restore", async () => {
	fixtures.getSession.mockResolvedValue(worker({ status: "terminated", isTerminated: true }));
	await render();

	expect(fixtures.getSession).toHaveBeenCalledExactlyOnceWith(fixtures.config, "worker-1");
	expect(workerRow()).toHaveLength(1);
	expect(pill("Restore worker")).toHaveLength(1);
	await act(async () => { pill("Restore worker")[0].props.onPress(); });
	expect(fixtures.restore).toHaveBeenCalledExactlyOnceWith("worker-1", "dev");
});

it("uses the board's copy of a live worker without reading it again", async () => {
	fixtures.sessions = [worker({ status: "working" })];
	await render();

	expect(fixtures.getSession).not.toHaveBeenCalled();
	expect(workerRow()).toHaveLength(1);
	expect(pill("Restore worker")).toHaveLength(0);
	expect(pill("Open worker")).toHaveLength(1);
});

it("leaves the Worker section out when the worker cannot be read", async () => {
	fixtures.getSession.mockRejectedValue(new Error("not found"));
	await render();

	expect(workerRow()).toHaveLength(0);
	expect(renderer!.root.findAll((node) => (node.type as unknown) === "ListSectionHeader" && node.props.label === "Worker")).toHaveLength(0);
});

it("reads the worker again when it leaves the poll, not an earlier live answer", async () => {
	// Cold start: the page mounts before the first poll, and reads the worker live.
	fixtures.getSession.mockResolvedValueOnce(worker({ status: "working" }));
	await render();
	expect(pill("Open worker")).toHaveLength(1);

	// The poll lands with the worker listed.
	fixtures.sessions = [worker({ status: "working" })];
	await act(async () => { renderer!.update(<ReviewDetailScreen />); });
	expect(fixtures.getSession).toHaveBeenCalledTimes(1);

	// Later the worker is archived and drops out of the poll.
	fixtures.sessions = [];
	fixtures.getSession.mockResolvedValueOnce(worker({ status: "terminated", isTerminated: true }));
	await act(async () => { renderer!.update(<ReviewDetailScreen />); });
	await act(async () => { await Promise.resolve(); });

	expect(fixtures.getSession).toHaveBeenCalledTimes(2);
	expect(pill("Restore worker")).toHaveLength(1);
	expect(pill("Open worker")).toHaveLength(0);
});

it("shows a failed restore under the Worker row, and a reload does not clear it", async () => {
	fixtures.getSession.mockResolvedValue(worker({ status: "terminated", isTerminated: true }));
	fixtures.restore.mockRejectedValue(new Error("The worktree is gone."));
	await render();

	await act(async () => { pill("Restore worker")[0].props.onPress(); });
	expect(alerts()).toEqual(["The worktree is gone."]);

	// A page load clears the page-wide error; the worker's stays.
	const scroll = renderer!.root.find((node) => (node.type as unknown) === "ScrollView");
	await act(async () => { await scroll.props.refreshControl.props.onRefresh(); });
	expect(alerts()).toEqual(["The worktree is gone."]);
});

it("names each Open for screen readers, and fills the reviewer's only without a Worker row", async () => {
	fixtures.reviewerSurface = { mode: "chat", reviewId: "review-1", harness: "codex" };
	fixtures.sessions = [worker()];
	await render();
	expect(pill("Open")).toHaveLength(0);
	expect(pill("Open worker")).toHaveLength(1);
	expect(outlined(pill("Open reviewer")[0])).toBe(true);

	act(() => renderer!.unmount());
	fixtures.sessions = [];
	fixtures.getSession.mockRejectedValue(new Error("not found"));
	await render();
	expect(workerRow()).toHaveLength(0);
	expect(outlined(pill("Open reviewer")[0])).toBe(false);
});
