/**
 * The per-turn undo control and the honesty of its confirmation.
 *
 * ChatWorkspace takes props only, so these render it directly with a fixture and a
 * spy. What is asserted is deliberately about behaviour a user would notice: the
 * control appears only when an undo can actually work, the confirmation says what is
 * lost, and the turn id that reaches the daemon is the one that was clicked.
 */

import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixture } from "../../lib/chat-fixture";
import type { ConversationSnapshot } from "../../types/conversation";
import type { components } from "../../../api/schema";
import { renderChat as render } from "../../test/render-chat";

type RollbackPreview = components["schemas"]["RollbackPreviewResponse"];

/** A conversation with nothing in flight, which is when an undo is offered. */
function idleSnapshot(): ConversationSnapshot {
	return {
		...chatFixture,
		controller: { state: "ready" },
		turns: chatFixture.turns.map((turn) =>
			turn.state === "running"
				? { ...turn, state: "completed" as const, completedAt: turn.requestedAt }
				: turn,
		),
	};
}

describe("ChatWorkspace rollback", () => {
	it("offers an undo on each settled turn and reports the clicked turn", async () => {
		const onRollback = vi.fn();
		render(<ChatWorkspace snapshot={idleSnapshot()} onRollback={onRollback} />);

		const controls = screen.getAllByRole("button", { name: "Roll back to here" });
		expect(controls.length).toBeGreaterThan(0);

		await userEvent.click(controls[0]!);
		const dialog = screen.getByRole("dialog");
		await userEvent.click(within(dialog).getByRole("button", { name: "Roll back" }));

		// The first settled turn in the fixture is turn-1; the daemon must be given
		// AO's own turn id, which is what the snapshot exposes. Without a preview the
		// dialog promised only the conversation, so files are not restored.
		expect(onRollback).toHaveBeenCalledWith("turn-1", false);
	});

	it("lists the files it will restore before offering the button, then restores them", async () => {
		const onRollback = vi.fn();
		let answer: (preview: RollbackPreview) => void = () => {};
		const onPreviewRollback = vi.fn(() => new Promise<RollbackPreview>((resolve) => { answer = resolve; }));
		render(<ChatWorkspace snapshot={idleSnapshot()} onRollback={onRollback} onPreviewRollback={onPreviewRollback} />);

		await userEvent.click(screen.getAllByRole("button", { name: "Roll back to here" })[0]!);
		const dialog = screen.getByRole("dialog");
		expect(onPreviewRollback).toHaveBeenCalledWith("turn-1");
		expect(dialog.textContent).toContain("Checking which files this would restore");
		expect(within(dialog).getByRole("button", { name: "Roll back" })).toBeDisabled();

		answer({
			filesRestorable: true,
			files: [
				{ path: "src/auth/session.ts", change: "reverted" },
				{ path: "src/auth/refresh.ts", change: "removed" },
				{ path: "docs/auth.md", change: "restored" },
			],
			totalFiles: 4,
			commitsSince: 1,
		});
		const list = await within(dialog).findByRole("list", { name: "Files that will change" });
		expect(within(list).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
			"src/auth/session.tsReverted",
			"src/auth/refresh.tsDeleted",
			"docs/auth.mdRestored",
		]);
		expect(dialog.textContent).toContain("the worktree goes back to how it was before it");
		expect(dialog.textContent).toContain("4 files will change");
		expect(dialog.textContent).toContain("and 1 more");
		expect(dialog.textContent).toContain("1 commit made since then stays on the branch");
		expect(dialog.textContent).not.toContain("left exactly as they are");

		await userEvent.click(within(dialog).getByRole("button", { name: "Roll back" }));
		expect(onRollback).toHaveBeenCalledWith("turn-1", true);
	});

	it("says why files stay as they are when they cannot be restored", async () => {
		const onRollback = vi.fn();
		const onPreviewRollback = vi.fn(async (): Promise<RollbackPreview> => ({
			filesRestorable: false,
			reason: "the workspace is not a git repository",
			files: [],
			totalFiles: 0,
			commitsSince: 0,
		}));
		render(<ChatWorkspace snapshot={idleSnapshot()} onRollback={onRollback} onPreviewRollback={onPreviewRollback} />);

		await userEvent.click(screen.getAllByRole("button", { name: "Roll back to here" })[0]!);
		const dialog = screen.getByRole("dialog");
		await within(dialog).findByText(/Files can't be restored: the workspace is not a git repository\./);
		expect(dialog.textContent).toContain("left exactly as they are");
		expect(within(dialog).queryByRole("list")).toBeNull();

		await userEvent.click(within(dialog).getByRole("button", { name: "Roll back" }));
		expect(onRollback).toHaveBeenCalledWith("turn-1", false);
	});

	// An older daemon has no preview route; the dialog must then promise only what
	// that daemon does.
	it("falls back to a conversation-only rollback when the preview fails", async () => {
		const onRollback = vi.fn();
		const onPreviewRollback = vi.fn(() => Promise.reject(new Error("Method Not Allowed")));
		render(<ChatWorkspace snapshot={idleSnapshot()} onRollback={onRollback} onPreviewRollback={onPreviewRollback} />);

		await userEvent.click(screen.getAllByRole("button", { name: "Roll back to here" })[0]!);
		const dialog = screen.getByRole("dialog");
		await within(dialog).findByText(/left exactly as they are/);

		await userEvent.click(within(dialog).getByRole("button", { name: "Roll back" }));
		expect(onRollback).toHaveBeenCalledWith("turn-1", false);
	});

	it("says the agent forgets, and that the worktree does not change", async () => {
		render(<ChatWorkspace snapshot={idleSnapshot()} onRollback={vi.fn()} />);
		await userEvent.click(screen.getAllByRole("button", { name: "Roll back to here" })[0]!);

		const dialog = screen.getByRole("dialog");
		expect(dialog.textContent).toContain("forget this exchange and everything after it");
		// The provider does not revert files, so a confirmation that implied otherwise
		// would be the one thing a user could not recover from believing.
		expect(dialog.textContent).toContain("left exactly as they are");
		expect(dialog.textContent).toContain("cannot be undone");
	});

	it("does not commit anything when the confirmation is cancelled", async () => {
		const onRollback = vi.fn();
		render(<ChatWorkspace snapshot={idleSnapshot()} onRollback={onRollback} />);

		await userEvent.click(screen.getAllByRole("button", { name: "Roll back to here" })[0]!);
		const dialog = screen.getByRole("dialog");
		await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

		expect(onRollback).not.toHaveBeenCalled();
	});

	// Keep the action slot stable while a turn is in flight; it becomes available
	// when the response settles without shifting the message footer.
	it("keeps the rollback control mounted but disabled while a turn is in flight", () => {
		render(<ChatWorkspace snapshot={chatFixture} onRollback={vi.fn()} />);
		expect(screen.getAllByRole("button", { name: "Roll back to here" })[0]).toBeDisabled();
	});

	// Feature detection reaches the UI as an absent callback, following how the model
	// picker already hides itself for a provider that offers no choice.
	it("draws no control when the agent cannot undo", () => {
		render(<ChatWorkspace snapshot={idleSnapshot()} />);
		expect(screen.queryByRole("button", { name: "Roll back to here" })).toBeNull();
	});

	// A turn the provider never accepted holds no history to discard, and the daemon
	// refuses it.
	it("draws no control on a turn the provider never accepted", () => {
		const snapshot = idleSnapshot();
		render(
			<ChatWorkspace
				snapshot={{
					...snapshot,
					turns: snapshot.turns.map((turn) => ({ ...turn, providerTurnId: undefined })),
				}}
				onRollback={vi.fn()}
			/>,
		);
		expect(screen.queryByRole("button", { name: "Roll back to here" })).toBeNull();
	});

	it("says how much an undo took away", () => {
		const snapshot = idleSnapshot();
		render(
			<ChatWorkspace
				snapshot={{
					...snapshot,
					turns: [
						snapshot.turns[0]!,
						{ ...snapshot.turns[1]!, rolledBack: true },
					],
				}}
				onRollback={vi.fn()}
			/>,
		);
		expect(screen.getByText(/1 turn was rolled back/)).toBeInTheDocument();
		expect(screen.getByText(/no longer remembers it/)).toBeInTheDocument();
	});

	it("surfaces a refusal inside the confirmation rather than closing on it", async () => {
		render(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				onRollback={vi.fn()}
				rollbackError="stop the agent before rolling back: it is in the middle of a turn"
			/>,
		);
		await userEvent.click(screen.getAllByRole("button", { name: "Roll back to here" })[0]!);
		expect(screen.getByRole("alert").textContent).toContain("stop the agent");
	});

	it("shows the thread title in the primary agent tab when there is one", () => {
		render(<ChatWorkspace snapshot={{ ...chatFixture, title: "Fix OAuth Return URL Loss" }} />);
		expect(screen.getByRole("tab", { name: "Fix OAuth Return URL Loss · Codex" })).toBeInTheDocument();
		expect(screen.queryByText("Codex")).toBeNull();
		expect(screen.queryByText(chatFixture.sessionId)).toBeNull();
	});

	it("falls back to the session id when the thread has no name", () => {
		render(<ChatWorkspace snapshot={chatFixture} />);
		expect(screen.getByRole("tab", { name: `${chatFixture.sessionId} · Codex` })).toBeInTheDocument();
		expect(screen.queryByText("Codex")).toBeNull();
	});
});
