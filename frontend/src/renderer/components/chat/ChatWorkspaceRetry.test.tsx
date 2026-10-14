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
import type { ConversationSnapshot } from "../../types/conversation";
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

	it("resumes the original prompt when the reset has passed", async () => {
		const snapshot = limitedSnapshot();
		snapshot.turns[0]!.usageLimit!.resetsAt = new Date(Date.now() - 1_000).toISOString();
		const retry = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} retryControl={{ retry }} />);
		await userEvent.click(screen.getByRole("button", { name: "Resume" }));
		expect(retry).toHaveBeenCalledWith("turn-1");
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
