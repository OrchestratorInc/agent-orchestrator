import { act, cleanup, fireEvent, render as rtlRender, screen, within } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
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
	it("draws the agent's work in the conversation while the turn runs", () => {
		const reply: ConversationItem = { kind: "message", id: "reply", turnId: "t", sequence: 4, revision: 0, role: "assistant", origin: "provider", text: "Here is what I found so far", streaming: true, createdAt: at };
		render(<ChatWorkspace snapshot={snapshot("running", [user, note, command, reply])} />);

		const log = screen.getByRole("log", { name: "Conversation" });
		expect(within(log).getByText("Checking the folder layout first.")).toBeInTheDocument();
		expect(within(log).getByText("Here is what I found so far")).toBeInTheDocument();
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

// A reader who scrolled up into a running turn keeps their place when it settles
// and its steps fold under "Worked for" all at once.
describe("settling under a reader who scrolled up", () => {
	const secondNote: ConversationItem = { kind: "message", id: "note-2", turnId: "t", sequence: 4, revision: 0, role: "assistant", origin: "provider", text: "Now the tests.", streaming: false, createdAt: at };
	const testRun: ConversationItem = { kind: "activity", id: "cmd-2", turnId: "t", sequence: 5, revision: 0, activityKind: "command", status: "completed", summary: "npm test", detail: { command: "npm test", exitCode: 0 }, createdAt: at };
	const reply: ConversationItem = { kind: "message", id: "reply", turnId: "t", sequence: 6, revision: 0, role: "assistant", origin: "provider", text: "All green.", streaming: false, createdAt: at };
	const items = [user, note, command, secondNote, testRun, reply];
	let restoreLayout: (() => void) | undefined;
	afterEach(async () => {
		cleanup();
		// TanStack's 150ms scroll-end debounce survives unmount. Drain it before
		// jsdom removes window, as in WorkspaceDiffView's virtualizer tests.
		await act(async () => {
			await new Promise((resolve) => setTimeout(resolve, 200));
		});
		restoreLayout?.();
		restoreLayout = undefined;
	});

	/** jsdom has no layout: stack the turn's rows 100px apart ("Worked for" is 28px) under a scrollable log. */
	function stubTurnLayout(log: HTMLElement, scrollTop: number) {
		const bounds = HTMLElement.prototype.getBoundingClientRect;
		const height = (row: Element) => (row.hasAttribute("data-turn-worked") ? 28 : 100);
		const spy = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
			if (this === log) return { ...bounds.call(this), top: 0, bottom: 800, height: 800 } as DOMRect;
			const turn = this.parentElement;
			if (!turn?.parentElement?.hasAttribute("data-chat-turn")) return bounds.call(this);
			let top = 20 - log.scrollTop;
			for (const row of turn.children) {
				if (row === this) break;
				top += height(row);
			}
			return { ...bounds.call(this), top, bottom: top + height(this), height: height(this) } as DOMRect;
		});
		Object.defineProperty(log, "scrollHeight", { configurable: true, value: 3000 });
		Object.defineProperty(log, "clientHeight", { configurable: true, value: 800 });
		Object.defineProperty(log, "scrollTop", { configurable: true, writable: true, value: scrollTop });
		restoreLayout = () => spy.mockRestore();
		// Reading back up the turn releases the follow.
		fireEvent.wheel(log, { deltaY: -120 });
		fireEvent.scroll(log);
	}

	function settle(rerender: (ui: ReactElement) => void) {
		rerender(<TooltipProvider><ChatWorkspace snapshot={snapshot("completed", items)} /></TooltipProvider>);
	}

	it("lands Worked for at the top of the view when the reader was among the steps", () => {
		const { rerender } = render(<ChatWorkspace snapshot={snapshot("running", items)} />);
		const log = screen.getByRole("log", { name: "Conversation" });
		// Rows: prompt 20, note 120, ls 220, second note 320, npm test 420, reply 520.
		// At 350 the second note sits across the top edge.
		stubTurnLayout(log, 350);

		settle(rerender);

		// Folded: prompt 20, "Worked for" 120, reply 148.
		expect(screen.getByRole("button", { name: /Worked for/ })).toBeInTheDocument();
		expect(log.scrollTop).toBe(120);
	});

	it("holds the reply still when the reader was already on it", () => {
		const { rerender } = render(<ChatWorkspace snapshot={snapshot("running", items)} />);
		const log = screen.getByRole("log", { name: "Conversation" });
		// The reply starts at 520, 40px above the top edge.
		stubTurnLayout(log, 560);

		settle(rerender);

		// Folded, the reply starts at 148: still 40px above the top edge.
		expect(log.scrollTop).toBe(188);
	});
});
