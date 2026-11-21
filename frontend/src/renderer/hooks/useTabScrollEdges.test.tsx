import { act, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { useTabScrollEdges } from "./useTabScrollEdges";

// The session tab strip mounts through a portal after its owner rendered, so
// the hook must wire the wheel handler when the element appears, not only on
// the owner's first render.
function LateStrip() {
	const { scrollRef } = useTabScrollEdges();
	const [mounted, setMounted] = useState(false);
	return (
		<>
			<button onClick={() => setMounted(true)} type="button">
				Mount
			</button>
			{mounted ? <div data-testid="strip" ref={scrollRef} /> : null}
		</>
	);
}

describe("useTabScrollEdges", () => {
	it("scrolls a strip that mounts after the first render with the mouse wheel", () => {
		render(<LateStrip />);
		act(() => screen.getByRole("button", { name: "Mount" }).click());

		const strip = screen.getByTestId("strip");
		Object.defineProperty(strip, "scrollWidth", { configurable: true, value: 500 });
		Object.defineProperty(strip, "clientWidth", { configurable: true, value: 200 });
		let scrolledBy = 0;
		strip.scrollBy = ((options: ScrollToOptions) => {
			scrolledBy += options.left ?? 0;
		}) as typeof strip.scrollBy;

		const wheel = new WheelEvent("wheel", { bubbles: true, cancelable: true, deltaY: 40 });
		strip.dispatchEvent(wheel);

		expect(wheel.defaultPrevented).toBe(true);
		expect(scrolledBy).toBe(40);
	});
});
