import { useEffect, useRef } from "react";
import { Pressable, StyleSheet, View } from "react-native";
import Animated, {
	cancelAnimation,
	Easing,
	useAnimatedStyle,
	useSharedValue,
	withRepeat,
	withTiming,
	type SharedValue,
} from "react-native-reanimated";
import Svg, { Ellipse, Path, Rect, SvgXml } from "react-native-svg";

import { haptics } from "./haptics";
import { RUTO_BATON, RUTO_BODY, RUTO_DEFS } from "./rutoArt";
import {
	batonAt,
	BEAT_MS,
	BEATS_PER_BAR,
	blinkAt,
	bobAt,
	breathAt,
	BURST_NOTES,
	burstNoteAt,
	FINALE_ICTUS,
	FINALE_MS,
	finaleAt,
	LOOP_MS,
	noteAt,
	POKE_MS,
	pokeAt,
	RUTO_PIVOT,
	RUTO_TIP,
	tipAt,
} from "./updateMascotPose";
import { useReducedMotion } from "./useReducedMotion";

// Ruto from the brand source (rutoArt.ts, copied from assets/brand/ruto.svg), in
// two layers so the baton can move on its own: the baton behind, the body over
// it, as the source draws them. The performance itself — conducting, the poke,
// the finale — is in updateMascotPose.ts. Coordinates are the brand file's 1024
// grid; the frame is cropped to Ruto and padded above for the notes.
const VB = { x: 150, y: 20, w: 800, h: 780 };
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
/** Body dip on each beat and its breathing, in grid units and scale. */
const BOB = 7;
const BREATH = 0.012;
const NOTE = { w: 58, h: 76 };

export type UpdateMascotProps = {
	size?: number;
	shadowColor: string;
	/** Two note colours, alternated. */
	noteColors: readonly [string, string];
	/** Play the finale (the restart has begun). */
	finale: boolean;
};

export function UpdateMascot({ size = 240, shadowColor, noteColors, finale }: UpdateMascotProps) {
	const reduceMotion = useReducedMotion();
	const clock = useSharedValue(0);
	const poke = useSharedValue(0);
	const pokeFrom = useSharedValue(0);
	const fin = useSharedValue(0);
	const finFrom = useSharedValue(0);
	const finaleTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

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
		finFrom.value = reduceMotion ? 0 : batonAt(clock.value) + pokeAt(poke.value).spin;
		cancelAnimation(clock);
		fin.value = 0;
		fin.value = withTiming(1, { duration: FINALE_MS, easing: Easing.linear });
		// The downstroke lands: the one strong haptic of the screen.
		finaleTimer.current = setTimeout(() => haptics.success(), reduceMotion ? 0 : FINALE_MS * FINALE_ICTUS);
		return () => {
			if (finaleTimer.current) clearTimeout(finaleTimer.current);
		};
	}, [finale, reduceMotion, clock, fin, finFrom, poke]);

	const onPoke = () => {
		if (finale) return;
		haptics.tap();
		if (reduceMotion) return;
		pokeFrom.value = batonAt(clock.value);
		poke.value = 0;
		poke.value = withTiming(1, { duration: POKE_MS, easing: Easing.linear });
	};

	const k = size / VB.w;
	const px = (x: number) => (x - VB.x) * k;
	const py = (y: number) => (y - VB.y) * k;
	const height = VB.h * k;
	const still = reduceMotion;

	const stageStyle = useAnimatedStyle(() => ({ opacity: fin.value > 0 ? finaleAt(fin.value, finFrom.value).opacity : 1 }));
	const bodyStyle = useAnimatedStyle(() => {
		if (fin.value > 0) {
			const f = finaleAt(fin.value, finFrom.value);
			return still
				? { transform: [] }
				: { transform: [{ translateY: f.lift * k }, { scaleX: 1 + (1 - f.scaleY) * 0.5 }, { scaleY: f.scaleY }] };
		}
		const bob = bobAt(clock.value);
		const p = pokeAt(poke.value);
		const breath = 1 + BREATH * breathAt(clock.value);
		return {
			transform: [
				{ translateY: bob * BOB * k },
				{ scaleX: p.scaleX * (1 + 0.015 * bob) },
				{ scaleY: p.scaleY * breath * (1 - 0.025 * bob) },
			],
		};
	});
	const shadowStyle = useAnimatedStyle(() => {
		const lift = fin.value > 0 ? finaleAt(fin.value, finFrom.value).lift : -bobAt(clock.value) * BOB;
		return { transform: [{ scaleX: 1 + lift * 0.004 }] };
	});
	const batonStyle = useAnimatedStyle(() => {
		if (still) return { transform: [{ rotate: "0deg" }] };
		const angle = fin.value > 0 ? finaleAt(fin.value, finFrom.value).angle : batonAt(clock.value) + pokeAt(poke.value).spin;
		return { transform: [{ rotate: `${angle}deg` }] };
	});
	const lidStyle = useAnimatedStyle(() => {
		const shut = still
			? 0
			: fin.value > 0
				? finaleAt(fin.value, finFrom.value).shut
				: Math.max(blinkAt(clock.value), pokeAt(poke.value).squint);
		return { transform: [{ scaleY: shut }] };
	});
	const glowStyle = useAnimatedStyle(() => {
		// Brightest as each beat lands; held bright through the finale's freeze.
		const sinceBeat = (clock.value % BEAT_MS) / BEAT_MS;
		let g = 0.35 + 0.65 * (1 - sinceBeat) * (1 - sinceBeat);
		if (fin.value > 0) g = fin.value < FINALE_ICTUS ? 0.4 : 1 - 0.6 * Math.max(0, (fin.value - 0.52) / 0.3);
		return { opacity: still ? 0.3 : 0.12 + 0.35 * g, transform: [{ scale: 0.7 + 0.6 * g }] };
	});

	const glowSize = 90 * k;

	return (
		<Pressable onPress={onPoke} accessibilityRole="image" accessibilityLabel="Orchestrator mascot">
			<Animated.View style={[{ width: size, height }, stageStyle]}>
				<Animated.View
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
						shadowStyle,
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
				{still
					? null
					: Array.from({ length: BEATS_PER_BAR }, (_, beat) => (
							<BeatNote key={`beat${beat}`} beat={beat} clock={clock} k={k} color={noteColors[beat % 2]} />
						))}
				{still
					? null
					: Array.from({ length: BURST_NOTES }, (_, i) => (
							<BurstNote key={`poke${i}`} i={i} progress={poke} origin={pokeFrom} mode="poke" k={k} color={noteColors[i % 2]} />
						))}
				{still
					? null
					: Array.from({ length: BURST_NOTES }, (_, i) => (
							<BurstNote key={`fin${i}`} i={i} progress={fin} origin={finFrom} mode="finale" k={k} color={noteColors[(i + 1) % 2]} />
						))}
			</Animated.View>
		</Pressable>
	);
}

function NoteGlyph({ color, scale }: { color: string; scale: number }) {
	return (
		<Svg width={NOTE.w * scale} height={NOTE.h * scale} viewBox="0 0 24 32">
			<Ellipse cx={8.5} cy={25} rx={6.2} ry={4.6} fill={color} transform="rotate(-22 8.5 25)" />
			<Rect x={13} y={4} width={2.4} height={21.5} rx={1.2} fill={color} />
			<Path d="M15 4 C18.5 7.5 23 9.5 20.5 17.5 C20 13.5 17.5 12 15 11.5 Z" fill={color} />
		</Svg>
	);
}

/** The note that leaves the baton tip on one beat of every bar. */
function BeatNote({ beat, clock, k, color }: { beat: number; clock: SharedValue<number>; k: number; color: string }) {
	const style = useAnimatedStyle(() => {
		const n = noteAt(clock.value, beat);
		return {
			opacity: n.opacity,
			transform: [
				{ translateX: (n.from.x - VB.x + n.x) * k - (NOTE.w * k) / 2 },
				{ translateY: (n.from.y - VB.y + n.y) * k - NOTE.h * k },
				{ rotate: `${n.rotate}deg` },
				{ scale: n.scale },
			],
		};
	});
	return (
		<Animated.View style={[styles.abs, styles.origin, style]} pointerEvents="none">
			<NoteGlyph color={color} scale={k} />
		</Animated.View>
	);
}

/** One note of a burst from the tip: the poke's, or the finale's downstroke. */
function BurstNote({
	i,
	progress,
	origin,
	mode,
	k,
	color,
}: {
	i: number;
	progress: SharedValue<number>;
	origin: SharedValue<number>;
	mode: "poke" | "finale";
	k: number;
	color: string;
}) {
	const style = useAnimatedStyle(() => {
		const p = mode === "poke" ? pokeAt(progress.value).burst : finaleAt(progress.value, origin.value).burst;
		const n = burstNoteAt(p, i);
		// The poke bursts from where the tip was when tapped; the finale from the
		// downstroke's landing.
		const from = tipAt(mode === "poke" ? origin.value : 30);
		return {
			opacity: n.opacity,
			transform: [
				{ translateX: (from.x - VB.x + n.x) * k - (NOTE.w * k) / 2 },
				{ translateY: (from.y - VB.y + n.y) * k - NOTE.h * k },
				{ rotate: `${n.rotate}deg` },
				{ scale: n.scale },
			],
		};
	});
	return (
		<Animated.View style={[styles.abs, styles.origin, style]} pointerEvents="none">
			<NoteGlyph color={color} scale={k} />
		</Animated.View>
	);
}

const styles = StyleSheet.create({
	abs: { position: "absolute" },
	origin: { left: 0, top: 0 },
	// Closes from the top down; the pupil-dark edge rides at its bottom.
	lid: { transformOrigin: "top", justifyContent: "flex-end" },
});
