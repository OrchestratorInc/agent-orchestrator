import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { BackHandler, Keyboard } from "react-native";
import { onTouchOutside } from "./touchOutside";
import type { EffortChoice } from "./modelEffort";

/** How long the slider stays open, whatever happens on it. */
export const EFFORT_SLIDER_TIMEOUT_MS = 12_000;

/**
 * Whether a composer's effort slider is open, and how far above the bottom of
 * its container it floats, held outside React state. The gauge and the slider
 * each subscribe on their own, so opening the slider re-renders those two and
 * not the composer around them: re-rendering a whole chat composer (its native
 * menus included) on each open held the slider back by a good half second.
 */
export type EffortSliderSwitch = {
	isOpen(): boolean;
	setOpen(open: boolean): void;
	bottom(): number;
	setBottom(bottom: number): void;
	subscribe(listener: () => void): () => void;
};

export function createEffortSliderSwitch(): EffortSliderSwitch {
	let open = false;
	let bottom = 0;
	const listeners = new Set<() => void>();
	const emit = () => { for (const listener of [...listeners]) listener(); };
	return {
		isOpen: () => open,
		setOpen: (next) => { if (next !== open) { open = next; emit(); } },
		bottom: () => bottom,
		setBottom: (next) => { if (next !== bottom) { bottom = next; emit(); } },
		subscribe: (listener) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
	};
}

/** A switch that lives as long as the component that makes it. */
export function useEffortSliderSwitch(): EffortSliderSwitch {
	return useState(createEffortSliderSwitch)[0];
}

/** Whether the switch is on, re-rendering the caller only when that changes. */
export function useEffortSliderSwitchOpen(slider: EffortSliderSwitch): boolean {
	return useSyncExternalStore(slider.subscribe, slider.isOpen);
}

/**
 * Whether the effort slider is open. It stays up while the level is adjusted
 * and closes on a tap anywhere outside it (the field included), when the
 * keyboard comes up, on Android's back, and after a hard twelve seconds; it
 * also closes when the task starts sending or the model takes no effort.
 * `onClose` hears every close, however it came about.
 */
export function useEffortSliderOpen(choices: readonly EffortChoice[], busy: boolean, onClose?: () => void, slider?: EffortSliderSwitch) {
	const own = useEffortSliderSwitch();
	const store = slider ?? own;
	const open = useEffortSliderSwitchOpen(store);
	const shown = open && !busy && choices.length > 0;
	const closeRef = useRef(onClose);
	closeRef.current = onClose;
	const wasShown = useRef(false);
	const close = useCallback(() => store.setOpen(false), [store]);
	// Unmounting while shown is a close too: the owner's commit must hear it.
	useEffect(() => () => { if (wasShown.current) closeRef.current?.(); }, []);
	useEffect(() => {
		if (wasShown.current && !shown) closeRef.current?.();
		wasShown.current = shown;
		if (!shown) return;
		const back = BackHandler.addEventListener("hardwareBackPress", () => { close(); return true; });
		const keyboard = Keyboard.addListener("keyboardDidShow", close);
		const outside = onTouchOutside(close);
		const timer = setTimeout(close, EFFORT_SLIDER_TIMEOUT_MS);
		return () => { back.remove(); keyboard.remove(); outside(); clearTimeout(timer); };
	}, [shown, close]);
	return { open: shown, toggle: () => store.setOpen(!shown), close };
}

export type EffortSliderProps = {
	choices: readonly EffortChoice[];
	onChoose: (value: string) => void;
	/** Called when the finger lifts off the slider, its level settled. */
	onRelease?: () => void;
};

/**
 * The slider's level: the thumb's stop follows the finger at once while the
 * owner's choice catches up, and each new stop ticks and is chosen.
 */
export function useEffortSlider(choices: readonly EffortChoice[], onChoose: (value: string) => void, tick: () => void, visible = true) {
	const chosen = choices.findIndex((choice) => choice.selected);
	const [index, setIndex] = useState(chosen);
	const indexRef = useRef(chosen);
	// Back on the chosen level whenever it changes, and each time the slider
	// is put away: it is kept mounted between opens, and a pick whose save
	// failed never moves `chosen`, so the next open would show that level.
	useEffect(() => { indexRef.current = chosen; setIndex(chosen); }, [chosen, visible]);
	const pick = (next: number) => {
		if (next < 0 || next >= choices.length || next === indexRef.current) return;
		indexRef.current = next;
		setIndex(next);
		tick();
		onChoose(choices[next].value);
	};
	// The level kept from before the choices changed can point past them (a
	// model with fewer levels, or none) until the effect above catches up; the
	// slider stays mounted across model changes, so fall back to the chosen one.
	const current = index < choices.length ? index : chosen;
	return { index: current, label: current >= 0 ? choices[current].label : "Default", pick };
}
