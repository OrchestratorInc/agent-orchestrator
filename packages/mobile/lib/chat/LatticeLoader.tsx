import { memo, useContext, useEffect } from "react";
import { Animated, PixelRatio, StyleSheet, View } from "react-native";

import { useReducedMotion } from "../useReducedMotion";
import { LatticeActiveContext } from "./latticeActivity";
import {
	LATTICE_COMET,
	LATTICE_EDGE_OPACITY,
	LATTICE_INNER_OPACITY,
	LATTICE_SIDE,
	LATTICE_STILL_OPACITY,
	LATTICE_TRAIL,
	createLatticeClock,
	latticeCellOrigin,
	latticeCometPath,
} from "./latticeTrail";

// The desktop's 2.5px dots on a 1px gap, snapped to whole device pixels so the
// dots stay crisp at 3x as well as 2x.
const DOT = PixelRatio.roundToNearestPixel(2.5);
const GAP = PixelRatio.roundToNearestPixel(1);
const SIZE = DOT * LATTICE_SIDE + GAP * (LATTICE_SIDE - 1);
/** Each dot's place, fixed up front so no density can reflow the lattice (see latticeCellOrigin). */
const ORIGINS = Array.from({ length: LATTICE_SIDE * LATTICE_SIDE }, (_, cell) => latticeCellOrigin(cell, DOT, GAP));
const EDGE = new Set<number>(LATTICE_TRAIL);

/**
 * Each comet dot's path, and the opacity that, drawn over an edge dot at rest,
 * shows the comet's level.
 */
const COMET = LATTICE_COMET.map((level, behind) => ({
	...latticeCometPath(behind, DOT, GAP),
	opacity: (level - LATTICE_EDGE_OPACITY) / (1 - LATTICE_EDGE_OPACITY),
}));

/**
 * The step every lattice's comet stands on. Made native at once, so a step is
 * one value sent to the platform, which moves every comet dot on screen
 * without JavaScript or a React render.
 */
const step = new Animated.Value(0);
Animated.timing(step, { toValue: 0, duration: 0, useNativeDriver: true }).start();
const holdClock = createLatticeClock((next) => step.setValue(next));

/** Each comet dot's place at each step. */
const COMET_STYLES = COMET.map(({ inputRange, left, top, opacity }) => ({
	opacity,
	transform: [
		{ translateX: step.interpolate({ inputRange, outputRange: left }) },
		{ translateY: step.interpolate({ inputRange, outputRange: top }) },
	],
}));
/** Where the comet rests while its lattice holds still (off screen). */
const COMET_RESTING = COMET.map(({ left, top, opacity }) => ({ opacity, transform: [{ translateX: left[0]! }, { translateY: top[0]! }] }));

/**
 * The live turn's working mark, after the desktop's: a 4×4 lattice of small
 * dots with a short bright trail running clockwise round the outer twelve while
 * the inner four rest (timing in latticeTrail.ts).
 *
 * The sixteen dots hold still; the trail is three more dots, a comet stepping
 * one edge dot on every twelfth of a lap. One clock steps every lattice on
 * screen at once, ten times a second, through a value the native animation
 * driver applies: a step is one value sent to the platform, and between steps
 * nothing runs on any thread. A desktop-faithful trail, twelve dots each fading
 * every frame, held a board of working rows' UI thread three times as busy as
 * the spinner the lattice replaced. Decorative: the label beside it says what
 * is happening. Under Reduce Motion, and off screen, the lattice holds still.
 */
export const LatticeLoader = memo(function LatticeLoader({ color }: { color: string }) {
	const reduceMotion = useReducedMotion();
	// Off screen (a board row scrolled out of view) the comet rests.
	const moving = useContext(LatticeActiveContext) && !reduceMotion;
	useEffect(() => (moving ? holdClock() : undefined), [moving]);
	const fill = { backgroundColor: color };
	const comet = moving ? COMET_STYLES : COMET_RESTING;
	return (
		<View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={styles.lattice} testID="lattice-loader">
			{ORIGINS.map((origin, cell) => (
				<View key={cell} style={[styles.dot, origin, fill, { opacity: !EDGE.has(cell) ? LATTICE_INNER_OPACITY : reduceMotion ? LATTICE_STILL_OPACITY : LATTICE_EDGE_OPACITY }]} />
			))}
			{reduceMotion ? null : comet.map((style, behind) => <Animated.View key={`comet-${behind}`} style={[styles.dot, fill, style]} />)}
		</View>
	);
});

const styles = StyleSheet.create({
	lattice: { width: SIZE, height: SIZE },
	dot: { position: "absolute", left: 0, top: 0, width: DOT, height: DOT, borderRadius: DOT / 2 },
});
