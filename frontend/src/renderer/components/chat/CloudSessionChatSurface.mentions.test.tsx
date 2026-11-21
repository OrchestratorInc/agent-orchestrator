import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { cloudChatEventsKey } from "../../lib/cloud-chat-history";
import type { CloudCpClientEvent, CloudCpNotification } from "../../lib/cloud-cp/types";
import { CloudCpError } from "../../lib/cloud-cp/errors";
import { clearMentionFocus, mentionOpenTarget, peekMentionFocus, requestMentionFocus } from "../../lib/cloud-mention-open";
import { recordCloudInboxSupport, resetCloudInboxSupport } from "../../lib/cloud-notifications";
import type { ConversationActivity, ConversationMessage } from "../../types/conversation";
import type { WorkspaceSession } from "../../types/workspace";
import { CloudSessionChatSurface, toSnapshot } from "./CloudSessionChatSurface";

const cloudMocks = vi.hoisted(() => ({
	listChatEvents: vi.fn(),
	sendSessionMessage: vi.fn(),
	steerTurn: vi.fn(),
	listChatModels: vi.fn(),
	listUserAgentModels: vi.fn(),
	listMentionCandidates: vi.fn(),
	chatProps: vi.fn(),
}));
const BASE = "https://cp.test";
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: cloudMocks, baseUrl: BASE, userId: "u-priya" }),
}));
vi.mock("../../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ features: [] }) }));
vi.mock("../../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-1", displayName: "Northwind", slug: "northwind", role: "member" } }) }));
vi.mock("./ChatWorkspace", () => ({
	ChatWorkspace: (props: unknown) => {
		cloudMocks.chatProps(props);
		return <div data-testid="cloud-chat" />;
	},
}));

const session = {
	id: "session-1",
	workspaceId: "project-1",
	workspaceName: "checkout-service",
	title: "Fix flaky payment retry test",
	provider: "codex",
	kind: "worker",
	mode: "chat",
	status: "working",
	runtimeConnected: true,
	updatedAt: "2026-11-13T10:00:00Z",
	prs: [],
	cloud: { orgId: "org-1" },
} satisfies WorkspaceSession;

const at = "2026-11-13T10:00:00Z";
const events: CloudCpClientEvent[] = [
	{ sessionId: session.id, sequence: 40, type: "chat.user_message", payload: { text: "The payment retry test fails about 1 run in 20.", turnId: "turn-1", authorUserId: "u-marco", authorName: "Marco Rossi" }, createdAt: at },
	{ sessionId: session.id, sequence: 41, type: "chat.assistant_delta", payload: { text: "The retry uses a real backoff.", turnId: "turn-1", itemId: "reply" }, createdAt: at },
	{
		sessionId: session.id, sequence: 42, type: "chat.user_message",
		payload: {
			text: "Can you check the retry backoff before we merge? @Priya Shah @Platform", turnId: "turn-2", authorUserId: "u-chen", authorName: "Chen Li",
			mentions: [{ kind: "person", id: "u-priya", name: "Priya Shah" }, { kind: "team", id: "t-platform", name: "Platform" }],
		},
		createdAt: at,
	},
];

const notification: CloudCpNotification = {
	id: "n-1",
	source: "cloud",
	orgId: "org-1",
	projectId: "project-1",
	sessionId: session.id,
	type: "mentioned",
	title: "Chen Li mentioned you",
	body: "Can you check the retry backoff before we merge? @Priya Shah @Platform",
	status: "unread",
	metadata: { authorUserId: "u-chen", authorName: "Chen Li", sequence: 42, turnId: "turn-2" },
	createdAt: at,
	updatedAt: at,
};

type ChatProps = {
	snapshot: { items: Array<ConversationMessage | ConversationActivity> };
	historyPending?: boolean;
	focusMessage?: { messageId: string; key: string };
	onFocusMessageDone?: (key: string) => void;
	composerMentions?: { onQuery: (query: string | undefined) => void; candidates?: unknown; workspaceName?: string; selfUserId?: string };
	onSend: (...args: unknown[]) => Promise<unknown>;
	onSteer?: (...args: unknown[]) => Promise<unknown>;
};
const firstProps = () => cloudMocks.chatProps.mock.calls[0]?.[0] as ChatProps;
const lastProps = () => cloudMocks.chatProps.mock.lastCall?.[0] as ChatProps;
const messageTexts = (props: ChatProps) => props.snapshot.items.flatMap((item) => item.kind === "message" ? [item.text] : []);

function renderSurface(queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
	const view = render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={session} /></QueryClientProvider>);
	return { queryClient, view };
}

beforeEach(() => {
	localStorage.clear();
	resetCloudInboxSupport();
	const pending = peekMentionFocus();
	if (pending) clearMentionFocus(pending.sessionId, pending.nonce);
	cloudMocks.chatProps.mockClear();
	cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
	cloudMocks.listUserAgentModels.mockReset().mockResolvedValue({ models: [] });
	cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events, hasMore: false, nextAfter: 42, hasOlder: false });
	cloudMocks.sendSessionMessage.mockReset().mockResolvedValue({ event: events[2] });
	cloudMocks.steerTurn.mockReset();
	cloudMocks.listMentionCandidates.mockReset().mockResolvedValue({ people: [], teams: [] });
});

describe("chips in the timeline", () => {
	it("keeps a message's mentions, with the names they had, for drawing chips", () => {
		const snapshot = toSnapshot(session, events, "u-priya");
		const mentioned = snapshot.items.find((item): item is ConversationMessage => item.kind === "message" && item.sequence === 42);
		expect(mentioned?.mentions).toEqual([
			{ kind: "person", id: "u-priya", name: "Priya Shah" },
			{ kind: "team", id: "t-platform", name: "Platform" },
		]);
		expect(snapshot.items.find((item) => item.kind === "message" && item.sequence === 40)).not.toHaveProperty("mentions");
	});

	it("keeps a steer's mentions", () => {
		const steered: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Go", turnId: "turn-1" }, createdAt: at },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: at },
			{ sessionId: session.id, sequence: 3, type: "chat.turn_steered", payload: { turnId: "turn-1", text: "@Platform check this", clientMessageId: "c-1", mentions: [{ kind: "team", id: "t-platform", name: "Platform" }] }, createdAt: at },
		];
		const steer = toSnapshot(session, steered).items.find((item): item is ConversationActivity => item.kind === "activity" && item.detail?.event === "steer");
		expect(steer?.detail?.mentions).toEqual([{ kind: "team", id: "t-platform", name: "Platform" }]);
	});
});

describe("opening a mention", () => {
	it("draws the session at the message from the cache its arrival filled, with no loading state", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(cloudChatEventsKey("org-1", session.id), events);
		const request = requestMentionFocus(mentionOpenTarget(notification)!);
		renderSurface(queryClient);
		// The very first frame already holds the message, and asks to land on it.
		const first = firstProps();
		expect(first.historyPending).toBe(false);
		expect(messageTexts(first)).toContain("Can you check the retry backoff before we merge? @Priya Shah @Platform");
		expect(first.focusMessage).toEqual({ messageId: "cloud-event-42", key: `${request.nonce}:loaded` });
		// Once the chat landed, the request is done.
		act(() => first.onFocusMessageDone?.(`${request.nonce}:loaded`));
		expect(peekMentionFocus()).toBeUndefined();
		await waitFor(() => expect(lastProps().focusMessage).toBeUndefined());
	});

	it("opens at once with nothing cached, showing the message from the notification's text until the rest loads", async () => {
		let answer!: (page: unknown) => void;
		cloudMocks.listChatEvents.mockReset().mockImplementationOnce(() => new Promise((resolve) => { answer = resolve; }))
			.mockResolvedValue({ events: [], hasMore: false, nextAfter: 42, hasOlder: false });
		const request = requestMentionFocus(mentionOpenTarget(notification)!);
		renderSurface();
		const first = firstProps();
		expect(first.historyPending).toBe(true);
		const fallback = first.snapshot.items.find((item): item is ConversationMessage => item.kind === "message");
		expect(fallback).toMatchObject({ id: "cloud-event-42", text: notification.body, authorName: "Chen Li" });
		expect(first.focusMessage).toEqual({ messageId: "cloud-event-42", key: `${request.nonce}:fallback` });
		// Landing on the stand-in keeps the request, to land again on the real row.
		act(() => first.onFocusMessageDone?.(`${request.nonce}:fallback`));
		expect(peekMentionFocus()?.nonce).toBe(request.nonce);
		await act(async () => answer({ events, hasMore: false, nextAfter: 42, hasOlder: false }));
		await waitFor(() => expect(lastProps().historyPending).toBe(false));
		expect(lastProps().focusMessage).toEqual({ messageId: "cloud-event-42", key: `${request.nonce}:loaded` });
		// The real conversation took the stand-in's place, its row included.
		expect(messageTexts(lastProps())).toHaveLength(3);
		expect(lastProps().snapshot.items.filter((item) => item.id === "cloud-event-42")).toHaveLength(1);
	});
});

describe("sending mentions", () => {
	it("sends a message's chips as typed tokens beside the text", async () => {
		renderSurface();
		await waitFor(() => expect(cloudMocks.chatProps).toHaveBeenCalled());
		await act(async () => {
			await lastProps().onSend("@Priya Shah can you look?", undefined, "client-1", undefined, undefined, undefined,
				[{ kind: "person", id: "u-priya", name: "Priya Shah" }, { kind: "team", id: "t-platform", name: "Platform" }]);
		});
		expect(cloudMocks.sendSessionMessage).toHaveBeenCalledWith("org-1", session.id, expect.objectContaining({
			text: "@Priya Shah can you look?",
			mentions: [{ kind: "person", userId: "u-priya" }, { kind: "team", teamId: "t-platform" }],
		}), { idempotencyKey: "client-1" });
	});

	it("sends no mentions field for a message without chips", async () => {
		renderSurface();
		await waitFor(() => expect(cloudMocks.chatProps).toHaveBeenCalled());
		await act(async () => { await lastProps().onSend("plain", undefined, "client-2"); });
		expect(cloudMocks.sendSessionMessage.mock.lastCall?.[2]).not.toHaveProperty("mentions");
	});

	it("says a refused mention in the reader's words and keeps the draft's validation code", async () => {
		cloudMocks.sendSessionMessage.mockRejectedValue(new CloudCpError("mentions: no access", {
			status: 422, code: "validation_error", details: { field: "mentions", reason: "no_access" },
		}));
		renderSurface();
		await waitFor(() => expect(cloudMocks.chatProps).toHaveBeenCalled());
		await expect(lastProps().onSend("@Cara hi", undefined, "client-3", undefined, undefined, undefined, [{ kind: "person", id: "u-cara", name: "Cara" }]))
			.rejects.toMatchObject({ message: "Someone you mentioned can't open this session. Nothing was sent.", code: "validation_error" });
	});
});

describe("the @ menu's source", () => {
	it("offers no people or teams on a control plane without mentions", async () => {
		recordCloudInboxSupport(BASE, "unsupported");
		renderSurface();
		await waitFor(() => expect(cloudMocks.chatProps).toHaveBeenCalled());
		expect(lastProps().composerMentions).toBeUndefined();
	});

	it("offers them, as the control plane lists them, on one that has mentions", async () => {
		recordCloudInboxSupport(BASE, "supported");
		const candidates = { people: [{ userId: "u-chen", name: "Chen Li", present: true, inSession: true }], teams: [] };
		cloudMocks.listMentionCandidates.mockResolvedValue(candidates);
		renderSurface();
		await waitFor(() => expect(lastProps().composerMentions).toBeDefined());
		expect(lastProps().composerMentions).toMatchObject({ workspaceName: "Northwind", selfUserId: "u-priya" });
		act(() => lastProps().composerMentions?.onQuery("ch"));
		await waitFor(() => expect(cloudMocks.listMentionCandidates).toHaveBeenCalledWith("org-1", session.id, { q: "ch", limit: 20 }, expect.anything()));
		await waitFor(() => expect(lastProps().composerMentions?.candidates).toEqual(candidates));
	});
});
