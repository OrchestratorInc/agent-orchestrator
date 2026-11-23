import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { ConversationActivity, ConversationMessage } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";
import {
	CommentMarkersContext,
	CommentTarget,
	commentDraftForSelection,
	findQuoteRange,
	messageExcerpt,
	type CommentMarkers,
	type CommentMarkerState,
	type MessageComments,
} from "./AsideMarker";
import { AssistantMessage, HumanMessage, SteerMessage } from "./ChatTimelineItems";
import { MentionTintContext } from "./MentionChip";

const mentions = [
	{ kind: "person" as const, id: "u-priya", name: "Priya Shah" },
	{ kind: "team" as const, id: "t-platform", name: "Platform" },
];

function tagged(overrides: Partial<ConversationMessage> = {}): ConversationMessage {
	return {
		kind: "message", id: "cloud-event-42", sequence: 42, revision: 1, role: "user", origin: "human",
		text: "Can you check the retry backoff before we merge? @Priya Shah @Platform",
		authorName: "Chen Li", mentions, asideId: "a-1", streaming: false, delivery: "accepted",
		createdAt: "2026-11-13T10:00:00Z",
		...overrides,
	};
}

const answer: ConversationMessage = {
	kind: "message", id: "cloud-assistant-t1:item", sequence: 60, revision: 1, role: "assistant", origin: "provider",
	text: "The time goes to the additional repositories. litellm-docs alone is 700 MB.", streaming: false, createdAt: "2026-11-13T10:01:00Z",
};

function comments(state: CommentMarkerState, asideIds = ["a-1"], quotes: MessageComments["quotes"] = []): MessageComments {
	return { state, asideIds, quotes };
}

function markers(overrides: Partial<CommentMarkers> = {}, forMessage: CommentMarkers["forMessage"] = () => undefined): CommentMarkers {
	return { canComment: true, forMessage, openMessage: vi.fn(), openThread: vi.fn(), startComment: vi.fn(), ...overrides };
}

/** Rendered for Priya, who is tagged: a tinted chip would be hers. */
function renderFor(node: ReactNode, value: CommentMarkers | undefined) {
	return render(
		<TooltipProvider>
			<MentionTintContext.Provider value={{ userId: "u-priya", teamIds: new Set(["t-platform"]) }}>
				<CommentMarkersContext.Provider value={value}>{node}</CommentMarkersContext.Provider>
			</MentionTintContext.Provider>
		</TooltipProvider>,
	);
}

function target(message: ConversationMessage, children: ReactNode, finished = true) {
	return (
		<CommentTarget asideId={message.asideId} authorName={message.authorName} finished={finished}
			kind={message.role === "assistant" ? "agent_message" : "user_message"} sequence={message.sequence} text={message.text}>
			{children}
		</CommentTarget>
	);
}

describe("a message that tags people", () => {
	it("is only a dashed box with its text and chips: no author line, no fill, no tint", () => {
		const { container } = renderFor(<HumanMessage message={tagged()} sessionId="s1" />, markers());
		const box = container.querySelector<HTMLElement>("[data-chat-aside-box]")!;
		expect(box).toHaveClass("border-dashed", "bg-transparent");
		expect(box).not.toHaveClass("cursor-chat-human-message");
		expect(container.querySelector("[data-chat-message-author]")).toBeNull();
		expect(screen.queryByText("Chen Li")).toBeNull();
		expect(box.querySelectorAll("[data-mention-chip]")).toHaveLength(2);
		expect(box.querySelector("[data-mention-self]")).toBeNull();
		expect(container.querySelector(".cursor-chat-human-message")).toBeNull();
		expect(container.querySelector("[data-chat-focus-bubble]")).toBeNull();
	});

	it("has nothing under the box: its thread's marker is the margin icon", () => {
		const { container } = renderFor(target(tagged(), <HumanMessage message={tagged()} sessionId="s1" />), markers({}, () => comments("idle")));
		const column = container.querySelector<HTMLElement>("[data-chat-aside='a-1']")!;
		expect(column.children).toHaveLength(1);
		expect(column.querySelector("button")).toBeNull();
		expect(container.querySelectorAll("[data-comment-marker]")).toHaveLength(1);
	});

	it("draws as before on a control plane without comments", () => {
		const { container } = renderFor(target(tagged(), <HumanMessage message={tagged()} sessionId="s1" />), undefined);
		expect(container.querySelector("[data-chat-aside-box]")).toBeNull();
		expect(container.querySelector(".cursor-chat-human-message")).not.toBeNull();
		expect(screen.getByText("Chen Li")).toBeInTheDocument();
		expect(container.querySelector("[data-comment-anchor]")).toBeNull();
		expect(container.querySelector("[data-comment-toolbar]")).toBeNull();
	});

	it("draws a steer that tags people the same way", () => {
		const steer: ConversationActivity = {
			kind: "activity", id: "cloud-steer-50", sequence: 50, revision: 1, activityKind: "system", status: "completed",
			summary: "Steered: @Platform check this", createdAt: "2026-11-13T10:00:00Z",
			detail: { event: "steer", text: "@Platform check this", origin: "human", mentions: [mentions[1]!], asideId: "a-2" },
		};
		const { container } = renderFor(<SteerMessage activity={steer} sessionId="s1" />, markers());
		expect(container.querySelector("[data-chat-aside='a-2'] [data-chat-aside-box]")).toHaveClass("border-dashed");
		expect(container.querySelector("[data-chat-focus-bubble]")).toBeNull();
		expect(container.querySelector("[data-chat-aside='a-2'] button")).toBeNull();
	});
});

describe("commenting on a message", () => {
	it("offers exactly one hover button, Comment, on a finished message", () => {
		const startComment = vi.fn();
		const { container } = renderFor(target(answer, <AssistantMessage message={answer} />), markers({ startComment }));
		const toolbar = container.querySelector<HTMLElement>("[data-comment-toolbar]")!;
		const buttons = toolbar.querySelectorAll("button");
		expect(buttons).toHaveLength(1);
		expect(buttons[0]).toHaveAccessibleName("Comment");
		fireEvent.click(buttons[0]!);
		expect(startComment).toHaveBeenCalledWith({
			anchor: { sequence: 60, kind: "agent_message" },
			excerpt: "The time goes to the additional repositories. litellm-docs alone is 700 MB.",
		});
	});

	it("offers no Comment while a message is still streaming, or once the session ended", () => {
		const { container, rerender } = renderFor(target(answer, <AssistantMessage message={answer} />, false), markers());
		expect(container.querySelector("[data-comment-toolbar]")).toBeNull();
		rerender(<TooltipProvider><CommentMarkersContext.Provider value={markers({ canComment: false })}>{target(answer, <AssistantMessage message={answer} />)}</CommentMarkersContext.Provider></TooltipProvider>);
		expect(container.querySelector("[data-comment-toolbar]")).toBeNull();
	});

	it("names the author of a person's message in the new comment", () => {
		const startComment = vi.fn();
		const message = tagged({ asideId: undefined, mentions: undefined, text: "Ship it after the review." });
		renderFor(target(message, <HumanMessage message={message} sessionId="s1" />), markers({ startComment }));
		fireEvent.click(screen.getByRole("button", { name: "Comment" }));
		expect(startComment).toHaveBeenCalledWith({ anchor: { sequence: 42, kind: "user_message" }, excerpt: "Ship it after the review.", authorName: "Chen Li" });
	});
});

describe("the margin icon", () => {
	it("is one icon for a message, whatever its number of threads, with no count", () => {
		const { container } = renderFor(target(answer, <AssistantMessage message={answer} />), markers({}, () => comments("idle", ["a-1", "a-2", "a-3"])));
		const icons = container.querySelectorAll<HTMLElement>("[data-comment-marker]");
		expect(icons).toHaveLength(1);
		expect(icons[0]).toHaveTextContent("");
		expect(icons[0]).toHaveAccessibleName("Open comments");
		expect(container.querySelectorAll("[data-aside-unread]")).toHaveLength(0);
	});

	it("is absent for a message with no open thread", () => {
		const { container } = renderFor(target(answer, <AssistantMessage message={answer} />), markers());
		expect(container.querySelector("[data-comment-marker]")).toBeNull();
		// "Open in session" from the threads view can still land on the answer.
		expect(container.querySelector(`[data-chat-focus-id="${answer.id}"]`)).not.toBeNull();
	});

	it("turns blue for unseen replies and lights while its thread is open", () => {
		const { container, rerender } = renderFor(target(answer, <AssistantMessage message={answer} />), markers({}, () => comments("unread")));
		let icon = container.querySelector<HTMLElement>("[data-comment-marker]")!;
		expect(icon).toHaveAttribute("data-comment-marker", "unread");
		expect(icon).toHaveClass("text-status-working");
		expect(icon).toHaveAccessibleName("Open comments, new replies");
		rerender(<TooltipProvider><CommentMarkersContext.Provider value={markers({}, () => comments("active"))}>{target(answer, <AssistantMessage message={answer} />)}</CommentMarkersContext.Provider></TooltipProvider>);
		icon = container.querySelector<HTMLElement>("[data-comment-marker]")!;
		expect(icon).toHaveAttribute("aria-pressed", "true");
		expect(icon).toHaveAccessibleName("Close comments");
	});

	it("opens the message's threads", () => {
		const openMessage = vi.fn();
		const value = comments("idle", ["a-1", "a-2"]);
		const { container } = renderFor(target(answer, <AssistantMessage message={answer} />), markers({ openMessage }, () => value));
		fireEvent.click(container.querySelector("[data-comment-marker]")!);
		expect(openMessage).toHaveBeenCalledWith(60, value);
	});
});

describe("quoted passages", () => {
	it("finds a passage across text nodes and line breaks", () => {
		document.body.innerHTML = "<div id='m'><p>The time goes to the <b>additional</b>\n repositories. litellm-docs alone is 700 MB.</p></div>";
		const range = findQuoteRange(document.getElementById("m")!, "the additional repositories.");
		expect(range?.toString()).toBe("the additional\n repositories.");
		expect(findQuoteRange(document.getElementById("m")!, "not in this message")).toBeUndefined();
	});

	it("turns a selection inside one message into a comment on that passage", () => {
		document.body.innerHTML = `<div data-comment-anchor="60" data-comment-kind="agent_message"><div data-chat-message-body><p>The time goes to the additional repositories.</p></div></div>
			<div data-comment-anchor="61" data-comment-kind="user_message" data-comment-author="Mohak"><div data-chat-message-body><p>Other</p></div></div>`;
		const text = document.querySelector("[data-comment-anchor='60'] p")!.firstChild!;
		const range = document.createRange();
		range.setStart(text, 4);
		range.setEnd(text, 8);
		expect(commentDraftForSelection(range)).toEqual({ anchor: { sequence: 60, kind: "agent_message" }, quote: "time", excerpt: "The time goes to the additional repositories." });
		// A selection across two messages is no comment.
		const across = document.createRange();
		across.setStart(text, 4);
		across.setEnd(document.querySelector("[data-comment-anchor='61'] p")!.firstChild!, 2);
		expect(commentDraftForSelection(across)).toBeUndefined();
	});

	it("quotes whole words when a selection stops inside one", () => {
		document.body.innerHTML = `<div data-comment-anchor="11" data-comment-kind="agent_message"><div data-chat-message-body><p>Hi! I'm the orchestrator for the finance-controller project. I can spawn workers.</p></div></div>`;
		const text = document.querySelector("[data-comment-anchor='11'] p")!.firstChild as Text;
		const at = (part: string) => text.data.indexOf(part);
		const select = (from: number, to: number) => {
			const range = document.createRange();
			range.setStart(text, from);
			range.setEnd(text, to);
			return commentDraftForSelection(range)?.quote;
		};
		// The drag stopped inside "spawn" (the "…I can spa" thread): the quote takes the whole word.
		expect(select(at("orchestrator"), at("spawn") + 3)).toBe("orchestrator for the finance-controller project. I can spawn");
		// And one that started inside a word.
		expect(select(at("chestrator"), at(" the finance"))).toBe("orchestrator for");
		// Edges already on word boundaries, or on punctuation, stay where they are.
		expect(select(at("orchestrator"), at(" for"))).toBe("orchestrator");
		expect(select(at("project."), at(" I can"))).toBe("project.");
	});

	it("keeps an excerpt to the message's first line", () => {
		expect(messageExcerpt("\n  First line here\nsecond")).toBe("First line here");
		expect(messageExcerpt("x".repeat(250))).toHaveLength(200);
	});
});

describe("a message Also send to agent sent", () => {
	it("draws as the thread it carried: quote, then every comment in order", async () => {
		const { FromCommentThreadCard } = await import("./ChatTimelineItems");
		const { container } = render(
			<FromCommentThreadCard thread={{ asideId: "c-1", quote: "wait for it in provisioning", comments: [{ authorName: "Pritom", text: "Budget it by repo size?" }, { authorName: "Mohak", text: "Against the clone cost." }] }} />,
		);
		const card = container.querySelector<HTMLElement>("[data-from-comment-thread='c-1']")!;
		expect(card).toHaveTextContent("From a comment thread");
		expect(card).toHaveTextContent("· 2 comments");
		expect(card.querySelector("[data-from-comment-quote]")).toHaveTextContent("“wait for it in provisioning”");
		const rows = card.querySelectorAll("p");
		expect(rows[0]?.querySelector("span")).toHaveTextContent("Pritom");
		expect(rows[0]).toHaveTextContent("Budget it by repo size?");
		expect(rows[1]?.querySelector("span")).toHaveTextContent("Mohak");
		expect(rows[1]).toHaveTextContent("Against the clone cost.");
	});

	it("puts the request first, then the quote, then the comments", async () => {
		const { FromCommentThreadCard } = await import("./ChatTimelineItems");
		const { container } = render(
			<FromCommentThreadCard thread={{
				asideId: "c-1", framed: true, requestText: "Add jitter to the retry.", quote: "real backoff", commentCount: 2,
				comments: [{ authorName: "Pritom", text: "Budget it by repo size?" }, { authorName: "Mohak", text: "Against the clone cost." }],
			}} />,
		);
		const card = container.querySelector<HTMLElement>("[data-from-comment-thread='c-1']")!;
		const order = Array.from(card.querySelectorAll("[data-from-comment-request], [data-from-comment-quote], [data-from-comment-entry]")).map((node) => node.textContent);
		expect(order).toEqual(["Add jitter to the retry.", "“real backoff”", "PritomBudget it by repo size?", "MohakAgainst the clone cost."]);
		// The count is the comments, not the request.
		expect(card).toHaveTextContent("· 2 comments");
	});

	it("says the thread was sent as it was when the reply was empty", async () => {
		const { FromCommentThreadCard } = await import("./ChatTimelineItems");
		const { container } = render(<FromCommentThreadCard thread={{ asideId: "c-1", framed: true, quote: "real backoff", commentCount: 1, comments: [{ authorName: "Pritom", text: "Should this use jitter?" }] }} />);
		expect(container.querySelector("[data-from-comment-request]")).toHaveTextContent("Sent the thread to the agent");
	});

	it("shows three comments, the rest behind Show all, and what the limits left out", async () => {
		const { FromCommentThreadCard } = await import("./ChatTimelineItems");
		const comments = ["one", "two", "three", "four", "five"].map((text, index) => ({ authorName: index % 2 ? "Mohak" : "Pritom", text }));
		const { container } = render(<FromCommentThreadCard thread={{ asideId: "c-1", framed: true, requestText: "Go.", commentCount: 8, omittedComments: 3, comments }} />);
		const card = container.querySelector<HTMLElement>("[data-from-comment-thread='c-1']")!;
		const shown = () => Array.from(card.querySelectorAll("[data-from-comment-entry]")).map((node) => node.textContent?.replace(/^(Pritom|Mohak)/, ""));
		expect(shown()).toEqual(["one", "two", "three"]);
		// The control plane keeps the first comment and the newest: the gap is after the first.
		expect(card.querySelector("[data-from-comment-entry] + [data-from-comment-omitted]")).toHaveTextContent("3 earlier comments omitted");
		expect(card).toHaveTextContent("· 8 comments");
		fireEvent.click(screen.getByRole("button", { name: "Show all 5 comments" }));
		expect(shown()).toEqual(["one", "two", "three", "four", "five"]);
		fireEvent.click(screen.getByRole("button", { name: "Show fewer" }));
		expect(shown()).toHaveLength(3);
	});

	it("says when the earlier comments were sent, for a thread sent again", async () => {
		const { FromCommentThreadCard } = await import("./ChatTimelineItems");
		const { container } = render(<FromCommentThreadCard thread={{ asideId: "c-1", framed: true, requestText: "And now the tests.", sinceSent: true, previouslySentAt: "2026-11-24T10:00:00Z", commentCount: 1, comments: [{ authorName: "Mohak", text: "Tests too." }] }} />);
		expect(container.querySelector("[data-from-comment-since]")).toHaveTextContent(/^Earlier comments were sent at /);
	});

	it("replaces the message's text in its bubble", () => {
		const message = tagged({ asideId: undefined, mentions: undefined, text: "Comment thread on: …", fromAside: { asideId: "c-1", comments: [{ authorName: "Pritom", text: "Do it" }] } });
		const { container } = renderFor(<HumanMessage message={message} sessionId="s1" />, markers());
		expect(container.querySelector("[data-from-comment-thread='c-1']")).not.toBeNull();
		expect(screen.queryByText("Comment thread on: …")).toBeNull();
	});
});
