import { fireEvent, render, screen } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { usePersistentGutterUtility } from "./usePersistentGutterUtility";

afterEach(() => {
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

it("skips hover replay when elementFromPoint is unavailable", () => {
	let frame: FrameRequestCallback | undefined;
	vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => { frame = callback; return 1; });
	vi.stubGlobal("PointerEvent", MouseEvent);
	const original = Object.getOwnPropertyDescriptor(document, "elementFromPoint");
	Object.defineProperty(document, "elementFromPoint", { configurable: true, value: undefined });
	try {
		function Probe() {
			const ref = useRef<HTMLDivElement>(null);
			const { onPointerMove, restoreAfterRender } = usePersistentGutterUtility(ref);
			return <div ref={ref} onPointerMove={onPointerMove} data-testid="gutter"><button onClick={restoreAfterRender}>Refresh</button></div>;
		}
		render(<Probe />);
		fireEvent.pointerMove(screen.getByTestId("gutter"), { clientX: 12, clientY: 20 });
		fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
		expect(frame).toBeTypeOf("function");
		expect(() => frame?.(0)).not.toThrow();
	} finally {
		if (original) Object.defineProperty(document, "elementFromPoint", original);
		else Reflect.deleteProperty(document, "elementFromPoint");
	}
});
