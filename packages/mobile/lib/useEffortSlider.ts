import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { BackHandler } from "react-native";
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
 *
 * It also holds the level being picked, ahead of the owner's choices: the
 * level under the finger while it drags, then the one it chose until the
 * owner's choices show it saved (see useChatEffort). The gauge and the slider
 * both show it, so they never disagree, and the composer is not re-rendered
 * for each level the finger crosses.
 */
export type EffortSliderSwitch = {
	isOpen(): boolean;
	setOpen(open: boolean): void;
	bottom(): number;
	setBottom(bottom: number): void;
	pick(): string | undefined;
	setPick(value: string | undefined): void;
	subscribe(listener: () => void): () => void;
};

export function createEffortSliderSwitch(): EffortSliderSwitch {
	let open = false;
	let bottom = 0;
	let pick: string | undefined;
	const listeners = new Set<() => void>();
	const emit = () => { for (const listener of [...listeners]) listener(); };
	return {
		isOpen: () => open,
		setOpen: (next) => { if (next !== open) { open = next; emit(); } },
		bottom: () => bottom,
		setBottom: (next) => { if (next !== bottom) { bottom = next; emit(); } },
		pick: () => pick,
		setPick: (next) => { if (next !== pick) { pick = next; emit(); } },
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

/** `choices` with the switch's pick checked in place of the owner's, when it is one of them. */
export function withPick<T extends { value: string; selected: boolean }>(choices: readonly T[], pick: string | undefined): readonly T[] {
	if (pick === undefined || !choices.some((choice) => choice.value === pick)) return choices;
	return choices.map((choice) => (choice.selected === (choice.value === pick) ? choice : { ...choice, selected: choice.value === pick }));
}

/** The choices as the gauge and the slider show them: the level being picked, else the owner's. */
export function useEffortChoicesShown<T extends { value: string; selected: boolean }>(slider: EffortSliderSwitch, choices: readonly T[]): readonly T[] {
	const pick = useSyncExternalStore(slider.subscribe, slider.pick);
	return useMemo(() => withPick(choices, pick), [choices, pick]);
}

/**
 * Whether the effort slider is open. It stays up while the level is adjusted
 * and closes on a tap anywhere outside it (the field included), on Android's
 * back, and after `timeoutMs` where one is given. The keyboard coming up is no
 * close: a picker opened with the keys down brings them up itself
 * (useKeysWithPickers), and lies over them; it also closes when the model takes no
 * effort and the slider carries no switch (`toggles`: Fast mode, Ultracode).
 * Busy keeps it from opening, never shuts one in use: a send, a save or a
 * reconnect flickering busy closed the slider under the finger.
 * `onClose` hears every close, however it came about.
 */
export function useEffortSliderOpen(
	choices: readonly { selected: boolean }[],
	busy: boolean,
	onClose?: () => void,
	slider?: EffortSliderSwitch,
	/** How long it may stay up; `null` for no limit, as a list being read wants. */
	timeoutMs: number | null = EFFORT_SLIDER_TIMEOUT_MS,
	/** The slider carries a switch, so it stays open with no levels to pick. */
	toggles = false,
) {
	const own = useEffortSliderSwitch();
	const store = slider ?? own;
	const open = useEffortSliderSwitchOpen(store);
	const wasShown = useRef(false);
	const shown = open && (choices.length > 0 || toggles) && (!busy || wasShown.current);
	const closeRef = useRef(onClose);
	closeRef.current = onClose;
	const close = useCallback(() => store.setOpen(false), [store]);
	// Unmounting while shown is a close too: the owner's commit must hear it.
	useEffect(() => () => { if (wasShown.current) closeRef.current?.(); }, []);
	useEffect(() => {
		if (wasShown.current && !shown) closeRef.current?.();
		wasShown.current = shown;
		if (!shown) return;
		const back = BackHandler.addEventListener("hardwareBackPress", () => { close(); return true; });
		const outside = onTouchOutside(close);
		const timer = timeoutMs === null ? undefined : setTimeout(close, timeoutMs);
		return () => { back.remove(); outside(); if (timer !== undefined) clearTimeout(timer); };
	}, [shown, close, timeoutMs]);
	return { open: shown, toggle: () => store.setOpen(!shown), close };
}

export type EffortSliderProps = {
	choices: readonly EffortChoice[];
	onChoose: (value: string) => void;
	/** Called when the finger lifts off the slider, its level settled. */
	onRelease?: () => void;
};

/**
 * The slider's level. A drag shows each level it crosses at once (`show`: the
 * label and a tick) and chooses only where it ends (`choose`): choosing
 * re-renders the owner, and doing it per level was the JS work a streaming
 * reply competed with. `pick` does both, for a screen reader's step.
 */
export function useEffortSlider(choices: readonly EffortChoice[], onChoose: (value: string) => void, tick: () => void) {
	const chosen = choices.findIndex((choice) => choice.selected);
	const [index, setIndex] = useState(chosen);
	const indexRef = useRef(chosen);
	useEffect(() => { indexRef.current = chosen; setIndex(chosen); }, [chosen]);
	const show = (next: number) => {
		if (next < 0 || next >= choices.length || next === indexRef.current) return;
		indexRef.current = next;
		setIndex(next);
		tick();
	};
	const choose = (next: number) => {
		if (next < 0 || next >= choices.length || next === chosen) return;
		onChoose(choices[next].value);
	};
	const pick = (next: number) => {
		if (next === indexRef.current) return;
		show(next);
		choose(next);
	};
	// The level kept from before the choices changed can point past them (a
	// model with fewer levels, or none) until the effect above catches up; the
	// slider stays mounted across model changes, so fall back to the chosen one.
	const current = index < choices.length ? index : chosen;
	return { index: current, label: current >= 0 ? choices[current].label : "Default", show, choose, pick };
}
