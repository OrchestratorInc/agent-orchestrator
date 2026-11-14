import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { QueuedMessageDock, type QueuedMessage } from "./QueuedMessageDock";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixture } from "../../lib/chat-fixture";
import { renderChat } from "../../test/render-chat";

const { dragEnds, dragStarts } = vi.hoisted(() => ({
	dragEnds: new Map<string, (event: { active: { id: string }; over: { id: string } | null }) => void>(),
	dragStarts: new Map<string, (event: {
		active: { id: string; rect: { current: { initial: { width: number } } } };
	}) => void>(),
}));

vi.mock("@dnd-kit/core", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@dnd-kit/core")>();
	return {
		...actual,
		DndContext: ({
			children,
			id,
			onDragEnd,
			onDragStart,
		}: {
			children: React.ReactNode;
			id?: string;
			onDragEnd?: (event: { active: { id: string }; over: { id: string } | null }) => void;
			onDragStart?: (event: {
				active: { id: string; rect: { current: { initial: { width: number } } } };
			}) => void;
		}) => {
			if (id && onDragEnd) dragEnds.set(id, onDragEnd);
			if (id && onDragStart) dragStarts.set(id, onDragStart);
			return children;
		},
		DragOverlay: ({ children }: { children: React.ReactNode }) => children,
	};
});

function queuedMessages(texts: string[]): QueuedMessage[] {
	return texts.map((text, index) => {
		const turnId = `queued-${index + 1}`;
		return {
			turnId,
			message: {
				kind: "message",
				id: `${turnId}-message`,
				turnId,
				sequence: 100 + index,
				revision: 0,
				role: "user",
				origin: "human",
				text,
				streaming: false,
				createdAt: `2026-08-11T10:0${index + 1}:00Z`,
			},
		};
	});
}

function rowTexts() {
	const dock = screen.getByTestId("queued-message-dock");
	return within(dock)
		.getAllByTestId(/^queued-message-queued-/, { exact: false })
		.map((row) => row.textContent?.replace(/\s+/g, " ").trim() ?? "");
}

function simulateDrag(activeId: string, overId: string) {
	const dragStart = dragStarts.get("queue-dock-reorder");
	const dragEnd = dragEnds.get("queue-dock-reorder");
	if (!dragStart || !dragEnd) {
		throw new Error("queue dock drag handlers were not registered");
	}
	act(() => {
		dragStart({
			active: {
				id: activeId,
				rect: { current: { initial: { width: 420 } } },
			},
		});
		dragEnd({ active: { id: activeId }, over: { id: overId } });
	});
}

describe("QueuedMessageDock reorder", () => {
	it("submits FIFO order when dragging the last row to the top", async () => {
		const onReorderQueuedTurns = vi.fn().mockResolvedValue(undefined);
		render(
			<QueuedMessageDock
				messages={queuedMessages(["first queued", "second queued", "third queued"])}
				onReorderQueuedTurns={onReorderQueuedTurns}
			/>,
		);

		simulateDrag("queued-3", "queued-1");

		await waitFor(() => {
			expect(onReorderQueuedTurns).toHaveBeenCalledWith(["queued-3", "queued-1", "queued-2"]);
		});
	});

	it("submits FIFO order when dragging the first row to the bottom", async () => {
		const onReorderQueuedTurns = vi.fn().mockResolvedValue(undefined);
		render(
			<QueuedMessageDock
				messages={queuedMessages(["first queued", "second queued", "third queued"])}
				onReorderQueuedTurns={onReorderQueuedTurns}
			/>,
		);

		simulateDrag("queued-1", "queued-3");

		await waitFor(() => {
			expect(onReorderQueuedTurns).toHaveBeenCalledWith(["queued-2", "queued-3", "queued-1"]);
		});
	});

	it("restores the visual order and surfaces an error when reorder fails", async () => {
		const onReorderQueuedTurns = vi.fn().mockRejectedValue(new Error("reorder failed"));
		render(
			<QueuedMessageDock
				messages={queuedMessages(["first queued", "second queued", "third queued"])}
				onReorderQueuedTurns={onReorderQueuedTurns}
			/>,
		);

		expect(rowTexts()).toEqual([
			expect.stringContaining("first queued"),
			expect.stringContaining("second queued"),
			expect.stringContaining("third queued"),
		]);

		simulateDrag("queued-3", "queued-1");

		await waitFor(() => {
			expect(screen.getByRole("status")).toHaveTextContent("reorder failed");
		});
		expect(rowTexts()).toEqual([
			expect.stringContaining("first queued"),
			expect.stringContaining("second queued"),
			expect.stringContaining("third queued"),
		]);
	});

	it("freezes hover-only steer visibility while dragging", async () => {
		const user = userEvent.setup();
		const onReorderQueuedTurns = vi.fn().mockResolvedValue(undefined);
		render(
			<QueuedMessageDock
				canSteer
				messages={queuedMessages(["first queued", "second queued"])}
				onReorderQueuedTurns={onReorderQueuedTurns}
			/>,
		);

		await user.hover(screen.getByTestId("queued-message-queued-2"));
		simulateDrag("queued-2", "queued-1");

		await waitFor(() => {
			expect(onReorderQueuedTurns).toHaveBeenCalledWith(["queued-2", "queued-1"]);
		});
	});
});

describe("QueuedMessageDock order and steering", () => {
	it("lists messages in send order with a pending send last", () => {
		render(
			<QueuedMessageDock
				messages={[
					...queuedMessages(["first queued", "second queued"]),
					{ ...queuedMessages(["just sent"])[0]!, turnId: "local:pending", pending: true },
				]}
			/>,
		);

		expect(rowTexts()).toEqual([
			expect.stringContaining("first queued"),
			expect.stringContaining("second queued"),
		]);
		const dock = screen.getByTestId("queued-message-dock");
		const texts = Array.from(dock.querySelectorAll(".queue-dock-row-text")).map((node) => node.textContent);
		expect(texts).toEqual(["first queued", "second queued", "just sent"]);
	});

	it("keeps every row's actions and grip in place while a new message is sent", () => {
		const props = {
			canSteer: true,
			onPromoteQueuedTurn: vi.fn().mockResolvedValue(undefined),
			onCancelQueuedTurn: vi.fn().mockResolvedValue(undefined),
			onReorderQueuedTurns: vi.fn().mockResolvedValue(undefined),
		};
		const confirmed = queuedMessages(["first queued", "second queued"]);
		const pending = { ...queuedMessages(["just sent"])[0]!, turnId: "local:pending", pending: true };
		const { rerender } = render(<QueuedMessageDock {...props} messages={confirmed} />);
		const grips = () => screen.getAllByRole("button", { name: "Drag to reorder queued message" });
		const deletes = () => screen.getAllByRole("button", { name: "Delete queued message" });
		const steers = () => screen.getAllByRole("button", { name: "Steer this queued message into the running turn" });
		expect(grips()).toHaveLength(2);
		for (const grip of grips()) expect(grip).toBeEnabled();

		rerender(<QueuedMessageDock {...props} messages={[...confirmed, pending]} sending />);

		// Every row, the unconfirmed one included, keeps the same controls.
		expect(grips()).toHaveLength(3);
		expect(deletes()).toHaveLength(3);
		expect(steers()).toHaveLength(3);
		const pendingRow = screen.getByTestId("queued-message-local:pending");
		for (const button of within(pendingRow).getAllByRole("button")) {
			expect(button).toBeDisabled();
		}

		rerender(<QueuedMessageDock {...props} messages={[...confirmed, pending]} />);
		expect(grips()[0]).toBeEnabled();
		for (const button of within(pendingRow).getAllByRole("button")) {
			expect(button).toBeDisabled();
		}
	});

	it("keeps Steer on every row while a turn can be steered", () => {
		render(
			<QueuedMessageDock
				canSteer
				messages={queuedMessages(["first queued", "second queued", "third queued"])}
				onPromoteQueuedTurn={vi.fn().mockResolvedValue(undefined)}
			/>,
		);

		for (const turnId of ["queued-1", "queued-2", "queued-3"]) {
			const steer = within(screen.getByTestId(`queued-message-${turnId}`)).getByRole("button", {
				name: "Steer this queued message into the running turn",
			});
			expect(steer).not.toHaveClass("opacity-0");
		}
	});

	it("keeps rows steady instead of dimming them while the composer sends", () => {
		const props = {
			canSteer: true,
			messages: queuedMessages(["first queued", "second queued", "third queued"]),
			onPromoteQueuedTurn: vi.fn().mockResolvedValue(undefined),
			onCancelQueuedTurn: vi.fn().mockResolvedValue(undefined),
			onBeginQueuedEdit: vi.fn(),
		};
		const { rerender } = render(<QueuedMessageDock {...props} sending />);
		const steerButtons = () =>
			screen.getAllByRole("button", { name: "Steer this queued message into the running turn" });

		expect(steerButtons()).toHaveLength(3);
		for (const button of [
			...steerButtons(),
			...screen.getAllByRole("button", { name: "Delete queued message" }),
		]) {
			expect(button).toBeDisabled();
			expect(button).toHaveClass("disabled:opacity-100");
		}

		rerender(<QueuedMessageDock {...props} />);
		for (const button of steerButtons()) expect(button).toBeEnabled();
		expect(screen.getAllByRole("button", { name: "Delete queued message" })[0]).toBeEnabled();
	});
});

describe("ChatWorkspace queue reorder", () => {
	function queuedPrompt(turnId: string, sequence: number, origin: "human" | "automation", text: string) {
		return {
			kind: "message" as const,
			id: `${turnId}-message`,
			turnId,
			sequence,
			revision: 0,
			role: "user" as const,
			origin,
			text,
			streaming: false,
			createdAt: `2026-08-11T10:0${sequence - 99}:00Z`,
		};
	}

	it("keeps a queued automation turn in place when the user reorders their own prompts", async () => {
		const onReorderQueuedTurns = vi.fn().mockResolvedValue(undefined);
		renderChat(
			<ChatWorkspace
				snapshot={{
					...chatFixture,
					turns: [
						...chatFixture.turns,
						{ id: "queued-first", state: "queued" as const, requestedAt: "2026-08-11T10:01:00Z" },
						{ id: "queued-automation", state: "queued" as const, requestedAt: "2026-08-11T10:02:00Z" },
						{ id: "queued-last", state: "queued" as const, requestedAt: "2026-08-11T10:03:00Z" },
					],
					items: [
						...chatFixture.items,
						queuedPrompt("queued-first", 100, "human", "first prompt"),
						queuedPrompt("queued-automation", 101, "automation", "automation follow-up"),
						queuedPrompt("queued-last", 102, "human", "last prompt"),
					],
				}}
				onSteer={vi.fn()}
				onReorderQueuedTurns={onReorderQueuedTurns}
			/>,
		);
		expect(screen.queryByTestId("queued-message-queued-automation")).not.toBeInTheDocument();

		simulateDrag("queued-last", "queued-first");

		await waitFor(() => {
			expect(onReorderQueuedTurns).toHaveBeenCalledWith(["queued-last", "queued-automation", "queued-first"]);
		});
	});
});

describe("QueuedMessageDock action errors", () => {
	it("shows the daemon's reason when steering a queued message is refused", async () => {
		// The typed client throws the API error envelope, not an Error instance.
		const onPromoteQueuedTurn = vi.fn().mockRejectedValue({
			error: {
				code: "CHAT_TURN_NOT_STEERABLE",
				message: "the running turn cannot take guidance: it collects between tool calls",
			},
		});
		render(
			<QueuedMessageDock
				messages={queuedMessages(["use staging instead"])}
				canSteer
				onPromoteQueuedTurn={onPromoteQueuedTurn}
			/>,
		);

		await userEvent.click(
			screen.getAllByRole("button", { name: "Steer this queued message into the running turn" })[0]!,
		);

		await waitFor(() => {
			expect(screen.getByTestId("queued-message-dock")).toHaveTextContent(
				"the running turn cannot take guidance: it collects between tool calls",
			);
		});
		expect(screen.getByTestId("queued-message-dock")).not.toHaveTextContent("That action failed.");
		expect(screen.getByTestId("queued-message-queued-1")).toBeInTheDocument();
	});
});
