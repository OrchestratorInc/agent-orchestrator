import { useRef } from "react";

/**
 * Handlers for a composer trigger that acts as a finger lands rather than as
 * it lifts: a tap is held for a tenth of a second or so, and waiting for the
 * lift read as lag. `act` runs once per physical touch, and still on the
 * activations that send no touch (a screen reader, a keyboard), which reach
 * `onPress` alone.
 *
 * - A finger that slides off and back on fires `onPressIn` again; the touch
 *   has already acted, so it is skipped.
 * - The same touch's `onPress`, as the finger lifts, is skipped.
 * - A touch released outside the trigger gets no `onPress`. Its end clears
 *   the touch, so the next screen reader activation is not swallowed. The
 *   clear waits a tick, since the touch's end and its `onPress` arrive in the
 *   same dispatch in either order.
 *
 * Spread the result on a Pressable.
 */
export function useTouchDownPress(act: () => void) {
	const touch = useRef({ active: false, pending: false });
	// The clear applies only to the touch that scheduled it: a touch that
	// lands before the tick has its own state, which the clear must keep.
	const end = () => {
		const ended = touch.current;
		setTimeout(() => { if (touch.current === ended) touch.current = { active: false, pending: false }; }, 0);
	};
	return {
		onPressIn: () => {
			if (touch.current.active) return;
			touch.current = { active: true, pending: true };
			act();
		},
		onPress: () => {
			const { pending } = touch.current;
			touch.current = { active: false, pending: false };
			if (pending) return;
			act();
		},
		onTouchEnd: end,
		onTouchCancel: end,
	};
}
