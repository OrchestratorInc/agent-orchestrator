import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	asideIdOf,
	asideReplyOf,
	closeAside,
	cloudAsideKey,
	loadAsideThread,
	mergeAsideReplies,
	openAside,
	peekOpenAside,
	resetOpenAsides,
	toggleAside,
} from "./cloud-asides";
import type { CloudCpAsideReply, CloudCpAsideThread, CloudCpClientEvent, CloudCpNotification, CloudCpNotificationEvent } from "./cloud-cp/types";
import { mentionFallbackEvents, mentionOpenTarget, prefetchMentionedSession } from "./cloud-mention-open";
import { asideReplyMetadata, cloudSessionWatched, shouldAlertCloudNotification } from "./cloud-notifications";

const at = "2026-11-13T10:00:00Z";
const reply = (replyId: string, createdAt: string, text = replyId): CloudCpAsideReply => ({
	replyId, asideId: "a-1", text, mentions: [], authorUserId: "u-marco", authorName: "Marco Rossi", createdAt,
});

const replyRow = (overrides: Partial<CloudCpNotification> = {}): CloudCpNotification => ({
	id: "n-2",
	source: "cloud",
	orgId: "org-1",
	projectId: "project-1",
	sessionId: "session-1",
	type: "aside_reply",
	title: "Priya Nair replied in an aside",
	body: "1 s with jitter looks right. Approved.",
	status: "unread",
	eventId: "e-2",
	metadata: { asideId: "a-1", replyId: "r-3", authorUserId: "u-nair", authorName: "Priya Nair", sequence: 42, turnId: "turn-2", sessionTitle: "Fix flaky payment retry test", projectName: "checkout-service" },
	createdAt: at,
	updatedAt: "2026-11-13T10:05:00Z",
	...overrides,
});
const created = (notification: CloudCpNotification): CloudCpNotificationEvent => ({
	sequence: 1, orgId: notification.orgId, recipientUserId: "u-chen", kind: "notification_created", notification, createdAt: notification.createdAt,
});

beforeEach(() => resetOpenAsides());

describe("an aside on the session's events", () => {
	it("is named by the message or steer that started it", () => {
		expect(asideIdOf({ type: "chat.user_message", payload: { text: "hi", asideId: "a-1" } })).toBe("a-1");
		expect(asideIdOf({ type: "chat.turn_steered", payload: { text: "hi", asideId: "a-2" } })).toBe("a-2");
		expect(asideIdOf({ type: "chat.turn_steer_requested", payload: { text: "hi", asideId: "a-3" } })).toBe("a-3");
		// A control plane without asides names none; other events never do.
		expect(asideIdOf({ type: "chat.user_message", payload: { text: "hi" } })).toBeUndefined();
		expect(asideIdOf({ type: "chat.assistant_delta", payload: { asideId: "a-1" } })).toBeUndefined();
	});

	it("reads a chat.aside_reply as its reply, and leaves anything malformed out", () => {
		const event: CloudCpClientEvent = {
			sessionId: "session-1", sequence: 50, type: "chat.aside_reply", createdAt: at,
			payload: { asideId: "a-1", replyId: "r-1", text: "@Priya Nair can you review?", mentions: [{ kind: "person", id: "u-nair", name: "Priya Nair" }], authorUserId: "u-chen", authorName: "Chen Li", createdAt: at },
		};
		expect(asideReplyOf(event)).toEqual({
			asideId: "a-1", replyId: "r-1", text: "@Priya Nair can you review?",
			mentions: [{ kind: "person", id: "u-nair", name: "Priya Nair" }],
			authorUserId: "u-chen", authorName: "Chen Li", createdAt: at,
		});
		expect(asideReplyOf({ ...event, payload: { text: "no ids" } })).toBeUndefined();
		expect(asideReplyOf({ ...event, type: "chat.user_message" })).toBeUndefined();
	});

	it("merges replies from every source once each, oldest first", () => {
		const merged = mergeAsideReplies(
			[reply("r-2", "2026-11-13T10:02:00Z"), reply("r-1", "2026-11-13T10:01:00Z")],
			[reply("r-2", "2026-11-13T10:02:00Z", "edited copy"), reply("r-3", "2026-11-13T10:03:00Z")],
		);
		expect(merged.map((item) => item.replyId)).toEqual(["r-1", "r-2", "r-3"]);
		expect(merged[1]?.text).toBe("edited copy");
	});

	it("reads every page of an aside's replies", async () => {
		const page = (replies: CloudCpAsideReply[], nextCursor?: string): CloudCpAsideThread => ({
			aside: { asideId: "a-1", anchor: { sequence: 42 }, replyCount: 3, unread: true, muted: false },
			message: { sequence: 42, text: "Can you check the retry backoff?", mentions: [], createdAt: at },
			replies, hasMore: Boolean(nextCursor), ...(nextCursor ? { nextCursor } : {}),
		});
		const getSessionAside = vi.fn()
			.mockResolvedValueOnce(page([reply("r-1", "2026-11-13T10:01:00Z"), reply("r-2", "2026-11-13T10:02:00Z")], "c-2"))
			.mockResolvedValueOnce(page([reply("r-3", "2026-11-13T10:03:00Z")]));
		const thread = await loadAsideThread({ getSessionAside }, "org-1", "session-1", "a-1");
		expect(thread.replies.map((item) => item.replyId)).toEqual(["r-1", "r-2", "r-3"]);
		expect(thread.hasMore).toBe(false);
		expect(getSessionAside).toHaveBeenLastCalledWith("org-1", "session-1", "a-1", { cursor: "c-2", limit: 200 }, { signal: undefined });
	});
});

describe("the open aside", () => {
	it("opens one aside per session, with the reply box focused, and the icon toggles it", () => {
		openAside("session-1", "a-1");
		expect(peekOpenAside("session-1")).toMatchObject({ asideId: "a-1", focusKey: expect.any(Number) });
		toggleAside("session-1", "a-2");
		expect(peekOpenAside("session-1")?.asideId).toBe("a-2");
		toggleAside("session-1", "a-2");
		expect(peekOpenAside("session-1")).toBeUndefined();
		openAside("session-1", "a-1", { focusReply: false });
		expect(peekOpenAside("session-1")).toEqual({ asideId: "a-1" });
		// Closing another aside leaves this one open.
		closeAside("session-1", "a-9");
		expect(peekOpenAside("session-1")?.asideId).toBe("a-1");
		closeAside("session-1");
		expect(peekOpenAside("session-1")).toBeUndefined();
	});
});

describe("aside reply notifications", () => {
	it("open on the aside, at the message that started it", () => {
		expect(mentionOpenTarget(replyRow())).toMatchObject({
			notificationId: "n-2", kind: "aside_reply", sessionId: "session-1", sequence: 42, asideId: "a-1", authorName: "Priya Nair",
		});
		expect(asideReplyMetadata(replyRow())).toMatchObject({ asideId: "a-1", replyId: "r-3", sessionTitle: "Fix flaky payment retry test" });
	});

	it("never draw the reply as the message while the conversation loads", () => {
		expect(mentionFallbackEvents(mentionOpenTarget(replyRow()))).toBeUndefined();
	});

	it("make a mention open on its aside too", () => {
		const mention = replyRow({ id: "n-1", type: "mentioned", title: "Chen Li mentioned you", body: "Can you check the retry backoff? @Priya Shah", metadata: { authorName: "Chen Li", sequence: 42, asideId: "a-1" } });
		const target = mentionOpenTarget(mention)!;
		expect(target).toMatchObject({ kind: "mention", asideId: "a-1" });
		expect(mentionFallbackEvents(target)?.[0]?.payload).toMatchObject({ text: mention.body, asideId: "a-1" });
	});

	it("read the aside's replies into the cache on arrival, so it opens with them", async () => {
		const queryClient = new QueryClient();
		const thread: CloudCpAsideThread = {
			aside: { asideId: "a-1", anchor: { sequence: 42 }, replyCount: 1, unread: true, muted: false },
			message: { sequence: 42, text: "Can you check the retry backoff?", mentions: [], createdAt: at },
			replies: [reply("r-3", at)], hasMore: false,
		};
		const getSessionAside = vi.fn(async () => thread);
		await prefetchMentionedSession(queryClient, {
			getSession: async () => ({ session: { id: "session-1" } }),
			listChatEvents: async () => ({ events: [], hasMore: false, nextAfter: 0, hasOlder: false }),
			getSessionAside,
		} as never, "https://cp.example", mentionOpenTarget(replyRow())!);
		expect(queryClient.getQueryData(cloudAsideKey("org-1", "session-1", "a-1"))).toEqual(thread);
	});

	it("alert by default, and not once New replies in asides I'm in is off", () => {
		expect(shouldAlertCloudNotification(created(replyRow()))).toBe(true);
		expect(shouldAlertCloudNotification(created(replyRow()), { mentions: true, teamMentions: true, asideReplies: false })).toBe(false);
		// A control plane from before the switch leaves it on.
		expect(shouldAlertCloudNotification(created(replyRow()), { mentions: false, teamMentions: false })).toBe(true);
		expect(shouldAlertCloudNotification(created(replyRow({ status: "read" })))).toBe(false);
	});
});

describe("the alert policy", () => {
	const focused = { visible: true, focused: true };
	it("stays quiet only for the session on screen in a focused window", () => {
		expect(cloudSessionWatched({ sessionId: "session-1" }, "session-1", focused)).toBe(true);
		// Focused on another session: it alerts.
		expect(cloudSessionWatched({ sessionId: "session-2" }, "session-1", focused)).toBe(false);
		// Away from AO, or the window hidden: it alerts.
		expect(cloudSessionWatched({ sessionId: "session-1" }, "session-1", { visible: true, focused: false })).toBe(false);
		expect(cloudSessionWatched({ sessionId: "session-1" }, "session-1", { visible: false, focused: true })).toBe(false);
		// A remote host's session of the same id is another session.
		expect(cloudSessionWatched({ sessionId: "session-1", hostId: "h-1" }, "session-1", focused)).toBe(false);
	});
});
