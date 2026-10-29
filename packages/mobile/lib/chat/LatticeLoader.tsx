import { useEffect } from "react";
import { PixelRatio, StyleSheet, View, type StyleProp, type ViewStyle } from "react-native";
import Animated, {
	cancelAnimation,
	Easing,
	useAnimatedStyle,
	useSharedValue,
	withRepeat,
	withTiming,
	type SharedValue,
} from "react-native-reanimated";

import { useReducedMotion } from "../useReducedMotion";
import {
	LATTICE_INNER_OPACITY,
	LATTICE_LAP_MS,
	LATTICE_SIDE,
	LATTICE_STILL_OPACITY,
	LATTICE_TRAIL,
	latticeCellOrigin,
	latticeTrailOpacity,
} from "./latticeTrail";

// The desktop's 2.5px dots on a 1px gap, snapped to whole device pixels so the
// dots stay crisp at 3x as well as 2x.
const DOT = PixelRatio.roundToNearestPixel(2.5);
const GAP = PixelRatio.roundToNearestPixel(1);
const SIZE = DOT * LATTICE_SIDE + GAP * (LATTICE_SIDE - 1);
/** Each dot's place, fixed up front so no density can reflow the lattice (see latticeCellOrigin). */
const ORIGINS = Array.from({ length: LATTICE_SIDE * LATTICE_SIDE }, (_, cell) => latticeCellOrigin(cell, DOT, GAP));

/**
 * The live turn's working mark, matching the desktop's: a 4×4 lattice of small
 * dots with a short bright trail running clockwise round the outer twelve while
 * the inner four rest (timing in latticeTrail.ts).
 *
 * One shared lap drives every dot on the UI thread, each reading it a twelfth of
 * a lap behind the last, so the trail moves from the first frame and keeps
 * moving while the JS thread is busy streaming the reply. Decorative: the label
 * beside it says what is happening. Under Reduce Motion the lattice holds still.
 */
export function LatticeLoader({ color }: { color: string }) {
	const reduceMotion = useReducedMotion();
	const lap = useSharedValue(0);
	useEffect(() => {
		if (reduceMotion) return;
		lap.value = 0;
		lap.value = withRepeat(withTiming(1, { duration: LATTICE_LAP_MS, easing: Easing.linear }), -1);
		return () => cancelAnimation(lap);
	}, [lap, reduceMotion]);
	return (
		<View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={styles.lattice} testID="lattice-loader">
			{Array.from({ length: LATTICE_SIDE * LATTICE_SIDE }, (_, cell) => {
				const step = LATTICE_TRAIL.indexOf(cell as (typeof LATTICE_TRAIL)[number]);
				const dot = [styles.dot, ORIGINS[cell], { backgroundColor: color }];
				if (step < 0) return <View key={cell} style={[dot, { opacity: LATTICE_INNER_OPACITY }]} />;
				if (reduceMotion) return <View key={cell} style={[dot, { opacity: LATTICE_STILL_OPACITY }]} />;
				return <TrailDot key={cell} lap={lap} offset={step / LATTICE_TRAIL.length} style={dot} />;
			})}
		</View>
	);
}

function TrailDot({ lap, offset, style }: { lap: SharedValue<number>; offset: number; style: StyleProp<ViewStyle> }) {
	const trail = useAnimatedStyle(() => ({ opacity: latticeTrailOpacity(lap.value - offset) }));
	return <Animated.View style={[style, trail]} />;
}

const styles = StyleSheet.create({
	lattice: { width: SIZE, height: SIZE },
	dot: { position: "absolute", width: DOT, height: DOT, borderRadius: DOT / 2 },
});
