import { useNavigation, type NativeStackNavigationProp } from "expo-router";
import { useEffect, useState } from "react";

// A page that opens without a push (a cold start on a deep link, a stack's
// first page) never hears `transitionEnd`. Comfortably past a push's slide,
// whose end is reported late while the JS thread is busy.
export const PUSH_SETTLE_FALLBACK_MS = 800;

/**
 * Whether the page has finished sliding in. True once the push's
 * `transitionEnd` arrives, or after `PUSH_SETTLE_FALLBACK_MS` if none does;
 * it never turns false again.
 *
 * For holding back a heavy mount that would otherwise land mid-slide: the
 * native stack runs its animation on the UI thread, and mounting a large view
 * tree there stalls the slide, which then jumps to its end.
 */
export function usePushSettled(): boolean {
	const navigation = useNavigation<NativeStackNavigationProp<Record<string, object | undefined>>>();
	const [settled, setSettled] = useState(false);
	useEffect(() => {
		if (settled) return;
		const timer = setTimeout(() => setSettled(true), PUSH_SETTLE_FALLBACK_MS);
		const off = navigation.addListener("transitionEnd", (event) => {
			if (!event.data.closing) setSettled(true);
		});
		return () => {
			clearTimeout(timer);
			off();
		};
	}, [navigation, settled]);
	return settled;
}
