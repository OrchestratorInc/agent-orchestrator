import { describe, expect, it, vi } from "vitest";
import { markTouchInside, onTouchOutside, rootTouchStart } from "./touchOutside";

describe("touches outside a floating control", () => {
	it("tells listeners of an unmarked touch, and not of one a control marked", () => {
		const listener = vi.fn();
		const stop = onTouchOutside(listener);
		markTouchInside();
		rootTouchStart();
		expect(listener).not.toHaveBeenCalled();
		// A mark lasts for its own touch only.
		rootTouchStart();
		expect(listener).toHaveBeenCalledOnce();
		stop();
		rootTouchStart();
		expect(listener).toHaveBeenCalledOnce();
	});
});
