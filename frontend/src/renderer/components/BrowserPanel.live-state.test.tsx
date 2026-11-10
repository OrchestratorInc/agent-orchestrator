// The memoized BrowserPanelView driven by the real useBrowserView and
// useBrowserAnnotationQueue: every state change the main process pushes must
// still reach the panel, while its parent also renders for unrelated reasons.
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	resetClosedTabsForTest,
	resetConsumedPreviewTriggersForTest,
	resetNativeViewCacheForTest,
	useBrowserView,
	type BrowserNavState,
} from "../hooks/useBrowserView";
import type { BrowserTabsState } from "../../main/browser-view-host";
import type { BrowserAnnotationModeInput, BrowserAnnotationStatePayload } from "../../shared/browser-annotations";
import type { WorkspaceSession } from "../types/workspace";
import { BrowserPanelView, useBrowserAnnotationQueue } from "./BrowserPanel";
import { TooltipProvider } from "./ui/tooltip";

const postMock = vi.hoisted(() => vi.fn());

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock },
	apiErrorMessage: (_error: unknown, fallback = "Request failed") => fallback,
}));

vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ POST: postMock }) }));

vi.mock("../hooks/useBrowserRuntimeState", () => ({
	useBrowserRuntimeState: () => ({ connected: true, connecting: false, rejected: false }),
}));

function makeSession(id: string): WorkspaceSession {
	return {
		id,
		workspaceId: "ws-1",
		workspaceName: "my-app",
		title: id,
		provider: "claude-code",
		kind: "worker",
		branch: "feat/ns",
		status: "needs_input",
		updatedAt: "2026-06-15T00:00:00Z",
		prs: [],
	};
}

const sessions: Record<string, WorkspaceSession> = {
	"sess-1": makeSession("sess-1"),
	"sess-2": makeSession("sess-2"),
};

const hostBySession: Record<string, string> = { "sess-1": "alpha.test", "sess-2": "beta.test" };

function navState(viewId: string, host: string, patch: Partial<BrowserNavState> = {}): BrowserNavState {
	return {
		viewId,
		url: `http://${host}/`,
		title: host,
		canGoBack: false,
		canGoForward: false,
		isLoading: false,
		...patch,
	};
}

function Harness({
	sessionId,
	unrelated,
	onTogglePopOut,
}: {
	sessionId: string;
	unrelated: number;
	onTogglePopOut: (next: boolean) => void;
}) {
	const browserView = useBrowserView({ sessionId, active: true, poppedOut: false });
	const annotationQueue = useBrowserAnnotationQueue({ sessionId, navUrl: browserView.navState.url });
	return (
		<TooltipProvider>
			{/* Stands in for the SessionView state that changes without touching Browser. */}
			<span data-testid="unrelated">{unrelated}</span>
			<BrowserPanelView
				active
				annotationQueue={annotationQueue}
				browserView={browserView}
				onTogglePopOut={onTogglePopOut}
				poppedOut={false}
				session={sessions[sessionId]!}
			/>
		</TooltipProvider>
	);
}

describe("BrowserPanelView with the live browser hook", () => {
	const original = window.ao!;
	const navListeners = new Set<(state: BrowserNavState) => void>();
	const tabsListeners = new Set<(state: BrowserTabsState) => void>();
	const annotationListeners = new Set<(payload: BrowserAnnotationStatePayload) => void>();
	const setAnnotationModeMock = vi.fn<(input: BrowserAnnotationModeInput) => Promise<void>>(async () => undefined);

	beforeEach(() => {
		navListeners.clear();
		tabsListeners.clear();
		annotationListeners.clear();
		postMock.mockReset();
		setAnnotationModeMock.mockClear();
		window.ao = {
			...original,
			browser: {
				...original.browser,
				ensure: vi.fn(async (sessionId: string) => navState(`v:${sessionId}`, hostBySession[sessionId] ?? "other.test")),
				getTabs: vi.fn(async (viewId: string) => ({
					viewId,
					activeTabId: "t1",
					tabs: [{ id: "t1", url: "http://alpha.test/", title: "First", active: true }],
				})),
				setAnnotationMode: setAnnotationModeMock,
				onNavState: vi.fn((listener: (state: BrowserNavState) => void) => {
					navListeners.add(listener);
					return () => {
						navListeners.delete(listener);
					};
				}),
				onTabsState: vi.fn((listener: (state: BrowserTabsState) => void) => {
					tabsListeners.add(listener);
					return () => {
						tabsListeners.delete(listener);
					};
				}),
				onAnnotationState: vi.fn((listener: (payload: BrowserAnnotationStatePayload) => void) => {
					annotationListeners.add(listener);
					return () => {
						annotationListeners.delete(listener);
					};
				}),
			},
		};
	});

	afterEach(() => {
		window.ao = original;
		resetConsumedPreviewTriggersForTest();
		resetClosedTabsForTest();
		resetNativeViewCacheForTest();
	});

	const emitNav = (state: BrowserNavState) => act(() => navListeners.forEach((listener) => listener(state)));
	const emitTabs = (state: BrowserTabsState) => act(() => tabsListeners.forEach((listener) => listener(state)));
	const emitAnnotation = (payload: BrowserAnnotationStatePayload) =>
		act(() => annotationListeners.forEach((listener) => listener(payload)));
	const address = () => screen.getByRole("textbox", { name: "Browser URL" }) as HTMLInputElement;

	async function renderHarness(onTogglePopOut: (next: boolean) => void = () => undefined) {
		const view = render(<Harness onTogglePopOut={onTogglePopOut} sessionId="sess-1" unrelated={0} />);
		await waitFor(() => expect(address().value).toBe("alpha.test"));
		await waitFor(() => expect(screen.getByRole("tab", { name: "First" })).toBeInTheDocument());
		return view;
	}

	it("shows pushed navigation and loading state across unrelated parent renders", async () => {
		const { rerender } = await renderHarness();
		expect(screen.getByTestId("browser-panel")).toHaveAttribute("data-browser-native-page", "live");

		rerender(<Harness onTogglePopOut={() => undefined} sessionId="sess-1" unrelated={1} />);
		expect(screen.getByTestId("unrelated")).toHaveTextContent("1");
		expect(address().value).toBe("alpha.test");

		emitNav(navState("v:sess-1", "gamma.test", { isLoading: true, canGoBack: true }));
		expect(address().value).toBe("gamma.test");
		expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Back" })).toBeEnabled();

		rerender(<Harness onTogglePopOut={() => undefined} sessionId="sess-1" unrelated={2} />);
		emitNav(navState("v:sess-1", "gamma.test", { isLoading: false, canGoBack: true }));
		expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Stop" })).not.toBeInTheDocument();
	});

	it("shows pushed tab list, active tab, title and favicon changes", async () => {
		const { container, rerender } = await renderHarness();

		emitTabs({
			viewId: "v:sess-1",
			activeTabId: "t2",
			tabs: [
				{ id: "t1", url: "http://alpha.test/", title: "First", active: false },
				{ id: "t2", url: "http://docs.test/", title: "Docs", active: true },
			],
		});
		expect(screen.getByRole("tab", { name: "Docs" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByRole("tab", { name: "First" })).toHaveAttribute("aria-selected", "false");

		rerender(<Harness onTogglePopOut={() => undefined} sessionId="sess-1" unrelated={1} />);
		emitTabs({
			viewId: "v:sess-1",
			activeTabId: "t2",
			tabs: [{ id: "t2", url: "http://docs.test/", title: "Docs v2", active: true, favicon: "data:image/png;base64,AA==" }],
		});
		expect(screen.queryByRole("tab", { name: "First" })).not.toBeInTheDocument();
		expect(screen.getByRole("tab", { name: "Docs v2" })).toHaveAttribute("aria-selected", "true");
		expect(container.querySelector('[data-browser-tab-id="t2"] img')).toHaveAttribute("src", "data:image/png;base64,AA==");
	});

	it("follows a session switch and ignores the departed view's pushes", async () => {
		const { rerender } = await renderHarness();

		rerender(<Harness onTogglePopOut={() => undefined} sessionId="sess-2" unrelated={0} />);
		await waitFor(() => expect(address().value).toBe("beta.test"));

		emitNav(navState("v:sess-1", "stale.test"));
		expect(address().value).toBe("beta.test");

		rerender(<Harness onTogglePopOut={() => undefined} sessionId="sess-2" unrelated={1} />);
		emitNav(navState("v:sess-2", "delta.test"));
		expect(address().value).toBe("delta.test");
	});

	it("calls the latest onTogglePopOut", async () => {
		const first = vi.fn();
		const latest = vi.fn();
		const { rerender } = await renderHarness(first);

		rerender(<Harness onTogglePopOut={latest} sessionId="sess-1" unrelated={0} />);
		fireEvent.click(screen.getByRole("button", { name: "Maximize" }));
		expect(latest).toHaveBeenCalledWith(true);
		expect(first).not.toHaveBeenCalled();
	});

	it("shows annotation mode, queue status and pushed annotation counts", async () => {
		const { rerender } = await renderHarness();
		const annotate = screen.getByRole("button", { name: "Annotate page" });
		expect(annotate).toHaveAttribute("aria-pressed", "false");

		fireEvent.click(annotate);
		await waitFor(() => expect(setAnnotationModeMock).toHaveBeenCalledWith(expect.objectContaining({ viewId: "v:sess-1", enabled: true })));
		await waitFor(() => expect(screen.getByRole("button", { name: "Cancel annotation" })).toHaveAttribute("aria-pressed", "true"));

		rerender(<Harness onTogglePopOut={() => undefined} sessionId="sess-1" unrelated={1} />);
		emitAnnotation({ viewId: "v:sess-1", count: 2, screenshotCount: 0, hasDraft: false });
		expect(await screen.findByText("2 annotations")).toBeInTheDocument();
	});
});
