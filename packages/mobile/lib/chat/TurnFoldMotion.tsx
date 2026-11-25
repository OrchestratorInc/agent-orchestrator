import { useEffect, type ReactNode } from "react";
import { Platform, type StyleProp, type ViewStyle } from "react-native";
import Animated, {
	FadeIn,
	useAnimatedStyle,
	useSharedValue,
	withTiming,
} from "react-native-reanimated";

import { AppSymbol } from "../AppSymbol";
import { CROSSFADE_MS, LAYOUT_MS } from "../motion";
import { iconSize } from "../tokens";
import { useReducedMotion } from "../useReducedMotion";

/**
 * Motion for a turn's folds (the Worked for row, a plan) inside the
 * conversation list: opacity and transform only.
 *
 * Nothing here takes a Reanimated `layout` transition. Reanimated 4.5 keeps a
 * moved view at its old frame and steps it to the new one on the UI thread;
 * when that thread stalls past the animation's length (a settling turn mounts
 * a lot of text at once), the only frame is the last one, and 4.5 drops it.
 * The view then stays where it was while everything around it moves, so a
 * settled turn's reply was drawn over the next turn. Rows jump to their new
 * place instead. Exit fades go too: with neighbours jumping, a fading row
 * would sit under whatever took its place.
 */

/** A fold's body, fading in as it opens. Plain under Reduce Motion. */
export function FoldBody({ children, style }: { children: ReactNode; style?: StyleProp<ViewStyle> }) {
	const reduceMotion = useReducedMotion();
	if (reduceMotion) return <Animated.View style={style}>{children}</Animated.View>;
	return <Animated.View entering={FadeIn.duration(CROSSFADE_MS)} style={style}>{children}</Animated.View>;
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
