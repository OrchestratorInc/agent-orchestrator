import { useEffect } from "react";
import { StyleSheet, View } from "react-native";
import Animated, {
	cancelAnimation,
	Easing,
	useAnimatedStyle,
	useSharedValue,
	withRepeat,
	withTiming,
	type SharedValue,
} from "react-native-reanimated";
import Svg, { Path, SvgXml } from "react-native-svg";

import { RUTO_BATON, RUTO_BODY, RUTO_DEFS } from "./rutoArt";
import { batonAt, eyesAt, glowAt, hopAt, LOOP_MS, sparkleAt, SPARKLE_COUNT } from "./updateMascotPose";
import { useReducedMotion } from "./useReducedMotion";

// Ruto from the brand source (rutoArt.ts, copied from assets/brand/ruto.svg), in
// two layers so the baton can move on its own: the baton behind, the body over
// it, as the source draws them. Coordinates are the brand file's 1024 grid. The
// frame is cropped to Ruto and padded on the top and right for the sparkles.
const VB = { x: 170, y: 110, w: 760, h: 690 };
const VIEWBOX = `${VB.x} ${VB.y} ${VB.w} ${VB.h}`;
const svg = (inner: string) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${VIEWBOX}">${inner}</svg>`;
const BODY_XML = svg(RUTO_DEFS + RUTO_BODY);
const BATON_XML = svg(RUTO_BATON);

// The baton swings around the middle of its end inside the hand — where the body
// draws that end's sunk shading — so the shading stays put as it moves.
const PIVOT = { x: 779, y: 421 };
const TIP = { x: 835.5, y: 236 };
const FEET = { x: 493, y: 763 };
// Eye holes with their sunken walls. A blink drops a face-coloured lid over each
// from the top; its bottom edge, pupil-dark, is the closed eye. Colours sampled
// from the face gradient just around each eye.
const EYES = [
	{ x: 371, color: "#56C0FB" },
	{ x: 558, color: "#46B9F9" },
] as const;
const EYE_Y = 427;
const EYE_W = 63;
const EYE_H = 68;
const LID_EDGE = 7;
const PUPIL = "#111213";
const HOP = 45;
// The choreography's offsets are in its own units; this many grid units each.
const UNIT = 1.6;

const C = { glow: "#FFE58A", spark: ["#FFE08A", "#CFE7FF"] } as const;
const STAR = "M0 -10 C1.6 -1.6 1.6 -1.6 10 0 C1.6 1.6 1.6 1.6 0 10 C-1.6 1.6 -1.6 1.6 -10 0 C-1.6 -1.6 -1.6 -1.6 0 -10 Z";

export function UpdateMascot({ size = 220, shadowColor }: { size?: number; shadowColor: string }) {
	const reduceMotion = useReducedMotion();
	const clock = useSharedValue(0);

	useEffect(() => {
		if (reduceMotion) {
			cancelAnimation(clock);
			clock.value = 0;
			return;
		}
		clock.value = 0;
		clock.value = withRepeat(withTiming(LOOP_MS, { duration: LOOP_MS, easing: Easing.linear }), -1, false);
		return () => cancelAnimation(clock);
	}, [reduceMotion, clock]);

	const k = size / VB.w;
	const px = (x: number) => (x - VB.x) * k;
	const py = (y: number) => (y - VB.y) * k;
	const height = VB.h * k;

	const hopStyle = useAnimatedStyle(() => {
		const { lift, squash } = hopAt(clock.value);
		return {
			transform: [{ translateY: -lift * HOP * k }, { scaleX: 1 + 0.05 * squash }, { scaleY: 1 - 0.07 * squash }],
		};
	});
	const shadowStyle = useAnimatedStyle(() => {
		const { lift } = hopAt(clock.value);
		return { opacity: 1 - 0.55 * lift, transform: [{ scaleX: 1 - 0.3 * lift }] };
	});
	const batonStyle = useAnimatedStyle(() => ({ transform: [{ rotate: `${batonAt(clock.value)}deg` }] }));
	const lidStyle = useAnimatedStyle(() => ({ transform: [{ scaleY: 1 - eyesAt(clock.value).open }] }));
	const glowStyle = useAnimatedStyle(() => {
		const g = glowAt(clock.value);
		return { opacity: 0.18 + 0.3 * g, transform: [{ scale: 0.75 + 0.45 * g }] };
	});

	const glowSize = 80 * k;

	return (
		<View style={{ width: size, height }} accessible accessibilityRole="image" accessibilityLabel="Orchestrator mascot">
			<Animated.View
				style={[
					styles.abs,
					{
						left: px(FEET.x - 200),
						top: py(FEET.y + 6),
						width: 400 * k,
						height: 26 * k,
						borderRadius: 13 * k,
						backgroundColor: shadowColor,
					},
					shadowStyle,
				]}
			/>
			<Animated.View style={[StyleSheet.absoluteFill, { transformOrigin: [px(FEET.x), py(FEET.y), 0] }, hopStyle]}>
				<Animated.View style={[StyleSheet.absoluteFill, { transformOrigin: [px(PIVOT.x), py(PIVOT.y), 0] }, batonStyle]}>
					<SvgXml xml={BATON_XML} width={size} height={height} />
					<Animated.View
						style={[
							styles.abs,
							{
								left: px(TIP.x) - glowSize / 2,
								top: py(TIP.y) - glowSize / 2,
								width: glowSize,
								height: glowSize,
								borderRadius: glowSize / 2,
								backgroundColor: C.glow,
							},
							glowStyle,
						]}
					/>
					{reduceMotion
						? null
						: Array.from({ length: SPARKLE_COUNT }, (_, i) => (
								<Sparkle key={i} index={i} clock={clock} k={k} left={px(TIP.x)} top={py(TIP.y)} />
							))}
				</Animated.View>
				<SvgXml xml={BODY_XML} width={size} height={height} style={StyleSheet.absoluteFill} />
				{EYES.map((eye) => (
					<Animated.View
						key={eye.x}
						style={[
							styles.abs,
							styles.lid,
							{ left: px(eye.x), top: py(EYE_Y), width: EYE_W * k, height: EYE_H * k, backgroundColor: eye.color },
							lidStyle,
						]}
					>
						<View style={{ height: LID_EDGE * k, backgroundColor: PUPIL }} />
					</Animated.View>
				))}
			</Animated.View>
		</View>
	);
}

function Sparkle({
	index,
	clock,
	k,
	left,
	top,
}: {
	index: number;
	clock: SharedValue<number>;
	k: number;
	left: number;
	top: number;
}) {
	const side = (index % 2 === 0 ? 40 : 28) * k;
	const style = useAnimatedStyle(() => {
		const s = sparkleAt(clock.value, index);
		return {
			opacity: s.opacity,
			transform: [
				{ translateX: s.x * UNIT * k },
				{ translateY: s.y * UNIT * k },
				{ rotate: `${s.rotate}deg` },
				{ scale: s.scale },
			],
		};
	});
	return (
		<Animated.View style={[styles.abs, { left: left - side / 2, top: top - side / 2, width: side, height: side }, style]}>
			<Svg width={side} height={side} viewBox="-10 -10 20 20">
				<Path d={STAR} fill={C.spark[index % 2]} />
			</Svg>
		</Animated.View>
	);
}

const styles = StyleSheet.create({
	abs: { position: "absolute" },
	// Closes from the top down; the pupil-dark edge rides at its bottom.
	lid: { transformOrigin: "top", justifyContent: "flex-end" },
});
