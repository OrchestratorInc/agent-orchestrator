import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	back: new Set<() => boolean>(),
	keyboard: new Set<() => void>(),
	keysUp: false,
}));

vi.mock("react-native", () => ({
	BackHandler: { addEventListener: (_event: string, handler: () => boolean) => { fixtures.back.add(handler); return { remove: () => fixtures.back.delete(handler) }; } },
	Keyboard: {
		addListener: (_event: string, handler: () => void) => { fixtures.keyboard.add(handler); return { remove: () => fixtures.keyboard.delete(handler) }; },
		isVisible: () => fixtures.keysUp,
	},
}));

import { markTouchInside, rootTouchStart } from "./touchOutside";
import { EFFORT_SLIDER_TIMEOUT_MS, useEffortSliderOpen } from "./useEffortSlider";

const levels = [{ value: "low", label: "Low", selected: true }, { value: "high", label: "High", selected: false }];

let renderer: ReactTestRenderer | undefined;
let slider: ReturnType<typeof useEffortSliderOpen>;
beforeEach(() => { fixtures.keysUp = false; vi.useFakeTimers(); vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.useRealTimers(); vi.unstubAllGlobals(); });

function render(over: { choices?: typeof levels; busy?: boolean; toggles?: boolean } = {}) {
	const onClose = vi.fn();
	function Harness({ busy }: { busy: boolean }) {
		slider = useEffortSliderOpen(over.choices ?? levels, busy, onClose, undefined, undefined, over.toggles);
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

	// A send, a save or a reconnect flickering busy shut the slider under the
	// finger: busy keeps it from opening, and leaves one in use up.
	it("stays up through busy, never opens while busy, and never for a model without levels", () => {
		const { onClose, setBusy } = render();
		setBusy(true);
		expect(slider.open).toBe(true);
		expect(onClose).not.toHaveBeenCalled();
		act(() => slider.toggle());
		expect(slider.open).toBe(false);
		act(() => slider.toggle());
		expect(slider.open).toBe(false);
		render({ choices: [] });
		expect(slider.open).toBe(false);
	});

	// Claude Haiku takes no effort, but Ultracode or Fast mode still rides on the slider.
	it("opens with no levels while the slider carries a switch", () => {
		render({ choices: [], toggles: true });
		expect(slider.open).toBe(true);
	});

	// Over the keys, Android reports the keyboard shown again whenever it
	// changes height (its suggestion strip, another keyboard): that closed the
	// slider mid-drag. Only the keyboard coming up under a slider opened
	// without it puts the slider away.
	// A picker opened with the keys down brings them up itself (useKeysWithPickers):
	// the keyboard arriving must not put it away, nor Android re-reporting it.
	it("stays up as the keyboard comes up", () => {
		render();
		expect(fixtures.keyboard.size).toBe(0);
		expect(slider.open).toBe(true);
	});

	it("listens to nothing once closed", () => {
		render();
		act(() => slider.toggle());
		expect(fixtures.back.size + fixtures.keyboard.size).toBe(0);
		act(() => { vi.advanceTimersByTime(EFFORT_SLIDER_TIMEOUT_MS); rootTouchStart(); });
		expect(slider.open).toBe(false);
	});

	// A request card replacing the composer unmounts the slider's host
	// mid-pick; the owner's commit must still hear the close.
	it("tells its owner it closed when it goes away while up", () => {
		const { onClose } = render();
		act(() => renderer!.unmount());
		renderer = undefined;
		expect(onClose).toHaveBeenCalledOnce();
	});

	it("says nothing on going away while closed", () => {
		const { onClose } = render();
		act(() => slider.toggle());
		expect(onClose).toHaveBeenCalledOnce();
		act(() => renderer!.unmount());
		renderer = undefined;
		expect(onClose).toHaveBeenCalledOnce();
	});
});
