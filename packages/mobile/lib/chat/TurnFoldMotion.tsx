import { useEffect, type ReactNode } from "react";
import type { StyleProp, ViewStyle } from "react-native";
import Animated, {
	FadeIn,
	FadeOut,
	LinearTransition,
	useAnimatedStyle,
	useSharedValue,
	withTiming,
} from "react-native-reanimated";

import { Feather } from "../icons";
import { CROSSFADE_MS, LAYOUT_MS, ROW_ENTER_MS } from "../motion";
import { useReducedMotion } from "../useReducedMotion";

/**
 * Motion for a turn's Working / Worked status row, matching the desktop handoff.
 *
 * Opacity, transform and layout only, so it stays smooth while the list is busy.
 * Every piece returns its children unwrapped under Reduce Motion: a zero-duration
 * layout animation still schedules work every frame, and the setting asks for no
 * movement rather than instant movement.
 */

/** Rows that sit below a fold slide to their new place instead of jumping when it opens or closes. */
export function FoldLayout({ children, style }: { children: ReactNode; style?: StyleProp<ViewStyle> }) {
	const reduceMotion = useReducedMotion();
	if (reduceMotion) return <Animated.View style={style}>{children}</Animated.View>;
	return <Animated.View layout={LinearTransition.duration(LAYOUT_MS)} style={style}>{children}</Animated.View>;
}

/** The folded work, fading in as it opens and out as it closes. */
export function FoldBody({ children, style }: { children: ReactNode; style?: StyleProp<ViewStyle> }) {
	const reduceMotion = useReducedMotion();
	if (reduceMotion) return <Animated.View style={style}>{children}</Animated.View>;
	return (
		<Animated.View
			layout={LinearTransition.duration(LAYOUT_MS)}
			entering={FadeIn.duration(CROSSFADE_MS)}
			exiting={FadeOut.duration(ROW_ENTER_MS)}
			style={style}
		>
			{children}
		</Animated.View>
	);
}

/**
 * The live row's spinner leaves with a fade while the label slides into the
 * space it vacated (see StatusLabel), landing where the settled row puts it.
 */
export function StatusSpinner({ children }: { children: ReactNode }) {
	const reduceMotion = useReducedMotion();
	if (reduceMotion) return <>{children}</>;
	return <Animated.View exiting={FadeOut.duration(ROW_ENTER_MS)}>{children}</Animated.View>;
}

export function StatusLabel({ children }: { children: ReactNode }) {
	const reduceMotion = useReducedMotion();
	if (reduceMotion) return <>{children}</>;
	return <Animated.View layout={LinearTransition.duration(LAYOUT_MS)}>{children}</Animated.View>;
}

/** A right-pointing chevron that turns down as the fold opens. */
export function FoldChevron({ open, color }: { open: boolean; color: string }) {
	const reduceMotion = useReducedMotion();
	const progress = useSharedValue(open ? 1 : 0);
	useEffect(() => {
		progress.value = withTiming(open ? 1 : 0, { duration: reduceMotion ? 0 : LAYOUT_MS });
	}, [open, progress, reduceMotion]);
	const style = useAnimatedStyle(() => ({ transform: [{ rotate: `${progress.value * 90}deg` }] }));
	return (
		<Animated.View style={style}>
			<Feather name="chevron-right" size={12} color={color} />
		</Animated.View>
	);
}
