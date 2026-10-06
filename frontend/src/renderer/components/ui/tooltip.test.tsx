import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LazyTooltip, TooltipProvider } from "./tooltip";

describe("LazyTooltip", () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	function setup() {
		render(
			<TooltipProvider>
				<LazyTooltip content="Pin session">
					<button aria-label="Pin session" type="button" />
				</LazyTooltip>
			</TooltipProvider>,
		);
		return screen.getByRole("button", { name: "Pin session" });
	}

	it("mounts no tooltip machinery until the trigger is hovered", () => {
		const button = setup();
		expect(button.parentElement?.querySelector("span[aria-hidden='true']")).toBeNull();
		expect(screen.queryByRole("tooltip")).toBeNull();
		fireEvent.pointerEnter(button.parentElement!);
		expect(button.parentElement?.querySelector("span[aria-hidden='true']")).not.toBeNull();
	});

	it("shows its content after the standard hover delay and hides on leave", () => {
		const button = setup();
		fireEvent.pointerEnter(button.parentElement!);
		act(() => void vi.advanceTimersByTime(300));
		expect(screen.queryByRole("tooltip")).toBeNull();
		act(() => void vi.advanceTimersByTime(150));
		expect(screen.getByRole("tooltip")).toHaveTextContent("Pin session");
		fireEvent.pointerLeave(button.parentElement!);
		expect(screen.queryByRole("tooltip")).toBeNull();
	});

	it("keeps the child's own accessible name", () => {
		setup();
		expect(screen.getByLabelText("Pin session")).toBeInTheDocument();
	});
});
