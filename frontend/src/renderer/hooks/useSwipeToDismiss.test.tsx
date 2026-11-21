import { render, screen, waitFor } from "@testing-library/react";
import { useRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { useSwipeToDismiss } from "./useSwipeToDismiss";
import { resetSwipeClaim, swipeClaimed } from "../lib/swipe-ownership";

function Card({ label, onDismiss }: { label: string; onDismiss: () => void }) {
	const ref = useRef<HTMLDivElement>(null);
	useSwipeToDismiss(ref, onDismiss);
	return <div ref={ref}>{label}</div>;
}

function wheel(element: HTMLElement, deltaX: number, deltaY = 0) {
	element.dispatchEvent(new WheelEvent("wheel", { deltaX, deltaY, deltaMode: WheelEvent.DOM_DELTA_PIXEL }));
}

describe("useSwipeToDismiss", () => {
	it("dismisses a hard swipe once it decays into momentum, without the momentum swiping the next card", async () => {
		const first = vi.fn();
		const second = vi.fn();
		render(<><Card label="First" onDismiss={first} /><Card label="Second" onDismiss={second} /></>);

		for (const deltaX of [-10, -20, -30, -25, -20, -15, -10]) wheel(screen.getByText("First"), deltaX);
		// The rest of the momentum lands on the card below, which claims it so it cannot navigate either.
		resetSwipeClaim();
		for (const deltaX of [-8, -6, -5, -4, -3, -2, -1]) wheel(screen.getByText("Second"), deltaX);
		expect(swipeClaimed()).toBe(true);

		await waitFor(() => expect(first).toHaveBeenCalledOnce());
		await new Promise((resolve) => setTimeout(resolve, 600));
		expect(second).not.toHaveBeenCalled();
	});
	it("keeps its swipes from swipe navigation: opts out and claims the swipes it takes", () => {
		const { unmount } = render(<Card label="Card" onDismiss={vi.fn()} />);
		const card = screen.getByText("Card");
		expect(card).toHaveAttribute("data-no-swipe-nav");

		// Scrolling down through it is not a swipe it takes.
		resetSwipeClaim();
		for (let i = 0; i < 4; i++) wheel(card, 0, 20);
		expect(swipeClaimed()).toBe(false);
		unmount();

		render(<Card label="Other" onDismiss={vi.fn()} />);
		resetSwipeClaim();
		for (let i = 0; i < 3; i++) wheel(screen.getByText("Other"), -10);
		expect(swipeClaimed()).toBe(true);
	});
});
