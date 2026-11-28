import { useCallback, useEffect, useState } from "react";
import { BackHandler, Keyboard } from "react-native";
import { onTouchOutside } from "./touchOutside";

/**
 * Whether a session's pull request list is open over the composer. Like the
 * effort slider's (useEffortSliderOpen) it closes on a tap anywhere outside
 * it, when the keyboard comes up, and on Android's back; unlike it, there is
 * no timeout, since it is a list to read. It only opens with two or more pull
 * requests (one goes straight to its review) and closes when the session drops
 * below two or the composer is busy.
 */
export function usePullRequestPickerOpen(count: number, busy: boolean) {
	const [open, setOpen] = useState(false);
	const shown = open && !busy && count >= 2;
	const close = useCallback(() => setOpen(false), []);
	// A list hidden by its count or the composer stays closed, rather than
	// reappearing when the PRs or the turn come back.
	useEffect(() => { if (open && !shown) setOpen(false); }, [open, shown]);
	useEffect(() => {
		if (!shown) return;
		const back = BackHandler.addEventListener("hardwareBackPress", () => { close(); return true; });
		const keyboard = Keyboard.addListener("keyboardDidShow", close);
		const outside = onTouchOutside(close);
		return () => { back.remove(); keyboard.remove(); outside(); };
	}, [shown, close]);
	const show = useCallback(() => { if (!busy && count >= 2) setOpen(true); }, [busy, count]);
	const toggle = useCallback(() => { if (shown) setOpen(false); else show(); }, [shown, show]);
	return { open: shown, show, toggle, close };
}
