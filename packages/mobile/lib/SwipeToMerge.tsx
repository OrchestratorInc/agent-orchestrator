import { useCallback, useEffect } from "react";
import { ActivityIndicator, StyleSheet, Text, View, type LayoutChangeEvent } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, {
	cancelAnimation,
	Easing,
	interpolate,
	interpolateColor,
	useAnimatedStyle,
	useSharedValue,
	withRepeat,
	withSequence,
	withSpring,
	withTiming,
} from "react-native-reanimated";
import { scheduleOnRN } from "react-native-worklets";

import { haptics } from "./haptics";
import { Feather } from "./icons";
import { boundSwipeToMergeTranslation, shouldCommitSwipeToMerge, swipeToMergeDetent, swipeToMergeTravel } from "./swipe-to-merge-model";
import { useThemeState } from "./ThemeProvider";
import { type, space } from "./tokens";
import { useReducedMotion } from "./useReducedMotion";

const TRACK_HEIGHT = 56;
const INSET = 4;
const KNOB = TRACK_HEIGHT - INSET * 2;
const COMMIT_MS = 160;
const MERGED_FADE_MS = 700;
const SPRING = { damping: 20, stiffness: 240, mass: 0.7 };

/**
 * Light glass in GitHub's merge green (Primer's #2ea043 / #3fb950, and #1f883d
 * on light): a faint green wash with a white sheen down from the top edge, and
 * a green border that brightens as the knob travels. Dragging floods solid
 * merge green in behind the knob, only as far as the knob has gone, and the
 * knob itself turns solid with a white icon. Once merged, the full green bar
 * fades to GitHub's merged purple (#8957e5, #8250df on light). Plain React
 * Native gradient styles, so iOS and Android draw it the same.
 */
const GITHUB_MERGE = {
	dark: {
		track: "rgba(46,160,67,0.07)", knob: "rgba(46,160,67,0.14)",
		sheen: "linear-gradient(180deg, rgba(255,255,255,0.06) 0%, rgba(255,255,255,0) 60%)",
		knobSheen: "linear-gradient(180deg, rgba(255,255,255,0.12) 0%, rgba(255,255,255,0) 60%)",
		fill: "linear-gradient(90deg, #2ea043 0%, #238636 100%)", fillBase: "#238636",
		rim: "rgba(46,160,67,0.35)", rimArmed: "rgba(63,185,80,0.95)", knobRim: "rgba(63,185,80,0.7)", icon: "#3fb950", ink: "#ffffff", label: "rgba(255,255,255,0.88)",
		merged: "linear-gradient(90deg, #a371f7 0%, #8957e5 100%)", mergedBase: "#8957e5", mergedRim: "rgba(163,113,247,0.95)",
	},
	light: {
		track: "rgba(31,136,61,0.05)", knob: "rgba(31,136,61,0.1)",
		sheen: "linear-gradient(180deg, rgba(255,255,255,0.5) 0%, rgba(255,255,255,0) 60%)",
		knobSheen: "linear-gradient(180deg, rgba(255,255,255,0.6) 0%, rgba(255,255,255,0) 60%)",
		fill: "linear-gradient(90deg, #2da44e 0%, #1f883d 100%)", fillBase: "#1f883d",
		rim: "rgba(31,136,61,0.3)", rimArmed: "rgba(31,136,61,0.9)", knobRim: "rgba(31,136,61,0.65)", icon: "#1a7f37", ink: "#1a7f37", label: "#1a7f37",
		merged: "linear-gradient(90deg, #8957e5 0%, #8250df 100%)", mergedBase: "#8250df", mergedRim: "rgba(130,80,223,0.9)",
	},
} as const;

/**
 * A slide-to-confirm control for merging a pull request. Carrying the knob to
 * the end of the track is the confirmation, so no alert follows it. Screen
 * reader users cannot drag, so the accessibility "activate" action calls
 * `onAccessibilityActivate`, which should confirm before merging.
 *
 * `onMerge` resolves true when the merge went through; the knob then stays at
 * the end. False springs it back. Pass `merged` once the pull request is
 * merged: the bar stays full, fades to purple, reads `mergedLabel`, and locks.
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
	const { scheme } = useThemeState();
	const gh = GITHUB_MERGE[scheme === "light" ? "light" : "dark"];
	const reduceMotion = useReducedMotion();
	const travel = useSharedValue(0);
	const translateX = useSharedValue(0);
	const startX = useSharedValue(0);
	const armed = useSharedValue(false);
	const detent = useSharedValue(0);
	const committed = useSharedValue(false);
	const shimmer = useSharedValue(0);
	const locked = disabled || busy || merged;
	// 0 → 1 as the merged bar fades from green to purple. A pull request that was
	// already merged when the screen opened starts purple, with no fade.
	const mergedness = useSharedValue(merged ? 1 : 0);
	useEffect(() => {
		mergedness.value = merged ? (reduceMotion ? 1 : withTiming(1, { duration: MERGED_FADE_MS, easing: Easing.inOut(Easing.quad) })) : 0;
	}, [merged, mergedness, reduceMotion]);

	useEffect(() => {
		if (reduceMotion || locked) {
			cancelAnimation(shimmer);
			shimmer.value = 0;
			return;
		}
		shimmer.value = withRepeat(withSequence(
			withTiming(1, { duration: 900, easing: Easing.inOut(Easing.quad) }),
			withTiming(0, { duration: 900, easing: Easing.inOut(Easing.quad) }),
		), -1);
		return () => cancelAnimation(shimmer);
	}, [locked, reduceMotion, shimmer]);

	const settle = useCallback((merged: boolean) => {
		if (merged) return;
		haptics.error();
		committed.value = false;
		translateX.value = withSpring(0, SPRING);
	}, [committed, translateX]);
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
			if (!success && !committed.value) translateX.value = withSpring(0, SPRING);
		});

	// Each style reads the shared values itself: a style that only reaches them
	// through a helper worklet is not subscribed to them and goes stale.
	const ratio = (x: number, max: number) => {
		"worklet";
		return max > 0 ? x / max : 0;
	};
	// A merged bar holds the knob at the end, however it got there.
	const knobStyle = useAnimatedStyle(() => ({
		transform: [{ translateX: mergedness.value > 0 ? travel.value : translateX.value }],
		borderColor: interpolateColor(mergedness.value, [0, 1], [gh.knobRim, gh.mergedRim]),
	}));
	const trackStyle = useAnimatedStyle(() => ({
		borderColor: interpolateColor(
			mergedness.value,
			[0, 1],
			[interpolateColor(ratio(mergedness.value > 0 ? travel.value : translateX.value, travel.value), [0, 1], [gh.rim, gh.rimArmed]), gh.mergedRim],
		),
	}));
	const purpleStyle = useAnimatedStyle(() => ({ opacity: mergedness.value }));
	// The fill trails the knob: it reaches from the track's left edge to the
	// knob's right edge, so only the ground the knob has covered turns green.
	const fillStyle = useAnimatedStyle(() => ({
		width: (mergedness.value > 0 ? travel.value : translateX.value) + KNOB + INSET * 2 - 1,
		opacity: mergedness.value > 0 ? 1 : interpolate(ratio(translateX.value, travel.value), [0, 0.04], [0, 1], "clamp"),
	}));
	// The same switch on the knob: glass at rest, solid with a white icon once moving.
	// Its own background fades, not a layer over it: an overlay sits inside the
	// translucent rim, which then showed a green ring on the purple bar.
	const solidStyle = useAnimatedStyle(() => ({
		opacity: mergedness.value > 0 ? 1 : interpolate(ratio(translateX.value, travel.value), [0, 0.04], [0, 1], "clamp"),
		backgroundColor: interpolateColor(mergedness.value, [0, 1], [gh.fillBase, gh.mergedBase]),
	}));
	const labelStyle = useAnimatedStyle(() => ({
		opacity: interpolate(ratio(translateX.value, travel.value), [0, 0.6], [1, 0.45], "clamp"),
	}));
	const chevronStyle = useAnimatedStyle(() => ({
		opacity: interpolate(shimmer.value, [0, 1], [0.35, 1]),
		transform: [{ translateX: interpolate(shimmer.value, [0, 1], [0, 4]) }],
	}));
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
			style={disabled && !busy && !merged ? styles.disabled : undefined}
		>
			<Animated.View onLayout={onLayout} style={[styles.track, { backgroundColor: gh.track, experimental_backgroundImage: gh.sheen }, trackStyle]}>
				{merged ? null : <Animated.View pointerEvents="none" style={[styles.labelRow, labelStyle]}>
					<Text style={[styles.label, { color: gh.label }]} numberOfLines={1}>{label}</Text>
					<Animated.View style={[styles.chevrons, chevronStyle]}>
						<Feather name="chevron-right" size={15} color={gh.label} />
						<Feather name="chevron-right" size={15} color={gh.label} style={styles.chevronTail} />
					</Animated.View>
				</Animated.View>}
				<Animated.View pointerEvents="none" style={[styles.fill, { backgroundColor: gh.fillBase, experimental_backgroundImage: gh.fill }, fillStyle]}>
					<Animated.View style={[styles.fillPurple, { backgroundColor: gh.mergedBase, experimental_backgroundImage: gh.merged }, purpleStyle]} />
				</Animated.View>
				{busy || merged ? <View pointerEvents="none" style={styles.busyRow}><Text style={[styles.label, { color: "#ffffff" }]} numberOfLines={1}>{merged ? mergedLabel : busyLabel}</Text></View> : null}
				<GestureDetector gesture={pan}>
					<Animated.View hitSlop={12} style={[styles.knob, { backgroundColor: gh.knob, experimental_backgroundImage: gh.knobSheen }, knobStyle]}>
						<Feather name="git-merge" size={20} color={gh.icon} />
						<Animated.View pointerEvents="none" style={[styles.knobSolid, solidStyle]}>
							{busy && !merged ? <ActivityIndicator size="small" color="#ffffff" /> : <Feather name="git-merge" size={20} color="#ffffff" />}
						</Animated.View>
					</Animated.View>
				</GestureDetector>
			</Animated.View>
		</View>
	);
}

const styles = StyleSheet.create({
	track: { height: TRACK_HEIGHT, borderRadius: TRACK_HEIGHT / 2, borderWidth: 1, justifyContent: "center" },
	disabled: { opacity: 0.45 },
	labelRow: { position: "absolute", top: 0, right: 0, bottom: 0, left: 0, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.xs, paddingLeft: KNOB },
	fill: { position: "absolute", left: 0, top: 0, bottom: 0, borderRadius: TRACK_HEIGHT / 2, overflow: "hidden" },
	fillPurple: { position: "absolute", top: 0, right: 0, bottom: 0, left: 0 },
	knobSolid: { position: "absolute", top: -1, right: -1, bottom: -1, left: -1, borderRadius: KNOB / 2, borderWidth: 1, borderColor: "rgba(255,255,255,0.35)", alignItems: "center", justifyContent: "center", boxShadow: "0px 2px 6px 0px rgba(0,0,0,0.25)" },
	busyRow: { position: "absolute", top: 0, right: 0, bottom: 0, left: 0, alignItems: "center", justifyContent: "center", paddingRight: KNOB },
	label: { fontFamily: "Geist_600SemiBold", fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
	chevrons: { flexDirection: "row", alignItems: "center" },
	chevronTail: { marginLeft: -9 },
	knob: { position: "absolute", left: INSET - 1, borderWidth: 1, width: KNOB, height: KNOB, borderRadius: KNOB / 2, alignItems: "center", justifyContent: "center" },
});
