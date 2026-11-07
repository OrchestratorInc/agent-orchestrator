import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpNotification, CloudCpNotificationEvent, CloudCpNotificationListResponse } from "../lib/cloud-cp/types";
import { CloudCpError } from "../lib/cloud-cp/errors";
import { cloudInboxSupport, resetCloudInboxSupport, subscribeLiveCloudNotificationEvents } from "../lib/cloud-notifications";
import { isCleared, useCloudNotifications, useReadCloudSessionNotifications } from "./useCloudNotifications";

type StreamOptions = { onEvent: (event: CloudCpNotificationEvent) => void; onError?: (error: CloudCpError) => void; after?: number; signal?: AbortSignal };

const { cloudCpMock, subscribeNotificationsMock, subscribeMyNotificationsMock } = vi.hoisted(() => ({
	cloudCpMock: vi.fn(),
	subscribeNotificationsMock: vi.fn(async (_options?: { onEvent: (event: { sequence: number }) => void; after?: number }): Promise<void> => undefined),
	subscribeMyNotificationsMock: vi.fn(async (_options?: StreamOptions): Promise<void> => undefined),
}));

vi.mock("./useCloudCp", () => ({ useCloudCp: () => cloudCpMock() }));
vi.mock("./useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-1" } }) }));
vi.mock("./useWorkspaceQuery", () => ({ cloudSessionsQueryKey: ["cloud-sessions"] }));
vi.mock("./useOrchestratorChildren", () => ({ orchestratorChildrenQueryKey: ["orchestrator-children"] }));
vi.mock("../lib/cloud-cp/stream-bridge", () => ({
	subscribeNotificationEventsBridged: subscribeNotificationsMock,
	subscribeMyNotificationEventsBridged: subscribeMyNotificationsMock,
}));
vi.mock("../lib/cloud-notification-hints", () => ({ subscribeCloudNotificationHints: () => () => undefined }));

// A control plane from before team notifications has no /me inbox route.
const missingRoute = () => new CloudCpError("Cloud control plane request failed with status 404.", { status: 404 });

// Holds a stream open until the hook unmounts.
const holdOpen = async (options?: StreamOptions) => {
	await new Promise<void>((resolve) => options?.signal?.addEventListener("abort", () => resolve(), { once: true }));
};

beforeEach(() => {
	resetCloudInboxSupport();
	subscribeNotificationsMock.mockReset().mockResolvedValue(undefined);
	subscribeMyNotificationsMock.mockReset().mockImplementation(holdOpen);
});

afterEach(() => {
	vi.restoreAllMocks();
});

function wrapperFor(queryClient: QueryClient) {
	return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client: queryClient }, children);
}

const newQueryClient = () => new QueryClient({ defaultOptions: { queries: { retry: false } } });

const at = (value: string) => Date.parse(value);
const row = (overrides: Partial<{ id: string; createdAt: string; updatedAt: string; status: "unread" | "read" }> = {}) => ({
	id: "cntf_1",
	createdAt: "2026-07-21T10:00:00Z",
	updatedAt: "2026-07-21T10:00:00Z",
	status: "read" as const,
	...overrides,
});

describe("isCleared", () => {
	it("keeps rows visible when nothing was cleared", () => {
		expect(isCleared(row(), { before: 0, ids: {} })).toBe(false);
	});

	it("hides rows at or before the clear-all cutoff", () => {
		expect(isCleared(row(), { before: at("2026-07-21T10:00:00Z"), ids: {} })).toBe(true);
	});

	it("shows rows created after the clear-all cutoff", () => {
		expect(isCleared(row({ createdAt: "2026-07-21T11:00:00Z" }), { before: at("2026-07-21T10:00:00Z"), ids: {} })).toBe(false);
	});

	it("keeps a cleared row hidden while its mark-read is still in flight", () => {
		const clears = { before: 0, ids: { cntf_1: at("2026-07-21T10:00:00Z") } };
		expect(isCleared(row({ status: "unread" }), clears)).toBe(true);
	});

	it("keeps a cleared row hidden after mark-read bumps its updatedAt", () => {
		const clears = { before: 0, ids: { cntf_1: at("2026-07-21T10:00:00Z") } };
		expect(isCleared(row({ updatedAt: "2026-07-21T10:05:00Z" }), clears)).toBe(true);
	});

	it("shows a cleared row again when the control plane re-raises it", () => {
		const clears = { before: at("2026-07-21T10:00:00Z"), ids: { cntf_1: at("2026-07-21T10:00:00Z") } };
		expect(isCleared(row({ status: "unread", updatedAt: "2026-07-21T12:00:00Z" }), clears)).toBe(false);
	});

	it("keeps a revived row visible after it is read, even under the clear-all cutoff", () => {
		const clears = { before: at("2026-07-21T12:00:00Z"), ids: { cntf_1: null } };
		expect(isCleared(row({ updatedAt: "2026-07-21T12:30:00Z" }), clears)).toBe(false);
	});

	it("uses the per-row marker over the clear-all cutoff", () => {
		const clears = { before: at("2026-07-21T09:00:00Z"), ids: { cntf_1: at("2026-07-21T10:00:00Z") } };
		expect(isCleared(row({ createdAt: "2026-07-21T10:00:00Z" }), clears)).toBe(true);
	});
});

function cloudRow(overrides: Partial<CloudCpNotification> = {}): CloudCpNotification {
	return {
		id: "cntf_1",
		source: "cloud",
		orgId: "org-1",
		type: "ready_to_merge",
		title: "Pull request ready to merge",
		body: "acme/cloud#8 is ready to merge.",
		status: "read",
		createdAt: "2026-07-21T10:00:00Z",
		updatedAt: "2026-07-21T10:00:00Z",
		...overrides,
	};
}

function page(items: CloudCpNotification[], extra: Partial<CloudCpNotificationListResponse> = {}): CloudCpNotificationListResponse {
	return { items, unreadCount: items.filter((item) => item.status === "unread").length, page: { hasMore: false }, latestSequence: 0, ...extra };
}

describe("useCloudNotifications on a control plane from before team notifications", () => {
	let items: CloudCpNotification[];
	const listNotifications = vi.fn();
	const listMyNotifications = vi.fn();
	const markNotificationsRead = vi.fn();
	const changeNotificationState = vi.fn();

	function renderCloudNotifications(userId = "user-1", queryClient = newQueryClient()) {
		cloudCpMock.mockReturnValue({
			client: { listNotifications, listMyNotifications, markNotificationsRead, changeNotificationState },
			ready: true,
			baseUrl: "https://cloud.test",
			userId,
		});
		return renderHook(() => useCloudNotifications(), { wrapper: wrapperFor(queryClient) });
	}

	beforeEach(() => {
		window.localStorage.clear();
		items = [cloudRow()];
		listNotifications.mockReset().mockImplementation(async () => page(items));
		listMyNotifications.mockReset().mockRejectedValue(missingRoute());
		markNotificationsRead.mockReset().mockResolvedValue({ updated: 1 });
		changeNotificationState.mockReset();
	});

	it("falls back to the organization's list once the inbox route is missing", async () => {
		const { result } = renderCloudNotifications();
		await waitFor(() => expect(result.current.mode).toBe("legacy"));
		await waitFor(() => expect(result.current.all).toHaveLength(1));
		expect(listMyNotifications).toHaveBeenCalledTimes(1);
		expect(listNotifications).toHaveBeenCalledWith("org-1", { status: "all" });
		expect(cloudInboxSupport("https://cloud.test/")).toBe("unsupported");
	});

	it("falls back when the route exists without the store behind it (501)", async () => {
		listMyNotifications.mockRejectedValue(new CloudCpError("Team notifications aren't available on this server.", { status: 501, code: "not_available" }));
		const { result } = renderCloudNotifications();
		await waitFor(() => expect(result.current.mode).toBe("legacy"));
	});

	it("stays undecided on a failure that says nothing about the route", async () => {
		listMyNotifications.mockRejectedValue(new CloudCpError("Service unavailable.", { status: 503 }));
		const { result } = renderCloudNotifications();
		await waitFor(() => expect(listMyNotifications).toHaveBeenCalled());
		await act(async () => { await Promise.resolve(); });
		expect(result.current.mode).toBe("pending");
		expect(cloudInboxSupport("https://cloud.test")).toBe("unknown");
		expect(listNotifications).not.toHaveBeenCalled();
	});

	it("puts an unanswered question in Needs you and the rest in Updates", async () => {
		items = [
			cloudRow({ id: "q", type: "needs_input", status: "unread" }),
			cloudRow({ id: "answered", type: "needs_input", resolvedAt: "2026-07-21T10:05:00Z" }),
			cloudRow({ id: "pr", type: "pr_merged" }),
		];
		const { result } = renderCloudNotifications();
		await waitFor(() => expect(result.current.all).toHaveLength(3));
		expect(result.current.needsYou.map((item) => item.id)).toEqual(["q"]);
		expect(result.current.updates.map((item) => item.id)).toEqual(["answered", "pr"]);
		expect(result.current.needsYouCount).toBe(1);
		expect(result.current.unreadCount).toBe(1);
	});

	it("hides a read row without asking the control plane to mark it read again", async () => {
		const { result } = renderCloudNotifications();
		await waitFor(() => expect(result.current.all).toHaveLength(1));

		await act(() => result.current.clearOne(items[0]));

		expect(markNotificationsRead).not.toHaveBeenCalled();
		expect(changeNotificationState).not.toHaveBeenCalled();
		expect(result.current.all).toHaveLength(0);
	});

	it("treats a 404 for an already-read row as cleared", async () => {
		items = [cloudRow({ status: "unread" })];
		markNotificationsRead.mockRejectedValue(new CloudCpError("The notification was not found.", { status: 404 }));
		const { result } = renderCloudNotifications();
		await waitFor(() => expect(result.current.all).toHaveLength(1));

		await act(() => result.current.clearOne(items[0]));

		expect(markNotificationsRead).toHaveBeenCalledWith("org-1", ["cntf_1"]);
		expect(result.current.all).toHaveLength(0);
	});

	it("puts the row back when marking it read fails", async () => {
		items = [cloudRow({ status: "unread" })];
		markNotificationsRead.mockRejectedValue(new CloudCpError("Service unavailable.", { status: 503 }));
		const { result } = renderCloudNotifications();
		await waitFor(() => expect(result.current.all).toHaveLength(1));

		await act(() => expect(result.current.clearOne(items[0])).rejects.toThrow("Service unavailable."));

		expect(result.current.all).toHaveLength(1);
	});

	it("puts all rows back when marking all read fails", async () => {
		items = [cloudRow({ status: "unread" })];
		markNotificationsRead.mockRejectedValue(new CloudCpError("Service unavailable.", { status: 503 }));
		const { result } = renderCloudNotifications();
		await waitFor(() => expect(result.current.all).toHaveLength(1));

		await act(() => expect(result.current.clearAll()).rejects.toThrow("Service unavailable."));

		expect(markNotificationsRead).toHaveBeenCalledWith("org-1");
		expect(result.current.all).toHaveLength(1);
		expect(window.localStorage.getItem("ao.cloudNotifications.cleared:https://cloud.test:org-1:user-1")).toBeNull();
	});

	it("scopes cleared rows to the signed-in cloud user", async () => {
		const first = renderCloudNotifications("user-1");
		await waitFor(() => expect(first.result.current.all).toHaveLength(1));
		await act(() => first.result.current.clearAll());
		expect(first.result.current.all).toHaveLength(0);
		first.unmount();

		const second = renderCloudNotifications("user-2");
		await waitFor(() => expect(second.result.current.all).toHaveLength(1));
	});

	it("does not show another user's cached rows when accounts share an org", async () => {
		const firstRow = cloudRow({ id: "cntf_private_a", title: "Private for A" });
		const secondRow = cloudRow({ id: "cntf_private_b", title: "Private for B" });
		let finishSecondFetch: () => void = () => undefined;
		const secondFetchPending = new Promise<void>((resolve) => { finishSecondFetch = resolve; });
		listNotifications
			.mockResolvedValueOnce(page([firstRow]))
			.mockImplementationOnce(async () => {
				await secondFetchPending;
				return page([secondRow]);
			});
		const queryClient = newQueryClient();
		const first = renderCloudNotifications("user-1", queryClient);
		await waitFor(() => expect(first.result.current.all).toEqual([firstRow]));
		first.unmount();

		const second = renderCloudNotifications("user-2", queryClient);
		expect(second.result.current.all).toEqual([]);
		await act(async () => { finishSecondFetch(); });
		await waitFor(() => expect(second.result.current.all).toEqual([secondRow]));
	});

	it("keeps a re-raised row visible after it is read again", async () => {
		const { result } = renderCloudNotifications();
		await waitFor(() => expect(result.current.all).toHaveLength(1));
		await act(() => result.current.clearAll());
		expect(result.current.all).toHaveLength(0);

		// Re-raised in place: back to unread with a newer updatedAt.
		items = [cloudRow({ status: "unread", updatedAt: "2026-07-21T12:00:00Z" })];
		await act(() => result.current.refetch());
		await waitFor(() => expect(result.current.all).toHaveLength(1));

		// Opening the panel marks it read, which must not hide it again.
		items = [cloudRow({ status: "read", updatedAt: "2026-07-21T12:05:00Z" })];
		await act(() => result.current.refetch());
		await waitFor(() => expect(result.current.all[0]?.status).toBe("read"));
		expect(result.current.all).toHaveLength(1);
	});

	it("reconnects the organization stream after it closes and resumes after the last event", async () => {
		subscribeNotificationsMock.mockImplementation(async (options?: { onEvent: (event: { sequence: number }) => void }) => {
			if (subscribeNotificationsMock.mock.calls.length === 1) options?.onEvent({ sequence: 42 });
		});
		const hook = renderCloudNotificationsLive();
		await waitFor(() => expect(subscribeNotificationsMock).toHaveBeenCalledTimes(2), { timeout: 2500 });
		expect(subscribeNotificationsMock.mock.calls[1]?.[0]).toMatchObject({ after: 42 });
		expect(subscribeMyNotificationsMock).not.toHaveBeenCalled();
		hook.unmount();
	});

	function renderCloudNotificationsLive() {
		cloudCpMock.mockReturnValue({ client: { listNotifications, listMyNotifications }, ready: true, baseUrl: "https://cloud.test", userId: "user-1" });
		return renderHook(() => useCloudNotifications({ live: true }), { wrapper: wrapperFor(newQueryClient()) });
	}
});

describe("useCloudNotifications on a control plane with team notifications", () => {
	const listNotifications = vi.fn();
	const listMyNotifications = vi.fn();
	const changeNotificationState = vi.fn();
	const markNotificationsRead = vi.fn();
	let needsYou: CloudCpNotification[];
	let updates: CloudCpNotification[];
	let latestSequence: number;

	function render(live = false, queryClient = newQueryClient()) {
		cloudCpMock.mockReturnValue({
			client: { listNotifications, listMyNotifications, changeNotificationState, markNotificationsRead },
			ready: true,
			baseUrl: "https://cloud.test/",
			userId: "user-1",
		});
		return renderHook(() => useCloudNotifications({ live }), { wrapper: wrapperFor(queryClient) });
	}

	beforeEach(() => {
		window.localStorage.clear();
		latestSequence = 40;
		needsYou = [cloudRow({ id: "approve", type: "approval_needed", tier: 0, status: "unread", createdAt: "2026-07-21T11:00:00Z", sessionId: "s1" })];
		updates = [
			cloudRow({ id: "merged", type: "pr_merged", tier: 2, status: "unread", createdAt: "2026-07-21T12:00:00Z", sessionId: "s1" }),
			cloudRow({ id: "handled", type: "approval_needed", tier: 0, resolvedAt: "2026-07-21T09:30:00Z", resolution: "handled", resolvedBy: { userId: "u2", name: "Ben Ortiz" }, createdAt: "2026-07-21T09:00:00Z" }),
		];
		listMyNotifications.mockReset().mockImplementation(async (query: { view: string }) =>
			query.view === "needs_you" ? page(needsYou, { needsYouCount: needsYou.length + 2, unreadCount: 5, latestSequence }) : page(updates, { needsYouCount: needsYou.length + 2, unreadCount: 5, latestSequence }));
		listNotifications.mockReset();
		changeNotificationState.mockReset().mockResolvedValue({ updated: 1 });
		markNotificationsRead.mockReset();
	});

	it("reads both tabs across organizations and never the organization's list", async () => {
		const { result } = render();
		await waitFor(() => expect(result.current.mode).toBe("inbox"));
		await waitFor(() => expect(result.current.updates).toHaveLength(2));
		expect(listMyNotifications).toHaveBeenCalledWith({ view: "needs_you", state: "active" });
		expect(listMyNotifications).toHaveBeenCalledWith({ view: "updates", state: "active" });
		expect(listNotifications).not.toHaveBeenCalled();
		expect(result.current.needsYou.map((item) => item.id)).toEqual(["approve"]);
		expect(result.current.all.map((item) => item.id)).toEqual(["merged", "approve", "handled"]);
		// The badge is the server's count of blocking rows, not the loaded page.
		expect(result.current.needsYouCount).toBe(3);
		expect(result.current.unreadCount).toBe(5);
	});

	it("sends each triage action to the caller's state route", async () => {
		const { result } = render();
		await waitFor(() => expect(result.current.updates).toHaveLength(2));
		const until = new Date("2026-07-22T09:00:00.000Z");
		await act(() => result.current.markRead(["merged"]));
		await act(() => result.current.markUnread(["merged"]));
		await act(() => result.current.setDone(["approve"], true));
		await act(() => result.current.setDone(["approve"], false));
		await act(() => result.current.snooze(["merged"], until));
		expect(changeNotificationState.mock.calls.map(([change]) => change)).toEqual([
			{ ids: ["merged"], read: true },
			{ ids: ["merged"], read: false },
			{ ids: ["approve"], done: true },
			{ ids: ["approve"], done: false },
			{ ids: ["merged"], snoozedUntil: "2026-07-22T09:00:00.000Z" },
		]);
		expect(markNotificationsRead).not.toHaveBeenCalled();
	});

	it("marks every loaded unread row read and clears as done on the server, not on this device", async () => {
		const { result } = render();
		await waitFor(() => expect(result.current.updates).toHaveLength(2));
		await act(() => result.current.markAllRead());
		await act(() => result.current.clearOne(needsYou[0]));
		await waitFor(() => expect(result.current.all).toHaveLength(3));
		await act(() => result.current.clearAll());
		expect(changeNotificationState.mock.calls.map(([change]) => change)).toEqual([
			{ ids: ["merged", "approve"], read: true },
			// One blocking row is dismissed on its own...
			{ ids: ["approve"], done: true },
			// ...but Clear all leaves it: it clears when someone handles it.
			{ ids: ["merged", "handled"], done: true },
		]);
		expect(window.localStorage.length).toBe(0);
	});

	it("drops a done row from its tab at once and settles from the server", async () => {
		let release: () => void = () => undefined;
		changeNotificationState.mockImplementation(() => new Promise((resolve) => { release = () => resolve({ updated: 1 }); }));
		const { result } = render();
		await waitFor(() => expect(result.current.needsYou).toHaveLength(1));
		let pending: Promise<void> = Promise.resolve();
		act(() => { pending = result.current.setDone(["approve"], true); });
		await waitFor(() => expect(result.current.needsYou).toHaveLength(0));
		expect(result.current.needsYouCount).toBe(2);
		needsYou = [];
		await act(async () => { release(); await pending; });
		expect(result.current.needsYou).toHaveLength(0);
		expect(result.current.needsYouCount).toBe(2);
	});

	it("puts a row back when the server refuses the change", async () => {
		changeNotificationState.mockRejectedValue(new CloudCpError("Service unavailable.", { status: 503 }));
		const { result } = render();
		await waitFor(() => expect(result.current.needsYou).toHaveLength(1));
		await act(() => expect(result.current.setDone(["approve"], true)).rejects.toThrow("Service unavailable."));
		await waitFor(() => expect(result.current.needsYou).toHaveLength(1));
	});

	it("opens the /me stream after the newest sequence it read and publishes only what follows", async () => {
		const published: CloudCpNotificationEvent[] = [];
		const stop = subscribeLiveCloudNotificationEvents((event) => published.push(event));
		const event: CloudCpNotificationEvent = {
			sequence: 41, orgId: "org-2", recipientUserId: "user-1", kind: "notification_created",
			notification: cloudRow({ id: "new", tier: 0, desktop: true, status: "unread" }), createdAt: "2026-07-21T13:00:00Z",
		};
		subscribeMyNotificationsMock.mockImplementationOnce(async (options?: StreamOptions) => {
			options?.onEvent(event);
			// The server closes the stream; whatever happens next is read, not replayed.
			latestSequence = 57;
		});
		const hook = render(true);
		await waitFor(() => expect(subscribeMyNotificationsMock).toHaveBeenCalledTimes(1));
		expect(subscribeMyNotificationsMock.mock.calls[0]?.[0]).toMatchObject({ baseUrl: "https://cloud.test", after: 40 });
		expect(published).toEqual([event]);
		await waitFor(() => expect(subscribeMyNotificationsMock).toHaveBeenCalledTimes(2), { timeout: 2500 });
		expect(subscribeMyNotificationsMock.mock.calls[1]?.[0]).toMatchObject({ after: 57 });
		expect(published).toEqual([event]);
		expect(subscribeNotificationsMock).not.toHaveBeenCalled();
		stop();
		hook.unmount();
	});
});

describe("useReadCloudSessionNotifications", () => {
	const listMyNotifications = vi.fn();
	const changeNotificationState = vi.fn();
	let rows: CloudCpNotification[];

	function render(sessionId: string | undefined, enabled = true) {
		cloudCpMock.mockReturnValue({
			client: { listMyNotifications, changeNotificationState, listNotifications: vi.fn() },
			ready: true,
			baseUrl: "https://cloud.test",
			userId: "user-1",
		});
		return renderHook(({ id }: { id: string | undefined }) => {
			useReadCloudSessionNotifications(id, enabled);
			return useCloudNotifications();
		}, { wrapper: wrapperFor(newQueryClient()), initialProps: { id: sessionId } });
	}

	beforeEach(() => {
		vi.spyOn(document, "hasFocus").mockReturnValue(true);
		rows = [
			cloudRow({ id: "a", sessionId: "s1", tier: 0, status: "unread", eventId: "approval:1" }),
			cloudRow({ id: "b", sessionId: "s1", tier: 2, status: "read" }),
			cloudRow({ id: "c", sessionId: "s2", tier: 2, status: "unread" }),
		];
		listMyNotifications.mockReset().mockImplementation(async (query: { view: string }) =>
			page(rows.filter((item) => (query.view === "needs_you") === (item.tier === 0))));
		changeNotificationState.mockReset().mockImplementation(async (change: { ids: string[]; read?: boolean }) => {
			rows = rows.map((item) => change.ids.includes(item.id) && change.read !== undefined ? { ...item, status: change.read ? "read" : "unread" } : item);
			return { updated: change.ids.length };
		});
	});

	it("reads the open session's unread rows and nothing else", async () => {
		render("s1");
		await waitFor(() => expect(changeNotificationState).toHaveBeenCalledWith({ ids: ["a"], read: true }));
		expect(changeNotificationState).toHaveBeenCalledTimes(1);
	});

	it("waits until the window has focus", async () => {
		vi.mocked(document.hasFocus).mockReturnValue(false);
		const { result } = render("s1");
		await waitFor(() => expect(result.current.all).toHaveLength(3));
		expect(changeNotificationState).not.toHaveBeenCalled();
		vi.mocked(document.hasFocus).mockReturnValue(true);
		act(() => { window.dispatchEvent(new Event("focus")); });
		await waitFor(() => expect(changeNotificationState).toHaveBeenCalledWith({ ids: ["a"], read: true }));
	});

	it("keeps a row the person marked unread again while the session is open", async () => {
		const { result } = render("s1");
		await waitFor(() => expect(changeNotificationState).toHaveBeenCalledTimes(1));
		await act(() => result.current.markUnread(["a"]));
		await waitFor(() => expect(result.current.all.find((item) => item.id === "a")?.status).toBe("unread"));
		expect(changeNotificationState).toHaveBeenCalledTimes(2);
	});

	it("reads the rows of the next session opened", async () => {
		const hook = render("s1");
		await waitFor(() => expect(changeNotificationState).toHaveBeenCalledTimes(1));
		hook.rerender({ id: "s2" });
		await waitFor(() => expect(changeNotificationState).toHaveBeenLastCalledWith({ ids: ["c"], read: true }));
	});

	it("reads nothing on a control plane from before team notifications", async () => {
		listMyNotifications.mockRejectedValue(missingRoute());
		const { result } = render("s1");
		await waitFor(() => expect(result.current.mode).toBe("legacy"));
		expect(changeNotificationState).not.toHaveBeenCalled();
	});
});
