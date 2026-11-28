import { createContext } from "react";
import type { View } from "react-native";

/**
 * Brings a focused request-form field into view above the keyboard and the
 * composer. The timeline provides it: its list does not scroll a focused child
 * into view (`scrollsChildToFocus={false}`, which kept selectable text from
 * nudging the conversation), so a field asks for itself when it takes focus.
 * `field` is the whole field (label, hint and input), `input` the input alone.
 */
export const RevealFocusedField = createContext<((field: View | null, input?: View | null) => void) | undefined>(undefined);

/** Window-coordinate spans, in points. */
export type FieldRevealSpans = {
	/** The whole field: its label, its hint and its input. */
	fieldTop: number;
	fieldBottom: number;
	/** The input alone, within the field; the whole field when absent. */
	inputTop?: number;
	inputBottom?: number;
	/** The top of the region the list shows, below the header and banners. */
	visibleTop: number;
	/** The bottom of that region, above the composer and the keyboard. */
	visibleBottom: number;
	/** The list's current scroll offset. */
	offset: number;
};

/**
 * The scroll offset that shows the whole field, moving the list as little as
 * possible, or undefined when it already shows: a field in view never moves the
 * conversation. A field taller than the region (a long hint) shows its input,
 * which is what takes the typing; with its input in view it does not move.
 */
export function fieldRevealOffset({ fieldTop, fieldBottom, inputTop = fieldTop, inputBottom = fieldBottom, visibleTop, visibleBottom, offset }: FieldRevealSpans): number | undefined {
	const fits = fieldBottom - fieldTop <= visibleBottom - visibleTop;
	const top = fits ? fieldTop : inputTop;
	const bottom = fits ? fieldBottom : inputBottom;
	if (top >= visibleTop && bottom <= visibleBottom) return undefined;
	const below = bottom - visibleBottom;
	const above = top - visibleTop;
	// Under the keyboard: up by what is hidden, but never past the top of what
	// has to show (an input taller than the region shows from its top).
	const delta = below > 0 ? Math.min(below, above) : above;
	return delta === 0 ? undefined : offset + delta;
}

/** What `createFieldReveals` needs of React Native's Keyboard. */
export type RevealKeyboard = {
	isVisible(): boolean;
	addListener(event: "keyboardDidShow", listener: () => void): { remove(): void };
};

/**
 * One field reveal at a time for a timeline. A request waits for the keyboard
 * to finish opening (at most `waitMs`; at once when it is already up), then
 * runs `measure`. Each request cancels the one before it, its keyboard
 * listener and its timer, and `isCurrent` turns false for it, so a field
 * focused earlier never scrolls back into view once a later one took focus.
 * `cancel` drops the pending one (a focus with nothing to reveal, unmount).
 */
export function createFieldReveals(keyboard: RevealKeyboard, waitMs: number) {
	let cancelPending: (() => void) | undefined;
	const cancel = () => {
		cancelPending?.();
		cancelPending = undefined;
	};
	return {
		request(measure: (isCurrent: () => boolean) => void) {
			cancel();
			let current = true;
			let waiting = true;
			const stopWaiting = () => {
				if (!waiting) return false;
				waiting = false;
				shown.remove();
				clearTimeout(fallback);
				return true;
			};
			const run = () => { if (stopWaiting()) measure(() => current); };
			const shown = keyboard.addListener("keyboardDidShow", run);
			const fallback = setTimeout(run, keyboard.isVisible() ? 0 : waitMs);
			cancelPending = () => {
				current = false;
				stopWaiting();
			};
		},
		cancel,
	};
}
