import { useEffect, type ReactNode } from "react";
import { Platform, type StyleProp, type ViewStyle } from "react-native";
import Animated, {
	FadeIn,
	FadeOut,
	LinearTransition,
	useAnimatedStyle,
	useSharedValue,
	withTiming,
} from "react-native-reanimated";

import { AppSymbol } from "../AppSymbol";
import { CROSSFADE_MS, LAYOUT_MS, ROW_ENTER_MS } from "../motion";
import { iconSize } from "../tokens";
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

// Each platform's disclosure: iOS's chevron points forward and turns down, as a
// DisclosureGroup's does; Material's expand arrow points down and turns up.
const FOLD_GLYPH = Platform.OS === "android" ? "chevronDown" : "chevronForward";
const FOLD_TURN = Platform.OS === "android" ? 180 : 90;

/** The disclosure chevron of a fold, turning as the fold opens. */
export function FoldChevron({ open, color, size = iconSize.sm }: { open: boolean; color: string; size?: number }) {
	const reduceMotion = useReducedMotion();
	const progress = useSharedValue(open ? 1 : 0);
	useEffect(() => {
		progress.value = withTiming(open ? 1 : 0, { duration: reduceMotion ? 0 : LAYOUT_MS });
	}, [open, progress, reduceMotion]);
	const style = useAnimatedStyle(() => ({ transform: [{ rotate: `${progress.value * FOLD_TURN}deg` }] }));
	return (
		<Animated.View style={style}>
			<AppSymbol name={FOLD_GLYPH} size={size} color={color} />
		</Animated.View>
	);
}
