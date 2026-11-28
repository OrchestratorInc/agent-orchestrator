import { memo, useContext, useEffect, useMemo, useState } from "react";
import { Animated, Easing, PixelRatio, StyleSheet, View } from "react-native";

import { useReducedMotion } from "../useReducedMotion";
import { LatticeActiveContext } from "./latticeActivity";
import {
	LATTICE_COMET,
	LATTICE_EDGE_OPACITY,
	LATTICE_INNER_OPACITY,
	LATTICE_LAP_MS,
	LATTICE_SIDE,
	LATTICE_STILL_OPACITY,
	LATTICE_TRAIL,
	latticeCellOrigin,
	latticeCometPath,
} from "./latticeTrail";

// The desktop's 2.5px dots on a 1px gap, snapped to whole device pixels so the
// dots stay crisp at 3x as well as 2x (as LatticeLoader).
const DOT = PixelRatio.roundToNearestPixel(2.5);
const GAP = PixelRatio.roundToNearestPixel(1);
const SIZE = DOT * LATTICE_SIDE + GAP * (LATTICE_SIDE - 1);
/** Each dot's place, fixed up front so no density can reflow the lattice (see latticeCellOrigin). */
const ORIGINS = Array.from({ length: LATTICE_SIDE * LATTICE_SIDE }, (_, cell) => latticeCellOrigin(cell, DOT, GAP));
const EDGE = new Set<number>(LATTICE_TRAIL);

/**
 * Each comet dot's stepped path, and the opacity that, drawn over an edge dot
 * at rest, shows the comet's level.
 */
const COMET = LATTICE_COMET.map((level, behind) => ({
	...latticeCometPath(behind, DOT, GAP),
	opacity: (level - LATTICE_EDGE_OPACITY) / (1 - LATTICE_EDGE_OPACITY),
}));

/**
 * The Workers board's working mark: the chat's lattice (LatticeLoader), drawn
 * cheaply enough for a board of working rows.
 *
 * The sixteen dots hold still; the trail is three more dots, a comet stepping
 * one edge dot on every twelfth of a lap. Each lattice owns one value that a
 * native-driven loop runs from 0 to 12 over a lap, and a stepped interpolation
 * holds each comet dot on its edge dot for the whole step: the loop runs on the
 * UI thread without JavaScript, so a busy JS thread cannot stall it, and between
 * steps no dot moves. A desktop-faithful trail, twelve dots each fading every
 * frame, held a board of working rows' UI thread three times as busy as the
 * spinner the lattice replaced.
 *
 * The value and its interpolations belong to this lattice alone, so a row
 * leaving and coming back, or the board remounting, builds them afresh. Off
 * screen or out of focus (LatticeActiveContext) the loop stops and the comet
 * rests at the lap's start. Decorative: the label beside it says what is happening. Under Reduce
 * Motion the lattice holds still.
 *
 * Memoized: the row around it re-renders every second for its "12s" label.
 */
export const LatticeCometLoader = memo(function LatticeCometLoader({ color }: { color: string }) {
	const reduceMotion = useReducedMotion();
	const moving = useContext(LatticeActiveContext) && !reduceMotion;
	const [step] = useState(() => new Animated.Value(0));
	const cometStyles = useMemo(() => COMET.map(({ inputRange, left, top, opacity }) => ({
		opacity,
		transform: [
			{ translateX: step.interpolate({ inputRange, outputRange: left }) },
			{ translateY: step.interpolate({ inputRange, outputRange: top }) },
		],
	})), [step]);
	useEffect(() => {
		if (!moving) return;
		// A single native-driven timing under an endless loop loops natively too,
		// so no frame of it waits on JavaScript.
		const lap = Animated.loop(Animated.timing(step, { toValue: LATTICE_TRAIL.length, duration: LATTICE_LAP_MS, easing: Easing.linear, useNativeDriver: true }));
		lap.start();
		return () => {
			lap.stop();
			// Rest at the lap's start, and start the next lap from there.
			step.setValue(0);
		};
	}, [moving, step]);
	const fill = { backgroundColor: color };
	return (
		<View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={styles.lattice} testID="lattice-loader">
			{ORIGINS.map((origin, cell) => (
				<View key={cell} style={[styles.dot, origin, fill, { opacity: !EDGE.has(cell) ? LATTICE_INNER_OPACITY : reduceMotion ? LATTICE_STILL_OPACITY : LATTICE_EDGE_OPACITY }]} />
			))}
			{reduceMotion ? null : cometStyles.map((style, behind) => <Animated.View key={`comet-${behind}`} style={[styles.dot, fill, style]} />)}
		</View>
	);
});

const styles = StyleSheet.create({
	lattice: { width: SIZE, height: SIZE },
	dot: { position: "absolute", left: 0, top: 0, width: DOT, height: DOT, borderRadius: DOT / 2 },
});
