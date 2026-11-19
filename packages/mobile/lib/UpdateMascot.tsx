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
import Svg, { Circle, G, Line, Path, Rect } from "react-native-svg";

import { eyesAt, glowAt, hopAt, LOOP_MS, sparkleAt, SPARKLE_COUNT, wandAt } from "./updateMascotPose";
import { useReducedMotion } from "./useReducedMotion";

// `assets/mascot.png`, redrawn as parts so each can move: the body (with trunk
// and legs), the eyes, and the arm holding the wand. Coordinates are the PNG's
// own 512x447 pixel grid, so the drawing can be checked against the artwork
// shape for shape. The frame is padded beyond the PNG on the top and right for
// the sparkles, and centred on body + wand rather than on the body alone.
const VB = { x: -40, y: -80, w: 540, h: 530 };
const VIEWBOX = `${VB.x} ${VB.y} ${VB.w} ${VB.h}`;

// Sampled from the PNG.
const C = {
	body: "#79B0DC",
	light: "#A9CDEB",
	shade: "#2C3E6B",
	eye: "#1F2A3C",
	wandLight: "#DCE6EA",
	wandDark: "#7E8BB0",
	glow: "#FFE58A",
	spark: ["#FFE08A", "#CFE7FF"],
} as const;

// The PNG draws its depth as a dark edge along the right and bottom of each
// block; a dark copy of the shape offset by this much reproduces it.
const DEPTH = 6;

const BODY_RECTS: readonly [number, number, number, number][] = [
	[75, 159, 243, 219], // head and body
	[12, 242, 70, 58], // trunk root
	[12, 300, 46, 50], // trunk
	[75, 370, 36, 46], // legs
	[135, 370, 33, 46],
	[228, 370, 33, 46],
	[285, 370, 33, 46],
];
const HIGHLIGHT_RECTS: readonly [number, number, number, number][] = [
	[75, 159, 243, 6],
	[75, 159, 6, 83],
	[12, 242, 63, 6],
	[12, 242, 6, 108],
];
const EYES: readonly [number, number][] = [
	[129, 213],
	[251, 213],
];
const EYE_W = 27;
const EYE_H = 33;
const GLANCE = 7;
const HOP = 30;

// The arm starts inside the body so rotating it never opens a seam at the
// shoulder; its depth edge starts at the body's edge so it never darkens the body.
const ARM_FILL = "M296 240 H390 Q406 240 404 258 Q400 282 370 286 H296 Z";
const ARM_DEPTH = "M318 240 H390 Q406 240 404 258 Q400 282 370 286 H318 Z";
const HAND = { cx: 377, cy: 229, r: 27 };
const SHOULDER = { x: 318, y: 262 };
const TIP = { x: 440, y: 34 };
const FEET = { x: 200, y: 416 };

const STAR = "M0 -10 C1.6 -1.6 1.6 -1.6 10 0 C1.6 1.6 1.6 1.6 0 10 C-1.6 1.6 -1.6 1.6 -10 0 C-1.6 -1.6 -1.6 -1.6 0 -10 Z";

export function UpdateMascot({ size = 200, shadowColor }: { size?: number; shadowColor: string }) {
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
	const armStyle = useAnimatedStyle(() => ({ transform: [{ rotate: `${wandAt(clock.value)}deg` }] }));
	const eyeStyle = useAnimatedStyle(() => {
		const { open, glance } = eyesAt(clock.value);
		return { transform: [{ translateX: glance * GLANCE * k }, { scaleY: open }] };
	});
	const glowStyle = useAnimatedStyle(() => {
		const g = glowAt(clock.value);
		return { opacity: 0.18 + 0.3 * g, transform: [{ scale: 0.75 + 0.45 * g }] };
	});

	const glowSize = 54 * k;

	return (
		<View
			style={{ width: size, height }}
			accessible
			accessibilityRole="image"
			accessibilityLabel="Orchestrator mascot"
		>
			<Animated.View
				style={[
					styles.abs,
					{
						left: px(FEET.x - 130),
						top: py(FEET.y + 4),
						width: 260 * k,
						height: 20 * k,
						borderRadius: 10 * k,
						backgroundColor: shadowColor,
					},
					shadowStyle,
				]}
			/>
			<Animated.View style={[StyleSheet.absoluteFill, { transformOrigin: [px(FEET.x), py(FEET.y), 0] }, hopStyle]}>
				<Svg width={size} height={height} viewBox={VIEWBOX}>
					<G transform={`translate(${DEPTH} ${DEPTH})`} fill={C.shade}>
						{BODY_RECTS.map(([x, y, w, h]) => (
							<Rect key={`d${x},${y}`} x={x} y={y} width={w} height={h} />
						))}
					</G>
					<G fill={C.body}>
						{BODY_RECTS.map(([x, y, w, h]) => (
							<Rect key={`b${x},${y}`} x={x} y={y} width={w} height={h} />
						))}
					</G>
					<G fill={C.light}>
						{HIGHLIGHT_RECTS.map(([x, y, w, h]) => (
							<Rect key={`h${x},${y},${w}`} x={x} y={y} width={w} height={h} />
						))}
					</G>
				</Svg>
				{EYES.map(([x, y]) => (
					<Animated.View
						key={x}
						style={[
							styles.abs,
							{ left: px(x), top: py(y), width: EYE_W * k, height: EYE_H * k, borderRadius: 2 * k, backgroundColor: C.eye },
							eyeStyle,
						]}
					/>
				))}
				<Animated.View
					style={[StyleSheet.absoluteFill, { transformOrigin: [px(SHOULDER.x), py(SHOULDER.y), 0] }, armStyle]}
				>
					<Svg width={size} height={height} viewBox={VIEWBOX}>
						<Path d={ARM_DEPTH} fill={C.shade} transform={`translate(${DEPTH} ${DEPTH})`} />
						<Circle cx={HAND.cx + DEPTH} cy={HAND.cy + DEPTH} r={HAND.r} fill={C.shade} />
						<Path d={ARM_FILL} fill={C.body} />
						<Circle cx={HAND.cx} cy={HAND.cy} r={HAND.r} fill={C.body} />
						<Rect x={318} y={240} width={28} height={5} fill={C.light} />
						<Line x1={366} y1={248} x2={TIP.x} y2={TIP.y} stroke={C.wandDark} strokeWidth={13} strokeLinecap="round" />
						<Line x1={363} y1={247} x2={TIP.x - 3} y2={TIP.y - 1} stroke={C.wandLight} strokeWidth={7} strokeLinecap="round" />
					</Svg>
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
	const side = (index % 2 === 0 ? 26 : 18) * k;
	const style = useAnimatedStyle(() => {
		const s = sparkleAt(clock.value, index);
		return {
			opacity: s.opacity,
			transform: [{ translateX: s.x * k }, { translateY: s.y * k }, { rotate: `${s.rotate}deg` }, { scale: s.scale }],
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
});
