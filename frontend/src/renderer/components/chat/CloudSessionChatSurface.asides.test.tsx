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
import type { AsideMarkers } from "./AsideMarker";
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
	chatProps: vi.fn(),
	markers: undefined as AsideMarkers | undefined,
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
	const { AsideMarkersContext } = await import("./AsideMarker");
	return {
		ChatWorkspace: (props: { sidePane?: ReactNode }) => {
			cloudMocks.chatProps(props);
			cloudMocks.markers = useContext(AsideMarkersContext);
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

	it("gives each aside icon its unread dot from the session's asides", async () => {
		renderSurface();
		await waitFor(() => expect(cloudMocks.markers?.stateOf("a-1")).toBe("unread"));
		expect(cloudMocks.listSessionAsides).toHaveBeenCalledWith("org-1", session.id, expect.anything());
	});

	it("asks for no asides on a control plane that names none", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: events.slice(0, 2), hasMore: false, nextAfter: 41, hasOlder: false });
		renderSurface();
		await waitFor(() => expect(cloudMocks.chatProps).toHaveBeenCalled());
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(cloudMocks.markers).toBeUndefined();
		expect(cloudMocks.listSessionAsides).not.toHaveBeenCalled();
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
		expect(within(pane).getByText("Aside")).toBeInTheDocument();
		expect(within(pane).getByText("Fix flaky payment retry test")).toBeInTheDocument();
		await waitFor(() => expect(cloudMocks.markers?.stateOf("a-1")).toBe("open"));
		// Its reply box takes the caret.
		await waitFor(() => expect(document.activeElement).toBe(within(pane).getByRole("combobox", { name: "Reply in the aside" })));
	});

	it("opens and closes from the icon, and from the pane's close button", async () => {
		renderSurface();
		await waitFor(() => expect(cloudMocks.markers).toBeDefined());
		act(() => cloudMocks.markers!.toggle("a-1"));
		const pane = await screen.findByTestId("aside-pane");
		expect(peekOpenAside(session.id)?.asideId).toBe("a-1");
		act(() => cloudMocks.markers!.toggle("a-1"));
		expect(screen.queryByTestId("aside-pane")).toBeNull();
		act(() => cloudMocks.markers!.toggle("a-1"));
		fireEvent.click(within(await screen.findByTestId("aside-pane")).getByRole("button", { name: "Close aside" }));
		expect(screen.queryByTestId("aside-pane")).toBeNull();
		expect(pane).not.toBeInTheDocument();
	});

	it("shows the message, an N replies divider and the replies, and marks the aside read", async () => {
		openAside(session.id, "a-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(within(pane).getByText("1 reply")).toBeInTheDocument());
		const entries = pane.querySelectorAll("[data-aside-entry]");
		expect(entries[0]).toHaveTextContent("Chen Li");
		expect(entries[0]).toHaveTextContent("Can you check the retry backoff before we merge?");
		expect(entries[0]?.querySelectorAll("[data-mention-chip]")).toHaveLength(2);
		expect(entries[1]).toHaveTextContent("Marco Rossi");
		expect(entries[1]).toHaveTextContent("Looking now.");
		await waitFor(() => expect(cloudMocks.markSessionAsideRead).toHaveBeenCalledWith("org-1", session.id, "a-1"));
		await waitFor(() => expect(cloudMocks.markers?.stateOf("a-1")).toBe("open"));
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
		await waitFor(() => expect(within(pane).getByText("2 replies")).toBeInTheDocument());
		expect(within(pane).getByText("Platform owns that limit. I'd keep 1 s with jitter.")).toBeInTheDocument();
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
		expect(within(pane).getByText("1 reply")).toBeInTheDocument();
		expect(within(pane).getByText("Looking now. The 2 s backoff was set when the gateway rate limited us.")).toBeInTheDocument();
	});
});

describe("replying", () => {
	it("sends a reply with its mentions to the aside, and nothing to the agent", async () => {
		const user = userEvent.setup();
		openAside(session.id, "a-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		const box = within(pane).getByRole("combobox", { name: "Reply in the aside" });
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
		await waitFor(() => expect(within(pane).getByText("2 replies")).toBeInTheDocument());
	});

	it("lets a viewer reply, with no composer lease", async () => {
		const user = userEvent.setup();
		openAside(session.id, "a-1");
		renderSurface(undefined, { ...session, cloud: { orgId: "org-1", shared: true } });
		const pane = await screen.findByTestId("aside-pane");
		await typeInLexicalEditor(within(pane).getByRole("combobox", { name: "Reply in the aside" }), "Good enough for now.");
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
		await typeInLexicalEditor(within(pane).getByRole("combobox", { name: "Reply in the aside" }), "One more thing");
		await user.keyboard("{Enter}");
		expect(await within(pane).findByRole("alert")).toHaveTextContent("This session ended. Replies are closed.");
		expect(within(pane).getByRole("combobox", { name: "Reply in the aside" })).toHaveTextContent("One more thing");
	});

	it("takes no replies once the session ended", async () => {
		openAside(session.id, "a-1");
		renderSurface(undefined, { ...session, isTerminated: true });
		const pane = await screen.findByTestId("aside-pane");
		expect(within(pane).getByRole("button", { name: "Send reply" })).toBeDisabled();
		expect(within(pane).queryByRole("button", { name: "Mention someone" })).toBeNull();
	});
});

describe("muting", () => {
	it("mutes the aside from the pane's menu, and unmutes it", async () => {
		const user = userEvent.setup();
		openAside(session.id, "a-1");
		renderSurface();
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(within(pane).getByRole("button", { name: "Aside options" })).toBeInTheDocument());
		await user.click(within(pane).getByRole("button", { name: "Aside options" }));
		await user.click(await screen.findByRole("menuitem", { name: "Mute aside" }));
		await waitFor(() => expect(cloudMocks.muteSessionAside).toHaveBeenCalledWith("org-1", session.id, "a-1", true));
		await user.click(within(pane).getByRole("button", { name: "Aside options" }));
		await user.click(await screen.findByRole("menuitem", { name: "Unmute aside" }));
		await waitFor(() => expect(cloudMocks.muteSessionAside).toHaveBeenLastCalledWith("org-1", session.id, "a-1", false));
	});
});
