import { act, render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cloudInboxQueryKey, MENTIONED } from "../lib/cloud-notifications";
import type { CloudCpNotification, CloudCpNotificationListResponse } from "../lib/cloud-cp/types";
import { CloudSessionNotificationReader } from "./CloudSessionNotificationReader";

const BASE = "https://cp.test";
const mocks = vi.hoisted(() => ({ changeMyNotificationState: vi.fn() }));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: mocks, baseUrl: BASE, userId: "u-priya" }),
}));
vi.mock("../hooks/useWindowAttention", () => ({ useWindowAttention: () => true }));
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
