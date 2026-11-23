import type { TextFieldRef } from "@expo/ui/swift-ui";
import { createContext, useContext, useEffect, useMemo, useState } from "react";

/** How long a blurred field gets to say it has let go before the submit goes ahead anyway. */
export const BLUR_SETTLE_MS = 300;

/** How many fields in a row a submit lets go of (one per tap during the wait) before going ahead. */
export const MAX_RELEASES = 5;

/** What one SwiftUI text field takes to be tracked: its ref and its focus callback. */
export type TrackedField = {
	ref(field: TextFieldRef | null): void;
	onFocusChange(focused: boolean): void;
};

/** A tracked field as a form's field reads it: also whether it is locked while the form submits. */
export type TrackedFieldState = TrackedField & { locked: boolean };

export type FormFocus = {
	/** The props that track the field `id`; the same object for the same id. */
	field(id: string): TrackedField;
	/**
	 * Runs `submit` once no field holds focus: the focused one is blurred first
	 * and given until it reports the loss (or `BLUR_SETTLE_MS`), and every
	 * tracked field is then locked until `submit` settles. A second call while
	 * one is waiting or running is ignored, and so is a submit whose form
	 * closed while it waited (Cancel, or the sheet switching face).
	 */
	submit(submit: () => unknown): Promise<void>;
	/** Whether the form's fields are locked: a submit is running. */
	locked: boolean;
};

/**
 * Taking focus off a SwiftUI form before submitting it, and keeping it off.
 *
 * A form sheet that changes or closes as soon as its submit returns (New
 * project's Create: the POST can answer in a few milliseconds) rebuilt its
 * list while the text field's cell was still first responder, and UIKit
 * aborted the app (`-[UICollectionView
 * _resignOrRebaseFirstResponderViewWithIndexPathMapping:]`). `blur()` only
 * sets SwiftUI's focus state, so the field is let go of by the time its
 * `onFocusChange(false)` arrives, not when the promise resolves. The fields
 * stay locked while the request runs, so tapping one during a slow reply
 * cannot make it first responder again before the sheet closes. They are
 * locked only once focus is off: nothing focused is changed under the user.
 */
export function useFormFocus(): FormFocus {
	const [locked, setLocked] = useState(false);
	const core = useMemo(() => {
		const refs = new Map<string, TextFieldRef>();
		const fields = new Map<string, TrackedField>();
		let focused: string | null = null;
		let letGo: (() => void) | null = null;
		let busy = false;
		let closed = false;

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
							// Another field taking focus means the one being let go of has.
							const previous = focused;
							focused = id;
							if (previous !== id) letGo?.();
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
			const target = focused;
			const ref = target ? refs.get(target) : undefined;
			if (!ref) return Promise.resolve();
			return new Promise<void>((resolve) => {
				const done = () => {
					clearTimeout(timer);
					letGo = null;
					resolve();
				};
				// Silent past the wait: counted as let go, so it isn't waited for twice.
				const timer = setTimeout(() => {
					if (focused === target) focused = null;
					done();
				}, BLUR_SETTLE_MS);
				letGo = done;
				void ref.blur().catch(done);
			});
		};

		const submit = async (run: () => unknown) => {
			if (busy) return;
			busy = true;
			try {
				// A field tapped while another let go takes focus in turn: keep
				// letting go while a tracked field holds it.
				for (let pass = 0; pass < MAX_RELEASES && focused && refs.has(focused) && !closed; pass++) {
					await release();
				}
				if (closed) return;
				setLocked(true);
				await run();
			} finally {
				busy = false;
				if (!closed) setLocked(false);
			}
		};

		const setClosed = (value: boolean) => { closed = value; };
		return { field, submit, setClosed };
	}, []);
	useEffect(() => {
		core.setClosed(false);
		return () => core.setClosed(true);
	}, [core]);
	return useMemo(() => ({ field: core.field, submit: core.submit, locked }), [core, locked]);
}

/**
 * The form's focus tracker, for the fields inside it: `FormTextField` joins
 * by itself, keyed by its testID (or label); a bare TextField calls
 * useTrackedField.
 */
export const FormFocusContext = createContext<FormFocus | null>(null);

/** The tracking props for field `id` in the surrounding form, if it tracks focus. */
export function useTrackedField(id: string): TrackedFieldState | undefined {
	const focus = useContext(FormFocusContext);
	if (!focus) return undefined;
	const tracked = focus.field(id);
	return { ref: tracked.ref, onFocusChange: tracked.onFocusChange, locked: focus.locked };
}
