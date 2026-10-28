import { act, fireEvent, render as rtlRender, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { LiveActivityWindow } from "./LiveActivityWindow";
import { chatFixture } from "../../lib/chat-fixture";
import type { ConversationItem, ConversationSnapshot, TurnState } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";

// The window is about where things sit, not how they scroll (jsdom has no layout):
// a live turn's work goes inside it, the reply being written stays outside at full
// size, and a finished turn hands the work to "Worked for" with no window left.

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

function snapshot(state: TurnState): ConversationSnapshot {
	const at = "2026-10-17T10:00:00Z";
	const items: ConversationItem[] = [
		{ kind: "message", id: "u", turnId: "t", sequence: 1, revision: 0, role: "user", origin: "human", text: "Walk the repo", content: [], streaming: false, delivery: "accepted", createdAt: at },
		{ kind: "message", id: "note", turnId: "t", sequence: 2, revision: 0, role: "assistant", origin: "provider", text: "Checking the folder layout first.", streaming: false, createdAt: at },
		{ kind: "activity", id: "cmd", turnId: "t", sequence: 3, revision: 0, activityKind: "command", status: "completed", summary: "ls -la", detail: { command: "ls -la", exitCode: 0 }, createdAt: at },
		{ kind: "message", id: "reply", turnId: "t", sequence: 4, revision: 0, role: "assistant", origin: "provider", text: "Here is what I found so far", streaming: state === "running", createdAt: at },
	];
	return {
		...chatFixture,
		controller: { state: state === "running" ? "busy" : "ready" },
		turns: [{ id: "t", state, providerTurnId: "p", requestedAt: at, startedAt: at, completedAt: state === "running" ? undefined : at }],
		items,
		latestSequence: 4,
		oldestSequence: 1,
		continueTurnId: "",
	};
}

describe("live activity window", () => {
	it("holds a live turn's work and leaves the reply being written outside it", () => {
		const { container } = render(<ChatWorkspace snapshot={snapshot("running")} />);
		const window = container.querySelector<HTMLElement>("[data-live-activity]");

		expect(window).not.toBeNull();
		expect(window).toHaveTextContent("Checking the folder layout first.");
		expect(window).not.toHaveTextContent("Here is what I found so far");
		expect(screen.getByText("Here is what I found so far")).toBeInTheDocument();
	});

	it("leaves no window once the turn has finished", () => {
		const { container } = render(<ChatWorkspace snapshot={snapshot("completed")} />);

		expect(container.querySelector("[data-live-activity]")).toBeNull();
		expect(screen.getByRole("button", { name: /Worked for/ })).toBeInTheDocument();
		expect(screen.getByText("Here is what I found so far")).toBeInTheDocument();
	});
});

// A keyboard user takes over the window the same way a pointer user does: once they
// are working inside it, new steps must not pull it to the bottom under them.
describe("live activity window follow", () => {
	const originalObserver = window.ResizeObserver;
	afterEach(() => {
		window.ResizeObserver = originalObserver;
	});

	/** The window with a step to focus, its geometry stated, and a way to report growth. */
	function mountWindow(scrollTop = 500) {
		const callbacks: ResizeObserverCallback[] = [];
		window.ResizeObserver = class {
			constructor(callback: ResizeObserverCallback) { callbacks.push(callback); }
			observe() {}
			unobserve() {}
			disconnect() {}
		} as unknown as typeof ResizeObserver;
		const { container } = render(
			<LiveActivityWindow>
				<button type="button">Ran npm test</button>
			</LiveActivityWindow>,
		);
		const viewport = container.querySelector<HTMLElement>("[data-live-activity]")!;
		let height = 1000;
		Object.defineProperty(viewport, "scrollHeight", { configurable: true, get: () => height });
		Object.defineProperty(viewport, "clientHeight", { configurable: true, value: 240 });
		viewport.scrollTop = scrollTop;
		const grow = (to: number) => {
			height = to;
			act(() => callbacks.forEach((callback) => callback([], {} as ResizeObserver)));
		};
		return { viewport, step: screen.getByRole("button", { name: "Ran npm test" }), grow };
	}

	it("follows new steps until the reader works inside it", () => {
		const { viewport, grow } = mountWindow();
		grow(1200);
		expect(viewport.scrollTop).toBe(1200);
	});

	it("stops following once a step inside takes keyboard focus", () => {
		const { viewport, step, grow } = mountWindow();
		act(() => step.focus());
		grow(1200);
		expect(viewport.scrollTop).toBe(500);
	});

	it("follows again once the pointer leaves a window the reader clicked in while at the bottom", () => {
		const { viewport, step, grow } = mountWindow(760);
		fireEvent.pointerDown(step);
		fireEvent.mouseLeave(viewport);
		grow(1200);
		expect(viewport.scrollTop).toBe(1200);
	});

	it("stays put after the pointer leaves a window the reader scrolled up in", () => {
		const { viewport, step, grow } = mountWindow(500);
		fireEvent.pointerDown(step);
		fireEvent.mouseLeave(viewport);
		grow(1200);
		expect(viewport.scrollTop).toBe(500);
	});

	it("stops following once a key is pressed on a step inside", () => {
		const { viewport, step, grow } = mountWindow();
		fireEvent.keyDown(step, { key: "Enter" });
		grow(1200);
		expect(viewport.scrollTop).toBe(500);
	});
});
