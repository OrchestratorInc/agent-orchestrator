import { useCallback, useEffect, useRef, useState } from "react";
import { BackHandler, Keyboard } from "react-native";
import { onTouchOutside } from "./touchOutside";
import type { EffortChoice } from "./modelEffort";

/** How long the slider stays open, whatever happens on it. */
export const EFFORT_SLIDER_TIMEOUT_MS = 12_000;

/**
 * Whether the effort slider is open. It stays up while the level is adjusted
 * and closes on a tap anywhere outside it (the field included), when the
 * keyboard comes up, on Android's back, and after a hard twelve seconds; it
 * also closes when the task starts sending or the model takes no effort.
 * `onClose` hears every close, however it came about.
 */
export function useEffortSliderOpen(choices: readonly EffortChoice[], busy: boolean, onClose?: () => void) {
	const [open, setOpen] = useState(false);
	const shown = open && !busy && choices.length > 0;
	const closeRef = useRef(onClose);
	closeRef.current = onClose;
	const wasShown = useRef(false);
	const close = useCallback(() => setOpen(false), []);
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
	return { open: shown, toggle: () => setOpen(!shown), close };
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
export function useEffortSlider(choices: readonly EffortChoice[], onChoose: (value: string) => void, tick: () => void) {
	const chosen = choices.findIndex((choice) => choice.selected);
	const [index, setIndex] = useState(chosen);
	const indexRef = useRef(chosen);
	useEffect(() => { indexRef.current = chosen; setIndex(chosen); }, [chosen]);
	const pick = (next: number) => {
		if (next < 0 || next >= choices.length || next === indexRef.current) return;
		indexRef.current = next;
		setIndex(next);
		tick();
		onChoose(choices[next].value);
	};
	return { index, label: index >= 0 ? choices[index].label : "Default", pick };
}
