import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { closeThreadInView, openThreadInView, peekThreadInView } from "../lib/cloud-asides";
import type { CloudCpAsideThread, CloudCpCommentThread, CloudCpCommentThreadPage } from "../lib/cloud-cp/types";
import { TooltipProvider } from "./ui/tooltip";
import { ThreadsView } from "./ThreadsView";

const mocks = vi.hoisted(() => ({
	listMyCommentThreads: vi.fn(),
	countMyUnreadCommentThreads: vi.fn(),
	getSessionAside: vi.fn(),
	markSessionAsideRead: vi.fn(),
	markSessionAsideUnread: vi.fn(),
	muteSessionAside: vi.fn(),
	resolveSessionAside: vi.fn(),
	reopenSessionAside: vi.fn(),
	replyToSessionAside: vi.fn(),
	listMentionCandidates: vi.fn(),
	getSession: vi.fn(),
	openInSession: vi.fn(),
	navigate: vi.fn(),
}));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: mocks, baseUrl: "https://cp.test", userId: "u-priya" }),
}));
vi.mock("../hooks/useWindowAttention", () => ({ useWindowAttention: () => true }));
vi.mock("./CloudMentionRuntime", () => ({ useOpenThreadInSession: () => mocks.openInSession }));
// The control plane offers mentions and comment threads unless a test says it does not.
const offering = vi.hoisted(() => ({ features: ["mentions"] as string[], loaded: true }));
vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => offering }));
vi.mock("@tanstack/react-router", async (original) => ({ ...(await original<typeof import("@tanstack/react-router")>()), useNavigate: () => mocks.navigate }));

const at = "2026-11-24T10:00:00Z";

function row(asideId: string, overrides: Partial<CloudCpCommentThread> = {}): CloudCpCommentThread {
	return {
		orgId: "org-1",
		sessionId: `session-${asideId}`,
		projectId: "project-1",
		sessionTitle: `Session ${asideId}`,
		projectName: "finance-controller",
		sessionTerminated: false,
		canPrompt: true,
		aside: { asideId, anchor: { sequence: 41, kind: "agent_message" }, replyCount: 2, unread: false, muted: false, origin: "comment", quote: "real backoff" },
		lastComment: { authorName: "Mohak", preview: "Yes, 1 s with jitter.", at },
		participants: [{ userId: "u-mohak", name: "Mohak" }, { userId: "u-priya", name: "Priya Shah" }],
		participantCount: 2,
		commentCount: 2,
		unread: false,
		muted: false,
		lastActivityAt: at,
		...overrides,
	};
}

/** A thread's row, once the list shows it. */
const findRow = (asideId: string) => waitFor(() => {
	const node = document.querySelector<HTMLElement>(`[data-thread-row='${asideId}']`);
	if (!node) throw new Error(`no row ${asideId} yet`);
	return node;
});

const page = (items: CloudCpCommentThread[], nextCursor?: string): CloudCpCommentThreadPage => ({ items, hasMore: Boolean(nextCursor), ...(nextCursor ? { nextCursor } : {}) });

function thread(asideId: string): CloudCpAsideThread {
	return {
		aside: { asideId, anchor: { sequence: 41, kind: "agent_message" }, replyCount: 1, unread: true, muted: false, origin: "comment", quote: "real backoff" },
		message: { kind: "agent_message", sequence: 41, text: "The retry uses a real backoff.", mentions: [], createdAt: at },
		replies: [{ replyId: "r-1", asideId, text: "Should this use jitter?", mentions: [], authorUserId: "u-mohak", authorName: "Mohak", createdAt: at, sequence: 50 }],
		hasMore: false,
	};
}

function renderView() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<ThreadsView />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return queryClient;
}

beforeEach(() => {
	for (const mock of Object.values(mocks)) mock.mockReset();
	offering.features = ["mentions"];
	closeThreadInView();
	mocks.listMyCommentThreads.mockResolvedValue(page([]));
	mocks.countMyUnreadCommentThreads.mockResolvedValue({ count: 0 });
	mocks.getSessionAside.mockImplementation(async (_org: string, _session: string, asideId: string) => thread(asideId));
	mocks.markSessionAsideRead.mockImplementation(async (_org: string, _session: string, asideId: string) => ({ ...thread(asideId).aside, unread: false }));
	mocks.listMentionCandidates.mockResolvedValue({ people: [], teams: [] });
});

describe("the threads view behind the mentions feature", () => {
	it("reads no threads and goes home while the control plane does not offer mentions", async () => {
		offering.features = [];
		renderView();
		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith({ to: "/" }));
		expect(screen.queryByTestId("threads-view")).toBeNull();
		expect(mocks.listMyCommentThreads).not.toHaveBeenCalled();
		expect(mocks.countMyUnreadCommentThreads).not.toHaveBeenCalled();
	});

	it("shows the page while it does", async () => {
		renderView();
		expect(await screen.findByTestId("threads-view")).toBeInTheDocument();
		expect(mocks.navigate).not.toHaveBeenCalled();
	});
});

describe("the threads view's tabs and filter", () => {
	it("lists open threads by default, then resolved ones, and Unread only asks for unread", async () => {
		renderView();
		await waitFor(() => expect(mocks.listMyCommentThreads).toHaveBeenCalledWith({ status: "open", limit: 50 }, expect.anything()));
		const tabs = screen.getByRole("tablist", { name: "Thread status" });
		expect(within(tabs).getByRole("tab", { name: "Open" })).toHaveAttribute("aria-selected", "true");
		fireEvent.click(within(tabs).getByRole("tab", { name: "Resolved" }));
		await waitFor(() => expect(mocks.listMyCommentThreads).toHaveBeenCalledWith({ status: "resolved", limit: 50 }, expect.anything()));
		expect(within(tabs).getByRole("tab", { name: "Resolved" })).toHaveAttribute("aria-selected", "true");
		// A switch with a visible label that names it.
		const unreadOnly = screen.getByRole("switch", { name: "Unread only" });
		expect(unreadOnly).toHaveAttribute("aria-checked", "false");
		expect(unreadOnly).toHaveAttribute("data-threads-unread-only");
		// Clicking the label turns it on too.
		fireEvent.click(screen.getByText("Unread only"));
		expect(unreadOnly).toHaveAttribute("aria-checked", "true");
		await waitFor(() => expect(mocks.listMyCommentThreads).toHaveBeenCalledWith({ status: "resolved", unread: true, limit: 50 }, expect.anything()));
	});

	it("says what an empty list means on each tab", async () => {
		renderView();
		expect(await screen.findByText("No open threads. Comment on any message to start one.")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("switch", { name: "Unread only" }));
		expect(await screen.findByText("You're all caught up.")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("switch", { name: "Unread only" }));
		fireEvent.click(screen.getByRole("tab", { name: "Resolved" }));
		expect(await screen.findByText("No resolved threads yet.")).toBeInTheDocument();
	});

	it("offers a retry when the list can't be read", async () => {
		// It asks twice (one retry) before saying so.
		mocks.listMyCommentThreads.mockRejectedValueOnce(new Error("offline")).mockRejectedValueOnce(new Error("offline")).mockResolvedValue(page([row("c-1")]));
		renderView();
		expect(await screen.findByRole("alert", undefined, { timeout: 4000 })).toHaveTextContent("Could not load threads.");
		fireEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(await screen.findByText("Session c-1")).toBeInTheDocument();
	});
});

describe("the Unread only switch", () => {
	it("says how many threads are unread, as the sidebar badge does", async () => {
		mocks.countMyUnreadCommentThreads.mockResolvedValue({ count: 2 });
		renderView();
		await waitFor(() => expect(mocks.countMyUnreadCommentThreads).toHaveBeenCalled());
		await waitFor(() => expect(document.querySelector("[data-threads-unread-count]")).not.toBeNull());
		expect(screen.getByRole("switch", { name: "Unread only · 2" })).toBeInTheDocument();
		expect(document.querySelector("[data-threads-unread-count]")).toHaveTextContent(/^2$/);
	});

	it("says no count when nothing is unread", async () => {
		renderView();
		await waitFor(() => expect(mocks.countMyUnreadCommentThreads).toHaveBeenCalled());
		expect(screen.getByRole("switch", { name: "Unread only" })).toBeInTheDocument();
		expect(document.querySelector("[data-threads-unread-count]")).toBeNull();
	});
});

describe("a thread's row", () => {
	it("shows where it is, what it's about, the newest comment, who is in it and how many comments", async () => {
		mocks.listMyCommentThreads.mockResolvedValue(page([
			row("c-1", { unread: true, sentToAgentAt: at, aside: { ...row("c-1").aside, unread: true } }),
			row("c-2", { muted: true, aside: { asideId: "c-2", anchor: { sequence: 41, kind: "agent_message" }, replyCount: 3, unread: false, muted: true, origin: "comment" }, commentCount: 3 }),
		]));
		renderView();
		const first = await findRow("c-1");
		expect(first).toHaveTextContent("Session c-1");
		expect(first).toHaveTextContent("finance-controller");
		expect(first.querySelector("[data-thread-anchor]")).toHaveTextContent("“real backoff”");
		expect(first.querySelector("[data-thread-last]")).toHaveTextContent("Mohak: Yes, 1 s with jitter.");
		expect(first.querySelectorAll("[data-thread-participants] > *")).toHaveLength(2);
		expect(first).toHaveTextContent("2 comments");
		expect(within(first).getByRole("img", { name: "Unread" })).toBeInTheDocument();
		expect(first.querySelector("[data-thread-sent]")).toHaveTextContent("Sent to agent");
		const second = document.querySelector<HTMLElement>("[data-thread-row='c-2']")!;
		// A thread on the whole answer, with no quote: and no anchorAt (the message is gone), so no time.
		expect(second.querySelector("[data-thread-anchor]")).toHaveTextContent(/^On the agent's answer$/);
		expect(second.querySelector("[data-thread-muted]")).toHaveTextContent("Muted");
		expect(second.querySelector("[data-thread-unread]")).toBeNull();
		expect(second.querySelector("[data-thread-sent]")).toBeNull();
	});

	it("says when its session has ended, which is why an unresolved thread lists under Resolved", async () => {
		mocks.listMyCommentThreads.mockResolvedValue(page([row("c-1", { sessionTerminated: true }), row("c-2")]));
		renderView();
		const ended = await findRow("c-1");
		expect(ended.querySelector("[data-thread-ended]")).toHaveTextContent("Session ended");
		expect(document.querySelector("[data-thread-row='c-2'] [data-thread-ended]")).toBeNull();
	});

	it("reads the next 50 with Load more, each thread once", async () => {
		mocks.listMyCommentThreads
			.mockResolvedValueOnce(page([row("c-1"), row("c-2")], "cursor-2"))
			// c-2 moved (new activity) and shows up on the next page again: it stays one row.
			.mockResolvedValueOnce(page([row("c-2"), row("c-3")]));
		renderView();
		fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
		await waitFor(() => expect(mocks.listMyCommentThreads).toHaveBeenLastCalledWith({ status: "open", cursor: "cursor-2", limit: 50 }, expect.anything()));
		await waitFor(() => expect(document.querySelectorAll("[data-thread-row]")).toHaveLength(3));
		expect(Array.from(document.querySelectorAll<HTMLElement>("[data-thread-row]")).map((node) => node.dataset.threadRow)).toEqual(["c-1", "c-2", "c-3"]);
		expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
	});
});

describe("opening a thread from the list", () => {
	it("shows it beside the list and marks it read", async () => {
		mocks.listMyCommentThreads.mockResolvedValue(page([row("c-1", { unread: true, aside: { ...row("c-1").aside, unread: true } })]));
		renderView();
		fireEvent.click(await findRow("c-1"));
		const pane = await screen.findByTestId("aside-pane");
		expect(pane.querySelector("[data-comment-pane-title]")).toHaveTextContent("Session c-1");
		await waitFor(() => expect(within(pane).getByText("Should this use jitter?")).toBeInTheDocument());
		await waitFor(() => expect(mocks.markSessionAsideRead).toHaveBeenCalledWith("org-1", "session-c-1", "c-1"));
		// Its row's dot goes at once.
		await waitFor(() => expect(document.querySelector("[data-thread-row='c-1'] [data-thread-unread]")).toBeNull());
		expect(document.querySelector("[data-thread-row='c-1']")).toHaveAttribute("aria-current", "true");
	});

	it("reads a new comment on the thread it shows, and keeps it read", async () => {
		mocks.listMyCommentThreads.mockResolvedValue(page([row("c-1")]));
		const queryClient = renderView();
		fireEvent.click(await findRow("c-1"));
		await waitFor(() => expect(mocks.markSessionAsideRead).toHaveBeenCalledTimes(1));
		const reads = mocks.getSessionAside.mock.calls.length;
		// Someone comments while it is open: the list says so (unread, newer activity).
		mocks.listMyCommentThreads.mockResolvedValue(page([row("c-1", { unread: true, lastActivityAt: "2026-11-24T11:00:00Z", aside: { ...row("c-1").aside, unread: true } })]));
		await act(() => queryClient.invalidateQueries({ queryKey: ["cloud-comment-threads"] }));
		await waitFor(() => expect(mocks.getSessionAside.mock.calls.length).toBeGreaterThan(reads));
		await waitFor(() => expect(mocks.markSessionAsideRead).toHaveBeenCalledTimes(2));
	});

	it("opens its session at the message with Open in session", async () => {
		mocks.listMyCommentThreads.mockResolvedValue(page([row("c-1")]));
		renderView();
		fireEvent.click(await findRow("c-1"));
		const pane = await screen.findByTestId("aside-pane");
		fireEvent.click(await within(pane).findByRole("button", { name: "Open in session" }));
		expect(mocks.openInSession).toHaveBeenCalledWith({ orgId: "org-1", projectId: "project-1", sessionId: "session-c-1", asideId: "c-1", sequence: 41 });
	});

	it("opens a tagged message's thread in its session at that message, as it does a comment thread", async () => {
		const tagAside = { asideId: "a-1", anchor: { sequence: 42, kind: "user_message" as const }, replyCount: 1, unread: false, muted: false, origin: "tag" as const, startedBy: { userId: "u-chen", name: "Chen Li" } };
		mocks.listMyCommentThreads.mockResolvedValue(page([row("a-1", { aside: tagAside })]));
		mocks.getSessionAside.mockResolvedValue({ aside: tagAside, message: { kind: "user_message", sequence: 42, text: "Can you check the backoff?", mentions: [], authorName: "Chen Li", createdAt: at }, replies: [], hasMore: false });
		mocks.markSessionAsideRead.mockResolvedValue({ ...tagAside, unread: false });
		renderView();
		fireEvent.click(await findRow("a-1"));
		const pane = await screen.findByTestId("aside-pane");
		await within(pane).findByText("Can you check the backoff?");
		fireEvent.click(within(pane).getByRole("button", { name: "Open in session" }));
		expect(mocks.openInSession).toHaveBeenCalledWith({ orgId: "org-1", projectId: "project-1", sessionId: "session-a-1", asideId: "a-1", sequence: 42 });
	});

	it("keeps a thread a notification opened read-only until its session says it is live", async () => {
		let answer: (value: unknown) => void = () => undefined;
		mocks.getSession.mockImplementation(() => new Promise((resolve) => { answer = resolve; }));
		act(() => openThreadInView({ orgId: "org-1", sessionId: "session-x", asideId: "c-9" }, { focusReply: true }));
		renderView();
		const pane = await screen.findByTestId("aside-pane");
		expect(pane.querySelector("[data-aside-reply-box]")).toBeNull();
		await act(async () => answer({ session: { id: "session-x", projectId: "project-9", displayName: "Notification session", isTerminated: false } }));
		await waitFor(() => expect(pane.querySelector("[data-aside-reply-box]")).not.toBeNull());
	});

	it("stays read-only for a thread a notification opened in an ended session", async () => {
		mocks.getSession.mockResolvedValue({ session: { id: "session-x", projectId: "project-9", displayName: "Notification session", isTerminated: true } });
		act(() => openThreadInView({ orgId: "org-1", sessionId: "session-x", asideId: "c-9" }, { focusReply: true }));
		renderView();
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(pane.querySelector("[data-comment-pane-title]")).toHaveTextContent("Notification session"));
		expect(pane.querySelector("[data-aside-reply-box]")).toBeNull();
		expect(pane.querySelector("[data-comment-read-only]")).not.toBeNull();
	});

	it("takes a session that ended since the thread was opened from its current row", async () => {
		mocks.listMyCommentThreads.mockResolvedValue(page([row("c-1")]));
		const queryClient = renderView();
		fireEvent.click(await findRow("c-1"));
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(pane.querySelector("[data-aside-reply-box]")).not.toBeNull());
		mocks.listMyCommentThreads.mockResolvedValue(page([row("c-1", { sessionTerminated: true })]));
		await act(() => queryClient.invalidateQueries({ queryKey: ["cloud-comment-threads"] }));
		await waitFor(() => expect(pane.querySelector("[data-aside-reply-box]")).toBeNull());
		expect(pane.querySelector("[data-comment-read-only]")).not.toBeNull();
	});	it("keeps a thread in an ended session read-only", async () => {
		mocks.listMyCommentThreads.mockResolvedValue(page([row("c-1", { sessionTerminated: true })]));
		renderView();
		fireEvent.click(await findRow("c-1"));
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(within(pane).getByText("Should this use jitter?")).toBeInTheDocument());
		expect(pane.querySelector("[data-aside-reply-box]")).toBeNull();
		expect(pane.querySelector("[data-comment-read-only]")).not.toBeNull();
		expect(within(pane).queryByRole("button", { name: "Resolve" })).toBeNull();
	});

	it("takes a notification-opened thread's title and send rights from its row once the list has it", async () => {
		act(() => openThreadInView({ orgId: "org-1", sessionId: "session-c-1", asideId: "c-1", projectId: "project-1" }, { focusReply: true }));
		// The list answers after the pane is up.
		let answer: (page: CloudCpCommentThreadPage) => void = () => undefined;
		mocks.listMyCommentThreads.mockImplementation(() => new Promise((resolve) => { answer = resolve; }));
		mocks.getSession.mockImplementation(() => new Promise(() => undefined));
		renderView();
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(within(pane).getByText("Should this use jitter?")).toBeInTheDocument());
		expect(within(pane).queryByRole("checkbox", { name: "Also send to agent" })).toBeNull();
		await act(async () => answer(page([row("c-1", { canPrompt: true, sessionTitle: "Speed up session start" })])));
		await waitFor(() => expect(pane.querySelector("[data-comment-pane-title]")).toHaveTextContent("Speed up session start"));
		expect(await within(pane).findByRole("checkbox", { name: "Also send to agent" })).toBeInTheDocument();
	});

	it("shows a thread a notification opened, before its row is listed", async () => {
		mocks.getSession.mockResolvedValue({ session: { id: "session-x", projectId: "project-9", displayName: "Notification session" } });
		act(() => openThreadInView({ orgId: "org-1", sessionId: "session-x", asideId: "c-9" }, { focusReply: true }));
		renderView();
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(pane.querySelector("[data-comment-pane-title]")).toHaveTextContent("Notification session"));
		await waitFor(() => expect(mocks.markSessionAsideRead).toHaveBeenCalledWith("org-1", "session-x", "c-9"));
		expect(peekThreadInView()?.asideId).toBe("c-9");
	});
});

describe("muting from the pane", () => {
	it("marks the row muted at once", async () => {
		mocks.listMyCommentThreads.mockResolvedValue(page([row("c-1")]));
		mocks.muteSessionAside.mockImplementation(async (_o: string, _s: string, asideId: string, muted: boolean) => ({ ...thread(asideId).aside, muted, unread: false }));
		renderView();
		fireEvent.click(await findRow("c-1"));
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(within(pane).getByText("Should this use jitter?")).toBeInTheDocument());
		expect((await findRow("c-1")).querySelector("[data-thread-muted]")).toBeNull();
		// The list is read again after the mute: answer with the muted row from then on.
		mocks.listMyCommentThreads.mockResolvedValue(page([row("c-1", { muted: true })]));
		const user = userEvent.setup();
		await user.click(within(pane).getByRole("button", { name: "Comment options" }));
		await user.click(await screen.findByRole("menuitem", { name: "Mute thread" }));
		await waitFor(() => expect(mocks.muteSessionAside).toHaveBeenCalledWith("org-1", "session-c-1", "c-1", true));
		await waitFor(() => expect(document.querySelector("[data-thread-row='c-1'] [data-thread-muted]")).toHaveTextContent("Muted"));
	});
});

describe("a standalone session's row", () => {
	it("says the session's name once, not again as its project", async () => {
		mocks.listMyCommentThreads.mockResolvedValue(page([row("c-1", { sessionTitle: "Speed up session start", projectName: "Speed up session start" }), row("c-2")]));
		renderView();
		expect((await findRow("c-1")).querySelector("[data-thread-project]")).toBeNull();
		expect((await findRow("c-2")).querySelector("[data-thread-project]")).toHaveTextContent("finance-controller");
	});
});

describe("a thread on a whole message", () => {
	it("says when the message was sent, when it still exists", async () => {
		const whole = (asideId: string, anchor: CloudCpCommentThread["aside"]["anchor"], extra: Partial<CloudCpCommentThread["aside"]> = {}, anchorAt?: string) =>
			row(asideId, { ...(anchorAt ? { anchorAt } : {}), aside: { asideId, anchor, replyCount: 1, unread: false, muted: false, origin: "comment", ...extra } });
		mocks.listMyCommentThreads.mockResolvedValue(page([
			whole("c-1", { sequence: 41, kind: "agent_message" }, {}, at),
			whole("c-2", { sequence: 42, kind: "user_message" }, { origin: "tag", startedBy: { userId: "u-chen", name: "Chen Li" } }, at),
			whole("c-3", { sequence: 43, kind: "user_message" }, {}, at),
		]));
		renderView();
		const anchorOf = async (asideId: string) => (await findRow(asideId)).querySelector("[data-thread-anchor]")?.textContent;
		expect(await anchorOf("c-1")).toMatch(/^On the agent's answer · \S/);
		// A tagged message's thread: its author started it.
		expect(await anchorOf("c-2")).toMatch(/^On Chen Li's message · \S/);
		expect(await anchorOf("c-3")).toMatch(/^On a message · \S/);
	});
});

describe("Also send to agent in the threads view", () => {
	it("follows whether the row says this person may prompt the session", async () => {
		mocks.listMyCommentThreads.mockResolvedValue(page([row("c-1", { canPrompt: true }), row("c-2", { canPrompt: false })]));
		renderView();
		fireEvent.click(await findRow("c-1"));
		let pane = await screen.findByTestId("aside-pane");
		expect(await within(pane).findByRole("checkbox", { name: "Also send to agent" })).toBeInTheDocument();
		fireEvent.click(await findRow("c-2"));
		pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(pane.querySelector("[data-comment-pane-title]")).toHaveTextContent("Session c-2"));
		await waitFor(() => expect(within(pane).getByText("Should this use jitter?")).toBeInTheDocument());
		expect(within(pane).queryByRole("checkbox", { name: "Also send to agent" })).toBeNull();
	});

	it("isn't offered by an older control plane that doesn't say", async () => {
		const older = row("c-1");
		delete older.canPrompt;
		mocks.listMyCommentThreads.mockResolvedValue(page([older]));
		renderView();
		fireEvent.click(await findRow("c-1"));
		const pane = await screen.findByTestId("aside-pane");
		await waitFor(() => expect(within(pane).getByText("Should this use jitter?")).toBeInTheDocument());
		expect(within(pane).queryByRole("checkbox", { name: "Also send to agent" })).toBeNull();
	});
});
