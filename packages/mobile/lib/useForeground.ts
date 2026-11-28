import { useEffect, useState } from "react";
import { AppState } from "react-native";

import { shouldPoll } from "./appStatePoll";

/**
 * Whether the app is in front, by the same rule as the board's poll: a
 * backgrounded app stops polling and closes its sockets, while "inactive" (the
 * app switcher, Control Centre, a permission prompt) still counts as in front.
 */
export function useForeground(): boolean {
	const [foreground, setForeground] = useState(() => shouldPoll(AppState.currentState));
	useEffect(() => {
		const sub = AppState.addEventListener("change", (state) => setForeground(shouldPoll(state)));
		return () => sub.remove();
	}, []);
	return foreground;
}
