import { render, screen, waitFor } from "@testing-library/react";
import { useRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { useSwipeToDismiss } from "./useSwipeToDismiss";

function Card({ label, onDismiss }: { label: string; onDismiss: () => void }) {
	const ref = useRef<HTMLDivElement>(null);
	useSwipeToDismiss(ref, onDismiss);
	return <div ref={ref}>{label}</div>;
}

function wheel(element: HTMLElement, deltaX: number) {
	element.dispatchEvent(new WheelEvent("wheel", { deltaX, deltaMode: WheelEvent.DOM_DELTA_PIXEL }));
}

describe("useSwipeToDismiss", () => {
	it("dismisses a hard swipe once it decays into momentum, without the momentum swiping the next card", async () => {
		const first = vi.fn();
		const second = vi.fn();
		render(<><Card label="First" onDismiss={first} /><Card label="Second" onDismiss={second} /></>);

		for (const deltaX of [-10, -20, -30, -25, -20, -15, -10]) wheel(screen.getByText("First"), deltaX);
		// The rest of the momentum lands on the card below.
		for (const deltaX of [-8, -6, -5, -4, -3, -2, -1]) wheel(screen.getByText("Second"), deltaX);

		await waitFor(() => expect(first).toHaveBeenCalledOnce());
		await new Promise((resolve) => setTimeout(resolve, 600));
		expect(second).not.toHaveBeenCalled();
	});
});
