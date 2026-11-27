import { waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { CloudCpClientEvent } from "../../lib/cloud-cp";
import { mentionFallbackEvents } from "../../lib/cloud-mention-open";
import { renderChat as render } from "../../test/render-chat";
import type { WorkspaceSession } from "../../types/workspace";
import { COMMENT_COLUMN_CLASS, CommentMarkersContext, type CommentMarkers } from "./AsideMarker";
import { ChatWorkspace } from "./ChatWorkspace";
import { toSnapshot } from "./CloudSessionChatSurface";
import { MentionTintContext } from "./MentionChip";

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
} satisfies WorkspaceSession;

const at = "2026-11-13T10:00:00Z";
const events: CloudCpClientEvent[] = [
	{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "The retry test is flaky.", turnId: "turn-1", authorUserId: "u-marco", authorName: "Marco Rossi" }, createdAt: at },
	{ sessionId: session.id, sequence: 2, type: "chat.assistant_delta", payload: { text: "It uses a real backoff.", turnId: "turn-1", itemId: "reply" }, createdAt: at },
	{ sessionId: session.id, sequence: 3, type: "chat.turn_completed", payload: { turnId: "turn-1" }, createdAt: at },
	{
		sessionId: session.id, sequence: 4, type: "chat.user_message",
		payload: {
			text: "Can you check the backoff? @Priya Shah @Platform @Ben Ortiz", turnId: "turn-2", authorUserId: "u-chen", authorName: "Chen Li",
			mentions: [
				{ kind: "person", id: "u-priya", name: "Priya Shah" },
				{ kind: "team", id: "t-platform", name: "Platform" },
				{ kind: "person", id: "u-ben", name: "Ben Ortiz" },
			],
		},
		createdAt: at,
	},
];

describe("mentions in a Cloud chat's timeline", () => {
	it("draws a sent message's mentions as chips, tinting the reader's own and their team's", () => {
		render(
			<MentionTintContext.Provider value={{ userId: "u-priya", teamIds: new Set(["t-platform"]) }}>
				<ChatWorkspace snapshot={toSnapshot(session, events, "u-priya")} session={session} />
			</MentionTintContext.Provider>,
		);
		const chips = Array.from(document.querySelectorAll<HTMLElement>("[data-mention-chip]"));
		expect(chips.map((chip) => [chip.dataset.mentionChip, chip.textContent?.replace(/^[A-Z]{1,2}/, ""), chip.hasAttribute("data-mention-self")])).toEqual([
			["person", "Priya Shah", true],
			["team", "Platform", true],
			["person", "Ben Ortiz", false],
		]);
		// The text around the chips is the message's own.
		expect(document.querySelector('[data-chat-message-id="cloud-event-4"]')?.textContent).toContain("Can you check the backoff?");
	});

	it("leaves a message without mentions as text", () => {
		render(<ChatWorkspace snapshot={toSnapshot(session, events.slice(0, 3), "u-priya")} session={session} />);
		expect(document.querySelector("[data-mention-chip]")).toBeNull();
	});
});

describe("opening a chat at a mention", () => {
	it("lands on the message in its first layout and tints its bubble for a moment", async () => {
		const done = vi.fn();
		render(
			<ChatWorkspace
				snapshot={toSnapshot(session, events, "u-priya")}
				session={session}
				focusMessage={{ messageId: "cloud-event-4", key: "1:loaded" }}
				onFocusMessageDone={done}
			/>,
		);
		// Landed before anything waited on a read: the message was already there.
		expect(done).toHaveBeenCalledWith("1:loaded");
		const row = document.querySelector<HTMLElement>('[data-chat-focus-id="cloud-event-4"]');
		// The shared flash tints the bubble, not the row around it.
		const bubble = row?.querySelector<HTMLElement>(".cursor-chat-human-message");
		expect(bubble).toHaveAttribute("data-chat-flash");
		expect(row).not.toHaveAttribute("data-chat-flash");
		await waitFor(() => expect(bubble).not.toHaveAttribute("data-chat-flash"), { timeout: 3000 });
	});

	it("lands on the notification's own text when nothing else is loaded yet", () => {
		const done = vi.fn();
		const fallback = mentionFallbackEvents({
			notificationId: "n-1", kind: "mention", orgId: "org-1", sessionId: session.id, sequence: 4,
			authorUserId: "u-chen", authorName: "Chen Li", body: "Can you check the backoff? @Priya Shah", createdAt: at,
		})!;
		render(
			<ChatWorkspace
				snapshot={toSnapshot(session, fallback, "u-priya", { partial: true })}
				session={session}
				historyPending
				focusMessage={{ messageId: "cloud-event-4", key: "1:fallback" }}
				onFocusMessageDone={done}
			/>,
		);
		const row = document.querySelector<HTMLElement>('[data-chat-focus-id="cloud-event-4"]');
		expect(row?.textContent).toContain("Can you check the backoff? @Priya Shah");
		expect(row?.textContent).toContain("Chen Li");
		expect(row?.querySelector("[data-chat-flash]")).not.toBeNull();
		expect(done).toHaveBeenCalledWith("1:fallback");
	});

	it("opens as usual when the message is not in the history it can read", () => {
		const done = vi.fn();
		render(
			<ChatWorkspace
				snapshot={toSnapshot(session, events, "u-priya")}
				session={session}
				focusMessage={{ messageId: "cloud-event-99", key: "2:loaded" }}
				onFocusMessageDone={done}
			/>,
		);
		expect(done).toHaveBeenCalledWith("2:loaded");
		expect(document.querySelector("[data-chat-flash]")).toBeNull();
	});
});

describe("the column of a Cloud chat with comments", () => {
	const markers: CommentMarkers = {
		canComment: true,
		// Every message has a thread, so each draws its margin icon.
		forMessage: () => ({ state: "idle", asideIds: ["a-1"], quotes: [] }),
		openMessage: vi.fn(),
		openThread: vi.fn(),
		startComment: vi.fn(),
	};
	const column = (name: "messages" | "composer") => document.querySelector<HTMLElement>(`[data-chat-column="${name}"]`);
	const widthClasses = (element: HTMLElement | null) => Array.from(element?.classList ?? []).filter((name) => /^(max-w-|w-|p[xrl]?-|m[xrl]-)/.test(name)).sort();

	it("gives the messages exactly the composer's column, with no gutter of their own", () => {
		render(
			<CommentMarkersContext.Provider value={markers}>
				<ChatWorkspace snapshot={toSnapshot(session, events.slice(0, 3), "u-priya")} session={session} />
			</CommentMarkersContext.Provider>,
		);
		expect(column("messages")).toHaveClass(COMMENT_COLUMN_CLASS);
		expect(column("composer")).toHaveClass(COMMENT_COLUMN_CLASS);
		expect(widthClasses(column("messages"))).toEqual(widthClasses(column("composer")));
	});

	it("keeps the column of a chat without comments", () => {
		render(<ChatWorkspace snapshot={toSnapshot(session, events.slice(0, 3), "u-priya")} session={session} />);
		expect(widthClasses(column("messages"))).toEqual(["max-w-3xl", "mx-auto", "w-full"]);
		expect(widthClasses(column("composer"))).toEqual(widthClasses(column("messages")));
	});

	it("draws the margin icon beside a message, never around it", () => {
		render(
			<CommentMarkersContext.Provider value={markers}>
				<ChatWorkspace snapshot={toSnapshot(session, events.slice(0, 3), "u-priya")} session={session} />
			</CommentMarkersContext.Provider>,
		);
		const icons = Array.from(document.querySelectorAll<HTMLElement>("[data-comment-marker]"));
		// The person's message and the agent's answer.
		expect(icons).toHaveLength(2);
		for (const icon of icons) {
			const target = icon.parentElement!;
			expect(target).toHaveAttribute("data-comment-anchor");
			// Out of the flow, so the message keeps the column's full width.
			expect(icon).toHaveClass("absolute");
			expect(icon.querySelector("[data-chat-message-body], [data-chat-message-id], [data-chat-message-role]")).toBeNull();
			// The target only positions its icon: no width, padding or margin of its own.
			expect(widthClasses(target)).toEqual([]);
			expect(target.querySelector("[data-chat-message-body]")).not.toBeNull();
		}
		for (const toolbar of document.querySelectorAll<HTMLElement>("[data-comment-toolbar]")) {
			expect(toolbar).toHaveClass("absolute");
			expect(toolbar.querySelector("[data-chat-message-body]")).toBeNull();
		}
	});
});
