import { fireEvent, render as rtlRender, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { describe, expect, it } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixture } from "../../lib/chat-fixture";
import type { ConversationItem, ConversationSnapshot, TurnState } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";

// A live turn's work sits in the conversation itself, not in a scroll box of its
// own: the transcript is the one thing that scrolls, and whatever the agent shows
// along the way (an image it rendered, say) stays where it is while the next steps
// arrive under it.

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

const at = "2026-10-17T10:00:00Z";
const user: ConversationItem = { kind: "message", id: "u", turnId: "t", sequence: 1, revision: 0, role: "user", origin: "human", text: "Walk the repo", content: [], streaming: false, delivery: "accepted", createdAt: at };
const note: ConversationItem = { kind: "message", id: "note", turnId: "t", sequence: 2, revision: 0, role: "assistant", origin: "provider", text: "Checking the folder layout first.", streaming: false, createdAt: at };
const command: ConversationItem = { kind: "activity", id: "cmd", turnId: "t", sequence: 3, revision: 0, activityKind: "command", status: "completed", summary: "ls -la", detail: { command: "ls -la", exitCode: 0 }, createdAt: at };

function snapshot(state: TurnState, items: ConversationItem[]): ConversationSnapshot {
	return {
		...chatFixture,
		controller: { state: state === "running" ? "busy" : "ready" },
		turns: [{ id: "t", state, providerTurnId: "p", requestedAt: at, startedAt: at, completedAt: state === "running" ? undefined : at }],
		items,
		latestSequence: items.at(-1)!.sequence,
		oldestSequence: 1,
		continueTurnId: "",
	};
}

describe("live turn", () => {
	it("draws the agent's work in the conversation, with no scroll box of its own", () => {
		const reply: ConversationItem = { kind: "message", id: "reply", turnId: "t", sequence: 4, revision: 0, role: "assistant", origin: "provider", text: "Here is what I found so far", streaming: true, createdAt: at };
		const { container } = render(<ChatWorkspace snapshot={snapshot("running", [user, note, command, reply])} />);

		expect(screen.getByText("Checking the folder layout first.")).toBeInTheDocument();
		expect(screen.getByText("Here is what I found so far")).toBeInTheDocument();
		const log = screen.getByRole("log", { name: "Conversation" });
		const nested = [...log.querySelectorAll<HTMLElement>("*")].filter((node) => /overflow-y-(auto|scroll)/.test(node.className));
		expect(nested).toEqual([]);
		expect(container.querySelector("[data-live-activity]")).toBeNull();
	});

	it("keeps an image the agent showed open while its next step arrives", () => {
		const shown: ConversationItem = { kind: "message", id: "frames", turnId: "t", sequence: 4, revision: 0, role: "assistant", origin: "provider", text: "First frame:\n\n![Frame one](https://example.com/one.png)", streaming: false, createdAt: at };
		const next: ConversationItem = { kind: "activity", id: "render", turnId: "t", sequence: 5, revision: 0, activityKind: "command", status: "running", summary: "node render.js", detail: { command: "node render.js" }, createdAt: at };
		const { rerender } = render(<ChatWorkspace snapshot={snapshot("running", [user, note, command, shown])} />);

		fireEvent.click(screen.getByRole("button", { name: "Open image: Frame one" }));
		expect(screen.getByRole("dialog")).toBeInTheDocument();

		rerender(<TooltipProvider><ChatWorkspace snapshot={snapshot("running", [user, note, command, shown, next])} /></TooltipProvider>);
		expect(screen.getByRole("dialog")).toBeInTheDocument();
	});

	it("folds the work under Worked for once the turn has finished", () => {
		const reply: ConversationItem = { kind: "message", id: "reply", turnId: "t", sequence: 4, revision: 0, role: "assistant", origin: "provider", text: "Here is what I found so far", streaming: false, createdAt: at };
		render(<ChatWorkspace snapshot={snapshot("completed", [user, note, command, reply])} />);

		expect(screen.getByRole("button", { name: /Worked for/ })).toBeInTheDocument();
		expect(screen.queryByText("Checking the folder layout first.")).toBeNull();
		expect(screen.getByText("Here is what I found so far")).toBeInTheDocument();
	});
});
