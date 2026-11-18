import { useCallback, useEffect, useState, type RefObject } from "react";
import { TextInput } from "react-native";

/** Long enough for the field's focus event to arrive; past it, stop holding. */
const FOCUS_SETTLE_MS = 500;

/**
 * Keeps typing in the composer's field across a send made with Return on a
 * hardware keyboard. The field is read-only while the send is in flight, which
 * can take its focus; once the send lands, the field gets focus back, once,
 * unless the user has since moved to another input, which keeps it.
 *
 * `holding` is true from the send until the field reports focus again (`onFocus`),
 * so a composer that opens while its field is focused can stay open through the
 * gap rather than starting to close for the frame where the draft has cleared
 * but focus has not yet returned.
 */
export function useKeyboardSendRefocus(inputRef: RefObject<TextInput | null>, submitting: boolean) {
	const [phase, setPhase] = useState<"idle" | "sending" | "refocusing">("idle");
	useEffect(() => {
		if (phase !== "sending" || submitting) return;
		const field = inputRef.current;
		const focusedInput = TextInput.State?.currentlyFocusedInput?.();
		if (!field || field.isFocused?.() || (focusedInput && focusedInput !== field)) {
			setPhase("idle");
			return;
		}
		field.focus();
		setPhase("refocusing");
	}, [inputRef, phase, submitting]);
	useEffect(() => {
		if (phase !== "refocusing") return;
		// A focus event that never arrives must not keep the composer open.
		const settle = setTimeout(() => setPhase("idle"), FOCUS_SETTLE_MS);
		return () => clearTimeout(settle);
	}, [phase]);
	return {
		holding: phase !== "idle",
		/** Call when Return on a hardware keyboard starts a send, in the same update as `submitting` turns on. */
		begin: useCallback(() => setPhase("sending"), []),
		/** Call from the field's `onFocus`. */
		focused: useCallback(() => setPhase((current) => current === "refocusing" ? "idle" : current), []),
	};
}
