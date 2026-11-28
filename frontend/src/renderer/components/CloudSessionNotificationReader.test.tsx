import { act, render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cloudInboxQueryKey, MENTIONED } from "../lib/cloud-notifications";
import type { CloudCpNotification, CloudCpNotificationListResponse } from "../lib/cloud-cp/types";
import { CloudSessionNotificationReader } from "./CloudSessionNotificationReader";
import { MainViewVisibleContext } from "../lib/main-view-visibility";
import type { ReactElement } from "react";

const BASE = "https://cp.test";
const mocks = vi.hoisted(() => ({ changeMyNotificationState: vi.fn() }));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: mocks, baseUrl: BASE, userId: "u-priya" }),
}));
vi.mock("../hooks/useWindowAttention", () => ({ useWindowAttention: () => true }));
// Mentions and comment threads are offered unless a test says they are not.
const offered = vi.hoisted(() => ({ mentions: true }));
vi.mock("../hooks/useMentionsOffered", () => ({ useMentionsOffered: () => offered.mentions }));
vi.mock("../hooks/useCloudNotifications", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/useCloudNotifications")>()),
	useCloudInboxSupport: () => "supported",
}));

const at = "2026-11-21T10:00:00Z";
const mention = {
	id: "n-1", orgId: "org-1", sessionId: "session-1", type: MENTIONED, status: "unread",
	title: "Chen mentioned you", body: "", createdAt: at, updatedAt: at,
} as CloudCpNotification;
const page = (items: CloudCpNotification[]): CloudCpNotificationListResponse =>
	({ items, unreadCount: items.filter((row) => row.status === "unread").length, latestSequence: 1 }) as CloudCpNotificationListResponse;

let errors: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
	mocks.changeMyNotificationState.mockReset().mockResolvedValue(undefined);
	offered.mentions = true;
	errors = vi.spyOn(console, "error").mockImplementation(() => undefined);
});
afterEach(() => errors.mockRestore());

describe("CloudSessionNotificationReader", () => {
	it("reads the session's mentions from the inbox without taking over its query, so a live refresh still reads it", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const key = cloudInboxQueryKey(BASE, "u-priya");
		const readInbox = vi.fn().mockResolvedValueOnce(page([])).mockResolvedValue(page([mention]));
		// The shell's bell keeps the inbox live: the one reader of it that fetches.
		function Bell() {
			useQuery({ queryKey: key, queryFn: readInbox });
			return null;
		}
		render(
			<QueryClientProvider client={queryClient}>
				<Bell />
				<CloudSessionNotificationReader orgId="org-1" sessionId="session-1" />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(readInbox).toHaveBeenCalledTimes(1));
		// A mention arrives while the person watches the session: the bell reads
		// the inbox again, and the session reads the new mention at once.
		await act(() => queryClient.invalidateQueries({ queryKey: key }));
		// (Reading the mention refreshes the inbox once more, so at least twice.)
		expect(readInbox.mock.calls.length).toBeGreaterThanOrEqual(2);
		expect(queryClient.getQueryState(key)?.status).toBe("success");
		await waitFor(() => expect(mocks.changeMyNotificationState).toHaveBeenCalledWith({ orgId: "org-1", sessionId: "session-1", read: true }));
		expect(errors.mock.calls.flat().map(String).join(" ")).not.toMatch(/skipToken|Missing queryFn/);
	});
});

describe("CloudSessionNotificationReader behind the mentions feature", () => {
	it("reads nothing while the control plane does not offer mentions", async () => {
		offered.mentions = false;
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(cloudInboxQueryKey(BASE, "u-priya"), page([mention]));
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionNotificationReader orgId="org-1" sessionId="session-1" />
			</QueryClientProvider>,
		);
		await act(async () => undefined);
		expect(mocks.changeMyNotificationState).not.toHaveBeenCalled();
	});
});

describe("CloudSessionNotificationReader while the conversation is covered", () => {
	it.each([
		["a file or other tab is in front", (reader: ReactElement) => reader, false],
		["a popped-out browser covers the main view", (reader: ReactElement) => <MainViewVisibleContext.Provider value={false}>{reader}</MainViewVisibleContext.Provider>, true],
	] as const)("reads nothing while %s", async (_case, wrap, conversationShown) => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(cloudInboxQueryKey(BASE, "u-priya"), page([mention]));
		render(
			<QueryClientProvider client={queryClient}>
				{wrap(<CloudSessionNotificationReader conversationShown={conversationShown} orgId="org-1" sessionId="session-1" />)}
			</QueryClientProvider>,
		);
		await act(async () => undefined);
		expect(mocks.changeMyNotificationState).not.toHaveBeenCalled();
	});
});
