import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { ConversationActivity, ConversationMessage } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";
import { AsideMarkersContext, type AsideIconState, type AsideMarkers } from "./AsideMarker";
import { HumanMessage, SteerMessage } from "./ChatTimelineItems";
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

function markers(state: AsideIconState, toggle = vi.fn()): AsideMarkers {
	return { stateOf: () => state, toggle };
}

/** Rendered for Priya, who is tagged: a tinted chip would be hers. */
function renderFor(node: ReactNode, value: AsideMarkers | undefined) {
	return render(
		<TooltipProvider>
			<MentionTintContext.Provider value={{ userId: "u-priya", teamIds: new Set(["t-platform"]) }}>
				<AsideMarkersContext.Provider value={value}>{node}</AsideMarkersContext.Provider>
			</MentionTintContext.Provider>
		</TooltipProvider>,
	);
}

describe("a message that tags people", () => {
	it("is only a dashed box with its text and chips: no author line, no fill, no tint", () => {
		const { container } = renderFor(<HumanMessage message={tagged()} sessionId="s1" />, markers("closed"));
		const box = container.querySelector<HTMLElement>("[data-chat-aside-box]")!;
		expect(box).toHaveClass("border-dashed", "bg-transparent");
		expect(box).not.toHaveClass("cursor-chat-human-message");
		expect(container.querySelector("[data-chat-message-author]")).toBeNull();
		expect(screen.queryByText("Chen Li")).toBeNull();
		// Its chips stay chips, and look the same for the sender and everyone tagged.
		expect(box.querySelectorAll("[data-mention-chip]")).toHaveLength(2);
		expect(box.querySelector("[data-mention-self]")).toBeNull();
		// The old bubble never draws, so nothing glows when a mention opens on it.
		expect(container.querySelector(".cursor-chat-human-message")).toBeNull();
		expect(container.querySelector("[data-chat-focus-bubble]")).toBeNull();
	});

	it("has the aside icon right under the box, at its left edge", () => {
		const { container } = renderFor(<HumanMessage message={tagged()} sessionId="s1" />, markers("closed"));
		const column = container.querySelector<HTMLElement>("[data-chat-aside='a-1']")!;
		expect(column).toHaveClass("items-start");
		expect(column.children[0]).toHaveAttribute("data-chat-aside-box");
		const icon = screen.getByRole("button", { name: "Open aside" });
		expect(column.contains(icon)).toBe(true);
		expect(icon.querySelector(".icon-messages-square")).not.toBeNull();
		// The icon alone: no text, no avatars, no count.
		expect(icon).toHaveTextContent("");
	});

	it("draws the icon closed, lit while its aside is open, and with a dot for unseen replies", () => {
		const { rerender } = renderFor(<HumanMessage message={tagged()} sessionId="s1" />, markers("closed"));
		let icon = screen.getByRole("button", { name: "Open aside" });
		expect(icon).toHaveAttribute("aria-pressed", "false");
		expect(icon.querySelector("[data-aside-unread]")).toBeNull();

		rerender(<TooltipProvider><AsideMarkersContext.Provider value={markers("open")}><HumanMessage message={tagged()} sessionId="s1" /></AsideMarkersContext.Provider></TooltipProvider>);
		icon = screen.getByRole("button", { name: "Close aside" });
		expect(icon).toHaveAttribute("aria-pressed", "true");
		expect(icon).toHaveAttribute("data-active", "true");

		rerender(<TooltipProvider><AsideMarkersContext.Provider value={markers("unread")}><HumanMessage message={tagged()} sessionId="s1" /></AsideMarkersContext.Provider></TooltipProvider>);
		icon = screen.getByRole("button", { name: "Open aside, new replies" });
		expect(icon.querySelector("[data-aside-unread]")).not.toBeNull();
	});

	it("opens and closes its aside from the icon", () => {
		const toggle = vi.fn();
		renderFor(<HumanMessage message={tagged()} sessionId="s1" />, markers("closed", toggle));
		fireEvent.click(screen.getByRole("button", { name: "Open aside" }));
		expect(toggle).toHaveBeenCalledWith("a-1");
	});

	it("draws as before on a control plane without asides", () => {
		const { container } = renderFor(<HumanMessage message={tagged()} sessionId="s1" />, undefined);
		expect(container.querySelector("[data-chat-aside-box]")).toBeNull();
		expect(container.querySelector(".cursor-chat-human-message")).not.toBeNull();
		expect(screen.getByText("Chen Li")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Open aside" })).toBeNull();
	});

	it("draws a steer that tags people the same way", () => {
		const steer: ConversationActivity = {
			kind: "activity", id: "cloud-steer-50", sequence: 50, revision: 1, activityKind: "system", status: "completed",
			summary: "Steered: @Platform check this", createdAt: "2026-11-13T10:00:00Z",
			detail: { event: "steer", text: "@Platform check this", origin: "human", mentions: [mentions[1]!], asideId: "a-2" },
		};
		const { container } = renderFor(<SteerMessage activity={steer} sessionId="s1" />, markers("unread"));
		expect(container.querySelector("[data-chat-aside='a-2'] [data-chat-aside-box]")).toHaveClass("border-dashed");
		expect(container.querySelector("[data-chat-focus-bubble]")).toBeNull();
		expect(screen.getByRole("button", { name: "Open aside, new replies" })).toBeInTheDocument();
	});
});
