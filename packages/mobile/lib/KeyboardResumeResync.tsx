import { useEffect } from "react";
import { AppState, Platform } from "react-native";
import { useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";

/**
 * Re-applies every keyboard-driven animated style when the app comes back.
 *
 * On Android, a keyboard that closes while the activity is paused (send, then
 * Home at once; or the mic permission dialog) runs its whole animation in the
 * background. The keyboard's values end at closed, and the views follow them,
 * but on resume the views snap back to their keyboard-open transforms: the chat
 * composer stood a keyboard's height up over the conversation until the
 * keyboard next opened. Nothing in JS changes at that point, so no style reruns.
 *
 * Forcing an update of the keyboard's own values wakes every style that reads
 * them, and each redraws from the closed values.
 */
export function KeyboardResumeResync() {
	const { height, progress } = useReanimatedKeyboardAnimation();
	useEffect(() => {
		if (Platform.OS !== "android") return;
		const subscription = AppState.addEventListener("change", (state) => {
			if (state !== "active") return;
			// `modify()` with no modifier forces the update: a plain write of the
			// same value is dropped before any style hears it.
			height.modify();
			progress.modify();
		});
		return () => subscription.remove();
	}, [height, progress]);
	return null;
}
