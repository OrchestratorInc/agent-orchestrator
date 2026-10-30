import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { QueuedMessageDock, type QueuedMessage } from "./QueuedMessageDock";

// Real DndContext (no mock): proves the grip is wired to dnd-kit's KeyboardSensor.

function queued(texts: string[]): QueuedMessage[] {
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

describe("QueuedMessageDock keyboard reorder", () => {
	it("exposes each grip as a focusable sortable button", () => {
		render(<QueuedMessageDock messages={queued(["a", "b"])} onReorderQueuedTurns={vi.fn()} />);
		const grips = screen.getAllByRole("button", { name: "Drag to reorder queued message" });
		expect(grips).toHaveLength(2);
		for (const grip of grips) {
			expect(grip).toHaveAttribute("aria-roledescription", "sortable");
			expect(grip).toHaveProperty("tabIndex", 0);
		}
	});

	it("starts a keyboard drag on Space and cancels it on Escape", async () => {
		const user = userEvent.setup();
		const onReorder = vi.fn().mockResolvedValue(undefined);
		render(<QueuedMessageDock messages={queued(["a", "b"])} onReorderQueuedTurns={onReorder} />);
		const [grip] = screen.getAllByRole("button", { name: "Drag to reorder queued message" });
		grip.focus();
		await user.keyboard(" ");
		await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent(/queued-2/));
		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent(/cancelled/i));
		expect(onReorder).not.toHaveBeenCalled();
	});

	it("keeps Enter on the row's own action buttons working", async () => {
		const user = userEvent.setup();
		const onBeginQueuedEdit = vi.fn();
		render(
			<QueuedMessageDock
				messages={queued(["a", "b"])}
				onBeginQueuedEdit={onBeginQueuedEdit}
				onReorderQueuedTurns={vi.fn()}
			/>,
		);
		const [edit] = screen.getAllByRole("button", { name: "Edit queued message" });
		edit.focus();
		await user.keyboard("{Enter}");
		expect(onBeginQueuedEdit).toHaveBeenCalledTimes(1);
	});
});
