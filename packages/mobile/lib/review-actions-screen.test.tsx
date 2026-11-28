import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { RECONNECTING_ACTION_COPY } from "./connectionError";

const PR_URL = "https://github.com/acme/repo/pull/12";
const fixtures = vi.hoisted(() => ({
	// The machine's verified connection; false while the app re-races its endpoints (after an app switch).
	connected: true,
	backend: {} as Record<string, unknown>,
}));

vi.mock("react-native", () => ({ Alert: { alert: vi.fn() }, StyleSheet: { create: (styles: object) => styles }, View: "View" }));
vi.mock("expo-router", () => ({ useLocalSearchParams: () => ({ sessionId: "worker-1", prUrl: "https://github.com/acme/repo/pull/12", hostId: "dev" }) }));
vi.mock("./store", () => ({ HostScope: ({ children }: { children: unknown }) => children, useApp: () => ({ currentHostId: "dev" }) }));
vi.mock("./hostRoute", () => ({ hostRouteMatches: (route: string | undefined, current: string | undefined) => !route || route === current }));
vi.mock("./reviewBackend", () => ({ useReviewBackend: () => ({ backend: fixtures.connected ? fixtures.backend : null, signedOut: false }) }));
vi.mock("./review-actions-sheet", () => ({ ReviewActionsSheet: "ReviewActionsSheet" }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn() } }));
vi.mock("./ThemeProvider", () => ({ useThemedStyles: (make: (theme: object) => object) => make({}) }));
vi.mock("./ui", () => ({ SheetHeader: "SheetHeader", SHEET_SCROLL_CONTENT: {} }));
vi.mock("./RouteErrorBoundary", () => ({ SheetErrorBoundary: () => null }));
// A reviewer who left comments, to ask again: built from GitHub's reviews, which this test need not spell out.
vi.mock("./reviewView", async (importOriginal) => ({
	...await importOriginal<typeof import("./reviewView")>(),
	rerequestableReviewers: () => [{ reviewerId: "maya-chen", count: 3 }],
}));

import ReviewActionsRoute from "../app/sheets/review-actions";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.connected = true;
	fixtures.backend = {
		cloud: false,
		settings: vi.fn(async () => ({
			reviewers: [{ id: "codex", label: "Codex", selectable: true }],
			selectedReviewer: "",
			defaultReviewer: "codex",
			reviewerConfig: {},
			inheritedConfig: {},
			policies: { autoReviewEnabled: false, autoInjectReview: true, autoInjectCI: true },
		})),
		pullRequests: vi.fn(async () => [{ url: PR_URL, number: 12, title: "Improve mobile review" }]),
		reviews: vi.fn(async () => ({ reviews: [], runs: [] })),
		reviewerModels: vi.fn(async () => ({
			agentId: "codex", selectionMode: "catalog", allowCustom: false, source: "test", stale: false, fetchedAt: "",
			models: [{ id: "gpt-6.1", label: "GPT-6.1", isDefault: true, efforts: ["low", "high"], defaultEffort: "low" }],
		})),
		setPolicy: vi.fn(async () => {}),
		setReviewer: vi.fn(async () => {}),
		rerequest: vi.fn(async () => {}),
	};
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

const settle = () => act(async () => { for (let turn = 0; turn < 6; turn++) await Promise.resolve(); });
const sheet = () => renderer!.root.find((node) => (node.type as unknown) === "ReviewActionsSheet").props;
async function rerender() {
	await act(async () => { renderer!.update(<ReviewActionsRoute />); });
	await settle();
}

// Leaving the app drops the machine's connection until its endpoints answer
// again. The sheet is about the machine: what it read stays, and an action
// taken meanwhile says it is still reconnecting instead of doing nothing.
it("keeps what it read while the machine reconnects, and says so when asked to act", async () => {
	await act(async () => { renderer = create(<ReviewActionsRoute />); });
	await settle();
	expect(sheet().reviewer.model).toBeDefined();
	expect(sheet().reviewer.modelChoice).toBe(true);
	expect(sheet().externalReviewers.map((row: { id: string }) => row.id)).toEqual(["maya-chen"]);

	fixtures.connected = false;
	await rerender();
	expect(sheet().reviewer.model).toBeDefined();
	expect(sheet().reviewer.modelChoice).toBe(true);
	expect(sheet().externalReviewers.map((row: { id: string }) => row.id)).toEqual(["maya-chen"]);
	expect(sheet().alerts).toEqual([]);

	await act(async () => { sheet().onPolicyChange("autoReviewEnabled", true); });
	expect(fixtures.backend.setPolicy).not.toHaveBeenCalled();
	expect(sheet().policies[0].value).toBe(false);
	expect(sheet().alerts).toEqual([RECONNECTING_ACTION_COPY]);
	await act(async () => { sheet().onRerequest("maya-chen"); });
	expect(fixtures.backend.rerequest).not.toHaveBeenCalled();
	await act(async () => { sheet().reviewer.onSelectEffort("high"); });
	expect(fixtures.backend.setReviewer).not.toHaveBeenCalled();

	// Back: the note goes, and the sheet works again.
	fixtures.connected = true;
	await rerender();
	expect(sheet().alerts).toEqual([]);
	await act(async () => { sheet().onPolicyChange("autoReviewEnabled", true); });
	expect(fixtures.backend.setPolicy).toHaveBeenCalledWith("worker-1", "autoReviewEnabled", true);
});

// Opened while the machine does not answer: no "no reviewer agents" claim, and no endless spinners without a word.
it("says it is reconnecting, not that no reviewer exists, when opened before the machine answers", async () => {
	fixtures.connected = false;
	await act(async () => { renderer = create(<ReviewActionsRoute />); });
	await settle();
	expect(sheet().alerts).toEqual([RECONNECTING_ACTION_COPY]);
	expect(sheet().reviewer).toBeUndefined();
	expect(sheet().reviewersLoading).toBe(true);

	fixtures.connected = true;
	await rerender();
	expect(sheet().alerts).toEqual([]);
	expect(sheet().reviewersLoading).toBe(false);
	expect(sheet().reviewer).toBeDefined();
});
