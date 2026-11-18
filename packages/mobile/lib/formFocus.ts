import type { TextFieldRef } from "@expo/ui/swift-ui";
import { createContext, useContext, useMemo } from "react";

/** How long a blurred field gets to say it has let go before the submit goes ahead anyway. */
export const BLUR_SETTLE_MS = 300;

/** What one SwiftUI text field takes to be tracked: its ref and its focus callback. */
export type TrackedField = {
	ref(field: TextFieldRef | null): void;
	onFocusChange(focused: boolean): void;
};

export type FormFocus = {
	/** The props that track the field `id`; the same object for the same id. */
	field(id: string): TrackedField;
	/**
	 * Runs `submit` once no field holds focus: the focused one is blurred first
	 * and given until it reports the loss (or `BLUR_SETTLE_MS`). A second call
	 * while one is waiting or running is ignored.
	 */
	submit(submit: () => unknown): Promise<void>;
};

/**
 * Taking focus off a SwiftUI form before submitting it.
 *
 * A form sheet that changes or closes as soon as its submit returns (New
 * project's Create: the POST can answer in a few milliseconds) rebuilt its
 * list while the text field's cell was still first responder, and UIKit
 * aborted the app (`-[UICollectionView
 * _resignOrRebaseFirstResponderViewWithIndexPathMapping:]`). `blur()` only
 * sets SwiftUI's focus state, so the field is let go of by the time its
 * `onFocusChange(false)` arrives, not when the promise resolves.
 */
export function useFormFocus(): FormFocus {
	return useMemo(() => {
		const refs = new Map<string, TextFieldRef>();
		const fields = new Map<string, TrackedField>();
		let focused: string | null = null;
		let letGo: (() => void) | null = null;
		let busy = false;

		const field = (id: string): TrackedField => {
			let tracked = fields.get(id);
			if (!tracked) {
				tracked = {
					ref: (ref) => {
						if (ref) refs.set(id, ref);
						else refs.delete(id);
					},
					onFocusChange: (isFocused) => {
						if (isFocused) {
							focused = id;
						} else if (focused === id) {
							focused = null;
							letGo?.();
						}
					},
				};
				fields.set(id, tracked);
			}
			return tracked;
		};

		const release = (): Promise<void> => {
			const ref = focused ? refs.get(focused) : undefined;
			if (!ref) return Promise.resolve();
			return new Promise<void>((resolve) => {
				const done = () => {
					clearTimeout(timer);
					letGo = null;
					resolve();
				};
				const timer = setTimeout(done, BLUR_SETTLE_MS);
				letGo = done;
				void ref.blur().catch(done);
			});
		};

		const submit = async (run: () => unknown) => {
			if (busy) return;
			busy = true;
			try {
				await release();
				await run();
			} finally {
				busy = false;
			}
		};

		return { field, submit };
	}, []);
}

/**
 * The form's focus tracker, for the fields inside it: `FormTextField` joins
 * by itself, keyed by its testID (or label); a bare TextField calls
 * useTrackedField.
 */
export const FormFocusContext = createContext<FormFocus | null>(null);

/** The tracking props for field `id` in the surrounding form, if it tracks focus. */
export function useTrackedField(id: string): TrackedField | undefined {
	return useContext(FormFocusContext)?.field(id);
}
