import { useCallback, useEffect } from "react";
import { StyleSheet, Text, View, type LayoutChangeEvent } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, {
	Easing,
	interpolate,
	interpolateColor,
	useAnimatedStyle,
	useSharedValue,
	withSpring,
	withTiming,
} from "react-native-reanimated";
import { scheduleOnRN } from "react-native-worklets";

import { AppSymbol } from "./AppSymbol";
import { haptics } from "./haptics";
import { Feather } from "./icons";
import { Spinner } from "./Spinner";
import { boundSwipeToMergeTranslation, shouldCommitSwipeToMerge, swipeToMergeDetent, swipeToMergeTravel } from "./swipe-to-merge-model";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, iconSize, radius, space, stateOpacity, type } from "./tokens";
import { useReducedMotion } from "./useReducedMotion";

/** The track's resting height; a label that wraps at a large text size grows it. */
const TRACK_HEIGHT = 56;
const INSET = 4;
const KNOB = TRACK_HEIGHT - INSET * 2;
/** Where the label starts and ends, clear of the knob at either end of the track. */
const KNOB_CLEARANCE = KNOB + INSET * 2 + space.xs;
const COMMIT_MS = 160;
const PIN_MS = 220;
const MERGED_FADE_MS = 700;
const SPRING = { damping: 20, stiffness: 240, mass: 0.7 };

/**
 * A slide-to-confirm control for merging a pull request. Carrying the knob to
 * the end of the track is the confirmation, so no alert follows it. Screen
 * reader users cannot drag, so the accessibility "activate" action calls
 * `onAccessibilityActivate`, which should confirm before merging.
 *
 * It draws in the theme's own status hues: a green tint for the track, solid
 * green flooding in behind the knob as it travels, and the theme's merged
 * purple once the pull request has landed.
 *
 * `onMerge` resolves true when the merge went through; the knob then stays at
 * the end. False springs it back. While `busy`, and once `merged`, the bar is
 * full: merged fades it to purple, reads `mergedLabel`, and locks it.
 */
export function SwipeToMerge({
	label,
	busyLabel,
	busy = false,
	disabled = false,
	merged = false,
	mergedLabel = "Merged",
	onMerge,
	onAccessibilityActivate,
}: {
	label: string;
	busyLabel: string;
	busy?: boolean;
	disabled?: boolean;
	merged?: boolean;
	mergedLabel?: string;
	onMerge: () => Promise<boolean>;
	onAccessibilityActivate: () => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const reduceMotion = useReducedMotion();
	const travel = useSharedValue(0);
	const translateX = useSharedValue(0);
	const startX = useSharedValue(0);
	const armed = useSharedValue(false);
	const detent = useSharedValue(0);
	const committed = useSharedValue(false);
	const locked = disabled || busy || merged;
	// A busy or merged bar is full however the merge started, the knob held at
	// the end: 0 → 1 as it gets there. Already full when the screen opens on a
	// merged pull request.
	const settled = busy || merged;
	const pinned = useSharedValue(settled ? 1 : 0);
	useEffect(() => {
		pinned.value = reduceMotion ? (settled ? 1 : 0) : withTiming(settled ? 1 : 0, { duration: PIN_MS, easing: Easing.out(Easing.cubic) });
	}, [settled, pinned, reduceMotion]);
	// 0 → 1 as the merged bar fades from green to purple. A pull request that was
	// already merged when the screen opened starts purple, with no fade.
	const mergedness = useSharedValue(merged ? 1 : 0);
	useEffect(() => {
		mergedness.value = merged ? (reduceMotion ? 1 : withTiming(1, { duration: MERGED_FADE_MS, easing: Easing.inOut(Easing.quad) })) : 0;
	}, [merged, mergedness, reduceMotion]);

	const settle = useCallback((merged: boolean) => {
		if (merged) return;
		haptics.error();
		committed.value = false;
		translateX.value = reduceMotion ? 0 : withSpring(0, SPRING);
	}, [committed, translateX, reduceMotion]);
	const commit = useCallback(() => {
		haptics.press();
		onMerge().then(settle, () => settle(false));
	}, [onMerge, settle]);
	// Haptics run on the JS thread; the gesture worklets schedule them by name.
	const grab = useCallback(() => haptics.tap(), []);
	const tick = useCallback(() => haptics.detent(), []);
	const arm = useCallback(() => haptics.armed(), []);

	const onLayout = useCallback((event: LayoutChangeEvent) => {
		travel.value = swipeToMergeTravel(event.nativeEvent.layout.width, KNOB, INSET);
	}, [travel]);

	const pan = Gesture.Pan()
		.enabled(!locked)
		.activeOffsetX([-10, 10])
		.failOffsetY([-12, 12])
		// Measured from where the finger went down, not from translationX: Android
		// reports translation from the point the pan activated, so a fast flick lost
		// its first jump and the knob stopped short of the end.
		.onBegin((event) => {
			startX.value = event.absoluteX;
			detent.value = 0;
			if (!committed.value) scheduleOnRN(grab);
		})
		.onUpdate((event) => {
			if (committed.value) return;
			translateX.value = boundSwipeToMergeTranslation(event.absoluteX - startX.value, travel.value);
			// A ratchet: a light tick per detent the knob passes, and a firmer bump
			// in place of that frame's tick when it crosses the point of no return.
			const nextArmed = shouldCommitSwipeToMerge(translateX.value, travel.value);
			const nextDetent = swipeToMergeDetent(translateX.value, travel.value);
			if (nextArmed !== armed.value) {
				armed.value = nextArmed;
				detent.value = nextDetent;
				scheduleOnRN(nextArmed ? arm : tick);
			} else if (nextDetent !== detent.value) {
				detent.value = nextDetent;
				scheduleOnRN(tick);
			}
		})
		.onEnd((event) => {
			if (committed.value) return;
			armed.value = false;
			// The finger can move between the last update and the lift.
			translateX.value = boundSwipeToMergeTranslation(event.absoluteX - startX.value, travel.value);
			if (shouldCommitSwipeToMerge(translateX.value, travel.value)) {
				committed.value = true;
				translateX.value = withTiming(travel.value, { duration: reduceMotion ? 0 : COMMIT_MS, easing: Easing.out(Easing.cubic) }, (finished) => {
					if (finished) scheduleOnRN(commit);
				});
				return;
			}
			translateX.value = reduceMotion ? 0 : withSpring(0, SPRING);
		})
		.onFinalize((_event, success) => {
			if (!success && !committed.value) translateX.value = reduceMotion ? 0 : withSpring(0, SPRING);
		});

	// Each style reads the shared values itself: a style that only reaches them
	// through a helper worklet is not subscribed to them and goes stale.
	const ratio = (x: number, max: number) => {
		"worklet";
		return max > 0 ? x / max : 0;
	};
	const knobStyle = useAnimatedStyle(() => ({
		transform: [{ translateX: translateX.value + (travel.value - translateX.value) * pinned.value }],
		backgroundColor: interpolateColor(mergedness.value, [0, 1], [t.green, t.purple]),
	}));
	// The fill trails the knob: it reaches from the track's left edge to the
	// knob's right edge, so only the ground the knob has covered turns green.
	const fillStyle = useAnimatedStyle(() => {
		const x = translateX.value + (travel.value - translateX.value) * pinned.value;
		return {
			width: x + KNOB + INSET * 2,
			opacity: Math.max(pinned.value, interpolate(ratio(translateX.value, travel.value), [0, 0.04], [0, 1], "clamp")),
			backgroundColor: interpolateColor(mergedness.value, [0, 1], [t.green, t.purple]),
		};
	});
	const labelStyle = useAnimatedStyle(() => ({
		opacity: Math.max(pinned.value, interpolate(ratio(translateX.value, travel.value), [0, 0.6], [1, 0.45], "clamp")),
	}));

	// Ink on the solid fill is the success button's, which reads on every theme's green.
	const ink = t.bgBase;
	return (
		// One accessible element: screen readers announce the merge as a button.
		<View
			accessible
			accessibilityRole="button"
			accessibilityLabel={merged ? mergedLabel : busy ? busyLabel : label}
			accessibilityHint={merged ? undefined : "Swipe right to merge"}
			accessibilityState={{ busy, disabled: locked }}
			accessibilityActions={locked ? [] : [{ name: "activate", label }]}
			onAccessibilityAction={(event) => { if (event.nativeEvent.actionName === "activate") onAccessibilityActivate(); }}
			style={disabled && !settled ? styles.disabled : undefined}
		>
			<View onLayout={onLayout} style={styles.track}>
				<Animated.View pointerEvents="none" style={[styles.fill, fillStyle]} />
				{/* In the flow rather than laid over the track, so a label that wraps at a
				    large text size makes the track taller instead of being cut off. */}
				<Animated.View pointerEvents="none" style={[styles.labelRow, settled ? styles.labelSettled : styles.labelResting, labelStyle]}>
					<Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={2} style={[styles.label, { color: settled ? ink : t.green }]}>
						{merged ? mergedLabel : busy ? busyLabel : label}
					</Text>
					{settled ? null : <AppSymbol name="chevronForward" size={iconSize.sm} color={t.green} />}
				</Animated.View>
				<GestureDetector gesture={pan}>
					<Animated.View hitSlop={12} style={[styles.knob, knobStyle]}>
						{busy && !merged ? <Spinner color={ink} /> : <Feather name="git-merge" size={iconSize.lg} color={ink} />}
					</Animated.View>
				</GestureDetector>
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		track: { minHeight: TRACK_HEIGHT, borderRadius: radius.pill, borderCurve: "continuous", justifyContent: "center", backgroundColor: t.tintGreen },
		disabled: { opacity: stateOpacity.disabled },
		fill: { position: "absolute", left: 0, top: 0, bottom: 0, borderRadius: radius.pill, borderCurve: "continuous" },
		labelRow: { flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.xs, paddingVertical: space.sm },
		labelResting: { paddingLeft: KNOB_CLEARANCE, paddingRight: space.lg },
		labelSettled: { paddingLeft: space.lg, paddingRight: KNOB_CLEARANCE },
		label: { flexShrink: 1, textAlign: "center", fontFamily: "Geist_600SemiBold", fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
		// Centred on the track's height by its `justifyContent`, however tall a
		// wrapped label makes it.
		knob: { position: "absolute", left: INSET, width: KNOB, height: KNOB, borderRadius: KNOB / 2, alignItems: "center", justifyContent: "center" },
	});
