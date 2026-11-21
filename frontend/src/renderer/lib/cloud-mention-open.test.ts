import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { cloudChatEventsKey } from "./cloud-chat-history";
import type { CloudCpChatEventsResponse, CloudCpClientEvent, CloudCpNotification } from "./cloud-cp/types";
import {
	clearMentionFocus,
	cloudSessionQueryKey,
	mentionFallbackEvents,
	mentionOpenTarget,
	peekMentionFocus,
	prefetchMentionedSession,
	requestMentionFocus,
} from "./cloud-mention-open";

const row = (overrides: Partial<CloudCpNotification> = {}): CloudCpNotification => ({
	id: "n-1",
	source: "cloud",
	orgId: "org-1",
	projectId: "project-1",
	sessionId: "session-1",
	type: "mentioned",
	title: "Chen Li mentioned you",
	body: "Can you check the retry backoff before we merge? @Priya Shah",
	status: "unread",
	metadata: { authorUserId: "u-chen", authorName: "Chen Li", sequence: 300, turnId: "turn-9", teamId: "t-1" },
	createdAt: "2026-11-13T10:00:00Z",
	updatedAt: "2026-11-13T10:05:00Z",
	...overrides,
});

const event = (sequence: number): CloudCpClientEvent => ({
	sessionId: "session-1",
	sequence,
	type: "chat.user_message",
	payload: { text: `message ${sequence}` },
	createdAt: "2026-11-13T10:00:00Z",
});

describe("mentionOpenTarget", () => {
	it("opens a mention at its message, with what the row knows", () => {
		expect(mentionOpenTarget(row())).toEqual({
			notificationId: "n-1",
			kind: "mention",
			orgId: "org-1",
			projectId: "project-1",
			sessionId: "session-1",
			sequence: 300,
			turnId: "turn-9",
			authorUserId: "u-chen",
			authorName: "Chen Li",
			body: "Can you check the retry backoff before we merge? @Priya Shah",
			teamId: "t-1",
			createdAt: "2026-11-13T10:05:00Z",
		});
	});

	it("has no target for another kind or a row without a session", () => {
		expect(mentionOpenTarget(row({ type: "pr_merged" }))).toBeUndefined();
		expect(mentionOpenTarget(row({ sessionId: undefined }))).toBeUndefined();
	});
});

describe("mentionFallbackEvents", () => {
	it("is the message as the notification tells it, at the message's own sequence", () => {
		const target = mentionOpenTarget(row())!;
		expect(mentionFallbackEvents(target)).toEqual([{
			sessionId: "session-1",
			sequence: 300,
			type: "chat.user_message",
			payload: { text: target.body, authorUserId: "u-chen", authorName: "Chen Li" },
			createdAt: "2026-11-13T10:05:00Z",
		}]);
	});

	it("is nothing without a sequence or text", () => {
		expect(mentionFallbackEvents(mentionOpenTarget(row({ metadata: { authorName: "Chen Li" } })))).toBeUndefined();
		expect(mentionFallbackEvents(mentionOpenTarget(row({ body: " " })))).toBeUndefined();
		expect(mentionFallbackEvents(undefined)).toBeUndefined();
	});
});

describe("the open request", () => {
	it("keeps the newest request, and clears only the one asked about", () => {
		const first = requestMentionFocus(mentionOpenTarget(row())!);
		const second = requestMentionFocus(mentionOpenTarget(row())!);
		expect(second.nonce).toBeGreaterThan(first.nonce);
		clearMentionFocus("session-1", first.nonce);
		expect(peekMentionFocus()?.nonce).toBe(second.nonce);
		clearMentionFocus("session-1", second.nonce);
		expect(peekMentionFocus()).toBeUndefined();
	});
});

describe("prefetchMentionedSession", () => {
	const session = { id: "session-1", projectId: "project-1", title: "Fix flaky payment retry test" };

	it("reads the session's header and its conversation into the cache the session view reads", async () => {
		const queryClient = new QueryClient();
		const getSession = vi.fn(async () => ({ session }));
		const listChatEvents = vi.fn(async (): Promise<CloudCpChatEventsResponse> => ({
			events: [event(299), event(300)], hasMore: false, nextAfter: 300, hasOlder: false,
		}));
		await prefetchMentionedSession(queryClient, { getSession, listChatEvents } as never, "https://cp.example", mentionOpenTarget(row())!);
		expect(queryClient.getQueryData(cloudSessionQueryKey("https://cp.example", "org-1", "session-1"))).toEqual(session);
		expect(queryClient.getQueryData<CloudCpClientEvent[]>(cloudChatEventsKey("org-1", "session-1"))?.map((item) => item.sequence)).toEqual([299, 300]);
	});

	it("reads older pages until the message is in the conversation", async () => {
		const queryClient = new QueryClient();
		const listChatEvents = vi.fn(async (_org: string, _session: string, query?: { before?: number; after?: number }) => {
			if (query?.before === 9_007_199_254_740_991) return { events: [event(500), event(501)], hasMore: false, nextAfter: 501, hasOlder: true, nextBefore: 500 } as CloudCpChatEventsResponse;
			return { events: [event(300), event(301)], hasMore: false, nextAfter: 301, hasOlder: true, nextBefore: 300 } as CloudCpChatEventsResponse;
		});
		await prefetchMentionedSession(queryClient, { getSession: async () => ({ session }), listChatEvents } as never, "https://cp.example", mentionOpenTarget(row())!);
		const sequences = queryClient.getQueryData<CloudCpClientEvent[]>(cloudChatEventsKey("org-1", "session-1"))?.map((item) => item.sequence);
		expect(sequences).toContain(300);
		expect(sequences).toContain(501);
	});

	it("leaves a failed read for the chat to retry", async () => {
		const queryClient = new QueryClient();
		await expect(prefetchMentionedSession(queryClient, {
			getSession: async () => { throw new Error("offline"); },
			listChatEvents: async () => { throw new Error("offline"); },
		} as never, "https://cp.example", mentionOpenTarget(row())!)).resolves.toBeUndefined();
		expect(queryClient.getQueryData(cloudChatEventsKey("org-1", "session-1"))).toBeUndefined();
	});
});
