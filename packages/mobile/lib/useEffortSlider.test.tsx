import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	back: new Set<() => boolean>(),
	keyboard: new Set<() => void>(),
}));

vi.mock("react-native", () => ({
	BackHandler: { addEventListener: (_event: string, handler: () => boolean) => { fixtures.back.add(handler); return { remove: () => fixtures.back.delete(handler) }; } },
	Keyboard: { addListener: (_event: string, handler: () => void) => { fixtures.keyboard.add(handler); return { remove: () => fixtures.keyboard.delete(handler) }; } },
}));

import { markTouchInside, rootTouchStart } from "./touchOutside";
import { EFFORT_SLIDER_TIMEOUT_MS, useEffortSliderOpen } from "./useEffortSlider";

const levels = [{ value: "low", label: "Low", selected: true }, { value: "high", label: "High", selected: false }];

let renderer: ReactTestRenderer | undefined;
let slider: ReturnType<typeof useEffortSliderOpen>;
beforeEach(() => { vi.useFakeTimers(); vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.useRealTimers(); vi.unstubAllGlobals(); });

function render(over: { choices?: typeof levels; busy?: boolean } = {}) {
	const onClose = vi.fn();
	function Harness({ busy }: { busy: boolean }) {
		slider = useEffortSliderOpen(over.choices ?? levels, busy, onClose);
		return null;
	}
	act(() => { renderer = create(<Harness busy={over.busy ?? false} />); });
	act(() => slider.toggle());
	return { onClose, setBusy: (busy: boolean) => act(() => renderer!.update(<Harness busy={busy} />)) };
}

describe("effort slider open", () => {
	it("stays up through taps on itself and closes on a tap anywhere else", () => {
		const { onClose } = render();
		expect(slider.open).toBe(true);
		act(() => { markTouchInside(); rootTouchStart(); });
		expect(slider.open).toBe(true);
		act(() => rootTouchStart());
		expect(slider.open).toBe(false);
		expect(onClose).toHaveBeenCalledOnce();
	});

	it("closes when the keyboard comes up", () => {
		render();
		act(() => { for (const listener of [...fixtures.keyboard]) listener(); });
		expect(slider.open).toBe(false);
		expect(fixtures.keyboard.size).toBe(0);
	});

	it("closes after a hard twelve seconds, however busy it has been", () => {
		const { onClose } = render();
		act(() => { vi.advanceTimersByTime(EFFORT_SLIDER_TIMEOUT_MS - 1); markTouchInside(); rootTouchStart(); });
		expect(slider.open).toBe(true);
		act(() => { vi.advanceTimersByTime(1); });
		expect(slider.open).toBe(false);
		expect(onClose).toHaveBeenCalledOnce();
	});

	it("closes on Android's back before the screen does", () => {
		render();
		let handled = false;
		act(() => { handled = [...fixtures.back][0](); });
		expect(handled).toBe(true);
		expect(slider.open).toBe(false);
	});

	it("closes when the task starts sending, and never opens for a model without levels", () => {
		const { onClose, setBusy } = render();
		setBusy(true);
		expect(slider.open).toBe(false);
		expect(onClose).toHaveBeenCalledOnce();
		render({ choices: [] });
		expect(slider.open).toBe(false);
	});

	it("listens to nothing once closed", () => {
		render();
		act(() => slider.toggle());
		expect(fixtures.back.size + fixtures.keyboard.size).toBe(0);
		act(() => { vi.advanceTimersByTime(EFFORT_SLIDER_TIMEOUT_MS); rootTouchStart(); });
		expect(slider.open).toBe(false);
	});
});
