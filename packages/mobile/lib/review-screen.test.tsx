import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession, SessionReviews } from "./api";

const fixtures = vi.hoisted(() => ({
	config: { host: "dev.local", httpPort: "3011", muxPort: "3012", secure: false, password: "secret", hostId: "dev" },
	sessions: [] as DashboardSession[],
	getSession: vi.fn(),
	reviewerSurface: undefined as undefined | { mode: string; reviewId: string; harness: string },
	restore: vi.fn(async (_id: string, _hostId?: string) => {}),
	hostId: "dev",
	cloudClient: {} as Record<string, ReturnType<typeof vi.fn>>,
	theme: new Proxy({}, { get: () => "#000000" }),
	getSessionPR: vi.fn(async (): Promise<unknown[]> => []),
	loadPRDetails: vi.fn(async (): Promise<void> => {}),
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
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
// The Comments section renders for real; only its native leaves are stood in for.
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(async () => null), setItem: vi.fn(async () => {}), removeItem: vi.fn(async () => {}) } }));
vi.mock("expo-clipboard", () => ({ setStringAsync: vi.fn() }));
vi.mock("./AccountAvatar", () => ({ AccountAvatar: "AccountAvatar" }));
vi.mock("./chat/AutomationMessage", () => ({ GitHubComment: "GitHubComment", WorkerSessionsProvider: ({ children }: { children: unknown }) => children }));
vi.mock("./review-comment-filter", () => ({ ReviewCommentFilter: "ReviewCommentFilter" }));
vi.mock("./ReviewReplyDock", () => ({ ReviewReplyDock: "ReviewReplyDock" }));
vi.mock("./Spinner", () => ({ Spinner: "Spinner" }));
vi.mock("./pressFeedback", () => ({ usePressFeedback: () => ({ pressed: {}, android_ripple: undefined }) }));
vi.mock("./refreshControlColors", () => ({ refreshControlColors: () => ({}) }));
vi.mock("./divider", () => ({ rowDividerWidth: 1 }));
// The native button, as a host element that keeps its props.
vi.mock("./review-action-button", async () => {
	const React = await import("react");
	return { ReviewActionButton: (props: object) => React.createElement("ReviewActionButton", props) };
});
vi.mock("expo-router", async () => {
	const React = await import("react");
	const Toolbar = Object.assign(() => null, { Button: () => null });
	return {
		Stack: { Toolbar },
		useLocalSearchParams: () => ({ sessionId: "worker-1", prNumber: "12", prUrl: "https://github.com/acme/repo/pull/12", hostId: fixtures.hostId }),
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
	getSessionPR: fixtures.getSessionPR,
	loadPRDetails: fixtures.loadPRDetails,
	getSession: fixtures.getSession,
	sessionTitle: (session: DashboardSession) => session.displayName ?? session.id,
	cancelSessionReview: vi.fn(), killSessionReviewer: vi.fn(), mergeSessionPR: vi.fn(), restoreSessionReviewer: vi.fn(), sendMessage: vi.fn(), triggerSessionReview: vi.fn(),
}));
vi.mock("./store", () => ({
	HostScope: ({ children }: { children: unknown }) => children,
	useApp: () => ({ config: fixtures.hostId === "dev" ? fixtures.config : null, sessions: fixtures.sessions, currentHostId: fixtures.hostId, restore: fixtures.restore }),
}));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme) }));
vi.mock("./ui", () => ({ Button: "Button", Dot: "Dot", EmptyState: "EmptyState", ListSectionHeader: "ListSectionHeader" }));
vi.mock("./AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("./SwipeToMerge", () => ({ SwipeToMerge: "SwipeToMerge" }));
vi.mock("./item-actions-menu", () => ({ ItemActionsMenu: "ItemActionsMenu" }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn() } }));
vi.mock("./openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("./pageNavigation", () => ({ useOpenPage: () => vi.fn() }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));
const cloudState = vi.hoisted(() => ({ client: {} as unknown, org: null as null | { id: string }, status: "signed-out" }));
vi.mock("./cloud/CloudProvider", () => ({ useCloud: () => cloudState }));

import ReviewDetailScreen from "../app/review/[sessionId]";

const worker = (over: Partial<DashboardSession> = {}) => ({
	id: "worker-1", projectId: "repo", status: "idle", harness: "claude-code", displayName: "Fix the PR page", ...over,
}) as DashboardSession;

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.sessions = [];
	fixtures.getSession.mockReset();
	fixtures.getSessionPR.mockReset().mockResolvedValue([]);
	fixtures.loadPRDetails.mockReset().mockResolvedValue(undefined);
	fixtures.restore.mockReset();
	fixtures.reviewerSurface = undefined;
	fixtures.hostId = "dev";
	cloudState.client = {};
	cloudState.org = null;
	cloudState.status = "signed-out";
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
const pill = (label: string) => renderer!.root.findAll((node) => (node.type as unknown) === "ReviewActionButton" && node.props.accessibilityLabel === label);
// The row's own label goes on to name the worker; the button's stops at "Open worker".
const workerRow = () => renderer!.root.findAll((node) => (node.type as unknown) === "Pressable" && String(node.props.accessibilityLabel).startsWith("Open worker "));
const alerts = () => renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.accessibilityRole === "alert").map((node) => node.props.children);
const outlined = (node: { props: { tone?: string } }) => node.props.tone === "outlined";

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

const reviewerMenu = () => renderer!.root.find((node) => (node.type as unknown) === "ItemActionsMenu" && node.props.accessibilityLabel === "Reviewer session actions");

// Desktop's Reviewer section: one Review now button beside the agent, its session in a menu.
it("opens the reviewer from its row or its menu, beside the worker's own Open", async () => {
	fixtures.reviewerSurface = { mode: "chat", reviewId: "review-1", harness: "codex" };
	fixtures.sessions = [worker()];
	await render();
	expect(pill("Open worker")).toHaveLength(1);
	expect(reviewerMenu().props.actions.map((action: { id: string; label: string }) => [action.id, action.label])).toEqual([["open", "Open reviewer chat"]]);
	const row = renderer!.root.findAll((node) => (node.type as unknown) === "Pressable" && String(node.props.accessibilityLabel).startsWith("Open reviewer chat. "));
	expect(row).toHaveLength(1);
	expect(row[0]!.props.accessibilityLabel).toBe("Open reviewer chat. Not reviewed yet");
});

// Automatic review no longer locks the button, as on desktop.
it("keeps Review now available while automatic review is on", async () => {
	fixtures.sessions = [worker({ autoReviewEnabled: true })];
	await render();
	const reviewNow = renderer!.root.find((node) => (node.type as unknown) === "ReviewActionButton" && node.props.label === "Review now");
	expect(reviewNow.props.disabled).toBe(false);
});

// A PR whose threads were never read gets one details read. A quiet reload (the
// page polls every 2 s while a review runs or auto-review is on) hands the section
// a new PR object; that must not throw the read's answer away.
it("finishes the details read even when the page reloads while it is out", async () => {
	const pr = () => ({
		url: "https://github.com/acme/repo/pull/12", htmlUrl: "https://github.com/acme/repo/pull/12", number: 12, title: "Improve mobile review", state: "open", provider: "github",
		repo: "acme/repo", author: "ao", sourceBranch: "ao/fix", targetBranch: "main", additions: 1, deletions: 0, changedFiles: 1,
		ci: { state: "passing", failingChecks: [] }, review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
		mergeability: { state: "mergeable", reasons: [] },
	});
	fixtures.getSession.mockRejectedValue(new Error("not found"));
	fixtures.getSessionPR.mockImplementation(async () => [pr()]);
	let finish!: (failed?: boolean) => void;
	fixtures.loadPRDetails.mockImplementation(() => new Promise<void>((resolve, reject) => { finish = (failed) => failed ? reject(new Error("forge down")) : resolve(); }));
	const spinner = () => renderer!.root.findAll((node) => (node.type as unknown) === "Spinner" && node.props.accessibilityLabel === "Loading comments");
	const text = (value: string) => renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.children === value);
	await render();
	expect(fixtures.loadPRDetails).toHaveBeenCalledTimes(1);
	expect(spinner()).toHaveLength(1);

	// The reload lands while the read is out: a new PR object, the same PR.
	const scroll = renderer!.root.find((node) => (node.type as unknown) === "ScrollView");
	await act(async () => { await scroll.props.refreshControl.props.onRefresh(); });
	await act(async () => { finish(true); await Promise.resolve(); });

	expect(fixtures.loadPRDetails).toHaveBeenCalledTimes(1);
	expect(spinner()).toHaveLength(0);
	expect(text("Could not load this pull request's threads.")).toHaveLength(1);

	// Retry, reload mid-read again, and this time the read lands: the page reloads for the threads.
	const retry = renderer!.root.find((node) => (node.type as unknown) === "ReviewActionButton" && node.props.label === "Retry");
	await act(async () => { retry.props.onPress(); });
	expect(fixtures.loadPRDetails).toHaveBeenCalledTimes(2);
	await act(async () => { await scroll.props.refreshControl.props.onRefresh(); });
	const reads = fixtures.getSessionPR.mock.calls.length;
	await act(async () => { finish(); await Promise.resolve(); });
	await act(async () => { await Promise.resolve(); });
	expect(spinner()).toHaveLength(0);
	expect(text("No comments yet.")).toHaveLength(1);
	expect(fixtures.getSessionPR.mock.calls.length).toBeGreaterThan(reads);
});

describe("a Cloud worker's pull request", () => {
	const PR_URL = "https://github.com/acme/repo/pull/12";
	function signedIn(state: { reviewerSurface?: object; runs?: object[]; reviews?: object[] } = {}) {
		fixtures.hostId = "orchestrator-cloud";
		const client = {
			getSessionReviewState: vi.fn(async () => ({
				sessionId: "worker-1",
				reviews: [{ pullRequestUrl: PR_URL, pullRequestNumber: 12, title: "Improve mobile review", targetSha: "abcdef123456", status: "needs_review" }],
				runs: [],
				...state,
			})),
			listSessionPullRequests: vi.fn(async () => ({ sessionId: "worker-1", pullRequests: [{
				url: PR_URL, number: 12, title: "Improve mobile review", state: "open", provider: "github", repository: "acme/repo", author: "ao", sourceBranch: "ao/fix", targetBranch: "main", headSha: "abc123",
				additions: 3, deletions: 1, changedFiles: 2, ci: { state: "passing", failingChecks: [] },
				review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] },
				mergeability: { state: "mergeable", reasons: [], pullRequestUrl: PR_URL },
			}] })),
			getSession: vi.fn(async () => { throw new Error("not found"); }),
			triggerSessionReviews: vi.fn(async () => ({ sessionId: "worker-1", reviews: [], runs: [], created: false })),
			mergePullRequest: vi.fn(async () => ({ status: "merge_accepted" })),
		};
		cloudState.client = client;
		cloudState.org = { id: "org-1" };
		cloudState.status = "ready";
		return client;
	}
	const mergeBar = () => renderer!.root.findAll((node) => (node.type as unknown) === "SwipeToMerge");

	// The PR tab's card used to open this page on a Cloud worker and spin for good.
	it("loads its reviews and pull request from Orchestrator Cloud", async () => {
		const client = signedIn();
		await render();
		expect(client.getSessionReviewState).toHaveBeenCalledWith("org-1", "worker-1");
		expect(client.listSessionPullRequests).toHaveBeenCalledWith("org-1", "worker-1");
		expect(renderer!.root.findAll((node) => (node.type as unknown) === "Spinner")).toHaveLength(0);
		expect(mergeBar()).toHaveLength(1);
	});

	it("merges through the control plane, fenced to the head commit on screen", async () => {
		const client = signedIn();
		await render();
		await act(async () => { await mergeBar()[0].props.onMerge(); });
		expect(client.mergePullRequest).toHaveBeenCalledWith("org-1", "worker-1", 12, PR_URL, "abc123");
	});

	it("says when the head commit was already reviewed", async () => {
		const client = signedIn();
		await render();
		const start = renderer!.root.find((node) => (node.type as unknown) === "ReviewActionButton" && node.props.label === "Review now");
		await act(async () => { start.props.onPress(); });
		expect(client.triggerSessionReviews).toHaveBeenCalledWith("org-1", "worker-1");
		expect(renderer!.root.findAll((node) => (node.type as unknown) === "Text" && String(node.props.children).startsWith("This commit has already been reviewed"))).toHaveLength(1);
	});

	// Cloud reviewers run as conversations: no reviewer session to restore or stop.
	it("opens the reviewer's conversation and offers nothing a Cloud reviewer cannot do", async () => {
		signedIn({ reviewerSurface: { mode: "chat", reviewId: "run-1", harness: "codex" } });
		await render();
		expect(reviewerMenu().props.actions.map((action: { id: string }) => action.id)).toEqual(["open"]);
	});

	// As the desktop does: Cloud sends the stored run, and marks it sent.
	it("sends a finished review to the worker through the control plane's review route", async () => {
		const run = { id: "run-1", reviewId: "run-1", sessionId: "worker-1", batchId: "run-1", harness: "codex", triggerSource: "manual", pullRequestUrl: PR_URL, targetSha: "abcdef123456", status: "delivered", verdict: "changes_requested", body: "Fix the sum.", createdAt: "2026-11-08T21:00:00Z", autoInjectReview: false };
		const client = Object.assign(signedIn({ runs: [run], reviews: [{ pullRequestUrl: PR_URL, pullRequestNumber: 12, title: "Improve mobile review", targetSha: "abcdef123456", status: "changes_requested", latestRun: run }] } as object), {
			sendReviewToWorker: vi.fn(async () => ({ event: {} })),
			sendMessage: vi.fn(),
		});
		await render();
		// The Orchestrator's review is the Comments section's first row, open with Send to agent in reach.
		const sendButton = renderer!.root.find((node) => (node.type as unknown) === "ReviewActionButton" && node.props.label === "Send to agent");
		const send = { onPress: sendButton.props.onPress as () => void };
		await act(async () => { send!.onPress(); });
		expect(client.sendReviewToWorker).toHaveBeenCalledWith("org-1", "worker-1", "run-1");
		expect(client.sendMessage).not.toHaveBeenCalled();
	});

	it("asks to sign in rather than spinning when signed out", async () => {
		fixtures.hostId = "orchestrator-cloud";
		await render();
		expect(renderer!.root.findAll((node) => (node.type as unknown) === "EmptyState" && node.props.title === "Signed out of Orchestrator Cloud")).toHaveLength(1);
	});
});
