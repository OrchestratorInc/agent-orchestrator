import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { SelectionAskButton } from "./SelectionAskButton";
import type { CodeSelection } from "./useCodeSelection";

function source(selection: Partial<CodeSelection> | null) {
	return { get: () => selection as CodeSelection | null, subscribe: () => () => {} };
}

describe("SelectionAskButton", () => {
	it("shows nothing without a selection", () => {
		render(<SelectionAskButton onAsk={vi.fn()} source={source(null)} />);
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
	});

	it("asks in chat without taking the selection away", () => {
		const onAsk = vi.fn();
		render(<SelectionAskButton onAsk={onAsk} source={source({ range: { start: 2, end: 4 }, point: { x: 120, y: 80 }, dragging: false })} />);
		const button = screen.getByRole("button", { name: "Ask in chat" });
		expect(fireEvent.mouseDown(button)).toBe(false);
		fireEvent.click(button);
		expect(onAsk).toHaveBeenCalledTimes(1);
	});

	it("lets a selection still being dragged pass through it", () => {
		render(<SelectionAskButton onAsk={vi.fn()} source={source({ range: { start: 2, end: 4 }, point: { x: 0, y: 0 }, dragging: true })} />);
		expect(screen.getByRole("button", { name: "Ask in chat" }).parentElement).toHaveStyle({ pointerEvents: "none" });
	});
});
