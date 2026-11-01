import { useEffect, useRef, useState } from "react";
import { StyleSheet, Text, View, type DimensionValue, type GestureResponderEvent, type LayoutChangeEvent } from "react-native";
import Animated, { Easing, FadeIn, FadeOut, useAnimatedStyle, useSharedValue, withTiming } from "react-native-reanimated";
import { haptics } from "./haptics";
import { magneticOffset, nearestStop } from "./modelEffort";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { duration, radius, space, type } from "./tokens";
import { useReducedMotion } from "./useReducedMotion";
import { markTouchInside } from "./touchOutside";
import { useEffortSlider, type EffortSliderProps } from "./useEffortSlider";

/*
 * The effort slider, the same on both platforms: a fat track with a dot per
 * level the model takes, lit in the theme's accent up to a white blob that
 * rides inside it. The blob follows the finger but is pulled toward the
 * nearest level, ticks as it lands on each, and glides onto its level when let
 * go. It stays up until a tap lands outside it (see useEffortSliderOpen).
 */

const TRACK = 40;
const INSET = 4;
const BLOB = TRACK - INSET * 2;
// The blob's centre at either end of the track.
const END = TRACK / 2;
const DOT = 3;
// The blob is white in both themes, as a system switch's thumb is.
const BLOB_FILL = "#ffffff";
const BLOB_SHADOW = "#000000";

/**
 * The slider floating over the screen as a card, a small gap above `bottom`
 * in its container: nothing under it moves when it opens. Android hands it a
 * touch outside its container only while no ancestor clips.
 */
export function EffortSliderOverlay({ bottom, ...props }: EffortSliderProps & { bottom: DimensionValue }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<Animated.View entering={FadeIn.duration(duration.base)} exiting={FadeOut.duration(duration.fast)} onTouchStart={markTouchInside} style={[styles.overlay, { bottom }]}>
			<EffortSlider {...props} />
		</Animated.View>
	);
}

export function EffortSlider({ choices, onChoose, onRelease }: EffortSliderProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const reduceMotion = useReducedMotion();
	const [width, setWidth] = useState(0);
	const { index, label, pick } = useEffortSlider(choices, onChoose, haptics.detent);
	const count = choices.length;
	const step = count > 1 ? (width - END * 2) / (count - 1) : 0;
	const stopX = (i: number) => END + Math.max(i, 0) * step;

	// The blob's centre along the track.
	const blobX = useSharedValue(0);
	const dragging = useRef(false);
	const glide = (x: number) => {
		blobX.value = reduceMotion ? x : withTiming(x, { duration: duration.base, easing: Easing.out(Easing.cubic) });
	};
	// Settled on the chosen level whenever no finger holds it.
	useEffect(() => { if (!dragging.current) blobX.value = stopX(index); }, [index, width]); // eslint-disable-line react-hooks/exhaustive-deps
	const blobStyle = useAnimatedStyle(() => ({ transform: [{ translateX: blobX.value - BLOB / 2 }] }));
	const fillStyle = useAnimatedStyle(() => ({ width: blobX.value + BLOB / 2 + INSET }));

	const follow = (event: GestureResponderEvent, animate: boolean) => {
		const x = Math.min(width - END, Math.max(END, event.nativeEvent.locationX));
		const stop = nearestStop(x, END, step, count);
		pick(stop);
		const pulled = stopX(stop) + magneticOffset(x - stopX(stop), step);
		if (animate) glide(pulled);
		else blobX.value = pulled;
	};

	const release = () => {
		dragging.current = false;
		glide(stopX(nearestStop(blobX.value, END, step, count)));
		onRelease?.();
	};

	return (
		<View style={styles.row}>
			<Text style={styles.label} numberOfLines={1}>{label}</Text>
			<View
				style={styles.track}
				onLayout={(event: LayoutChangeEvent) => setWidth(event.nativeEvent.layout.width)}
				onStartShouldSetResponder={() => true}
				onMoveShouldSetResponder={() => true}
				onResponderTerminationRequest={() => false}
				onResponderGrant={(event) => { dragging.current = true; follow(event, true); }}
				onResponderMove={(event) => follow(event, false)}
				onResponderRelease={release}
				onResponderTerminate={release}
				accessible
				accessibilityRole="adjustable"
				accessibilityLabel="Effort"
				accessibilityValue={{ text: label }}
				accessibilityActions={[{ name: "increment" }, { name: "decrement" }]}
				onAccessibilityAction={(event) => pick(Math.max(index, 0) + (event.nativeEvent.actionName === "increment" ? 1 : -1))}
				testID="effort-slider"
			>
				{width && index >= 0 ? <Animated.View pointerEvents="none" style={[styles.fill, fillStyle]} /> : null}
				{width ? choices.map((choice, i) => (
					<View key={choice.value} pointerEvents="none" style={[styles.dot, { left: stopX(i) - DOT, backgroundColor: i <= index ? t.textSecondary : t.textFaint }]} />
				)) : null}
				{width && index >= 0 ? <Animated.View pointerEvents="none" style={[styles.blob, blobStyle]} /> : null}
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		overlay: {
			position: "absolute",
			left: 0,
			right: 0,
			zIndex: 1,
			marginBottom: space.xs,
			padding: space.xs,
			paddingLeft: space.md,
			borderRadius: radius.pill,
			borderCurve: "continuous",
			backgroundColor: t.bgElevated,
			borderWidth: StyleSheet.hairlineWidth,
			borderColor: t.borderDefault,
			shadowColor: BLOB_SHADOW,
			shadowOpacity: 0.25,
			shadowRadius: 12,
			shadowOffset: { width: 0, height: 4 },
			elevation: 8,
		},
		row: { flexDirection: "row", alignItems: "center", gap: space.md },
		label: { ...type.subheadline, fontFamily: type.headline.fontFamily, color: t.textPrimary, minWidth: 56 },
		track: { flex: 1, height: TRACK, borderRadius: TRACK / 2, borderCurve: "continuous", backgroundColor: t.bgElevatedHover, overflow: "hidden", justifyContent: "center" },
		fill: { position: "absolute", left: 0, top: 0, bottom: 0, borderRadius: TRACK / 2, backgroundColor: t.accentBorder },
		dot: { position: "absolute", width: DOT * 2, height: DOT * 2, borderRadius: DOT },
		blob: {
			position: "absolute",
			left: 0,
			width: BLOB,
			height: BLOB,
			borderRadius: BLOB / 2,
			backgroundColor: BLOB_FILL,
			shadowColor: BLOB_SHADOW,
			shadowOpacity: 0.22,
			shadowRadius: 4,
			shadowOffset: { width: 0, height: 1 },
			elevation: 3,
		},
	});
