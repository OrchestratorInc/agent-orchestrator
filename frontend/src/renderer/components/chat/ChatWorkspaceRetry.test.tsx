/**
 * The per-turn retry control for a failed turn.
 *
 * Retry is the inverse of rollback: it re-dispatches a failed turn's durable
 * prompt as a NEW turn instead of discarding the exchange. What is asserted is
 * behaviour a user would notice: the control appears only on an eligible failed
 * turn, and the turn id that reaches the daemon is the one that was clicked.
 */

import { render as rtlRender, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixture } from "../../lib/chat-fixture";
import type { ConversationItem, ConversationSnapshot } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

/** A conversation with one failed turn and nothing in flight. */
function failedSnapshot(): ConversationSnapshot {
	return {
		...chatFixture,
		controller: { state: "ready" },
		turns: chatFixture.turns.map((turn) =>
			turn.id === "turn-1"
				? {
						...turn,
						state: "failed" as const,
						completedAt: turn.requestedAt,
						errorMessage: "stream disconnected before completion",
					}
				: turn.state === "running"
					? { ...turn, state: "completed" as const, completedAt: turn.requestedAt }
					: turn,
		),
	};
}

function limitedSnapshot(autoResume = false): ConversationSnapshot {
	const snapshot = failedSnapshot();
	snapshot.turns = snapshot.turns.filter((turn) => turn.id === "turn-1").map((turn) => ({
		...turn,
		errorMessage: "You've hit your session limit",
		hasHumanPrompt: true,
		usageLimit: { resetsAt: new Date(Date.now() + 60_000).toISOString(), autoResume },
	}));
	snapshot.items = snapshot.items.filter((item) => item.turnId === "turn-1");
	return snapshot;
}

describe("ChatWorkspace retry", () => {
	it("offers opt-in auto-resume next to the composer for the latest limited prompt", async () => {
		const setAutoResume = vi.fn();
		render(<ChatWorkspace snapshot={limitedSnapshot()} retryControl={{ retry: vi.fn(), setAutoResume }} />);
		expect(screen.queryByText("The agent ran into a problem")).toBeNull();
		expect(screen.queryByRole("button", { name: "Retry this turn" })).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Auto-resume at reset" }));
		expect(setAutoResume).toHaveBeenCalledWith("turn-1", true);
	});

	it("cancels durable auto-resume for that same source turn", async () => {
		const setAutoResume = vi.fn();
		render(<ChatWorkspace snapshot={limitedSnapshot(true)} retryControl={{ retry: vi.fn(), setAutoResume }} />);
		await userEvent.click(screen.getByRole("button", { name: "Cancel auto-resume" }));
		expect(setAutoResume).toHaveBeenCalledWith("turn-1", false);
	});

	it.each([
		{ autoResume: false, action: "Resume" },
		{ autoResume: true, action: "Cancel auto-resume" },
	])("keeps $action available when pagination omits the prompt", async ({ autoResume, action }) => {
		const snapshot = limitedSnapshot(autoResume);
		snapshot.turns[0]!.usageLimit!.resetsAt = new Date(Date.now() - 1_000).toISOString();
		snapshot.items = snapshot.items.filter((item) => item.kind !== "message" || item.role !== "user");
		snapshot.hasMoreBefore = true;
		snapshot.oldestSequence = 2;
		const retry = vi.fn();
		const setAutoResume = vi.fn();
		const loadOlder = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry, setAutoResume }} hasOlder onLoadOlder={loadOlder} />);

		expect(screen.getByRole("button", { name: action })).toBeEnabled();
		await userEvent.click(screen.getByRole("button", { name: action }));
		if (autoResume) expect(setAutoResume).toHaveBeenCalledWith("turn-1", false);
		else expect(retry).toHaveBeenCalledWith("turn-1");
		expect(loadOlder).not.toHaveBeenCalled();
	});

	it("does not offer usage-limit recovery without a durable human prompt", () => {
		const snapshot = limitedSnapshot();
		snapshot.turns[0]!.hasHumanPrompt = false;
		snapshot.turns[0]!.usageLimit!.resetsAt = new Date(Date.now() - 1_000).toISOString();
		snapshot.items = snapshot.items.map((item): ConversationItem => item.kind === "message" && item.role === "user"
			? { ...item, origin: "automation" } : item);
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn(), setAutoResume: vi.fn() }} />);

		expect(screen.queryByRole("button", { name: "Resume" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Auto-resume at reset" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Cancel auto-resume" })).toBeNull();
	});

	it("keeps an earlier limited turn resumable when pagination omits its prompt", async () => {
		const snapshot = limitedSnapshot();
		snapshot.turns[0]!.usageLimit!.resetsAt = new Date(Date.now() - 1_000).toISOString();
		snapshot.items = snapshot.items.filter((item) => item.kind !== "message" || item.role !== "user");
		snapshot.hasMoreBefore = true;
		snapshot.oldestSequence = 2;
		snapshot.turns.push({ id: "later", state: "completed", requestedAt: new Date().toISOString(), completedAt: new Date().toISOString() });
		const retry = vi.fn();
		const loadOlder = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry }} hasOlder onLoadOlder={loadOlder} />);

		await userEvent.click(screen.getByRole("button", { name: "Resume" }));
		expect(retry).toHaveBeenCalledWith("turn-1");
		expect(loadOlder).not.toHaveBeenCalled();
	});

	it.each([
		{ agentResuming: true },
		{ controllerTransitioning: true },
		{ busy: true },
		{ agentInputDisabled: true },
		{ newWorkDisabled: true },
	])("keeps overdue cancellation available while new work is unavailable: %j", async (props) => {
		const snapshot = limitedSnapshot(true);
		snapshot.turns[0]!.usageLimit!.resetsAt = new Date(Date.now() - 1_000).toISOString();
		const setAutoResume = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn(), setAutoResume }} {...props} />);

		expect(screen.getByRole("button", { name: "Cancel auto-resume" })).toBeEnabled();
		expect(screen.getByText("Auto-resume is waiting to retry.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Resume" })).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Cancel auto-resume" }));
		expect(setAutoResume).toHaveBeenCalledWith("turn-1", false);
	});

	it("resumes the original prompt when the reset has passed", async () => {
		const snapshot = limitedSnapshot();
		snapshot.turns[0]!.usageLimit!.resetsAt = new Date(Date.now() - 1_000).toISOString();
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry, error: "The previous resume request failed", turnId: "turn-1" }} />);
		expect(screen.getByRole("alert")).toHaveTextContent("The previous resume request failed");
		await userEvent.click(screen.getByRole("button", { name: "Resume" }));
		expect(retry).toHaveBeenCalledWith("turn-1");
	});

	it.each([
		{ resetInMs: -1_000, ready: true },
		{ resetInMs: 60_000, ready: false },
		{ resetInMs: undefined, ready: true },
	])("preserves older limited-prompt recovery and refusal after later work: %j", async ({ resetInMs, ready }) => {
		const snapshot = limitedSnapshot();
		snapshot.turns[0]!.usageLimit!.resetsAt = resetInMs === undefined ? undefined : new Date(Date.now() + resetInMs).toISOString();
		snapshot.turns.push({ id: "later", state: "completed", requestedAt: new Date().toISOString(), completedAt: new Date().toISOString() });
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry, error: "Could not retry the earlier prompt", turnId: "turn-1" }} />);

		expect(screen.getByRole("alert")).toHaveTextContent("Could not retry the earlier prompt");
		expect(screen.queryByRole("button", { name: "Auto-resume at reset" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Cancel auto-resume" })).toBeNull();
		if (ready) {
			await userEvent.click(screen.getByRole("button", { name: "Resume" }));
			expect(retry).toHaveBeenCalledWith("turn-1");
		} else {
			expect(screen.queryByRole("button", { name: "Resume" })).toBeNull();
			expect(screen.queryByRole("button", { name: "Retry this turn" })).toBeNull();
		}
	});

	it("keeps limit history but hides resume actions after a later turn continues", () => {
		const snapshot = limitedSnapshot();
		snapshot.turns.push({ id: "continued", state: "running", requestedAt: new Date().toISOString() });
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn(), setAutoResume: vi.fn() }} />);
		expect(screen.queryByRole("button", { name: "Auto-resume at reset" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Resume" })).toBeNull();
	});

	it("offers a retry on the failed turn and reports the clicked turn", async () => {
		const onRetryTurn = vi.fn();
		render(<ChatWorkspace snapshot={failedSnapshot()} retryControl={{ retry: onRetryTurn }} />);

		const retry = screen.getByRole("button", { name: "Retry this turn" });
		expect(retry).toBeDefined();

		await userEvent.click(retry);

		// The failed turn in the fixture is turn-1; the daemon must be given AO's
		// own turn id, which is what the snapshot exposes.
		expect(onRetryTurn).toHaveBeenCalledWith("turn-1");
	});

	it("draws no retry control for a turn that succeeded", () => {
		// The idle fixture has only completed turns, so retry must be absent.
		const completed = {
			...chatFixture,
			controller: { state: "ready" as const },
			turns: chatFixture.turns.map((turn) =>
				turn.state === "running"
					? { ...turn, state: "completed" as const, completedAt: turn.requestedAt }
					: turn,
			),
		};
		render(<ChatWorkspace snapshot={completed} retryControl={{ retry: vi.fn() }} />);
		expect(screen.queryByRole("button", { name: "Retry this turn" })).toBeNull();
	});

	it("draws no retry control when the daemon offers none", () => {
		render(<ChatWorkspace snapshot={failedSnapshot()} />);
		expect(screen.queryByRole("button", { name: "Retry this turn" })).toBeNull();
	});

	it("disables retry while another turn is running", () => {
		const snapshot = failedSnapshot();
		snapshot.controller = { state: "busy" };
		snapshot.turns = snapshot.turns.map((turn) =>
			turn.id === "turn-2"
				? { ...turn, state: "running" as const, completedAt: undefined }
				: turn,
		);

		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);

		expect(screen.getByRole("button", { name: "Retry this turn" })).toBeDisabled();
	});

	it("shows a retry refusal next to the affected turn", () => {
		render(
			<ChatWorkspace
				snapshot={failedSnapshot()}
				retryControl={{
					retry: vi.fn(),
					error: "Stop the current turn before retrying this one",
					turnId: "turn-1",
				}}
			/>,
		);

		expect(screen.getByRole("alert")).toHaveTextContent(
			"Stop the current turn before retrying this one",
		);
	});

	it("shows and disables the pending retry on the affected turn", () => {
		render(
			<ChatWorkspace
				snapshot={failedSnapshot()}
				retryControl={{ retry: vi.fn(), pending: true, turnId: "turn-1" }}
			/>,
		);

		const retry = screen.getByRole("button", { name: "Retry this turn" });
		expect(retry).toBeDisabled();
		expect(retry).toHaveTextContent("Retrying…");
	});

	it("hides retry when delivery was never provider-confirmed", () => {
		const snapshot = failedSnapshot();
		snapshot.turns = snapshot.turns.map((turn) =>
			turn.id === "turn-1" ? { ...turn, providerTurnId: undefined } : turn,
		);

		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);

		expect(screen.queryByRole("button", { name: "Retry this turn" })).toBeNull();
	});

	it("hides a source retry after its attempt is rolled back out of the timeline", () => {
		const snapshot = failedSnapshot();
		snapshot.turns = [
			...snapshot.turns,
			{
				id: "turn-retry",
				state: "completed",
				providerTurnId: "provider-retry",
				retryOfTurnId: "turn-1",
				rolledBack: true,
				requestedAt: snapshot.turns[0]!.requestedAt,
				completedAt: snapshot.turns[0]!.requestedAt,
			},
		];

		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);

		expect(screen.queryByRole("button", { name: "Retry this turn" })).toBeNull();
	});

	it("hides a source retry when its attempt is outside the active branch", () => {
		const snapshot = failedSnapshot();
		snapshot.turns = snapshot.turns.map((turn) =>
			turn.id === "turn-1" ? { ...turn, hasRetryAttempt: true } : turn,
		);

		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry: vi.fn() }} />);

		expect(screen.queryByRole("button", { name: "Retry this turn" })).toBeNull();
	});
});
