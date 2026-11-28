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
 * - A touch released outside the trigger gets no `onPress`. Its end marks
 *   the touch over at once, so the next finger acts even if it lands within
 *   the same tick. The end also clears the skip on `onPress`, so the next
 *   screen reader activation is not swallowed; that clear waits a tick,
 *   since the touch's end and its `onPress` arrive in the same dispatch in
 *   either order.
 *
 * Spread the result on a Pressable.
 */
export function useTouchDownPress(act: () => void) {
	const touch = useRef({ active: false, pending: false, id: 0 });
	// The deferred clear applies only to the touch that scheduled it: a touch
	// that lands before the tick has its own skip, which the clear must keep.
	const end = () => {
		const state = touch.current;
		state.active = false;
		const ended = state.id;
		setTimeout(() => { if (state.id === ended) state.pending = false; }, 0);
	};
	return {
		onPressIn: () => {
			const state = touch.current;
			if (state.active) return;
			state.active = true;
			state.pending = true;
			state.id += 1;
			act();
		},
		onPress: () => {
			const state = touch.current;
			const { pending } = state;
			state.active = false;
			state.pending = false;
			if (pending) return;
			act();
		},
		onTouchEnd: end,
		onTouchCancel: end,
	};
}
