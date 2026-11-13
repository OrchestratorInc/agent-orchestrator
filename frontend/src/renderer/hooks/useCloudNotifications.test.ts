import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpNotification } from "../lib/cloud-cp/types";
import { CloudCpError } from "../lib/cloud-cp/errors";
import { activeCloudNotification, cloudInboxQueryKey, resetCloudInboxSupport, subscribeLiveCloudNotificationEvents } from "../lib/cloud-notifications";
import { isCleared, readCloudSessionMentions, useCloudNotifications } from "./useCloudNotifications";

const { cloudCpMock, subscribeNotificationsMock, subscribeMyNotificationsMock } = vi.hoisted(() => ({
	cloudCpMock: vi.fn(),
	subscribeNotificationsMock: vi.fn(async (_options?: { onEvent: (event: { sequence: number }) => void; after?: number }): Promise<void> => undefined),
	subscribeMyNotificationsMock: vi.fn(async (_options?: { onEvent: (event: unknown) => void; after?: number; signal?: AbortSignal }): Promise<void> => undefined),
}));

/** A control plane from before mentions: it has no /me inbox route. */
const noInbox = () => vi.fn().mockRejectedValue(new CloudCpError("Not found.", { status: 404 }));

vi.mock("./useCloudCp", () => ({ useCloudCp: () => cloudCpMock() }));
vi.mock("./useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-1" } }) }));
vi.mock("./useWorkspaceQuery", () => ({ cloudSessionsQueryKey: ["cloud-sessions"], sharedCloudWorkspacesQueryKey: ["cloud-shared-workspaces"] }));
vi.mock("./useOrchestratorChildren", () => ({ orchestratorChildrenQueryKey: ["orchestrator-children"] }));
vi.mock("../lib/cloud-cp/stream-bridge", () => ({ subscribeNotificationEventsBridged: subscribeNotificationsMock, subscribeMyNotificationEventsBridged: subscribeMyNotificationsMock }));
vi.mock("../lib/cloud-notification-hints", () => ({ subscribeCloudNotificationHints: () => () => undefined }));

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

describe("useCloudNotifications clearing", () => {
	let items: CloudCpNotification[];
	const listNotifications = vi.fn();
	const markNotificationsRead = vi.fn();

	function renderCloudNotifications(
		userId = "user-1",
		queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
	) {
		cloudCpMock.mockReturnValue({
			client: { listNotifications, markNotificationsRead, listMyNotifications: noInbox() },
			ready: true,
			baseUrl: "https://cloud.test",
			userId,
		});
		const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client: queryClient }, children);
		return renderHook(() => useCloudNotifications("all", { live: false }), { wrapper });
	}

	beforeEach(() => {
		window.localStorage.clear();
		resetCloudInboxSupport();
		items = [cloudRow()];
		listNotifications.mockReset().mockImplementation(async () => ({
			items,
			unreadCount: items.filter((item) => item.status === "unread").length,
			page: { hasMore: false },
			latestSequence: 0,
		}));
		markNotificationsRead.mockReset().mockResolvedValue({ updated: 1 });
	});

	it("hides a read row without asking the control plane to mark it read again", async () => {
		const { result } = renderCloudNotifications();
		await waitFor(() => expect(result.current.items).toHaveLength(1));

		await act(() => result.current.clearOne(items[0]));

		expect(markNotificationsRead).not.toHaveBeenCalled();
		expect(result.current.items).toHaveLength(0);
	});

	it("treats a 404 for an already-read row as cleared", async () => {
		items = [cloudRow({ status: "unread" })];
		markNotificationsRead.mockRejectedValue(new CloudCpError("The notification was not found.", { status: 404 }));
		const { result } = renderCloudNotifications();
		await waitFor(() => expect(result.current.items).toHaveLength(1));

		await act(() => result.current.clearOne(items[0]));

		expect(markNotificationsRead).toHaveBeenCalledWith("org-1", ["cntf_1"]);
		expect(result.current.items).toHaveLength(0);
	});

		it("puts the row back when marking it read fails", async () => {
			items = [cloudRow({ status: "unread" })];
			markNotificationsRead.mockRejectedValue(new CloudCpError("Service unavailable.", { status: 503 }));
			const { result } = renderCloudNotifications();
			await waitFor(() => expect(result.current.items).toHaveLength(1));

		await act(() => expect(result.current.clearOne(items[0])).rejects.toThrow("Service unavailable."));

			expect(result.current.items).toHaveLength(1);
		});

		it("puts all rows back when marking all read fails", async () => {
			items = [cloudRow({ status: "unread" })];
			markNotificationsRead.mockRejectedValue(new CloudCpError("Service unavailable.", { status: 503 }));
			const { result } = renderCloudNotifications();
			await waitFor(() => expect(result.current.items).toHaveLength(1));

			await act(() => expect(result.current.clearAll()).rejects.toThrow("Service unavailable."));

			expect(markNotificationsRead).toHaveBeenCalledWith("org-1");
			expect(result.current.items).toHaveLength(1);
			expect(window.localStorage.getItem("ao.cloudNotifications.cleared:https://cloud.test:org-1:user-1")).toBeNull();
		});

		it("scopes cleared rows to the signed-in cloud user", async () => {
			const first = renderCloudNotifications("user-1");
		await waitFor(() => expect(first.result.current.items).toHaveLength(1));
		await act(() => first.result.current.clearAll());
		expect(first.result.current.items).toHaveLength(0);
		first.unmount();

		const second = renderCloudNotifications("user-2");
			await waitFor(() => expect(second.result.current.items).toHaveLength(1));
		});

		it("does not show another user's cached rows when accounts share an org", async () => {
			const firstRow = cloudRow({ id: "cntf_private_a", title: "Private for A" });
			const secondRow = cloudRow({ id: "cntf_private_b", title: "Private for B" });
			let finishSecondFetch: () => void = () => undefined;
			const secondFetchPending = new Promise<void>((resolve) => { finishSecondFetch = resolve; });
			listNotifications
				.mockResolvedValueOnce({ items: [firstRow], unreadCount: 0, page: { hasMore: false }, latestSequence: 0 })
				.mockImplementationOnce(async () => {
					await secondFetchPending;
					return { items: [secondRow], unreadCount: 0, page: { hasMore: false }, latestSequence: 0 };
				});
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const first = renderCloudNotifications("user-1", queryClient);
			await waitFor(() => expect(first.result.current.items).toEqual([firstRow]));
			first.unmount();

			const second = renderCloudNotifications("user-2", queryClient);
			expect(second.result.current.items).toEqual([]);
			expect(second.result.current.data).toBeUndefined();
			await act(async () => { finishSecondFetch(); });
			await waitFor(() => expect(second.result.current.items).toEqual([secondRow]));
		});

	it("keeps a re-raised row visible after it is read again", async () => {
		const { result } = renderCloudNotifications();
		await waitFor(() => expect(result.current.items).toHaveLength(1));
		await act(() => result.current.clearAll());
		expect(result.current.items).toHaveLength(0);

		// Re-raised in place: back to unread with a newer updatedAt.
		items = [cloudRow({ status: "unread", updatedAt: "2026-07-21T12:00:00Z" })];
		await act(() => result.current.refetch());
		await waitFor(() => expect(result.current.items).toHaveLength(1));

		// Opening the panel marks it read, which must not hide it again.
		items = [cloudRow({ status: "read", updatedAt: "2026-07-21T12:05:00Z" })];
		await act(() => result.current.refetch());
		await waitFor(() => expect(result.current.items[0]?.status).toBe("read"));
		expect(result.current.items).toHaveLength(1);
	});
});

it("reconnects the notification stream after it closes and resumes after the last event", async () => {
	resetCloudInboxSupport();
	const listNotifications = vi.fn(async () => ({ items: [], unreadCount: 0, page: { hasMore: false }, latestSequence: 0 }));
	cloudCpMock.mockReturnValue({ client: { listNotifications, listMyNotifications: noInbox() }, ready: true, baseUrl: "https://cloud.test", userId: "user-1" });
	subscribeNotificationsMock.mockReset().mockImplementation(async (options?: { onEvent: (event: { sequence: number }) => void }) => {
		if (subscribeNotificationsMock.mock.calls.length === 1) options?.onEvent({ sequence: 42 });
	});
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client: queryClient }, children);
	const hook = renderHook(() => useCloudNotifications("all"), { wrapper });
	await waitFor(() => expect(subscribeNotificationsMock).toHaveBeenCalledTimes(2), { timeout: 2500 });
	expect(subscribeNotificationsMock.mock.calls[1]?.[0]).toMatchObject({ after: 42 });
	hook.unmount();
});

describe("the personal inbox (/me)", () => {
	const page = (rows: CloudCpNotification[], latestSequence = 7) => ({
		items: rows,
		unreadCount: rows.filter((row) => row.status === "unread").length,
		page: { hasMore: false },
		latestSequence,
	});
	const mention = (overrides: Partial<CloudCpNotification> = {}) => cloudRow({
		id: "cntf_mention",
		orgId: "org-guest",
		sessionId: "session-9",
		projectId: "project-9",
		type: "mentioned",
		title: "Chen Li mentioned you",
		body: "Can you check the retry backoff before we merge? @Priya Shah",
		status: "unread",
		metadata: { authorUserId: "u-chen", authorName: "Chen Li", sequence: 42, turnId: "turn-1" },
		...overrides,
	});

	function renderInbox(client: Record<string, unknown>, options: { live?: boolean } = {}) {
		cloudCpMock.mockReturnValue({ client, ready: true, baseUrl: "https://cloud.test", userId: "user-1" });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client: queryClient }, children);
		return { queryClient, ...renderHook(() => useCloudNotifications("all", { live: options.live ?? false }), { wrapper }) };
	}

	beforeEach(() => {
		window.localStorage.clear();
		resetCloudInboxSupport();
		subscribeMyNotificationsMock.mockReset().mockResolvedValue(undefined);
		subscribeNotificationsMock.mockClear();
	});

	it("lists the caller's notifications from every organization, a guest's mention included", async () => {
		const listMyNotifications = vi.fn(async () => page([mention()]));
		const listNotifications = vi.fn();
		const { result } = renderInbox({ listMyNotifications, listNotifications });
		await waitFor(() => expect(result.current.mode).toBe("inbox"));
		expect(result.current.items.map((row) => row.id)).toEqual(["cntf_mention"]);
		expect(result.current.data?.unreadCount).toBe(1);
		expect(listNotifications).not.toHaveBeenCalled();
	});

	it.each([404, 405, 501])("falls back to the organization's list when /me answers %i", async (status) => {
		const listMyNotifications = vi.fn().mockRejectedValue(new CloudCpError("No such route.", { status }));
		const listNotifications = vi.fn(async () => page([cloudRow()]));
		const { result } = renderInbox({ listMyNotifications, listNotifications });
		await waitFor(() => expect(result.current.mode).toBe("legacy"));
		await waitFor(() => expect(result.current.items.map((row) => row.id)).toEqual(["cntf_1"]));
		expect(listNotifications).toHaveBeenCalledWith("org-1", { status: "all" });
	});

	it("does not fall back for a 404 that names what was not found, or an outage", async () => {
		const listMyNotifications = vi.fn().mockRejectedValue(new CloudCpError("Gone.", { status: 404, code: "not_found" }));
		const listNotifications = vi.fn(async () => page([cloudRow()]));
		const { result } = renderInbox({ listMyNotifications, listNotifications });
		await waitFor(() => expect(listMyNotifications).toHaveBeenCalled());
		await act(async () => undefined);
		expect(result.current.mode).toBe("pending");
		expect(listNotifications).not.toHaveBeenCalled();
	});

	it("marks rows read by id, and all of them, through /me/notifications/state", async () => {
		const listMyNotifications = vi.fn(async () => page([mention()]));
		const changeMyNotificationState = vi.fn(async () => ({ updated: 1 }));
		const { result } = renderInbox({ listMyNotifications, changeMyNotificationState });
		await waitFor(() => expect(result.current.mode).toBe("inbox"));
		await act(() => result.current.markRead(["cntf_mention"]));
		expect(changeMyNotificationState).toHaveBeenCalledWith({ ids: ["cntf_mention"], read: true });
		await act(() => result.current.markAllRead());
		expect(changeMyNotificationState).toHaveBeenLastCalledWith({ all: true, read: true });
	});

	it("reads a session's mentions at once in the cache, then on the control plane", async () => {
		const listMyNotifications = vi.fn(async () => page([mention(), cloudRow({ id: "other", sessionId: "session-9", orgId: "org-guest", status: "unread" })]));
		let finish!: () => void;
		const changeMyNotificationState = vi.fn(() => new Promise<{ updated: number }>((resolve) => { finish = () => resolve({ updated: 1 }); }));
		const { result, queryClient } = renderInbox({ listMyNotifications, changeMyNotificationState });
		await waitFor(() => expect(result.current.mode).toBe("inbox"));
		const reading = readCloudSessionMentions(queryClient, { changeMyNotificationState }, "https://cloud.test", "user-1", "org-guest", "session-9");
		// Read before the control plane answers: no badge while the person is looking.
		const cached = queryClient.getQueryData<{ items: CloudCpNotification[]; unreadCount: number }>(cloudInboxQueryKey("https://cloud.test", "user-1"));
		expect(cached?.items.find((row) => row.id === "cntf_mention")?.status).toBe("read");
		// Only mentions: other kinds in the session keep their state.
		expect(cached?.items.find((row) => row.id === "other")?.status).toBe("unread");
		expect(cached?.unreadCount).toBe(1);
		expect(changeMyNotificationState).toHaveBeenCalledWith({ orgId: "org-guest", sessionId: "session-9", read: true });
		finish();
		await reading;
	});

	it("drops a mention or aside reply from the bell once it is resolved, at once from its event", async () => {
		const first = mention({ metadata: { authorName: "Chen Li", sequence: 42, asideId: "a-1" } });
		const reply = mention({ id: "cntf_reply", type: "aside_reply", title: "Dana Kim replied in an aside", metadata: { asideId: "a-1", replyId: "r-1", authorName: "Dana Kim", sequence: 42 } });
		const second = mention({ id: "cntf_mention_2", metadata: { authorName: "Marco Rossi", sequence: 57, asideId: "a-2" } });
		const rows = [first, second, reply, cloudRow({ id: "kept", resolvedAt: "2026-07-21T10:05:00Z" })];
		const listMyNotifications = vi.fn(async () => page(rows, 41));
		let deliver!: (event: unknown) => void;
		subscribeMyNotificationsMock.mockImplementation(async (options) => {
			deliver = (event) => options?.onEvent(event);
			await new Promise<void>((resolve) => options?.signal?.addEventListener("abort", () => resolve()));
		});
		const hook = renderInbox({ listMyNotifications }, { live: true });
		await waitFor(() => expect(hook.result.current.items.map((row) => row.id)).toEqual(["cntf_mention", "cntf_mention_2", "cntf_reply", "kept"]));
		await waitFor(() => expect(subscribeMyNotificationsMock).toHaveBeenCalled());
		// The person replied in aside a-1: that aside's mention and reply rows resolve; a-2's stays.
		const resolvedAt = "2026-07-21T10:06:00Z";
		// The list is not read again in time: the events alone take the rows out.
		listMyNotifications.mockImplementation(() => new Promise(() => undefined));
		act(() => {
			deliver({ sequence: 42, orgId: "org-guest", recipientUserId: "user-1", kind: "notification_resolved", notification: { ...first, resolvedAt }, createdAt: resolvedAt });
			deliver({ sequence: 43, orgId: "org-guest", recipientUserId: "user-1", kind: "notification_resolved", notification: { ...reply, resolvedAt }, createdAt: resolvedAt });
		});
		await waitFor(() => expect(hook.result.current.items.map((row) => row.id)).toEqual(["cntf_mention_2", "kept"]));
		// The badge loses the two unread rows that left.
		expect(hook.result.current.data?.unreadCount).toBe(1);
		hook.unmount();
	});

	it("keeps other kinds in the bell after they resolve", () => {
		expect(activeCloudNotification({ type: "needs_input", resolvedAt: "2026-07-21T10:06:00Z" })).toBe(true);
		expect(activeCloudNotification({ type: "mentioned", resolvedAt: "2026-07-21T10:06:00Z" })).toBe(false);
		expect(activeCloudNotification({ type: "aside_reply", resolvedAt: undefined })).toBe(true);
	});

	it("streams /me events after the inbox's sequence and publishes them for alerts", async () => {
		const listMyNotifications = vi.fn(async () => page([], 41));
		const event = { sequence: 42, orgId: "org-guest", recipientUserId: "user-1", kind: "notification_created", notification: mention(), createdAt: "2026-07-21T10:00:00Z" };
		subscribeMyNotificationsMock.mockImplementation(async (options) => {
			options?.onEvent(event);
			await new Promise<void>((resolve) => options?.signal?.addEventListener("abort", () => resolve()));
		});
		const heard = vi.fn();
		const stop = subscribeLiveCloudNotificationEvents(heard);
		const hook = renderInbox({ listMyNotifications }, { live: true });
		await waitFor(() => expect(heard).toHaveBeenCalledWith(event));
		expect(subscribeMyNotificationsMock.mock.calls[0]?.[0]).toMatchObject({ baseUrl: "https://cloud.test", after: 41 });
		expect(subscribeNotificationsMock).not.toHaveBeenCalled();
		stop();
		hook.unmount();
	});
});
