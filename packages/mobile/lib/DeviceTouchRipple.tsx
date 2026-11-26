import { useEffect, useRef } from "react";
import { Animated, Easing, StyleSheet } from "react-native";
import { useTheme } from "./ThemeProvider";

export type RipplePoint = { x: number; y: number; id: number };

const SIZE = 56;

/**
 * A ring that opens where a finger lands on the device. The picture only shows
 * a tap once the next frame arrives; the ring answers the touch the instant it
 * happens, which is most of what makes the screen feel under the finger.
 */
export function DeviceTouchRipple({ point }: { point: RipplePoint | null }) {
	const t = useTheme();
	const progress = useRef(new Animated.Value(1)).current;
	useEffect(() => {
		if (!point) return;
		progress.setValue(0);
		Animated.timing(progress, { toValue: 1, duration: 380, easing: Easing.out(Easing.quad), useNativeDriver: true }).start();
	}, [point, progress]);
	if (!point) return null;
	return <Animated.View
		pointerEvents="none"
		style={[styles.ring, {
			left: point.x - SIZE / 2,
			top: point.y - SIZE / 2,
			borderColor: t.accent,
			backgroundColor: t.accent,
			opacity: progress.interpolate({ inputRange: [0, 1], outputRange: [0.45, 0] }),
			transform: [{ scale: progress.interpolate({ inputRange: [0, 1], outputRange: [0.35, 1.25] }) }],
		}]}
	/>;
}

const styles = StyleSheet.create({
	ring: { position: "absolute", width: SIZE, height: SIZE, borderRadius: SIZE / 2, borderWidth: 2 },
});
