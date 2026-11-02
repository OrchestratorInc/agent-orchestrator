import { useEffect, useRef, useState } from "react";
import { Animated, Easing, StyleSheet, View, type StyleProp, type ViewStyle } from "react-native";
import Svg, { Defs, LinearGradient, Rect, Stop } from "react-native-svg";
import { SHIMMER_MS, SHIMMER_REST_MS } from "../motion";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { radius, space } from "../tokens";
import { useReducedMotion } from "../useReducedMotion";
import { renderSkeletonLines } from "./renderFrameModel";

/**
 * Where a page will be: a block for its content and, when the frame is tall
 * enough, two caption lines, with a soft band sweeping across while it loads.
 * Reduce Motion leaves the blocks still; their shape alone says "loading".
 * With a delay, it appears only if loading is still going then, so a page that
 * comes from cache does not flash a skeleton on its way in.
 */
export function RenderSkeleton({ height, label, style, delayMs = 0 }: { height: number; label: string; style?: StyleProp<ViewStyle>; delayMs?: number }) {
	const styles = useThemedStyles(makeStyles);
	const reduceMotion = useReducedMotion();
	const [width, setWidth] = useState(0);
	const sweep = useRef(new Animated.Value(0)).current;
	const [shown, setShown] = useState(delayMs <= 0);
	useEffect(() => {
		if (shown) return;
		const timer = setTimeout(() => setShown(true), delayMs);
		return () => clearTimeout(timer);
	}, [shown, delayMs]);
	const animate = shown && !reduceMotion && width > 0;
	useEffect(() => {
		if (!animate) return;
		const loop = Animated.loop(Animated.sequence([
			Animated.timing(sweep, { toValue: 1, duration: SHIMMER_MS, easing: Easing.linear, useNativeDriver: true }),
			Animated.delay(SHIMMER_REST_MS),
		]));
		loop.start();
		return () => {
			loop.stop();
			sweep.setValue(0);
		};
	}, [animate, sweep]);
	if (!shown) return null;
	const lines = renderSkeletonLines(height);
	// Every block starts at the frame's left edge, so one translation lines the band up across them.
	const band = animate ? <ShimmerBand width={width} sweep={sweep} /> : null;
	return <View
		accessible
		accessibilityRole="progressbar"
		accessibilityLabel={`Loading ${label}`}
		onLayout={(event) => setWidth(event.nativeEvent.layout.width)}
		style={[styles.root, style]}
	>
		<View style={[styles.block, styles.content]}>{band}</View>
		{lines.map((fraction, index) => <View key={index} style={[styles.block, styles.line, { width: `${fraction * 100}%` }]}>{band}</View>)}
	</View>;
}

function ShimmerBand({ width, sweep }: { width: number; sweep: Animated.Value }) {
	const t = useTheme();
	const { scheme } = useThemeState();
	// A highlight, so lighter than the block in both themes: the light surface
	// colour on a light block, a trace of the text colour on a dark one.
	const highlight = scheme === "light" ? { color: t.bgElevated, opacity: 0.9 } : { color: t.textPrimary, opacity: 0.08 };
	const bandWidth = Math.max(120, width * 0.6);
	const translateX = sweep.interpolate({ inputRange: [0, 1], outputRange: [-bandWidth, width] });
	return <Animated.View pointerEvents="none" style={{ position: "absolute", top: 0, bottom: 0, left: 0, width: bandWidth, transform: [{ translateX }] }}>
		<Svg width={bandWidth} height="100%">
			<Defs>
				<LinearGradient id="shimmer" x1="0" y1="0" x2="1" y2="0">
					<Stop offset="0" stopColor={highlight.color} stopOpacity={0} />
					<Stop offset="0.5" stopColor={highlight.color} stopOpacity={highlight.opacity} />
					<Stop offset="1" stopColor={highlight.color} stopOpacity={0} />
				</LinearGradient>
			</Defs>
			<Rect x="0" y="0" width={bandWidth} height="100%" fill="url(#shimmer)" />
		</Svg>
	</Animated.View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	root: { flex: 1, gap: space.sm, paddingVertical: space.xs },
	block: { overflow: "hidden", borderRadius: radius.md, borderCurve: "continuous", backgroundColor: t.bgSubtle },
	content: { flex: 1 },
	line: { height: 12, borderRadius: radius.xs },
});
