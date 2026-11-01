import { useEffect } from "react";
import { StyleSheet, Text, View, type StyleProp, type ViewStyle } from "react-native";
import Animated, { useAnimatedStyle, useSharedValue, withTiming, type SharedValue } from "react-native-reanimated";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { fontScaleCap, space, type } from "../tokens";
import { useReducedMotion } from "../useReducedMotion";
import type { VoiceState } from "./types";

/** The waveform keeps this many of the latest input levels, newest at the trailing end. */
export const WAVEFORM_BARS = 28;
const BAR_WIDTH = 3;
const BAR_GAP = 2;
/** A bar at silence, and at full voice. */
const BAR_MIN = 3;
const BAR_MAX = 22;
/** Each bar eases to its new height over about one meter interval, so the trace moves rather than steps. */
const BAR_EASE_MS = 90;

/**
 * Dictation drawn in the composer's own field: what has been heard so far (or
 * "Listening…") and a waveform of the input level, in the place the text
 * field takes the rest of the time.
 *
 * It replaced a banner over the composer that came up as "Keep holding…" while
 * the mic warmed up and changed to "Listening…" a moment later, so it flickered
 * on every tap. The field keeps one label for the whole recording; the warm-up
 * shows as the waveform lying flat in a quiet tint, and the trace takes the
 * recording colour once the mic captures, alongside the haptic that says so.
 */
export function DictationField({ state, partial, subscribeLevel, style }: {
	state: VoiceState;
	/** The live transcript; empty until the recogniser has heard something. */
	partial: string;
	subscribeLevel(listener: (level: number) => void): () => void;
	style?: StyleProp<ViewStyle>;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const capturing = state === "recording";
	const label = partial || (state === "transcribing" ? "Transcribing…" : "Listening…");
	return (
		<View style={[styles.field, style]} accessibilityLiveRegion="polite" accessible accessibilityLabel={label}>
			{/* The latest words matter most, so a long transcript gives up its start. */}
			<Text numberOfLines={1} ellipsizeMode="head" maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.label, { color: partial ? t.textPrimary : capturing ? t.textSecondary : t.textFaint }]}>
				{label}
			</Text>
			<VoiceWaveform live={capturing} subscribeLevel={subscribeLevel} />
		</View>
	);
}

/** The input level over the last couple of seconds, as a row of bars. */
export function VoiceWaveform({ live, subscribeLevel }: { live: boolean; subscribeLevel(listener: (level: number) => void): () => void }) {
	const t = useTheme();
	const levels = useSharedValue<number[]>(silence());
	useEffect(() => {
		if (!live) {
			levels.value = silence();
			return;
		}
		return subscribeLevel((level) => {
			levels.value = [...levels.value.slice(1), level];
		});
	}, [levels, live, subscribeLevel]);
	return (
		<View importantForAccessibility="no-hide-descendants" accessibilityElementsHidden style={waveformStyles.row}>
			{Array.from({ length: WAVEFORM_BARS }, (_, index) => (
				<WaveformBar key={index} index={index} levels={levels} color={live ? t.red : t.textFaint} />
			))}
		</View>
	);
}

function WaveformBar({ index, levels, color }: { index: number; levels: SharedValue<number[]>; color: string }) {
	const reduceMotion = useReducedMotion();
	const height = useAnimatedStyle(() => {
		const next = BAR_MIN + (BAR_MAX - BAR_MIN) * (levels.value[index] ?? 0);
		return { height: reduceMotion ? next : withTiming(next, { duration: BAR_EASE_MS }) };
	});
	return <Animated.View style={[waveformStyles.bar, { backgroundColor: color }, height]} />;
}

function silence(): number[] {
	return Array.from({ length: WAVEFORM_BARS }, () => 0);
}

const waveformStyles = StyleSheet.create({
	row: { flexDirection: "row", alignItems: "center", gap: BAR_GAP, height: BAR_MAX, flexShrink: 0 },
	bar: { width: BAR_WIDTH, minHeight: BAR_MIN, borderRadius: BAR_WIDTH / 2 },
});

const makeStyles = (t: Theme) => StyleSheet.create({
	// The caller gives it the field's own resting height, so swapping the field
	// for this moves nothing.
	field: { flex: 1, minWidth: 0, flexDirection: "row", alignItems: "center", gap: space.sm },
	label: { flex: 1, minWidth: 0, fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
});
