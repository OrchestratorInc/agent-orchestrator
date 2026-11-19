import { useCallback, useLayoutEffect, useRef } from "react";

type NativeTextState = { get(): string; set(value: string): void };

/** Keep native text immediate, with at most one parent acknowledgement in flight. */
export function useNativeTextInput<T extends string | number>(value: string, text: NativeTextState, onChange: (value: T) => void) {
	const committed = useRef(value);
	const pending = useRef<string | undefined>(undefined);
	const latest = useRef<{ value: T; text: string } | undefined>(undefined);

	useLayoutEffect(() => {
		if (value === committed.current) return;
		committed.current = value;
		if (value === pending.current) {
			pending.current = undefined;
			const next = latest.current;
			if (next && next.text !== value) {
				// Coalesce callbacks while the previous update awaits a commit. Repeated
				// values then cannot leave obsolete acknowledgements in a value queue.
				pending.current = next.text;
				onChange(next.value);
			} else {
				latest.current = undefined;
			}
			return;
		}
		// An unmatched parent change is an external reset.
		pending.current = undefined;
		latest.current = undefined;
		if (text.get() !== value) text.set(value);
	}, [value, text, onChange]);

	return useCallback((next: T, resetNative = false) => {
		const normalized = String(next);
		if (resetNative) {
			// Clear cancels queued typing, including an echo the parent may skip
			// because it has returned to the already committed value.
			pending.current = normalized === committed.current ? undefined : normalized;
			latest.current = pending.current === undefined ? undefined : { value: next, text: normalized };
			if (text.get() !== normalized) text.set(normalized);
			onChange(next);
			return;
		}
		latest.current = { value: next, text: normalized };
		if (pending.current !== undefined) return;
		if (normalized === committed.current) {
			latest.current = undefined;
			return;
		}
		pending.current = normalized;
		onChange(next);
	}, [text, onChange]);
}
