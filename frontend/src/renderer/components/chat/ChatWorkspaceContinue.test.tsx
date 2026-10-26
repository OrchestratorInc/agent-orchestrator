/**
 * Continuing a stopped turn, like play after pause.
 *
 * The composer's one button stops a running turn; once the user stopped it and
 * the composer is empty, the same button continues it, sending the continue
 * prompt as the user's own message. These tests cover when the workspace offers
 * that: only for the latest turn that still counts, only once it was stopped.
 */

import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CONTINUE_STOPPED_TURN_PROMPT, ChatWorkspace } from "./ChatWorkspace";
import { chatFixture } from "../../lib/chat-fixture";
import type { ConversationSnapshot, ConversationTurn } from "../../types/conversation";
import { renderChat as render } from "../../test/render-chat";

/** turn-1 completed and turn-2 stopped by the user, with nothing in flight. */
function stoppedSnapshot(change: (turns: ConversationTurn[]) => ConversationTurn[] = (turns) => turns): ConversationSnapshot {
	const turns = chatFixture.turns.map((turn) =>
		turn.id === "turn-2"
			? { ...turn, state: "interrupted" as const, completedAt: turn.requestedAt }
			: turn.state === "running"
				? { ...turn, state: "completed" as const, completedAt: turn.requestedAt }
				: turn,
	);
	return { ...chatFixture, controller: { state: "ready" }, turns: change(turns), continueTurnId: "turn-2" };
}

const continueButton = () => screen.queryByRole("button", { name: "Continue turn" });

describe("ChatWorkspace continue", () => {
	it("turns the composer's button into Continue after a stop and continues the turn", async () => {
		const onContinueTurn = vi.fn();
		const onSend = vi.fn();
		render(<ChatWorkspace snapshot={stoppedSnapshot()} onSend={onSend} onContinueTurn={onContinueTurn} />);

		expect(screen.getByText("The agent was interrupted by you")).toBeInTheDocument();
		await userEvent.click(continueButton()!);

		expect(onContinueTurn).toHaveBeenCalledTimes(1);
		expect(onSend).not.toHaveBeenCalled();
	});

	it("uses daemon eligibility instead of a swept queued turn", () => {
		const snapshot = stoppedSnapshot();
		snapshot.continueTurnId = "";
		snapshot.turns.push({ id: "swept", state: "interrupted", requestedAt: snapshot.turns[0]!.requestedAt });
		render(<ChatWorkspace snapshot={snapshot} onContinueTurn={vi.fn()} />);
		expect(continueButton()).toBeNull();
	});

	it("keeps daemon-selected Continue after a provider switch clears provider ids", () => {
		const snapshot = stoppedSnapshot();
		snapshot.continueTurnId = "turn-2";
		snapshot.turns = snapshot.turns.map((turn) => ({ ...turn, providerTurnId: undefined }));
		render(<ChatWorkspace snapshot={snapshot} onContinueTurn={vi.fn()} />);
		expect(continueButton()).toBeInTheDocument();
	});

	it("shows a continue as a Continued marker, not as a message the user wrote", () => {
		const snapshot = stoppedSnapshot();
		const stopped = snapshot.turns.find((turn) => turn.id === "turn-2")!;
		snapshot.turns = [...snapshot.turns, { id: "turn-continue", state: "completed", requestedAt: stopped.requestedAt, completedAt: stopped.requestedAt }];
		snapshot.items = [
			...snapshot.items,
			{
				kind: "message",
				id: "message-continue",
				turnId: "turn-continue",
				sequence: snapshot.latestSequence + 1,
				revision: 0,
				role: "user",
				origin: "human",
				text: CONTINUE_STOPPED_TURN_PROMPT,
				continuation: true,
				streaming: false,
				createdAt: stopped.requestedAt,
			},
		];
		snapshot.latestSequence += 1;
		snapshot.continueTurnId = "";
		render(<ChatWorkspace snapshot={snapshot} onContinueTurn={vi.fn()} />);

		expect(screen.getByText("Continued")).toBeInTheDocument();
		expect(screen.queryByText(CONTINUE_STOPPED_TURN_PROMPT)).toBeNull();
		// The continued turn is now the latest, so there is nothing left to continue.
		expect(continueButton()).toBeNull();
	});

	it("hides Continue when an older daemon omits eligibility", () => {
		const snapshot = stoppedSnapshot();
		delete snapshot.continueTurnId;
		render(<ChatWorkspace snapshot={snapshot} onContinueTurn={vi.fn()} />);
		expect(continueButton()).toBeNull();
	});

	it("does not offer Continue while the session cannot take new work, or offers no continue", () => {
		const { unmount } = render(<ChatWorkspace snapshot={stoppedSnapshot()} onContinueTurn={vi.fn()} newWorkDisabled />);
		expect(continueButton()).toBeNull();
		unmount();

		render(<ChatWorkspace snapshot={stoppedSnapshot()} />);
		expect(continueButton()).toBeNull();
	});
});
