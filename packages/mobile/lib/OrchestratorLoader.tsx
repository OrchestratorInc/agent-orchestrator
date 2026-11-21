import { memo, useEffect, useMemo } from "react";
import { StyleSheet, Text, View } from "react-native";
import Animated, { cancelAnimation, Easing, interpolate, useAnimatedStyle, useSharedValue, withDelay, withRepeat, withSequence, withTiming, type SharedValue } from "react-native-reanimated";
import { SvgXml } from "react-native-svg";
import { ensemblePhrase, PLAYERS, type Ease, type Track } from "./ensemblePhrase";
import { RUTO_BODY_XML, RUTO_VIEWBOX as VB } from "./rutoArt";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { font, fontScaleCap, space } from "./tokens";
import { useReducedMotion } from "./useReducedMotion";

/** What each player sings. */
const NOTE_GLYPHS = ["♪", "♫", "♩"] as const;
/** Each player blinks on its own clock, so they never blink as one. */
const BLINK_EVERY_MS = [3100, 3700, 4300] as const;
const BLINK_MS = 70;

const PLAYER_WIDTH = 34;
const PLAYER_GAP = 12;
/** Room above the players for a hop and the notes rising off it. */
const HEADROOM = 30;

/** Ruto's eye socket, the colour a closed eye shows (assets/brand/ruto.svg). */
const RUTO_LID = "#2A9FE0";
/** The two pupils, in the brand file's 1024 units. */
const EYES = [{ x: 381, y: 437 }, { x: 568, y: 437 }] as const;
const EYE = { width: 43, height: 48 };

const EASE: Record<Ease, ReturnType<typeof Easing.in>> = {
	linear: Easing.linear,
	fall: Easing.in(Easing.cubic),
	rebound: Easing.out(Easing.cubic),
	smooth: Easing.inOut(Easing.quad),
};

/** A track as a looping animation: each key in turn, holding between them. */
function play(track: Track, length: number) {
	const steps = [];
	let cursor = 0;
	let value = track.from;
	for (const key of track.keys) {
		if (key.at > cursor) steps.push(withTiming(value, { duration: key.at - cursor }));
		steps.push(withTiming(key.to, { duration: key.ms, easing: EASE[key.ease] }));
		cursor = key.at + key.ms;
		value = key.to;
	}
	if (length > cursor) steps.push(withTiming(value, { duration: length - cursor }));
	return withRepeat(withSequence(...steps), -1);
}

const Body = memo(function Body({ width, height }: { width: number; height: number }) {
	return <SvgXml xml={RUTO_BODY_XML} width={width} height={height} />;
});

/** One of the ensemble: a small Ruto who hops when cued, sways with the beat, sings, and blinks. */
function Player({ index, hop, note, sway, blink, left, floor, color }: {
	index: number;
	hop: SharedValue<number>;
	note: SharedValue<number>;
	sway: SharedValue<number>;
	blink: SharedValue<number>;
	left: number;
	floor: number;
	color: string;
}) {
	const width = PLAYER_WIDTH;
	const k = width / VB.width;
	const height = VB.height * k;
	const jump = width * 0.36;
	const body = useAnimatedStyle(() => {
		const h = hop.value;
		return {
			transform: [
				{ translateY: h > 0 ? -h * jump : -h * 2 },
				{ rotate: `${sway.value * (index % 2 === 0 ? 1.4 : -1.4)}deg` },
				// Squashed in the bow, stretched in the air.
				{ scaleY: h > 0 ? 1 + Math.min(h, 1) * 0.06 : 1 + h * 0.14 },
				{ scaleX: h > 0 ? 1 - Math.min(h, 1) * 0.03 : 1 - h * 0.06 },
			],
		};
	});
	const lid = useAnimatedStyle(() => ({ transform: [{ scaleY: blink.value }] }));
	const sung = useAnimatedStyle(() => {
		const p = note.value;
		const drift = index === 0 ? -7 : index === 1 ? 2 : 8;
		return {
			opacity: p <= 0 ? 0 : interpolate(p, [0, 0.1, 0.6, 1], [0, 1, 0.75, 0]),
			transform: [{ translateX: drift * p }, { translateY: -HEADROOM * 0.8 * p }, { rotate: `${(index - 1) * 14 * p}deg` }, { scale: interpolate(p, [0, 0.15, 1], [0.4, 1, 0.9]) }],
		};
	});
	return (
		<>
			<Animated.View style={[{ position: "absolute", left, top: floor - height, width, height, transformOrigin: "bottom" }, body]}>
				<Body width={width} height={height} />
				{EYES.map((eye, i) => (
					<Animated.View key={i} style={[{ position: "absolute", left: (eye.x - VB.x) * k, top: (eye.y - VB.y) * k, width: EYE.width * k, height: EYE.height * k, backgroundColor: RUTO_LID, transformOrigin: "top" }, lid]} />
				))}
			</Animated.View>
			<Animated.Text style={[{ position: "absolute", left: left + width / 2 - 5, top: floor - height - 14, fontSize: 14, color }, sung]}>
				{NOTE_GLYPHS[index % NOTE_GLYPHS.length]}
			</Animated.Text>
		</>
	);
}

/**
 * Three small Rutos, the orchestrator's mascot, playing while a conversation
 * is on its way: a phrase of four bars (see ensemblePhrase.ts) in which each
 * takes a solo with a hop and a note, all three come in together, answer in a
 * wave, rise with a crescendo, hit the cut-off and bow. They sway with the
 * beat between cues and blink on their own clocks. The artwork is the brand
 * file itself (see rutoArt.ts).
 *
 * Under Reduce Motion they stand still; the title says what is happening
 * either way.
 */
export function OrchestratorLoader() {
	const t = useTheme();
	const reduced = useReducedMotion();
	const phrase = useMemo(ensemblePhrase, []);

	const sway = useSharedValue(0);
	const hop0 = useSharedValue(0);
	const hop1 = useSharedValue(0);
	const hop2 = useSharedValue(0);
	const note0 = useSharedValue(0);
	const note1 = useSharedValue(0);
	const note2 = useSharedValue(0);
	const blink0 = useSharedValue(0);
	const blink1 = useSharedValue(0);
	const blink2 = useSharedValue(0);
	const hops = [hop0, hop1, hop2];
	const notes = [note0, note1, note2];
	const blinks = [blink0, blink1, blink2];

	useEffect(() => {
		const all = [sway, hop0, hop1, hop2, note0, note1, note2, blink0, blink1, blink2];
		if (reduced) {
			all.forEach((value) => { cancelAnimation(value); value.value = 0; });
			return;
		}
		const { length } = phrase;
		sway.value = play(phrase.lean, length);
		[hop0, hop1, hop2].forEach((value, i) => { value.value = play(phrase.hops[i]!, length); });
		[note0, note1, note2].forEach((value, i) => { value.value = play(phrase.notes[i]!, length); });
		[blink0, blink1, blink2].forEach((value, i) => {
			value.value = withRepeat(withSequence(withDelay(BLINK_EVERY_MS[i]!, withTiming(1, { duration: BLINK_MS })), withTiming(0, { duration: BLINK_MS + 40 })), -1);
		});
		return () => all.forEach((value) => cancelAnimation(value));
	}, [phrase, sway, hop0, hop1, hop2, note0, note1, note2, blink0, blink1, blink2, reduced]);

	const height = (VB.height / VB.width) * PLAYER_WIDTH;
	const floor = HEADROOM + height;
	const width = PLAYERS * PLAYER_WIDTH + (PLAYERS - 1) * PLAYER_GAP;

	return (
		<View style={{ width, height: floor }} testID="orchestrator-loader" accessible={false} importantForAccessibility="no-hide-descendants">
			{hops.map((hop, i) => (
				<Player key={i} index={i} hop={hop} note={notes[i]!} sway={sway} blink={blinks[i]!} left={i * (PLAYER_WIDTH + PLAYER_GAP)} floor={floor} color={t.textTertiary} />
			))}
		</View>
	);
}

/** The whole-screen state a conversation shows while it loads. */
export function ConversationLoading({ title, message }: { title: string; message?: string }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.root} accessible accessibilityRole="progressbar" accessibilityLabel={title} accessibilityState={{ busy: true }}>
			<OrchestratorLoader />
			<Text maxFontSizeMultiplier={fontScaleCap.title} style={styles.title}>{title}</Text>
			{message ? <Text numberOfLines={2} maxFontSizeMultiplier={fontScaleCap.body} style={styles.message}>{message}</Text> : null}
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	root: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.xxs, paddingHorizontal: space.xl },
	title: { marginTop: space.md, fontFamily: font.medium, fontSize: 15, color: t.textPrimary, textAlign: "center" },
	message: { fontFamily: font.regular, fontSize: 13, color: t.textTertiary, textAlign: "center" },
});
