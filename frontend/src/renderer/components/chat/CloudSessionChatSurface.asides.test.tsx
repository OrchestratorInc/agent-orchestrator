import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { cloudAsideKey, peekOpenAside, resetOpenAsides, openAside } from "../../lib/cloud-asides";
import { cloudChatEventsKey } from "../../lib/cloud-chat-history";
import type { CloudCpAsideThread, CloudCpClientEvent, CloudCpNotification } from "../../lib/cloud-cp/types";
import { clearMentionFocus, mentionOpenTarget, peekMentionFocus, requestMentionFocus } from "../../lib/cloud-mention-open";
import { resetCloudInboxSupport } from "../../lib/cloud-notifications";
import type { ConversationActivity, ConversationMessage } from "../../types/conversation";
import type { WorkspaceSession } from "../../types/workspace";
import { typeInLexicalEditor } from "../../test/lexical";
import { TooltipProvider } from "../ui/tooltip";
import type { CommentMarkers } from "./AsideMarker";
import { CloudSessionChatSurface, toSnapshot } from "./CloudSessionChatSurface";

const cloudMocks = vi.hoisted(() => ({
	listChatEvents: vi.fn(),
	sendSessionMessage: vi.fn(),
	steerTurn: vi.fn(),
	listChatModels: vi.fn(),
	listUserAgentModels: vi.fn(),
	listMentionCandidates: vi.fn(),
	listSessionAsides: vi.fn(),
	getSessionAside: vi.fn(),
	replyToSessionAside: vi.fn(),
	markSessionAsideRead: vi.fn(),
	muteSessionAside: vi.fn(),
	createSessionAside: vi.fn(),
	resolveSessionAside: vi.fn(),
	reopenSessionAside: vi.fn(),
	editSessionAsideReply: vi.fn(),
	deleteSessionAsideReply: vi.fn(),
	chatProps: vi.fn(),
	markers: undefined as CommentMarkers | undefined,
}));
const BASE = "https://cp.test";
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: cloudMocks, baseUrl: BASE, userId: "u-priya" }),
}));
vi.mock("../../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ features: [] }) }));
vi.mock("../../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-1", displayName: "Northwind", slug: "northwind", role: "member" } }) }));
// The chat itself is stood in for: it shows the aside pane it is handed, and
// reports the aside icons' state the surface gives the timeline.
vi.mock("./ChatWorkspace", async () => {
	const { useContext } = await import("react");
	const { CommentMarkersContext } = await import("./AsideMarker");
	return {
		ChatWorkspace: (props: { sidePane?: ReactNode }) => {
			cloudMocks.chatProps(props);
			cloudMocks.markers = useContext(CommentMarkersContext);
			return <div data-testid="cloud-chat">{props.sidePane}</div>;
		},
	};
});

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
const ROOT_TEXT = "Can you check the retry backoff before we merge? @Priya Shah @Platform";
const rootMentions = [{ kind: "person", id: "u-priya", name: "Priya Shah" }, { kind: "team", id: "t-platform", name: "Platform" }];
const tagged: CloudCpClientEvent = {
	sessionId: session.id, sequence: 42, type: "chat.user_message",
	payload: { text: ROOT_TEXT, turnId: "turn-2", authorUserId: "u-chen", authorName: "Chen Li", mentions: rootMentions, asideId: "a-1" },
	createdAt: at,
};
const replyEvent = (sequence: number, replyId: string, text: string, author = { id: "u-marco", name: "Marco Rossi" }): CloudCpClientEvent => ({
	sessionId: session.id, sequence, type: "chat.aside_reply", createdAt: at,
	payload: { asideId: "a-1", replyId, text, mentions: [], authorUserId: author.id, authorName: author.name, createdAt: at },
});
const events: CloudCpClientEvent[] = [
	{ sessionId: session.id, sequence: 40, type: "chat.user_message", payload: { text: "The payment retry test fails about 1 run in 20.", turnId: "turn-1", authorUserId: "u-marco", authorName: "Marco Rossi" }, createdAt: at },
	{ sessionId: session.id, sequence: 41, type: "chat.assistant_delta", payload: { text: "The retry uses a real backoff.", turnId: "turn-1", itemId: "reply" }, createdAt: at },
	tagged,
	replyEvent(43, "r-1", "Looking now. The 2 s backoff was set when the gateway rate limited us."),
];

const thread: CloudCpAsideThread = {
	aside: { asideId: "a-1", anchor: { sequence: 42, turnId: "turn-2" }, replyCount: 1, lastReplyAt: at, unread: true, muted: false },
	message: { sequence: 42, turnId: "turn-2", text: ROOT_TEXT, mentions: rootMentions as CloudCpAsideThread["message"]["mentions"], authorUserId: "u-chen", authorName: "Chen Li", createdAt: at },
	replies: [{ replyId: "r-1", asideId: "a-1", text: "Looking now. The 2 s backoff was set when the gateway rate limited us.", mentions: [], authorUserId: "u-marco", authorName: "Marco Rossi", createdAt: at }],
	hasMore: false,
};

type ChatProps = { snapshot: { items: Array<ConversationMessage | ConversationActivity> }; onSend: (...args: unknown[]) => Promise<unknown>; sidePane?: ReactNode };
const firstProps = () => cloudMocks.chatProps.mock.calls[0]?.[0] as ChatProps;
const lastProps = () => cloudMocks.chatProps.mock.lastCall?.[0] as ChatProps;

function renderSurface(queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } }), shown: WorkspaceSession = session) {
	const view = render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<CloudSessionChatSurface session={shown} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return { queryClient, view };
}

beforeEach(() => {
	localStorage.clear();
	resetCloudInboxSupport();
	resetOpenAsides();
	const pending = peekMentionFocus();
	if (pending) clearMentionFocus(pending.sessionId, pending.nonce);
	cloudMocks.markers = undefined;
	cloudMocks.chatProps.mockClear();
	cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
	cloudMocks.listUserAgentModels.mockReset().mockResolvedValue({ models: [] });
	cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events, hasMore: false, nextAfter: 43, hasOlder: false });
	cloudMocks.sendSessionMessage.mockReset().mockResolvedValue({ event: tagged });
	cloudMocks.steerTurn.mockReset();
	cloudMocks.listMentionCandidates.mockReset().mockResolvedValue({ people: [{ userId: "u-nair", name: "Priya Nair", present: false, inSession: false }], teams: [] });
	cloudMocks.listSessionAsides.mockReset().mockResolvedValue({ items: [thread.aside] });
	cloudMocks.getSessionAside.mockReset().mockResolvedValue(thread);
	cloudMocks.replyToSessionAside.mockReset().mockImplementation(async (_org: string, _session: string, asideId: string, body: { text: string; clientMessageId: string }) => ({
		reply: { replyId: `r-${body.clientMessageId}`, asideId, text: body.text, mentions: [], authorUserId: "u-priya", authorName: "Priya Shah", createdAt: at },
	}));
	cloudMocks.markSessionAsideRead.mockReset().mockResolvedValue({ ...thread.aside, unread: false });
	cloudMocks.muteSessionAside.mockReset().mockImplementation(async (_o: string, _s: string, _a: string, muted: boolean) => ({ ...thread.aside, muted }));
	cloudMocks.createSessionAside.mockReset();
	cloudMocks.resolveSessionAside.mockReset().mockImplementation(async () => ({ ...thread.aside, unread: false, resolvedAt: at, resolvedBy: { userId: "u-priya", name: "Priya Shah" } }));
	cloudMocks.reopenSessionAside.mockReset().mockImplementation(async () => ({ ...thread.aside, unread: false }));
	cloudMocks.editSessionAsideReply.mockReset();
	cloudMocks.deleteSessionAsideReply.mockReset().mockResolvedValue(undefined);
	vi.spyOn(document, "hasFocus").mockReturnValue(true);
});

describe("the conversation", () => {
	it("draws a tagged message with its aside, and keeps every reply out of the conversation", () => {
		const snapshot = toSnapshot(session, events, "u-priya");
		const message = snapshot.items.find((item): item is ConversationMessage => item.kind === "message" && item.sequence === 42);
		expect(message).toMatchObject({ asideId: "a-1", mentions: rootMentions });
		// The reply is between people: nothing of it is in the conversation the agent's turns make up.
		expect(JSON.stringify(snapshot.items)).not.toContain("Looking now");
		expect(snapshot.items.some((item) => item.sequence === 43)).toBe(false);
	});

	it("keeps a tagged steer's aside", () => {
		const steered: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Go", turnId: "turn-1" }, createdAt: at },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: at },
			{ sessionId: session.id, sequence: 3, type: "chat.turn_steered", payload: { turnId: "turn-1", text: "@Platform check this", clientMessageId: "c-1", mentions: [rootMentions[1]], asideId: "a-2" }, createdAt: at },
		];
		const steer = toSnapshot(session, steered).items.find((item): item is ConversationActivity => item.kind === "activity" && item.detail?.event === "steer");
		expect(steer?.detail?.asideId).toBe("a-2");
	});

	it("finds a steer's aside on the event its request returned, by clientMessageId", () => {
		const steered: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Go", turnId: "turn-1" }, createdAt: at },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: at },
			{ sessionId: session.id, sequence: 3, type: "chat.turn_steer_requested", payload: { turnId: "turn-1", text: "@Platform check this", clientMessageId: "c-7", mentions: [rootMentions[1]], asideId: "a-7" }, createdAt: at },
			{ sessionId: session.id, sequence: 4, type: "chat.turn_steered", payload: { turnId: "turn-1", text: "@Platform check this", clientMessageId: "c-7", mentions: [rootMentions[1]] }, createdAt: at },
		];
		const steer = toSnapshot(session, steered).items.find((item): item is ConversationActivity => item.kind === "activity" && item.detail?.event === "steer");
		expect(steer?.detail?.asideId).toBe("a-7");
	});

	it("gives a message's comment icon its unread state from the session's threads", async () => {
		renderSurface();
		await waitFor(() => expect(cloudMocks.markers?.forMessage(42, "a-1")?.state).toBe("unread"));
		expect(cloudMocks.listSessionAsides).toHaveBeenCalledWith("org-1", session.id, expect.anything());
		expect(cloudMocks.markers?.forMessage(40)).toBeUndefined();
	});

	it("draws no comments on a control plane without them", async () => {
		const { CloudCpError } = await import("../../lib/cloud-cp/errors");
		cloudMocks.listSessionAsides.mockRejectedValue(new CloudCpError("not found", { status: 404 }));
		renderSurface();
		await waitFor(() => expect(cloudMocks.listSessionAsides).toHaveBeenCalled());
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(cloudMocks.markers).toBeUndefined();
	});

	it("keeps comment events and resolves out of the conversation", () => {
		const withState: CloudCpClientEvent[] = [
			...events,
			{ sessionId: session.id, sequence: 44, type: "chat.aside_created", payload: { asideId: "c-1", anchor: { sequence: 41, kind: "agent_message" }, quote: "real backoff", origin: "comment", startedBy: { userId: "u-marco", name: "Marco Rossi" }, createdAt: at }, createdAt: at },
			{ sessionId: session.id, sequence: 45, type: "chat.aside_reply_edited", payload: { asideId: "c-1", replyId: "r-9", text: "edited secret words", mentions: [], editedAt: at }, createdAt: at },
			{ sessionId: session.id, sequence: 46, type: "chat.aside_resolved", payload: { asideId: "c-1", resolvedBy: { userId: "u-marco", name: "Marco Rossi" }, resolvedAt: at, sentToAgent: false }, createdAt: at },
		];
		const snapshot = toSnapshot(session, withState, "u-priya");
		expect(JSON.stringify(snapshot.items)).not.toContain("edited secret words");
		expect(snapshot.items.some((item) => (item.sequence ?? 0) >= 44)).toBe(false);
	});

	it("draws a message Also send to agent sent as its thread", () => {
		const sent: CloudCpClientEvent = {
			sessionId: session.id, sequence: 50, type: "chat.user_message", createdAt: at,
			payload: {
				text: "Comment thread on: real backoff\nMarco Rossi: Use jitter\nPriya Shah: Agreed", turnId: "turn-3", authorUserId: "u-priya", authorName: "Priya Shah",
				fromAside: { asideId: "c-1", quote: "real backoff", anchorKind: "agent_message", comments: [{ authorName: "Marco Rossi", text: "Use jitter" }, { authorName: "Priya Shah", text: "Agreed" }] },
			},
		};
		const message = toSnapshot(session, [...events, sent], "u-priya").items.find((item): item is ConversationMessage => item.kind === "message" && item.sequence === 50);
		expect(message?.fromAside).toEqual({ asideId: "c-1", quote: "real backoff", comments: [{ authorName: "Marco Rossi", text: "Use jitter" }, { authorName: "Priya Shah", text: "Agreed" }] });
	});
});

describe("opening an aside", () => {
	it("opens the sender's pane on the new aside when a tagged message is sent", async () => {
		renderSurface();
		await waitFor(() => expect(cloudMocks.chatProps).toHaveBeenCalled());
		expect(screen.queryByTestId("aside-pane")).toBeNull();
		await act(async () => {
			await lastProps().onSend(ROOT_TEXT, undefined, "client-1", undefined, undefined, undefined, [
				{ kind: "person", id: "u-priya", name: "Priya Shah" },
				{ kind: "team", id: "t-platform", name: "Platform" },
			]);
		});
		// The message went to the agent like any other.
		expect(cloudMocks.sendSessionMessage).toHaveBeenCalledWith("org-1", session.id, expect.objectContaining({ text: ROOT_TEXT }), { idempotencyKey: "client-1" });
		const pane = await screen.findByTestId("aside-pane");
		expect(within(pane).getByText("Comment")).toBeInTheDocument();
		expect(within(pane).getByText("Fix flaky payment retry test")).toBeInTheDocument();
		await waitFor(() => expect(cloudMocks.markers?.forMessage(42, "a-1")?.state).toBe("active"));
		// Its reply box takes the caret.
		await waitFor(() => expect(document.activeElement).toBe(within(pane).getByRole("combobox", { name: "Reply to the comment" })));
	});

	it("opens and closes from the comment icon, and from the pane's close button", async () => {
		renderSurface();
		await waitFor(() => expect(cloudMocks.markers?.forMessage(42, "a-1")).toBeDefined());
		const toggle = () => act(() => cloudMocks.markers!.openMessage(42, cloudMocks.markers!.forMessage(42, "a-1")!));
		toggle();
		const pane = await screen.findByTestId("aside-pane");
		expect(peekOpenAside(session.id)?.asideId).toBe("a-1");
		toggle();
		expect(screen.queryByTestId("aside-pane")).toBeNull();
		toggle();
		fireEvent.click(within(await screen.findByTestId("aside-pane")).getByRole("button", { name: "Close" }));
		expect(screen.queryByTestId("aside-pane")).toBeNull();
		expect(pane).not.toBeInTheDocument();
	});

	it("shows the tagged message and the comments, with no footer note, and marks the thread read", async () => {
		openAside(session.id, "a-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(pane.querySelectorAll("[data-aside-entry]")).toHaveLength(2));
		expect(pane.querySelector("[data-aside-divider]")).toBeNull();
		expect(pane).not.toHaveTextContent("The agent doesn't read");
		const entries = pane.querySelectorAll("[data-aside-entry]");
		expect(entries[0]).toHaveTextContent("Chen Li");
		expect(entries[0]).toHaveTextContent("Can you check the retry backoff before we merge?");
		expect(entries[0]?.querySelectorAll("[data-mention-chip]")).toHaveLength(2);
		expect(entries[1]).toHaveTextContent("Marco Rossi");
		expect(entries[1]).toHaveTextContent("Looking now.");
		await waitFor(() => expect(cloudMocks.markSessionAsideRead).toHaveBeenCalledWith("org-1", session.id, "a-1"));
		await waitFor(() => expect(cloudMocks.markers?.forMessage(42, "a-1")?.state).toBe("active"));
	});

	it("adds replies from the session's stream as they arrive, and reads them while watched", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		openAside(session.id, "a-1");
		renderSurface(queryClient);
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(cloudMocks.markSessionAsideRead).toHaveBeenCalledTimes(1));
		act(() => {
			queryClient.setQueryData<CloudCpClientEvent[]>(cloudChatEventsKey("org-1", session.id), (current) => [
				...(current ?? []),
				replyEvent(44, "r-2", "Platform owns that limit. I'd keep 1 s with jitter.", { id: "u-dana", name: "Dana Kim" }),
			]);
		});
		await waitFor(() => expect(within(pane).getByText("Platform owns that limit. I'd keep 1 s with jitter.")).toBeInTheDocument());
		await waitFor(() => expect(cloudMocks.markSessionAsideRead).toHaveBeenCalledTimes(2));
	});

	it("opens a notification onto its aside instantly, from what its arrival read into the cache", () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(cloudChatEventsKey("org-1", session.id), events);
		queryClient.setQueryData(cloudAsideKey("org-1", session.id, "a-1"), thread);
		const row: CloudCpNotification = {
			id: "n-5", source: "cloud", orgId: "org-1", projectId: "project-1", sessionId: session.id, type: "aside_reply",
			title: "Marco Rossi replied in an aside", body: "Looking now.", status: "unread",
			metadata: { asideId: "a-1", replyId: "r-1", authorName: "Marco Rossi", sequence: 42 }, createdAt: at, updatedAt: at,
		};
		const target = mentionOpenTarget(row)!;
		openAside(target.sessionId, target.asideId!);
		requestMentionFocus(target);
		renderSurface(queryClient);
		// The very first frame has the pane open with the message and its reply.
		expect(firstProps().sidePane).toBeTruthy();
		const pane = screen.getByTestId("aside-pane");
		expect(within(pane).getByText("Looking now. The 2 s backoff was set when the gateway rate limited us.")).toBeInTheDocument();
	});
});

describe("replying", () => {
	it("sends a reply with its mentions to the aside, and nothing to the agent", async () => {
		const user = userEvent.setup();
		openAside(session.id, "a-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		const box = within(pane).getByRole("combobox", { name: "Reply to the comment" });
		await typeInLexicalEditor(box, "@Pri");
		await user.click(await within(pane).findByRole("option", { name: /Priya Nair/ }));
		await typeInLexicalEditor(box, "can you review?");
		await user.keyboard("{Enter}");
		await waitFor(() => expect(cloudMocks.replyToSessionAside).toHaveBeenCalledTimes(1));
		expect(cloudMocks.replyToSessionAside).toHaveBeenCalledWith("org-1", session.id, "a-1", {
			text: "@Priya Nair can you review?",
			mentions: [{ kind: "person", userId: "u-nair" }],
			clientMessageId: expect.any(String),
		});
		expect(cloudMocks.listMentionCandidates).toHaveBeenCalledWith("org-1", session.id, expect.objectContaining({ q: "Pri" }), expect.anything());
		// The agent never reads an aside.
		expect(cloudMocks.sendSessionMessage).not.toHaveBeenCalled();
		expect(cloudMocks.steerTurn).not.toHaveBeenCalled();
		await waitFor(() => expect(pane.querySelectorAll("[data-aside-entry]")).toHaveLength(3));
	});

	it("lets a viewer reply, with no composer lease", async () => {
		const user = userEvent.setup();
		openAside(session.id, "a-1");
		renderSurface(undefined, { ...session, cloud: { orgId: "org-1", shared: true } });
		const pane = await screen.findByTestId("aside-pane");
		await typeInLexicalEditor(within(pane).getByRole("combobox", { name: "Reply to the comment" }), "Good enough for now.");
		await user.keyboard("{Enter}");
		await waitFor(() => expect(cloudMocks.replyToSessionAside).toHaveBeenCalledWith("org-1", session.id, "a-1", expect.objectContaining({ text: "Good enough for now." })));
		expect(cloudMocks.replyToSessionAside.mock.lastCall?.[3]).not.toHaveProperty("composerEpoch");
	});

	it("keeps the draft and says why when a reply is refused", async () => {
		const { CloudCpError } = await import("../../lib/cloud-cp/errors");
		cloudMocks.replyToSessionAside.mockRejectedValue(new CloudCpError("ended", { status: 409, code: "SESSION_TERMINATED" }));
		const user = userEvent.setup();
		openAside(session.id, "a-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		await typeInLexicalEditor(within(pane).getByRole("combobox", { name: "Reply to the comment" }), "One more thing");
		await user.keyboard("{Enter}");
		expect(await within(pane).findByRole("alert")).toHaveTextContent("This session ended. Comments are read-only.");
		expect(within(pane).getByRole("combobox", { name: "Reply to the comment" })).toHaveTextContent("One more thing");
	});

	it("takes no replies once the session ended", async () => {
		openAside(session.id, "a-1");
		renderSurface(undefined, { ...session, isTerminated: true });
		const pane = await screen.findByTestId("aside-pane");
		expect(within(pane).getByRole("button", { name: "Send comment" })).toBeDisabled();
		expect(within(pane).queryByRole("button", { name: "Resolve" })).toBeNull();
		expect(pane.querySelector("[data-comment-send-to-agent]")).toBeNull();
		expect(within(pane).queryByRole("button", { name: "Mention someone" })).toBeNull();
	});
});

describe("muting", () => {
	it("mutes the thread from the pane's menu, and unmutes it", async () => {
		const user = userEvent.setup();
		openAside(session.id, "a-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(within(pane).getByRole("button", { name: "Comment options" })).toBeInTheDocument());
		await user.click(within(pane).getByRole("button", { name: "Comment options" }));
		await user.click(await screen.findByRole("menuitem", { name: "Mute thread" }));
		await waitFor(() => expect(cloudMocks.muteSessionAside).toHaveBeenCalledWith("org-1", session.id, "a-1", true));
		await user.click(within(pane).getByRole("button", { name: "Comment options" }));
		await user.click(await screen.findByRole("menuitem", { name: "Unmute thread" }));
		await waitFor(() => expect(cloudMocks.muteSessionAside).toHaveBeenLastCalledWith("org-1", session.id, "a-1", false));
	});
});

// A comment thread (origin `comment`) on the agent's answer, quoting a passage of it.
const commentAside = {
	asideId: "c-1", anchor: { sequence: 41, kind: "agent_message" as const, turnId: "turn-1", itemId: "reply" }, replyCount: 1, lastReplyAt: at,
	unread: false, muted: false, origin: "comment" as const, quote: "real backoff", startedBy: { userId: "u-marco", name: "Marco Rossi" },
};
const commentThread: CloudCpAsideThread = {
	aside: commentAside,
	message: { kind: "agent_message", sequence: 41, turnId: "turn-1", text: "The retry uses a real backoff.", mentions: [], createdAt: at },
	replies: [{ replyId: "cr-1", asideId: "c-1", text: "Should this use jitter?", mentions: [], authorUserId: "u-marco", authorName: "Marco Rossi", createdAt: at }],
	hasMore: false,
};

function withCommentThread() {
	cloudMocks.listSessionAsides.mockResolvedValue({ items: [thread.aside, commentAside] });
	cloudMocks.getSessionAside.mockImplementation(async (_o: string, _s: string, asideId: string) => (asideId === "c-1" ? commentThread : thread));
	cloudMocks.markSessionAsideRead.mockImplementation(async (_o: string, _s: string, asideId: string) => ({ ...(asideId === "c-1" ? commentAside : thread.aside), unread: false }));
}

describe("comment threads", () => {
	it("marks the agent's answer with the thread's quote to highlight", async () => {
		withCommentThread();
		renderSurface();
		await waitFor(() => expect(cloudMocks.markers?.forMessage(41)).toBeDefined());
		expect(cloudMocks.markers!.forMessage(41)).toEqual({ state: "idle", asideIds: ["c-1"], quotes: [{ asideId: "c-1", quote: "real backoff", active: false }] });
		act(() => cloudMocks.markers!.openThread("c-1"));
		await waitFor(() => expect(cloudMocks.markers!.forMessage(41)?.quotes[0]?.active).toBe(true));
		expect(cloudMocks.markers!.forMessage(41)?.state).toBe("active");
	});

	it("draws nothing for a resolved thread", async () => {
		cloudMocks.listSessionAsides.mockResolvedValue({ items: [{ ...commentAside, resolvedAt: at }] });
		renderSurface();
		await waitFor(() => expect(cloudMocks.markers).toBeDefined());
		expect(cloudMocks.markers!.forMessage(41)).toBeUndefined();
	});

	it("shows the quote it is about and its comments, with Resolve", async () => {
		withCommentThread();
		openAside(session.id, "c-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		const quote = await waitFor(() => pane.querySelector("[data-comment-anchor-block]")!);
		expect(quote).toHaveTextContent("“real backoff”");
		expect(within(pane).getByRole("button", { name: "Jump to message" })).toBeInTheDocument();
		await waitFor(() => expect(within(pane).getByText("Should this use jitter?")).toBeInTheDocument());
		// A comment thread has no tagged message at its top.
		expect(pane.querySelectorAll("[data-aside-entry]")).toHaveLength(1);
		expect(within(pane).getByRole("button", { name: "Resolve" })).toBeInTheDocument();
	});

	it("resolves without telling the agent, and reopens", async () => {
		const user = userEvent.setup();
		withCommentThread();
		openAside(session.id, "c-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		await user.click(await within(pane).findByRole("button", { name: "Resolve" }));
		await waitFor(() => expect(cloudMocks.resolveSessionAside).toHaveBeenCalledWith("org-1", session.id, "c-1"));
		expect(await within(pane).findByText(/Resolved by Priya Shah/)).toBeInTheDocument();
		expect(cloudMocks.sendSessionMessage).not.toHaveBeenCalled();
		await user.click(within(pane).getByRole("button", { name: "Reopen" }));
		await waitFor(() => expect(cloudMocks.reopenSessionAside).toHaveBeenCalledWith("org-1", session.id, "c-1"));
	});

	it("sends a reply and the whole thread to the agent when the box is ticked", async () => {
		const user = userEvent.setup();
		withCommentThread();
		cloudMocks.replyToSessionAside.mockImplementation(async (_o: string, _s: string, asideId: string, body: { text: string; clientMessageId: string }) => ({
			reply: { replyId: "cr-2", asideId, text: body.text, mentions: [], authorUserId: "u-priya", authorName: "Priya Shah", createdAt: at },
			aside: { ...commentAside, resolvedAt: at, resolvedBy: { userId: "u-priya", name: "Priya Shah" }, sentToAgentAt: at, sentToAgentBy: { userId: "u-priya", name: "Priya Shah" } },
			sentMessage: { sequence: 60 },
		}));
		openAside(session.id, "c-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		const checkbox = await within(pane).findByRole("checkbox", { name: "Also send to agent" });
		expect(checkbox).not.toBeChecked();
		await user.click(checkbox);
		await typeInLexicalEditor(within(pane).getByRole("combobox", { name: "Reply to the comment" }), "Agreed, use jitter.");
		await user.keyboard("{Enter}");
		await waitFor(() => expect(cloudMocks.replyToSessionAside).toHaveBeenCalledWith("org-1", session.id, "c-1", {
			text: "Agreed, use jitter.", clientMessageId: expect.any(String), sendToAgent: true,
		}));
		// The server sent it: the client sends nothing to the agent itself.
		expect(cloudMocks.sendSessionMessage).not.toHaveBeenCalled();
		expect(await within(pane).findByText(/Resolved by Priya Shah · sent to the agent/)).toBeInTheDocument();
		expect(pane.querySelector("[data-comment-send-to-agent]")).toBeNull();
	});

	it("sends the thread as it is with the box ticked and nothing typed", async () => {
		const user = userEvent.setup();
		withCommentThread();
		cloudMocks.replyToSessionAside.mockResolvedValue({ aside: { ...commentAside, resolvedAt: at, sentToAgentAt: at } });
		openAside(session.id, "c-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		const send = within(pane).getByRole("button", { name: "Send comment" });
		expect(send).toBeDisabled();
		await user.click(await within(pane).findByRole("checkbox", { name: "Also send to agent" }));
		const sendAll = within(pane).getByRole("button", { name: "Send, resolve and send the thread to the agent" });
		expect(sendAll).toBeEnabled();
		await user.click(sendAll);
		await waitFor(() => expect(cloudMocks.replyToSessionAside).toHaveBeenCalledWith("org-1", session.id, "c-1", { text: "", clientMessageId: expect.any(String), sendToAgent: true }));
	});

	it("never offers Also send to agent to a viewer", async () => {
		withCommentThread();
		openAside(session.id, "c-1");
		renderSurface(undefined, { ...session, cloud: { orgId: "org-1", shared: true } });
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(within(pane).getByText("Should this use jitter?")).toBeInTheDocument());
		expect(within(pane).queryByRole("checkbox", { name: "Also send to agent" })).toBeNull();
		// Viewers still resolve, as in a shared document.
		expect(within(pane).getByRole("button", { name: "Resolve" })).toBeInTheDocument();
	});

	it("starts a new comment on a passage: nothing exists until it is sent, then the pane shows the thread", async () => {
		const user = userEvent.setup();
		cloudMocks.createSessionAside.mockResolvedValue({ aside: commentAside, reply: commentThread.replies[0] });
		renderSurface();
		await waitFor(() => expect(cloudMocks.markers).toBeDefined());
		act(() => cloudMocks.markers!.startComment({ anchor: { sequence: 41, kind: "agent_message" }, quote: "real backoff", excerpt: "The retry uses a real backoff." }));
		const pane = await screen.findByTestId("aside-pane");
		expect(within(pane).getByText("New comment")).toBeInTheDocument();
		expect(pane.querySelector("[data-comment-anchor-block]")).toHaveTextContent("“real backoff”");
		// The first comment has no Also send to agent: there is no thread to send yet.
		expect(within(pane).queryByRole("checkbox")).toBeNull();
		expect(cloudMocks.createSessionAside).not.toHaveBeenCalled();
		await waitFor(() => expect(document.activeElement).toBe(within(pane).getByRole("combobox", { name: "Write a comment" })));
		await typeInLexicalEditor(within(pane).getByRole("combobox", { name: "Write a comment" }), "Should this use jitter?");
		await user.keyboard("{Enter}");
		await waitFor(() => expect(cloudMocks.createSessionAside).toHaveBeenCalledWith("org-1", session.id, {
			anchor: { sequence: 41 }, quote: "real backoff", text: "Should this use jitter?", clientMessageId: expect.any(String),
		}));
		await waitFor(() => expect(peekOpenAside(session.id)?.asideId).toBe("c-1"));
		expect(cloudMocks.sendSessionMessage).not.toHaveBeenCalled();
	});

	it("starts a comment on a whole message, saying which", async () => {
		renderSurface();
		await waitFor(() => expect(cloudMocks.markers).toBeDefined());
		act(() => cloudMocks.markers!.startComment({ anchor: { sequence: 40, kind: "user_message" }, excerpt: "The payment retry test fails about 1 run in 20.", authorName: "Marco Rossi" }));
		const block = (await screen.findByTestId("aside-pane")).querySelector("[data-comment-anchor-block]")!;
		expect(block).toHaveTextContent("On Marco Rossi's message");
		expect(block).toHaveTextContent("The payment retry test fails about 1 run in 20.");
	});

	it("lists a message's threads when its icon has several", async () => {
		const second = { ...commentAside, asideId: "c-2", quote: "uses a", lastReplyAt: "2026-11-13T11:00:00Z" };
		cloudMocks.listSessionAsides.mockResolvedValue({ items: [commentAside, second] });
		renderSurface();
		await waitFor(() => expect(cloudMocks.markers?.forMessage(41)?.asideIds).toEqual(["c-2", "c-1"]));
		act(() => cloudMocks.markers!.openMessage(41, cloudMocks.markers!.forMessage(41)!));
		const pane = await screen.findByTestId("aside-pane");
		expect(pane.querySelectorAll("[data-comment-card]")).toHaveLength(2);
		fireEvent.click(pane.querySelector("[data-comment-card='c-1']")!);
		expect(peekOpenAside(session.id)?.asideId).toBe("c-1");
	});

	it("edits and deletes your own comment from its menu", async () => {
		const user = userEvent.setup();
		const own = { ...commentThread, replies: [{ ...commentThread.replies[0]!, authorUserId: "u-priya", authorName: "Priya Shah" }, { replyId: "cr-3", asideId: "c-1", text: "Second", mentions: [], authorUserId: "u-priya", authorName: "Priya Shah", createdAt: at }] };
		cloudMocks.listSessionAsides.mockResolvedValue({ items: [commentAside] });
		cloudMocks.getSessionAside.mockResolvedValue(own);
		cloudMocks.editSessionAsideReply.mockResolvedValue({ reply: { ...own.replies[1]!, text: "Second, edited", editedAt: at } });
		openAside(session.id, "c-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(within(pane).getByText("Second")).toBeInTheDocument());
		const row = pane.querySelector<HTMLElement>("[data-aside-entry='cr-3']")!;
		await user.click(within(row).getByRole("button", { name: "Comment actions" }));
		await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
		const box = within(row).getByRole("textbox", { name: "Edit" });
		await user.clear(box);
		await user.type(box, "Second, edited");
		await user.click(within(row).getByRole("button", { name: "Save" }));
		await waitFor(() => expect(cloudMocks.editSessionAsideReply).toHaveBeenCalledWith("org-1", session.id, "c-1", "cr-3", { text: "Second, edited" }));
		expect(await within(pane).findByText("Second, edited")).toBeInTheDocument();
		await user.click(within(pane.querySelector<HTMLElement>("[data-aside-entry='cr-3']")!).getByRole("button", { name: "Comment actions" }));
		await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
		await waitFor(() => expect(cloudMocks.deleteSessionAsideReply).toHaveBeenCalledWith("org-1", session.id, "c-1", "cr-3"));
		await waitFor(() => expect(pane.querySelector("[data-aside-entry='cr-3']")).toBeNull());
	});

	it("offers no edit or delete on someone else's comment", async () => {
		withCommentThread();
		openAside(session.id, "c-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(within(pane).getByText("Should this use jitter?")).toBeInTheDocument());
		expect(within(pane).queryByRole("button", { name: "Comment actions" })).toBeNull();
	});
});
