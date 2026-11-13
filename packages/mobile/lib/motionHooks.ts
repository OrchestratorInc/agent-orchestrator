import { useEffect, useRef } from "react";
import { Animated, Easing } from "react-native";
import { duration, easingCurve } from "./tokens";
import { useReducedMotion } from "./useReducedMotion";

/**
 * The React Native half of the motion system.
 *
 * Kept apart from `motion.ts` on purpose: that module holds the durations and
 * the rules, and stays free of React Native imports so it can be unit-tested
 * under Node — the same split `pushStatus.ts` uses against the screens that read
 * it. Everything here needs `Animated`, so it cannot live there.
 */

/** The standard curve, as an `Easing` function for `Animated.timing`. */
export const easing = {
	standard: Easing.bezier(easingCurve.standard[0], easingCurve.standard[1], easingCurve.standard[2], easingCurve.standard[3]),
	decelerate: Easing.bezier(easingCurve.decelerate[0], easingCurve.decelerate[1], easingCurve.decelerate[2], easingCurve.decelerate[3]),
	accelerate: Easing.bezier(easingCurve.accelerate[0], easingCurve.accelerate[1], easingCurve.accelerate[2], easingCurve.accelerate[3]),
};

/** True when the platform asks for motion to be kept to a minimum. */
export function useReduceMotion(): boolean {
	return useReducedMotion();
}

/**
 * A quiet entrance for content that appears once — an empty state, a sheet body.
 * Skipped entirely under Reduce Motion, and a no-op for content already on screen
 * when it mounts, so lists never animate while scrolling.
 */
export function useEnterTransition(offset = 4) {
	const progress = useRef(new Animated.Value(0)).current;
	const reduced = useReduceMotion();

	useEffect(() => {
		if (reduced) {
			progress.setValue(1);
			return;
		}
		Animated.timing(progress, {
			toValue: 1,
			duration: duration.slow,
			easing: easing.decelerate,
			useNativeDriver: true,
		}).start();
	}, [progress, reduced]);

	return {
		opacity: progress,
		transform: [
			{
				translateY: progress.interpolate({ inputRange: [0, 1], outputRange: [offset, 0] }),
			},
		],
	};
}
