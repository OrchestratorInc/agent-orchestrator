import { useEffect } from "react";
import { Pressable, StyleSheet, View } from "react-native";
import Animated, {
	cancelAnimation,
	Easing,
	useAnimatedProps,
	useAnimatedStyle,
	useSharedValue,
	withRepeat,
	withTiming,
} from "react-native-reanimated";
import Svg, { ClipPath, Defs, G, LinearGradient, Path, Rect, Stop, SvgXml } from "react-native-svg";

import { haptics } from "./haptics";
import { RUTO_BATON, RUTO_BODY, RUTO_DEFS, RUTO_OUTLINE } from "./rutoArt";
import {
	batonAt,
	blinkAt,
	breathAt,
	FINALE_MS,
	FINALE_NOD,
	finaleAt,
	glowAt,
	LOOP_MS,
	POKE_MS,
	pokeAt,
	RUTO_PIVOT,
	RUTO_TIP,
	SHEEN_FROM,
	SHEEN_TO,
	sheenAt,
} from "./updateMascotPose";
import { useReducedMotion } from "./useReducedMotion";

// Ruto from the brand source (rutoArt.ts, copied from assets/brand/ruto.svg), in
// two layers so the baton can move on its own: the baton behind, the body over
// it, as the source draws them. A sheen clipped to Ruto's outline passes over
// the body. The performance itself is in updateMascotPose.ts. Coordinates are
// the brand file's 1024 grid; the frame is cropped to Ruto.
const VB = { x: 150, y: 140, w: 800, h: 680 };
const VIEWBOX = `${VB.x} ${VB.y} ${VB.w} ${VB.h}`;
const svg = (inner: string) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${VIEWBOX}">${inner}</svg>`;
const BODY_XML = svg(RUTO_DEFS + RUTO_BODY);
const BATON_XML = svg(RUTO_BATON);

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
const GLOW = "#FFE58A";
const BREATH = 0.015;
const SHEEN_W = 150;

const AnimatedRect = Animated.createAnimatedComponent(Rect);

export type UpdateMascotProps = {
	size?: number;
	shadowColor: string;
	/** Play the finale (the restart has begun). */
	finale: boolean;
};

export function UpdateMascot({ size = 260, shadowColor, finale }: UpdateMascotProps) {
	const reduceMotion = useReducedMotion();
	const clock = useSharedValue(0);
	const poke = useSharedValue(0);
	const fin = useSharedValue(0);

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

	useEffect(() => {
		if (!finale) return;
		fin.value = 0;
		fin.value = withTiming(1, { duration: FINALE_MS, easing: Easing.linear });
		// The nod: the one haptic of the screen.
		const timer = setTimeout(() => haptics.success(), reduceMotion ? 0 : FINALE_MS * FINALE_NOD);
		return () => clearTimeout(timer);
	}, [finale, reduceMotion, fin]);

	const onPoke = () => {
		if (finale) return;
		haptics.tap();
		if (reduceMotion) return;
		poke.value = 0;
		poke.value = withTiming(1, { duration: POKE_MS, easing: Easing.linear });
	};

	const k = size / VB.w;
	const px = (x: number) => (x - VB.x) * k;
	const py = (y: number) => (y - VB.y) * k;
	const height = VB.h * k;
	const still = reduceMotion;

	const stageStyle = useAnimatedStyle(() => ({ opacity: fin.value > 0 ? finaleAt(fin.value).opacity : 1 }));
	const bodyStyle = useAnimatedStyle(() => {
		if (still) return { transform: [] };
		if (fin.value > 0) {
			const f = finaleAt(fin.value);
			return { transform: [{ translateY: f.dip * k }, { scaleY: f.scaleY }] };
		}
		const p = pokeAt(poke.value);
		return { transform: [{ scaleX: p.scaleX }, { scaleY: p.scaleY * (1 + BREATH * breathAt(clock.value)) }] };
	});
	const batonStyle = useAnimatedStyle(() => ({ transform: [{ rotate: `${still ? 0 : batonAt(clock.value)}deg` }] }));
	const lidStyle = useAnimatedStyle(() => {
		const shut = still ? 0 : fin.value > 0 ? finaleAt(fin.value).shut : blinkAt(clock.value);
		return { transform: [{ scaleY: shut }] };
	});
	const glowStyle = useAnimatedStyle(() => {
		const g = still ? 0.4 : Math.min(1, glowAt(clock.value) + (fin.value > 0 ? finaleAt(fin.value).glow : 0));
		return { opacity: 0.12 + 0.3 * g, transform: [{ scale: 0.8 + 0.35 * g }] };
	});
	const sheenProps = useAnimatedProps(() => {
		const s = still ? -1 : fin.value > 0 ? finaleAt(fin.value).sheen : sheenAt(clock.value);
		return {
			x: s < 0 ? SHEEN_FROM : SHEEN_FROM + (SHEEN_TO - SHEEN_FROM) * s,
			opacity: s < 0 ? 0 : fin.value > 0 ? 1 : 0.75,
		};
	});

	const glowSize = 80 * k;

	return (
		<Pressable onPress={onPoke} accessibilityRole="image" accessibilityLabel="Orchestrator mascot">
			<Animated.View style={[{ width: size, height }, stageStyle]}>
				<View
					style={[
						styles.abs,
						{
							left: px(FEET.x - 200),
							top: py(FEET.y + 8),
							width: 400 * k,
							height: 24 * k,
							borderRadius: 12 * k,
							backgroundColor: shadowColor,
						},
					]}
				/>
				<Animated.View style={[StyleSheet.absoluteFill, { transformOrigin: [px(FEET.x), py(FEET.y), 0] }, bodyStyle]}>
					<Animated.View
						style={[StyleSheet.absoluteFill, { transformOrigin: [px(RUTO_PIVOT.x), py(RUTO_PIVOT.y), 0] }, batonStyle]}
					>
						<SvgXml xml={BATON_XML} width={size} height={height} />
						<Animated.View
							style={[
								styles.abs,
								{
									left: px(RUTO_TIP.x) - glowSize / 2,
									top: py(RUTO_TIP.y) - glowSize / 2,
									width: glowSize,
									height: glowSize,
									borderRadius: glowSize / 2,
									backgroundColor: GLOW,
								},
								glowStyle,
							]}
						/>
					</Animated.View>
					<SvgXml xml={BODY_XML} width={size} height={height} style={StyleSheet.absoluteFill} />
					{/* The sheen, clipped to Ruto's outline so it only ever lights Ruto. */}
					<Svg width={size} height={height} viewBox={VIEWBOX} style={StyleSheet.absoluteFill} pointerEvents="none">
						<Defs>
							<ClipPath id="rutoSheenClip">
								<Path d={RUTO_OUTLINE} fillRule="evenodd" />
							</ClipPath>
							<LinearGradient id="rutoSheen" x1="0" y1="0" x2="1" y2="0">
								<Stop offset="0" stopColor="#FFFFFF" stopOpacity={0} />
								<Stop offset="0.5" stopColor="#FFFFFF" stopOpacity={0.55} />
								<Stop offset="1" stopColor="#FFFFFF" stopOpacity={0} />
							</LinearGradient>
						</Defs>
						<G clipPath="url(#rutoSheenClip)">
							<AnimatedRect
								y={300}
								width={SHEEN_W}
								height={520}
								fill="url(#rutoSheen)"
								transform="skewX(-18)"
								animatedProps={sheenProps}
							/>
						</G>
					</Svg>
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
			</Animated.View>
		</Pressable>
	);
}

const styles = StyleSheet.create({
	abs: { position: "absolute" },
	// Closes from the top down; the pupil-dark edge rides at its bottom.
	lid: { transformOrigin: "top", justifyContent: "flex-end" },
});
