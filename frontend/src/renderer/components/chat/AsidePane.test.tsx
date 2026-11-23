import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ComponentProps } from "react";
import { describe, expect, it, vi } from "vitest";
import type { CloudCpAsideHistoryEntry } from "../../lib/cloud-cp/types";
import { TooltipProvider } from "../ui/tooltip";
import { AsidePaneView, type AsideEntry } from "./AsidePane";

const at = "2026-11-24T10:00:00Z";
const mohak = { userId: "u-mohak", name: "Mohak" };
const replies: AsideEntry[] = [
	{ id: "r-1", text: "Should this use jitter?", mentions: [], authorName: "Pritom", createdAt: at, sequence: 10 },
	{ id: "r-2", text: "Yes, 1 s with jitter.", mentions: [], authorName: "Mohak", createdAt: at, sequence: 14 },
];
const history: CloudCpAsideHistoryEntry[] = [
	{ kind: "resolved", actor: mohak, at, sequence: 12 },
	{ kind: "reopened", actor: mohak, at, sequence: 15 },
	{ kind: "sent_to_agent", actor: mohak, at, sequence: 21, sentMessage: { sequence: 22 } },
];

function renderPane(props: Partial<ComponentProps<typeof AsidePaneView>> = {}) {
	const onReply = vi.fn(async () => undefined);
	render(
		<TooltipProvider>
			<AsidePaneView
				anchor={{ quote: "real backoff", onJump: vi.fn() }}
				label="Comment"
				muted={false}
				onClose={vi.fn()}
				onReply={onReply}
				replies={replies}
				sessionTitle="Evidence verifier"
				{...props}
			/>
		</TooltipProvider>,
	);
	return { pane: screen.getByTestId("aside-pane"), onReply };
}

describe("an open thread", () => {
	it("shows its comments with its resolves, reopens and sends among them", () => {
		const { pane } = renderPane({ history, onResolve: vi.fn() });
		expect(pane).toHaveAttribute("data-comment-state", "open");
		const rows = Array.from(pane.querySelectorAll("[data-aside-entry], [data-aside-history]")).map((row) => row.textContent);
		expect(rows[0]).toContain("Should this use jitter?");
		expect(rows[1]).toContain("Mohak resolved this");
		expect(rows[2]).toContain("Yes, 1 s with jitter.");
		expect(rows[3]).toContain("Mohak reopened this");
		// A send is one entry, and it resolved the thread too.
		expect(rows[4]).toContain("Sent to the agent · resolved by Mohak");
		expect(within(pane).getByRole("button", { name: "Resolve" })).toBeInTheDocument();
		expect(within(pane).queryByRole("button", { name: "Reopen" })).toBeNull();
		// The reply box sends with an arrow, not "Reply and reopen".
		expect(within(pane).getByRole("button", { name: "Send comment" })).toBeInTheDocument();
		expect(pane.querySelector("[data-comment-reply-reopen]")).toBeNull();
	});

	it("offers Mark unread and Mute thread in its menu", async () => {
		const user = userEvent.setup();
		const onMarkUnread = vi.fn();
		const onMute = vi.fn();
		const { pane } = renderPane({ onMarkUnread, onMute });
		await user.click(within(pane).getByRole("button", { name: "Comment options" }));
		await user.click(await screen.findByRole("menuitem", { name: "Mark unread" }));
		expect(onMarkUnread).toHaveBeenCalledTimes(1);
		await user.click(within(pane).getByRole("button", { name: "Comment options" }));
		await user.click(await screen.findByRole("menuitem", { name: "Mute thread" }));
		expect(onMute).toHaveBeenCalledWith(true);
	});
});

describe("a resolved thread", () => {
	it("collapses to one status line, with its comments behind Show the N comments", () => {
		const { pane } = renderPane({ history: history.slice(0, 1), resolved: { byName: "Mohak", at, sent: false }, onReopen: vi.fn() });
		expect(pane).toHaveAttribute("data-comment-state", "resolved");
		expect(pane.querySelector("[data-comment-resolved]")).toHaveTextContent(/^Resolved by Mohak · /);
		// The quote stays: what the thread was about.
		expect(pane.querySelector("[data-comment-anchor-block]")).toHaveTextContent("“real backoff”");
		expect(pane.querySelectorAll("[data-aside-entry]")).toHaveLength(0);
		const toggle = within(pane).getByRole("button", { name: "Show the 2 comments" });
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		fireEvent.click(toggle);
		expect(pane.querySelectorAll("[data-aside-entry]")).toHaveLength(2);
		expect(pane.querySelector("[data-aside-history='resolved']")).toHaveTextContent("Mohak resolved this");
		fireEvent.click(within(pane).getByRole("button", { name: "Hide comments" }));
		expect(pane.querySelectorAll("[data-aside-entry]")).toHaveLength(0);
	});

	it("has Reopen instead of Resolve, and its send button reads Reply and reopen", () => {
		const onReopen = vi.fn();
		const { pane } = renderPane({ resolved: { byName: "Mohak", at, sent: false }, onReopen, canSendToAgent: true });
		expect(within(pane).queryByRole("button", { name: "Resolve" })).toBeNull();
		fireEvent.click(within(pane).getByRole("button", { name: "Reopen" }));
		expect(onReopen).toHaveBeenCalledTimes(1);
		expect(within(pane).getByRole("button", { name: "Reply and reopen" })).toBeDisabled();
		// A resolved thread is not sent from here: replying reopens it first.
		expect(within(pane).queryByRole("checkbox", { name: "Also send to agent" })).toBeNull();
	});

	it("says when it was sent to the agent, with a jump to the message it became", () => {
		const onJumpToSent = vi.fn();
		const { pane } = renderPane({ resolved: { byName: "Mohak", at, sent: true, onJumpToSent }, onReopen: vi.fn() });
		expect(pane.querySelector("[data-comment-resolved]")).toHaveTextContent(/^Sent to the agent · resolved by Mohak · /);
		fireEvent.click(within(pane).getByRole("button", { name: "Jump to the sent message" }));
		expect(onJumpToSent).toHaveBeenCalledTimes(1);
	});

	it("names the jumps for outside the session", () => {
		const { pane } = renderPane({
			anchor: { quote: "real backoff", onJump: vi.fn(), jumpLabel: "Open in session" },
			resolved: { byName: "Mohak", at, sent: true, onJumpToSent: vi.fn(), jumpLabel: "Open the sent message" },
			onReopen: vi.fn(),
		});
		expect(within(pane).getByRole("button", { name: "Open in session" })).toBeInTheDocument();
		expect(within(pane).getByRole("button", { name: "Open the sent message" })).toBeInTheDocument();
		expect(within(pane).queryByRole("button", { name: "Jump to message" })).toBeNull();
	});

	it("is read-only once its session ended: no reply box, Resolve or Reopen", () => {
		const { pane } = renderPane({ replyDisabled: "This session ended. Comments are read-only.", resolved: { byName: "Mohak", at, sent: false } });
		expect(pane.querySelector("[data-aside-reply-box]")).toBeNull();
		expect(pane.querySelector("[data-comment-read-only]")).toHaveTextContent("This session ended. Comments are read-only.");
		expect(within(pane).queryByRole("button", { name: "Reopen" })).toBeNull();
		expect(within(pane).queryByRole("button", { name: "Resolve" })).toBeNull();
	});
});

describe("the pane's header", () => {
	it("is the session's title alone", () => {
		const { pane } = renderPane();
		expect(pane.querySelector("header")?.firstElementChild).toHaveTextContent(/^Evidence verifier$/);
		expect(pane).toHaveAccessibleName("Comment");
	});
});
