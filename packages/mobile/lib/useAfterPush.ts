import { useEffect, useState } from "react";

/**
 * False for a page's first render, then true from the next frame on.
 *
 * The native stack starts sliding a page in once its first render commits, so
 * everything that render draws is time between the tap and anything moving.
 * A page draws its frame (header, composer) first and its heavy body (a
 * transcript) one frame later: the slide starts at once and the body is ready
 * long before it ends.
 *
 * Measured on a mid-range Android phone, production bundle: the chat page
 * committed 40 ms after the tap instead of stalling 180–440 ms, with fewer
 * janky frames over the push. Waiting for the whole slide (transitionEnd)
 * instead delayed the transcript by ~600 ms and was janky as it popped in.
 */
export function useAfterPush(): boolean {
	const [ready, setReady] = useState(false);
	useEffect(() => {
		if (ready) return;
		const show = () => setReady(true);
		// React Native always has requestAnimationFrame; a test environment may not.
		if (typeof requestAnimationFrame !== "function") {
			const timer = setTimeout(show, 0);
			return () => clearTimeout(timer);
		}
		const frame = requestAnimationFrame(show);
		return () => cancelAnimationFrame(frame);
	}, [ready]);
	return ready;
}
