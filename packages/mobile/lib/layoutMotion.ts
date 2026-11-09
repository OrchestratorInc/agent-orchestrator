import { Platform } from "react-native";
import { LinearTransition } from "react-native-reanimated";
import { LAYOUT_MS } from "./motion";

/**
 * Material 3's standard spatial spring (damping ratio 0.9, stiffness 700), the
 * one Compose moves its own components with, as physical damping for a unit
 * mass: 2 × 0.9 × √700.
 */
export const MATERIAL_SPATIAL_SPRING = { damping: 48, stiffness: 700, mass: 1 } as const;

/**
 * A block moving to its new place when something above it opens or closes.
 *
 * Android rides Material's spatial spring, so the move keeps the speed it
 * started with and settles the way the Compose controls beside it do, instead
 * of a fixed-length curve that starts and stops on the clock. iOS keeps its
 * timed curve.
 */
export function layoutTransition() {
	if (Platform.OS !== "android") return LinearTransition.duration(LAYOUT_MS);
	const { damping, stiffness, mass } = MATERIAL_SPATIAL_SPRING;
	return LinearTransition.springify().damping(damping).stiffness(stiffness).mass(mass);
}
