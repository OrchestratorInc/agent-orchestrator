import { waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { CloudCpClientEvent } from "../../lib/cloud-cp";
import { mentionFallbackEvents } from "../../lib/cloud-mention-open";
import { renderChat as render } from "../../test/render-chat";
import type { WorkspaceSession } from "../../types/workspace";
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
	it("lands on the message in its first layout and gives it a brief glow", async () => {
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
		expect(row).toHaveAttribute("data-mention-focus");
		await waitFor(() => expect(row).not.toHaveAttribute("data-mention-focus"), { timeout: 3000 });
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
		expect(row).toHaveAttribute("data-mention-focus");
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
		expect(document.querySelector("[data-mention-focus]")).toBeNull();
	});
});
